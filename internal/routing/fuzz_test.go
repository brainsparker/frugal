package routing

import (
	"net/http"
	"testing"
)

func FuzzDomainPolicy(f *testing.F) {
	f.Add("example.com", "*.internal", "https://a.internal/x")
	f.Add("", "", "http://[::1]:80/")
	f.Add("*.", "*", "://")
	f.Add("EXAMPLE.com.", "gist.github.com", "https://user:pw@Example.COM./p?q#f")
	f.Add("xn--nxasmq6b", "127.0.0.1", "http://0x7f.1/")
	f.Add("a..b", "%zz", "http://%41.com")
	f.Fuzz(func(t *testing.T, allow, deny, rawURL string) {
		_, _ = NormalizeDomainRule(allow)
		_, _ = NormalizeDomainRule(deny)
		var al, dl []string
		if allow != "" {
			al = []string{allow}
		}
		if deny != "" {
			dl = []string{deny}
		}
		p, err := NewDomainPolicy(al, dl, true)
		if err != nil {
			return
		}
		err = p.Check(rawURL)
		if (err == nil) != p.Allowed(rawURL) {
			t.Fatalf("Check and Allowed disagree for %q", rawURL)
		}
		_ = p.Describe()
	})
}

func FuzzParseRetryAfter(f *testing.F) {
	for _, s := range []string{"", "0", "-1", "3", "999999999999999999999", "Wed, 21 Oct 2015 07:28:00 GMT", "soon", " 5 "} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, v string) {
		h := http.Header{}
		h.Set("Retry-After", v)
		if d := ParseRetryAfter(h); d < 0 {
			t.Fatalf("negative Retry-After %s for %q", d, v)
		}
	})
}
