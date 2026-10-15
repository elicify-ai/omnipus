package promptcache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// ParseUsage preserves the distinction between absent usage and a known zero
// split. No provider text or unknown extension survives normalization.
func ParseUsage(providerID string, raw []byte) (*protocoltypes.UsageInfo, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil //nolint:nilnil // A missing usage object is distinct from a known zero split.
	}
	u, err := NormalizeUsage(providerID, raw)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// NormalizeUsage reads a documented usage object into additive token counts.
// Repeated aliases are alternatives, never separate contributions. Unknown
// fields and non-integer, negative or overflowing counters are ignored.
func NormalizeUsage(providerID string, raw []byte) (protocoltypes.UsageInfo, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return protocoltypes.UsageInfo{}, fmt.Errorf("decode provider usage: %w", err)
	}
	count := func(path ...string) int { return usageCount(fields, path...) }
	var u protocoltypes.UsageInfo
	u.CompletionTokens = count("completion_tokens")
	if _, ok := fields["output_tokens"]; ok {
		u.CompletionTokens = count("output_tokens")
	}
	// Anthropic's input excludes both reads and writes. Alibaba's chat
	// object also has a top-level creation field: the nested chat shape wins.
	_, chat := fields["prompt_tokens"]
	_, anthropicRead := fields["cache_read_input_tokens"]
	_, anthropicWrite := fields["cache_creation_input_tokens"]
	if (anthropicRead && providerID != "fireworks-ai") || (!chat && (anthropicWrite || providerID == "anthropic")) {
		u.PromptTokens = count("input_tokens")
		if chat {
			// Some gateways forward Anthropic's exclusive counters in a
			// chat-shaped response; their inclusion rule is not documented.
			u.PromptTokens = count("prompt_tokens")
		}
		u.CacheReadTokens = count("cache_read_input_tokens")
		u.CacheWriteTokens = count("cache_creation_input_tokens")
		u.CacheWrite5mTokens = count("cache_creation", "ephemeral_5m_input_tokens")
		u.CacheWrite1hTokens = count("cache_creation", "ephemeral_1h_input_tokens")
		if u.CacheWrite5mTokens > 0 || u.CacheWrite1hTokens > 0 {
			u.CacheWriteTokens = u.CacheWrite5mTokens + u.CacheWrite1hTokens
		}
	} else {
		u.PromptTokens = count("prompt_tokens")
		details := "prompt_tokens_details"
		if _, ok := fields["input_tokens"]; ok {
			u.PromptTokens = count("input_tokens")
			details = "input_tokens_details"
		}
		u.CacheReadTokens = count(details, "cached_tokens")
		if providerID != "fireworks-ai" {
			if u.CacheReadTokens == 0 {
				u.CacheReadTokens = count("prompt_cache_hit_tokens")
			}
			if u.CacheReadTokens == 0 {
				u.CacheReadTokens = count("cached_tokens")
			}
			u.CacheWriteTokens = count(details, "cache_write_tokens")
			if nestedWrite := count(details, "cache_creation_input_tokens"); nestedWrite > 0 || providerID == "alibaba" || providerID == "alibaba-cn" {
				// The same documented nested shape can arrive on a coding-plan
				// or router row; its count does not depend on the catalog alias.
				u.CacheWriteTokens = nestedWrite
			}
			if u.CacheWriteTokens == 0 && providerID != "alibaba" && providerID != "alibaba-cn" {
				u.CacheWriteTokens = count("cache_write_tokens")
			}
		}
		// DeepInfra's write field belongs to a retention mode we never send.
		if providerID == "deepinfra" {
			u.CacheWriteTokens = 0
		}
		// Gemini's native/SDK examples do not establish inclusion. Retain
		// the reported prompt rather than inventing a subtraction rule.
		geminiRead := count("total_cached_tokens")
		if geminiRead == 0 {
			geminiRead = count("cachedContentTokenCount")
		}
		if geminiRead > 0 && providerID != "fireworks-ai" {
			u.CacheReadTokens = geminiRead
		} else if providerID != "google" {
			u.PromptTokens = max(0, u.PromptTokens-u.CacheReadTokens-u.CacheWriteTokens)
		}
	}
	u.TotalTokens = u.PromptTokens + u.CacheReadTokens + u.CacheWriteTokens + u.CompletionTokens
	return u, nil
}

func usageCount(fields map[string]json.RawMessage, path ...string) int {
	for i, key := range path {
		raw := fields[key]
		if i == len(path)-1 {
			n, err := strconv.Atoi(string(raw))
			if err != nil || n < 0 {
				return 0
			}
			return n
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) != nil {
			return 0
		}
		fields = nested
	}
	return 0
}
