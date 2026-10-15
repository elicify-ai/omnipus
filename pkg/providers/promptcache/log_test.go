package promptcache

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// TestLogLine_UsageOnly is TDD row 17. One info line, the correlation ids and
// the numeric counters, a dollar cost, and no prompt text.
func TestLogLine_UsageOnly(t *testing.T) {
	const prompt = "SECRET-PROMPT-DO-NOT-LOG"
	line := FormatCallAccounting(CallAccounting{
		ProviderID:   "anthropic",
		ModelID:      "claude-sonnet-4-5",
		AgentID:      "mia",
		SessionID:    "sess-1",
		CallID:       "call-9",
		BillingClass: BillingClassPriced,
		Cost:         0.0204,
		Usage: protocoltypes.UsageInfo{
			PromptTokens:     100,
			CacheReadTokens:  800,
			CacheWriteTokens: 50,
			CompletionTokens: 10,
		},
	})
	if line == "" {
		t.Fatal("accounting line is empty, want one info line")
	}
	for _, want := range []string{
		"anthropic", "claude-sonnet-4-5", "mia", "sess-1", "call-9",
		"100", "800", "50", "0.0204",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, prompt) {
		t.Errorf("line contains prompt text %q", prompt)
	}
	if strings.Contains(line, "{") || strings.Contains(line, "prompt_tokens") {
		t.Errorf("line contains a raw usage object: %q", line)
	}

	sub := FormatCallAccounting(CallAccounting{
		ProviderID:   "openai",
		ModelID:      "gpt-4o",
		AgentID:      "mia",
		SessionID:    "sess-sub",
		CallID:       "call-sub",
		BillingClass: BillingClassSubscription,
		Usage:        protocoltypes.UsageInfo{PromptTokens: 10, CacheReadTokens: 20},
	})
	if !strings.Contains(sub, "subscription") {
		t.Errorf("subscription line %q does not contain the word subscription", sub)
	}
	if strings.Contains(sub, "$") || strings.Contains(sub, "0.00") {
		t.Errorf("subscription line %q includes a dollar cost", sub)
	}
}

// TestLogLine_DropsTextInsideUsage is TDD row 28. Counters and the session id
// stay. Private text that arrived inside a usage payload must not.
func TestLogLine_DropsTextInsideUsage(t *testing.T) {
	const privateText = "SECRET-PROMPT-DO-NOT-LOG"
	line := FormatCallAccounting(CallAccounting{
		ProviderID:   "openai",
		ModelID:      "gpt-4o",
		AgentID:      "mia",
		SessionID:    "sess-28",
		CallID:       "call-28",
		BillingClass: BillingClassPriced,
		Cost:         0.0204,
		OneHourRule:  OneHourRuleTwoTimesInput,
		Usage: protocoltypes.UsageInfo{
			PromptTokens:       100,
			CacheReadTokens:    800,
			CacheWriteTokens:   100,
			CacheWrite5mTokens: 40,
			CacheWrite1hTokens: 60,
		},
	})
	for _, want := range []string{"sess-28", "100", "800", "40", "60", "0.0204", OneHourRuleTwoTimesInput} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, privateText) {
		t.Errorf("line contains private usage text %q", privateText)
	}
	if strings.Contains(line, "{") || strings.Contains(line, "private_prompt_text") {
		t.Errorf("line contains a raw usage extension: %q", line)
	}
}
