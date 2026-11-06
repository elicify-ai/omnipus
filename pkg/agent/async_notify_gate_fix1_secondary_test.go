package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// SF-C2's exact safe field/message oracle is the bounded architect addendum.
// This drives the real finished-tool callback; it never invokes Notify directly.
func TestGateFix1FinishedCallbackReportsSecondaryNoticeSaveFailure(t *testing.T) {
	// The three-attempt delivery oracle below (require.Len(primary, 3)) needs
	// three REAL publish attempts; shorten only the wall-clock timers so they
	// are real but fast (see b3ShortenPublishRetryTimers).
	b3ShortenPublishRetryTimers(t)
	const nativeToolName = "gate1_async_notice_probe"
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{{ID: "gate1-real-native-async-call", Function: &providers.FunctionCall{Name: nativeToolName, Arguments: "{}"}}}).WithText("gate1 native producer completed")
	al, mb, _ := newAsyncChildPublicationTestLoop(t, provider, false)
	probe := &b3AsyncTool{al: al, name: nativeToolName}
	al.RegisterTool(probe)
	setAskPolicyForAllAgents(t, al, probe.Name(), config.ToolPolicyAllow)
	t.Cleanup(func() {
		b3DrainInbound(mb)
		for _, invocation := range probe.invocations() {
			invocation.open()
			b3DrainInbound(mb)
			b3JoinTool(t, invocation)
		}
		mb.Close()
	})
	_, err := al.ProcessDirectWithChannelAs(context.Background(), testDefaultAgentID, "gate1 native callback", "agent:"+testDefaultAgentID+":telegram:gate1", "telegram", "gate1-native-conversation")
	require.NoError(t, err)
	calls := probe.invocations()
	require.Len(t, calls, 1, "BLOCKED: native loop must really execute the registered asynchronous tool")
	invocation := calls[0]
	require.NoError(t, invocation.readErr)
	require.NotEmpty(t, invocation.id)
	require.NotEmpty(t, invocation.sid)
	require.NotNil(t, invocation.owner)
	select {
	case <-invocation.owner.done:
	case <-time.After(b3JoinWatchdog):
		t.Fatal("BLOCKED: actual producing owner did not finish")
	}
	invocation.owner.mu.Lock()
	settlement := invocation.owner.result
	invocation.owner.mu.Unlock()
	require.NoError(t, settlement)
	require.Nil(t, al.ownLiveTurn(invocation.sid))
	rec, err := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()).Load(invocation.sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, rec.State)
	logPath := filepath.Join(t.TempDir(), "native-secondary.jsonl")
	oldLevel := logger.GetLevel()
	require.NoError(t, logger.EnableFileLogging(logPath))
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(oldLevel) })
	var observed atomic.Int32
	al.asyncNotifier.registerObserver(func(AsyncNotifyEvent) { observed.Add(1) })
	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)
	store := al.GetSessionStore()
	partition := filepath.Join(store.BaseDir(), invocation.sid, "transcript.jsonl")
	st, err := os.Stat(partition)
	require.NoError(t, err, "BLOCKED: native canonical archive must exist")
	require.True(t, st.Mode().IsRegular())
	backup := partition + ".gate1-secondary-owned-backup"
	require.NoError(t, os.Rename(partition, backup))
	restored := false
	restore := func() {
		if restored {
			return
		}
		require.NoError(t, os.Remove(partition))
		require.NoError(t, os.Rename(backup, partition))
		restored = true
	}
	require.NoError(t, os.Mkdir(partition, 0700))
	t.Cleanup(restore)
	_, fault := store.ReadTranscript(invocation.sid)
	require.Error(t, fault, "BLOCKED: owned canonical partition fault must be genuine")
	b3FillInbound(t, mb)
	invocation.open()
	b3JoinTool(t, invocation)
	restore()
	require.Equal(t, int32(1), observed.Load(), "actual callback must notify observers once")
	require.Len(t, probe.invocations(), 1, "failed notification must never reexecute the native tool")
	require.Empty(t, b3Rows(t, al, invocation.sid), "failed secondary append cannot be reported as a durable warning")
	primary := b3Logs(t, logPath, b3AttemptMessage)
	require.Len(t, primary, 3, "BLOCKED: real callback must exhaust the original bounded handoff")
	b3AssertDiagnostic(t, primary, false, "asynchronous update", invocation.sid, probe.Name(), invocation.id)
	queued := 0
	for {
		select {
		case event := <-sub.C:
			if event.Kind == EventKindFollowUpQueued {
				queued++
			}
		default:
			goto drained
		}
	}
