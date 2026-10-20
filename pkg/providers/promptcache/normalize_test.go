package promptcache

import (
	"os"
	"path/filepath"
	"testing"
)

// shapeExpect is one row of the spec's "Each published usage shape" Examples
// table. Counts are copied from that table. Output is the fixture's own
// completion count (the table does not fix it). Total is uncached + read +
// write + output, the table's total rule.
type shapeExpect struct {
	name     string
	provider string
	file     string
	uncached int
	read     int
	write    int
	write5m  int
	write1h  int
	output   int
}

func publishedShapes() []shapeExpect {
	const out = 10
	return []shapeExpect{
		{"Anthropic exclusive input", "anthropic", "anthropic-exclusive.json", 100, 800, 50, 0, 0, out},
		{"Anthropic 5-minute and 1-hour writes", "anthropic", "anthropic-split-writes.json", 100, 0, 100, 40, 60, out},
		{"OpenAI chat details, read inside prompt total", "openai", "openai-chat.json", 200, 800, 0, 0, 0, out},
		{"OpenAI responses details, read and write inside input total", "openai", "openai-responses.json", 100, 700, 200, 0, 0, out},
		{"DeepSeek hit and miss, nested cached tokens repeating the hit", "deepseek", "deepseek-hit-miss.json", 300, 700, 0, 0, 0, out},
		{"Moonshot chat, read and write inside prompt total", "moonshotai", "moonshot-chat.json", 100, 800, 100, 0, 0, out},
		{"Alibaba chat, nested usage.prompt_tokens_details", "alibaba", "alibaba-nested.json", 100, 800, 100, 0, 0, out},
		{"Fireworks, only usage.prompt_tokens_details.cached_tokens", "fireworks-ai", "fireworks.json", 100, 900, 0, 0, 0, out},
		{"Together top-level cached tokens repeating the nested field", "togetherai", "together.json", 100, 900, 0, 0, 0, out},
		// Include rule is not stated. Do not subtract 400 from the reported 1000.
		{"Gemini total cached tokens field, include rule unknown", "google", "gemini-total-cached.json", 1000, 400, 0, 0, 0, out},
		{"Ollama cached count inside prompt total", "ollama", "ollama.json", 100, 50, 0, 0, 0, out},
		{"Missing cache fields", "openai", "missing-cache-fields.json", 250, 0, 0, 0, 0, out},
	}
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// TestUsageShapes_Normalize is TDD row 1. Expected counts are the Examples
// table, not a parser's output.
func TestUsageShapes_Normalize(t *testing.T) {
	for _, tc := range publishedShapes() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeUsage(tc.provider, loadFixture(t, tc.file))
			if err != nil {
				t.Fatalf("NormalizeUsage(%q) error = %v, want nil", tc.provider, err)
			}
			if got.PromptTokens != tc.uncached {
				t.Errorf("uncached prompt = %d, want %d", got.PromptTokens, tc.uncached)
			}
			if got.CacheReadTokens != tc.read {
				t.Errorf("cache read = %d, want %d", got.CacheReadTokens, tc.read)
			}
			if got.CacheWriteTokens != tc.write {
				t.Errorf("cache write = %d, want %d", got.CacheWriteTokens, tc.write)
			}
			if got.CacheWrite5mTokens != tc.write5m || got.CacheWrite1hTokens != tc.write1h {
				t.Errorf("write split = %d five-minute + %d one-hour, want %d + %d",
					got.CacheWrite5mTokens, got.CacheWrite1hTokens, tc.write5m, tc.write1h)
			}
			if got.CompletionTokens != tc.output {
				t.Errorf("output = %d, want %d", got.CompletionTokens, tc.output)
			}
			wantTotal := tc.uncached + tc.read + tc.write + tc.output
			if got.TotalTokens != wantTotal {
				t.Errorf("total = %d, want uncached+read+write+output = %d", got.TotalTokens, wantTotal)
			}
		})
	}
}

// TestUsageShapes_DeepSeekHitNotDoubleCounted is TDD row 2. The hit field and
// the nested cached-token field are the same 700 tokens.
func TestUsageShapes_DeepSeekHitNotDoubleCounted(t *testing.T) {
	got, err := NormalizeUsage("deepseek", loadFixture(t, "deepseek-hit-miss.json"))
	if err != nil {
		t.Fatalf("NormalizeUsage error = %v", err)
	}
	const wantRead = 700
	if got.CacheReadTokens != wantRead {
		t.Fatalf("cache read = %d, want %d (hit and nested cached_tokens are one count, not %d)",
			got.CacheReadTokens, wantRead, wantRead*2)
	}
	if got.PromptTokens != 300 {
		t.Errorf("uncached prompt = %d, want the miss count 300", got.PromptTokens)
	}
}

