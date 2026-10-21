package agent

// Gate-1 fix round 1 for PR #1345 (parent wake). One test per fixed defect;
// reviewer IDs are in each test's comment. Oracles come from the spec
// (docs/internal/specs/parent-wake-visibility-spec.md) and the founder
// decisions it records, not from the implementation.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// gate1WireSessionMessaging gives a chat-target loop the same session
// messaging stores and steering runtime the gateway wires at boot.
func gate1WireSessionMessaging(t *testing.T, al *AgentLoop) {
	t.Helper()
	home := t.TempDir()
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
		session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	pwWireRuntime(t, al)
}

func gate1CountInMessages(msgs []providers.Message, needle string) int {
	n := 0
	for _, m := range msgs {
		n += strings.Count(m.Content, needle)
	}
	return n
}

// TestParkedQuestion_OutcomeHeldThenDeliveredWithAnswerOnce — PW-D15 / FR-020
// (SFH-01, ARCH-1, SFH-03, ARCH-3, PW-SEC-02, PTA-03). A real parked question
// card holds a helper outcome: no outcome-only model call. The person's answer
// continues the session once and that continuation carries BOTH the answer and
// the held report, exactly once. Afterwards the retained (answered) card
// record no longer holds new outcomes.
func TestParkedQuestion_OutcomeHeldThenDeliveredWithAnswerOnce(t *testing.T) {
	const heldBody = "HELPER-REPORT-HELD-FOR-THE-ANSWER"
	const laterBody = "HELPER-REPORT-AFTER-THE-ANSWER"
	provider := &scriptChatProvider{
		scripted: []*providers.LLMResponse{
			e2eToolCallResponse("call_ask", tools.AskUserQuestionToolName,
				`{"questions":[{"header":"Scope","question":"Single or multiplayer?",`+
					`"options":[{"label":"Single","description":"one player"},`+
					`{"label":"Multi","description":"two players"}]}]}`),
			e2eTextResponse("Continuing with the answer and the helper report."),
		},
		fallback: "later turn",
	}
	al, msgBus := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	reg := askuser.NewRegistry(store, &throughBusResumeDispatcher{msgBus: msgBus}, askuser.Options{})
	al.SetAskUserRegistry(reg)
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	go func() { _ = al.Run(runCtx) }()

	pubCtx, pubCancel := context.WithTimeout(context.Background(), busWaitBudget)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-1", SessionID: sid, Content: "Pick a game with me.",
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel",
		Metadata: map[string]string{"agent_id": "mia"},
	}))
	waitForLLMCallCount(t, provider, 1, busWaitBudget)
	pending := waitForPendingCard(t, reg, sid, busWaitBudget)
	deadline := time.Now().Add(busWaitBudget)
	for al.liveTurnForSession(sid) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.Nil(t, al.liveTurnForSession(sid), "SETUP: the question turn must have parked")

	held, err := al.WakeSession(context.Background(), sid, "finished", "helper-parked:1:final", heldBody)
	require.NoError(t, err)
	require.Equal(t, "held_for_question", held.Disposition, "an unanswered card holds the outcome (PW-D15)")
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, 1, provider.callCount(), "no outcome-only model call while the card is unanswered")

	require.NoError(t, reg.Submit(pending.CardID, sid, "daniel",
		[]askuser.SubmittedAnswer{{Header: "Scope", Selected: []string{"Single"}}}))
	waitForLLMCallCount(t, provider, 2, busWaitBudget)
	second, ok := provider.lastMessages(2)
	require.True(t, ok)
	require.Equal(t, 1, gate1CountInMessages(second, heldBody),
		"the answer's continuation carries the held report exactly once")
	foundResume := false
	for _, m := range second {
		if cardID, ok := askuser.ParseResumeCardID(m.Content); ok && cardID == pending.CardID {
			foundResume = true
		}
	}
	require.True(t, foundResume, "the same continuation carries the person's answer")
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, 2, provider.callCount(), "exactly one continuation for answer + report")

	// The answered card record is retained for history; it must not hold.
	later, err := al.WakeSession(context.Background(), sid, "finished", "helper-later:1:final", laterBody)
	require.NoError(t, err)
	require.Equal(t, "published_idle_wake", later.Disposition,
		"a retained answered card is not an unanswered question")
}

