package routing

import (
	"errors"
	"strings"
	"testing"
)

func mustPolicy(t *testing.T, allow, deny []string, denyPrivate bool) *DomainPolicy {
	t.Helper()
	p, err := NewDomainPolicy(allow, deny, denyPrivate)
	if err != nil {
		t.Fatalf("NewDomainPolicy: %v", err)
	}
	return p
}

func TestDomainPolicy_NilAndEmptyAllowEverything(t *testing.T) {
	var nilPolicy *DomainPolicy
	if err := nilPolicy.Check("https://anything.example/x"); err != nil {
		t.Fatalf("nil policy must allow, got %v", err)
	}
	if nilPolicy.Enabled() {
		t.Fatalf("nil policy must not report enabled")
	}
	p, err := NewDomainPolicy(nil, nil, false)
	if err != nil || p != nil {
		t.Fatalf("empty config should yield nil policy, got %v %v", p, err)
	}
}

func TestDomainPolicy_DenyMatchesHostAndSubdomains(t *testing.T) {
	p := mustPolicy(t, nil, []string{"Reddit.com", "tracker.example."}, false)
	for _, u := range []string{
		"https://reddit.com/r/golang",
		"https://www.reddit.com/r/golang",
		"https://old.REDDIT.com:443/",
		"http://tracker.example/pixel",
		"https://a.b.tracker.example/",
	} {
		err := p.Check(u)
		var de *DomainError
		if !errors.As(err, &de) {
			t.Fatalf("%s: expected DomainError, got %v", u, err)
		}
		if de.Rule == "" || !strings.Contains(err.Error(), "routing.domains.deny") {
			t.Fatalf("%s: error should name the rule and list, got %q", u, err)
		}
	}
	for _, u := range []string{
		"https://notreddit.com/",
		"https://reddit.com.evil.example/", // suffix must respect label boundary
		"https://example.com/reddit.com",   // path never participates
	} {
		if err := p.Check(u); err != nil {
			t.Fatalf("%s: should be allowed, got %v", u, err)
		}
	}
}

func TestDomainPolicy_WildcardRuleIsSubdomainsOnly(t *testing.T) {
	p := mustPolicy(t, nil, []string{"*.example.com"}, false)
	if err := p.Check("https://api.example.com/"); err == nil {
		t.Fatalf("subdomain should be denied by *.example.com")
	}
	if err := p.Check("https://example.com/"); err != nil {
		t.Fatalf("apex should not match *.example.com, got %v", err)
	}
}

func TestDomainPolicy_AllowListRestrictsAndDenyWins(t *testing.T) {
	p := mustPolicy(t, []string{"docs.python.org", "github.com"}, []string{"gist.github.com"}, false)
	cases := map[string]bool{
		"https://docs.python.org/3/library/":  true,
		"https://github.com/brainsparker/x":   true,
		"https://raw.github.com/x":            true,  // subdomain of an allowed host
		"https://gist.github.com/x":           false, // deny wins over allow
		"https://pypi.org/project/requests/":  false, // not in allow
		"https://python.org/":                 false, // allow rule was docs.python.org, apex is not under it
		"docs.python.org/3/tutorial/":         true,  // schemeless bare domain, read as https
		"https://user:pw@github.com:8443/x?y": true,  // credentials, port, query ignored
	}
	for u, want := range cases {
		err := p.Check(u)
		if (err == nil) != want {
			t.Errorf("%s: allowed=%v, want %v (err=%v)", u, err == nil, want, err)
		}
	}
	err := p.Check("https://pypi.org/")
	if err == nil || !strings.Contains(err.Error(), "routing.domains.allow") {
		t.Fatalf("not-in-allow error should name the allow list, got %v", err)
	}
}

