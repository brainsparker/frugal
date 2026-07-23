package tools

import (
	"regexp"
	"strings"
)

// Intent is the outcome of classifying a plain-language job description
// onto one of the routed capabilities. Classification is deterministic
// keyword / URL heuristics — no model call — and it never fails: when
// nothing matches, the intent is treated as a web search and Note says
// so. Note always explains the decision; it ships in the tool response
// so the agent can see what was decided and why.
type Intent struct {
	Capability string // "search" | "extract" | "browse"
	URL        string // target for extract / browse
	Query      string // query text for search
	Freshness  string // "", "day", "week", "month" (search only)
	Note       string // one-line classification explanation
}

var (
	schemedURLRe = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"')\]]+`)
	wwwURLRe     = regexp.MustCompile(`(?i)\bwww\.[a-z0-9][^\s<>"')\]]*`)
	// bareDomainRe candidates still need a path or an allowlisted TLD
	// (see bareTLDs) before they count as a URL — that's what keeps
	// "node.js", "v1.2", and "e.g." from classifying as extracts.
	bareDomainRe = regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]{0,62}(\.[a-z0-9][a-z0-9-]{0,62})+(/[^\s<>"']*)?`)
)

// bareTLDs is the conservative allowlist a schemeless bare domain must
// end in (unless it carries a path) to be treated as a URL.
var bareTLDs = map[string]bool{
	"com": true, "org": true, "net": true, "io": true, "dev": true,
	"ai": true, "app": true, "sh": true, "co": true, "edu": true,
	"gov": true, "mil": true, "int": true, "info": true, "biz": true,
	"me": true, "us": true, "uk": true, "de": true, "fr": true,
	"jp": true, "cn": true, "in": true, "au": true, "ca": true,
	"nl": true, "se": true, "no": true, "es": true, "it": true,
	"ch": true, "eu": true, "nz": true, "br": true, "pl": true,
	"xyz": true, "tech": true, "cloud": true, "site": true, "news": true,
	"blog": true, "wiki": true, "tv": true,
}

// browseCues force a URL-bearing intent to the headless renderer.
var browseCues = []string{
	"render", "headless", "screenshot", "js-rendered", "javascript",
	"after js", "post-render",
}

// searchCues force search even when the intent contains a URL — "find
// alternatives to https://x.com" is a search about the URL, not a fetch
// of it.
var searchCues = []string{
	"search", "find ", "look up", "lookup", "alternatives to",
	"similar to", "reviews of", "news about", "who links",
}

// searchPrefixes are lead-in verb phrases stripped from the front of a
// search query — the agent said "search for X"; the provider should see
// "X". Longest-first so "web search for " wins over "search ".
var searchPrefixes = []string{
	"web search for ", "search the web for ", "search for ", "search ",
	"look up ", "google ", "find ",
}

// freshnessCues maps phrasing to the Query.Freshness windows the search
// drivers understand. Checked in day → week → month order; first hit wins.
var freshnessCues = []struct {
	window string
	cues   []string
}{
	{"day", []string{"today", "latest", "breaking", "right now"}},
	{"week", []string{"this week", "past week", "recent"}},
	{"month", []string{"this month", "past month"}},
}

// trimURLTail strips the trailing punctuation prose drags along ("read
// https://x.com/a." → the dot is the sentence's, not the URL's).
func trimURLTail(u string) string {
	return strings.TrimRight(u, ".,;:!?'\"")
}

// findURL returns the first URL-looking token in raw (normalized to
// carry a scheme) and whether more than one candidate appeared.
func findURL(raw string) (url string, multiple bool) {
	if m := schemedURLRe.FindAllString(raw, 2); len(m) > 0 {
		return trimURLTail(m[0]), len(m) > 1
	}
	if m := wwwURLRe.FindAllString(raw, 2); len(m) > 0 {
		return "https://" + trimURLTail(m[0]), len(m) > 1
	}
	var hits []string
	for _, m := range bareDomainRe.FindAllStringSubmatch(raw, 2) {
		candidate := trimURLTail(m[0])
		hasPath := strings.Contains(candidate, "/")
		host := candidate
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		labels := strings.Split(host, ".")
		tld := strings.ToLower(labels[len(labels)-1])
		if hasPath || bareTLDs[tld] {
			hits = append(hits, "https://"+candidate)
		}
	}
	if len(hits) > 0 {
		return hits[0], len(hits) > 1
	}
	return "", false
}

func containsAny(lower string, cues []string) bool {
	for _, c := range cues {
		if strings.Contains(lower, c) {
			return true
		}
	}
	return false
}

// ClassifyIntent maps one plain-language intent onto a capability.
// Decision order: a URL plus a render cue is a browse; a search cue
// wins over a URL; a URL alone is an extract; everything else is a
// search with lead-in verbs stripped and freshness phrasing mapped to
// the drivers' time windows.
func ClassifyIntent(raw string) Intent {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	url, multiple := findURL(trimmed)

	note := func(s string) string {
		if multiple {
			return s + " (multiple URLs found; using the first)"
		}
		return s
	}

	if url != "" && containsAny(lower, browseCues) {
		return Intent{
			Capability: "browse",
			URL:        url,
			Note:       note("URL with a render cue; routed to a headless browse"),
		}
	}
	if url != "" && containsAny(lower, searchCues) {
		return Intent{
			Capability: "search",
			Query:      trimmed,
			Freshness:  freshnessOf(lower),
			Note:       note("search cue outweighs the URL; routed to a web search"),
		}
	}
	if url != "" {
		return Intent{
			Capability: "extract",
			URL:        url,
			Note:       note("URL detected; routed to a page extract"),
		}
	}

	query := trimmed
	lowerQuery := lower
	for _, p := range searchPrefixes {
		if strings.HasPrefix(lowerQuery, p) {
			query = strings.TrimSpace(query[len(p):])
			break
		}
	}
	n := "routed to a web search"
	if !containsAny(lower, searchCues) {
		n = "no URL detected; treated as a web search"
	}
	return Intent{
		Capability: "search",
		Query:      query,
		Freshness:  freshnessOf(lower),
		Note:       n,
	}
}

func freshnessOf(lower string) string {
	for _, fc := range freshnessCues {
		if containsAny(lower, fc.cues) {
			return fc.window
		}
	}
	return ""
}
