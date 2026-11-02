package agent

// 6c UAT follow-up: chief's U-D1-6 (a)/(b), B93 of the history-paging spec,
// and team-lead's explicit steered-boot, ask-lifecycle and cancel/write cases.
// All stores, receipt writers, approval/dispatch stages and recovery are real.
// Only external model/tool/user responses and a serialization delay are controlled.
import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Read the physical source, not a deduplicated display. Checkpoints are taken
// while the external approval/tool is parked, or after every writer has joined.
func uat6cOnlyToolSource(t *testing.T, store *session.UnifiedStore, sid string) (session.ArchiveAddress, session.TranscriptEntry) {
	t.Helper()
	raw, err := session.NewArchiveDayStore(store.BaseDir(), sid)
	require.NoError(t, err)
	var addresses []session.ArchiveAddress
	var entries []session.TranscriptEntry
	require.NoError(t, raw.ScanAll(func(addr session.ArchiveAddress, rec session.ArchiveRecord) bool {
		if rec.Type == session.EntryTypeToolCall {
			for _, call := range rec.ToolCalls {
				if call.ID == uat6cCall {
					addresses = append(addresses, addr)
					entries = append(entries, rec.TranscriptEntry)
				}
			}
		}
		return true
	}))
	require.Len(t, addresses, 1, "one physical tool_call source per call ID; effects must not conceal duplicates")
	return addresses[0], entries[0]
}

func uat6cReceive[T any](t *testing.T, ch <-chan T, stage string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("instrument: %s did not complete", stage)
		var zero T
		return zero
	}
}

func Test6cUAT_UD16_e_SteeredCrashLeftRunningRecordReloadsInterrupted(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	parent := f.sid
	child, err := f.store.NewSession(session.SessionTypeDelegate, "delegate", "mia")
	require.NoError(t, err)
	require.NoError(t, f.store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parent}))
	f.sid = child.ID
	f.newTurn()
	f.append(t, uat6cAEntry(t))
	f.begin(t) // genuine dispatch; deliberately no completion or cancellation
	f.append(t, uat6cSteerEntry(t))
	running := session.ToolCall{ID: uat6cCall, Tool: "bash", Status: "running", Parameters: map[string]any{"command": uat6cCmd}}
	require.Equal(t, []session.ToolCall{running}, toolCallsFor(t, f.store, f.sid, uat6cCall))
	address, source := uat6cOnlyToolSource(t, f.store, f.sid)
	oldBoot := session.NewBootEpochStore(f.home)
	oldEpoch, err := oldBoot.Mint()
	require.NoError(t, err)
	lifecycleDir := filepath.Join(f.home, "lifecycle")
	lifecycle := session.NewLifecycleStore(lifecycleDir)
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: parent, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "ws", AgentID: "mia",
	}))
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: f.sid, Generation: 1, State: session.LifecycleRunning,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent, RootSessionID: parent},
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parent,
		WorkspaceID: "ws", AgentID: "mia", ParentAgentID: "mia",
		ExecutionID: &session.ExecutionIdentity{RunID: uat6cTurn, BootSeq: oldEpoch},
	}))

	f.reopen(t) // discard startedTool/callRecords; reload the durable parent edge too
	lifecycle = session.NewLifecycleStore(lifecycleDir)
	boot := session.NewBootEpochStore(f.home)
	currentEpoch, err := boot.Mint()
	require.NoError(t, err)
	require.Greater(t, currentEpoch, oldEpoch, "instrument: this is a different physical boot")
	classifier := NewSteerRecordClassifier(lifecycle, f.store)
	class, err := classifier.Classify(context.Background(), f.sid)
	require.NoError(t, err)
	require.Equal(t, steer.ClassSteered, class, "instrument: exercise recoverSteered, never the ordinary-root arm")
	inbox := session.NewMessageInboxStore(filepath.Join(f.home, "inbox"))
	var notices []string
	recovery := &SteerBootRecovery{
		Lifecycle: lifecycle, Sessions: f.store, Inbox: inbox, BootEpoch: boot, Classifier: classifier,
		Deliverer:      &bootRecordingDeliverer{inbox: inbox, lifecycle: lifecycle},
		OperatorNotice: func(message string) { notices = append(notices, message) },
	}
	require.NoError(t, recovery.Run(context.Background()))
	// As in boot_recovery_adr091_test's recording harness, parent wake/resume
	// is deliberately unwired. Pin its diagnostics; do not silently tolerate
	// an unrelated boot refusal or claim this test certifies helper resumption.
	require.Equal(t, []string{
		"helper " + f.sid + " resume did not start: stopped-child notice deliverer is not wired",
		"session " + f.sid + " stopped-child notice was not delivered: stopped-child notice deliverer is not wired",
	}, notices)
	want := running
	want.Status = "interrupted"
	assert.Equal(t, []session.ToolCall{want}, toolCallsFor(t, f.store, f.sid, uat6cCall), "a steered crash cannot leave a running spinner")
	gotAddress, gotSource := uat6cOnlyToolSource(t, f.store, f.sid)
	assert.Equal(t, address, gotAddress, "interrupt in place at the original source address")
	assert.Equal(t, source, gotSource, "the original running source is immutable; a settle effect changes its display")
	entries := f.entries(t)
	require.GreaterOrEqual(t, len(entries), 3)
	assert.Equal(t, []string{uat6cA, string(uat6cCall), uat6cSteer}, uat6cIDs(entries[:3]), "restart notices must not move the tool below the steer")
	record, err := lifecycle.Load(f.sid)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, record.State, "D8.3: the execution stops, not the session's permanent lifetime")
	require.NotNil(t, record.StopNote)
	assert.Equal(t, session.StopCauseRestart, record.StopNote.Cause)
	assert.Equal(t, currentEpoch, record.StopNote.BootSeq)
	before := f.archiveBytes(t)
	require.NoError(t, recovery.Run(context.Background()))
	assert.Equal(t, before, f.archiveBytes(t), "a repeat boot pass cannot append a second interrupt/source")
	f.reopen(t)
	assert.Equal(t, []session.ToolCall{want}, toolCallsFor(t, f.store, f.sid, uat6cCall), "interruption survives another reload")
}

