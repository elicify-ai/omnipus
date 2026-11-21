package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder-approved #1857 W17, not a string copied from a runtime translation.
const opencodeGateSaveChat = "This turn finished, but I couldn't save its final state. Recent messages may be missing from your history; check the answer above before repeating anything."
const opencodeGateSaveParent = "The run finished, but its final state could not be saved. Recent messages may be missing from the history; check the result before repeating any part of the task."

// SILENT-FAILURE SFH-1: a later successful answer cannot repair a segment lost
// from the original turn's archive. Keep the whole process/queue/drain/hub real.
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
	var answers []string
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == opencode1895LiveAnswer {
			answers = append(answers, entry.Content)
		}
		assert.NotContains(t, entry.Content, failedPath, "filesystem cause never belongs in saved chat")
	}
	require.Equal(t, []string{opencode1895LiveAnswer}, answers, "control: the continuation itself succeeded and saved its new answer")
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
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleFailed, rec.State, "SFH-1: a later successful answer must not erase the outer execution's missing-history failure")
	inbox, _, more, err := al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), child.SessionID, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, inbox, 1)
	kind, err := inbox[0].Discriminator()
	require.NoError(t, err)
	if assert.Equal(t, "error", kind, "parent must receive a failure, not a successful handback") {
		failure, err := inbox[0].AsSessionMessageError()
		require.NoError(t, err)
		assert.True(t, failure.Fatal)
		assert.Contains(t, failure.Text, opencodeGateSaveParent, "parent must receive W17's neutral missing-history notice")
		assert.NotContains(t, failure.Text, failedPath)
	}
	assert.Equal(t, observed[0].raw, observed[1].raw)
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
