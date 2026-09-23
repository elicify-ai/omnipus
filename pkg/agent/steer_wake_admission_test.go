// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-3 — the wake is an ENTRY PATH, and the two
// properties every entry path owes:
//
//  1. I-6's reservation. "Stop stamps a durable marker on every session it
//     reaches, and no dispatch starts a turn on a stamped session" (landing
//     order §0). steer_audience.go::Deliver's FR-B-013 check reads the
//     recipient's record at SEND time; a Stop landing between that read and
//     the wake's own turn was caught nowhere at all, and the stopped session
//     went back to work.
//
//  2. I-3's admission gate. D9 and the founder's round-9 answer make the
//     concurrency counter count "turns executing right now" — a woken turn
//     that never touches steerAdmission makes max_parallel_agents count
//     DISPATCHES instead, which is not the number the settings screen
//     describes.
//
// Both run a real *AgentLoop (newSteerAL) with its real session, lifecycle
// and inbox stores — never a spy standing in for any of them.

package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// appendWakeInboxEntry stores one durable inbox entry for childID under its
// OWN owner key (the woken session is the recipient) and returns its id, so a
// test can assert whether the wake was consumed or left pending.
func appendWakeInboxEntry(t *testing.T, al *AgentLoop, ownerID, text string) string {
	t.Helper()
	var msg generated.SessionMessage
	if err := msg.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId:      "wake-entry-" + ownerID,
		SessionId:      ownerID,
		CreatedAt:      time.Now().UTC(),
		Depth:          1,
		SenderIdentity: testDefaultAgentID,
		Mode:           generated.SessionMessageHandbackModeFinal,
		ResultSoFar:    text,
		Artifacts:      []string{},
		OpenQuestions:  []string{},
	}); err != nil {
		t.Fatalf("encode handback: %v", err)
	}
	res, err := al.GetMessageInboxStore().Append(ownerID, msg)
	if err != nil {
		t.Fatalf("Append(wake inbox entry): %v", err)
	}
	return res.MessageID
}

func wakeMessage(sessionID, messageID string, generation int) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:                  "system",
		AsyncTranscriptSessionID: sessionID,
		Content:                  "a worker you delegated to has finished",
		Metadata: map[string]string{
			"steer_message_id": messageID,
			"steer_generation": strconv.Itoa(generation),
		},
	}
}

// TestWake_StopLandingBeforeTheTurnStopsTheSession proves I-6 on the wake
// route: a Stop stamped after the wake has been decided on and before its
// turn starts must refuse the turn, must survive on disk, and must leave the
// wake entry unconsumed (FR-B-013 — "the entry is stored and acknowledged at
// revival", not swallowed by a turn that never ran).
func TestWake_StopLandingBeforeTheTurnStopsTheSession(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	steerer := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, steerer, "call-wake-stop", "parked work that must not resume")
	messageID := appendWakeInboxEntry(t, al, childID, "your own worker finished")

	canceller := NewSteerCanceller(lifecycle, al.SteerGenerationCancel)
	var once sync.Once
	dispatchStateWriteTestHook = func(hookSessionID string, _ int) {
		if hookSessionID != childID {
			return
		}
		once.Do(func() {
			if _, err := canceller.CancelSubtree(context.Background(), childID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("CancelSubtree inside the wake window: %v", err)
			}
		})
	}
	t.Cleanup(func() { dispatchStateWriteTestHook = nil })

	_, err := al.processSteeredSystemWake(context.Background(), wakeMessage(childID, messageID, childGen))
	if err == nil {
		t.Errorf("processSteeredSystemWake(stopped mid-window) = nil error; want a refusal — a stopped session must never be woken into a turn")
	}
	if !errors.Is(err, steer.ErrDispatchCancelled) {
		t.Errorf("processSteeredSystemWake error = %v, want ErrDispatchCancelled", err)
	}

	rec, loadErr := lifecycle.Load(childID)
	if loadErr != nil {
		t.Fatalf("Load(child): %v", loadErr)
	}
	if rec.Stop == nil {
		t.Fatalf("no Stop marker on disk (state=%q) — the wake either never consulted I-6's reservation "+
			"at all or wrote its stale snapshot back over the marker", rec.State)
	}
	if rec.State == session.LifecycleRunning {
		t.Errorf("persisted State = running; the stopped session must be left un-started")
	}
	if ts := al.getActiveTurnState(childID); ts != nil {
		t.Errorf("a turn is still registered for the stopped session %s — the refused wake must not leave one behind", childID)
	}
	if al.steerAdmission().hasReservation(childID, childGen) {
		t.Errorf("the refused wake kept its admission slot")
	}

	entries, readErr := al.GetSessionStore().ReadTranscript(childID)
	if readErr != nil {
		t.Fatalf("ReadTranscript(child): %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Content, "consumed ") {
			t.Errorf("the refused wake wrote a consumed marker (%q) — the entry must stay pending for revival", entry.Content)
		}
	}
	pending, _, _, drainErr := al.GetMessageInboxStore().Drain(childID, "", "", 10)
	if drainErr != nil {
		t.Fatalf("Drain(child inbox): %v", drainErr)
	}
	if len(pending) != 1 {
		t.Errorf("pending inbox entries = %d, want 1 — the refused wake acknowledged an entry no turn ever read", len(pending))
	}
}

