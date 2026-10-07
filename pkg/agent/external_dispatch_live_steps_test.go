// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_dispatch_live_steps_test.go — #492: an external-CLI (subagent_3p)
// child's tool calls must be broadcast live, not only written to the
// transcript, so the SPA's SubagentSpan.steps shows them while the CLI works.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestExternalDispatch_ToolCallsAreBroadcastLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "codex", "")
	const (
		wantChatID = "chat-ext-492"
		parentCall = "call_spawn_492"
	)
	ts.opts.ChatID = wantChatID
	ts.chatID = wantChatID
	ts.parentSpawnCallID = parentCall

	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("UnifiedStore.Close: %v", closeErr)
		}
	})
	ts.transcriptStore = store
	ts.transcriptSessionID = "session_ext_492"

	sub := al.SubscribeEvents(64)
	defer al.UnsubscribeEvents(sub.ID)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindToolCall,
			ToolCall: &runner.ToolCallEvent{
				CallID: "ext-call-1", ToolName: "write_file", ToolInput: []byte(`{"path":"a.txt"}`),
			},
		})
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindToolResult,
			ToolResult: &runner.ToolResultEvent{
				CallID: "ext-call-1", ToolName: "write_file", Output: []byte(`{"ok":true}`),
			},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	_, _ = runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)

	var start *ToolExecStartPayload
	var end *ToolExecEndPayload
	deadline := time.After(5 * time.Second)
collect:
	for start == nil || end == nil {
		select {
		case ev := <-sub.C:
			switch p := ev.Payload.(type) {
			case ToolExecStartPayload:
				if ev.Kind == EventKindToolExecStart {
					start = &p
				}
			case ToolExecEndPayload:
				if ev.Kind == EventKindToolExecEnd {
					end = &p
				}
			}
		case <-deadline:
			break collect
		}
	}
	require.NotNil(t, start, "tool_call_start must be broadcast live for an external-CLI child")
	require.NotNil(t, end, "tool_call_result must be broadcast live for an external-CLI child")

	require.Equal(t, session.ToolCallID("ext-call-1"), start.ToolCallID)
	require.Equal(t, "write_file", start.Tool)
	require.Equal(t, map[string]any{"path": "a.txt"}, start.Arguments)
	require.Equal(t, wantChatID, start.ChatID)
	require.Equal(t, "session_ext_492", start.SessionID)
	require.Equal(t, session.ToolCallID(parentCall), start.ParentSpawnCallID,
		"must be tied to the parent spawn call so the SPA attaches it to the subagent span")

	require.Equal(t, session.ToolCallID("ext-call-1"), end.ToolCallID, "result must pair with its call")
	require.Equal(t, `{"ok":true}`, end.Result)
	require.False(t, end.IsError)
	require.Equal(t, session.ToolCallID(parentCall), end.ParentSpawnCallID)
	require.Equal(t, wantChatID, end.ChatID)
}

// collectExternalToolEvents runs an external-CLI sub-turn that emits evs and
// returns every live tool_call_start / tool_call_result payload broadcast.
func collectExternalToolEvents(t *testing.T, evs []runner.RunEvent, prep func(al *AgentLoop)) ([]ToolExecStartPayload, []ToolExecEndPayload) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "codex", "")
	if prep != nil {
		prep(al)
	}
	ts.chatID = "chat-ext-492b"
	ts.opts.ChatID = ts.chatID
	ts.parentSpawnCallID = "call_spawn_492b"
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ts.transcriptStore = store
	ts.transcriptSessionID = "session_ext_492b"

	sub := al.SubscribeEvents(64)
	defer al.UnsubscribeEvents(sub.ID)
	fr, restore := withFakeDriver(t)
	defer restore()
	go func() {
		for _, ev := range evs {
			fr.InjectEvent(ev)
		}
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()
	_, _ = runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)

	var starts []ToolExecStartPayload
	var ends []ToolExecEndPayload
	for {
		select {
		case ev := <-sub.C:
			switch p := ev.Payload.(type) {
			case ToolExecStartPayload:
				starts = append(starts, p)
			case ToolExecEndPayload:
				ends = append(ends, p)
			}
		default:
			return starts, ends
		}
	}
}

// #492 review F5(a): a runner that sends no CallID must still pair the live
// result with its start.
func TestExternalDispatch_EmptyCallID_ResultPairsWithStart(t *testing.T) {
	starts, ends := collectExternalToolEvents(t, []runner.RunEvent{
		{Kind: runner.EventKindToolCall, ToolCall: &runner.ToolCallEvent{ToolName: "bash", ToolInput: []byte(`{"cmd":"ls"}`)}},
		{Kind: runner.EventKindToolResult, ToolResult: &runner.ToolResultEvent{ToolName: "bash", Output: []byte(`"ok"`)}},
	}, nil)
	require.Len(t, starts, 1)
	require.Len(t, ends, 1)
	require.NotEmpty(t, starts[0].ToolCallID)
	require.Equal(t, starts[0].ToolCallID, ends[0].ToolCallID, "id-less result must reuse the recorded start id")
}

// F5(c): an error result and an empty tool input both broadcast cleanly.
func TestExternalDispatch_ErrorResultAndEmptyInput(t *testing.T) {
	starts, ends := collectExternalToolEvents(t, []runner.RunEvent{
		{Kind: runner.EventKindToolCall, ToolCall: &runner.ToolCallEvent{CallID: "c1", ToolName: "bash"}},
		{Kind: runner.EventKindToolResult, ToolResult: &runner.ToolResultEvent{CallID: "c1", ToolName: "bash", Output: []byte(`{"err":"boom"}`), IsError: true}},
	}, nil)
	require.Len(t, starts, 1)
	require.Len(t, ends, 1)
	require.Empty(t, starts[0].Arguments, "empty ToolInput means no arguments")
	require.True(t, ends[0].IsError)
	require.Equal(t, `{"err":"boom"}`, ends[0].Result)
}

// F5(b): the native path redacts credentials from a tool result before its
// tool_call_result carries it (config.FilterSensitiveData); the external path
// reuses that function.
func TestExternalDispatch_ResultIsRedactedLikeNative(t *testing.T) {
	const secret = "sk-test-secret-value-492"
	t.Cleanup(logger.SetSensitiveValueReplacer(nil)) // RegisterSensitiveValues publishes a process-wide scrubber
	_, ends := collectExternalToolEvents(t, []runner.RunEvent{
		{Kind: runner.EventKindToolCall, ToolCall: &runner.ToolCallEvent{CallID: "c1", ToolName: "bash"}},
		{Kind: runner.EventKindToolResult, ToolResult: &runner.ToolResultEvent{
			CallID: "c1", ToolName: "bash", Output: []byte(`{"stdout":"key=` + secret + `"}`)}},
	}, func(al *AgentLoop) {
		cfg := al.GetConfig()
		cfg.Tools.FilterSensitiveData = true
		cfg.RegisterSensitiveValues([]string{secret})
	})
	require.Len(t, ends, 1)
	require.NotContains(t, ends[0].Result, secret, "credential must not reach the live frame")
	require.Contains(t, ends[0].Result, "[FILTERED]")
}
