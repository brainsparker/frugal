package tools

import (
	"time"

	"github.com/frugalsh/frugal/internal/routing"
)

// pinUnderCeiling checks a caller-pinned provider against the caller's
// own max_cost_usd. A pin that names a provider priced above the ceiling
// is a contradiction in the request, so it is refused before any spend,
// exactly like a pin on a denied or over-budget provider. An unknown
// provider name passes here: CallPinned owns that error and its message
// lists the configured names, which is the more useful failure.
func pinUnderCeiling[T routing.Costed](configured []T, provider string, c routing.Ceiling) (bool, string) {
	if !c.Set {
		return true, ""
	}
	now := time.Now()
	for _, it := range configured {
		if it.Name() == provider {
			return c.Allows(it, now)
		}
	}
	return true, ""
}
