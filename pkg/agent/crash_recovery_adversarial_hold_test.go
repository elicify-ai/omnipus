//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Third-party tool execution is the process edge here. It actually delegates
// to the real append engine, but deliberately has NO reconciliation adapter.
// Its name/description are not evidence of being read-only.
type crashOpaqueMutator struct {
	tools.BaseTool
	root string
}

func (*crashOpaqueMutator) Name() string           { return "external_opaque" }
func (*crashOpaqueMutator) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*crashOpaqueMutator) Description() string {
	return "Looks like a read-only lookup; untrusted description grants no recovery classification."
}
func (t *crashOpaqueMutator) Parameters() map[string]any {
	return tools.NewAppendFileTool(t.root, true).Parameters()
}
func (t *crashOpaqueMutator) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.NewAppendFileTool(t.root, true).Execute(ctx, args)
}

func TestCrashRecovery_UnsupportedMutatorNeverAutoContinues(t *testing.T) {
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	h := crashNew(t, p)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	inst := h.al.GetRegistry().primaryChatAgentForTest()
	inst.Tools.RegisterReplacing(&crashOpaqueMutator{root: inst.Home})
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"external_opaque": "allow", "append_file": "allow"}})
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	path := filepath.Join(work, "opaque.txt")
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("opaque-lost", "external_opaque", map[string]any{"path": path, "content": "A\n"})}, FinishReason: "tool_calls"}, nil
	}
	cut := &crashFileResultBarrier{tool: "external_opaque", entered: make(chan *ToolResultHookResponse, 1), release: make(chan struct{})}
	require.NoError(t, h.al.MountHook(NamedHook("opaque-after-write-cut", cut)))
	t.Cleanup(cut.open)
	rec, owner := crashChild(t, h, p)
	close(gate)
	result := crashAwait(t, cut.entered)
	require.False(t, result.Result.IsError)
	snapshot := h.snapshot(t)
	cut.open()
	crashAwaitDone(t, owner.done)
	next := &crashScript{}
	next.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		if i == 0 {
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("unsafe-opaque-continuation", "append_file", map[string]any{"path": path, "content": "UNSAFE\n"})}, FinishReason: "tool_calls"}, nil
		}
		return &providers.LLMResponse{Content: "done", FinishReason: "stop"}, nil
	}
	h.restore(t, snapshot, next)
	inst = h.al.GetRegistry().primaryChatAgentForTest()
	inst.Tools.RegisterReplacing(&crashOpaqueMutator{root: inst.Home})
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"external_opaque": "allow", "append_file": "allow"}})
	h.recover(t)
	got := h.record(t, rec.SessionID)
	if own := h.al.executionDispositionFor(h.al.executionClaimFor(got)); own != nil {
		crashAwaitDone(t, own.done)
	}
	assert.Equal(t, "interrupted", string(got.State))
	assert.Equal(t, "reconciliation_required", crashFields(t, got)["interruption_reason"])
	assert.Equal(t, 0, len(next.calls()))
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "A\n", string(bytes))
}

func TestCrashRecovery_TwoUnresolvedWritersOnSameTargetAreUnknown(t *testing.T) {
	crashRequireFileProof(t)
	first := crashFileCut(t, "append_file", func(f *crashFileFixture) { f.keepHeld = true })
	h := first.h
	h.al.GetConfig().Performance.MaxParallelAgents = 2
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("second-unresolved", "append_file", map[string]any{"path": first.path, "content": "B\n"})}, FinishReason: "tool_calls"}, nil
	}
	h.al.GetRegistry().primaryChatAgentForTest().Provider = p
	cut := &crashFileResultBarrier{tool: "append_file", matchContent: "B\n", entered: make(chan *ToolResultHookResponse, 1), release: make(chan struct{})}
	require.NoError(t, h.al.MountHook(NamedHook("second-unresolved-write", cut)))
	t.Cleanup(cut.open)
	// The first cut's interceptor matches only the first tool's content.
	// Its original AfterTool is already held; a second callback must not block it.
	second, owner := crashChild(t, h, p)
	close(gate)
	result := crashAwait(t, cut.entered)
	require.False(t, result.Result.IsError)
	snapshot := h.snapshot(t)
	cut.open()
	crashAwaitDone(t, owner.done)
	first.release()
	next := &crashScript{}
	h.restore(t, snapshot, next)
	h.recover(t)
	for _, id := range []string{first.record.SessionID, second.SessionID} {
		got := h.record(t, id)
		if own := h.al.executionDispositionFor(h.al.executionClaimFor(got)); own != nil {
			crashAwaitDone(t, own.done)
		}
		assert.Equal(t, "interrupted", string(h.record(t, id).State), "competing unresolved effects cannot be resolved by an ID-global or last-writer guess")
	}
	assert.Equal(t, 0, len(next.calls()))
	bytes, err := os.ReadFile(first.path)
	require.NoError(t, err)
	assert.Equal(t, "base\nA\nB\n", string(bytes), "readback is not a new managed write")
}

