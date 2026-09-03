package limit

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEstTokens_RoundsUp(t *testing.T) {
	cases := map[int]int{0: 0, -5: 0, 1: 1, 4: 1, 5: 2, 400: 100, 401: 101}
	for chars, want := range cases {
		if got := EstTokens(chars); got != want {
			t.Errorf("EstTokens(%d) = %d, want %d", chars, got, want)
		}
	}
}

func TestCount_UsesRunesNotBytes(t *testing.T) {
	if got := Count("héllo", "日本"); got != 7 {
		t.Errorf("Count = %d, want 7 runes", got)
	}
}

func TestCap_ZeroMeansUnlimited(t *testing.T) {
	md := strings.Repeat("a", 10_000)
	html := strings.Repeat("b", 20_000)
	rep := Cap(0, &md, &html)
	if rep.Truncated {
		t.Fatal("zero budget must not truncate")
	}
	if rep.CharsTotal != 30_000 || rep.CharsReturned != 30_000 {
		t.Errorf("report = %+v, want 30000/30000", rep)
	}
	if len(md) != 10_000 || len(html) != 20_000 {
		t.Error("fields must be untouched when unlimited")
	}
}

func TestCap_UnderBudgetIsANoOp(t *testing.T) {
	md := "short body"
	rep := Cap(1000, &md)
	if rep.Truncated || md != "short body" {
		t.Errorf("under-budget field changed: %q %+v", md, rep)
	}
	if rep.CharsTotal != 10 || rep.CharsReturned != 10 {
		t.Errorf("report = %+v", rep)
	}
}

func TestCap_BudgetIsSharedAcrossFieldsInOrder(t *testing.T) {
	md := strings.Repeat("m", 600)
	text := strings.Repeat("t", 600)
	html := strings.Repeat("h", 600)
	rep := Cap(1000, &md, &text, &html)
	if !rep.Truncated {
		t.Fatal("expected truncation")
	}
	if rep.CharsTotal != 1800 {
		t.Errorf("CharsTotal = %d, want 1800", rep.CharsTotal)
	}
	// markdown fits whole (600), text is cut to the remaining 400,
	// html is dropped.
	if utf8.RuneCountInString(md) != 600 {
		t.Errorf("markdown should be intact, got %d runes", utf8.RuneCountInString(md))
	}
	if !strings.HasPrefix(text, strings.Repeat("t", 400)) || strings.HasPrefix(text, strings.Repeat("t", 401)) {
		t.Errorf("text should keep exactly 400 chars before the marker; got %d runes", utf8.RuneCountInString(text))
	}
	if !strings.Contains(text, "[frugal: output truncated to 400 of 1800 chars") {
		t.Errorf("marker missing or wrong: %q", text[400:])
	}
	if html != "" {
		t.Errorf("html should be dropped once the budget is spent, got %d runes", utf8.RuneCountInString(html))
	}
	if rep.CharsReturned != 1000 {
		t.Errorf("CharsReturned = %d, want 1000 (marker excluded)", rep.CharsReturned)
	}
}

func TestCap_BacksOffToWhitespace(t *testing.T) {
	body := "alpha beta gamma delta epsilon zeta eta theta"
	// A budget of 28 lands inside "epsilon" ("alpha beta gamma delta epsi").
	rep := Cap(28, &body)
	if !rep.Truncated {
		t.Fatal("expected truncation")
	}
	kept := strings.SplitN(body, "\n\n[frugal:", 2)[0]
	if kept != "alpha beta gamma delta" {
		t.Errorf("expected a clean word boundary, got %q", kept)
	}
	if rep.CharsReturned != len("alpha beta gamma delta") {
		t.Errorf("CharsReturned = %d", rep.CharsReturned)
	}
}

func TestCap_HardCutsWhenNoWhitespaceNearby(t *testing.T) {
	body := strings.Repeat("x", 5000)
	rep := Cap(1000, &body)
	kept := strings.SplitN(body, "\n\n[frugal:", 2)[0]
	if len(kept) != 1000 {
		t.Errorf("expected a hard cut at 1000, got %d", len(kept))
	}
	if rep.CharsReturned != 1000 || rep.CharsTotal != 5000 {
		t.Errorf("report = %+v", rep)
	}
}

func TestCap_IsRuneSafe(t *testing.T) {
	// 3-byte runes with no whitespace: a byte-oriented cut would split one.
	body := strings.Repeat("日", 100)
	Cap(33, &body)
	kept := strings.SplitN(body, "\n\n[frugal:", 2)[0]
	if !utf8.ValidString(kept) {
		t.Fatal("cut produced invalid UTF-8")
	}
	if utf8.RuneCountInString(kept) != 33 {
		t.Errorf("kept %d runes, want 33", utf8.RuneCountInString(kept))
	}
}

func TestCap_SkipsNilAndEmptyFields(t *testing.T) {
	empty := ""
	body := strings.Repeat("y", 50)
	rep := Cap(20, nil, &empty, &body)
	if empty != "" {
		t.Error("empty field must stay empty")
	}
	if !rep.Truncated || rep.CharsTotal != 50 || rep.CharsReturned != 20 {
		t.Errorf("report = %+v", rep)
	}
}

func TestCap_MarkerNamesKeptAndTotal(t *testing.T) {
	body := strings.Repeat("z", 300)
	Cap(100, &body)
	if !strings.HasSuffix(body, "[frugal: output truncated to 100 of 300 chars; pass a larger max_chars to see more]") {
		t.Errorf("unexpected marker: %q", body[100:])
	}
}
