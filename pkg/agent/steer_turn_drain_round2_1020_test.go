// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Round-two regression coverage for issue #1020. These tests pin the
// same-generation re-entry, partial batch-consumption, and tombstone-lifetime
// failures found after the first post-turn-drain fix.
package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type steeredReentryProvider1020 struct {
	mu           sync.Mutex
	requests     [][]providers.Message
	firstStarted chan struct{}
	releaseFirst chan struct{}
	startOnce    sync.Once
}

func newSteeredReentryProvider1020() *steeredReentryProvider1020 {
	return &steeredReentryProvider1020{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (p *steeredReentryProvider1020) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	call := len(p.requests) + 1
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()

	if call == 1 {
		p.startOnce.Do(func() { close(p.firstStarted) })
		select {
		case <-p.releaseFirst:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if call > 2 {
		return nil, fmt.Errorf("issue 1020 re-entry provider received unexpected call %d", call)
	}
	return &providers.LLMResponse{
		Content:      fmt.Sprintf("issue 1020 parent response %d", call),
		FinishReason: "stop",
	}, nil
}

func (p *steeredReentryProvider1020) GetDefaultModel() string { return "issue-1020-reentry" }

func (p *steeredReentryProvider1020) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		requests[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return requests
}

func setLifecycleState1020(t *testing.T, al *AgentLoop, sessionID string, state session.LifecycleState) *session.LifecycleRecord {
	t.Helper()
	if err := al.GetSessionLifecycleStore().Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		rec.State = state
		return nil
	}); err != nil {
		t.Fatalf("Mutate(%s, state=%s): %v", sessionID, state, err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(sessionID)
	if err != nil {
		t.Fatalf("Load(%s): %v", sessionID, err)
	}
	return rec
}

// TestSteeredTurnDrain1020_SameGenerationReentryAcceptsSecondChildWake pins
// the full P -> {child 1, child 2} failure. P's first drain closes its queue
// but its completion defers. Child 1 then wakes P into a real same-generation
// turn; child 2 finishes while that turn is live. The second wake must join
// that live turn, and P must hand its final continuation result upward once.
func TestSteeredTurnDrain1020_SameGenerationReentryAcceptsSecondChildWake(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := newSteeredReentryProvider1020()
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider
	wireSteerCompletionDeps(t, al)

	rootID := newTestSteeringSession(t, al, "ws-1")
	parent := launchRunningChild(t, al, rootID, "issue-1020-parent")
	child1 := launchRunningChild(t, al, parent.SessionID, "issue-1020-child-1")
	child2 := launchRunningChild(t, al, parent.SessionID, "issue-1020-child-2")
	child1 = setLifecycleState1020(t, al, child1.SessionID, session.LifecycleQueued)
	child2 = setLifecycleState1020(t, al, child2.SessionID, session.LifecycleQueued)

	parentTS, err := al.reconstructSteeredTurn(parent, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn(parent): %v", err)
	}
	discardSteeredTurnDrain1020(al.drainSteeredTurn(
		context.Background(), parent, parentTS, turnResult{finalContent: "parent result before children"}, nil))
	if completeErr := al.completeSteeredTurn(
		context.Background(), parent, turnResult{finalContent: "parent result before children"}, nil); completeErr != nil {
		t.Fatalf("completeSteeredTurn(parent with queued descendants): %v", completeErr)
	}
	parentBeforeWake, err := al.GetSessionLifecycleStore().Load(parent.SessionID)
	if err != nil {
		t.Fatalf("Load(parent before wake): %v", err)
	}
	if parentBeforeWake.Terminal() {
		t.Fatalf("parent became terminal before either child completed: state=%q", parentBeforeWake.State)
	}

	// Frozen D2: complete a real admitted execution, never a forged run identity.
	child1 = admitSteeringRepairExecution(t, al, child1.SessionID, child1.Generation)
	if completeErr := al.completeSteeredTurn(
		context.Background(), child1, turnResult{finalContent: "child one result"}, nil); completeErr != nil {
		t.Fatalf("completeSteeredTurn(child 1): %v", completeErr)
	}

	var wakeDone = make(chan error, 1)
	select {
	case wake := <-al.bus.InboundChan():
		go func() {
			_, wakeErr := al.processSystemMessage(context.Background(), wake)
			wakeDone <- wakeErr
		}()
	case <-time.After(5 * time.Second):
		t.Fatal("child 1 completion did not wake the parent")
	}

	select {
	case <-provider.firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("parent wake/re-entry did not reach its live provider turn")
	}

	// Preserve the second-wake oracle; only repair its admission setup (D2).
	child2 = admitSteeringRepairExecution(t, al, child2.SessionID, child2.Generation)
	child2Err := al.completeSteeredTurn(
		context.Background(), child2, turnResult{finalContent: "child two result"}, nil)
	close(provider.releaseFirst)

	select {
	case wakeErr := <-wakeDone:
		if wakeErr != nil {
			t.Errorf("processSystemMessage(parent wake): %v", wakeErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("parent wake/re-entry did not finish after the provider was released")
	}
	if child2Err != nil {
		t.Errorf("completeSteeredTurn(child 2 while parent re-entry live): %v", child2Err)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Errorf("parent provider request count = %d, want 2: child-1 wake plus child-2 queued continuation", len(requests))
	} else if got := countMessagesContaining(requests[1], "child two result"); got != 1 {
		t.Errorf("child-2 wake occurrences in the continuation request = %d, want exactly 1", got)
	}

	for label, id := range map[string]string{
		"parent":  parent.SessionID,
		"child 1": child1.SessionID,
		"child 2": child2.SessionID,
	} {
		rec, loadErr := al.GetSessionLifecycleStore().Load(id)
		if loadErr != nil {
			t.Errorf("Load(%s): %v", label, loadErr)
			continue
		}
		if !rec.Terminal() {
			t.Errorf("%s lifecycle state = %q, want terminal", label, rec.State)
		}
	}

	msgs, _, _, drainErr := al.GetMessageInboxStore().Drain(rootID, parent.SessionID, "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(root completion): %v", drainErr)
	}
	if len(msgs) != 1 {
		t.Fatalf("parent completion count delivered to root = %d, want exactly 1", len(msgs))
	}
	handback, handbackErr := msgs[0].AsSessionMessageHandback()
	if handbackErr != nil {
		t.Fatalf("AsSessionMessageHandback(parent completion): %v", handbackErr)
	}
	if handback.ResultSoFar != "issue 1020 parent response 2" {
		t.Errorf("parent final result = %q, want final continuation result %q", handback.ResultSoFar, "issue 1020 parent response 2")
	}
}

// TestSteeredTurnDrain1020_LiveCheckCloseRaceFallsBackToDurableWake pins the
// IsAlive-to-enqueue race. If a non-terminal recipient's queue closes in that
// window, delivery must use the ordinary durable wake path instead of
// rejecting the child completion.
func TestSteeredTurnDrain1020_LiveCheckCloseRaceFallsBackToDurableWake(t *testing.T) {
	al, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "issue-1020-r2-parent", "issue-1020-r2-child"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})

	hookCalls := 0
	steerUpwardDelivererAfterLiveTurnCheckTestHook = func(sessionID string, generation int) {
		hookCalls++
		if sessionID != parentID || generation != 1 {
			t.Errorf("live-check hook = (%q, %d), want (%q, 1)", sessionID, generation, parentID)
		}
		al.steering.mu.Lock()
		if len(al.steering.queues[sessionID]) != 0 {
			t.Error("live-check hook could not close the empty steering scope")
		} else {
			al.steering.closedGenerations[sessionID] = generation
		}
		al.steering.mu.Unlock()
	}
	t.Cleanup(func() { steerUpwardDelivererAfterLiveTurnCheckTestHook = nil })

	delivery, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "ignored"))
	if err != nil {
		t.Fatalf("Deliver(live-check close race): %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("live-check hook calls = %d, want 1", hookCalls)
	}
	if delivery.Outcome != "woke" {
		t.Errorf("Delivery.Outcome = %q, want woke fallback", delivery.Outcome)
	}
	if got := al.pendingSteeringCountForScope(parentID); got != 0 {
		t.Errorf("live queue depth after fallback = %d, want 0", got)
	}

	select {
	case wake := <-al.bus.InboundChan():
		if wake.AsyncTranscriptSessionID != parentID {
			t.Errorf("fallback wake transcript session = %q, want %q", wake.AsyncTranscriptSessionID, parentID)
		}
		if got := inboundMetadata(wake, "steer_message_id"); got != delivery.MessageID {
			t.Errorf("fallback wake message id = %q, want %q", got, delivery.MessageID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary wake fallback was not published")
	}

	rec, loadErr := lifecycle.Load(parentID)
	if loadErr != nil {
		t.Fatalf("Load(parent after fallback): %v", loadErr)
	}
	if rec.Terminal() {
		t.Fatalf("parent became terminal during fallback: state=%q", rec.State)
	}
}

// TestSteeredTurnDrain1020_SteeringAllLaterMarkerFailureDoesNotLoseConsumedPrefix
// derives its oracle from the durable-consumption contract: marker A may be
// written only if A is processed exactly once. A later marker failure for B
// must not restore A, and the persistent B failure must produce one visible
// abandonment report for B alone.
func TestSteeredTurnDrain1020_SteeringAllLaterMarkerFailureDoesNotLoseConsumedPrefix(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider
	al.SetSteeringMode(SteeringAll)

	originalBackoff := continueDrainBackoff
	continueDrainBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { continueDrainBackoff = originalBackoff })

	// Frozen D2: "Persist it under the lifecycle lock **before admission**,
	// and copy it unchanged into the admission entry and live execution
	// handle." newSteerAL minted the genuine boot epoch; real Dispatch owns
	// this drain. Inject the identical A/B batch at the real final boundary,
	// rather than forging RUNNING and reconstructing an unadmitted claim.
	// R1 preserves the exact consumption/order/abandonment oracles below.
	deliverer := newSteerTurnDrainDeliverer1020()
	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, func(sessionID string) error {
		if err := al.EnqueueSteeringWake(
			sessionID, testDefaultAgentID, sessionID, "issue-1020-batch-wake-a",
			providers.Message{Role: "user", Content: "ISSUE-1020-BATCH-WAKE-A"},
		); err != nil {
			return fmt.Errorf("EnqueueSteeringWake(A): %w", err)
		}
		return al.EnqueueSteeringWake(
			sessionID, testDefaultAgentID, "session_01INVALIDBATCHMARKERTARGET", "issue-1020-batch-wake-b",
			providers.Message{Role: "user", Content: "ISSUE-1020-BATCH-WAKE-B"},
		)
	})
	awaitSteeredDrainOwner1020(t, al)

	requests := provider.Requests()
	if got := countMessagesContaining(flattenProviderRequests1020(requests), "ISSUE-1020-BATCH-WAKE-A"); got != 1 {
		t.Errorf("wake A provider-delivery count = %d, want exactly 1", got)
	}
	if got := countMessagesContaining(flattenProviderRequests1020(requests), "ISSUE-1020-BATCH-WAKE-B"); got != 0 {
		t.Errorf("wake B provider-delivery count = %d, want 0 after its marker failed", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering after persistent wake-B marker failure = %d, want 0 after loud abandonment", got)
	}

	entries, readErr := al.GetSessionStore().ReadTranscript(childID)
	if readErr != nil {
		t.Fatalf("ReadTranscript(child): %v", readErr)
	}
	markerA, markerB, reports := 0, 0, 0
	for _, entry := range entries {
		switch entry.Content {
		case "consumed issue-1020-batch-wake-a":
			markerA++
		case "consumed issue-1020-batch-wake-b":
			markerB++
		}
		if entry.Status == "error" {
			reports++
		}
	}
	if markerA != 1 {
		t.Errorf("wake A consumed-marker count = %d, want exactly 1", markerA)
	}
	if markerB != 0 {
		t.Errorf("wake B consumed-marker count = %d, want 0 because B was not processed", markerB)
	}
	if reports != 1 {
		t.Errorf("abandonment reports = %d, want exactly 1 for wake B alone", reports)
	}
}

// awaitSteeredDrainOwner1020 joins the actual admitted execution, not just
// its terminal record or delivery value. Keep the existing drain budget, but
// fail rather than treat a warning-only timeout as proof of a finished writer.
func awaitSteeredDrainOwner1020(t *testing.T, al *AgentLoop) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		al.steerAdmission().turns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("admitted drain/disposal owner did not finish within the existing five-second drain budget")
	}
}