type uat6cAllowOnceApprover struct {
	requests chan PolicyApprovalReq
	allow    <-chan struct{}
}

func (a *uat6cAllowOnceApprover) RequestApproval(ctx context.Context, req PolicyApprovalReq) (bool, string, bool) {
	select {
	case a.requests <- req:
	case <-ctx.Done():
		return false, "cancel", false
	}
	select {
	case <-a.allow:
		return true, "", false // a human Allow once, never a standing grant
	case <-ctx.Done():
		return false, "cancel", false
	}
}

const uat6cAskTool = "uat6c_approved_tool"

type uat6cParkedTool struct {
	tools.BaseTool
	entered chan map[string]any
	release <-chan struct{}
	calls   atomic.Int32
}

func (*uat6cParkedTool) Name() string { return uat6cAskTool }
func (*uat6cParkedTool) Description() string {
	return "Controlled external operation for 6c ask lifecycle"
}
func (*uat6cParkedTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*uat6cParkedTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"}}
}
func (tool *uat6cParkedTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tool.calls.Add(1)
	select {
	case tool.entered <- args:
	case <-ctx.Done():
		return tools.ErrorResult("external operation cancelled")
	}
	select {
	case <-tool.release:
		return &tools.ToolResult{ForLLM: "approved operation completed"}
	case <-ctx.Done():
		return tools.ErrorResult("external operation cancelled")
	}
}