// gate1HeldProvider holds its first call until released; later calls answer at
// once. Every request is captured.
type gate1HeldProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func newGate1HeldProvider() *gate1HeldProvider {
	return &gate1HeldProvider{entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *gate1HeldProvider) Chat(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), msgs...))
	first := len(p.requests) == 1
	p.mu.Unlock()
	if first {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &providers.LLMResponse{Content: "turn done"}, nil
}

func (*gate1HeldProvider) GetDefaultModel() string { return "gate1-held" }
func (p *gate1HeldProvider) open()                 { p.once.Do(func() { close(p.release) }) }
func (p *gate1HeldProvider) snapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

// TestLiveOrdinaryParent_OutcomeJoinsRunningTurn — ARCH-2 / FR-017 / US-6
// AS-3. A real ordinary chat turn (registered under its agent-scoped key) is
// running when a helper outcome arrives: the outcome joins that turn's
// steering queue instead of publishing a separate idle wake, and the model
// sees it once.
func TestLiveOrdinaryParent_OutcomeJoinsRunningTurn(t *testing.T) {
	const liveBody = "HELPER-OUTCOME-JOINS-THE-LIVE-TURN"
	provider := newGate1HeldProvider()
	al, msgBus := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	t.Cleanup(provider.open)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	go func() { _ = al.Run(runCtx) }()
	pubCtx, pubCancel := context.WithTimeout(context.Background(), busWaitBudget)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-live", SessionID: sid, Content: "Start working.",
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel",
		Metadata: map[string]string{"agent_id": "mia"},
	}))
	select {
	case <-provider.entered:
	case <-time.After(busWaitBudget):
		t.Fatal("SETUP: the ordinary turn never reached its provider")
	}
	require.Nil(t, al.getActiveTurnState(sid), "SETUP: an ordinary root is not registered under its bare id")

	joined, err := al.WakeSession(context.Background(), sid, "finished", "helper-live:1:final", liveBody)
	require.NoError(t, err)
	require.Equal(t, "joined_live_turn", joined.Disposition, "a running ordinary parent is joined, not woken again")
	require.Equal(t, steer.DeliveryQueuedIntoLiveTurn, joined.Outcome)

	provider.open()
	deadline := time.Now().Add(busWaitBudget)
	for len(provider.snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	requests := provider.snapshot()
	require.GreaterOrEqual(t, len(requests), 2, "the joined outcome reaches the model")
	total := 0
	for _, req := range requests {
		total += gate1CountInMessages(req, liveBody)
	}
	require.GreaterOrEqual(t, total, 1)
	require.Equal(t, 1, gate1CountInMessages(requests[len(requests)-1], liveBody), "once in the model input")
}

// TestLiveFence_StopHeldInputReleasesItsClaim — cr F2 / SFH-02. A saved input
// joined to a live turn is held by a Stop at the fence. The consumer gives
// back its in-process claim, so the input is not stranded as "in flight".
func TestLiveFence_StopHeldInputReleasesItsClaim(t *testing.T) {
	al, lifecycle, _, _ := newPWDeliverTestLoop(t)
	const parentID, childID = "gate1-stop-parent", "gate1-stop-child"
	pwSeedDeliverySessions(t, al, parentID, childID)
	_, err := al.GetSessionStore().SaveChatInput(parentID, "saved-input-o-stop", "parent-agent", "held body")
	require.NoError(t, err)
	al.asyncNotifier.sessionWakes.Store(parentID+"\x00o-stop", true)
	require.NoError(t, lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleStopped
		rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:owner", Seq: 1, Cause: session.StopCauseStop}
		return nil
	}))
	inst, ok := al.GetRegistry().GetAgent("parent-agent")
	require.True(t, ok)
	ts := &turnState{transcriptSessionID: parentID, agent: inst}
	include, err := al.admitSteeringInput(ts, providers.Message{Role: "user", Content: "held body"}, savedWakeCorrelationPrefix+"o-stop")
	require.NoError(t, err)
	require.False(t, include, "a Stop holds the input at the fence")
	require.False(t, al.wakePublicationInFlight(parentID, "o-stop"),
		"the held input's claim is released so the next legitimate fence can place it")
	pending, err := al.GetSessionStore().PendingChatInputs(context.Background(), parentID)
	require.NoError(t, err)
	require.Len(t, pending, 1, "the saved input stays pending, not consumed")
}

