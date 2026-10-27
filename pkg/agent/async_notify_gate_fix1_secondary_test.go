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