// TestWake_WokenTurnIsCountedByTheConcurrencyGate proves I-3/D9 on the wake
// route: with max_parallel_agents pinned to 1 and that one slot held by a
// live steered turn, a wake for a SECOND steered session must not start a
// second turn — it is queued, exactly as a first dispatch at the cap is.
//
// Regression for the defect this test's absence hid: processSteeredSystemWake
// registered a turn and ran it without ever calling tryAdmit, so the gate
// counted dispatches rather than turns actually executing.
func TestWake_WokenTurnIsCountedByTheConcurrencyGate(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxParallelAgents = 1

	provider := &dispatchContextProvider{entered: make(chan struct{}), release: make(chan struct{})}
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	agentInst.Provider = provider

	launcher := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")
	busyID, busyGen := launchSteeredChild(t, al, steerer, "call-wake-busy", "occupies the only slot")
	wokenID, wokenGen := launchSteeredChild(t, al, steerer, "call-wake-queued", "must not run while the slot is taken")
	messageID := appendWakeInboxEntry(t, al, wokenID, "your own worker finished")

	busy, err := launcher.Dispatch(context.Background(), busyID, busyGen)
	if err != nil {
		t.Fatalf("Dispatch(busy): %v", err)
	}
	if busy.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(busy) = %+v, want State=running", busy)
	}
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the busy child never reached its provider")
	}

	// The wake is driven from its own goroutine: an ungated wake RUNS the
	// turn inline, and this provider blocks until released, so a direct call
	// would hang the test rather than report the defect.
	var releaseOnce sync.Once
	releaseBusy := func() { releaseOnce.Do(func() { close(provider.release) }) }
	defer releaseBusy()

	woke := make(chan error, 1)
	go func() {
		_, wakeErr := al.processSteeredSystemWake(context.Background(), wakeMessage(wokenID, messageID, wokenGen))
		woke <- wakeErr
	}()
	select {
	case wakeErr := <-woke:
		if wakeErr != nil {
			t.Fatalf("processSteeredSystemWake(at cap) = %v, want a clean queue", wakeErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the wake is still running a SECOND turn while the only admission slot is taken — " +
			"a woken turn never reaches the admission gate, so max_parallel_agents counts dispatches, not executing turns")
	}

	gate := al.steerAdmission()
	if ts := al.getActiveTurnState(wokenID); ts != nil {
		t.Errorf("the wake ran a SECOND turn while the only admission slot was taken — the gate counts dispatches, not executing turns")
	}
	if got := gate.activeCount(); got != 1 {
		t.Errorf("active admission slots = %d, want exactly 1 (the busy child)", got)
	}
	if got := gate.queueLen(); got != 1 {
		t.Errorf("queue length = %d, want 1 (the woken session waiting for a slot)", got)
	}
	rec, loadErr := al.GetSessionLifecycleStore().Load(wokenID)
	if loadErr != nil {
		t.Fatalf("Load(woken): %v", loadErr)
	}
	if rec.State != session.LifecycleQueued {
		t.Errorf("woken session state = %q, want queued", rec.State)
	}

	releaseBusy()
	if ts := al.getActiveTurnState(busyID); ts != nil {
		select {
		case <-ts.Finished():
		case <-time.After(30 * time.Second):
			t.Fatal("the busy child did not finish after its provider was released")
		}
	}
	promoted := waitForActiveTurn(t, al, wokenID, 30*time.Second)
	select {
	case <-promoted.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("the promoted woken session's turn did not finish within 30s")
	}
}
