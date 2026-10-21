package gateway

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestWU15Rooms_BusyMemberQueuesWithoutJoiningRunningTurn(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	p := &wu15ScriptProvider{answer: "ordinary web answer", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("mia", p)
	h.startLoop()
	mainConn := h.connect()
	h.sendMain(mainConn, "mia", "ordinary busy input")
	select {
	case <-p.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: real main turn did not enter provider")
	}
	h.post(room.Id, "queued", "@Mia ROOM_MUST_NOT_JOIN")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "queued"), "mia")
	h.waitState(room.Id, request.RequestId, "admitted")
	require.Len(t, p.inputs(), 1, "FR-214: room intake does not cause another provider call in the running turn")
	close(p.release)
	// First turn settles through the actual socket. The model's next call is
	// inspected: the room input must be absent from the existing web turn.
	wu15ReadFrame(t, mainConn, "done")
	first := p.inputs()[0]
	for _, message := range first {
		require.NotContains(t, message.Content, "ROOM_MUST_NOT_JOIN", "FR-214: no steering/join into live main turn")
	}
	require.Eventually(t, func() bool { return len(p.inputs()) >= 2 }, 30*time.Second, 10*time.Millisecond, "FR-214: queued request runs only after the original turn settles")
	wu15AssertProviderSaw(t, p, "ROOM_MUST_NOT_JOIN")
}

func TestWU15Rooms_StoppedMemberHeldUntilRealMainRevival(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	h.post(room.Id, "held", "@Mia held input")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "held"), "mia")
	h.waitState(room.Id, request.RequestId, "held")
	p := &wu15ScriptProvider{requestID: request.RequestId, answer: "held answer"}
	h.provider("mia", p)
	h.startLoop()
	require.Empty(t, p.inputs(), "FR-252: the loop never automatically resumes a stopped member")
	mainConn := h.connect()
	h.sendMain(mainConn, "mia", "run again")
	h.waitState(room.Id, request.RequestId, "answered")
	wu15AssertProviderSaw(t, p, "held input")
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, capture.Delivered, "FR-252: real held delivery once on revival")
}

func (h *wu15RoomHarness) receiverPolicy(id string, policyValue config.ToolPolicy) {
	h.t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(id)
	require.True(h.t, ok)
	policy := *inst.LoadToolPolicy()
	policy.Policies = map[string]config.ToolPolicy{}
	for name, value := range inst.LoadToolPolicy().Policies {
		policy.Policies[name] = value
	}
	policy.Policies["send_message"] = policyValue
	inst.StoreToolPolicy(&policy)
}

func TestWU15Rooms_ReceiverAskIsBeforeCaptureAndDenyIsRefused(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.receiverPolicy("mia", config.ToolPolicyAsk)
	h.post(room.Id, "ask", "@Mia private request")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "ask"), "mia")
	h.waitState(room.Id, request.RequestId, "awaiting_approval")
	_, found, err := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
	require.NoError(t, err)
	require.False(t, found, "§6.11: approval precedes capture")
	require.Empty(t, h.messages("mia"), "§6.11: unapproved request never appended to receiver")
	pending := h.api.approvalReg.pendingApprovals()
	require.Len(t, pending, 1)
	require.Equal(t, h.main("mia"), pending[0].SessionID)
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+pending[0].ApprovalID, gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionDeny})
	require.Equal(t, http.StatusOK, status, "FR-251: real user decision: %s", body)
	h.waitState(room.Id, request.RequestId, "refused")
	_, found, err = h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
	require.NoError(t, err)
	require.False(t, found, "denied request never captured")
	require.Empty(t, h.api.approvalReg.pendingApprovals())
}

func TestWU15Rooms_ProviderFailureIsFailedNotWorkingOrAnswered(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.provider("mia", &wu15ScriptProvider{fail: &providers.FailoverError{Reason: providers.FailoverFormat, Wrapped: fmt.Errorf("wu15 provider failure sentinel")}})
	h.startLoop()
	h.post(room.Id, "provider-fail", "@Mia fail")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "provider-fail"), "mia")
	snap := h.waitState(room.Id, request.RequestId, "failed")
	for _, state := range snap.MemberStates {
		if state.RequestId == request.RequestId {
			require.NotNil(t, state.Reason, "§6.11: safe visible failure reason")
		}
	}
	for _, entry := range snap.Entries {
		require.NotEqual(t, "agent", entry.Author.Kind, "provider failure never manufactured an answer")
	}
}

func TestWU15Rooms_RateLimitIsUnavailableNotReplyCountCap(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	// FR-176: 60/min per sender+target. All are addressed to one stopped main
	// so provider speed cannot turn this into a different pacing experiment.
	for i := 1; i <= 61; i++ {
		h.post(room.Id, fmt.Sprintf("rate-%d", i), "@Mia pace test")
	}
	snap := h.snapshot(room.Id)
	for i := 1; i <= 60; i++ {
		delivery := wu15Delivery(t, wu15FindEntry(t, snap, fmt.Sprintf("rate-%d", i)), "mia")
		require.Equal(t, "held", delivery.State, "FR-176: first sixty are admitted/held, never a global reply cap")
	}
	last := wu15Delivery(t, wu15FindEntry(t, snap, "rate-61"), "mia")
	require.Equal(t, "unavailable", last.State)
	require.NotNil(t, last.Reason, "K27: rate-limited admission explains unavailability")
	require.Equal(t, "rate_limit", *last.Reason, "K27: distinguish pacing from held/working")
}

func TestWU15Rooms_RestartWorkingIsInterruptedAndNeverReplayed(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	p := &wu15ScriptProvider{answer: "not committed", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("mia", p)
	h.startLoop()
	h.post(room.Id, "interrupted", "@Mia unfinished request")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "interrupted"), "mia")
	h.waitState(room.Id, request.RequestId, "working")
	restoreCrashBytes := wu15DurableCrashImage(t, h.home)
	h.shutdown()
	restoreCrashBytes()
	h = wu15Boot(t, h.home, h.cfg)
	snap := h.waitState(room.Id, request.RequestId, "interrupted")
	restarted := &wu15ScriptProvider{answer: "must not replay"}
	h.provider("mia", restarted)
	h.startLoop()
	// Re-read snapshot/ledger as a synchronization point. No automatic wake is
	// allowed for the already-consumed interrupted request.
	require.Empty(t, restarted.inputs(), "FR-216: restart does not replay interrupted work")
	entry := wu15FindEntry(t, snap, "interrupted")
	require.Equal(t, request.RequestId, wu15Delivery(t, entry, "mia").RequestId)
	h.post(room.Id, "ask-again", "@Mia unfinished request")
	newRequest := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "ask-again"), "mia")
	require.NotEqual(t, request.RequestId, newRequest.RequestId, "§6.11: ask again is a new request, not implicit replay")
}

func TestWU15Rooms_UnknownReceiverFactsAfterRestartNeverMeanWorking(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	h.post(room.Id, "unknown", "@Mia preserve uncertain state")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "unknown"), "mia")
	// Only the real filesystem edge is faulted. The known receiver ledger path
	// is declared in addressing/ledger.go; no room-private storage layout.
	path := filepath.Join(h.al.GetSessionStore().BaseDir(), h.main("mia"), "requests.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("not-json\n"), 0o600))
	h = h.restart()
	snap := h.waitState(room.Id, request.RequestId, "unknown")
	for _, state := range snap.MemberStates {
		if state.RequestId == request.RequestId {
			require.Equal(t, "unknown", state.State, "§6.11: unreadable is unknown, never working")
		}
	}
}