func Test6cUAT_UD16_f_AskDispatchKeepsOneRecordThroughPendingRunningDone(t *testing.T) {
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{{
		ID: string(uat6cCall), Function: &providers.FunctionCall{Name: uat6cAskTool, Arguments: `{"command":"approved operation"}`},
	}}).WithText("Second.")
	al := newAutoTestLoop(t, provider, false, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies = map[string]string{uat6cAskTool: "ask"}
	})
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{uat6cAskTool: config.ToolPolicyAsk}})
	allow, release := make(chan struct{}), make(chan struct{})
	var allowOnce, releaseOnce sync.Once
	tool := &uat6cParkedTool{entered: make(chan map[string]any, 1), release: release}
	inst.Tools.Register(tool)
	registered, ok := inst.Tools.Get(uat6cAskTool)
	require.True(t, ok)
	require.Same(t, tool, registered)
	approver := &uat6cAllowOnceApprover{requests: make(chan PolicyApprovalReq, 1), allow: allow}
	al.SetToolApprover(approver)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, uat6cAEntry(t)))
	ctx, cancel := context.WithCancel(context.Background())
	type turnResult struct {
		answer string
		err    error
	}
	result, finished := make(chan turnResult, 1), make(chan struct{})
	t.Cleanup(func() {
		cancel()
		allowOnce.Do(func() { close(allow) })
		releaseOnce.Do(func() { close(release) })
		uat6cReceive(t, finished, "ask turn goroutine cleanup")
	})
	go func() {
		defer close(finished)
		answer, runErr := al.runAgentLoop(ctx, inst, processOptions{
			SessionKey: "agent:mia:session:" + meta.ID, Channel: "webchat", ChatID: meta.ID,
			UserMessage: "run the approved operation", UserInitiated: true, DefaultResponse: defaultResponse,
			TranscriptSessionID: meta.ID, TranscriptStore: store,
		})
		result <- turnResult{answer: answer, err: runErr}
	}()

	req := uat6cReceive(t, approver.requests, "real ask approval")
	assert.Equal(t, string(uat6cCall), req.ToolCallID)
	assert.Equal(t, uat6cAskTool, req.ToolName)
	assert.Equal(t, meta.ID, req.SessionID)
	assert.Equal(t, "mia", req.AgentID)
	args := map[string]any{"command": "approved operation"}
	assert.Equal(t, args, req.Args)
	pending := session.ToolCall{ID: uat6cCall, Tool: uat6cAskTool, Status: "pending", Parameters: args}
	require.Equal(t, []session.ToolCall{pending}, toolCallsFor(t, store, meta.ID, uat6cCall), "approval blocks only after saving the pending source")
	address, source := uat6cOnlyToolSource(t, store, meta.ID)
	assert.Equal(t, []session.ToolCall{pending}, source.ToolCalls)
	assert.Zero(t, tool.calls.Load(), "a pending ask must not execute before human approval")
	pendingEntries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, pendingEntries, 3, "S11: A, the textless round's accounting record, then the pending call")
	accounting := pendingEntries[1]
	require.NotEmpty(t, accounting.ID, "the accounting source has its own identity")
	assert.Equal(t, "assistant", accounting.Role)
	assert.Empty(t, accounting.Content, "S11 explicitly permits empty round-accounting sources")
	assert.Empty(t, accounting.ToolCalls, "the accounting source is not a second tool receipt")
	assert.Equal(t, []string{uat6cA, accounting.ID, string(uat6cCall)}, uat6cIDs(pendingEntries))

	allowOnce.Do(func() { close(allow) })
	assert.Equal(t, args, uat6cReceive(t, tool.entered, "approved real dispatch"))
	running := pending
	running.Status = "running"
	require.Equal(t, []session.ToolCall{running}, toolCallsFor(t, store, meta.ID, uat6cCall), "the already approved call must be running, not still pending")
	runningAddr, runningSource := uat6cOnlyToolSource(t, store, meta.ID)
	assert.Equal(t, address, runningAddr)
	assert.Equal(t, source, runningSource, "pending to running settles the same immutable source")
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, uat6cSteerEntry(t)))
	releaseOnce.Do(func() { close(release) })
	got := uat6cReceive(t, result, "approved turn completion")
	uat6cReceive(t, finished, "approved turn writer exit")
	require.NoError(t, got.err)
	assert.Equal(t, "Second.", got.answer)
	assert.Equal(t, int32(1), tool.calls.Load(), "exactly one real tool execution")
	assert.Equal(t, 2, provider.CallCount(), "the admitted result reaches the next real provider request")
	assert.Equal(t, "approved operation completed", toolResultText(t, provider, string(uat6cCall)), "the real next request receives the external operation's exact result")

	reloaded, err := session.NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reloaded.Close()) })
	calls := toolCallsFor(t, reloaded, meta.ID, uat6cCall)
	require.Len(t, calls, 1)
	assert.GreaterOrEqual(t, calls[0].DurationMS, int64(0), "contract duration is nonnegative, not a stopwatch oracle")
	calls[0].DurationMS = 0 // elapsed duration is unspecified; compare every stable field exactly
	done := pending
	done.Status = "success"
	done.Result = map[string]any{"text": "approved operation completed"} // ToolCall.yaml: the projected content the model saw
	assert.Equal(t, []session.ToolCall{done}, calls)
	doneAddr, doneSource := uat6cOnlyToolSource(t, reloaded, meta.ID)
	assert.Equal(t, address, doneAddr)
	assert.Equal(t, source, doneSource, "done is an effect, not a second source")
	entries, err := reloaded.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 5, "S11: A, accounting, tool, steer, then final text; no extra receipt")
	assert.Equal(t, []string{uat6cA, accounting.ID, string(uat6cCall), uat6cSteer}, uat6cIDs(entries[:4]), "the approved call and accounting source keep their exact pre-steer positions after reload")
	assert.Equal(t, "assistant", entries[4].Role)
	assert.Equal(t, "Second.", entries[4].Content, "the final answer remains after the accepted steer")
}

// A valid JSON string supplied as an input parameter. Only its first encoding
// pauses. This holds the real dispatch append before source acceptance without
// replacing any writer, lock, file, store or cancellation implementation.
type uat6cDispatchJSONBarrier struct {
	entered chan struct{}
	release <-chan struct{}
	encodes atomic.Int32
}

func (gate *uat6cDispatchJSONBarrier) MarshalJSON() ([]byte, error) {
	if gate.encodes.Add(1) == 1 {
		close(gate.entered)
		<-gate.release
	}
	return json.Marshal(uat6cCmd)
}

