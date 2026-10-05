// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for issue #1020: a steered child must drain steering
// queued after runTurn's final internal dequeue. BoundaryFinalReply is the
// deterministic boundary: runDispatchedSteeredTurn invokes it synchronously
// after runTurn returns and before disposing the child's result.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	steerTurnDrainWakeText1020      = "ISSUE-1020-LATE-UPWARD-WAKE"
	steerTurnDrainMessageText1020   = "ISSUE-1020-LATE-DELEGATE-STEER"
	steerTurnDrainWakeMessageID1020 = "issue-1020-wake-message"
)

type steerTurnDrainProvider1020 struct {
	mu       sync.Mutex
	requests [][]providers.Message
}

func (p *steerTurnDrainProvider1020) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	call := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()

	if call > 1 {
		return nil, fmt.Errorf("issue 1020 provider received unexpected call %d", call+1)
	}
	return &providers.LLMResponse{
		Content:      fmt.Sprintf("issue 1020 response %d", call+1),
		FinishReason: "stop",
	}, nil
}

func (p *steerTurnDrainProvider1020) GetDefaultModel() string { return "issue-1020-drain" }

func (p *steerTurnDrainProvider1020) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		requests[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return requests
}

type steerTurnDrainObserver1020 struct {
	once  sync.Once
	hook  func(sessionID string) error
	mu    sync.Mutex
	err   error
	calls int
	runs  int
}

func (o *steerTurnDrainObserver1020) Observe(boundary steer.Boundary, sessionID string, _ steer.Audience) {
	if boundary != steer.BoundaryFinalReply {
		return
	}
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()
	o.once.Do(func() {
		if o.hook == nil {
			return
		}
		err := o.hook(sessionID)
		o.mu.Lock()
		o.err = err
		o.runs++
		o.mu.Unlock()
	})
}

func (o *steerTurnDrainObserver1020) result() (calls, runs int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls, o.runs, o.err
}

type steerTurnDrainDeliverer1020 struct {
	once      sync.Once
	delivered chan struct{}
	mu        sync.Mutex
	events    []steer.UpwardEvent
}

func newSteerTurnDrainDeliverer1020() *steerTurnDrainDeliverer1020 {
	return &steerTurnDrainDeliverer1020{delivered: make(chan struct{})}
}

func (d *steerTurnDrainDeliverer1020) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	d.mu.Lock()
	d.events = append(d.events, event)
	d.mu.Unlock()
	d.once.Do(func() { close(d.delivered) })
	return steer.Delivery{MessageID: event.ChildSessionID + ":1:final", Outcome: steer.DeliveryWoke}, nil
}

func (d *steerTurnDrainDeliverer1020) Events() []steer.UpwardEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]steer.UpwardEvent(nil), d.events...)
}

type resetCountingMessageTool1020 struct {
	tools.BaseTool
	mu     sync.Mutex
	resets int
}

func (t *resetCountingMessageTool1020) Name() string           { return "send_message" }
func (t *resetCountingMessageTool1020) Description() string    { return "issue 1020 reset observer" }
func (t *resetCountingMessageTool1020) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (t *resetCountingMessageTool1020) Category() tools.ToolCategory {
	return tools.CategoryCommunication
}
func (t *resetCountingMessageTool1020) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *resetCountingMessageTool1020) Execute(context.Context, map[string]any) *tools.ToolResult {
	return &tools.ToolResult{}
}
func (t *resetCountingMessageTool1020) ResetSentInRound() {
	t.mu.Lock()
	t.resets++
	t.mu.Unlock()
}
func (t *resetCountingMessageTool1020) ResetCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.resets
}

func newSteerTurnDrainMultiAgentAL1020(t *testing.T, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir test home: %v", err)
	}
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{
			Home:              home,
			DefaultAgentID:    testDefaultAgentID,
			DefaultModel:      config.DefaultModel{Model: "test-model"},
			MaxTokens:         4096,
			MaxToolIterations: 10,
		},
		List: []config.AgentConfig{
			{ID: testDefaultAgentID, Home: home},
			{ID: "ray", Home: home},
		},
	}}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	t.Cleanup(func() { al.Close() })
	return al
}