// TestQueuedSavedWake_PromotedTurnSeesItOnce — cr F3 / PW-D16. A saved wake
// queued at the admission cap is promoted: the model sees the saved report
// once (placed from its saved record), never also as a fresh user message.
func TestQueuedSavedWake_PromotedTurnSeesItOnce(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxParallelAgents = 1
	wireSteerCompletionDeps(t, al)
	provider := &queuedWakeCaptureProvider{entered: make(chan struct{}), release: make(chan struct{})}
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	agentInst.Provider = provider
	var releaseOnce sync.Once
	releaseBusy := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(releaseBusy)

	steerer := newTestSteeringSession(t, al, "ws-1")
	busyID, busyGen := launchSteeredChild(t, al, steerer, "gate1-queued-busy", "occupy the only slot")
	wokenID, wokenGen := launchSteeredChild(t, al, steerer, "gate1-queued-recipient", "original launch instruction")
	busy, err := NewSteerLauncher(al).Dispatch(context.Background(), busyID, busyGen)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, busy.State)
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: busy turn never reached its provider")
	}

	const savedBody = "SAVED-DESCENDANT-OUTCOME-QUEUED-AT-THE-CAP"
	_, err = al.GetSessionStore().SaveChatInput(wokenID, "saved-input-gate1-saved-wake", testDefaultAgentID, savedBody)
	require.NoError(t, err)
	_, err = al.processSteeredSystemWake(context.Background(), bus.InboundMessage{
		Channel: "system", AsyncTranscriptSessionID: wokenID, Content: savedBody,
		Metadata: map[string]string{"steer_message_id": "gate1-saved-wake", "steer_generation": strconv.Itoa(wokenGen)},
	})
	require.NoError(t, err)
	queued, err := al.GetSessionLifecycleStore().Load(wokenID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, queued.State)

	gate := al.steerAdmission()
	releaseBusy()
	joined := make(chan struct{})
	go func() { gate.turns.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(30 * time.Second):
		t.Fatal("busy and promoted turns did not finish")
	}
	provider.mu.Lock()
	requests := append([][]providers.Message(nil), provider.requests...)
	provider.mu.Unlock()
	require.Len(t, requests, 2, "one busy turn and one promoted turn")
	require.Equal(t, 1, gate1CountInMessages(requests[1], savedBody),
		"the promoted turn's model input carries the saved report exactly once")
}

