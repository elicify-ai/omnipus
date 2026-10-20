package agent

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Tests for the agent side of group-chat rooms (omnipus-system-agent FR-200,
// FR-214, FR-215, FR-216, FR-217, FR-251). Expected values come from the
// spec: the delivery id IS the receiver's request id; a room request never
// joins a running turn; the Ask card is raised in the receiver's main chat
// labelled "for group <name>"; a committed capture is an admission already
// made; the answer goes to the room, not to a source session.

const roomTestDelivery = "d-0001"

func roomAdmission() RoomRequestAdmission {
	return RoomRequestAdmission{
		Receiver:   addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver},
		DeliveryID: roomTestDelivery, RoomID: "room-1", RoomName: "Launch",
		Sender: addressing.Sender{Principal: "user"}, SenderLabel: "the user",
		DisplayText: "status?", ModelText: "Message from the user:\nstatus?",
	}
}

func TestAdmitRoomRequest_DeliveryIDIsTheReceiverRequestID(t *testing.T) {
	f := newAddrFixture(t)
	res, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	if err != nil {
		t.Fatalf("AdmitRoomRequest: %v", err)
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	if res.ReceiverSessionID != mainID || res.AlreadyAdmitted || res.Held {
		t.Fatalf("result = %+v, want a fresh admission into %q", res, mainID)
	}
	c, found, err := f.al.RequestLedger().Get(mainID, roomTestDelivery)
	if err != nil || !found {
		t.Fatalf("capture for the delivery id: found=%v err=%v", found, err)
	}
	if c.Source.Kind != addressing.SourceRoom || c.Source.RoomID != "room-1" || c.Source.SessionID != "" {
		t.Fatalf("capture source = %+v, want a room source with no source session", c.Source)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(mainID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("receiver transcript: %d entries, err %v", len(entries), err)
	}
	e := entries[0]
	if e.ID != roomTestDelivery || e.Content != "status?" || e.RoomRef == nil ||
		e.RoomRef.RoomId != "room-1" || e.RoomRef.RoomName != "Launch" || e.RoomRef.RequestId != roomTestDelivery || e.RoomRef.Role != "request" {
		t.Fatalf("entry = %+v room_ref=%+v, want id=delivery id, the display text and room_ref role request", e, e.RoomRef)
	}
	wake, ok := addrDrainInbound(t, f.bus)
	if !ok {
		t.Fatal("no wake was published for an idle receiver")
	}
	if !isRoomRequestWake(wake) || wake.Metadata[roomIDMetadataKey] != "room-1" || wake.TranscriptEntryID != roomTestDelivery {
		t.Fatalf("wake metadata = %v entry=%q, want the no-join room wake for the delivery", wake.Metadata, wake.TranscriptEntryID)
	}
}

func TestAdmitRoomRequest_RetryWithCommittedCaptureWritesAndWakesNothing(t *testing.T) {
	f := newAddrFixture(t)
	if _, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission()); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	addrDrainInbound(t, f.bus)
	res, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	if err != nil || !res.AlreadyAdmitted {
		t.Fatalf("retry = %+v err=%v, want AlreadyAdmitted", res, err)
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	entries, _ := f.al.GetSessionStore().ReadTranscript(mainID)
	if len(entries) != 1 {
		t.Fatalf("receiver transcript has %d entries after a retry, want 1", len(entries))
	}
	if _, ok := addrDrainInbound(t, f.bus); ok {
		t.Fatal("a retry of an admitted request woke the receiver again")
	}
}

type recordingApprover struct {
	mu       sync.Mutex
	reqs     []PolicyApprovalReq
	approved bool
}

func (r *recordingApprover) RequestApproval(_ context.Context, req PolicyApprovalReq) (bool, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	if r.approved {
		return true, "", false
	}
	return false, "user", false
}

func TestAdmitRoomRequest_AskRaisesTheCardInTheReceiversMainChat(t *testing.T) {
	for _, approved := range []bool{false, true} {
		f := newAddrFixture(t)
		setSendMessagePolicy(t, f.al, addrReceiver, config.ToolPolicyAsk)
		ap := &recordingApprover{approved: approved}
		f.al.SetToolApprover(ap)
		awaiting := false
		a := roomAdmission()
		a.OnAwaitingApproval = func() { awaiting = true }
		_, err := f.al.AdmitRoomRequest(context.Background(), a)
		mainID, _ := session.MainSessionID(addrWS, addrReceiver)
		if len(ap.reqs) != 1 || !awaiting {
			t.Fatalf("approved=%v: %d approval requests, awaiting=%v; want exactly one card", approved, len(ap.reqs), awaiting)
		}
		req := ap.reqs[0]
		if req.SessionID != mainID || req.AgentID != addrReceiver || req.ToolCallID != roomTestDelivery ||
			req.OriginLabel != "for group Launch" || req.RoomRef == nil || req.RoomRef.RequestId != roomTestDelivery || req.TurnID == "" {
			t.Fatalf("card = %+v, want the receiver's main, request id = delivery id, room_ref and origin label", req)
		}
		_, found, _ := f.al.RequestLedger().Get(mainID, roomTestDelivery)
		if approved && (err != nil || !found) {
			t.Fatalf("approved card: err=%v captured=%v, want admitted", err, found)
		}
		if !approved && (!errors.Is(err, ErrRoomRequestRefused) || found) {
			t.Fatalf("denied card: err=%v captured=%v, want refused with nothing written", err, found)
		}
	}
}

type failingRoomWriter struct{}

func (failingRoomWriter) AppendTranscriptOnce(string, session.TranscriptEntry) (bool, error) {
	return false, errors.New("disk full")
}

func TestAdmitRoomRequest_ReceiverAppendFaultLeavesNoAnswerableCapture(t *testing.T) {
	f := newAddrFixture(t)
	f.al.SetRoomReceiverWriter(failingRoomWriter{})
	_, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	if !errors.Is(err, ErrRoomStorage) {
		t.Fatalf("err = %v, want ErrRoomStorage", err)
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	_, found, gErr := f.al.RequestLedger().Get(mainID, roomTestDelivery)
	if gErr != nil || found {
		t.Fatalf("capture after a failed append: found=%v err=%v, want no capture at all (WU-15 ruling 06:00Z)", found, gErr)
	}
	if _, ok := addrDrainInbound(t, f.bus); ok {
		t.Fatal("a failed receiver append still woke the receiver")
	}
	// A Retry with a healthy writer admits it exactly once.
	f.al.SetRoomReceiverWriter(nil)
	if res, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission()); err != nil || res.AlreadyAdmitted {
		t.Fatalf("retry after the failed append: %+v %v, want a fresh admission", res, err)
	}
	if entries, _ := f.al.GetSessionStore().ReadTranscript(mainID); len(entries) != 1 {
		t.Fatalf("receiver transcript has %d entries after the retry, want 1", len(entries))
	}
}

func TestTrySteerIntoLiveTurn_RoomRequestNeverJoinsARunningTurn(t *testing.T) {
	f := newAddrFixture(t)
	if _, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission()); err != nil {
		t.Fatalf("admission: %v", err)
	}
	wake, ok := addrDrainInbound(t, f.bus)
	if !ok {
		t.Fatal("no wake published")
	}
	w := newSessionWorker("agent:mia:session:"+wake.SessionID, f.al, func() {})
	w.inTurn.Store(true)
	if w.trySteerIntoLiveTurn(wake) {
		t.Fatal("a room request was steered into the running turn; it must wait for the turn to settle (FR-214)")
	}
	// Positive control: the same wake without the room marker is an ordinary
	// request wake, which the live turn takes (so the check above could fail).
	plain := wake
	plain.Metadata = map[string]string{}
	for k, v := range wake.Metadata {
		if k != roomRequestMetadataKey && k != roomIDMetadataKey {
			plain.Metadata[k] = v
		}
	}
	if !w.trySteerIntoLiveTurn(plain) {
		t.Fatal("positive control: an ordinary request wake must join the running turn")
	}
}

type fakeRoomRuntime struct {
	mu       sync.Mutex
	allow    bool
	answers  []RoomAnswer
	ended    []string
	begun    []string
	reminded []string
}

func (f *fakeRoomRuntime) BeginRoomConsume(_, requestID, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.begun = append(f.begun, requestID)
	return f.allow
}
func (f *fakeRoomRuntime) RoomConsumeHeld(string, string) {}
func (f *fakeRoomRuntime) RoomConsumeEnded(_, requestID string, _ error) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, requestID)
	return true, nil
}
func (f *fakeRoomRuntime) RoomReminderTurnEnded(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reminded = append(f.reminded, sessionID)
	return nil
}
func (f *fakeRoomRuntime) RoomTurnCost(RoomTurnCost) {}
func (f *fakeRoomRuntime) DeliverRoomAnswer(_ context.Context, a RoomAnswer) (tools.ReplyReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, a)
	return tools.ReplyReceipt{ReplyID: "ans-1", Destination: "the group chat Launch"}, nil
}

