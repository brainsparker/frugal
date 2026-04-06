package classifier

import (
	"regexp"
	"strings"

	"github.com/frugalsh/frugal/internal/types"
)

var (
	codeBlockRe   = regexp.MustCompile("(?s)```")
	codeFuncRe    = regexp.MustCompile(`\b(function|def|class|import|export|package|struct|interface|impl|fn|pub|const|let|var)\b`)
	mathLatexRe   = regexp.MustCompile(`\$[^$]+\$|\\(begin|end)\{|\\frac|\\sum|\\int|\\sqrt`)
	mathKeywordRe = regexp.MustCompile(`\b(equation|derivative|integral|matrix|eigenvalue|polynomial|theorem|proof|calculus)\b`)
)

func extractFeatures(req *types.ChatCompletionRequest) types.QueryFeatures {
	var f types.QueryFeatures

	allText := concatenateMessages(req.Messages)

	// Token estimation (~4 chars per token)
	f.EstimatedInputTokens = len(allText) / 4
	if f.EstimatedInputTokens < 1 {
		f.EstimatedInputTokens = 1
	}
	f.EstimatedOutputTokens = estimateOutputTokens(req)

	// System prompt analysis
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			f.HasSystemPrompt = true
			f.SystemPromptLength = len(msg.ContentString())
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

	// Domain hints
	f.DomainHints = detectDomains(allText)

	// Composite complexity score
	f.ComplexityScore = computeComplexity(f)

	return f
}

func concatenateMessages(msgs []types.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.ContentString())
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
		inputChars += len(m.ContentString())
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

func detectDomains(text string) []string {
	lower := strings.ToLower(text)
	var hints []string

	codingKeywords := []string{"code", "function", "bug", "error", "compile", "debug", "api", "endpoint", "database", "sql", "algorithm", "programming"}
	creativeKeywords := []string{"story", "poem", "creative", "write me", "imagine", "fiction", "essay", "blog"}
	analysisKeywords := []string{"analyze", "compare", "evaluate", "assess", "review", "summarize", "explain"}
	mathKeywords := []string{"calculate", "solve", "equation", "math", "formula", "compute", "probability", "statistics"}

	if matchesAny(lower, codingKeywords) {
		hints = append(hints, "coding")
	}
	if matchesAny(lower, creativeKeywords) {
		hints = append(hints, "creative")
	}
	if matchesAny(lower, analysisKeywords) {
		hints = append(hints, "analysis")
	}
	if matchesAny(lower, mathKeywords) {
		hints = append(hints, "math")
	}

	return hints
}

func matchesAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
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
