package gateway

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round-3 architect correction: the SINGLE-TURN done truthfully carries
// turn_failed=true for a save failure (#1178), while the run still completes
// and the parent gets warning + answer. The round-2 two-turn case cannot prove
// this: its failing turn defers done and a clean continuation finalizes instead.
//
// Real CLI process, dispatcher, broken archive append, save latch, finalize,
// completion outbox/inbox and two hub viewers. No turnResult is injected and no
// continuation is queued. W17 literals come from the approved catalogue.
func TestOpenCodeGateR3_SingleTurnSaveFailureCompletesWithAnswerAndFailedDone(t *testing.T) {
	// This process fixture supports narration and finish without requiring a
	// follow-up. Despite its historical mode name, this test queues NOTHING.
	al, child, viewers, process := opencodeFix1895Launch(t, opencodeFix1895Program(t), "queued_save_failure")
	t.Cleanup(al.Close)
	logPath := filepath.Join(t.TempDir(), "single-turn-save.jsonl")
	level := logger.GetLevel()
	logger.SetLevel(logger.ERROR)
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.ErrorCF("agent", "gate-r2-save-log-control", map[string]any{"session_id": child.SessionID})
	var observed [2]opencode1895LiveObservation
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "single process startup barrier missing", func() bool { return o.ready })
	}
	store := al.ResolveSessionStore(child.SessionID)
	restore, failedPath := opencodeGateBlockTranscript(t, store, child.SessionID)
	before := agent.TranscriptWriteFailures()
	_, err := fmt.Fprintln(process, "narrate")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "real post-append dispatcher barrier missing", func() bool {
			return opencodeGateObservedToolResult(t, o.raw, opencodeGateSaveBarrier)
		})
		require.Equal(t, opencodeGateLostNarration, o.text, "instrument: the single run's actual answer reached the viewers before completion")
		require.Empty(t, o.done, "instrument: the save-failing process has not finalized yet")
	}
	require.Greater(t, agent.TranscriptWriteFailures(), before, "instrument: real archive appender failed and latched the save error")
	restore()
	require.NoError(t, store.AppendTranscriptStrict(child.SessionID, session.TranscriptEntry{
		ID: "r3-save-storage-restored", Type: session.EntryTypeSystem, AgentID: opencodeFix1895Worker,
		Content: "Storage restored before single-turn finalize", Timestamp: time.Now().UTC(),
	}), "instrument: final notice can be saved; restoring storage must not erase the original save latch")
	opencodeFix1895Finish(t, al, child, process) // Finishes and joins THIS process; no steering/continuation.
	for i, viewer := range viewers {
		o := &observed[i]
		o.drain(t, viewer, child.SessionID)
		assert.Equal(t, opencodeGateLostNarration, o.text, "there was no continuation answer")
		require.NotEmpty(t, o.tokens)
		require.NotNil(t, o.tokens[0].TurnId)
		turnID := *o.tokens[0].TurnId
		require.NotEmpty(t, turnID)
		for _, token := range o.tokens {
			assert.Equal(t, &turnID, token.TurnId, "all live text belongs to the single producing turn")
		}
		if assert.Len(t, o.done, 1, "real finalize publishes exactly one done, not a replacement turn's done") {
			assert.Equal(t, &turnID, o.done[0].TurnId)
			require.NotNil(t, o.done[0].Stats, "corrected ruling: actual done stats must expose the save failure")
			require.NotNil(t, o.done[0].Stats.TurnFailed)
			assert.True(t, *o.done[0].Stats.TurnFailed, "round-3 corrected ruling preserves native #1178 save-failure reporting")
		}
		if assert.Len(t, o.errors, 1, "viewer sees the save notice once, not a second run-failed notice") {
			require.NotNil(t, o.errors[0].Payload)
			assert.Equal(t, "transcript_save_failed", o.errors[0].Payload.LlmError.Code)
			assert.Equal(t, opencodeGateSaveChat, o.errors[0].Message)
			assert.NotContains(t, o.errors[0].Message, failedPath)
		}
	}
	assert.Equal(t, observed[0].raw, observed[1].raw, "both real viewers receive the same session stream")
	opencodeR3AssertSingleTurnParent(t, al, child.SessionID, failedPath)
	entries := opencodeGateReopen(t, store, child.SessionID)
	var warnings []string
	for _, entry := range entries {
		if entry.ErrorCode == "transcript_save_failed" {
			warnings = append(warnings, entry.Content)
		}
		assert.NotEqual(t, opencodeGateLostNarration, entry.Content, "history still lacks the genuinely lost answer row")
		assert.NotContains(t, entry.Content, failedPath)
	}
	assert.Equal(t, []string{opencodeGateSaveChat}, warnings, "classified viewer notice is saved exactly once")
	opencodeGateAssertSaveFailureLog(t, logPath, child.SessionID, failedPath)
}

func opencodeR3AssertSingleTurnParent(t *testing.T, al *agent.AgentLoop, sid, failedPath string) {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, rec.State, "withoutSaveFailure must keep the history error out of the run outcome")
	if assert.NotNil(t, rec.FinalDelivery) {
		assert.Equal(t, "final_answer", rec.FinalDelivery.Outcome)
	}
	inbox, _, more, err := al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), sid, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, inbox, 1)
	kind, err := inbox[0].Discriminator()
	require.NoError(t, err)
	require.Equal(t, "handback", kind, "the single run must deliver its answer instead of a fatal error")
	final, err := inbox[0].AsSessionMessageHandback()
	require.NoError(t, err)
	assert.Equal(t, gen.SessionMessageHandbackModeFinal, final.Mode)
	assert.Equal(t, opencodeGateSaveParent+"\n\n"+opencodeGateLostNarration, final.ResultSoFar)
	assert.NotContains(t, final.ResultSoFar, failedPath)
}