func flattenProviderRequests1020(requests [][]providers.Message) []providers.Message {
	// Capacity is the actual sum of each request's length, not
	// len(request)*len(requests): later provider requests carry more
	// accumulated history than earlier ones, so per-request length is not
	// uniform and multiplying by one request's length would misestimate it.
	total := 0
	for _, request := range requests {
		total += len(request)
	}
	flattened := make([]providers.Message, 0, total)
	for _, request := range requests {
		flattened = append(flattened, request...)
	}
	return flattened
}

// TestSteeredTurnDrain1020_TerminalDisposalReclaimsClosedScopeTombstones
// repeats normal close-and-dispose across distinct sessions. A tombstone is
// useful only until terminal disposition; retaining one per completed child
// makes queue memory grow with process lifetime.
func TestSteeredTurnDrain1020_TerminalDisposalReclaimsClosedScopeTombstones(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	deliverer := newSteerTurnDrainDeliverer1020()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())),
		nil,
		deliverer,
	)

	const completedSessions = 32
	for i := 0; i < completedSessions; i++ {
		// A fresh deterministic provider per independent execution; only the
		// external model edge is replaced, not admission or disposal.
		agent.Provider = &steerTurnDrainProvider1020{}
		child := launchQueuedSteeredTurnDrainChild1020(
			t, al, testDefaultAgentID, fmt.Sprintf("complete tombstone session %d", i))
		// Frozen D2 requires the producing run's persisted execution identity.
		// Use its real admitted owner and normal drain/disposal, never a
		// synthetic RUNNING record with an empty claim. newSteerAL already
		// minted and registered a genuine boot epoch before these dispatches.
		dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
		if err != nil {
			t.Fatalf("session %d real Dispatch: %v", i, err)
		}
		if dispatched.State != steer.DispatchRunning || dispatched.Generation != child.Generation {
			t.Fatalf("session %d real admission = %+v, want running generation %d", i, dispatched, child.Generation)
		}
		awaitSteeredDrainOwner1020(t, al)

		rec, loadErr := al.GetSessionLifecycleStore().Load(child.SessionID)
		if loadErr != nil {
			t.Fatalf("session %d Load(child): %v", i, loadErr)
		}
		if !rec.Terminal() {
			t.Fatalf("session %d state = %q, want terminal before tombstone cleanup", i, rec.State)
		}
	}

	al.steering.mu.Lock()
	remaining := len(al.steering.closedGenerations)
	al.steering.mu.Unlock()
	if remaining != 0 {
		t.Errorf("closed-scope tombstones after %d terminal disposals = %d, want 0", completedSessions, remaining)
	}
	if got := len(deliverer.Events()); got != completedSessions {
		t.Errorf("upward completion events = %d, want exactly %d", got, completedSessions)
	}
}
