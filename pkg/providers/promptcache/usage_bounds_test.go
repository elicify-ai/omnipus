package promptcache

// Proof candidate for D20's bounded numeric counters and the additive,
// non-negative usage convention (FR-011; ModelTokens/SessionStats schemas).
// No production mutation. qa-lead owns any test that lands.

import (
	"fmt"
	"testing"
)

func TestSecurityUsageBounds_SplitAdditionCannotBecomeNegative(t *testing.T) {
	positive, err := NormalizeUsage("anthropic", []byte(`{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":4,"cache_creation_input_tokens":11,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":6}}`))
	if err != nil || positive.PromptTokens != 2 || positive.CompletionTokens != 3 || positive.CacheReadTokens != 4 || positive.CacheWriteTokens != 11 || positive.CacheWrite5mTokens != 5 || positive.CacheWrite1hTokens != 6 || positive.TotalTokens != 20 {
		t.Fatalf("positive control: got %+v, error=%v; want exact disjoint split 2/3/4/11 with writes 5+6 and total 20", positive, err)
	}

	maxInt := int(^uint(0) >> 1)
	raw := []byte(fmt.Sprintf(`{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":1}}`, maxInt))
	usage, err := NormalizeUsage("anthropic", raw)
	if err != nil {
		return // Refusing an unrepresentable aggregate is safe; no policy for its error wording is invented here.
	}
	if usage.CacheWriteTokens < 0 || usage.TotalTokens < 0 {
		t.Fatalf("accepted non-negative numeric fields became negative accounting: cache_write=%d total=%d; no negative token count may escape normalization", usage.CacheWriteTokens, usage.TotalTokens)
	}
}