func TestDomainPolicy_DenyPrivateBlocksLocalTargets(t *testing.T) {
	p := mustPolicy(t, nil, nil, true)
	blocked := []string{
		"http://localhost:3000/",
		"http://app.localhost/",
		"http://127.0.0.1:8080/admin",
		"http://[::1]/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.4.4/",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://100.64.0.1/",                       // CGNAT / tailnet
		"http://0.0.0.0/",
		"http://[fd00:ec2::254]/",
		"http://[fe80::1%25eth0]/",
		"http://nas.local/",
		"http://vault.internal/secret",
		"http://printer.home.arpa/",
	}
	for _, u := range blocked {
		err := p.Check(u)
		var de *DomainError
		if !errors.As(err, &de) {
			t.Errorf("%s: expected private-network refusal, got %v", u, err)
			continue
		}
		if de.Rule != "" || !strings.Contains(err.Error(), "deny_private") {
			t.Errorf("%s: private refusal should carry no rule and name deny_private, got %q", u, err)
		}
	}
	for _, u := range []string{
		"https://example.com/",
		"https://8.8.8.8/",
		"https://[2606:4700::1111]/",
		"https://mylocal.example/", // ".local" must be a suffix at a label boundary
	} {
		if err := p.Check(u); err != nil {
			t.Errorf("%s: should be allowed, got %v", u, err)
		}
	}
}

func TestDomainPolicy_DenyPrivateWinsOverAllow(t *testing.T) {
	p := mustPolicy(t, []string{"localhost"}, nil, true)
	if err := p.Check("http://localhost/"); err == nil {
		t.Fatalf("deny_private must win over an allow rule")
	}
}

func TestDomainPolicy_UnparseableURLIsLeftToProvider(t *testing.T) {
	p := mustPolicy(t, []string{"example.com"}, nil, true)
	for _, u := range []string{"", "   ", "://nohost", "https://"} {
		if err := p.Check(u); err != nil {
			t.Errorf("%q: hostless URL should pass through to the provider, got %v", u, err)
		}
	}
}

func TestDomainPolicy_AllowedAndDescribe(t *testing.T) {
	p := mustPolicy(t, []string{"a.example"}, []string{"*.b.example"}, true)
	if !p.Allowed("https://a.example/") || p.Allowed("https://c.example/") {
		t.Fatalf("Allowed disagrees with Check")
	}
	d := p.Describe()
	for _, want := range []string{"allow=a.example", "deny=*.b.example", "deny_private=true"} {
		if !strings.Contains(d, want) {
			t.Errorf("Describe %q missing %q", d, want)
		}
	}
	var none *DomainPolicy
	if none.Describe() != "none" {
		t.Errorf("nil Describe = %q", none.Describe())
	}
}

func TestNormalizeDomainRule(t *testing.T) {
	good := map[string]string{
		"Example.COM":     "example.com",
		" example.com. ":  "example.com",
		"*.Example.com":   "*.example.com",
		"169.254.169.254": "169.254.169.254",
		"[::1]":           "::1",
	}
	for in, want := range good {
		got, err := NormalizeDomainRule(in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomainRule(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := map[string]string{
		"":                    "empty",
		"https://example.com": "scheme",
		"example.com/path":    "path",
		"example.com:443":     "port",
		"*.*.example.com":     "wildcards",
		"foo*.example.com":    "wildcards",
		"*.10.0.0.1":          "subdomains",
		"user@example.com":    "credentials",
		"exa mple.com":        "path", // whitespace inside is caught by the same guard
	}
	for in, wantSub := range bad {
		_, err := NormalizeDomainRule(in)
		if err == nil || !strings.Contains(err.Error(), wantSub) {
			t.Errorf("NormalizeDomainRule(%q) = %v; want error mentioning %q", in, err, wantSub)
		}
	}
	if _, err := NewDomainPolicy([]string{"ok.example"}, []string{"http://bad"}, false); err == nil || !strings.HasPrefix(err.Error(), "deny:") {
		t.Errorf("NewDomainPolicy should prefix the failing list, got %v", err)
	}
}
