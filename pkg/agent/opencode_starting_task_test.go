package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1899 B / docs/agents.md, "A live instruction reaches an external worker
// running a task too": startup input waits without an interrupt and reaches
// the same CLI conversation after natural completion, inside the one task run.
// The U10b fixture supplies a real task-origin lifecycle and task loop; only
// its process double is replaced by the existing native-readiness/exit barrier.
func TestOpenCodeFix1899_StartingTaskMarksAndConsumesQueuedFollowupWithoutInterrupt(t *testing.T) {
	al, agent, _ := u10bTaskFixture(t)
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	al.sharedSessionStore = store
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	meta, err := store.NewSession(session.SessionTypeTask, "webchat", agent.ID)
	require.NoError(t, err)
	taskChatID := meta.ID
	taskRecord, err := al.GetSessionLifecycleStore().Load(u10bTaskChat)
	require.NoError(t, err)
	taskRecord.SessionID = taskChatID
	require.NoError(t, al.GetSessionLifecycleStore().Persist(taskRecord))
	agent.Subagents.Executor.CLI = "opencode"
	d := &opencodeFixDriver{started: make(chan struct{}), firstEvents: make(chan runner.RunEvent, 2)}
	previous := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return d, nil }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan taskRunResult, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		d.finishFirst()
		select {
		case <-joined:
		case <-time.After(10 * time.Second):
			t.Error("the real task dispatch did not join before fixture cleanup")
		}
		newExternalDriver = previous
	})
	go func() {
		defer close(joined)
		resp, runErr := al.processTaskDirectExternalCLI(ctx, agent, u10bTaskInput, "task-key", taskChatID, 0)
		done <- taskRunResult{resp, runErr}
	}()
	select {
	case <-d.started:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: the real task dispatch never reached the external process boundary")
	}
	rec, err := al.GetSessionLifecycleStore().Load(taskChatID)
	require.NoError(t, err)
	require.Equal(t, &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"}, rec.Origin,
		"instrument: this is a TASK-origin run, not the already-covered delegated helper")
	require.Nil(t, rec.SteeredBy, "instrument: no helper-parent path may consume this task's instruction")
	require.False(t, d.HasNativeConversation(), "instrument: the CLI is still starting with no native conversation")
	const followup = "deliver the TASK follow-up after original work completes"
	const savedID = "task-starting-human-row-1899"
	require.NoError(t, al.ResolveSessionStore(taskChatID).AppendTranscriptWithProvenance(taskChatID,
		session.TranscriptEntry{ID: savedID, Role: "user", Content: followup, Timestamp: time.Now().UTC()}, "person-1899"))
	require.True(t, al.deliverHumanHelperInput(bus.InboundMessage{
		Channel: "webchat", ChatID: taskChatID, SessionID: taskChatID,
		Content: followup, TranscriptEntryID: savedID, GatewayUserID: "person-1899",
	}), "the real human-message route must handle this live task chat")
	require.Equal(t, 1, al.pendingSteeringCountForScope(taskChatID), "#1899 B: retain exactly one accepted follow-up")
	assert.Zero(t, al.pendingSteeringCountForScope("task-key"), "the instruction belongs to the task chat, not an unrelated queue")
	holder := al.externalRunSessionIfPresent(taskChatID)
	require.NotNil(t, holder)
	holder.mu.Lock()
	marked, running := holder.steerInterrupt, holder.running
	holder.mu.Unlock()
	assert.True(t, marked, "#1899 B: a starting TASK needs its continuation mark even when no interrupt is sent")
	assert.True(t, running, "the original task run is still in flight")
	d.mu.Lock()
	interrupted, ended, runs, resumes := d.firstCtx.Err(), d.firstClosed, len(d.runOpts), len(d.resumeOpts)
	d.mu.Unlock()
	assert.NoError(t, interrupted, "#1899 B: never interrupt a CLI before it has a native conversation")
	assert.False(t, ended, "neither Cancel nor a premature completion may bypass the process-exit barrier")
	assert.Equal(t, 1, runs, "only the original task Run exists before natural completion")
	assert.Zero(t, resumes, "a queued follow-up cannot resume before the original run ends")

	d.finishFirst() // Only this process-edge barrier announces the native id and ends Run.
	select {
	case result := <-done:
		require.NoError(t, result.err, "the queued startup instruction must not fail the task")
		assert.Equal(t, "follow-up delivered", result.resp, "the same task call returns the continued conversation's answer")
	case <-time.After(10 * time.Second):
		t.Fatal("the task loop did not consume the queued follow-up after the original run ended")
	}
	<-joined
	d.mu.Lock()
	assert.Len(t, d.runOpts, 1, "never start a replacement native conversation")
	assert.Equal(t, []string{opencodeFixNativeID}, d.resumeIDs, "deliver once to the native conversation the original run announced")
	if assert.Len(t, d.resumeOpts, 1) {
		assert.Equal(t, 1, strings.Count(d.resumeOpts[0].Input, followup), "the complete follow-up reaches the CLI exactly once")
		assert.NotContains(t, d.resumeOpts[0].Input, u10bTaskInput, "do not replay the original task prompt")
		assert.Equal(t, d.runOpts[0].WorkDir, d.resumeOpts[0].WorkDir, "preserve the workspace across continuation")
		assert.Equal(t, d.runOpts[0].Model, d.resumeOpts[0].Model, "preserve the configured model")
		assert.Equal(t, d.runOpts[0].MaxTurns, d.resumeOpts[0].MaxTurns, "preserve the tool-step cap")
	}
	d.mu.Unlock()
	assert.Zero(t, al.pendingSteeringCountForScope(taskChatID), "consume the queued follow-up instead of stranding it")
	assert.False(t, al.takeExternalSteerInterrupt(taskChatID), "consume the task continuation mark exactly once")
}