func launchQueuedSteeredTurnDrainChild1020(t *testing.T, al *AgentLoop, targetAgentID, task string) steer.LaunchResult {
	t.Helper()
	parentID := newTestSteeringSession(t, al, "ws-1")
	launched, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     targetAgentID,
		Task:              task,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "issue-1020"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	return launched
}

func launchSteeredTurnDrainChild1020(
	t *testing.T,
	al *AgentLoop,
	provider *steerTurnDrainProvider1020,
	deliverer *steerTurnDrainDeliverer1020,
	hook func(sessionID string) error,
) string {
	t.Helper()

	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider
	observer := &steerTurnDrainObserver1020{hook: hook}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), observer, deliverer)

	launched := launchQueuedSteeredTurnDrainChild1020(
		t, al, testDefaultAgentID, "exercise the issue 1020 post-turn drain")
	var err error
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dispatched.State != steer.DispatchRunning {
		t.Fatalf("Dispatch state = %q, want %q", dispatched.State, steer.DispatchRunning)
	}

	select {
	case <-deliverer.delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the steered child to reach upward delivery")
	}
	boundaryCalls, hookRuns, hookErr := observer.result()
	if hookErr != nil {
		t.Fatalf("BoundaryFinalReply steering injection: %v", hookErr)
	}
	if boundaryCalls == 0 {
		t.Fatal("BoundaryFinalReply was not observed")
	}
	if hook != nil && hookRuns != 1 {
		t.Fatalf("BoundaryFinalReply steering injection count = %d, want exactly 1", hookRuns)
	}
	return launched.SessionID
}

func finalHandbackResult1020(t *testing.T, deliverer *steerTurnDrainDeliverer1020) string {
	t.Helper()
	events := deliverer.Events()
	if len(events) != 1 {
		t.Fatalf("upward completion event count = %d, want exactly 1", len(events))
	}
	if events[0].Outcome != steer.OutcomeFinalAnswer {
		t.Fatalf("upward completion outcome = %q, want %q", events[0].Outcome, steer.OutcomeFinalAnswer)
	}
	handback, err := events[0].Message.AsSessionMessageHandback()
	if err != nil {
		t.Fatalf("AsSessionMessageHandback: %v", err)
	}
	return handback.ResultSoFar
}

func discardSteeredTurnDrain1020(_ *turnState, _ turnResult, _ error) {}

func TestSteeredTurnDrain1020_LateWakeContinuesChildWithoutRestart(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, func(sessionID string) error {
		return al.EnqueueSteeringWake(
			sessionID,
			testDefaultAgentID,
			sessionID,
			steerTurnDrainWakeMessageID1020,
			providers.Message{Role: "user", Content: steerTurnDrainWakeText1020},
		)
	})

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Errorf("child provider request count = %d, want 2: initial turn plus late-wake continuation", len(requests))
	} else if got := countMessagesContaining(requests[1], steerTurnDrainWakeText1020); got != 1 {
		t.Errorf("late wake occurrences in continuation request = %d, want exactly 1", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after child delivery = %d, want 0", got)
	}

	entries, err := al.GetSessionStore().ReadTranscript(childID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	consumed := 0
	for _, entry := range entries {
		if entry.Content == "consumed "+steerTurnDrainWakeMessageID1020 {
			consumed++
		}
	}
	if consumed != 1 {
		t.Errorf("wake consumed-marker count = %d, want exactly 1", consumed)
	}
}

func TestSteeredTurnDrain1020_LateDelegateSteerContinuesChildWithoutRestart(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, func(sessionID string) error {
		_, err := al.EnqueueSteeringMessage(
			sessionID,
			testDefaultAgentID,
			providers.Message{Role: "user", Content: steerTurnDrainMessageText1020},
			"issue-1020-late-delegate-steer",
		)
		return err
	})

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Errorf("child provider request count = %d, want 2: initial turn plus late-steer continuation", len(requests))
	} else if got := countMessagesContaining(requests[1], steerTurnDrainMessageText1020); got != 1 {
		t.Errorf("late delegate-steer occurrences in continuation request = %d, want exactly 1", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after child delivery = %d, want 0", got)
	}
	al.drainSteeredTurns(5 * time.Second)
	if got := finalHandbackResult1020(t, deliverer); got != "issue 1020 response 2" {
		t.Errorf("upward handback result = %q, want continuation result %q", got, "issue 1020 response 2")
	}
}

