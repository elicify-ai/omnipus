// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// newTranscriptFixture writes a session transcript with the given tool_call
// entries and returns a UnifiedStore pointing at its parent directory, plus
// the session id.
func newTranscriptFixture(t *testing.T, entries []session.TranscriptEntry) (*session.UnifiedStore, string) {
	t.Helper()
	baseDir := t.TempDir()
	sessionID := "session_01TEST"
	if err := os.MkdirAll(filepath.Join(baseDir, sessionID), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcriptPath := filepath.Join(baseDir, sessionID, "transcript.jsonl")
	f, err := os.Create(transcriptPath)
	if err != nil {
		t.Fatalf("create transcript: %v", err)
	}
	defer f.Close()
	// Persist a minimal meta.json so ListSessions wouldn't fail if called.
	meta := map[string]any{"id": sessionID, "created_at": "2026-04-24T00:00:00Z"}
	metaData, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(baseDir, sessionID, "meta.json"), metaData, 0o600)
	for _, entry := range entries {
		data, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			t.Fatalf("marshal entry: %v", marshalErr)
		}
		if _, writeErr := f.Write(append(data, '\n')); writeErr != nil {
			t.Fatalf("write: %v", writeErr)
		}
	}
	store, err := session.NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	return store, sessionID
}

func toolCallEntry(id, tool string, params map[string]any) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID:   id,
		Type: "tool_call",
		ToolCalls: []session.ToolCall{{
			ID:         session.ToolCallID(id),
			Tool:       tool,
			Status:     "success",
			Parameters: params,
		}},
	}
}

func TestRepairHistory_OrphanToolResult_SynthesizesToolUse(t *testing.T) {
	// Mirrors the observed Ray session: an assistant message declares two
	// tool_uses, but a third tool_result arrives for an id that was never
	// declared. Repair must inject a matching tool_use into the preceding
	// assistant so Anthropic's invariant holds.
	store, sid := newTranscriptFixture(t, []session.TranscriptEntry{
		toolCallEntry("t_orphan", "web_fetch", map[string]any{"url": "https://example.com"}),
	})

	messages := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t_declared", Type: "function", Name: "web_search"},
		}},
		{Role: "tool", ToolCallID: "t_declared", Content: "some results"},
		{Role: "tool", ToolCallID: "t_orphan", Content: "orphan result"},
	}

	out, stats := repairHistory(context.Background(), messages, store, sid, nil, "test-agent")

	if stats.SyntheticToolUses != 1 {
		t.Errorf("expected 1 synthetic tool_use, got %d", stats.SyntheticToolUses)
	}
	if len(out) != len(messages) {
		t.Fatalf(
			"repair should not change message count on pure-result-orphan case, got %d want %d",
			len(out),
			len(messages),
		)
	}
	// The assistant message should now declare BOTH t_declared and t_orphan.
	assistant := out[1]
	ids := make([]string, 0, len(assistant.ToolCalls))
	for _, tc := range assistant.ToolCalls {
		ids = append(ids, tc.ID)
	}
	if len(ids) != 2 || ids[0] != "t_declared" || ids[1] != "t_orphan" {
		t.Errorf("assistant tool_calls = %v; want [t_declared, t_orphan]", ids)
	}
	// The synthesized tool_use must carry the tool name + arguments from the transcript.
	synth := assistant.ToolCalls[1]
	if synth.Name != "web_fetch" {
		t.Errorf("synth tool name = %q, want web_fetch", synth.Name)
	}
	if url, _ := synth.Arguments["url"].(string); url != "https://example.com" {
		t.Errorf("synth url = %q, want https://example.com", url)
	}
}

func TestRepairHistory_OrphanToolUse_DestructiveGetsErrorStub(t *testing.T) {
	// Assistant declared a write_file tool_use that never got resolved.
	// write_file is destructive — repair must NOT re-invoke it; instead, emit
	// a synthetic error stub so the conversation keeps moving.
	store, sid := newTranscriptFixture(t, []session.TranscriptEntry{
		toolCallEntry("t_unresolved", "write_file", map[string]any{"path": "/tmp/x", "content": "data"}),
	})

	messages := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t_unresolved", Type: "function", Name: "write_file"},
		}},
	}

	out, stats := repairHistory(context.Background(), messages, store, sid, nil, "test-agent")

	if stats.SyntheticErrorStubs != 1 {
		t.Errorf("expected 1 synthetic error stub, got %d", stats.SyntheticErrorStubs)
	}
	if stats.ReinvokedIdempotent != 0 {
		t.Errorf("destructive tool must NOT be reinvoked; got %d", stats.ReinvokedIdempotent)
	}
	if len(out) != 2 {
		t.Fatalf("expected assistant + synthetic tool_result, got %d messages", len(out))
	}
	if out[1].Role != "tool" || out[1].ToolCallID != "t_unresolved" {
		t.Errorf("out[1] = %+v, want a tool message with ToolCallID=t_unresolved", out[1])
	}
	if !strings.Contains(out[1].Content, "tool_result_recovered") {
		t.Errorf("synthetic stub should mention tool_result_recovered; got: %s", out[1].Content)
	}
}

func TestRepairHistory_NoOrphans_NoOp(t *testing.T) {
	// Well-formed history should return identical messages with zero stats.
	store, sid := newTranscriptFixture(t, nil)

	messages := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t1", Type: "function", Name: "web_search"},
		}},
		{Role: "tool", ToolCallID: "t1", Content: "result"},
		{Role: "assistant", Content: "done"},
	}

	out, stats := repairHistory(context.Background(), messages, store, sid, nil, "test-agent")

	if stats.anyRepaired() {
		t.Errorf("no-op case must have zero stats, got %+v", stats)
	}
	if len(out) != len(messages) {
		t.Errorf("no-op must not change message count: got %d want %d", len(out), len(messages))
	}
}

