// Package limit caps the size of tool results before they reach the
// agent's context window, and prices what does reach it in estimated
// tokens.
//
// Frugal prices every call in dollars. The other cost of a tool call is
// the tokens its result burns in the model on every later turn of the
// session, and that one has no receipt anywhere in the stack. Clients
// enforce their own ceilings blind (Claude Code refuses MCP results over
// 25,000 tokens by default and spills anything past 50,000 characters to
// a file with a 2 KB head preview), so a long page either fails the
// call or arrives cut mid-sentence with nothing telling the model that
// the tail is missing.
//
// This package moves that decision server-side and makes it explicit: a
// caller-chosen or operator-configured character budget is applied
// across the content fields of a result, the cut lands on a whitespace
// boundary when one is close, a visible marker is appended to the field
// that was cut, and a Report says exactly how much was kept. The MCP
// tools surface the Report as chars_returned, chars_total, truncated,
// and est_tokens so the agent can decide to re-call with a bigger
// budget instead of guessing at what it did not see.
package limit

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// CharsPerToken is the rule-of-thumb ratio used for EstTokens. Real
// tokenizers vary by model and by content (prose runs near 4 characters
// per token in English, dense HTML and code run lower), so this is a
// planning figure, not a bill. It matches the heuristic the major
// clients use for their own output warnings.
const CharsPerToken = 4

// backoffWindow is how far back from the hard cut Cap will look for a
// whitespace boundary before giving up and cutting mid-word. Large
// enough to reach the previous line break in ordinary prose, small
// enough that the caller still gets nearly the whole budget.
const backoffWindow = 256

// Report describes what Cap did to one result.
type Report struct {
	// Truncated is true when at least one field was shortened.
	Truncated bool
	// CharsTotal is the content size before capping, in characters
	// (Unicode code points, not bytes).
	CharsTotal int
	// CharsReturned is the content size after capping, in characters,
	// not counting the truncation marker.
	CharsReturned int
}

// EstTokens converts a character count into an estimated token count,
// rounding up so a one-character result still costs one token.
func EstTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + CharsPerToken - 1) / CharsPerToken
}

// Count sums the character lengths of the given strings.
func Count(fields ...string) int {
	total := 0
	for _, f := range fields {
		total += utf8.RuneCountInString(f)
	}
	return total
}

// Cap applies a total character budget across fields, in the order
// given. Fields are consumed until the budget runs out; the field that
// crosses the line is shortened to fit and every later field is
// emptied. A maxChars of zero or less means no limit: the fields are
// left untouched and the Report simply measures them.
//
// The order matters and callers should pass the most useful field
// first. frugal__extract passes markdown, then text, then html, so the
// human-readable rendering survives and the bulky raw HTML is the first
// thing to go.
//
// The cut is rune-safe (never splits a multi-byte character) and backs
// off to the nearest preceding whitespace when one sits within
// backoffWindow characters, so the agent sees a clean word boundary
// rather than half a token. A marker line naming the kept and total
// sizes is appended to the shortened field; the marker is not counted
// in CharsReturned.
func Cap(maxChars int, fields ...*string) Report {
	var rep Report
	for _, f := range fields {
		if f != nil {
			rep.CharsTotal += utf8.RuneCountInString(*f)
		}
	}
	if maxChars <= 0 {
		rep.CharsReturned = rep.CharsTotal
		return rep
	}

	remaining := maxChars
	for _, f := range fields {
		if f == nil {
			continue
		}
		n := utf8.RuneCountInString(*f)
		switch {
		case n == 0:
			continue
		case remaining <= 0:
			// Budget already spent by an earlier field: drop this one
			// entirely. The marker on the field that crossed the line
			// carries the total, so the agent knows more existed.
			*f = ""
			rep.Truncated = true
		case n <= remaining:
			remaining -= n
			rep.CharsReturned += n
		default:
			kept := cutAt(*f, remaining)
			keptN := utf8.RuneCountInString(kept)
			rep.CharsReturned += keptN
			remaining = 0
			rep.Truncated = true
			*f = kept + marker(keptN, rep.CharsTotal)
		}
	}
	return rep
}

// cutAt returns the first n runes of s, backed off to the nearest
// preceding whitespace when one falls within backoffWindow runes of the
// cut. Trailing whitespace is trimmed so the marker sits flush.
func cutAt(s string, n int) string {
	if n <= 0 {
		return ""
	}
	// Find the byte offset of the n-th rune.
	byteEnd := len(s)
	count := 0
	for i := range s {
		if count == n {
			byteEnd = i
			break
		}
		count++
	}
	if byteEnd >= len(s) {
		return s
	}
	hard := s[:byteEnd]

	// Back off to a whitespace boundary if one is close enough.
	window := 0
	for i := len(hard); i > 0 && window < backoffWindow; {
		r, size := utf8.DecodeLastRuneInString(hard[:i])
		if unicode.IsSpace(r) {
			return trimRightSpace(hard[:i])
		}
		i -= size
		window++
	}
	return trimRightSpace(hard)
}

func trimRightSpace(s string) string {
	end := len(s)
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	return s[:end]
}

// marker is the visible note appended to a shortened field. It is
// phrased for the model that reads it: what happened, how much is
// missing, and the one argument that changes it. Structured output
// carries the same numbers as fields, but clients that flatten results
// to text would otherwise lose the signal entirely.
func marker(kept, total int) string {
	return fmt.Sprintf("\n\n[frugal: output truncated to %d of %d chars; pass a larger max_chars to see more]", kept, total)
}