func TestSteeredTurnDrain1020_EmptyQueueAddsNoContinuation(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, nil)

	if got := len(provider.Requests()); got != 1 {
		t.Errorf("child provider request count = %d, want exactly 1 when the post-turn queue is empty", got)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after empty-queue child delivery = %d, want 0", got)
	}
}

func TestSteeredTurnDrain1020_EnqueueAfterFinalEmptyCheckIsConsumedOrRefused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	var hookMu sync.Mutex
	var enqueueErr error
	var hookCalls int
	var injected bool
	// Round-4 fixture correction (qa-lead, squad-lead ruling on 098be2a05):
	// completeStateWriteTestHook fires on EVERY terminal write, not just
	// this child's original one — production used to clear it after firing
	// (completeStateWriteTestHook = nil), a production write into a test
	// hook that 098be2a05 removed as an integrity violation. Injecting
	// unconditionally meant the REVIVED generation's own commit also saw a
	// "new" late steer and revived again, forever. Inject exactly once —
	// on the first call, which fires during the original generation's
	// commit — and only COUNT every later call.
	completeStateWriteTestHook = func(sessionID string) {
		hookMu.Lock()
		hookCalls++
		first := !injected
		if first {
			injected = true
		}
		hookMu.Unlock()
		if !first {
			return
		}
		_, err := al.EnqueueSteeringMessage(
			sessionID,
			testDefaultAgentID,
			providers.Message{Role: "user", Content: "ISSUE-1020-AFTER-FINAL-EMPTY-CHECK"},
			"issue-1020-after-final-empty-check",
		)
		hookMu.Lock()
		enqueueErr = err
		hookMu.Unlock()
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, nil)
	al.drainSteeredTurns(5 * time.Second)

	hookMu.Lock()
	errAtEnqueue, calls := enqueueErr, hookCalls
	hookMu.Unlock()
	// Derivation: 2 terminal writes total — the child's original
	// generation (where the fixture's one late steer is injected) and the
	// generation it revives to (whose own commit finds nothing new to
	// inject, since this fixture injects only once, and so does not
	// revive again).
	if calls != 2 {
		t.Fatalf("post-drain enqueue hook count = %d, want exactly 2 (one terminal write for the original generation, one for the generation it revives)", calls)
	}
	if errAtEnqueue == nil {
		if got := al.pendingSteeringCountForScope(childID); got != 0 {
			t.Errorf("enqueue reported success after the drain closed, but pending count = %d; want 0 because a successful enqueue must still have a consumer", got)
		}
		if got := len(provider.Requests()); got != 2 {
			t.Errorf("enqueue reported success after the drain closed, but provider request count = %d; want 2 because the late item must run", got)
		}
	}
}

// steerDrainAdmissionProviderQ2Fixture is used ONLY by the two direct-call
// fixture migrations below. Install it before Dispatch and never replace the
// shared AgentInstance.Provider while a real run is in flight. Both model
// counters stay exact: the first real request is setup; subsequent requests
// reach the independently measured continuation provider.
type steerDrainAdmissionProviderQ2Fixture struct {
	mu           sync.Mutex
	calls        int
	initial      providers.LLMProvider
	continuation providers.LLMProvider
}

