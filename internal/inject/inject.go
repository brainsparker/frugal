// Package inject scans fetched content for prompt-injection payloads
// before it reaches the agent's context, and reports what it found on
// the receipt.
//
// Fetched web content is the main untrusted input in an agent stack.
// A page can carry text aimed not at the human reader but at the model
// that will summarize it: "ignore your previous instructions", a fake
// SYSTEM turn, a request to call a tool or write a config file, hidden
// in a white-on-white span, an HTML comment, or a run of zero-width
// characters. The model cannot tell page text from operator text, and
// most fetch and search servers hand the page over verbatim with no
// label. Frugal already owns the point where provider output becomes
// tool output, so this is where a deterministic check belongs.
//
// The scanner is heuristic and pattern-based: no model call, no network,
// no new dependency. It looks for four things that have no business in
// article content: instruction overrides and role impersonation aimed at
// an assistant, bait that names tools, shells, or config files, hidden
// HTML (comments, display:none, zero font size, off-screen positioning,
// aria-hidden) and invisible Unicode (zero-width joiners, bidi overrides,
// Unicode tag characters), and base64 runs that decode to instruction
// text. Each hit is a Finding with a kind, the field it was in, and a
// short snippet so the operator can see what tripped it.
//
// Two modes do something with the findings. Flag leaves content
// byte-for-byte as the provider returned it and attaches the report.
// Redact additionally removes hidden HTML, strips invisible Unicode, and
// replaces each matched span with a visible marker, so the payload never
// reaches the model while the marker tells it something was cut. Off
// disables the scanner.
//
// Honest limits: this is risk reduction, not a guarantee. A payload
// phrased as ordinary prose, split across paragraphs, or written in a
// language the patterns do not cover will pass. Articles about prompt
// injection will trip it (the findings make that obvious). The
// deterministic rails around it, the domain policy and the client's own
// confirmation step for consequential actions, remain the real
// boundary; this one makes the attempt visible on the receipt.
package inject

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mode selects what the scanner does with its findings.
type Mode string

const (
	// ModeOff disables scanning entirely. Results carry no report.
	ModeOff Mode = "off"
	// ModeFlag scans and reports but leaves content untouched. The
	// default: an existing install sees new fields on the receipt and
	// nothing else changes.
	ModeFlag Mode = "flag"
	// ModeRedact scans, reports, and removes what it found: hidden HTML
	// and invisible Unicode are stripped, matched spans are replaced by
	// a visible marker.
	ModeRedact Mode = "redact"
)

// DefaultMode is what an absent config value means.
const DefaultMode = ModeFlag

// ParseMode normalizes a config string to a Mode. Empty means the
// default. Unknown values return an error naming the accepted set so a
// typo fails the config load instead of silently disabling a safety
// rail.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return DefaultMode, nil
	case "off", "none", "false":
		return ModeOff, nil
	case "flag", "warn", "detect":
		return ModeFlag, nil
	case "redact", "strip":
		return ModeRedact, nil
	}
	return "", fmt.Errorf("injection_scan must be one of off, flag, redact; got %q", s)
}

// Risk is the overall severity of a report.
type Risk string

