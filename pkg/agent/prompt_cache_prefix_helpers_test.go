package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// These hand-written captures are the FR-007 oracle, not output copied from
// BuildMessages. They prove the comparison accepts legal growing histories.
func TestCachePrefixHelpers_SharedBoundaryAndMutants(t *testing.T) {
	first := []providers.Message{
		{Role: "system", Content: "STABLE"},
		{Role: "user", Content: "TURN-ONE"},
		{Role: "user", Content: "Current Time 12:00:00Z SCRATCH-ONE"},
	}
	second := []providers.Message{
		{Role: "system", Content: "STABLE"},
		{Role: "user", Content: "TURN-ONE"},
		{Role: "assistant", Content: "Mock response"},
		{Role: "user", Content: "TURN-TWO"},
		{Role: "user", Content: "Current Time 12:00:05Z SCRATCH-TWO"},
	}
	const want = `[{"role":"system","content":"STABLE"},{"role":"user","content":"TURN-ONE"}]`
	for _, messages := range [][]providers.Message{first, second} {
		if got := mustPrefixThrough(t, messages, "user", "TURN-ONE"); got != want {
			t.Fatalf("valid shared-history prefix = %s, want %s", got, want)
		}
	}
	if err := volatileTailError(first, "TURN-ONE", "SCRATCH-ONE"); err != nil {
		t.Fatalf("valid first tail rejected: %v", err)
	}
	if err := volatileTailError(second, "TURN-TWO", "SCRATCH-TWO"); err != nil {
		t.Fatalf("valid second tail rejected: %v", err)
	}
	t.Run("clock back in front", func(t *testing.T) {
		mutantA := append([]providers.Message(nil), first...)
		mutantB := append([]providers.Message(nil), second...)
		mutantA[0].Content += "\nCurrent Time 12:00:00Z"
		mutantB[0].Content += "\nCurrent Time 12:00:05Z"
		if mustPrefixThrough(t, mutantA, "user", "TURN-ONE") == mustPrefixThrough(t, mutantB, "user", "TURN-ONE") {
			t.Fatal("clock-in-front mutation survived shared-history comparison")
		}
		if err := volatileTailError(mutantA, "TURN-ONE", "SCRATCH-ONE"); err == nil || !strings.Contains(err.Error(), "before the history boundary") {
			t.Fatalf("clock-in-front diagnostic = %v, want volatile note before the history boundary", err)
		}
	})
	t.Run("scratchpad back in front", func(t *testing.T) {
		mutant := append([]providers.Message(nil), second...)
		mutant[0].Content += " SCRATCH-TWO"
		if mustPrefixThrough(t, first, "user", "TURN-ONE") == mustPrefixThrough(t, mutant, "user", "TURN-ONE") {
			t.Fatal("scratchpad-in-front mutation survived")
		}
	})
	for _, tc := range []struct {
		name, role, content, wantError string
		messages                       []providers.Message
	}{
		{"missing reply is not whole request", "assistant", "Mock response", "is missing", first},
		{"missing user boundary", "user", "NOT-PRESENT", "is missing", first},
		{"duplicate boundary", "user", "TURN-ONE", "is ambiguous", append(append([]providers.Message(nil), first...), first[1])},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := prefixThrough(tc.messages, tc.role, tc.content); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("prefixThrough error = %v, want %q", err, tc.wantError)
			}
		})
	}
	t.Log("valid captures accepted; clock/scratchpad-in-front and missing/ambiguous boundary mutations rejected")
}

