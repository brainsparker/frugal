package limit

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzCap(f *testing.F) {
	f.Add(10, "hello world foo bar", "second field", "")
	f.Add(1, "日本語のテキスト", "x", "y")
	f.Add(3, "\xff\xfe\xfd", "", "z")
	f.Add(0, "a", "b", "c")
	f.Add(-5, "a b c", "", "")
	f.Add(5, "     ", "    x", "")
	f.Fuzz(func(t *testing.T, max int, a, b, c string) {
		if max > 1<<20 {
			max = 1 << 20
		}
		fa, fb, fc := a, b, c
		rep := Cap(max, &fa, &fb, &fc)
		if rep.CharsTotal != Count(a, b, c) {
			t.Fatalf("CharsTotal %d != %d", rep.CharsTotal, Count(a, b, c))
		}
		if max > 0 && rep.CharsReturned > max {
			t.Fatalf("returned %d > budget %d", rep.CharsReturned, max)
		}
		if max <= 0 && (fa != a || fb != b || fc != c || rep.Truncated) {
			t.Fatal("no-limit call modified fields")
		}
		for _, pair := range [][2]string{{a, fa}, {b, fb}, {c, fc}} {
			orig, got := pair[0], pair[1]
			if got == orig || got == "" {
				continue
			}
			if i := strings.Index(got, "\n\n["); i >= 0 {
				got = got[:i]
			}
			if !strings.HasPrefix(orig, got) {
				t.Fatalf("kept text is not a prefix of the original: %q vs %q", got, orig)
			}
			if utf8.ValidString(orig) && !utf8.ValidString(got) {
				t.Fatalf("cut split a rune: %q", got)
			}
		}
	})
}