drained:
	require.Equal(t, 0, queued)
	records := b3Logs(t, logPath, "Async notification secondary warning failed")
	require.Len(t, records, 1, "actual finished callback must expose its secondary notice-save failure on the existing safe native sink")
	record := records[0]
	require.Equal(t, "error", record["level"])
	require.Equal(t, "agent", record["component"])
	require.Equal(t, "asynchronous update", record["source"])
	require.Equal(t, primary[0]["notice_id"], record["notice_id"])
	require.Equal(t, "notice_save", record["stage"])
	require.Equal(t, "secondary_warning_failure", record["error_class"])
	allowed := map[string]bool{"level": true, "time": true, "caller": true, "component": true, "message": true, "source": true, "notice_id": true, "stage": true, "error_class": true, "tool": true, "tool_ref": true, "session_ref": true}
	for key := range record {
		require.True(t, allowed[key], "secondary diagnostic must contain only the architect-approved content-free fields")
	}
	if value, ok := record["tool"]; ok {
		require.Equal(t, probe.Name(), value)
	}
	if value, ok := record["tool_ref"]; ok {
		require.Equal(t, b3Ref(invocation.id), value)
	}
	if value, ok := record["session_ref"]; ok {
		require.Equal(t, b3Ref(invocation.sid), value)
	}
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), b3PrivateBody)
	require.NotContains(t, string(encoded), invocation.sid)
	require.NotContains(t, string(encoded), invocation.id)
	b3DrainInbound(mb)
}

// gate1OriginCompletedProducer reuses the real registered async producer and
// its actual ordinary-root callback identity. No Notify call or audience mock.
func gate1OriginCompletedProducer(t *testing.T) (*AgentLoop, *b3AsyncTool, *b3ToolInvocation, string) {
	t.Helper()
	const name = "gate1_origin_async_probe"
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{{ID: "gate1-real-origin-async-call", Function: &providers.FunctionCall{Name: name, Arguments: "{}"}}}).WithText("gate1 origin producer completed")
	al, mb, _ := newAsyncChildPublicationTestLoop(t, provider, false)
	probe := &b3AsyncTool{al: al, name: name}
	al.RegisterTool(probe)
	setAskPolicyForAllAgents(t, al, probe.Name(), config.ToolPolicyAllow)
	t.Cleanup(func() {
		b3DrainInbound(mb)
		for _, invocation := range probe.invocations() {
			invocation.open()
			b3DrainInbound(mb)
			b3JoinTool(t, invocation)
		}
		mb.Close()
	})
	_, err := al.ProcessDirectWithChannelAs(context.Background(), testDefaultAgentID, "gate1 native origin callback", "agent:"+testDefaultAgentID+":telegram:gate1-origin", "telegram", "gate1-origin-conversation")
	require.NoError(t, err)
	calls := probe.invocations()
	require.Len(t, calls, 1, "BLOCKED: actual loop must execute the native async producer once")
	invocation := calls[0]
	require.NoError(t, invocation.readErr)
	require.NotEmpty(t, invocation.id)
	require.NotEmpty(t, invocation.sid)
	require.NotNil(t, invocation.owner)
	select {
	case <-invocation.owner.done:
	case <-time.After(b3JoinWatchdog):
		t.Fatal("BLOCKED: actual producing owner did not fully complete")
	}
	invocation.owner.mu.Lock()
	settled := invocation.owner.result
	invocation.owner.mu.Unlock()
	require.NoError(t, settled)
	require.Nil(t, al.ownLiveTurn(invocation.sid))
	rec, err := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()).Load(invocation.sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, rec.State)
	allowed, err := al.asyncNotifyOriginReachesUser(context.Background(), invocation.sid)
	require.NoError(t, err, "BLOCKED: initial native canonical origin must be readable")
	require.True(t, allowed, "BLOCKED: actual producer must begin with a legitimate external-user origin")
	path := filepath.Join(t.TempDir(), "native-origin-secondary.jsonl")
	level := logger.GetLevel()
	require.NoError(t, logger.EnableFileLogging(path))
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	return al, probe, invocation, path
}

