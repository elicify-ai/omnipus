package promptcache

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// CallAccounting contains application correlation ids and normalized numbers
// only. No raw provider value or extension can reach the formatter.
type CallAccounting struct {
	ProviderID     string
	ModelID        string
	AgentID        string
	SessionID      string
	CallID         string
	Usage          protocoltypes.UsageInfo
	Cost           float64
	BillingClass   string
	OneHourRule    string
	UnpricedTokens int
}

// FormatCallAccounting emits one content-free line with fewer than 32 numeric
// counters. Correlation ids are quoted to escape newlines and control bytes.
// The caller sends the result through the existing credential-scrubbing logger.
func FormatCallAccounting(in CallAccounting) string {
	u := in.Usage
	var line strings.Builder
	fmt.Fprintf(&line, "llm_usage provider=%q model=%q agent=%q session=%q call=%q uncached=%d cache_read=%d cache_write=%d output=%d total=%d",
		in.ProviderID, in.ModelID, in.AgentID, in.SessionID, in.CallID,
		max(0, u.PromptTokens), max(0, u.CacheReadTokens), max(0, u.CacheWriteTokens), max(0, u.CompletionTokens), max(0, u.TotalTokens))
	if u.CacheWrite5mTokens > 0 || u.CacheWrite1hTokens > 0 {
		fmt.Fprintf(&line, " write_5m=%d write_1h=%d", max(0, u.CacheWrite5mTokens), max(0, u.CacheWrite1hTokens))
	}
	switch in.BillingClass {
	case BillingClassSubscription:
		line.WriteString(" billing=subscription")
	case BillingClassPriced:
		fmt.Fprintf(&line, " billing=priced cost_usd=$%g", in.Cost)
	default:
		line.WriteString(" billing=unpriced")
	}
	if in.UnpricedTokens > 0 {
		fmt.Fprintf(&line, " unpriced_tokens=%d", in.UnpricedTokens)
	}
	// Only application-defined rules are eligible, never arbitrary strings.
	switch in.OneHourRule {
	case OneHourRuleCacheWrite1h, OneHourRuleTwoTimesInput, OneHourRuleUnpriced:
		fmt.Fprintf(&line, " one_hour_rule=%q", in.OneHourRule)
	}
	return line.String()
}