const (
	RiskNone   Risk = "none"
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// Kind classifies one finding.
type Kind string

const (
	// KindInstructionOverride is text telling the model to ignore,
	// forget, or replace its instructions.
	KindInstructionOverride Kind = "instruction_override"
	// KindRoleImpersonation is a fake chat-transcript turn: a SYSTEM
	// line, a chat-template token, an "assistant:" turn in page prose.
	KindRoleImpersonation Kind = "role_impersonation"
	// KindAgentTargeting is prose addressed to an AI reader ("if you are
	// an AI assistant", "note to any LLM reading this").
	KindAgentTargeting Kind = "agent_targeting"
	// KindToolBait is a request to call a tool, run a shell command,
	// write a config file, or send data somewhere.
	KindToolBait Kind = "tool_bait"
	// KindSensitivePath is a mention of writing an agent config file
	// (mcp.json, CLAUDE.md, .cursor/) or reading a secret store. Common
	// in legitimate setup documentation, suspicious inside article
	// content, so it is medium severity on its own.
	KindSensitivePath Kind = "sensitive_path"
	// KindHiddenHTML is content a human reader would not see: comments,
	// display:none, zero font size, off-screen, aria-hidden, zero
	// opacity, or white-on-white text.
	KindHiddenHTML Kind = "hidden_html"
	// KindInvisibleUnicode is a run of zero-width, bidi-override, or
	// Unicode tag characters, the usual carriers for text that renders
	// as nothing.
	KindInvisibleUnicode Kind = "invisible_unicode"
	// KindEncodedPayload is a base64 run that decodes to text matching
	// one of the instruction patterns.
	KindEncodedPayload Kind = "encoded_payload"
)

// Finding is one hit.
type Finding struct {
	Kind Kind `json:"kind"`
	// Field names the content field the hit was in ("markdown", "text",
	// "html", or "results[3].snippet").
	Field string `json:"field"`
	// Snippet is a short excerpt around the match, whitespace-collapsed
	// and capped, so the operator can judge it without the full page.
	Snippet string `json:"snippet"`
	// Count is how many matches of this kind were folded into this
	// finding. Repeated identical hits are collapsed per kind and field.
	Count int `json:"count,omitempty"`
}

// Report is what the MCP tools attach to a result.
type Report struct {
	Risk     Risk      `json:"risk"`
	Findings []Finding `json:"findings,omitempty"`
	// Action is what the scanner did: "flagged" or "redacted".
	Action string `json:"action"`
	// RedactedChars counts the characters removed in redact mode.
	RedactedChars int `json:"redacted_chars,omitempty"`
}

// Field is one named content field to scan, passed by pointer so redact
// mode can rewrite it in place.
type Field struct {
	Name string
	// HTML marks the field as markup. Hidden-element and comment
	// detection runs only on HTML fields; text patterns run on all.
	HTML bool
	Ptr  *string
}

// Scanner holds a Mode. The zero value is ModeOff. Scanners are
// stateless and safe for concurrent use.
type Scanner struct {
	Mode Mode
}

// New returns a Scanner in the given mode.
func New(m Mode) *Scanner { return &Scanner{Mode: m} }

// Enabled reports whether Scan does anything. A nil Scanner is off.
func (s *Scanner) Enabled() bool { return s != nil && s.Mode != "" && s.Mode != ModeOff }

// snippetWidth caps each finding's excerpt.
const snippetWidth = 160

// marker is what replaces a redacted span. It names the kind so a
// model reading flattened text knows what was cut and why.
func marker(k Kind) string {
	return fmt.Sprintf("[frugal: redacted suspected prompt injection (%s)]", k)
}

type pattern struct {
	kind Kind
	re   *regexp.Regexp
}

// textPatterns run against every field. Case-insensitive unless noted.
// They are deliberately specific: the cost of a false positive is a
// noisy receipt in flag mode and cut prose in redact mode, so each one
// requires an instruction verb next to its object rather than a lone
// keyword, and forms that are common in legitimate documentation (an
// install guide saying "add this to ~/.zshrc", a Linux tutorial running
// cat /etc/passwd) are classified under the medium-severity
// sensitive_path kind rather than tool_bait.
var textPatterns = []pattern{
	{KindInstructionOverride, regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override|bypass|discard)\b[^.\n]{0,40}?\b(?:all |any |the |your |my |these |those )?(?:previous|prior|above|earlier|preceding|original|initial|system|developer|existing|former)\b[^.\n]{0,30}?\b(?:instructions?|prompts?|rules?|guidelines?|directives?|context|constraints?)\b`)},
	{KindInstructionOverride, regexp.MustCompile(`(?i)\b(?:new|updated|revised|real|actual|true|important|urgent)\s+(?:system\s+)?(?:instructions?|directives?)\s*(?::|follow\b|below\b)`)},
	{KindInstructionOverride, regexp.MustCompile(`(?i)\byou\s+are\s+now\s+(?:(?:a|an)\s+(?:\w+\s+){0,2}(?:ai|assistant|model|bot|agent|dan)\b|free\b|unrestricted\b|unfiltered\b|jailbroken\b|no\s+longer\s+(?:bound|restricted|an?\s+ai))`)},
	{KindInstructionOverride, regexp.MustCompile(`(?i)\b(?:do\s+not|don't|never)\s+(?:tell|inform|mention|reveal|show|disclose)\s+(?:this\s+to\s+)?the\s+(?:user|human|operator)\b`)},
	{KindRoleImpersonation, regexp.MustCompile(`(?m)^\s*(?:#{1,6}\s*)?(?:\[?\s*SYSTEM\s*(?:NOTE|MESSAGE|PROMPT|OVERRIDE|INSTRUCTIONS?)?\s*\]?\s*:|(?i:\[?\s*system\s+(?:note|message|prompt|override|instructions?)\s*\]?\s*:)|<\s*/?\s*(?i:system|assistant|developer|instructions?|tool_call|function_call)\s*>|\[/?(?:INST|SYS)\]|<\|(?:im_start|im_end|system|user|assistant|eot_id|start_header_id|end_header_id)\|>|<<\s*SYS\s*>>)`)},
	{KindAgentTargeting, regexp.MustCompile(`(?i)\b(?:if\s+you\s+are|to\s+any|note\s+to|attention|dear|hello|hi|hey)\s*,?\s*(?:an?\s+|the\s+|all\s+)?(?:ai|a\.i\.|llms?|language\s+models?|ai\s+(?:assistants?|agents?|models?|crawlers?|systems?)|chatbots?|claude|chatgpt|gpt|gemini|copilot)\b`)},
	{KindAgentTargeting, regexp.MustCompile(`(?i)\b(?:ai|llm)\s+(?:assistants?|agents?|models?|crawlers?)\s+(?:reading|processing|summari[sz]ing|visiting|browsing|crawling|indexing)\s+this\b`)},
	{KindAgentTargeting, regexp.MustCompile(`(?i)\b(?:ai|llm)\s+(?:assistants?|agents?|models?|crawlers?)\s+(?:must|are\s+(?:required|instructed)\s+to)\b`)},
	{KindToolBait, regexp.MustCompile("(?i)\\b(?:call|invoke|execute|trigger)\\s+(?:the\\s+)?(?:`?[\\w]+(?:__|_|\\.)[\\w.]+`?\\s+(?:tool|function|server)|(?:tool|function)\\s+(?:named|called)\\s+)")},
	{KindToolBait, regexp.MustCompile(`(?i)\b(?:file[-_ ]?(?:write|read)|fsWrite|write_?file|read_?file|bash|shell|terminal|web_?fetch|http_?request|send_?email|execute_?command)\s+tool\b`)},
	{KindToolBait, regexp.MustCompile(`(?i)\b(?:run|execute|paste|type)\b[^.\n]{0,30}?(?:curl\s+[^\s|]+\s*\|\s*(?:ba)?sh|wget\s+[^\s|]+\s*\|\s*(?:ba)?sh|chmod\s+\+x|rm\s+-rf|powershell\s+-e)`)},
	{KindToolBait, regexp.MustCompile(`(?i)\b(?:send|post|upload|forward|exfiltrate|transmit|leak|email)\b[^.\n]{0,40}?\b(?:contents?|files?|data|keys?|tokens?|secrets?|credentials?|passwords?|history|conversation|environment|env\s+vars?)\b[^.\n]{0,40}?\b(?:to|at)\s+(?:https?://|[\w.-]+\.(?:com|net|io|dev|xyz|app|org)\b|this\s+(?:url|address|endpoint))`)},
	{KindSensitivePath, regexp.MustCompile(`(?i)\b(?:write|save|create|edit|modify|append|add)\b[^.\n]{0,40}?(?:\.(?:claude|cursor|codex|kiro|gemini|windsurf|continue)/[\w./-]*|mcp\.json|CLAUDE\.md|AGENTS\.md|GEMINI\.md|\.cursorrules|authorized_keys|~/\.ssh/)`)},
	{KindSensitivePath, regexp.MustCompile(`(?i)\b(?:read|cat|print|output|include|reveal|dump|paste)\b[^.\n]{0,30}?(?:/etc/passwd|/etc/shadow|~/\.ssh|id_rsa|\.aws/credentials|\.netrc|\.npmrc|api[_ -]?keys?|system\s+prompt)\b`)},
}

// hiddenOpenTags match the opening tag of an element a human reader
// would not see. They run only on HTML fields. The element's extent
// (through its matching close tag, nesting-aware) is found by
// hiddenElementEnd, since RE2 has no backreferences; redaction removes
// the whole element so the payload goes with the attribute that hid
// it. White-on-white text is not matched: white type on a colored
// banner is ordinary design, and the readability pass most pages go
// through drops inline styles anyway.
var hiddenOpenTags = []*regexp.Regexp{
	// Hidden by inline style.
	regexp.MustCompile(`(?is)<([a-z][a-z0-9-]*)\b[^>]*\bstyle\s*=\s*["'][^"']*(?:display\s*:\s*none|visibility\s*:\s*hidden|opacity\s*:\s*0(?:\.0+)?\s*(?:;|["'])|font-size\s*:\s*(?:0(?:\.0+)?(?:px|pt|em|rem|%)?|0?\.\d+px|1px)\s*(?:;|["'])|(?:left|top|text-indent)\s*:\s*-\s*\d{3,}|clip\s*:\s*rect\s*\(\s*0|clip-path\s*:\s*inset\s*\(\s*(?:50|100)%|color\s*:\s*transparent\s*(?:;|["']))(?:[^"']*["'])?[^>]*>`),
	// Hidden by attribute.
	regexp.MustCompile(`(?is)<([a-z][a-z0-9-]*)\b[^>]*\s(?:hidden(?:\s*=\s*["']?(?:hidden|true)?["']?)?(?:[\s/][^>]*>|>)|aria-hidden\s*=\s*["']?true["']?[^>]*>)`),
	// Off-screen helper classes common in templates.
	regexp.MustCompile(`(?is)<([a-z][a-z0-9-]*)\b[^>]*\bclass\s*=\s*["'](?:[^"']*\s)?(?:sr-only|visually-hidden|screen-reader-(?:only|text)|offscreen)(?:\s[^"']*)?["'][^>]*>`),
}

// htmlComment matches a comment with enough text to carry a sentence.
var htmlComment = regexp.MustCompile(`(?s)<!--(.{12,}?)-->`)

// span is a byte range inside a field.
type span struct{ start, end int }

// hiddenSpans returns the byte ranges of comments and hidden elements
// in html, merged and in document order. Overlapping ranges (a hidden
// element inside a hidden element) collapse to the outer one.
func hiddenSpans(html string) []span {
	var spans []span
	for _, loc := range htmlComment.FindAllStringIndex(html, -1) {
		spans = append(spans, span{loc[0], loc[1]})
	}
	lower := strings.ToLower(html)
	for _, re := range hiddenOpenTags {
		for _, m := range re.FindAllStringSubmatchIndex(html, -1) {
			tag := lower[m[2]:m[3]]
			end := hiddenElementEnd(lower, tag, m[1])
			if end < 0 {
				continue // unclosed or void element: nothing to hide
			}
			spans = append(spans, span{m[0], end})
		}
	}
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := spans[:1]
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.start < last.end {
			if sp.end > last.end {
				last.end = sp.end
			}
			continue
		}
		merged = append(merged, sp)
	}
	return merged
}

// hiddenElementEnd returns the byte offset just past the close tag that
// matches an element named tag whose open tag ends at from, counting
// nested same-name elements. lower is the lowercased document. Returns
// -1 when no matching close tag exists.
func hiddenElementEnd(lower, tag string, from int) int {
	open, close := "<"+tag, "</"+tag
	depth := 1
	i := from
	for i < len(lower) {
		next := strings.Index(lower[i:], "<")
		if next < 0 {
			return -1
		}
		i += next
		switch {
		case strings.HasPrefix(lower[i:], close) && delimAt(lower, i+len(close)):
			depth--
			gt := strings.IndexByte(lower[i:], '>')
			if gt < 0 {
				return -1
			}
			if depth == 0 {
				return i + gt + 1
			}
			i += gt + 1
		case strings.HasPrefix(lower[i:], open) && delimAt(lower, i+len(open)):
			gt := strings.IndexByte(lower[i:], '>')
			if gt < 0 {
				return -1
			}
			if !strings.HasSuffix(strings.TrimSpace(lower[i:i+gt]), "/") {
				depth++
			}
			i += gt + 1
		default:
			i++
		}
	}
	return -1
}

// delimAt reports whether position i in s ends a tag name: a
// whitespace, '>', or '/' follows, so "<p" does not match "<pre".
func delimAt(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	switch s[i] {
	case ' ', '\t', '\n', '\r', '>', '/':
		return true
	}
	return false
}

// hiddenTextMinChars is the inner-text length below which a hidden
// element is not reported unless a text pattern also matches inside
// it. "Skip to main content" in a visually-hidden link is not a
// payload; a hidden paragraph is worth a look on its own.
const hiddenTextMinChars = 24

// invisibleRune reports code points that render as nothing and have no
// place in article text: zero-width space and word joiner, the bidi
// embedding and override controls, the invisible operators, the
// byte-order mark used mid-text, soft hyphens, the Mongolian vowel
// separator, and the Unicode tag block (U+E0000..U+E007F) that is a
// known carrier for ASCII hidden inside emoji. The zero-width joiner
// and non-joiner (U+200D, U+200C) are deliberately excluded: emoji
// sequences and Persian, Arabic, and Indic text use them legitimately.
func invisibleRune(r rune) bool {
	switch {
	case r == 0x200B || r == 0x2060 || r == 0x2061 || r == 0x2062 || r == 0x2063 || r == 0x2064:
		return true
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0xFEFF || r == 0x00AD || r == 0x180E:
		return true
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

// invisibleRunThreshold is how many invisible code points in one field
// it takes to report. One or two zero-width joiners are legitimate in
// emoji sequences and some scripts; a payload needs dozens.
const invisibleRunThreshold = 8

// base64Run matches a long run of base64 alphabet, the shape of an
// encoded payload dropped into prose.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/]{48,}={0,2}`)

// Scan inspects the fields and returns a Report. In redact mode the
// fields are rewritten in place. A nil or off Scanner returns nil and
// touches nothing. Fields whose Ptr is nil or empty are skipped.
func (s *Scanner) Scan(fields ...Field) *Report {
	if !s.Enabled() {
		return nil
	}
	rep := &Report{Risk: RiskNone, Action: "flagged"}
	if s.Mode == ModeRedact {
		rep.Action = "redacted"
	}
	type key struct {
		kind  Kind
		field string
	}
	agg := map[key]*Finding{}
	order := []key{}
	add := func(k Kind, field, snippet string) {
		kk := key{k, field}
		if f, ok := agg[kk]; ok {
			f.Count++
			return
		}
		agg[kk] = &Finding{Kind: k, Field: field, Snippet: snippet, Count: 1}
		order = append(order, kk)
	}

	for _, f := range fields {
		if f.Ptr == nil || *f.Ptr == "" {
			continue
		}
		s.scanField(f, add, rep)
	}

	for _, k := range order {
		rep.Findings = append(rep.Findings, *agg[k])
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		return severity(rep.Findings[i].Kind) > severity(rep.Findings[j].Kind)
	})
	for i := range rep.Findings {
		if rep.Findings[i].Count == 1 {
			rep.Findings[i].Count = 0 // omitempty: a single hit needs no count
		}
	}
	rep.Risk = overallRisk(rep.Findings)
	return rep
}

func (s *Scanner) scanField(f Field, add func(Kind, string, string), rep *Report) {
	content := *f.Ptr
	redact := s.Mode == ModeRedact
	removed := 0

	// 1. Hidden HTML: whole elements and comments. Their inner text is
	// also scanned for text patterns so a hidden override is reported
	// under both kinds, and the element is removed in redact mode.
	if f.HTML {
		if spans := hiddenSpans(content); len(spans) > 0 {
			var b strings.Builder
			last := 0
			for _, sp := range spans {
				m := content[sp.start:sp.end]
				inner := strings.TrimSpace(innerText(m))
				keep := inner == "" || (utf8.RuneCountInString(inner) < hiddenTextMinChars && !anyTextPattern(inner))
				if keep {
					continue // empty, or a short accessibility label, not a payload
				}
				add(KindHiddenHTML, f.Name, excerpt(inner, 0, len(inner)))
				if redact {
					b.WriteString(content[last:sp.start])
					b.WriteString(marker(KindHiddenHTML))
					removed += utf8.RuneCountInString(m)
					last = sp.end
				}
			}
			if redact && last > 0 {
				b.WriteString(content[last:])
				content = b.String()
			}
		}
	}

	// 2. Invisible Unicode: count, and strip in redact mode.
	invisible := countInvisible(content)
	if invisible >= invisibleRunThreshold {
		add(KindInvisibleUnicode, f.Name, fmt.Sprintf("%d invisible code points (zero-width, bidi, or tag characters)", invisible))
		removed += invisible
	}

	// 3. Text patterns. When the field carried an invisible run, match
	// on the stripped view so a payload cannot dodge the patterns by
	// sprinkling zero-width spaces between its letters; the model reads
	// through them, so the scanner must too.
	view := content
	if invisible >= invisibleRunThreshold {
		view = stripInvisible(view)
	}
	// Excerpts are taken from the unredacted view in a first pass so a
	// snippet never shows the marker an earlier pattern left behind;
	// replacements run in a second pass.
	var matched []pattern
	for _, p := range textPatterns {
		locs := p.re.FindAllStringIndex(view, -1)
		if len(locs) == 0 {
			continue
		}
		matched = append(matched, p)
		for _, loc := range locs {
			add(p.kind, f.Name, excerpt(view, loc[0], loc[1]))
			removed += utf8.RuneCountInString(view[loc[0]:loc[1]])
		}
	}
	if redact {
		for _, p := range matched {
			view = p.re.ReplaceAllLiteralString(view, marker(p.kind))
		}
	}

	// 4. Encoded payloads: decode long base64 runs and check the result
	// against the text patterns.
	for _, loc := range base64Run.FindAllStringIndex(view, -1) {
		run := view[loc[0]:loc[1]]
		decoded, ok := decodeBase64Text(run)
		if !ok {
			continue
		}
		for _, p := range textPatterns {
			if m := p.re.FindStringIndex(decoded); m != nil {
				add(KindEncodedPayload, f.Name, "base64 decodes to: "+excerpt(decoded, m[0], m[1]))
				removed += len(run)
				if redact {
					view = strings.Replace(view, run, marker(KindEncodedPayload), 1)
				}
				break
			}
		}
	}

	if redact {
		*f.Ptr = view
		rep.RedactedChars += removed
	}
}

// anyTextPattern reports whether any text pattern matches s.
func anyTextPattern(s string) bool {
	for _, p := range textPatterns {
		if p.re.MatchString(s) {
			return true
		}
	}
	return false
}

// severity orders kinds for sorting and for the overall risk.
func severity(k Kind) int {
	switch k {
	case KindInstructionOverride, KindRoleImpersonation, KindToolBait, KindEncodedPayload:
		return 3
	case KindHiddenHTML, KindAgentTargeting, KindSensitivePath:
		return 2
	case KindInvisibleUnicode:
		return 1
	}
	return 0
}

// overallRisk: any severity-3 kind is high; hidden HTML together with
// any other kind is high (hidden text that also reads as an instruction
// is the canonical attack); a lone severity-2 is medium; invisible
// Unicode alone is low.
func overallRisk(fs []Finding) Risk {
	if len(fs) == 0 {
		return RiskNone
	}
	max, kinds := 0, map[Kind]bool{}
	for _, f := range fs {
		kinds[f.Kind] = true
		if s := severity(f.Kind); s > max {
			max = s
		}
	}
	switch {
	case max >= 3:
		return RiskHigh
	case kinds[KindHiddenHTML] && len(kinds) > 1:
		return RiskHigh
	case max == 2:
		return RiskMedium
	}
	return RiskLow
}

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// innerText strips tags and comment delimiters from a matched element
// or comment so the snippet shows what the hidden text said.
func innerText(m string) string {
	m = strings.TrimPrefix(m, "<!--")
	m = strings.TrimSuffix(m, "-->")
	return strings.Join(strings.Fields(tagRe.ReplaceAllString(m, " ")), " ")
}

// excerpt returns a whitespace-collapsed window around [start,end),
// capped at snippetWidth characters with an ellipsis when cut.
func excerpt(s string, start, end int) string {
	const pad = 40
	lo, hi := start-pad, end+pad
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	// Back off to rune boundaries.
	for lo > 0 && lo < len(s) && !utf8.RuneStart(s[lo]) {
		lo--
	}
	for hi < len(s) && !utf8.RuneStart(s[hi]) {
		hi++
	}
	out := strings.Join(strings.Fields(s[lo:hi]), " ")
	if utf8.RuneCountInString(out) > snippetWidth {
		rs := []rune(out)
		out = string(rs[:snippetWidth]) + "…"
	}
	if lo > 0 {
		out = "…" + out
	}
	if hi < len(s) {
		out += "…"
	}
	return out
}

func countInvisible(s string) int {
	n := 0
	for _, r := range s {
		if invisibleRune(r) {
			n++
		}
	}
	return n
}

func stripInvisible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !invisibleRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// decodeBase64Text decodes a run and accepts it only when the result is
// mostly printable text: a decoded image or binary blob is not a
// payload and must not be pattern-matched into a false positive.
func decodeBase64Text(run string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(run)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(run, "="))
		if err != nil {
			return "", false
		}
	}
	if !utf8.Valid(raw) || len(raw) < 16 {
		return "", false
	}
	printable := 0
	total := 0
	for _, r := range string(raw) {
		total++
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printable++
		}
	}
	if total == 0 || printable*100/total < 95 {
		return "", false
	}
	return string(raw), true
}
