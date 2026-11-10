// Package promptcache normalizes provider usage, prices it from the catalog,
// and defines the documented cache transports and content-free accounting line.
package promptcache

import (
	"math"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

const (
	BillingClassPriced       = "priced"
	BillingClassUnpriced     = "unpriced"
	BillingClassSubscription = "subscription"
	OneHourRuleCacheWrite1h  = "cache_write_1h"
	OneHourRuleTwoTimesInput = "2x catalog input"
	OneHourRuleUnpriced      = "unpriced"
)

type PriceInput struct {
	ProviderID   string
	BillingClass string
	Cost         *catalog.ModelCost
	Usage        protocoltypes.UsageInfo
}

type PriceResult struct {
	Cost               float64
	UnpricedTokens     int
	SubscriptionTokens int
	BillingClass       string
	OneHourRule        string
}

// Price prices disjoint token portions, never treating an absent price as free.
// Prices are USD per million; a published zero remains a priced zero.
func Price(in PriceInput) PriceResult {
	u := in.Usage
	total := u.PromptTokens + u.CacheReadTokens + u.CacheWriteTokens + u.CompletionTokens
	if in.BillingClass == BillingClassSubscription || in.ProviderID == "openai-chatgpt" {
		return PriceResult{BillingClass: BillingClassSubscription, SubscriptionTokens: total}
	}
	cost := contextCost(in.Cost, u.PromptTokens+u.CacheReadTokens+u.CacheWriteTokens)
	r := PriceResult{BillingClass: BillingClassUnpriced}
	if u.CacheWrite1hTokens > 0 {
		r.OneHourRule = OneHourRuleUnpriced
	}
	if cost == nil || !validPrice(cost.Input) {
		r.UnpricedTokens = total
		return r
	}
	r.BillingClass = BillingClassPriced
	add := func(tokens int, rate *float64) {
		if tokens <= 0 {
			return
		}
		if !validPrice(rate) {
			r.UnpricedTokens += tokens
			return
		}
		r.Cost += float64(tokens) * *rate / 1e6
	}
	add(u.PromptTokens, cost.Input)
	add(u.CompletionTokens, cost.Output)
	add(u.CacheReadTokens, priceOrInput(cost.CacheRead, cost.Input))
	oneHour := u.CacheWrite1hTokens
	add(max(0, u.CacheWriteTokens-oneHour), priceOrInput(cost.CacheWrite, cost.Input))
	if oneHour > 0 {
		switch {
		case validPrice(cost.CacheWrite1h):
			r.OneHourRule = OneHourRuleCacheWrite1h
			add(oneHour, cost.CacheWrite1h)
		case in.ProviderID == "anthropic":
			r.OneHourRule = OneHourRuleTwoTimesInput
			rate := 2 * *cost.Input
			add(oneHour, &rate)
		default:
			r.OneHourRule = OneHourRuleUnpriced
			r.UnpricedTokens += oneHour
		}
	}
	if total > 0 && r.UnpricedTokens == total {
		r.BillingClass = BillingClassUnpriced
	}
	return r
}

func validPrice(rate *float64) bool {
	return rate != nil && *rate >= 0 && !math.IsInf(*rate, 0) && !math.IsNaN(*rate)
}

func priceOrInput(rate, input *float64) *float64 {
	if rate == nil {
		return input
	}
	return rate
}

// contextCost chooses the highest matching canonical context tier. The legacy
// alias is consulted only in the absence of canonical context tiers.
func contextCost(base *catalog.ModelCost, input int) *catalog.ModelCost {
	if base == nil {
		return nil
	}
	selected := *base
	boundary := -1
	canonical := false
	for _, t := range base.Tiers {
		if t.Tier.Type != "context" {
			continue
		}
		canonical = true
		matches := input > t.Tier.Size || (t.Tier.Boundary == catalog.CostBoundaryAtLeast && input == t.Tier.Size)
		if !matches || t.Tier.Size < boundary {
			continue
		}
		boundary = t.Tier.Size
		// A tier is a complete band except for prices the source leaves
		// absent. Cache fallback uses THIS band's input, not the base rate.
		selected = catalog.ModelCost{Input: t.Input, Output: t.Output, Reasoning: t.Reasoning, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, CacheWrite1h: t.CacheWrite1h}
	}
	if !canonical && input > 200000 && base.ContextOver200k != nil {
		selected = *base.ContextOver200k
	}
	return &selected
}
