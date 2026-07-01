package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		status int
		want   Kind
	}{
		{200, KindUnknown},
		{301, KindUnknown},
		{400, KindPermanent},
		{401, KindPermanent},
		{402, KindPermanent}, // quota/billing
		{403, KindPermanent},
		{404, KindPermanent},
		{408, KindTransient},
		{422, KindPermanent},
		{429, KindTransient},
		{500, KindTransient},
		{502, KindTransient},
		{503, KindTransient},
		{504, KindTransient},
		{599, KindTransient},
	}
	for _, c := range cases {
		if got := ClassifyHTTPStatus(c.status); got != c.want {
			t.Errorf("ClassifyHTTPStatus(%d) = %v, want %v", c.status, got, c.want)
		}
	}
}

func TestClassifyNetwork(t *testing.T) {
	if got := ClassifyNetwork(nil); got != KindUnknown {
		t.Errorf("nil err: got %v want unknown", got)
	}
	if got := ClassifyNetwork(context.DeadlineExceeded); got != KindTransient {
		t.Errorf("deadline: got %v want transient", got)
	}
	if got := ClassifyNetwork(context.Canceled); got != KindPermanent {
		t.Errorf("canceled: got %v want permanent (caller bailed)", got)
	}
	// url.Error wrapping a timeout-like net.Error
	timeoutErr := &timeoutNetError{}
	wrapped := &url.Error{Op: "Get", URL: "x", Err: timeoutErr}
	if got := ClassifyNetwork(wrapped); got != KindTransient {
		t.Errorf("url.Error timeout: got %v want transient", got)
	}
	// Other net errors default transient.
	dnsErr := &net.OpError{Op: "dial", Err: errors.New("no such host")}
	if got := ClassifyNetwork(dnsErr); got != KindTransient {
		t.Errorf("dns err: got %v want transient", got)
	}
}

func TestIsTransientWithWrappedError(t *testing.T) {
	inner := Transient("youcom", http.StatusServiceUnavailable, fmt.Errorf("upstream"))
	wrapped := fmt.Errorf("frugal__search: %w", inner)
	if !IsTransient(wrapped) {
		t.Errorf("IsTransient should walk the error chain")
	}
	if IsPermanent(wrapped) {
		t.Errorf("IsPermanent should be false for a wrapped transient")
	}
}

func TestIsTransientKinds(t *testing.T) {
	cases := []struct {
		kind Kind
		want bool
	}{
		{KindUnknown, true}, // documented contract: unknown treated as transient
		{KindTransient, true},
		{KindPermanent, false},
		{KindFatal, false},
	}
	for _, c := range cases {
		err := &Error{Provider: "youcom", Kind: c.kind, Err: errors.New("x")}
		if got := IsTransient(err); got != c.want {
			t.Errorf("IsTransient(kind=%v) = %v, want %v", c.kind, got, c.want)
		}
		if got := IsPermanent(err); got == c.want {
			t.Errorf("IsPermanent(kind=%v) = %v, want %v", c.kind, got, !c.want)
		}
	}
}

func TestErrorFormatting(t *testing.T) {
	e := Transient("youcom", 503, errors.New("upstream blip"))
	want := "youcom: transient: status 503: upstream blip"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	noStatus := Permanent("youcom", 0, errors.New("empty query"))
	want2 := "youcom: permanent: empty query"
	if got := noStatus.Error(); got != want2 {
		t.Errorf("Error() = %q, want %q", got, want2)
	}
}

// timeoutNetError is a minimal net.Error stub whose Timeout() returns true.
type timeoutNetError struct{}

func (*timeoutNetError) Error() string   { return "i/o timeout" }
func (*timeoutNetError) Timeout() bool   { return true }
func (*timeoutNetError) Temporary() bool { return true }
