// Package routing holds capability-agnostic primitives shared by every
// Frugal tool that fans out to multiple providers: the typed error +
// Kind classification, retry loop, and per-attempt observability hook.
//
// Search / extract / browse capabilities each have their own
// per-call interface and CallWithFallback in `internal/<capability>/`,
// but they reuse this package's Error and DoWithRetry so error
// classification stays consistent across the binary.
package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Kind classifies a provider error for routing decisions. Transient
// errors get a retry inside the driver and a fallback to the next
// provider at the router. Permanent errors skip the in-driver retry —
// retrying a 401 doesn't help — but still fall back at the router:
// auth, quota, and endpoint failures are provider-scoped verdicts, and
// the next provider may well succeed. Fatal errors stop both: the
// request itself can't succeed anywhere, so walking the rest of the
// chain only spends quota rediscovering the same answer.
type Kind int

const (
	// KindUnknown is the zero value. Errors with unknown kind are treated
	// as transient by IsTransient — safer default for "we couldn't tell."
	KindUnknown Kind = iota
	// KindTransient: retryable. Network failures, 408/429/5xx, context
	// deadline exceeded.
	KindTransient
	// KindPermanent: don't retry inside the driver. Auth failures
	// (401/403), quota/billing problems (402), bad request (400/422).
	// The router still falls through to the next provider — these are
	// facts about this provider, not about the request.
	KindPermanent
	// KindFatal: don't retry, don't fall back. The request itself cannot
	// succeed with any provider — e.g. the target URL is gone (404/410
	// from the target site) or unparseable. Falling back would spend the
	// next provider's quota rediscovering the same dead end.
	KindFatal
)

// String renders Kind for logs.
func (k Kind) String() string {
	switch k {
	case KindTransient:
		return "transient"
	case KindPermanent:
		return "permanent"
	case KindFatal:
		return "fatal"
	}
	return "unknown"
}

// Error is a typed provider error. Drivers wrap their HTTP/network/decode
// failures in one of these so the router can read .Kind without parsing
// strings. Use Transient(...) / Permanent(...) constructors.
type Error struct {
	Provider string // "searxng", "serper", "youcom", …
	Kind     Kind
	Status   int   // HTTP status code, or 0 if not an HTTP error
	Err      error // underlying cause
}

// Error implements error. Format is intentionally compact:
//
//	youcom: transient: status 503: upstream timeout
func (e *Error) Error() string {
	switch {
	case e.Status != 0:
		return fmt.Sprintf("%s: %s: status %d: %v", e.Provider, e.Kind, e.Status, e.Err)
	default:
		return fmt.Sprintf("%s: %s: %v", e.Provider, e.Kind, e.Err)
	}
}

// Unwrap exposes the underlying error so errors.Is / errors.As keep
// working — callers can still check for context.Canceled, etc.
func (e *Error) Unwrap() error { return e.Err }

// Transient constructs a transient *Error. provider is the driver name;
// status is the HTTP code (0 if none); cause is the underlying error.
func Transient(provider string, status int, cause error) *Error {
	return &Error{Provider: provider, Kind: KindTransient, Status: status, Err: cause}
}

// Permanent constructs a permanent *Error.
func Permanent(provider string, status int, cause error) *Error {
	return &Error{Provider: provider, Kind: KindPermanent, Status: status, Err: cause}
}

// Fatal constructs a fatal *Error — the request can't succeed with any
// provider, so the router stops the fallback chain.
func Fatal(provider string, status int, cause error) *Error {
	return &Error{Provider: provider, Kind: KindFatal, Status: status, Err: cause}
}

// ClassifyHTTPStatus maps an HTTP status code to a Kind. Used by drivers
// after a non-200 response. Treats 408/429 and any 5xx as transient.
// 401/403/402/422 and any other 4xx as permanent.
func ClassifyHTTPStatus(status int) Kind {
	switch {
	case status == http.StatusRequestTimeout: // 408
		return KindTransient
	case status == http.StatusTooManyRequests: // 429
		return KindTransient
	case status >= 500 && status <= 599:
		return KindTransient
	case status >= 400 && status <= 499:
		return KindPermanent
	}
	return KindUnknown
}

// ClassifyNetwork inspects a Go error returned by an HTTP roundtrip and
// returns the Kind. Treats timeouts, connection-refused, DNS failures,
// and most net.OpError variants as transient. context.Canceled is treated
// as transient (the request never completed, but the user's caller is
// the one that pulled the plug — surface it but don't crash retry logic).
func ClassifyNetwork(err error) Kind {
	if err == nil {
		return KindUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTransient
	}
	if errors.Is(err, context.Canceled) {
		// Caller went away. Classified permanent so the in-driver retry
		// loop stops; the routers' own ctx-done check is what stops the
		// fallback chain.
		return KindPermanent
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return KindTransient
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTransient
	}
	// Default for unrecognized network errors: transient. Almost all
	// "couldn't reach the server" cases benefit from a retry / fallback.
	return KindTransient
}

// IsTransient reports whether err (which may wrap an *Error) is in the
// transient class. Non-*Error errors are treated as transient — safer
// default for "we didn't classify, try the next provider anyway."
func IsTransient(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == KindTransient
	}
	return err != nil
}

// IsPermanent is the negation of IsTransient for clarity at call sites.
// Fatal errors are permanent in this sense — DoWithRetry gates on it, and
// fatal must not retry either.
func IsPermanent(err error) bool { return err != nil && !IsTransient(err) }

// IsFatal reports whether err (which may wrap an *Error) is fatal — no
// retry, no fallback.
func IsFatal(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == KindFatal
	}
	return false
}

// JoinAttempts collapses the per-provider errors from one fallback walk
// into a single error. Is/As/IsTransient classify against the LAST
// attempt (the error the caller would previously have seen), while the
// message also names every earlier provider that failed — so an agent
// staring at a timeout from the expensive provider still sees the cheap
// provider's 401 that actually needs fixing.
func JoinAttempts(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	parts := make([]string, 0, len(errs)-1)
	for _, e := range errs[:len(errs)-1] {
		parts = append(parts, e.Error())
	}
	return fmt.Errorf("%w (earlier attempts: %s)", errs[len(errs)-1], strings.Join(parts, "; "))
}