// A failed canonical read is not the same as an intentional nil-error refusal.
// The architect's existing two-stage diagnostic contract distinguishes them.
func TestGateFix1FinishedCallbackOriginReadDiagnostics(t *testing.T) {
	// Both subtests exhaust the original three handoffs (require.Len(primary, 3))
	// through the real finished callback; shorten only the wall-clock timers
	// (see b3ShortenPublishRetryTimers). Cleanup restores after both subtests.
	b3ShortenPublishRetryTimers(t)
	for _, failRead := range []bool{true, false} {
		label := "InitialTaskAudienceRefusal"
		if failRead {
			label = "OriginReadFailure"
		}
		t.Run(label, func(t *testing.T) {
			var al *AgentLoop
			var probe *b3AsyncTool
			var invocation *b3ToolInvocation
			var logPath string
			if failRead {
				al, probe, invocation, logPath = gate1OriginCompletedProducer(t)
			} else {
				al, probe, invocation, logPath = gate1InitialTaskOriginProducer(t)
			}
			lifecycle := al.GetSessionLifecycleStore()
			store := al.GetSessionStore()
			_, err := store.ReadTranscript(invocation.sid)
			require.NoError(t, err)
			var observed atomic.Int32
			al.asyncNotifier.registerObserver(func(AsyncNotifyEvent) { observed.Add(1) })
			sub := al.SubscribeEvents(32)
			defer al.UnsubscribeEvents(sub.ID)
			restore := func() {}
			if failRead {
				// Actual LifecycleStore.path suffix is .jsonl, independent of the
				// healthy canonical transcript and session metadata directory.
				path := filepath.Join(lifecycle.Dir(), invocation.sid+".jsonl")
				st, statErr := os.Stat(path)
				require.NoError(t, statErr, "BLOCKED: actual canonical lifecycle file must exist")
				require.True(t, st.Mode().IsRegular())
				backup := path + ".gate1-origin-owned-backup"
				require.NoError(t, os.Rename(path, backup))
				restored := false
				restore = func() {
					if restored {
						return
					}
					require.NoError(t, os.Remove(path))
					require.NoError(t, os.Rename(backup, path))
					restored = true
				}
				require.NoError(t, os.Mkdir(path, 0700))
				t.Cleanup(restore)
				_, readErr := session.NewLifecycleStore(lifecycle.Dir()).Load(invocation.sid)
				require.Error(t, readErr, "BLOCKED: owned canonical origin read must really fail")
				allowed, originErr := al.asyncNotifyOriginReachesUser(context.Background(), invocation.sid)
				require.Error(t, originErr, "BLOCKED: actual origin predicate must return the genuine read error")
				require.False(t, allowed)
			} else {
				// Founder Q1 A: the real task origin was created before admission;
				// never mutate a completed record or fabricate execution authority.
				rec, loadErr := session.NewLifecycleStore(lifecycle.Dir()).Load(invocation.sid)
				require.NoError(t, loadErr)
				require.NotNil(t, rec.Origin)
				require.Equal(t, session.OriginKindTask, rec.Origin.Kind)
				allowed, originErr := al.asyncNotifyOriginReachesUser(context.Background(), invocation.sid)
				require.NoError(t, originErr, "BLOCKED: control must be a refusal without a read error")
				require.False(t, allowed)
			}
			_, err = store.ReadTranscript(invocation.sid)
			require.NoError(t, err, "BLOCKED: notice persistence must remain independently healthy")
			b3FillInbound(t, al.bus)
			invocation.open()
			b3JoinTool(t, invocation)
			restore()
			_, err = session.NewLifecycleStore(lifecycle.Dir()).Load(invocation.sid)
			require.NoError(t, err)
			require.Equal(t, int32(1), observed.Load())
			require.Len(t, probe.invocations(), 1, "notification error must not reexecute the actual native tool")
			primary := b3Logs(t, logPath, b3AttemptMessage)
			require.Len(t, primary, 3, "BLOCKED: actual callback must exhaust the original three handoffs")
			b3AssertDiagnostic(t, primary, false, "asynchronous update", invocation.sid, probe.Name(), invocation.id)
			saved := b3Rows(t, al, invocation.sid)
			require.Len(t, saved, 1, "BLOCKED: only origin reading may fail; the warning must really be saved")
			require.Equal(t, primary[0]["notice_id"], saved[0].ID)
			require.Equal(t, session.EntryTypeSystem, saved[0].Type)
			require.Equal(t, "system", saved[0].Role)
			require.Empty(t, saved[0].Status)
			require.NotEmpty(t, saved[0].Content)
			require.NotContains(t, saved[0].Content, b3PrivateBody)
			queued := 0
			for {
				select {
				case event := <-sub.C:
					if event.Kind == EventKindFollowUpQueued {
						queued++
					}
				default:
					goto drained
				}
			}
		drained:
			require.Equal(t, 0, queued)
			require.Empty(t, b3DrainOutbound(al.bus), "an unreadable/refused canonical origin must never be guessed or rerouted")
			records := b3Logs(t, logPath, "Async notification secondary warning failed")
			if failRead {
				require.Len(t, records, 1, "actual finished callback must report exactly one canonical-origin read failure")
				gate1AssertOriginSecondaryFields(t, records[0], primary[0]["notice_id"], probe.Name(), invocation)
			} else {
				require.Empty(t, records, "nil-error canonical audience refusal must not manufacture a secondary failure diagnostic")
			}
			b3DrainInbound(al.bus)
		})
	}
}