// TestBoot_RepairsRowsRetainedByNoEdgeTaskRoot — cr F4 / SFH-05 / PTA-04. A
// create_task root (no steering edge, already completed) retained a helper
// event row for its captured recipient after the row write failed. Production
// boot recovery restores that row once; the payload lives on the lifecycle
// journal, not the inbox, so inbox acknowledgement/compaction cannot lose it.
func TestBoot_RepairsRowsRetainedByNoEdgeTaskRoot(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	recipient := h.rootSession(t)
	taskID := h.newSession(t, session.SessionTypeTask, "")
	h.persist(t, &session.LifecycleRecord{
		SessionID: taskID, Generation: 1, State: session.LifecycleCompleted,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "gate1-task"}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "agent-1",
	})
	const text = "Task run done: the export is written."
	require.NoError(t, h.lifecycle.SaveHelperEventRepair(taskID, session.HelperEventRepair{
		ID: "repair-gate1-task-row", SourceID: "gate1-task-final", Destinations: []string{recipient},
		Event: generated.HelperEvent{HelperSessionId: taskID, Kind: generated.HelperEventKindFinished, Text: text, CreatedAt: time.Now().UTC()},
	}))
	require.NoError(t, h.recovery().Run(context.Background()))
	rows := 0
	entries, err := h.sessions.ReadTranscript(recipient)
	require.NoError(t, err)
	for _, e := range entries {
		if e.SystemSubtype == "helper_event" && e.Content == text {
			rows++
		}
	}
	require.Equal(t, 1, rows, "boot restores the retained row for an edgeless task root")
	pending, err := h.lifecycle.PendingHelperEventRepairs(taskID)
	require.NoError(t, err)
	require.Empty(t, pending, "the repaired payload retires")
}

// TestPolledFinal_CrashBeforeWakeStillRepairsTheRow — SFH-04. The process
// dies right after the helper final became pollable in the parent's inbox.
// The parent polls and consumes it. The lasting finished row is still
// restored by the delivery-only repair driver, with no parent wake.
func TestPolledFinal_CrashBeforeWakeStillRepairsTheRow(t *testing.T) {
	al, _, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "gate1-poll-parent", "gate1-poll-child"
	pwSeedDeliverySessions(t, al, parentID, childID)
	wakes := pwObserveNotifier(t, al)
	steerUpwardAfterInboxAppendTestHook = func(string, string) error { return errors.New("injected crash after inbox append") }
	t.Cleanup(func() { steerUpwardAfterInboxAppendTestHook = nil })

	_, err := deliverer.Deliver(context.Background(), handbackEvent(childID, "ignored"))
	require.Error(t, err, "SETUP: the injected crash interrupts delivery after the inbox append")
	steerUpwardAfterInboxAppendTestHook = nil
	finalID := childID + ":1:final"
	already, err := al.recordPolledFinalConsumed(parentID, "parent-agent", finalID)
	require.NoError(t, err)
	require.False(t, already)

	require.NoError(t, al.repairHelperEvents(childID))
	rows := pwHelperEventRows(t, al, parentID, "finished")
	require.Len(t, rows, 1, "the finished row is restored once after the crash")
	require.Equal(t, 0, wakes(), "repair never wakes the parent")
}

// TestResume_RefusedWhenRecordedWorkspaceNoLongerHoldsTheAgent — PW-SEC-01.
// The helper was launched in workspace A; the agent was removed from A but is
// still a member of another workspace. Restart recovery must not continue A's
// saved work elsewhere: no admission, and a visible "Resume did not start."
// row.
func TestResume_RefusedWhenRecordedWorkspaceNoLongerHoldsTheAgent(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	const wsA = "gate1-ws-a"
	dir := filepath.Join(omnipusHome(), "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, wsA+".json"),
		[]byte(`{"id":"`+wsA+`","name":"A","core_team":["someone-else"]}`), 0o600))
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	rec.WorkspaceID = wsA
	h.persist(t, rec)
	h.seedCompletedToolHistory(t, child)

	require.NoError(t, h.recovery().Run(context.Background()))
	after, err := h.lifecycle.Load(child)
	require.NoError(t, err)
	require.NotEqual(t, session.LifecycleRunning, after.State, "no automatic resume outside workspace A")
	require.NotEqual(t, session.LifecycleQueued, after.State)
	select {
	case <-h.provider.requests:
		t.Fatal("the refused helper reached its provider")
	case <-time.After(300 * time.Millisecond):
	}
	refused := 0
	entries, err := h.sessions.ReadTranscript(parent)
	require.NoError(t, err)
	for _, e := range entries {
		if e.SystemSubtype == "helper_event" && strings.Contains(e.Content, "Resume did not start.") {
			refused++
		}
	}
	require.Equal(t, 1, refused, "the refusal is visible in the parent chat")
}

