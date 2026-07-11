// Wave 3 fix 5b — span status/duration mismatch (live vs. reload divergence).
//
// pkg/agent/subturn.go's spawnSubTurn now persists the sub-turn's REAL
// EventKindSubTurnEnd status/duration onto the spawning "delegate" tool
// call's own persisted session.ToolCall record (via session.UnifiedStore.
// UpdateToolCallStatus) at the point EventKindSubTurnEnd fires. This corrects
// the ASYNC-delegation placeholder ack record (Status="success",
// DurationMS=0, from tools.AsyncResult — see pkg/tools/delegate.go
// executeAsync) that loop.go's standard tool-completion path writes almost
// instantly, well before the sub-turn goroutine actually finishes.
//
// These tests verify spawnSubTurn performs that write for both terminal
// outcomes (its own err == nil vs err != nil), and that it is a true
// update-in-place (the record is corrected, not duplicated).
//
// Traces to: Wave 3 fix 5b (confirmed root cause: live-vs-reload chat
// rendering divergence — see pkg/gateway/replay_test.go for the companion
// replay-side coverage proving the outer span reads this persisted value
// instead of emitNestedToolCalls' recomputed child-tool-call aggregate).

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newWave5bTestAgentLoop is a variant of newTestAgentLoop (loop_test.go) that
// accepts a caller-supplied provider so this file's tests can control the
// child turn's outcome without touching the shared mockProvider fixture.
func newWave5bTestAgentLoop(t *testing.T, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	return mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
}

// seedPlaceholderDelegateAck writes the ASYNC-delegation placeholder ack
// ToolCall record exactly as loop.go's standard tool-completion path does
// right after DelegateTool.executeAsync returns tools.AsyncResult (Status=
// "success", DurationMS=0, IsError=false unconditionally — see
// pkg/tools/result.go AsyncResult). The tests below verify spawnSubTurn
// corrects this record in place once the real sub-turn finishes.
func seedPlaceholderDelegateAck(t *testing.T, store *session.UnifiedStore, sessionID, callID string) {
	t.Helper()
	require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:      callID,
		Type:    session.EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []session.ToolCall{
			{ID: session.ToolCallID(callID), Tool: "delegate", Status: "success", DurationMS: 0},
		},
	}))
}

// TestSpawnSubTurn_CorrectsAsyncAckRecord_Success drives a real spawnSubTurn
// call (native dispatch, via the default agent + a real AgentLoop) to a
// successful completion and verifies the pre-seeded placeholder ack record
// is updated in place with Status="success" and a real, non-zero DurationMS
// — proving the EventKindSubTurnEnd defer's new persistence call in
// subturn.go actually reaches the transcript.
func TestSpawnSubTurn_CorrectsAsyncAckRecord_Success(t *testing.T) {
	al := newWave5bTestAgentLoop(t, &slowMockProvider{delay: 20 * time.Millisecond})

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = "c-5b-success"
	seedPlaceholderDelegateAck(t, store, sessionID, callID)

	baseAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, baseAgent, "default agent must exist")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-5b-success",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 10),
		session:             &ephemeralSessionStore{},
		agent:               baseAgent,
		transcriptStore:     store,
		transcriptSessionID: sessionID,
	}

	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}}
	ctx := withSpawnToolCallID(context.Background(), callID)
	_, spawnErr := spawnSubTurn(ctx, al, parent, cfg)
	require.NoError(t, spawnErr, "spawnSubTurn must succeed with slowMockProvider")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var found bool
	var duplicateCount int
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.ID != session.ToolCallID(callID) {
				continue
			}
			duplicateCount++
			found = true
			assert.Equal(t, "success", call.Status,
				"placeholder ack Status must reflect the real EventKindSubTurnEnd status (success)")
			assert.Greater(t, call.DurationMS, int64(0),
				"DurationMS must be corrected to a real measured wall-clock value, not the 0ms placeholder")
		}
	}
	require.True(t, found, "the placeholder ack ToolCall record must still be present")
	assert.Equal(t, 1, duplicateCount, "the record must be updated in place, not duplicated")
}

// TestSpawnSubTurn_CorrectsAsyncAckRecord_Error forces spawnSubTurn's own
// Go-level error path (dispatchErr from an executor Kind="remote-a2a",
// runner.ErrRemoteA2AReserved) — deterministic and immediate, no LLM call
// involved — and verifies the placeholder ack record (which tools.
// AsyncResult always writes as Status="success" regardless of the eventual
// outcome) is corrected to Status="error".
//
// A synthetic minimal *AgentInstance (not the shared registry default agent)
// is used as parent.agent so the executor override does not mutate shared
// test/registry state. dispatchErr short-circuits spawnSubTurn before any
// AgentInstance field besides Subagents/Model/ID is read, so the zero-value
// mu/providerPool/toolPolicy fields are never exercised.
func TestSpawnSubTurn_CorrectsAsyncAckRecord_Error(t *testing.T) {
	al := newWave5bTestAgentLoop(t, &mockProvider{}) // provider is never invoked on this path

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = "c-5b-error"
	seedPlaceholderDelegateAck(t, store, sessionID, callID)

	baseAgent := &AgentInstance{
		ID:   "remote-a2a-agent",
		Name: "Remote A2A Test Agent",
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A},
		},
	}

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-5b-error",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 10),
		session:             &ephemeralSessionStore{},
		agent:               baseAgent,
		transcriptStore:     store,
		transcriptSessionID: sessionID,
	}

	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}}
	ctx := withSpawnToolCallID(context.Background(), callID)
	_, spawnErr := spawnSubTurn(ctx, al, parent, cfg)
	require.Error(t, spawnErr, "spawnSubTurn must fail for a remote-a2a executor (reserved, unresolvable in v0.1.0)")

	entries, err2 := store.ReadTranscript(sessionID)
	require.NoError(t, err2)

	var found bool
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.ID == session.ToolCallID(callID) {
				found = true
				assert.Equal(t, "error", call.Status,
					"placeholder ack Status must be corrected to 'error' when spawnSubTurn's own Go function errors")
			}
		}
	}
	require.True(t, found, "the placeholder ack ToolCall record must still be present")
}