// The stack is a scheduling instrument only: wait until the real canceller has
// reached a mutex wait inside cancel(). With the serialization channel held, it
// stays there. This also works if the outcome lock is wrongly removed: then the
// canceller blocks on the store shard AFTER choosing an append instead of a
// settle. Release only after observing that overlap, not after an elapsed sleep.
func uat6cAwaitBlockedCancellation(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		if n == len(buf) {
			return false
		}
		for _, stack := range bytes.Split(buf[:n], []byte("\n\n")) {
			if bytes.Contains(stack, []byte("(*turnState).requestHardAbort(")) &&
				bytes.Contains(stack, []byte("(*startedToolOutcome).cancel(")) &&
				bytes.Contains(stack, []byte("sync.(*Mutex).Lock")) {
				return true
			}
		}
		return false
	}, 10*time.Second, time.Millisecond, "instrument: cancellation must actually overlap the paused dispatch write")
}

func Test6cUAT_UD16_g_CancelRacingDispatchWriteKeepsOneCancelledRecord(t *testing.T) {
	f := newUAT6cTranscriptFixture(t)
	f.append(t, uat6cAEntry(t))
	before := f.archiveBytes(t)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	gate := &uat6cDispatchJSONBarrier{entered: make(chan struct{}), release: release}
	ex := f.execution()
	ex.rx.rr.rq.ri.rf.rt.al.eventBus = NewEventBus()
	t.Cleanup(ex.rx.rr.rq.ri.rf.rt.al.eventBus.Close)
	ex.toolArgs = map[string]any{"command": gate}
	accepted, dispatchExited := make(chan bool, 1), make(chan struct{})
	t.Cleanup(func() {
		unblock()
		uat6cReceive(t, dispatchExited, "dispatch goroutine cleanup")
	})
	go func() {
		defer close(dispatchExited)
		accepted <- ex.beginStartedTool(providers.ToolCall{ID: string(uat6cCall), Name: "bash"}, time.Now())
	}()
	uat6cReceive(t, gate.entered, "real dispatch serialization barrier")
	require.Equal(t, before, f.archiveBytes(t), "instrument: dispatch is in progress, but its source is not accepted yet")
	cancelled, cancelExited := make(chan bool, 1), make(chan struct{})
	t.Cleanup(func() {
		unblock()
		uat6cReceive(t, cancelExited, "cancel goroutine cleanup")
	})
	go func() {
		defer close(cancelExited)
		cancelled <- f.ts.requestHardAbort()
	}()
	uat6cAwaitBlockedCancellation(t)
	require.True(t, f.ts.hardAbortRequested(), "the real hard-cancel request arrived before the dispatch write finished")
	unblock()
	require.True(t, uat6cReceive(t, accepted, "dispatch receipt"))
	require.True(t, uat6cReceive(t, cancelled, "cancel receipt"))
	uat6cReceive(t, dispatchExited, "dispatch writer exit")
	uat6cReceive(t, cancelExited, "cancel writer exit")
	calls := toolCallsFor(t, f.store, f.sid, uat6cCall)
	require.Len(t, calls, 1, "the overlap must not create a running source after a cancelled source")
	assert.GreaterOrEqual(t, calls[0].DurationMS, int64(0))
	calls[0].DurationMS = 0
	want := session.ToolCall{ID: uat6cCall, Tool: "bash", Status: "cancelled", Parameters: map[string]any{"command": uat6cCmd}}
	assert.Equal(t, []session.ToolCall{want}, calls)
	address, source := uat6cOnlyToolSource(t, f.store, f.sid)
	assert.Equal(t, "running", source.ToolCalls[0].Status, "the dispatch source exists once; cancellation settles it in place")
	assert.Equal(t, []string{uat6cA, string(uat6cCall)}, uat6cIDs(f.entries(t)))
	ex.tcRecord = uat6cCompletedCall("success")
	settledBytes := f.archiveBytes(t)
	require.ErrorIs(t, ex.recordAdmittedTranscript(), context.Canceled, "late completion cannot replace the accepted cancellation")
	f.ts.cancelStartedTool() // a repeat cancellation is also a no-op
	assert.Equal(t, settledBytes, f.archiveBytes(t), "late success/repeat cancel must write nothing")
	f.reopen(t)
	reloaded := toolCallsFor(t, f.store, f.sid, uat6cCall)
	require.Len(t, reloaded, 1)
	reloaded[0].DurationMS = 0
	assert.Equal(t, []session.ToolCall{want}, reloaded)
	reloadedAddr, reloadedSource := uat6cOnlyToolSource(t, f.store, f.sid)
	assert.Equal(t, address, reloadedAddr)
	assert.Equal(t, source, reloadedSource)
}