// TestUsageShapes_GeminiDoesNotSubtractUntilEvidence is TDD row 3. The
// documented example does not state the include rule, so 400 is a read and
// the reported prompt 1000 stays 1000.
func TestUsageShapes_GeminiDoesNotSubtractUntilEvidence(t *testing.T) {
	got, err := NormalizeUsage("google", loadFixture(t, "gemini-total-cached.json"))
	if err != nil {
		t.Fatalf("NormalizeUsage error = %v", err)
	}
	if got.CacheReadTokens != 400 {
		t.Errorf("cache read = %d, want the reported total_cached_tokens 400", got.CacheReadTokens)
	}
	if got.PromptTokens != 1000 {
		t.Errorf("uncached prompt = %d, want the reported prompt 1000 (do not subtract the cached count)", got.PromptTokens)
	}
	if got.CacheWriteTokens != 0 {
		t.Errorf("cache write = %d, want 0", got.CacheWriteTokens)
	}
}

// TestUsageShapes_UnknownFieldDoesNotFail is TDD row 12a.
func TestUsageShapes_UnknownFieldDoesNotFail(t *testing.T) {
	raw := loadFixture(t, "unknown-field.json")
	got, err := NormalizeUsage("openai", raw)
	if err != nil {
		t.Fatalf("NormalizeUsage error = %v, want nil; an unknown usage field must not fail the call", err)
	}
	if got.CacheReadTokens != 0 || got.CacheWriteTokens != 0 {
		t.Errorf("cache read/write = %d/%d, want 0/0", got.CacheReadTokens, got.CacheWriteTokens)
	}
	if got.PromptTokens != 250 {
		t.Errorf("prompt = %d, want the provider prompt total 250", got.PromptTokens)
	}
}

// TestFireworks_StandardCachedTokensOnly is TDD row 33. perf_metrics says 7.
// The standard field says 900. Only 900 counts. The request must not ask for
// perf_metrics_in_response.
func TestFireworks_StandardCachedTokensOnly(t *testing.T) {
	got, err := NormalizeUsage("fireworks-ai", loadFixture(t, "fireworks.json"))
	if err != nil {
		t.Fatalf("NormalizeUsage error = %v", err)
	}
	if got.CacheReadTokens != 900 {
		t.Fatalf("cache read = %d, want 900 from usage.prompt_tokens_details.cached_tokens, not perf_metrics", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 0 {
		t.Errorf("cache write = %d, want 0", got.CacheWriteTokens)
	}
	if got.PromptTokens != 100 {
		t.Errorf("uncached prompt = %d, want 100", got.PromptTokens)
	}
	decision := CacheTransportFor("fireworks-ai", "")
	if !decision.SendPromptCacheKey {
		t.Error("Fireworks documents prompt_cache_key; SendPromptCacheKey = false, want true")
	}
	if decision.SendEphemeralCacheControl || decision.SessionHeader != "" || decision.SendBodySessionID {
		t.Errorf("Fireworks transport = %+v, want only prompt_cache_key (no marker, no header, no perf_metrics_in_response)", decision)
	}
}

// TestAlibaba_NestedWritePath is TDD row 34. The nested write is 100. A
// top-level cache_creation_input_tokens of 1 is the decoy a top-level-only
// parse would return.
func TestAlibaba_NestedWritePath(t *testing.T) {
	got, err := NormalizeUsage("alibaba", loadFixture(t, "alibaba-nested.json"))
	if err != nil {
		t.Fatalf("NormalizeUsage error = %v", err)
	}
	if got.CacheWriteTokens != 100 {
		t.Fatalf("cache write = %d, want 100 from usage.prompt_tokens_details.cache_creation_input_tokens, not the top-level 1", got.CacheWriteTokens)
	}
	if got.CacheReadTokens != 800 {
		t.Errorf("cache read = %d, want 800", got.CacheReadTokens)
	}
	if got.PromptTokens != 100 {
		t.Errorf("uncached prompt = %d, want 100 (prompt total includes the read and the write)", got.PromptTokens)
	}
}
