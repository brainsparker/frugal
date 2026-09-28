package routing

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DomainPolicy is the operator's rule for WHICH HOSTS Frugal will fetch
// on an agent's behalf, as opposed to Policy, which decides WHICH
// PROVIDER does the fetching. It is the compliance and safety rail for
// the URL-taking tools: frugal__extract, frugal__browse, and the URL
// intents of frugal__execute refuse a target the policy rejects before
// any provider is called, so nothing is spent and nothing leaves the
// machine. frugal__search applies the same rule to its result list, so
// an agent is never handed a URL it will then be refused.
//
// Why this exists: fetched web content is the main prompt-injection
// ingest point in an agent stack, and a page an agent was steered onto
// can carry instructions aimed at every other tool in the session. An
// allow list keeps a research agent on the sites it was meant to read;
// a deny list keeps it off the ones it must never touch; DenyPrivate
// keeps a remote-facing frugal from being used to read loopback, LAN,
// or cloud-metadata endpoints.
//
// A nil *DomainPolicy allows everything, so call sites can consult it
// unconditionally and the zero configuration reproduces the historical
// behavior exactly.
type DomainPolicy struct {
	allow       []domainRule
	deny        []domainRule
	denyPrivate bool
}

// domainRule is one normalized host pattern. A bare host ("example.com")
// matches itself and every subdomain; a wildcard rule ("*.example.com")
// matches subdomains only. Rules never carry a scheme, port, or path.
type domainRule struct {
	host           string
	subdomainsOnly bool
	raw            string // the operator's spelling, for error messages
}

// DomainError reports why a URL was refused. Host is the URL's host as
// matched; Rule is the operator's rule spelling when a list rule fired
// (empty for the private-network rail).
type DomainError struct {
	Host   string
	Rule   string
	Reason string
}

func (e *DomainError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("host %q %s (rule %q)", e.Host, e.Reason, e.Rule)
	}
	return fmt.Sprintf("host %q %s", e.Host, e.Reason)
}

// NewDomainPolicy validates and normalizes the operator's allow and deny
// lists (see NormalizeDomainRule) and returns the policy. Returns nil
// when nothing is configured, so callers can hand the result straight to
// the nil-safe Check.
func NewDomainPolicy(allow, deny []string, denyPrivate bool) (*DomainPolicy, error) {
	if len(allow) == 0 && len(deny) == 0 && !denyPrivate {
		return nil, nil
	}
	p := &DomainPolicy{denyPrivate: denyPrivate}
	for _, raw := range allow {
		r, err := parseRule(raw)
		if err != nil {
			return nil, fmt.Errorf("allow: %w", err)
		}
		p.allow = append(p.allow, r)
	}
	for _, raw := range deny {
		r, err := parseRule(raw)
		if err != nil {
			return nil, fmt.Errorf("deny: %w", err)
		}
		p.deny = append(p.deny, r)
	}
	return p, nil
}

// NormalizeDomainRule validates one rule and returns its canonical
// spelling: lower-cased, trailing dot dropped, optional "*." prefix kept.
// A rule must be a bare host: no scheme, no path, no port, no spaces.
// Shared with config validation so a typo fails the load with a precise
// message instead of silently matching nothing at runtime.
func NormalizeDomainRule(raw string) (string, error) {
	r, err := parseRule(raw)
	if err != nil {
		return "", err
	}
	if r.subdomainsOnly {
		return "*." + r.host, nil
	}
	return r.host, nil
}

func parseRule(raw string) (domainRule, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return domainRule{}, fmt.Errorf("empty domain rule")
	}
	if strings.Contains(s, "://") {
		return domainRule{}, fmt.Errorf("domain rule %q: drop the scheme, rules are bare hosts like example.com", raw)
	}
	if strings.ContainsAny(s, "/?# \t@") {
		return domainRule{}, fmt.Errorf("domain rule %q: rules are bare hosts, no path, query, or credentials", raw)
	}
	r := domainRule{raw: strings.TrimSpace(raw)}
	if strings.HasPrefix(s, "*.") {
		r.subdomainsOnly = true
		s = s[2:]
	}
	if strings.Contains(s, "*") {
		return domainRule{}, fmt.Errorf("domain rule %q: wildcards are only supported as a leading \"*.\"", raw)
	}
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return domainRule{}, fmt.Errorf("domain rule %q: no host", raw)
	}
	// An IP literal is an exact-match rule; a port on it is a mistake
	// (ports are ignored during matching, so the rule would never fire
	// the way the operator expects).
	if _, _, err := net.SplitHostPort(s); err == nil {
		return domainRule{}, fmt.Errorf("domain rule %q: drop the port, matching is host-only", raw)
	}
	if net.ParseIP(strings.Trim(s, "[]")) != nil && r.subdomainsOnly {
		return domainRule{}, fmt.Errorf("domain rule %q: an IP address has no subdomains", raw)
	}
	r.host = strings.Trim(s, "[]")
	return r, nil
}