func gate1AssertOriginSecondaryFields(t *testing.T, record map[string]any, noticeID any, tool string, invocation *b3ToolInvocation) {
	t.Helper()
	require.Equal(t, "error", record["level"])
	require.Equal(t, "agent", record["component"])
	require.Equal(t, "Async notification secondary warning failed", record["message"])
	require.Equal(t, "asynchronous update", record["source"])
	require.Equal(t, noticeID, record["notice_id"])
	require.Equal(t, "origin_read", record["stage"])
	require.Equal(t, "secondary_warning_failure", record["error_class"])
	allowed := map[string]bool{"level": true, "time": true, "caller": true, "component": true, "message": true, "source": true, "notice_id": true, "stage": true, "error_class": true, "tool": true, "tool_ref": true, "session_ref": true}
	for key := range record {
		require.True(t, allowed[key], "secondary record must contain only the architect-approved safe fields")
	}
	if value, ok := record["tool"]; ok {
		require.Equal(t, tool, value)
	}
	if value, ok := record["tool_ref"]; ok {
		require.Equal(t, b3Ref(invocation.id), value)
	}
	if value, ok := record["session_ref"]; ok {
		require.Equal(t, b3Ref(invocation.sid), value)
	}
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), b3PrivateBody)
	require.NotContains(t, string(encoded), invocation.sid)
	require.NotContains(t, string(encoded), invocation.id)
}

