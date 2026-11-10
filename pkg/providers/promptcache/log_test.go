package promptcache

import (
	"encoding/json"
	"fmt"
	"strconv"
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

// TestLogLine_DropsTextInsideUsage is TDD row 28. The raw usage object is the
// OpenAI chat shape (prompt total 1000, of which 800 were read, so uncached
// prompt is 200) plus private text, a long string, a nested object and array,
// and 33 numeric extension fields. Unknown fields are dropped. At most 32
// counters are written.
func TestLogLine_DropsTextInsideUsage(t *testing.T) {
	const (
		privateText  = "SECRET-PROMPT-DO-NOT-LOG"
		nestedSecret = "NESTED-SECRET"
		arraySecret  = "ARRAY-SECRET"
		deepSecret   = "DEEP-SECRET"
	)
	longNote := strings.Repeat("PRIVATE-LONG-NOTE ", 16)
	rawMap := map[string]any{
		"prompt_tokens":     1000,
		"completion_tokens": 10,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 800,
		},
		"private_prompt_text": privateText,
		"long_note":           longNote,
		"nested": map[string]any{
			"secret": nestedSecret,
			"items":  []any{arraySecret, map[string]any{"deep": deepSecret}},
		},
	}
	extVals := make([]int, 0, 33)
	for i := 1; i <= 33; i++ {
		v := 91000 + i
		rawMap[fmt.Sprintf("ext_%02d", i)] = v
		extVals = append(extVals, v)
	}
	raw, err := json.Marshal(rawMap)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := NormalizeUsage("openai", raw)
	if err != nil {
		t.Fatalf("NormalizeUsage: %v", err)
	}
	if usage.PromptTokens != 200 || usage.CacheReadTokens != 800 || usage.CacheWriteTokens != 0 || usage.CompletionTokens != 10 {
		t.Errorf("normalized = %+v, want uncached 200, read 800, write 0, output 10", usage)
	}
	line := FormatCallAccounting(CallAccounting{
		ProviderID:   "openai",
		ModelID:      "gpt-4o",
		AgentID:      "mia",
		SessionID:    "sess-28",
		CallID:       "call-28",
		BillingClass: BillingClassPriced,
		Cost:         0.0204,
		Usage:        usage,
	})
	if line == "" || strings.Contains(line, "\n") {
		t.Fatalf("accounting line = %q, want one info line", line)
	}
	for _, want := range []string{"openai", "gpt-4o", "mia", "sess-28", "call-28", "200", "800", "0.0204"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q does not contain %q", line, want)
		}
	}
	for _, secret := range []string{privateText, longNote, nestedSecret, arraySecret, deepSecret, "private_prompt_text", "ext_01", "long_note"} {
		if strings.Contains(line, secret) {
			t.Errorf("line contains %q", secret)
		}
	}
	present := 0
	for _, v := range extVals {
		if strings.Contains(line, strconv.Itoa(v)) {
			present++
		}
	}
	if present > 32 {
		t.Errorf("%d extension counters in the line, want at most 32", present)
	}
}
