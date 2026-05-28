package browse

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/routing"
)

type stubBrowser struct {
	name  string
	cost  float64
	res   Result
	err   error
	calls int
}

func (s *stubBrowser) Name() string         { return s.name }
func (s *stubBrowser) CostPerCall() float64 { return s.cost }
func (s *stubBrowser) Render(_ context.Context, _ Query) (Result, error) {
	s.calls++
	return s.res, s.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCallWithFallback_PicksCheapest(t *testing.T) {
	cheap := &stubBrowser{name: "cheap", cost: 0.001, res: Result{HTML: "ok", CostUSD: 0.001}}
	pricey := &stubBrowser{name: "pricey", cost: 0.01, res: Result{HTML: "should-not-be-used"}}
	used, res, err := CallWithFallback(context.Background(), []Browser{pricey, cheap}, Query{URL: "https://x"}, discardLogger(), nil)
	if err != nil {
		t.Fatalf("CallWithFallback: %v", err)
	}
	if used.Name() != "cheap" || res.HTML != "ok" {
		t.Errorf("unexpected: %v / %+v", used, res)
	}
	if pricey.calls != 0 {
		t.Errorf("pricey shouldn't have been called; calls=%d", pricey.calls)
	}
}

func TestCallWithFallback_StopsOnPermanent(t *testing.T) {
	first := &stubBrowser{name: "first", cost: 0.001,
		err: routing.Permanent("first", 400, errors.New("bad url"))}
	second := &stubBrowser{name: "second", cost: 0.002,
		res: Result{HTML: "should-not-be-tried"}}
	_, _, err := CallWithFallback(context.Background(), []Browser{first, second}, Query{URL: "https://x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected permanent error to propagate")
	}
	if second.calls != 0 {
		t.Errorf("second should not be called after permanent; calls=%d", second.calls)
	}
}

func TestCallWithFallback_NoBrowsersErrors(t *testing.T) {
	_, _, err := CallWithFallback(context.Background(), nil, Query{URL: "https://x"}, discardLogger(), nil)
	if err == nil {
		t.Fatalf("expected error when no browsers configured")
	}
}

func TestCallPinned_UnknownProviderErrors(t *testing.T) {
	bs := []Browser{&stubBrowser{name: "only", cost: 0.001}}
	_, _, err := CallPinned(context.Background(), bs, "nope", Query{URL: "https://x"}, discardLogger(), nil)
	var notConfigured *ErrProviderNotConfigured
	if !errors.As(err, &notConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
	if notConfigured.Name != "nope" {
		t.Errorf("Name = %q want nope", notConfigured.Name)
	}
}

// TestStripHTML_BasicStrip checks the inline HTML→text strip handles
// the common cases: tags removed, whitespace collapsed, script + style
// blocks dropped entirely.
func TestStripHTML_BasicStrip(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<h1>Title</h1><p>Body text.</p>", "Title Body text."},
		{"a<br/>b", "a b"},
		{"<script>alert(1)</script>after", "after"},
		{"<style>p{color:red}</style>after", "after"},
		{"  leading and  multiple   spaces  ", "leading and multiple spaces"},
		{"<div class=\"x\">attr-quoted</div>", "attr-quoted"},
		{"AT&amp;T &lt;ok&gt; &quot;yes&quot;", "AT&T <ok> \"yes\""},
		{"a&nbsp;&nbsp;b", "a b"},
	}
	for _, c := range cases {
		got := StripHTML(c.in)
		if got != c.want {
			t.Errorf("StripHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripHTML_RealWorldish smoke-tests on a heftier example.
func TestStripHTML_RealWorldish(t *testing.T) {
	in := `<!doctype html><html><head><title>T</title><style>body{color:#000}</style></head><body><nav>nav</nav><h1>Hi</h1><p>First paragraph. <a href="x">link</a></p><script>tracker()</script><footer>©</footer></body></html>`
	got := StripHTML(in)
	if !strings.Contains(got, "Hi") || !strings.Contains(got, "First paragraph") {
		t.Errorf("output should contain headline + paragraph; got %q", got)
	}
	if strings.Contains(got, "tracker") || strings.Contains(got, "color:#000") {
		t.Errorf("script + style content leaked; got %q", got)
	}
}