func (p *steerDrainAdmissionProviderQ2Fixture) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	provider := p.continuation
	if p.calls == 1 {
		provider = p.initial
	}
	p.mu.Unlock()
	return provider.Chat(ctx, messages, definitions, model, options)
}

func (p *steerDrainAdmissionProviderQ2Fixture) GetDefaultModel() string {
	return p.continuation.GetDefaultModel()
}

func TestSteeredTurnDrain1020_TwoItemsPersistentPreDequeueFailureAreAllAbandoned(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default agent is not registered")
	}
	boot := session.NewBootEpochStore(al.GetConfig().Agents.Defaults.Home)
	epoch, mintErr := boot.Mint()
	if mintErr != nil || epoch == 0 {
		t.Fatalf("Mint genuine fixture boot epoch = %d, error=%v", epoch, mintErr)
	}
	al.SetBootEpochStore(boot)
	// The required initial admission is a REAL live producer, parked only at
	// the external model boundary. Its request is counted separately from the
	// continuation provider whose unchanged zero-call oracle is below.
	initialProvider := &lateSteeringProvider{
		firstCallStarted: make(chan struct{}), releaseFirstCall: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseInitial := func() { releaseOnce.Do(func() { close(initialProvider.releaseFirstCall) }) }
	t.Cleanup(releaseInitial)
	agent.Provider = &steerDrainAdmissionProviderQ2Fixture{initial: initialProvider, continuation: provider}
	wireSteerCompletionDeps(t, al)
	child := launchQueuedSteeredTurnDrainChild1020(
		t, al, testDefaultAgentID, "exercise two-item abandonment after persistent pre-dequeue failure")
	dispatched, dispatchErr := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if dispatchErr != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("real initial Dispatch = %+v, error=%v, want running", dispatched, dispatchErr)
	}
	select {
	case <-initialProvider.firstCallStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("real initial admitted producer did not reach its external model boundary")
	}
	lifecycle := al.GetSessionLifecycleStore()
	snapshot, err := lifecycle.Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	ts := al.getActiveTurnState(child.SessionID)
	if ts == nil {
		t.Fatal("real initial producer has no registered live handle")
	}
	original := al.tsExecutionClaim(ts, child.SessionID)
	if original.SessionID != child.SessionID || original.Generation != child.Generation || original.BootSeq != epoch ||
		original.RunID == "" || !original.matches(snapshot) || !al.steerAdmission().hasExecutionReservation(original) {
		t.Fatalf("real initial producer claim/record/reservation mismatch: claim=%+v record=%+v", original, snapshot)
	}
	// The stable pre-dispatch provider routes any erroneous continuation
	// request to the measured provider; no shared provider swap is needed.
	for i := 1; i <= 2; i++ {
		if _, enqueueErr := al.EnqueueSteeringMessage(
			child.SessionID,
			testDefaultAgentID,
			providers.Message{Role: "user", Content: fmt.Sprintf("queued item %d", i)},
			fmt.Sprintf("issue-1020-abandon-%d", i),
		); enqueueErr != nil {
			t.Fatalf("EnqueueSteeringMessage(item %d): %v", i, enqueueErr)
		}
	}

	originalBackoff := continueDrainBackoff
	continueDrainBackoff = []time.Duration{0, 0, 0}
	t.Cleanup(func() { continueDrainBackoff = originalBackoff })
	logPath := filepath.Join(t.TempDir(), "steered-drain-abandonment.jsonl")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)

	// Keep the original REAL producer registered: Continue must fail before
	// dequeue, exactly as the old fake blocker modeled, without forging a
	// live handle or borrowing an admission's queued identity.
	if al.getActiveTurnState(child.SessionID) != ts || !al.steerAdmission().hasExecutionReservation(original) {
		t.Fatal("original producer lost its real pre-dequeue failure barrier")
	}
	discardSteeredTurnDrain1020(al.drainSteeredTurn(
		context.Background(), snapshot, ts, turnResult{finalContent: "initial response"}, nil))
	logger.DisableFileLogging()

	logData, logErr := os.ReadFile(logPath)
	if logErr != nil {
		t.Fatalf("ReadFile(drain abandonment log): %v", logErr)
	}
	matchedAbandonment := 0
	for _, line := range strings.Split(strings.TrimSpace(string(logData)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if decodeErr := json.Unmarshal([]byte(line), &entry); decodeErr != nil {
			t.Fatalf("decode drain abandonment log line: %v; line=%q", decodeErr, line)
		}
		if entry["message"] != "steer: persistent Continue failure — abandoning queued steering" ||
			entry["session_id"] != child.SessionID {
			continue
		}
		matchedAbandonment++
		if entry["attempts"] != float64(continueDrainMaxRetries) {
			t.Errorf("drain-reported retry attempts = %v, want exactly %d", entry["attempts"], continueDrainMaxRetries)
		}
		if entry["queue_depth"] != float64(2) {
			t.Errorf("drain-reported abandoned queue depth = %v, want exactly 2", entry["queue_depth"])
		}
	}
	if matchedAbandonment != 1 {
		t.Errorf("matching drain abandonment log entries = %d, want exactly 1", matchedAbandonment)
	}

	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("pending steering count after abandoning two items = %d, want 0", got)
	}
	entries, readErr := al.GetSessionStore().ReadTranscript(child.SessionID)
	if readErr != nil {
		t.Fatalf("ReadTranscript(child): %v", readErr)
	}
	reports := 0
	for _, entry := range entries {
		if entry.Status == "error" {
			reports++
		}
	}
	if reports != 2 {
		t.Errorf("abandonment error reports = %d, want exactly 2 (one per queued item)", reports)
	}

	canceller := NewSteerCanceller(lifecycle, al.SteerGenerationCancel)
	if _, stopErr := canceller.CancelSubtree(context.Background(), child.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "issue-1020-test"}); stopErr != nil {
		t.Fatalf("normal Stop for revival: %v", stopErr)
	}
	releaseInitial()
	finished := make(chan struct{})
	go func() { al.steerAdmission().turns.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("original admitted producer did not finish after Stop/release")
	}
	stopped, loadErr := lifecycle.Load(child.SessionID)
	if loadErr != nil || stopped.State != session.LifecycleStopped || stopped.Generation != original.Generation ||
		stopped.StopNote == nil || al.steerAdmission().hasExecutionReservation(original) {
		t.Fatalf("normal stopped original before revival = %+v, error=%v", stopped, loadErr)
	}
	revivedGeneration, reviveErr := canceller.Revive(
		context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "issue-1020-test"})
	if reviveErr != nil {
		t.Fatalf("Revive: %v", reviveErr)
	}
	if revivedGeneration != original.Generation {
		t.Fatalf("landed stopped child revived generation = %d, want same original generation %d", revivedGeneration, original.Generation)
	}
	// D2/Q2: Revive clears the OLD producer. That producer is inactive even
	// if the revived queue is empty; nil success was an unstamped-fixture
	// oracle, not permission to reuse a stopped admission.
	attempt, continueErr := al.continueSteeredTurn(context.Background(), child.SessionID, revivedGeneration, original)
	if !errors.Is(continueErr, errSteeredDrainInactive) {
		t.Fatalf("continueSteeredTurn(stopped original after revival) = %v, want %v", continueErr, errSteeredDrainInactive)
	}
	if attempt.turnRan {
		t.Error("a revived generation executed a stale item left by the failed prior-generation drain")
	}
	after, loadErr := lifecycle.Load(child.SessionID)
	if loadErr != nil || after.State != session.LifecycleQueued || after.Generation != revivedGeneration ||
		after.ExecutionID != nil || after.FinalDelivery != nil || after.StopNote != nil || after.Stop != nil {
		t.Fatalf("stale original drain changed revived state: %+v, error=%v", after, loadErr)
	}
	if got := len(provider.Requests()); got != 0 {
		t.Errorf("provider requests after revival = %d, want 0 because no stale item may survive abandonment", got)
	}
	initialProvider.mu.Lock()
	initialCalls := initialProvider.calls
	initialProvider.mu.Unlock()
	if initialCalls != 1 {
		t.Errorf("distinct initial-admission provider requests = %d, want exactly 1 setup call", initialCalls)
	}
}