func TestReply_RoomCaptureReturnsThroughTheRoomBranch(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fakeRoomRuntime{allow: true}
	f.al.SetRoomRuntime(rt)
	if _, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission()); err != nil {
		t.Fatalf("admission: %v", err)
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	receipt, err := f.r.Reply(context.Background(), tools.ReplyRequest{
		ReplyTo: roomTestDelivery, Content: "all green",
		Author: tools.SendOrigin{WorkspaceID: addrWS, AgentID: addrReceiver}, ResponderSessionID: mainID,
	})
	if err != nil || receipt.ReplyID != "ans-1" {
		t.Fatalf("Reply = %+v, %v", receipt, err)
	}
	if len(rt.answers) != 1 || rt.answers[0].RoomID != "room-1" || rt.answers[0].RequestID != roomTestDelivery ||
		rt.answers[0].ResponderSessionID != mainID || rt.answers[0].Content != "all green" {
		t.Fatalf("room answers = %+v, want one answer for the delivery from the member's main", rt.answers)
	}
	if _, ok := addrDrainOutbound(f.bus); ok {
		t.Fatal("a room answer was sent to a connector")
	}
}

func TestGateRoomRequest_CancelledRequestIsDiscardedAndNotRun(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fakeRoomRuntime{allow: false}
	f.al.SetRoomRuntime(rt)
	if _, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission()); err != nil {
		t.Fatalf("admission: %v", err)
	}
	wake, _ := addrDrainInbound(t, f.bus)
	if _, proceed := f.al.gateRoomRequest(wake); proceed {
		t.Fatal("the dequeue fence let a cancelled room request run")
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	c, _, err := f.al.RequestLedger().Get(mainID, roomTestDelivery)
	if err != nil || !c.Discarded {
		t.Fatalf("capture discarded=%v err=%v, want discarded so it can never be answered", c.Discarded, err)
	}
	rt.allow = true
	if _, proceed := f.al.gateRoomRequest(wake); !proceed {
		t.Fatal("positive control: a current room request must pass the fence")
	}
}