// matches reports whether host (already lower-cased, no port) falls under
// the rule.
func (r domainRule) matches(host string) bool {
	if host == r.host {
		return !r.subdomainsOnly
	}
	return strings.HasSuffix(host, "."+r.host)
}

// Enabled reports whether the policy constrains anything. nil-safe.
func (p *DomainPolicy) Enabled() bool {
	return p != nil && (len(p.allow) > 0 || len(p.deny) > 0 || p.denyPrivate)
}

// Describe renders the policy for startup logs.
func (p *DomainPolicy) Describe() string {
	if !p.Enabled() {
		return "none"
	}
	var parts []string
	if len(p.allow) > 0 {
		parts = append(parts, "allow="+strings.Join(ruleStrings(p.allow), ","))
	}
	if len(p.deny) > 0 {
		parts = append(parts, "deny="+strings.Join(ruleStrings(p.deny), ","))
	}
	if p.denyPrivate {
		parts = append(parts, "deny_private=true")
	}
	return strings.Join(parts, " ")
}

func ruleStrings(rules []domainRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.subdomainsOnly {
			out = append(out, "*."+r.host)
		} else {
			out = append(out, r.host)
		}
	}
	return out
}

// Check reports whether rawURL may be fetched under the policy. The
// decision order is deny-wins: the private-network rail first, then the
// deny list, then (when an allow list exists) the requirement to match
// it. Matching is host-only: scheme, port, path, and query never
// participate, and hosts compare case-insensitively with a trailing dot
// ignored. A URL without a scheme is read as https so the bare-domain
// intents frugal__execute accepts ("read example.com/post") are judged
// by the host the fetch would actually hit. A nil policy allows
// everything. Returns a *DomainError on refusal.
//
// Honest limits: Check inspects the URL as written. It does not resolve
// hostnames, so a public name that resolves to a private address is not
// caught by DenyPrivate (the fixed suffixes localhost, .localhost,
// .local, .internal, and .home.arpa are), and it does not follow
// redirects the provider may take. It is a policy rail, not a network
// sandbox.
func (p *DomainPolicy) Check(rawURL string) error {
	if !p.Enabled() {
		return nil
	}
	host := hostOf(rawURL)
	if host == "" {
		// Nothing to judge; the provider will reject the URL on its own
		// terms. Refusing here would turn every malformed URL into a
		// misleading "denied by policy" message.
		return nil
	}
	if p.denyPrivate && isPrivateHost(host) {
		return &DomainError{Host: host, Reason: "is a private or local network address and routing.domains.deny_private is set"}
	}
	for _, r := range p.deny {
		if r.matches(host) {
			return &DomainError{Host: host, Rule: r.raw, Reason: "is denied by routing.domains.deny"}
		}
	}
	if len(p.allow) > 0 {
		for _, r := range p.allow {
			if r.matches(host) {
				return nil
			}
		}
		return &DomainError{Host: host, Reason: "is not in routing.domains.allow"}
	}
	return nil
}

// Allowed is the boolean form of Check for filtering result lists.
func (p *DomainPolicy) Allowed(rawURL string) bool {
	return p.Check(rawURL) == nil
}

// hostOf extracts the lower-cased, port-less host from a URL, reading a
// schemeless value as https. Empty when no host can be found.
func hostOf(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		if strings.Contains(s, "://") {
			return ""
		}
		u, err = url.Parse("https://" + s)
		if err != nil || u.Host == "" {
			return ""
		}
	}
	host := strings.ToLower(u.Hostname())
	return strings.TrimSuffix(host, ".")
}

// privateSuffixes are hostname suffixes that by convention never name a
// public host. Matched without DNS.
var privateSuffixes = []string{".localhost", ".local", ".internal", ".home.arpa"}

// isPrivateHost reports whether host is a loopback, private, link-local,
// carrier-grade NAT, or unspecified IP literal, or a conventional
// local-only hostname. Cloud metadata endpoints (169.254.169.254,
// fd00:ec2::254) fall under link-local / unique-local and are covered.
func isPrivateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	for _, suf := range privateSuffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	// Strip an IPv6 zone ("fe80::1%eth0") before parsing.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10, carrier-grade NAT (also Tailscale's range) and
		// 0.0.0.0/8 "this network" are LAN-side in practice.
		if v4[0] == 100 && v4[1]&0xc0 == 64 {
			return true
		}
		if v4[0] == 0 {
			return true
		}
	}
	return false
}
