package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder-approved #1857 W17 contract sentences, referenced through the
// generated catalogue per team-lead's 2026-11-23 ruling, not runtime translation.
var (
	opencodeGateSaveChat   = gen.LLMErrorUserMessages["transcript_save_failed"]
	opencodeGateSaveParent = gen.LLMErrorNeutralMessages["transcript_save_failed"]
)

// CHANGED REQUIREMENT, not test weakening: ARCHITECT-RULING-R2.md section 1
// (/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/opencode-fixes/gate-20261121/ARCHITECT-RULING-R2.md).
// The answer must survive alongside the warning: Completed, not Failed. Keep
// every unaffected process/queue/drain/hub, notice, archive and privacy check.
func TestOpenCodeGateSFH1_SaveFailureSurvivesSuccessfulQueuedContinuation(t *testing.T) {
	resumeAddress, awaitResume := opencodeFix1895Gate(t)
	var messages *bus.MessageBus
	al, child, viewers, original := opencodeFix1895Launch(t, opencodeFix1895Program(t), "queued_save_failure", func(cfg *config.Config, actual *bus.MessageBus) {
		messages = actual
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == opencodeFix1895Worker {
				cfg.Agents.List[i].Subagents.Executor.CLIArgs += " --fixture-resume-address=" + resumeAddress
			}
		}
	})
	t.Cleanup(al.Close)
	owner, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "capture the real registered reader before the harness shuts down")
	reader, ok := owner.Tools.Get("delegate")
	require.True(t, ok)
	logPath := filepath.Join(t.TempDir(), "save-failure.jsonl")
	level := logger.GetLevel()
	logger.SetLevel(logger.ERROR)
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.ErrorCF("agent", "gate-r2-save-log-control", map[string]any{"session_id": child.SessionID})
	var observed [2]opencode1895LiveObservation
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "original startup readiness missing", func() bool { return o.ready })
	}
	events := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(events.ID) })
	ctx, stopIntake := opencode1895QueuedTextIntake(t, al)
	opencode1895PublishPerson(t, ctx, al, messages, child.SessionID, "gate-sfh-person-row")
	opencode1895AwaitQueuedPerson(t, events.C, child.SessionID)
	stopIntake() // No interrupt decision can race with the later native-id announcement.
	opencodeGateProbeStarting(t, original)

	store := al.ResolveSessionStore(child.SessionID)
	restore, failedPath := opencodeGateBlockTranscript(t, store, child.SessionID)
	beforeFailures := agent.TranscriptWriteFailures()
	_, err := fmt.Fprintln(original, "narrate")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "post-narration dispatcher barrier missing", func() bool {
			return opencodeGateObservedToolResult(t, o.raw, opencodeGateSaveBarrier)
		})
		require.Equal(t, opencodeGateLostNarration, o.text, "instrument: failed-to-save text was actually shown live")
	}
	require.Greater(t, agent.TranscriptWriteFailures(), beforeFailures, "instrument: real transcript writer attempted and failed, not just a preflight probe")
	restore()
	const repairID = "gate-sfh-restored-write"
	require.NoError(t, store.AppendTranscriptStrict(child.SessionID, session.TranscriptEntry{
		ID: repairID, Type: session.EntryTypeSystem, AgentID: opencodeFix1895Worker,
		Content: "Storage restored before original completion", Timestamp: time.Now().UTC(),
	}), "instrument: actual archive appender is writable again BEFORE continuation")
	beforeResume := opencodeGateReopen(t, store, child.SessionID)
	var repairIDs []string
	for _, entry := range beforeResume {
		assert.NotEqual(t, opencodeGateLostNarration, entry.Content, "the original live narration really was not saved")
		if entry.ID == repairID {
			repairIDs = append(repairIDs, entry.ID)
		}
	}
	require.Equal(t, []string{repairID}, repairIDs)
	_, err = fmt.Fprintln(original, "finish")
	require.NoError(t, err)
	resumed := awaitResume() // Same native conversation and new-input-only checked inside the real CLI fixture.
	_, err = fmt.Fprintln(resumed, "answer")
	require.NoError(t, err)
	for i, viewer := range viewers {
		o := &observed[i]
		o.readUntil(t, viewer, child.SessionID, "successful continuation answer missing", func() bool {
			return o.text == opencodeGateLostNarration+opencode1895LiveAnswer
		})
	}
	opencode1895ProbePaused(t, resumed)
	opencodeFix1895Finish(t, al, child, resumed)
	entries := opencodeGateReopen(t, store, child.SessionID)
	var answers, savedWarnings []string
	for _, entry := range entries {
		if entry.ErrorCode == "transcript_save_failed" {
			savedWarnings = append(savedWarnings, entry.Content)
		}
		if entry.Role == "assistant" && entry.Content == opencode1895LiveAnswer {
			answers = append(answers, entry.Content)
		}
		assert.NotContains(t, entry.Content, failedPath, "filesystem cause never belongs in saved chat")
	}
	require.Equal(t, []string{opencode1895LiveAnswer}, answers, "control: the continuation itself succeeded and saved its new answer")
	assert.Equal(t, []string{opencodeGateSaveChat}, savedWarnings, "the original turn's classified notice survives reopening exactly once")
	for i, viewer := range viewers {
		o := &observed[i]
		o.drain(t, viewer, child.SessionID)
		var saveNotices []gen.ErrorFrame
		for _, frame := range o.errors {
			require.NotNil(t, frame.Payload)
			if frame.Payload.LlmError.Code == "transcript_save_failed" {
				saveNotices = append(saveNotices, frame)
			}
			assert.NotContains(t, frame.Message, failedPath)
			if frame.Payload.LlmError.Detail != nil {
				assert.NotContains(t, *frame.Payload.LlmError.Detail, failedPath)
			}
		}
		if assert.Len(t, saveNotices, 1, "SFH-1: both helper viewers must see the original save failure despite the successful follow-up") {
			assert.Equal(t, opencodeGateSaveChat, saveNotices[0].Message)
		}
		if assert.Len(t, o.done, 1, "only the continuation's final done closes the helper") {
			if stats := o.done[0].Stats; stats != nil && stats.TurnFailed != nil {
				assert.False(t, *stats.TurnFailed, "R1-d: incomplete history must not label a real answer as engine failure")
			}
		}
	}
	opencodeGateAssertR2ParentAnswer(t, al, reader, child.SessionID, failedPath)
	opencodeGateAssertSaveFailureLog(t, logPath, child.SessionID, failedPath)
	assert.Equal(t, observed[0].raw, observed[1].raw)
}

