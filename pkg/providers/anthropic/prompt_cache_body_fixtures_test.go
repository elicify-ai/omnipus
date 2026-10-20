package anthropicprovider

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Hand-authored FR-005/007 fixture. JSON object members deliberately appear in
// reverse render order: JSON key order does not change tools/system/messages.
const validNativeCacheBody = `{
  "messages": [
    {"role":"user","content":[{"type":"text","text":"PRIOR-USER"}]},
    {"role":"assistant","content":[
      {"type":"tool_use","id":"call_a","name":"read","input":{"path":"a.txt"}},
      {"type":"tool_use","id":"call_b","name":"read","input":{"path":"b.txt"}}
    ]},
    {"role":"user","content":[
      {"type":"tool_result","tool_use_id":"call_a","content":"result-a"},
      {"type":"tool_result","tool_use_id":"call_b","content":"result-b"}
    ]},
    {"role":"assistant","content":[{"type":"text","text":"PRIOR-ASSISTANT"}]},
    {"role":"user","content":[{"type":"text","text":"latest question","cache_control":{"type":"ephemeral","ttl":"5m"}}]},
    {"role":"user","content":[{"type":"text","text":"Current Time 12:00:00Z SCRATCHPAD-NOTE"}]}
  ],
  "system":[{"type":"text","text":"STABLE-INSTRUCTIONS","cache_control":{"type":"ephemeral","ttl":"1h"}}],
  "tools":[{"name":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}]
}`

func TestCacheMarks_SemanticOrderAndMutants(t *testing.T) {
	wantMarks := []cacheMark{
		{"tools", -1, 0, bodyCacheControl{Type: "ephemeral", TTL: "1h"}},
		{"system", -1, 0, bodyCacheControl{Type: "ephemeral", TTL: "1h"}},
		{"messages", 4, 0, bodyCacheControl{Type: "ephemeral", TTL: "5m"}},
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validNativeCacheBody), &sections); err != nil {
		t.Fatal(err)
	}
	// All six member orders must yield the SAME semantic marks. No random loop.
	for _, order := range [][]string{
		{"tools", "system", "messages"}, {"tools", "messages", "system"},
		{"system", "tools", "messages"}, {"system", "messages", "tools"},
		{"messages", "tools", "system"}, {"messages", "system", "tools"},
	} {
		raw := fmt.Sprintf(`{%q:%s,%q:%s,%q:%s}`, order[0], sections[order[0]], order[1], sections[order[1]], order[2], sections[order[2]])
		body := parseCacheBody(t, []byte(raw))
		if got := cacheMarks(body); !reflect.DeepEqual(got, wantMarks) {
			t.Fatalf("JSON member order %v: marks = %+v, want %+v", order, got, wantMarks)
		}
		if err := nativeCacheBodyError(body); err != nil {
			t.Fatalf("valid body in member order %v rejected: %v", order, err)
		}
	}
	for _, tc := range []struct {
		name      string
		mutate    func(*cacheBody)
		wantError string
	}{
		{"default five minutes", func(b *cacheBody) { b.Messages[4].Content[0].CacheControl.TTL = "" }, ""},
		{"four marks allowed", func(b *cacheBody) {
			b.Messages[0].Content[0].CacheControl = &bodyCacheControl{Type: "ephemeral", TTL: "5m"}
		}, ""},
		{"no marks", func(b *cacheBody) {
			b.Tools[0].CacheControl, b.System[0].CacheControl, b.Messages[4].Content[0].CacheControl = nil, nil, nil
		}, "cache marks = 0"},
		{"one mark cannot cover all three sections", func(b *cacheBody) {
			b.Tools[0].CacheControl, b.System[0].CacheControl = nil, nil
		}, "tool list has no 1-hour mark"},
		{"two marks missing stable instructions", func(b *cacheBody) { b.System[0].CacheControl = nil }, "stable instructions have no 1-hour mark"},
		{"five minute before one hour", func(b *cacheBody) { b.Tools[0].CacheControl.TTL = "5m" }, "a 1-hour mark appears after a 5-minute mark"},
		{"more than four marks", func(b *cacheBody) {
			b.Messages[0].Content[0].CacheControl = &bodyCacheControl{Type: "ephemeral", TTL: "5m"}
			b.Messages[3].Content[0].CacheControl = &bodyCacheControl{Type: "ephemeral", TTL: "5m"}
		}, "cache marks = 5"},
		{"mark on a tool-call result gap", func(b *cacheBody) {
			b.Messages[1].Content[1].CacheControl = b.Messages[4].Content[0].CacheControl
			b.Messages[4].Content[0].CacheControl = nil
		}, "cache mark splits a tool-call/result group"},
		{"mark before final parallel result", func(b *cacheBody) {
			b.Messages[2].Content[0].CacheControl = b.Messages[4].Content[0].CacheControl
			b.Messages[4].Content[0].CacheControl = nil
		}, "cache mark splits a tool-call/result group"},
		{"missing tail history mark", func(b *cacheBody) { b.Messages[4].Content[0].CacheControl = nil }, "last history content before the volatile tail has no 5-minute mark"},
		{"mark moved onto volatile tail", func(b *cacheBody) {
			b.Messages[5].Content[0].CacheControl = b.Messages[4].Content[0].CacheControl
			b.Messages[4].Content[0].CacheControl = nil
		}, "volatile tail carries a cache mark"},
		{"wrong mark type", func(b *cacheBody) { b.Tools[0].CacheControl.Type = "persistent" }, "cache mark type ="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := parseCacheBody(t, []byte(validNativeCacheBody))
			tc.mutate(&body)
			err := nativeCacheBodyError(body)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("valid fixture rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("fixture diagnostic = %v, want %q", err, tc.wantError)
			}
			t.Logf("diagnostic: %v", err)
		})
	}
}

func TestDialectCacheHelpers_DefaultLifetimeAndEveryMark(t *testing.T) {
	for _, tc := range []struct {
		name, raw, wantError string
	}{
		{"automatic", `{"messages":[]}`, ""},
		{"documented default five minutes", `{"system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]}`, ""},
		{"explicit five minutes", `{"system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`, ""},
		{"one hour rejected", `{"system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`, "want ephemeral 5-minute"},
		{"one valid mark does not excuse another", `{"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral","ttl":"invalid"}},{"type":"text","text":"b","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`, "want ephemeral 5-minute"},
	} {
		t.Run("MiniMax/"+tc.name, func(t *testing.T) {
			err := miniMaxCacheBodyError(parseCacheBody(t, []byte(tc.raw)))
			if (tc.wantError == "" && err != nil) || (tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError))) {
				t.Fatalf("diagnostic = %v, want %q", err, tc.wantError)
			}
		})
	}
	for _, tc := range []struct {
		name, raw string
		wantError bool
	}{
		{"no cache fields", `{"messages":[]}`, false},
		{"ordinary text is not a cache option", `{"system":[{"type":"text","text":"cache_control ttl 1h"}]}`, false},
		{"block mark", `{"system":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral"}}]}`, true},
		{"top-level mark", `{"cache_control":{"type":"ephemeral","ttl":"1h"}}`, true},
		{"lifetime option", `{"prompt_cache_options":{"ttl":"1h"}}`, true},
	} {
		t.Run("Moonshot/"+tc.name, func(t *testing.T) {
			err := moonshotCacheBodyError(parseCacheBody(t, []byte(tc.raw)))
			if (err != nil) != tc.wantError || (err != nil && err.Error() != "Moonshot body has a cache field or lifetime option") {
				t.Fatalf("diagnostic = %v, wantError %v", err, tc.wantError)
			}
		})
	}
}
