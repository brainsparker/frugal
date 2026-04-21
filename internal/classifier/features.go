package classifier

import (
	"regexp"
	"strings"

	"github.com/frugalsh/frugal/internal/types"
)

var (
	// Require a fenced block that spans at least 3 newlines before the
	// terminator so backtick fragments in prose don't flag HasCode.
	codeBlockRe   = regexp.MustCompile("(?s)```[^`]{0,20}\n[^`]*\n[^`]*\n[^`]*```")
	codeFuncRe    = regexp.MustCompile(`(?i)\b(function|def|class|import|export|package|struct|interface|impl|fn|pub|const|let|var)\b`)
	mathLatexRe   = regexp.MustCompile(`\$[^$]+\$|\\(begin|end)\{|\\frac|\\sum|\\int|\\sqrt`)
	mathKeywordRe = regexp.MustCompile(`(?i)\b(equation|derivative|integral|matrix|eigenvalue|polynomial|theorem|proof|calculus)\b`)
)

// largeInputTokenThreshold is the point at which RequiresLargeContext trips.
// Chosen to fit comfortably below all in-tree models' context windows while
// still excluding anything that won't accept 64k+ inputs.
const largeInputTokenThreshold = 64 * 1024

// perProviderCharsPerToken is a conservative divisor applied to the raw
// character count when estimating input tokens. OpenAI BPE averages ~4 for
// English text; Anthropic and Google tokenizers emit more fine-grained pieces,
// so we round down to 3.3 (1/0.3) to avoid under-estimating and selecting a
// model whose context window doesn't actually fit.
const anthropicCharsPerToken = 3.3

func extractFeatures(req *types.ChatCompletionRequest) types.QueryFeatures {
	var f types.QueryFeatures

	allText := concatenateMessages(req.Messages)

	// Token estimation with a conservative lower bound. Classifier routes on
	// the higher of two estimates so we never pick a model whose context
	// window is tighter than the actual tokenized input would produce.
	charsPerTokenUpper := 4.0
	est := int(float64(len(allText)) / anthropicCharsPerToken)
	if v := len(allText) / int(charsPerTokenUpper); v > est {
		est = v
	}
	if est < 1 {
		est = 1
	}
	f.EstimatedInputTokens = est
	f.EstimatedOutputTokens = estimateOutputTokens(req)

	// System prompt analysis
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			f.HasSystemPrompt = true
			f.SystemPromptLength = len(msg.ContentText())
			break
		}
	}

	// Content analysis
	f.HasCode = codeBlockRe.MatchString(allText) || codeFuncRe.MatchString(allText)
	f.HasMath = mathLatexRe.MatchString(allText) || mathKeywordRe.MatchString(allText)

	// Conversation turns (count user messages)
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			f.ConversationTurns++
		}
	}

	// Output format requirements
	f.RequiresJSON = req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object"
	f.RequiresToolUse = len(req.Tools) > 0
	f.RequiresMultipleCompletions = req.N != nil && *req.N > 1

	// Vision: any message carrying non-text content (image_url, input_audio)
	// forces the router to only consider vision-capable models.
	for _, msg := range req.Messages {
		if msg.HasNonTextContent() {
			f.RequiresVision = true
			break
		}
	}

	// Domain hints
	f.DomainHints = detectDomains(allText)

	// Composite complexity score
	f.ComplexityScore = computeComplexity(f)

	return f
}

func concatenateMessages(msgs []types.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.ContentText())
		b.WriteByte(' ')
	}
	return b.String()
}

func estimateOutputTokens(req *types.ChatCompletionRequest) int {
	if req.MaxTokens != nil {
		return *req.MaxTokens
	}
	// Default estimate based on input size
	inputChars := 0
	for _, m := range req.Messages {
		inputChars += len(m.ContentText())
	}
	est := inputChars / 4 // rough: output ~= input length
	if est < 100 {
		return 100
	}
	if est > 4096 {
		return 4096
	}
	return est
}

// Precompiled word-boundary regexes per domain. The previous implementation
// used substring matching which flagged "import" in "important", "api" in
// "therapist", and similar false positives.
var (
	codingDomainRe   = regexp.MustCompile(`(?i)\b(code|function|bug|error|compile|debug|api|endpoint|database|sql|algorithm|programming)\b`)
	creativeDomainRe = regexp.MustCompile(`(?i)\b(story|poem|creative|imagine|fiction|essay|blog)\b|write me\b`)
	mathDomainRe     = regexp.MustCompile(`(?i)\b(calculate|solve|equation|math|formula|compute|probability|statistics)\b`)
)

// detectDomains flags the top-level topic of the query. The "analysis" domain
// was removed because its keyword set fired on nearly every request (summarize,
// explain, review, compare are boilerplate instructions), which made it
// useless as a routing signal.
func detectDomains(text string) []string {
	var hints []string
	if codingDomainRe.MatchString(text) {
		hints = append(hints, "coding")
	}
	if creativeDomainRe.MatchString(text) {
		hints = append(hints, "creative")
	}
	if mathDomainRe.MatchString(text) {
		hints = append(hints, "math")
	}
	return hints
}

func computeComplexity(f types.QueryFeatures) float64 {
	score := 0.0

	if f.HasCode {
		score += 0.20
	}
	if f.HasMath {
		score += 0.20
	}
	if f.HasSystemPrompt && f.SystemPromptLength > 500 {
		score += 0.15
	}
	if f.RequiresToolUse {
		score += 0.15
	}
	if f.RequiresJSON {
		score += 0.05
	}
	if f.EstimatedInputTokens > 4000 {
		score += 0.10
	}
	if f.ConversationTurns > 4 {
		score += 0.10
	}

	if score > 1.0 {
		return 1.0
	}
	return score
}
