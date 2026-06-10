package routing

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFatal_Classification(t *testing.T) {
	err := Fatal("goreadability", 404, errors.New("http 404"))
	if !IsFatal(err) {
		t.Errorf("Fatal error must report IsFatal")
	}
	// Fatal must also read as permanent so DoWithRetry skips retries.
	if !IsPermanent(err) {
		t.Errorf("Fatal error must report IsPermanent (retry gate)")
	}
	if IsTransient(err) {
		t.Errorf("Fatal error must not report IsTransient")
	}
	if !strings.Contains(err.Error(), "fatal") {
		t.Errorf("Error() should render the fatal kind; got %q", err.Error())
	}
}

func TestIsFatal_WrappedAndNonError(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", Fatal("p", 410, errors.New("gone")))
	if !IsFatal(wrapped) {
		t.Errorf("IsFatal must see through wrapping")
	}
	if IsFatal(errors.New("plain")) {
		t.Errorf("plain errors are not fatal")
	}
	if IsFatal(nil) {
		t.Errorf("nil is not fatal")
	}
}

func TestJoinAttempts(t *testing.T) {
	if JoinAttempts(nil) != nil {
		t.Errorf("no attempts → nil error")
	}

	single := Transient("a", 503, errors.New("blip"))
	if got := JoinAttempts([]error{single}); got != single {
		t.Errorf("single attempt should pass through unchanged; got %v", got)
	}

	first := Permanent("free", 401, errors.New("invalid api key"))
	last := Transient("cheap", 503, errors.New("upstream down"))
	joined := JoinAttempts([]error{first, last})

	// Classification follows the LAST attempt…
	if !IsTransient(joined) {
		t.Errorf("joined error should classify as the last attempt (transient); got %v", joined)
	}
	var typed *Error
	if !errors.As(joined, &typed) || typed.Provider != "cheap" {
		t.Errorf("errors.As should surface the last attempt's error; got %v", typed)
	}
	// …while the message names every failed provider.
	for _, want := range []string{"free", "invalid api key", "cheap", "upstream down"} {
		if !strings.Contains(joined.Error(), want) {
			t.Errorf("joined message should mention %q; got %q", want, joined.Error())
		}
	}
}
