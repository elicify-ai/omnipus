// W4 regression coverage: the persisted "delegate" tool_call record must
// carry the sub-turn's own OUTPUT, not just its terminal Status/DurationMS.
//
// Root cause: session.UnifiedStore.UpdateToolCallStatus (Wave 3 fix 5b)
// rewrote only Status+DurationMS on the spawning tool call's persisted
// record; pkg/agent/loop.go's tcRecord.Result is set only when the tool
// result carries Media. So a delegate tool_call's `result` field stayed
// empty even when the subagent succeeded — a session reload showed a
// terminal status with zero trace of what the delegate actually produced,
// unlike the live WS stream (SubTurnEndPayload).
//
// The fix adds session.UnifiedStore.UpdateToolCallStatusAndResult (a
// result-bearing sibling of UpdateToolCallStatus, mirroring
// recordExternalToolResultUpdateInPlace's read-modify-rewrite-one-line
// approach) and calls it from spawnSubTurn's cleanup defer with
// result.ForLLM on success / the error text on failure.
//
// These tests mirror wave3_fix5b_test.go's structure (same helpers, same
// drive-a-real-spawnSubTurn-to-completion approach) but assert on the
// persisted ToolCall.Result field instead of Status/DurationMS.
//
// Traces to: pkg/session/unified.go UpdateToolCallStatusAndResult,
// pkg/agent/subturn.go spawnSubTurn's cleanup defer (W4).

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSpawnSubTurn_PersistsDelegateResult_Success drives a real spawnSubTurn
// call (native dispatch) to a successful completion and verifies the
// pre-seeded placeholder ack record's Result field is populated with the
// sub-turn's actual output text (slowMockProvider's fixed response), not
// left empty.
func TestSpawnSubTurn_PersistsDelegateResult_Success(t *testing.T) {
	al := newWave5bTestAgentLoop(t, &slowMockProvider{delay: 20 * time.Millisecond})

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = "c-w4-success"
	seedPlaceholderDelegateAck(t, store, sessionID, callID)

	baseAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, baseAgent, "default agent must exist")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-w4-success",
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
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.ID != session.ToolCallID(callID) {
				continue
			}
			found = true
			require.NotEmpty(t, call.Result,
				"the persisted delegate tool_call's Result field must be populated on success, "+
					"not left empty — a session reload otherwise shows a terminal status with zero "+
					"trace of the sub-turn's actual output")
			text, ok := call.Result["text"]
			require.True(t, ok, "Result must carry a \"text\" key (matching the loop.go media-result "+
				"convention); got %+v", call.Result)
			assert.Equal(t, "slow response completed", text,
				"Result[\"text\"] must be the sub-turn's actual final content (result.ForLLM)")
			assert.NotContains(t, call.Result, "error",
				"a successful sub-turn's Result must not carry an \"error\" key")
		}
	}
	require.True(t, found, "the placeholder ack ToolCall record must still be present")
}

// TestSpawnSubTurn_PersistsDelegateResult_Error forces spawnSubTurn's own
// Go-level dispatch-reject error path (executor Kind="remote-a2a", reserved
// and always rejected) and verifies the persisted record's Result field
// carries the error text, not an empty map.
func TestSpawnSubTurn_PersistsDelegateResult_Error(t *testing.T) {
	al := newWave5bTestAgentLoop(t, &mockProvider{}) // provider is never invoked on this path

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = "c-w4-error"
	seedPlaceholderDelegateAck(t, store, sessionID, callID)

	baseAgent := &AgentInstance{
		ID:   "remote-a2a-agent-w4",
		Name: "Remote A2A Test Agent (W4)",
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A},
		},
	}

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-w4-error",
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
			if call.ID != session.ToolCallID(callID) {
				continue
			}
			found = true
			require.NotEmpty(t, call.Result,
				"the persisted delegate tool_call's Result field must carry the error text on "+
					"failure, not be left empty")
			text, ok := call.Result["text"]
			require.True(t, ok, "Result must carry a \"text\" key; got %+v", call.Result)
			assert.Contains(t, text, "SubTurn dispatch rejected",
				"Result[\"text\"] must carry the dispatch-reject error text")
		}
	}
	require.True(t, found, "the placeholder ack ToolCall record must still be present")
}

// TestSpawnSubTurn_PersistsDelegateResult_NativeError_MergesIsError drives a
// GENUINE native-turn failure — al.runTurn itself returns an error, via a
// provider whose Chat call always errors — rather than
// TestSpawnSubTurn_PersistsDelegateResult_Error's dispatch-reject path
// (executor Kind="remote-a2a", rejected before runTurn is ever called, so
// result.IsError stays false there: the ForLLM text alone happens to
// contain "SubTurn dispatch rejected", never exercising the separate
// IsError-driven merge).
//
// subturn.go's native path (~L1240-1254) sets result.IsError = true
// explicitly when turnErr != nil; the cleanup defer's toolCallResult
// construction (~L1090-1101) then merges that into the persisted record as
// a distinct "error": true key ON TOP OF "text", not folded into the text.
// This test is the only one in this file that actually reaches that merge.
func TestSpawnSubTurn_PersistsDelegateResult_NativeError_MergesIsError(t *testing.T) {
	al := newWave5bTestAgentLoop(t, &alwaysErrorProvider{})

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "", "jim")
	require.NoError(t, err)
	sessionID := meta.ID

	const callID = "c-w4-native-error"
	seedPlaceholderDelegateAck(t, store, sessionID, callID)

	baseAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, baseAgent, "default agent must exist")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-w4-native-error",
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
	require.Error(t, spawnErr, "spawnSubTurn must fail when the native runTurn call itself errors")

	entries, err2 := store.ReadTranscript(sessionID)
	require.NoError(t, err2)

	var found bool
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.ID != session.ToolCallID(callID) {
				continue
			}
			found = true
			require.NotEmpty(t, call.Result,
				"the persisted delegate tool_call's Result field must carry the error text on "+
					"a genuine turn failure, not be left empty")
			text, ok := call.Result["text"]
			require.True(t, ok, "Result must carry a \"text\" key; got %+v", call.Result)
			assert.Contains(t, text, "SubTurn failed",
				"Result[\"text\"] must carry the native-turn-failure error text (subturn.go's "+
					"'SubTurn failed: %%v' — distinct from the dispatch-reject wording)")
			isErr, ok := call.Result["error"]
			require.True(t, ok, "Result must carry an \"error\" key on a result.IsError==true "+
				"outcome; got %+v", call.Result)
			assert.Equal(t, true, isErr,
				"Result[\"error\"] must be true — this is the IsError merge onto toolCallResult "+
					"that only a genuine result.IsError==true outcome exercises")
		}
	}
	require.True(t, found, "the placeholder ack ToolCall record must still be present")
}
