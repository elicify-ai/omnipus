package anthropicprovider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Test-only decoding of the provider request, not a gateway/SPA wire format.
// Named sections and content slices preserve Anthropic's semantic render order.
type bodyCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type cacheBodyBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	Name         string            `json:"name"`
	ID           string            `json:"id"`
	ToolUseID    string            `json:"tool_use_id"`
	Content      json.RawMessage   `json:"content"`
	CacheControl *bodyCacheControl `json:"cache_control,omitempty"`
}

type cacheBodyMessage struct {
	Role         string            `json:"role"`
	Content      []cacheBodyBlock  `json:"content"`
	CacheControl *bodyCacheControl `json:"cache_control,omitempty"`
}

type cacheBody struct {
	Tools                 []cacheBodyBlock   `json:"tools"`
	System                []cacheBodyBlock   `json:"system"`
	Messages              []cacheBodyMessage `json:"messages"`
	CacheControl          *bodyCacheControl  `json:"cache_control,omitempty"`
	PromptCacheOptions    json.RawMessage    `json:"prompt_cache_options,omitempty"`
	PromptCacheBreakpoint json.RawMessage    `json:"prompt_cache_breakpoint,omitempty"`
	TTL                   string             `json:"ttl,omitempty"`
}

type cacheMark struct {
	Section string
	Message int
	Block   int
	Control bodyCacheControl
}

func parseCacheBody(t *testing.T, raw []byte) cacheBody {
	t.Helper()
	var body cacheBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode captured provider body: %v\n%s", err, raw)
	}
	return body
}

func markTTL(cc bodyCacheControl) string {
	// Anthropic and MiniMax document ephemeral WITHOUT ttl as five minutes.
	if cc.TTL == "" {
		return "5m"
	}
	return cc.TTL
}

func nativeCacheBodyError(body cacheBody) error {
	marks := cacheMarks(body)
	if len(marks) == 0 || len(marks) > 4 { // FR-005: maximum four.
		return fmt.Errorf("cache marks = %d, want required tool/instruction/history marks and at most 4", len(marks))
	}
	var saw5m, toolMark, systemMark bool
	for _, mark := range marks {
		if mark.Control.Type != "ephemeral" {
			return fmt.Errorf("cache mark type = %q, want ephemeral", mark.Control.Type)
		}
		ttl := markTTL(mark.Control)
		if ttl != "1h" && ttl != "5m" {
			return fmt.Errorf("cache mark lifetime = %q, want 1h or 5m", ttl)
		}
		if saw5m && ttl == "1h" {
			return fmt.Errorf("a 1-hour mark appears after a 5-minute mark in tools/system/messages order")
		}
		saw5m = saw5m || ttl == "5m"
		switch mark.Section {
		case "tools":
			toolMark = toolMark || (ttl == "1h" && mark.Block == len(body.Tools)-1)
		case "system":
			systemMark = systemMark || (ttl == "1h" && strings.Contains(body.System[mark.Block].Text, "STABLE-INSTRUCTIONS"))
		case "messages":
		default:
			return fmt.Errorf("cache mark is at invalid %s level", mark.Section)
		}
	}
	if !toolMark {
		return fmt.Errorf("tool list has no 1-hour mark on its final definition")
	}
	if !systemMark {
		return fmt.Errorf("stable instructions have no 1-hour mark")
	}
	return historyCacheMarkError(body)
}

func historyCacheMarkError(body cacheBody) error {
	pending := map[string]bool{}
	var last *cacheBodyBlock
	var tailSeen bool
	for _, msg := range body.Messages {
		for i := range msg.Content {
			block := &msg.Content[i]
			switch block.Type {
			case "tool_use":
				pending[block.ID] = true
			case "tool_result":
				if !pending[block.ToolUseID] {
					return fmt.Errorf("tool result %q has no preceding call", block.ToolUseID)
				}
				delete(pending, block.ToolUseID)
			}
			if block.CacheControl != nil && len(pending) > 0 {
				return fmt.Errorf("cache mark splits a tool-call/result group")
			}
			if strings.Contains(block.Text, "Current Time") {
				if msg.Role != "user" || tailSeen || len(pending) > 0 {
					return fmt.Errorf("volatile tail is not one user-role block after completed history")
				}
				if block.CacheControl != nil {
					return fmt.Errorf("volatile tail carries a cache mark")
				}
				tailSeen = true
				continue
			}
			if tailSeen {
				return fmt.Errorf("history appears after the volatile tail")
			}
			last = block
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("history has an incomplete tool-call/result group")
	}
	if !tailSeen {
		return fmt.Errorf("volatile tail is missing from the fixture request")
	}
	if last == nil || last.CacheControl == nil || last.CacheControl.Type != "ephemeral" || markTTL(*last.CacheControl) != "5m" {
		return fmt.Errorf("last history content before the volatile tail has no 5-minute mark")
	}
	return nil
}

func miniMaxCacheBodyError(body cacheBody) error {
	for _, mark := range cacheMarks(body) {
		if mark.Control.Type != "ephemeral" || markTTL(mark.Control) != "5m" {
			return fmt.Errorf("MiniMax mark = %+v, want ephemeral 5-minute mode or automatic (no marks)", mark.Control)
		}
	}
	return nil
}

func moonshotCacheBodyError(body cacheBody) error {
	if len(cacheMarks(body)) != 0 || body.PromptCacheOptions != nil || body.PromptCacheBreakpoint != nil || body.TTL != "" {
		return fmt.Errorf("Moonshot body has a cache field or lifetime option")
	}
	return nil
}