func TestCachePrefixHelpers_HistoryAndMessageFieldsMatter(t *testing.T) {
	first := []providers.Message{
		{Role: "system", Content: "STABLE", SystemParts: []providers.ContentBlock{{Type: "text", Text: "STABLE", CacheControl: &providers.CacheControl{Type: "ephemeral", TTL: "1h"}}}},
		{Role: "user", Content: "PRIOR-USER"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
		{Role: "assistant", Content: "HISTORY-END"},
	}
	before := mustPrefixThrough(t, first, "assistant", "HISTORY-END")
	for _, tc := range []struct {
		name   string
		mutate func([]providers.Message)
	}{
		{"changed history text", func(m []providers.Message) { m[1].Content = "CHANGED" }},
		{"changed structured instructions", func(m []providers.Message) {
			m[0].SystemParts = []providers.ContentBlock{{Type: "text", Text: "CHANGED"}}
		}},
		{"changed tool arguments", func(m []providers.Message) {
			m[2].ToolCalls = []providers.ToolCall{{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "b.txt"}}}
		}},
		{"changed result correlation", func(m []providers.Message) { m[3].ToolCallID = "call_OTHER" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutant := append([]providers.Message(nil), first...)
			tc.mutate(mutant)
			if before == mustPrefixThrough(t, mutant, "assistant", "HISTORY-END") {
				t.Fatal("changed rendered history field was ignored")
			}
		})
	}
	metadataOnly := append([]providers.Message(nil), first...)
	metadataOnly[0].SystemParts = []providers.ContentBlock{{Type: "text", Text: "STABLE"}}
	if got := mustPrefixThrough(t, metadataOnly, "assistant", "HISTORY-END"); got != before {
		t.Fatal("moving only cache metadata changed rendered prefix comparison")
	}
	if first[0].SystemParts[0].CacheControl == nil {
		t.Fatal("prefix serialization mutated the captured request")
	}
	if historyHitPasses(101, 100, false) || historyHitPasses(100, 100, true) || !historyHitPasses(101, 100, true) {
		t.Fatal("SC-001 controls: static-only or mismatched history accepted, or matching history read rejected")
	}
}

func TestCacheWireHelpers_TailAndToolGroupMutants(t *testing.T) {
	// Independent FR-007 wire fixture: one complete two-call group, history
	// mark before the single volatile user block, stable workspace front.
	const fixture = `{
	  "system":[{"type":"text","text":"WORKSPACE-INSTRUCTIONS"}],
	  "messages":[
	    {"role":"user","content":[{"type":"text","text":"PRIOR-USER"}]},
	    {"role":"assistant","content":[
	      {"type":"tool_use","id":"call_1","name":"read","input":{"path":"a.txt"}},
	      {"type":"tool_use","id":"call_2","name":"read","input":{"path":"b.txt"}}
	    ]},
	    {"role":"user","content":[
	      {"type":"tool_result","tool_use_id":"call_1","content":"file-body-a"},
	      {"type":"tool_result","tool_use_id":"call_2","content":"file-body-b"}
	    ]},
	    {"role":"assistant","content":[{"type":"text","text":"PRIOR-ASSISTANT"}]},
	    {"role":"user","content":[{"type":"text","text":"current question","cache_control":{"type":"ephemeral","ttl":"5m"}}]},
	    {"role":"user","content":[{"type":"text","text":"Current Time 12:00:00Z"}]}
	  ]
	}`
	for _, tc := range []struct {
		name      string
		mutate    func(*cacheWireBody)
		wantError string
	}{
		{"valid completed group", func(*cacheWireBody) {}, ""},
		{"clock in front", func(b *cacheWireBody) { b.System[0].Text += " Current Time 12:00:00Z" }, "clock is still in the system prefix"},
		{"system role tail", func(b *cacheWireBody) { b.Messages[5].Role = "system" }, "not user text after all paired tool results"},
		{"tail between parallel results", func(b *cacheWireBody) {
			b.Messages[2].Content = append(b.Messages[2].Content[:1], append(b.Messages[5].Content, b.Messages[2].Content[1])...)
			b.Messages = b.Messages[:5]
		}, "not user text after all paired tool results"},
		{"mark in tool result gap", func(b *cacheWireBody) {
			b.Messages[2].Content[0].CacheControl = &providers.CacheControl{Type: "ephemeral", TTL: "5m"}
		}, "cache mark splits a tool-call/result group"},
		{"no history mark", func(b *cacheWireBody) { b.Messages[4].Content[0].CacheControl = nil }, "last history content before the tail has no 5-minute cache mark"},
		{"tail itself marked", func(b *cacheWireBody) {
			b.Messages[5].Content[0].CacheControl = &providers.CacheControl{Type: "ephemeral"}
		}, "volatile tail is inside the history cache mark"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body cacheWireBody
			if err := json.Unmarshal([]byte(fixture), &body); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&body)
			err := cacheWireTailError(body)
			if (tc.wantError == "" && err != nil) || (tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError))) {
				t.Fatalf("wire diagnostic = %v, want %q", err, tc.wantError)
			}
			if tc.wantError == "" {
				assertCacheWireToolResults(t, body, map[string]string{"call_1": "file-body-a", "call_2": "file-body-b"})
			}
		})
	}
}
