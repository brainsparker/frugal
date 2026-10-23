package tools

import (
	"testing"
	"time"
)

func FuzzClassifyIntent(f *testing.F) {
	for _, s := range []string{"", "find frugal", "read https://example.com", "render https://x.example/app js", "https://", "\x00\xff", "search: <script>", "browse http://[::1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		ch := make(chan Intent, 1)
		go func() { ch <- ClassifyIntent(in) }()
		var it Intent
		select {
		case it = <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("ClassifyIntent hung on %q", in)
		}
		switch it.Capability {
		case "", "search", "extract", "browse":
		default:
			t.Fatalf("unknown capability %q for %q", it.Capability, in)
		}
	})
}
