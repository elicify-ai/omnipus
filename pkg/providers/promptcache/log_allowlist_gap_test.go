package promptcache

// CHECK gap-closing test (test-integrity-audit AUDIT mode, 2026-10-16).
// Written because the "log line prints an arbitrary OneHourRule string"
// mutation SURVIVED the pack: every existing test leaves OneHourRule empty or
// feeds one of the three production constants, so the formatter's allow-list
// switch was never observed rejecting a foreign string. FR-025/D20: the line
// carries only the documented numeric counters, correlation ids, and the
// application-defined one-hour rule tokens — never an arbitrary string.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func TestLogLine_OneHourRuleValueIsAllowListed(t *testing.T) {
	const rogue = "ROGUE-PROVIDER-STRING-NOT-A-RULE"
	line := FormatCallAccounting(CallAccounting{
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		AgentID:      "mia",
		SessionID:    "sess-gap",
		CallID:       "call-gap",
		BillingClass: BillingClassPriced,
		Cost:         0.0204,
		OneHourRule:  rogue, // anything that is not one of the three documented rules
		Usage: protocoltypes.UsageInfo{
			PromptTokens:       100,
			CacheReadTokens:    800,
			CacheWriteTokens:   200,
			CacheWrite5mTokens: 40,
			CacheWrite1hTokens: 60,
			CompletionTokens:   10,
			TotalTokens:        1110,
		},
	})
	if strings.Contains(line, rogue) {
		t.Fatalf("line leaked a non-allow-listed one-hour-rule string %q:\n%s", rogue, line)
	}
	if !strings.Contains(line, "write_5m=40") || !strings.Contains(line, "write_1h=60") {
		t.Fatalf("line lost the documented write split:\n%s", line)
	}

	// The three documented rules still print, so the guard is not a blanket drop.
	for _, rule := range []string{OneHourRuleCacheWrite1h, OneHourRuleTwoTimesInput, OneHourRuleUnpriced} {
		shown := FormatCallAccounting(CallAccounting{
			ProviderID: "anthropic", ModelID: "m", AgentID: "a", SessionID: "s", CallID: "c",
			BillingClass: BillingClassPriced, OneHourRule: rule,
		})
		if !strings.Contains(shown, rule) {
			t.Errorf("documented one-hour rule %q is missing from the line:\n%s", rule, shown)
		}
	}
}