// TestDeliver_OtherRecipientsFailedRepairDoesNotBlockThisOne — ARCH-5. The
// child still owes a row to another destination whose write keeps failing.
// That failure stays retained and reported, but this healthy recipient still
// gets its outcome and its wake.
func TestDeliver_OtherRecipientsFailedRepairDoesNotBlockThisOne(t *testing.T) {
	al, lifecycle, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "gate1-iso-parent", "gate1-iso-child"
	pwSeedDeliverySessions(t, al, parentID, childID)
	require.NoError(t, lifecycle.SaveHelperEventRepair(childID, session.HelperEventRepair{
		ID: "repair-gate1-ghost", SourceID: "gate1-older-event", Destinations: []string{"gate1-ghost-recipient"},
		Event: generated.HelperEvent{HelperSessionId: childID, Kind: generated.HelperEventKindFinished, Text: "older", CreatedAt: time.Now().UTC()},
	}))
	wakes := pwObserveNotifier(t, al)
	delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeParkedQuestion, Message: pwAgentQuestion(t, childID, "gate1-q", "Which file?"),
	})
	require.NoError(t, err, "another destination's failed repair must not fail this delivery")
	require.Equal(t, steer.DeliveryWoke, delivery.Outcome)
	require.Equal(t, 1, wakes())
	pending, err := lifecycle.PendingHelperEventRepairs(childID)
	require.NoError(t, err)
	stillPending := false
	for _, r := range pending {
		if r.ID == "repair-gate1-ghost" {
			stillPending = true
		}
	}
	require.True(t, stillPending, "the failed repair stays retained for a later retry")
}

// TestDeliver_PauseHandbackWithGoalIsANonEndingRow — SFH-06. A goal-bearing
// helper's pause hand-back is a non-ending report: it gets its own blocker
// row, never the goal-ending path.
func TestDeliver_PauseHandbackWithGoalIsANonEndingRow(t *testing.T) {
	al, lifecycle, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "gate1-pause-parent", "gate1-pause-child"
	pwSeedDeliverySessions(t, al, parentID, childID)
	goalID := activateTestGoalRecord(t, childID, "keep working on the export")
	require.NoError(t, lifecycle.Mutate(childID, func(rec *session.LifecycleRecord) error {
		rec.GoalRef = goalID
		return nil
	}))
	_, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeBlocker, Message: pauseHandbackMessage(t, childID, "gate1-pause"),
	})
	require.NoError(t, err)
	require.Len(t, pwHelperEventRows(t, al, parentID, string(generated.HelperEventKindBlocker)), 1,
		"a pause hand-back writes one non-ending blocker row")
	require.Empty(t, pwHelperEventRows(t, al, parentID, string(generated.HelperEventKindFinished)),
		"a pause is never labelled finished")
}

// TestResume_CompletedToolNotRepeatedThroughBootRecovery — PTA-01 (coverage,
// no production change). Through real boot recovery, the resumed helper's next
// model request carries the historical successful result, and the archive
// still holds exactly one result for that call: the completed tool operation
// is not executed again.
func TestResume_CompletedToolNotRepeatedThroughBootRecovery(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
	h.seedCompletedToolHistory(t, child)

	require.NoError(t, h.recovery().Run(context.Background()))
	request := h.provider.await(t)
	historical := 0
	for _, m := range request {
		if m.Role == "tool" && m.ToolCallID == "call_0" && m.Content == "historical successful result" {
			historical++
		}
	}
	require.Equal(t, 1, historical, "resume continues after the saved successful result")
	h.provider.open()
	joinGoalFixtureRuns(t, h.al)
	view, err := h.sessions.WindowView(context.Background(), child)
	require.NoError(t, err)
	results := 0
	for _, line := range viewArchived(view) {
		if line.Role == "tool" && line.ToolCallID == "call_0" {
			results++
		}
	}
	require.Equal(t, 1, results, fmt.Sprintf("call_0 results in the archive = %d; the completed tool must not run again", results))
}