// §6.14 / BDD-17.30 pricing seam: the room allocation reads the installed
// RoomCallPricer; the turn's own cost keeps the catalog price. Since the
// prompt-caching landing the one accounting path for a provider call is
// accountProviderCall (pkg/agent/prompt_cache_accounting.go); the room step
// rides it. Expected costs come from backendCallCatalog's rates (input 3,
// output 15 USD per million tokens): 1000 + 1000 tokens = 0.003 + 0.015.
func TestRoomCallPricer_FeedsOnlyTheRoomAllocation(t *testing.T) {
	cat, err := catalog.NewCatalog([]byte(backendCallCatalog))
	require.NoError(t, err, "load the priced catalog fixture")
	previous := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(previous) })

	al, ts := newTestAgentLoopForRateLimit(t)
	ts.opts.roomCost = &roomCostMeter{al: al, inputs: 1, rooms: map[string]int{}}
	usage := &providers.UsageInfo{PromptTokens: 1000, CompletionTokens: 1000, TotalTokens: 2000}
	const catalogCost = 0.003 + 0.015

	al.SetRoomCallPricer(func(string, providers.UsageInfo) float64 { return 0.03 })
	ts.accountProviderCall("anthropic", "cache-model", usage)
	if _, cost, unpriced, _, _ := ts.opts.roomCost.snapshot(); cost != 0.03 || unpriced {
		t.Fatalf("room meter cost = %v unpriced=%v, want the pricer's 0.03, priced", cost, unpriced)
	}
	if _, turnCost := ts.GetTurnStats(); math.Abs(turnCost-catalogCost) > 1e-12 {
		t.Fatalf("turn cost = %v, want the unchanged catalog price %v (never the pricer's 0.03)", turnCost, catalogCost)
	}

	al.SetRoomCallPricer(nil)
	ts.accountProviderCall("anthropic", "cache-model", usage)
	if _, cost, unpriced, _, _ := ts.opts.roomCost.snapshot(); math.Abs(cost-(0.03+catalogCost)) > 1e-12 || unpriced {
		t.Fatalf("without the pricer a priced call adds its catalog price: %v unpriced=%v", cost, unpriced)
	}

	// A model the catalog does not know has no price: the turn is marked
	// unpriced and nothing is added (§6.14 "plus unpriced turns").
	ts.accountProviderCall("anthropic", "no-such-model", usage)
	if _, cost, unpriced, _, _ := ts.opts.roomCost.snapshot(); math.Abs(cost-(0.03+catalogCost)) > 1e-12 || !unpriced {
		t.Fatalf("an unknown-priced call must mark unpriced and add nothing: %v unpriced=%v", cost, unpriced)
	}

	// A model the catalog lists without a price is unpriced too (fresh meter,
	// so this case is observed on its own).
	ts.opts.roomCost = &roomCostMeter{al: al, inputs: 1, rooms: map[string]int{}}
	ts.accountProviderCall("unpriced-fixture", "cache-model", usage)
	if _, cost, unpriced, _, _ := ts.opts.roomCost.snapshot(); cost != 0 || !unpriced {
		t.Fatalf("a catalog-unpriced call must mark unpriced and add nothing: %v unpriced=%v", cost, unpriced)
	}

	// Gate round 3, architect R3-N1: a MIXED call stays "priced" in the
	// caching accounting (it carries unpriced tokens; the cohere fixture
	// prices no one-hour cache write), but its price is not known as a whole,
	// so for the room it adds nothing and marks "plus unpriced turns"
	// (§6.14). Usage is the caching producer fixture's "mixed one-hour
	// unpriced" case.
	mixed := &providers.UsageInfo{PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 800,
		CacheWriteTokens: 100, CacheWrite5mTokens: 40, CacheWrite1hTokens: 60, TotalTokens: 1010}
	ts.opts.roomCost = &roomCostMeter{al: al, inputs: 1, rooms: map[string]int{}}
	_, mixedPrice := ts.accountProviderCall("cohere", "cache-model", mixed)
	if mixedPrice.BillingClass != "priced" || mixedPrice.UnpricedTokens != 60 {
		t.Fatalf("instrument: the fixture must be a mixed call (priced, 60 unpriced tokens), got %+v", mixedPrice)
	}
	if _, cost, unpriced, _, _ := ts.opts.roomCost.snapshot(); cost != 0 || !unpriced {
		t.Fatalf("a mixed call must mark the room unpriced and add nothing: %v unpriced=%v", cost, unpriced)
	}

	// Control: a subscription call is a known zero (no per-call dollar
	// debit), not an unknown price: it adds 0 and does not set the mark.
	ts.opts.roomCost = &roomCostMeter{al: al, inputs: 1, rooms: map[string]int{}}
	ts.accountProviderCall("openai-chatgpt", "cache-model", usage)
	if _, cost, unpriced, _, _ := ts.opts.roomCost.snapshot(); cost != 0 || unpriced {
		t.Fatalf("a subscription call is a known zero: %v unpriced=%v", cost, unpriced)
	}
}