func TestRepairHistory_NoTranscript_FallsThrough(t *testing.T) {
	// Orphans exist but we have no transcript to read. The repair must bail
	// gracefully and return the input unchanged (the wire-level sanitizer
	// will drop the orphan results as a last-resort defense).
	messages := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t1", Type: "function", Name: "web_search"},
		}},
		{Role: "tool", ToolCallID: "t1", Content: "ok"},
		{Role: "tool", ToolCallID: "t_unknown", Content: "orphan"},
	}

	out, stats := repairHistory(context.Background(), messages, nil, "", nil, "test-agent")

	if stats.anyRepaired() {
		t.Errorf("no transcript → no repair; got stats %+v", stats)
	}
	if len(out) != len(messages) {
		t.Errorf("fallthrough must preserve messages: got %d want %d", len(out), len(messages))
	}
}

func TestRepairHistory_MixedOrphansAcrossTwoTurns(t *testing.T) {
	// Two turns of history: the first turn has an orphan result, the second
	// has an orphan use. Both must be repaired independently.
	store, sid := newTranscriptFixture(t, []session.TranscriptEntry{
		toolCallEntry("t1_orphan", "web_fetch", map[string]any{"url": "a"}),
		toolCallEntry("t2_unresolved", "exec", map[string]any{"cmd": "ls"}),
	})

	messages := []providers.Message{
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t1_real", Type: "function", Name: "web_search"},
		}},
		{Role: "tool", ToolCallID: "t1_real", Content: "real result"},
		{Role: "tool", ToolCallID: "t1_orphan", Content: "orphan result"},
		{Role: "assistant", Content: "turn 1 summary"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t2_unresolved", Type: "function", Name: "exec"},
		}},
	}

	out, stats := repairHistory(context.Background(), messages, store, sid, nil, "test-agent")

	if stats.SyntheticToolUses != 1 {
		t.Errorf("expected 1 synthetic tool_use (for t1_orphan), got %d", stats.SyntheticToolUses)
	}
	if stats.SyntheticErrorStubs != 1 {
		t.Errorf("expected 1 synthetic stub (for t2_unresolved, destructive), got %d", stats.SyntheticErrorStubs)
	}
	// Structural check: the final output must satisfy the Anthropic invariant.
	declared, resolved := collectToolIDs(out)
	for id := range declared {
		if !resolved[id] {
			t.Errorf("tool_use %q still unresolved after repair", id)
		}
	}
	for id := range resolved {
		if !declared[id] {
			t.Errorf("tool_result %q still has no matching tool_use after repair", id)
		}
	}
}

// TestRepairHistory_PolicyDenyBlocksReinvocation covers H3: when the agent's
// tool policy denies an idempotent tool, repair must NOT re-invoke it.
// The orphan tool_use should get a synthetic error stub instead.
func TestRepairHistory_PolicyDenyBlocksReinvocation(t *testing.T) {
	store, sid := newTranscriptFixture(t, []session.TranscriptEntry{
		toolCallEntry("t_idempotent", "web_search", map[string]any{"query": "test"}),
	})

	messages := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t_idempotent", Type: "function", Name: "web_search"},
		}},
	}

	// Policy denies web_search — repair must NOT re-invoke it.
	denyPolicy := &tools.ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies:      map[string]string{"web_search": "deny"},
	}

	out, stats := repairHistory(context.Background(), messages, store, sid, nil, "test-agent", denyPolicy)

	// Must NOT reinvoke — should fall back to synthetic error stub.
	if stats.ReinvokedIdempotent != 0 {
		t.Errorf("expected 0 reinvocations when policy denies tool, got %d", stats.ReinvokedIdempotent)
	}
	if stats.SyntheticErrorStubs != 1 {
		t.Errorf("expected 1 synthetic error stub for policy-denied tool, got %d", stats.SyntheticErrorStubs)
	}
	// The conversation must still be structurally valid (orphan resolved).
	if len(out) == 0 {
		t.Fatal("expected non-empty output messages")
	}
}

// TestRepairHistory_PolicyAllowPermitsReinvocation covers the policy-allow path:
// when policy allows an idempotent tool but no registry is provided (nil), repair
// still skips reinvocation (nil registry guard), not a policy deny.
func TestRepairHistory_NilRegistrySkipsReinvocation(t *testing.T) {
	store, sid := newTranscriptFixture(t, []session.TranscriptEntry{
		toolCallEntry("t_idempotent", "web_search", map[string]any{"query": "test"}),
	})

	messages := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "t_idempotent", Type: "function", Name: "web_search"},
		}},
	}

	// No policy restriction — allow-all.
	allowPolicy := &tools.ToolPolicyCfg{DefaultPolicy: "allow"}

	// Nil registry — can't reinvoke but not a policy deny.
	out, stats := repairHistory(context.Background(), messages, store, sid, nil, "test-agent", allowPolicy)

	if stats.ReinvokedIdempotent != 0 {
		t.Errorf("expected 0 reinvocations with nil registry, got %d", stats.ReinvokedIdempotent)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output messages")
	}
}

func TestIsIdempotentTool(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"web_search", true},
		{"web_fetch", true},
		{"read_file", true},
		{"list_dir", true},
		{"write_file", false},
		{"exec", false},
		{"spawn", false},
		{"task_create", false},
		{"unknown_tool", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isIdempotentTool(tc.name); got != tc.want {
			t.Errorf("isIdempotentTool(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