func TestSteeredTurnDrain1020_NonDefaultChildResetsOnlyItsOwnMessageState(t *testing.T) {
	provider := &steerTurnDrainProvider1020{}
	al := newSteerTurnDrainMultiAgentAL1020(t, provider)
	defaultAgent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default agent is not registered")
	}
	childAgent, ok := al.GetRegistry().GetAgent("ray")
	if !ok {
		t.Fatal("SETUP: non-default child agent is not registered")
	}
	defaultReset := &resetCountingMessageTool1020{}
	childReset := &resetCountingMessageTool1020{}
	defaultAgent.Tools.RegisterReplacing(defaultReset)
	childAgent.Tools.RegisterReplacing(childReset)

	boot := session.NewBootEpochStore(al.GetConfig().Agents.Defaults.Home)
	epoch, mintErr := boot.Mint()
	if mintErr != nil || epoch == 0 {
		t.Fatalf("Mint genuine non-default fixture boot epoch = %d, error=%v", epoch, mintErr)
	}
	al.SetBootEpochStore(boot)
	initialProvider := &lateSteeringProvider{
		firstCallStarted: make(chan struct{}), releaseFirstCall: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseInitial := func() { releaseOnce.Do(func() { close(initialProvider.releaseFirstCall) }) }
	t.Cleanup(releaseInitial)
	childAgent.Provider = &steerDrainAdmissionProviderQ2Fixture{initial: initialProvider, continuation: provider}
	child := launchQueuedSteeredTurnDrainChild1020(t, al, "ray", "exercise non-default child continuation")
	var original executionClaim
	var attempt steeredContinuationAttempt
	var defaultBaseline, childBaseline int
	observer := &steerTurnDrainObserver1020{hook: func(sessionID string) error {
		if sessionID != child.SessionID {
			return fmt.Errorf("non-default boundary session = %q, want %q", sessionID, child.SessionID)
		}
		record, loadErr := al.GetSessionLifecycleStore().Load(sessionID)
		if loadErr != nil {
			return loadErr
		}
		if !original.matches(record) || !al.steerAdmission().hasExecutionReservation(original) ||
			al.getActiveTurnState(sessionID) != nil {
			return fmt.Errorf("non-default continuation lost its actual post-turn original admission: claim=%+v record=%+v", original, record)
		}
		// The real initial turn has ended. Count exactly what THIS continuation
		// resets, without weakening the oracle to an absolute >=1 assertion.
		defaultBaseline, childBaseline = defaultReset.ResetCount(), childReset.ResetCount()
		if _, enqueueErr := al.EnqueueSteeringMessage(sessionID, "ray",
			providers.Message{Role: "user", Content: "continue the ray child"}, "issue-1020-ray-continuation"); enqueueErr != nil {
			return enqueueErr
		}
		var continueErr error
		attempt, continueErr = al.continueSteeredTurn(context.Background(), sessionID, child.Generation, original)
		return continueErr
	}}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), observer, NewSteerUpwardDeliverer())
	dispatched, dispatchErr := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if dispatchErr != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("real initial non-default Dispatch = %+v, error=%v, want running", dispatched, dispatchErr)
	}
	select {
	case <-initialProvider.firstCallStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("real initial non-default producer did not reach its model boundary")
	}
	ts := al.getActiveTurnState(child.SessionID)
	if ts == nil {
		t.Fatal("real initial non-default producer has no registered live handle")
	}
	original = al.tsExecutionClaim(ts, child.SessionID)
	record, loadErr := al.GetSessionLifecycleStore().Load(child.SessionID)
	if loadErr != nil || original.SessionID != child.SessionID || original.Generation != child.Generation ||
		original.RunID == "" || original.BootSeq != epoch || !original.matches(record) || !al.steerAdmission().hasExecutionReservation(original) {
		t.Fatalf("real original non-default claim/record/reservation mismatch: claim=%+v record=%+v error=%v", original, record, loadErr)
	}
	releaseInitial()
	finished := make(chan struct{})
	go func() { al.steerAdmission().turns.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("real non-default admission/direct continuation did not finish")
	}
	boundaryCalls, hookRuns, hookErr := observer.result()
	if boundaryCalls == 0 || hookRuns != 1 || hookErr != nil {
		t.Fatalf("normal non-default post-turn boundary = calls:%d runs:%d error:%v, want one successful continuation", boundaryCalls, hookRuns, hookErr)
	}
	if !attempt.turnRan {
		t.Fatal("non-default child continuation did not run")
	}
	if got := defaultReset.ResetCount() - defaultBaseline; got != 0 {
		t.Errorf("default agent send_message reset delta = %d, want 0", got)
	}
	if got := childReset.ResetCount() - childBaseline; got != 1 {
		t.Errorf("non-default child send_message reset delta = %d, want exactly 1", got)
	}
	if got := len(provider.Requests()); got != 1 {
		t.Errorf("non-default continuation provider requests = %d, want exactly 1", got)
	}
	initialProvider.mu.Lock()
	initialCalls := initialProvider.calls
	initialProvider.mu.Unlock()
	if initialCalls != 1 {
		t.Errorf("distinct initial non-default provider requests = %d, want exactly 1 setup call", initialCalls)
	}
}

