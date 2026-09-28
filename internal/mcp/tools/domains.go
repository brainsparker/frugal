package tools

import (
	"fmt"
	"log/slog"

	"github.com/frugalsh/frugal/internal/routing"
	"github.com/frugalsh/frugal/internal/search"
)

// checkDomain enforces the operator's domain policy on a URL a tool is
// about to fetch. It runs before routing, so a refused target costs
// nothing, calls no provider, and never reaches the ledger. The refusal
// is logged at Warn with the tool that asked, since a burst of refusals
// is exactly the signal an operator wants to see when an agent is being
// steered somewhere it should not go. A nil policy allows everything.
func checkDomain(p *routing.DomainPolicy, tool, rawURL string, logger *slog.Logger) error {
	err := p.Check(rawURL)
	if err == nil {
		return nil
	}
	if logger != nil {
		logger.Warn("domain policy: fetch refused", "tool", tool, "url", rawURL, "reason", err.Error())
	}
	return fmt.Errorf("%s: refused by domain policy: %w", tool, err)
}

// filterItems drops search hits whose URL the domain policy would refuse
// to fetch, so an agent is never handed a link it will then be denied.
// Returns the surviving items (the input slice is never mutated) and how
// many were dropped. Items without a URL are kept: there is nothing to
// judge and nothing to fetch. A nil policy keeps everything.
func filterItems(p *routing.DomainPolicy, items []search.Item) ([]search.Item, int) {
	if !p.Enabled() || len(items) == 0 {
		return items, 0
	}
	kept := make([]search.Item, 0, len(items))
	dropped := 0
	for _, it := range items {
		if it.URL != "" && !p.Allowed(it.URL) {
			dropped++
			continue
		}
		kept = append(kept, it)
	}
	return kept, dropped
}

// domainDropWarning is the Warnings entry search tools append when the
// domain policy removed hits, so the agent knows the list is shorter
// than the provider returned and why.
func domainDropWarning(dropped int) string {
	noun := "results"
	if dropped == 1 {
		noun = "result"
	}
	return fmt.Sprintf("domain policy dropped %d %s whose host is not fetchable under routing.domains", dropped, noun)
}
