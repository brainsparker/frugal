package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/frugalsh/frugal/internal/cache"
	"github.com/frugalsh/frugal/internal/extract"
	"github.com/frugalsh/frugal/internal/limit"
	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// Search-and-read: frugal__search can open the top N hits through the
// extract chain in the same call (read_top). The measured failure this
// closes is the agent that answers from snippets and never opens the
// page: a second tool round-trip is exactly the step agents skip, and
// a snippet-only answer is stale several times more often than one
// grounded in the page. Each page carries its own fetch receipt, so a
// dead link is reported instead of silently dropped, and an undated
// result says so rather than passing as current.
//
// Pages read under the same call deadline and the same max_cost_usd as
// the search: the ceiling left after the search is split evenly across
// the pages, so the whole call never spends more than the caller
// allowed. Reads run through the operator's extract policy (local
// Readability first by default, then a free hosted render, then a paid
// scrape), book spend and cooldowns under the extract capability, and
// share the extract result cache with frugal__extract.

const (
	// MaxReadTop bounds how many pages one search call may open. Five
	// pages at the default page cap is roughly 5,000 tokens of context,
	// which is already a wide read for one question; past that the
	// agent should narrow the query or call frugal__extract on a
	// specific hit.
	MaxReadTop = 5
	// DefaultPageMaxChars is the per-page content cap when neither the
	// caller (page_max_chars) nor the operator (limits.max_chars) set
	// one. One 100k-character page would otherwise crowd out the other
	// four and blow the context budget of clients that cap tool output;
	// 4,000 characters (about 1,000 tokens) keeps every page readable.
	DefaultPageMaxChars = 4000
	// readConcurrency is how many pages are fetched at once. Local
	// Readability is cheap; hosted rungs are rate limited per minute,
	// so a small fan-out keeps a five-page read fast without tripping a
	// keyless provider's limit.
	readConcurrency = 3
)