func TestCrashRecovery_DispatchHoldCannotBeBypassedByPromptOrToolList(t *testing.T) {
	crashRequireInterrupted(t)
	f := crashFileCut(t, "append_file")
	require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
	f.h.restore(t, f.snapshot, &crashScript{})
	f.h.recover(t)
	held := f.h.record(t, f.record.SessionID)
	require.Equal(t, "interrupted", string(held.State))
	inst := f.h.al.GetRegistry().primaryChatAgentForTest()
	ts := newTurnState(inst, processOptions{SessionKey: held.SessionID, TranscriptSessionID: held.SessionID, TranscriptStore: f.h.al.GetSessionStore(), SteeredSessionID: held.SessionID, WorkspaceID: testHarnessWorkspaceMembershipID}, turnEventScope{turnID: "late-old-producer"})
	ts.generation = held.Generation
	require.NoError(t, ts.setExecutionIdentity(held.ExecutionID.RunID, held.ExecutionID.BootSeq))
	call := crashCall("uncertain", "append_file", f.args)
	ts.recordCallIssuers(providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}}, f.assistant)
	fixture := &cwR1Harness{al: f.h.al, agent: inst, store: f.h.al.GetSessionStore(), cfg: f.h.al.GetConfig(), key: held.SessionID}
	rr := cwR1Flow(fixture, ts, nil, &crashScript{})
	rr.normalizedToolCalls = []providers.ToolCall{call}
	ctx := tools.WithTranscriptSessionID(tools.WithSessionKey(tools.WithWorkspaceID(tools.WithAgentID(context.Background(), testDefaultAgentID), testHarnessWorkspaceMembershipID), held.SessionID), held.SessionID)
	rr.rq.ri.rf.rt.turnCtx = withTurnState(ctx, ts)
	rx := &agentLoopRunTurnTools{ctx: ctx, rr: rr}
	rx.executeToolCalls()
	bytes, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, f.after, string(bytes), "a late model/executor call must hit the engine dispatch hold even when all prompt/tool-list filtering is bypassed")
	assert.Equal(t, held, f.h.record(t, held.SessionID))
}

func TestCrashRecovery_UnrelatedHumanRoomDoesNotAcceptHelperUncertainty(t *testing.T) {
	crashRequireInterrupted(t)
	f := crashFileCut(t, "append_file")
	require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
	p := &crashScript{}
	f.h.restore(t, f.snapshot, p)
	f.h.recover(t)
	held := f.h.record(t, f.record.SessionID)
	require.Equal(t, "interrupted", string(held.State))
	f.h.al.SetAddressDeps(&fakeAddressDeps{eligible: map[string]bool{testHarnessWorkspaceMembershipID + "/" + testDefaultAgentID: true}})
	f.h.al.SetRoomRuntime(&fakeRoomRuntime{allow: true})
	admitted, err := f.h.al.AdmitRoomRequest(context.Background(), RoomRequestAdmission{Receiver: addressing.Pair{WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID}, DeliveryID: "unrelated-human-H", RoomID: "independent-room", RoomName: "Independent", Sender: addressing.Sender{Principal: "owner"}, SenderLabel: "the user", DisplayText: "independent fresh room request", ModelText: "independent fresh room request"})
	require.NoError(t, err)
	var message bus.InboundMessage
	found := false
	for !found {
		select {
		case next := <-f.h.al.bus.InboundChan():
			if next.SessionID == admitted.ReceiverSessionID {
				message = next
				found = true
			}
		default:
			t.Fatal("fixture: unrelated fresh human room request published no own wake")
		}
	}
	_, _, err = f.h.al.processMessage(context.Background(), message)
	require.NoError(t, err)
	assert.Equal(t, held, f.h.record(t, held.SessionID), "fresh human traffic in ANOTHER chat cannot accept this helper's unresolved occurrence set")
	bytes, err := os.ReadFile(f.path)
	require.NoError(t, err)
	assert.Equal(t, f.after, string(bytes))
}

func TestCrashRecovery_OldReportWakeCannotAcceptUncertainty(t *testing.T) {
	crashRequireInterrupted(t)
	f := crashFileCut(t, "append_file")
	require.NoError(t, os.RemoveAll(filepath.Join(f.snapshot, "sessions", f.record.SessionID, "file_effects")))
	p := &crashScript{}
	f.h.restore(t, f.snapshot, p)
	f.h.recover(t)
	held := f.h.record(t, f.record.SessionID)
	require.Equal(t, "interrupted", string(held.State))
	id := appendWakeInboxEntry(t, f.h.al, held.SessionID, "ordinary report says checked, but cannot accept uncertainty")
	for range 2 {
		_, err := f.h.al.processSteeredSystemWake(context.Background(), wakeMessage(held.SessionID, id, held.Generation))
		require.NoError(t, err)
	}
	assert.Equal(t, 0, len(p.calls()))
	assert.Equal(t, held, f.h.record(t, held.SessionID))
}