func TestSteeredTurnDrain1020_WakeReentryDrainsLateSteeringBeforeDisposal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default agent is not registered")
	}
	agent.Provider = provider
	deliverer := newSteerTurnDrainDeliverer1020()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())),
		nil,
		deliverer,
	)
	child := launchQueuedSteeredTurnDrainChild1020(t, al, testDefaultAgentID, "exercise wake re-entry drain")
	messageID := appendWakeInboxEntry(t, al, child.SessionID, "wake the queued child")
	var injectionOnce sync.Once
	var injectionErr error
	injectionRuns := 0
	al.eventBus.SetSyncTap(func(evt Event) {
		if evt.Kind != EventKindTurnEnd {
			return
		}
		injectionOnce.Do(func() {
			injectionRuns++
			_, injectionErr = al.EnqueueSteeringMessage(
				child.SessionID,
				testDefaultAgentID,
				providers.Message{Role: "user", Content: "ISSUE-1020-LATE-WAKE-REENTRY-STEER"},
				"issue-1020-late-wake-reentry-steer",
			)
		})
	})
	t.Cleanup(func() { al.eventBus.SetSyncTap(nil) })
	if _, err := al.processSteeredSystemWake(
		context.Background(), wakeMessage(child.SessionID, messageID, child.Generation)); err != nil {
		t.Fatalf("processSteeredSystemWake: %v", err)
	}
	if injectionErr != nil {
		t.Fatalf("late wake/re-entry steering injection: %v", injectionErr)
	}
	if injectionRuns != 1 {
		t.Fatalf("late wake/re-entry steering injection count = %d, want exactly 1", injectionRuns)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Errorf("wake/re-entry provider request count = %d, want 2: wake turn plus late continuation", len(requests))
	}
	if got := al.pendingSteeringCountForScope(child.SessionID); got != 0 {
		t.Errorf("pending steering count after wake/re-entry disposal = %d, want 0", got)
	}
	if got := finalHandbackResult1020(t, deliverer); got != "issue 1020 response 2" {
		t.Errorf("wake/re-entry handback result = %q, want continuation result %q", got, "issue 1020 response 2")
	}
}

