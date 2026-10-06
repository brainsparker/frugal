package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/frugalsh/frugal/internal/routing"
)

// fingerprint hashes a tool's normalized input into the key the guard's
// loop rail counts on. Callers pass the input after the same
// normalization the handler applies (trimmed query, lower-cased
// provider, validated freshness), so "Go routing" and "Go routing " are
// one call, and so a pin spelled "Serper" matches "serper". The hash
// means the guard never holds query text, only a digest of it.
//
// The input is marshalled as JSON: struct field order is fixed, so the
// encoding is deterministic for a given value. A marshal failure
// (impossible for the plain structs the tools use, but the compiler
// doesn't know that) falls back to %#v, which is also deterministic.
func fingerprint(tool string, in any) string {
	b, err := json.Marshal(in)
	if err != nil {
		b = []byte(fmt.Sprintf("%#v", in))
	}
	sum := sha256.Sum256(append([]byte(tool+"\x00"), b...))
	return hex.EncodeToString(sum[:16])
}

// admitCall runs a tool call past the guard's loop rail before any
// routing happens. It returns a ready-to-return error when the call is
// the one too many for its fingerprint inside the window, and logs the
// refusal at Warn so the trip is visible in the server log next to the
// budget skips. A nil guard, or one with the rail off, always admits.
func admitCall(g *routing.Guard, tool string, in any, logger *slog.Logger) error {
	if g == nil {
		return nil
	}
	ok, why := g.Admit(tool, fingerprint(tool, in))
	if ok {
		return nil
	}
	if logger != nil {
		logger.Warn("routing loop guard: call refused", "tool", tool, "reason", why)
	}
	return fmt.Errorf("%s: %s", tool, why)
}