func opencodeGateAssertR2ParentAnswer(t *testing.T, al *agent.AgentLoop, reader tools.Tool, sid, failedPath string) {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, rec.State, "CHANGED REQUIREMENT R1-a: save failure does not discard a successful answer")
	if assert.NotNil(t, rec.FinalDelivery, "a final result is committed independently of the history rows") {
		assert.Equal(t, "final_answer", rec.FinalDelivery.Outcome)
	}
	inbox, _, more, err := al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), sid, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, inbox, 1)
	kind, err := inbox[0].Discriminator()
	require.NoError(t, err)
	want := opencodeGateSaveParent + "\n\n" + opencode1895LiveAnswer
	if assert.Equal(t, "handback", kind, "CHANGED REQUIREMENT R1-b: warning plus answer, not a fatal error that discards it") {
		final, decodeErr := inbox[0].AsSessionMessageHandback()
		require.NoError(t, decodeErr)
		assert.Equal(t, gen.SessionMessageHandbackModeFinal, final.Mode)
		assert.Equal(t, want, final.ResultSoFar, "neutral warning comes first, verbatim and exactly once")
		assert.NotContains(t, final.ResultSoFar, failedPath)
	}
	result := reader.Execute(tools.WithTranscriptSessionID(context.Background(), rec.SteeringSessionID()), map[string]any{
		"action": "result", "session_id": sid,
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "%s", result.ForLLM)
	assert.True(t, strings.HasSuffix(result.ForLLM, want), "delegate result must expose the exact same warning plus answer: %s", result.ForLLM)
	assert.Equal(t, 1, strings.Count(result.ForLLM, opencodeGateSaveParent))
	assert.NotContains(t, result.ForLLM, failedPath)
}

func opencodeGateAssertSaveFailureLog(t *testing.T, path, sid, cause string) {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var failures []map[string]any
	control := 0
	for scanner.Scan() {
		var row map[string]any // not-wire-format: structured test log observation
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		if row["session_id"] != sid {
			continue
		}
		if row["message"] == "gate-r2-save-log-control" {
			control++
		}
		if row["message"] == "external-cli: a helper message could not be saved to the transcript" {
			failures = append(failures, row)
		}
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 1, control, "instrument: the real structured error logger reaches this file")
	if assert.Len(t, failures, 1, "one error log per affected turn") {
		assert.Equal(t, "error", failures[0]["level"])
		assert.NotEmpty(t, failures[0]["turn_id"])
		assert.Contains(t, failures[0]["error"], cause)
	}
}

func opencodeGateProbeStarting(t *testing.T, process net.Conn) {
	t.Helper()
	require.NoError(t, process.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := fmt.Fprintln(process, "probe")
	require.NoError(t, err)
	reply, err := bufio.NewReader(process).ReadString('\n')
	require.NoError(t, err, "queued startup input must not interrupt the original process")
	require.Equal(t, "still-starting-without-native-id\n", reply)
	require.NoError(t, process.SetDeadline(time.Time{}))
}

func opencodeGateObservedToolResult(t *testing.T, frames [][]byte, callID string) bool {
	t.Helper()
	for _, raw := range frames {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields))
		var kind string
		require.NoError(t, json.Unmarshal(fields["type"], &kind))
		if kind == "tool_call_result" {
			var result gen.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(raw, &result))
			if result.CallId == callID {
				return true
			}
		}
	}
	return false
}

func opencodeGateBlockTranscript(t *testing.T, store *session.UnifiedStore, sid string) (func(), string) {
	t.Helper()
	path := filepath.Join(store.BaseDir(), sid, "transcript.jsonl")
	backup := filepath.Join(t.TempDir(), "original-transcript.saved")
	require.NoError(t, os.Rename(path, backup), "earlier real admission/user rows must already have been saved")
	require.NoError(t, os.Mkdir(path, 0o700)) // Directory fault works even under root; no permission assumptions.
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if file != nil {
		require.NoError(t, file.Close())
	}
	require.Error(t, err, "instrument: the exact archive append path must refuse writes")
	return restore, path
}