func TestSteeredTurnDrain1020_ConsumedMarkerFailureCannotCompleteCleanly(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	deliverer := newSteerTurnDrainDeliverer1020()

	childID := launchSteeredTurnDrainChild1020(t, al, provider, deliverer, func(sessionID string) error {
		return al.EnqueueSteeringWake(
			sessionID,
			testDefaultAgentID,
			"session_01INVALIDCONSUMEDMARKERTARGET",
			"issue-1020-consumed-marker-failure",
			providers.Message{Role: "user", Content: "must not become a clean completion"},
		)
	})
	al.drainSteeredTurns(5 * time.Second)

	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.State != session.LifecycleFailed {
		t.Errorf("child lifecycle after consumed-marker persistence failure = %q, want %q", rec.State, session.LifecycleFailed)
	}
	events := deliverer.Events()
	if len(events) != 1 {
		t.Fatalf("upward completion event count = %d, want exactly 1", len(events))
	}
	if events[0].Outcome != steer.OutcomeFailed {
		t.Errorf("upward outcome after consumed-marker persistence failure = %q, want %q", events[0].Outcome, steer.OutcomeFailed)
	}
	if got := al.pendingSteeringCountForScope(childID); got != 0 {
		t.Errorf("pending steering count after consumed-marker persistence failure = %d, want 0 after fail-loud abandonment", got)
	}
}