// Page is one top search hit read through the extract chain, with the
// fetch receipt that makes the read auditable: which provider produced
// it, what it cost, how much content came back, and whether the page
// is dated. A page that could not be read still appears, with
// fetch_status "error" and the reason, so the agent sees the dead link
// instead of a shorter list.
type Page struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	// Content is the page body, capped at page_max_chars, in the format
	// ContentFormat names: "markdown" when the provider produced real
	// markdown, "text" when the provider's markdown field was really
	// HTML (go-readability) and plain text is the denser read. One
	// format only: pages are a read, not an archive, and returning
	// html and text alongside would triple the context cost of every
	// page. Re-read one URL with frugal__extract for the other formats.
	Content       string `json:"content,omitempty"`
	ContentFormat string `json:"content_format,omitempty"`
	// FetchStatus is ok | error | skipped. skipped means Frugal chose
	// not to fetch (domain policy, no URL); error means every allowed
	// provider failed or the call deadline arrived first.
	FetchStatus string `json:"fetch_status"`
	Error       string `json:"error,omitempty"`
	// ErrorKind is the routing classification of the failure when one
	// applies: fatal (the page is gone: 404 / 410), permanent, or
	// transient. A fatal page is dead for every provider; do not retry.
	ErrorKind    string  `json:"error_kind,omitempty"`
	ProviderUsed string  `json:"provider_used,omitempty"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyMS    int64   `json:"latency_ms"`
	// Size footer, as on frugal__extract: what page_max_chars did.
	CharsReturned int  `json:"chars_returned"`
	CharsTotal    int  `json:"chars_total"`
	Truncated     bool `json:"truncated,omitempty"`
	// Cached reports that the content came from the shared extract
	// result cache; cost_usd is 0 on a hit.
	Cached bool `json:"cached,omitempty"`
	// AgeDays is how old the page is according to the search
	// provider's published_at, in whole days (0 = today). Undated is
	// set when the provider gave no parseable date, so the agent can
	// weigh an undated page accordingly instead of assuming it is
	// current. Exactly one of the two is present.
	AgeDays *int `json:"age_days,omitempty"`
	Undated bool `json:"undated,omitempty"`
}

// WithPageReaders supplies the extract chain frugal__search uses for
// read_top, with the extract capability's policy and latency source.
// Without it (the default), read_top is refused with an error naming
// the gap, and the search tool behaves exactly as before.
func WithPageReaders(extractors []extract.Extractor, policy routing.Policy, lat routing.LatencyLookup) ToolOption {
	return func(o *toolOptions) {
		o.readers = extractors
		o.readPolicy = policy
		o.readLat = lat
	}
}

// normalizeReadTop validates the caller's read_top. Negative is a
// caller error; values above MaxReadTop clamp, mirroring max_results,
// since "read more" is a preference and not a contradiction.
func normalizeReadTop(n int) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("read_top must be zero or positive, got %d", n)
	}
	if n > MaxReadTop {
		return MaxReadTop, nil
	}
	return n, nil
}

// effectivePageMaxChars resolves the per-page budget: the caller's
// page_max_chars wins, then the operator's limits.max_chars, then
// DefaultPageMaxChars. Unlike a single extract, a page read is never
// unlimited: five uncapped pages is the context blowout read_top exists
// to avoid.
func (o toolOptions) effectivePageMaxChars(requested int) int {
	if requested > 0 {
		return requested
	}
	if o.maxChars > 0 {
		return o.maxChars
	}
	return DefaultPageMaxChars
}

// pageCeiling splits what the caller's ceiling has left after the
// search evenly across n pages, so the pages together can never exceed
// the call's max_cost_usd. An unset ceiling stays unset. Splitting is
// conservative (one expensive page cannot borrow from a free one), but
// it is exact without serializing the reads, and the common chains
// (free rungs first) are unaffected.
func pageCeiling(c routing.Ceiling, spent float64, n int) routing.Ceiling {
	if !c.Set || n <= 0 {
		return c
	}
	remaining := c.MaxUSD - spent
	if remaining < 0 {
		remaining = 0
	}
	return routing.Ceiling{MaxUSD: remaining / float64(n), Set: true}
}

// readTopPages opens the first n items with a URL through the extract
// chain and returns one Page per item, in result order. The context
// carries the call deadline; a page the deadline reaches before its
// read starts is reported as an error, not dropped. hook fires for
// every provider attempt (metrics, guard, attempts[]).
func readTopPages(ctx context.Context, o toolOptions, items []search.Item, n int, pageMax int, ceiling routing.Ceiling, hook routing.AttemptHook, logger *slog.Logger) []Page {
	if n > len(items) {
		n = len(items)
	}
	if n <= 0 {
		return nil
	}
	pages := make([]Page, n)
	sem := make(chan struct{}, readConcurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pages[i] = readOnePage(ctx, o, items[i], pageMax, ceiling, hook, logger)
		}(i)
	}
	wg.Wait()
	return pages
}

func readOnePage(ctx context.Context, o toolOptions, it search.Item, pageMax int, ceiling routing.Ceiling, hook routing.AttemptHook, logger *slog.Logger) Page {
	p := Page{URL: it.URL, Title: it.Title}
	setPageAge(&p, it.PublishedAt, time.Now())
	if strings.TrimSpace(it.URL) == "" {
		p.FetchStatus = "skipped"
		p.Error = "result has no URL"
		return p
	}
	// Search results were already filtered by the domain policy, but
	// the check is cheap and keeps the invariant local: a page is never
	// fetched that frugal__extract would refuse.
	if err := o.domains.Check(it.URL); err != nil {
		p.FetchStatus = "skipped"
		p.Error = "refused by domain policy: " + err.Error()
		return p
	}
	start := time.Now()

	// Same key frugal__extract uses for an auto-routed, default-format
	// call, so a page read here is a free hit there and vice versa.
	key := cache.ExtractKey("", it.URL, nil)
	if v, _, ok := o.resultCache.Get(key); ok {
		if hit, isExtract := v.(cachedExtract); isExtract {
			p.FetchStatus = "ok"
			p.Cached = true
			p.ProviderUsed = hit.Provider
			p.Content, p.ContentFormat = pageContent(hit.Res)
			if p.Title == "" {
				p.Title = hit.Res.Title
			}
			capPage(&p, pageMax)
			p.LatencyMS = time.Since(start).Milliseconds()
			return p
		}
	}

	if ctx.Err() != nil {
		// The deadline arrived while this page waited its turn. Say so;
		// a missing page would read as "the search had fewer hits."
		return pageError(p, routing.ContextStopError(ctx, "extract", nil), start)
	}

	now := time.Now()
	ordered, reason := routing.Apply(o.readers, o.readPolicy, o.readLat, now)
	if len(ordered) == 0 {
		return pageError(p, errors.New("every configured extract provider is denied by the routing policy"), start)
	}
	ordered, reason = guardChain(o.guard, "extract", ordered, reason, logger)
	if len(ordered) == 0 {
		return pageError(p, guardEmptyError("extract"), start)
	}
	affordable := ordered
	ordered, reason = routing.ApplyCeiling(ordered, ceiling, reason, now)
	if len(ordered) == 0 {
		return pageError(p, routing.CeilingEmptyError("extract", ceiling, affordable, now), start)
	}
	logger.Debug("read_top routing", "url", it.URL, "reason", reason)

	used, res, err := extract.CallInOrder(ctx, ordered, extract.Query{URL: it.URL}, logger, hook)
	if err != nil {
		return pageError(p, err, start)
	}
	o.resultCache.Put(key, cachedExtract{Res: res, Provider: used.Name()}, res.CostUSD, o.extractTTL)

	p.FetchStatus = "ok"
	p.ProviderUsed = used.Name()
	p.CostUSD = res.CostUSD
	p.Content, p.ContentFormat = pageContent(res)
	if p.Title == "" {
		p.Title = res.Title
	}
	capPage(&p, pageMax)
	p.LatencyMS = time.Since(start).Milliseconds()
	return p
}

func pageError(p Page, err error, start time.Time) Page {
	p.FetchStatus = "error"
	p.Error = err.Error()
	var re *routing.Error
	if errors.As(err, &re) {
		p.ErrorKind = re.Kind.String()
	}
	p.LatencyMS = time.Since(start).Milliseconds()
	return p
}

func capPage(p *Page, pageMax int) {
	rep := limit.Cap(pageMax, &p.Content)
	p.CharsReturned, p.CharsTotal, p.Truncated = rep.CharsReturned, rep.CharsTotal, rep.Truncated
}

// pageContent picks the densest readable rendering of an extract
// result for a page read. Markdown is preferred, but some drivers
// (go-readability) fill the markdown field with the article HTML; HTML
// spends most of a small page budget on tags, so when the markdown
// opens with a tag and the driver also produced plain text, the text
// wins. Falls back to whatever is non-empty.
func pageContent(res extract.Result) (string, string) {
	md := strings.TrimSpace(res.Markdown)
	txt := strings.TrimSpace(res.Text)
	switch {
	case md != "" && !(strings.HasPrefix(md, "<") && txt != ""):
		return res.Markdown, "markdown"
	case txt != "":
		return squeezeWhitespace(res.Text), "text"
	case md != "":
		return res.Markdown, "markdown"
	case strings.TrimSpace(res.HTML) != "":
		return res.HTML, "html"
	}
	return "", ""
}

// publishedLayouts are the date shapes search providers emit for
// published_at. The field is documented as ISO-8601, but drivers pass
// through what the provider sent, which ranges from a bare date to a
// full RFC 3339 timestamp.
var publishedLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	time.RFC1123Z,
	time.RFC1123,
}

// setPageAge fills AgeDays from a published_at string, or marks the
// page Undated when the string is empty or unparseable. A date in the
// future (clock skew, a provider's scheduled-post quirk) reads as 0
// days old rather than negative.
func setPageAge(p *Page, publishedAt string, now time.Time) {
	s := strings.TrimSpace(publishedAt)
	if s == "" {
		p.Undated = true
		return
	}
	for _, layout := range publishedLayouts {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		days := int(now.Sub(t).Hours() / 24)
		if days < 0 {
			days = 0
		}
		p.AgeDays = &days
		return
	}
	p.Undated = true
}

// squeezeWhitespace collapses the whitespace runs Readability's text
// rendering inherits from page layout (navigation columns, table cells)
// so the page budget is spent on words: runs of spaces and tabs become
// one space, and three or more newlines become a paragraph break.
// Applied to the text format only; markdown whitespace is meaningful.
func squeezeWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	spaces, newlines := 0, 0
	flush := func() {
		switch {
		case newlines >= 2:
			b.WriteString("\n\n")
		case newlines == 1:
			b.WriteByte('\n')
		case spaces > 0:
			b.WriteByte(' ')
		}
		spaces, newlines = 0, 0
	}
	for _, r := range s {
		switch r {
		case '\n':
			newlines++
		case ' ', '\t', '\r', '\u00a0':
			spaces++
		default:
			flush()
			b.WriteRune(r)
		}
	}
	flush()
	return strings.TrimSpace(b.String())
}

// pageChars counts the characters the pages put into the agent's
// context: title, URL, and the (already capped) markdown per page.
func pageChars(pages []Page) int {
	total := 0
	for _, p := range pages {
		total += limit.Count(p.Title, p.URL, p.Content)
	}
	return total
}

// countPages reports how many pages were read and how many failed.
// Skipped pages (no URL, domain policy) count as neither: nothing was
// attempted.
func countPages(pages []Page) (read, failed int) {
	for _, p := range pages {
		switch p.FetchStatus {
		case "ok":
			read++
		case "error":
			failed++
		}
	}
	return read, failed
}
