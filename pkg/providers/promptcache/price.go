// Package promptcache is the prompt-caching seam: pricing, usage-shape
// normalisation, the cache-key allow-list, and the per-call accounting line.
// The functions here are stubs. They return zero values and do not price,
// parse, or log.
package promptcache

import (
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// Billing classes stored on a call and returned by Price.
const (
	BillingClassPriced       = "priced"
	BillingClassUnpriced     = "unpriced"
	BillingClassSubscription = "subscription"
)

// Names of the rule that priced a 1-hour write. Price does not set these yet.
const (
	// OneHourRuleCacheWrite1h means the catalog field cache_write_1h priced
	// the 1-hour portion.
	OneHourRuleCacheWrite1h = "cache_write_1h"
	// OneHourRuleTwoTimesInput means the portion was priced at 2 times the
	// catalog input price (Anthropic's documented multiplier).
	OneHourRuleTwoTimesInput = "2x catalog input"
	// OneHourRuleUnpriced means the provider documents no 1-hour multiplier
	// and cache_write_1h is absent, so that portion stays unpriced.
	OneHourRuleUnpriced = "unpriced"
)

// PriceInput is the catalog price, the normalised usage, and the billing
// class for one call. BillingClass is "subscription" for a ChatGPT sign-in
// call. Empty means the call is not a subscription and Price classifies it
// from the catalog price.
type PriceInput struct {
	ProviderID   string
	BillingClass string
	Cost         *catalog.ModelCost
	Usage        protocoltypes.UsageInfo
}

// PriceResult is the money result of one call. Cost is the priced portions
// only. UnpricedTokens is the unpriced portion, not the whole call.
// SubscriptionTokens is set for a subscription call and Cost stays 0 for
// those tokens. OneHourRule names which rule priced the 1-hour write.
type PriceResult struct {
	Cost               float64
	UnpricedTokens     int
	SubscriptionTokens int
	BillingClass       string
	OneHourRule        string
}

// Price returns the cost of one call. The skeleton returns the zero result.
func Price(in PriceInput) PriceResult {
	_ = in
	return PriceResult{}
}