// gate1InitialTaskOriginProducer is used ONLY by the founder-authorized new
// refusal control. Native launch/admission mint all ownership and execution IDs.
func gate1InitialTaskOriginProducer(t *testing.T) (*AgentLoop, *b3AsyncTool, *b3ToolInvocation, string) {
	t.Helper()
	const name = "gate1_initial_task_async_probe"
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{{ID: "gate1-real-initial-task-call", Function: &providers.FunctionCall{Name: name, Arguments: "{}"}}}).WithText("gate1 initial task producer completed")
	al, mb, _ := newAsyncChildPublicationTestLoop(t, provider, false)
	probe := &b3AsyncTool{al: al, name: name}
	al.RegisterTool(probe)
	setAskPolicyForAllAgents(t, al, probe.Name(), config.ToolPolicyAllow)
	t.Cleanup(func() {
		b3DrainInbound(mb)
		for _, invocation := range probe.invocations() {
			invocation.open()
			b3DrainInbound(mb)
			b3JoinTool(t, invocation)
		}
		mb.Close()
	})
	// This existing typed native helper calls the real SteerLauncher and
	// supplies its required task ID; it does not manufacture a record.
	launched, err := launchAs(t, al, "task")
	require.NoError(t, err, "BLOCKED: initial canonical task launch must succeed")
	initial, err := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()).Load(launched.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, initial.State)
	require.Equal(t, launched.Generation, initial.Generation)
	require.Nil(t, initial.ExecutionID)
	require.NotNil(t, initial.Origin)
	require.Equal(t, session.OriginKindTask, initial.Origin.Kind)
	require.NotEmpty(t, initial.Origin.TaskID)
	meta, err := al.GetSessionStore().GetMeta(launched.SessionID)
	require.NoError(t, err)
	require.Equal(t, initial.Origin.TaskID, meta.TaskID)
	require.Equal(t, initial.AgentID, meta.AgentID)
	// Actual owner-pinned, concrete-session non-human entry, with a real
	// messaging address captured by the callback. No route fallback is used.
	_, err = al.ProcessScheduled(context.Background(), testDefaultAgentID, launched.SessionID, "gate1 native initial task", "telegram", "gate1-initial-task-conversation")
	require.NoError(t, err)
	calls := probe.invocations()
	require.Len(t, calls, 1, "BLOCKED: actual task must execute the native async producer once")
	invocation := calls[0]
	require.NoError(t, invocation.readErr)
	require.Equal(t, launched.SessionID, invocation.sid)
	require.NotEmpty(t, invocation.id)
	require.NotNil(t, invocation.owner)
	channelType, _ := config.ParseInstanceKey(invocation.channel)
	require.True(t, isMessagingChannel(channelType), "BLOCKED: control must reach canonical audience refusal, not non-messaging preflight")
	require.NotEmpty(t, invocation.chat)
	select {
	case <-invocation.owner.done:
	case <-time.After(b3JoinWatchdog):
		t.Fatal("BLOCKED: actual producing task owner did not fully complete")
	}
	invocation.owner.mu.Lock()
	settled := invocation.owner.result
	invocation.owner.mu.Unlock()
	require.NoError(t, settled)
	require.Nil(t, al.ownLiveTurn(invocation.sid))
	completed, err := session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()).Load(invocation.sid)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, completed.State)
	require.NotNil(t, completed.Origin)
	require.Equal(t, session.OriginKindTask, completed.Origin.Kind)
	require.Equal(t, initial.Origin.TaskID, completed.Origin.TaskID)
	allowed, err := al.asyncNotifyOriginReachesUser(context.Background(), invocation.sid)
	require.NoError(t, err, "BLOCKED: initial task refusal must be nil-error with real canonical reads")
	require.False(t, allowed)
	path := filepath.Join(t.TempDir(), "native-initial-task-secondary.jsonl")
	level := logger.GetLevel()
	require.NoError(t, logger.EnableFileLogging(path))
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	return al, probe, invocation, path
}