func TestSteeredTurnDrain1020_StopAfterFinalRecordCheckPreventsContinuation(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &steerTurnDrainProvider1020{}
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	agent.Provider = provider
	deliverer := newSteerTurnDrainDeliverer1020()
	observer := &steerTurnDrainObserver1020{hook: func(sessionID string) error {
		_, err := al.EnqueueSteeringMessage(
			sessionID,
			testDefaultAgentID,
			providers.Message{Role: "user", Content: "ISSUE-1020-STOP-RACE-CONTINUATION"},
			"issue-1020-stop-race",
		)
		return err
	}}
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())),
		observer,
		deliverer,
	)

	hookReached := make(chan struct{})
	releaseHook := make(chan struct{})
	continueSteeredTurnBeforeRunTestHook = func(string, int) {
		close(hookReached)
		<-releaseHook
	}
	t.Cleanup(func() { continueSteeredTurnBeforeRunTestHook = nil })

	child := launchQueuedSteeredTurnDrainChild1020(
		t, al, testDefaultAgentID, "exercise Stop after the continuation's final lifecycle check")
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dispatched.State != steer.DispatchRunning {
		t.Fatalf("Dispatch state = %q, want %q", dispatched.State, steer.DispatchRunning)
	}

	select {
	case <-hookReached:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the continuation's pre-run hook")
	}
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
	report, cancelErr := canceller.CancelSubtree(context.Background(), child.SessionID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "issue-1020-test",
	})
	if cancelErr != nil {
		t.Fatalf("CancelSubtree: %v", cancelErr)
	}
	if len(report.Reached) != 1 || report.Reached[0] != child.SessionID {
		t.Fatalf("CancelSubtree reached = %v, want only %q", report.Reached, child.SessionID)
	}
	close(releaseHook)

	select {
	case <-deliverer.delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for interrupted completion")
	}
	al.drainSteeredTurns(5 * time.Second)
	if got := len(provider.Requests()); got != 1 {
		t.Errorf("provider request count after Stop in the continuation registration gap = %d, want 1 (the initial turn only)", got)
	}
	events := deliverer.Events()
	if len(events) != 1 {
		t.Fatalf("upward completion event count after Stop = %d, want exactly 1", len(events))
	}
	if events[0].Outcome != steer.OutcomeInterrupted {
		t.Errorf("upward completion outcome after Stop = %q, want %q", events[0].Outcome, steer.OutcomeInterrupted)
	}
}
