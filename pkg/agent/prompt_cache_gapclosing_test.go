package agent

// CHECK gap-closing tests (test-integrity-audit AUDIT mode, 2026-10-16).
// Each test here was written because a mutation probe SURVIVED the pack:
//
//   - TestFreezeSessionSkillMenu_DiskChangeDoesNotChangeAdvertisedList kills
//     the "skill list re-read each turn" mutant (spec row 15 / FR-009: the
//     advertised list is frozen for the session; the BDD scenario changes the
//     skill files between turns, which no existing test did).
//   - TestConsolidateVolatileNotes_ToolGroupStaysTogether kills the
//     "volatile note inserted between a tool call and its result" mutant on
//     the LOOP path (FR-007); the wire-level mutant on the window-normalizer
//     path was already killed by TestPrefixStable_AfterNormalizeAndAdapter,
//     but the loop-path consolidator had no tool-call coverage.
//
// Expected values come from the spec text, not from running the code.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestFreezeSessionSkillMenu_DiskChangeDoesNotChangeAdvertisedList(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)
	agent, ok := al.registry.GetAgent("worker")
	if !ok {
		t.Fatal("worker agent is not registered")
	}
	ts := fakeTurnState(agent, "sess-skill-freeze")

	menu := func(name string) string {
		// The production parser cuts names between <name> and </name> inside
		// the part that starts "# Skills\n" (pkg/agent/prompt_cache_sessions.go).
		return "# Agent\n\n---\n\n# Skills\n### <name>" + name + "</name>\n" + name + " does things"
	}

	first := al.freezeSessionSkillMenu(ts, []providers.Message{{Role: "system", Content: menu("alpha")}})
	if len(first) == 0 || first[0].Role != "system" {
		t.Fatalf("first turn = %+v, want the system message echoed", first)
	}
	if !strings.Contains(first[0].Content, "<name>alpha</name>") {
		t.Fatalf("first turn does not advertise alpha:\n%s", first[0].Content)
	}

	// The skill files on disk change before the next turn: the next built
	// system message carries beta. The advertised list must stay alpha.
	second := al.freezeSessionSkillMenu(ts, []providers.Message{{Role: "system", Content: menu("beta")}})
	if !strings.Contains(second[0].Content, "<name>alpha</name>") {
		t.Fatalf("advertised skill list changed when the disk content changed; want alpha still advertised:\n%s", second[0].Content)
	}
	if strings.Contains(second[0].Content, "<name>beta</name>") {
		t.Fatalf("the second turn's disk menu leaked into the advertised list:\n%s", second[0].Content)
	}
}

func TestConsolidateVolatileNotes_ToolGroupStaysTogether(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "SYS"},
		{Role: "user", Content: "PRIOR-USER"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
			{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "file-body-a"},
		{Role: "tool", ToolCallID: "call_2", Content: "file-body-b"},
		{Role: "assistant", Content: "HISTORY-END"},
	}
	got := consolidateVolatileNotes(appendVolatileNote(msgs, buildVolatileContext()))
	if len(got) != len(msgs)+1 {
		t.Fatalf("consolidated length = %d, want history plus exactly one tail", len(got))
	}
	tail := got[len(got)-1]
	if !tail.Volatile || tail.Role != "user" || !strings.Contains(tail.Content, "Current Time") {
		t.Fatalf("last message = %+v, want the volatile user-role tail", tail)
	}
	for i, m := range got[:len(got)-1] {
		if m.Volatile {
			t.Fatalf("volatile note at position %d is not the final message; it sits inside the history", i)
		}
	}
	// FR-007: no volatile text between a tool call and its results.
	pending := map[string]bool{}
	for i, m := range got {
		if m.Volatile && len(pending) != 0 {
			t.Fatalf("volatile tail at position %d splits a tool-call/result group (pending %v)", i, pending)
		}
		for _, call := range m.ToolCalls {
			pending[call.ID] = true
		}
		if m.ToolCallID != "" {
			delete(pending, m.ToolCallID)
		}
	}
	if len(pending) != 0 {
		t.Fatalf("unpaired tool calls after consolidation: %v", pending)
	}
}
