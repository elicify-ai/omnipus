// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Founder Q2=A (2026-10-03T16:41:48Z): automatic processing of an already
// accepted steer is the SAME admitted execution. The original full claim,
// captured before acceptance, must survive reconstruction, injection and the
// outcome/outbox commit. An ordinary working-session wake AFTER release is a
// new admission, not another item accepted into that earlier drain.
//
// Oracles: frozen ADR-20260928-sub-agent-control-plane@cd20cf8b, D2/D4/D5/D8
// and T27, as clarified by Q2=A. These tests use stable launcher/wake APIs on
// both the pre-drain a62f6f032 and Q2 source 046527e24. No changed direct
// continueSteeredTurn signature is used here; this file can travel separately
// from the two legacy fixture migrations for a real remote RED witness.
//
// Real: launcher, minted boot epoch, admission, live handles, drain, transcript,
// lifecycle/outbox and upward inbox delivery. Only the external model provider
// is a test double; the existing BoundaryFinalReply observer submits a real
// wake-bearing instruction and observes actual acceptance/ownership.
//
// DEPENDENCY BLOCKED, not covered by a fabricated substitute: accepted RESUME
// or redirect must use its accepted control_id as run_id; the delayed Stop
// must carry the selected execution plus its accepted control_id; older queued
// controls must be superseded by Stop and never dispatched at boot. This base
// has generation-only SteerCanceller.StopTurns/cancelStamped and Revive plus
// ordinary Dispatch, but no typed accepted-control/stop_effect producer or
// control-ledger boot reconciliation. The independent queued-old-A/B pack
// covers Revive plus ordinary replacement admission, not those missing APIs.
// These D2/D5/D8/T27 cases remain W2a/W3 dependencies; no skip or unconditional
// fatal is used to pretend the independent drain property is blocked.
//
// Q3 SOURCE-ONLY: authoring this source is NOT behavioral RED or compile GREEN.
// Named remote CI must establish RED against the pre-drain witness. GREEN,
// mutation probes and the proof-of-failability checklist are deferred to a
// fresh CHECK instance; no local gate is authorized for this unit.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	q2DrainTask       = "Q2 original admitted producer task"
	q2DrainLateText   = "Q2 already-accepted late instruction"
	q2DrainLateID     = "q2-accepted-before-original-release"
	q2DrainFirstFinal = "Q2 original round answer"
	q2DrainLastFinal  = "Q2 accepted instruction final answer"
	q2DrainWakeText   = "Q2 genuinely later ordinary working-session wake"
	q2DrainWakeFinal  = "Q2 later admission answer"
)

type q2DrainObservation struct {
	handle      *turnState
	claim       executionClaim
	record      *session.LifecycleRecord
	reservation executionClaim
	reserved    bool
	messages    []providers.Message
	err         error
}

// q2DrainOwnership observes the real selected state under the admission
// synchronization. It never stamps a claim or writes the registry/store.
func q2DrainOwnership(al *AgentLoop, childID string) q2DrainObservation {
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	defer gate.entryMu.Unlock()
	observation := q2DrainObservation{handle: al.getActiveTurnState(childID)}
	if observation.handle != nil {
		observation.handle.mu.RLock()
		observation.claim = executionClaim{
			SessionID: observation.handle.sessionKey, Generation: observation.handle.generation,
			RunID: observation.handle.executionRunID, BootSeq: observation.handle.executionBootSeq,
		}
		observation.handle.mu.RUnlock()
	}
	observation.record, observation.err = al.GetSessionLifecycleStore().Load(childID)
	gate.mu.Lock()
	entry, reserved := gate.active[childID]
	observation.reservation, observation.reserved = entry.executionClaim(), reserved
	gate.mu.Unlock()
	return observation
}

type q2DrainProvider struct {
	al       *AgentLoop
	childID  string
	mu       sync.Mutex
	requests []q2DrainObservation
	entered  chan q2DrainObservation
	release  []chan struct{}
}

func newQ2DrainProvider(al *AgentLoop, childID string, answers int) *q2DrainProvider {
	p := &q2DrainProvider{al: al, childID: childID, entered: make(chan q2DrainObservation, answers)}
	for range answers {
		p.release = append(p.release, make(chan struct{}))
	}
	return p
}

func (p *q2DrainProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	observation := q2DrainOwnership(p.al, p.childID)
	observation.messages = append([]providers.Message(nil), messages...)
	p.mu.Lock()
	call := len(p.requests)
	p.requests = append(p.requests, observation)
	p.mu.Unlock()
	if call >= len(p.release) {
		return nil, fmt.Errorf("Q2 unexpected actual provider request %d (allowed %d)", call+1, len(p.release))
	}
	p.entered <- observation
	if observation.err != nil {
		return nil, observation.err
	}
	select {
	case <-p.release[call]:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// These are external model replies, not identity/acceptance oracles.
	answers := []string{q2DrainFirstFinal, q2DrainLastFinal, q2DrainWakeFinal}
	return &providers.LLMResponse{Content: answers[call], FinishReason: "stop"}, nil
}

func (p *q2DrainProvider) GetDefaultModel() string { return "q2-normal-drain-model-boundary" }

func (p *q2DrainProvider) releaseRequest(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.release[index]:
	default:
		close(p.release[index])
	}
}

func (p *q2DrainProvider) releaseAll() {
	for index := range p.release {
		p.releaseRequest(index)
	}
}

func (p *q2DrainProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

type q2DrainAcceptance struct {
	before q2DrainObservation
	after  q2DrainObservation
	items  []steeringQueueItem
	err    error
}

type q2DrainObserver struct {
	al       *AgentLoop
	childID  string
	once     sync.Once
	accepted chan q2DrainAcceptance
}

func (o *q2DrainObserver) Observe(boundary steer.Boundary, sessionID string, _ steer.Audience) {
	if boundary != steer.BoundaryFinalReply || sessionID != o.childID {
		return
	}
	o.once.Do(func() {
		acceptance := q2DrainAcceptance{before: q2DrainOwnership(o.al, o.childID)}
		acceptance.err = o.al.EnqueueSteeringWake(o.childID, testDefaultAgentID, o.childID,
			q2DrainLateID, providers.Message{Role: "user", Content: q2DrainLateText})
		acceptance.after = q2DrainOwnership(o.al, o.childID)
		o.al.steering.mu.Lock()
		acceptance.items = append([]steeringQueueItem(nil), o.al.steering.queues[o.childID]...)
		o.al.steering.mu.Unlock()
		o.accepted <- acceptance
	})
}

func q2DrainHarness(t *testing.T, acceptLate bool, answers int) (*AgentLoop, *q2DrainProvider, *q2DrainObserver, steer.LaunchResult, string, uint64) {
	t.Helper()
	al, _ := newSteerAL(t)
	boot := session.NewBootEpochStore(al.GetConfig().Agents.Defaults.Home)
	epoch, err := boot.Mint()
	if err != nil {
		t.Fatalf("SETUP Mint genuine boot epoch: %v", err)
	}
	if epoch == 0 || boot.Current() != epoch {
		t.Fatalf("SETUP minted/current boot epoch = %d/%d, require one genuine nonzero epoch", epoch, boot.Current())
	}
	al.SetBootEpochStore(boot)
	parentID := newTestSteeringSession(t, al, "ws-q2-normal-drain")
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID, Task: q2DrainTask,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2-original-launch"},
	})
	if err != nil {
		t.Fatalf("SETUP real Launch: %v", err)
	}
	provider := newQ2DrainProvider(al, child.SessionID, answers)
	agent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP default agent is not registered")
	}
	agent.Provider = provider
	// Registered after newSteerAL's Close: release parked provider requests
	// before joining the real detached dispatch and removing temporary stores.
	t.Cleanup(provider.releaseAll)
	observer := &q2DrainObserver{al: al, childID: child.SessionID, accepted: make(chan q2DrainAcceptance, 1)}
	var boundaryObserver steer.BoundaryObserver = steer.NopBoundaryObserver{}
	if acceptLate {
		boundaryObserver = observer
	}
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), boundaryObserver, NewSteerUpwardDeliverer())
	return al, provider, observer, child, parentID, epoch
}

func q2DrainDispatch(t *testing.T, al *AgentLoop, child steer.LaunchResult) {
	t.Helper()
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil {
		t.Fatalf("real original Dispatch: %v", err)
	}
	if result.State != steer.DispatchRunning || result.Generation != child.Generation {
		t.Fatalf("original Dispatch = %+v, want running generation %d", result, child.Generation)
	}
}

func q2DrainAwaitRequest(t *testing.T, provider *q2DrainProvider, number int) q2DrainObservation {
	t.Helper()
	select {
	case observation := <-provider.entered:
		if observation.err != nil {
			t.Fatalf("actual provider request %d ownership read: %v", number, observation.err)
		}
		return observation
	case <-time.After(10 * time.Second):
		t.Fatalf("actual provider request %d did not execute; request count=%d", number, provider.requestCount())
		return q2DrainObservation{}
	}
}

func q2DrainRequireOwner(t *testing.T, observation q2DrainObservation, expected executionClaim, what string, requireHandle bool) {
	t.Helper()
	if observation.err != nil {
		t.Fatalf("%s: lifecycle read: %v", what, observation.err)
	}
	if requireHandle && (observation.handle == nil || observation.claim != expected) {
		t.Fatalf("%s: actual live immutable claim = %+v (handle=%p), want original %+v", what, observation.claim, observation.handle, expected)
	}
	if observation.record == nil || observation.record.ExecutionID == nil {
		t.Fatalf("%s: no durable execution identity in actual lifecycle record", what)
	}
	actualRecord := executionClaim{
		SessionID: observation.record.SessionID, Generation: observation.record.Generation,
		RunID: observation.record.ExecutionID.RunID, BootSeq: observation.record.ExecutionID.BootSeq,
	}
	if actualRecord != expected || observation.record.State != session.LifecycleRunning {
		t.Fatalf("%s: actual lifecycle owner/state = %+v/%q, want %+v/%q", what, actualRecord, observation.record.State, expected, session.LifecycleRunning)
	}
	if !observation.reserved || observation.reservation != expected {
		t.Fatalf("%s: actual admission reservation = %+v (owned=%v), want exactly %+v", what, observation.reservation, observation.reserved, expected)
	}
}

func q2DrainOriginal(t *testing.T, first q2DrainObservation, child steer.LaunchResult, epoch uint64) executionClaim {
	t.Helper()
	// Select from the FIRST actual producer, before its provider can return
	// and before the observer can accept any late instruction. Never derive
	// expected ownership from the continuation/replacement record.
	original := first.claim
	if original.SessionID != child.SessionID || original.Generation != child.Generation || original.BootSeq != epoch || original.RunID == "" {
		t.Fatalf("first admitted producer claim = %+v, want session=%q generation=%d genuine boot=%d and nonempty runtime run_id", original, child.SessionID, child.Generation, epoch)
	}
	q2DrainRequireOwner(t, first, original, "first actual provider request before late acceptance", true)
	return original
}

func q2DrainRequireAcceptance(t *testing.T, observer *q2DrainObserver, original executionClaim) {
	t.Helper()
	var acceptance q2DrainAcceptance
	select {
	case acceptance = <-observer.accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("late instruction never reached actual production enqueue at post-run/pre-drain boundary")
	}
	if acceptance.err != nil {
		t.Fatalf("real EnqueueSteeringWake refused late instruction: %v", acceptance.err)
	}
	q2DrainRequireOwner(t, acceptance.before, original, "immediately before real late enqueue", false)
	q2DrainRequireOwner(t, acceptance.after, original, "immediately after real late enqueue accepted, before release", false)
	if len(acceptance.items) != 1 {
		t.Fatalf("accepted exact wake-bearing steering items = %d, want 1 before drain", len(acceptance.items))
	}
	item := acceptance.items[0]
	if item.message.Role != "user" || item.message.Content != q2DrainLateText || item.wake == nil {
		t.Fatalf("actually accepted late queue item = %+v, want the exact submitted user instruction with wake identity", item)
	}
	if item.wake.messageID != q2DrainLateID || item.wake.transcriptSessionID != original.SessionID || item.wake.agentID != testDefaultAgentID {
		t.Fatalf("actually accepted queue wake identity = %+v, want id=%q transcript=%q agent=%q", item.wake, q2DrainLateID, original.SessionID, testDefaultAgentID)
	}
}

func q2DrainRequireInjection(t *testing.T, al *AgentLoop, childID string, second q2DrainObservation) {
	t.Helper()
	matches := 0
	for _, message := range second.messages {
		if message.Role == "user" && message.Content == q2DrainLateText {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("exact accepted instruction in SECOND actual provider request = %d copies, want 1", matches)
	}
	entries, err := al.GetSessionStore().ReadTranscript(childID)
	if err != nil {
		t.Fatalf("ReadTranscript(actual child): %v", err)
	}
	injected, consumed := 0, 0
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == q2DrainLateText {
			injected++
		}
		if entry.ID == "consumed-"+q2DrainLateID && entry.Content == "consumed "+q2DrainLateID {
			consumed++
		}
	}
	if injected != 1 || consumed != 1 {
		t.Fatalf("persisted accepted instruction/consumed marker = %d/%d, want exactly 1/1", injected, consumed)
	}
	if pending := al.pendingSteeringCountForScope(childID); pending != 0 {
		t.Fatalf("pending already-accepted instruction count = %d, want 0 after real injection", pending)
	}
}

func q2DrainRequireReleased(t *testing.T, al *AgentLoop, original executionClaim) {
	t.Helper()
	// Join the actual dispatched job, not a transcript field that happens to
	// appear before its completion/slot-release defers finish. Unlike the
	// shutdown helper's log-only timeout, an unfinished job fails this test.
	gate := al.steerAdmission()
	finished := make(chan struct{})
	go func() {
		gate.turns.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("actual original dispatch/drain job did not finish")
	}
	if current := al.getActiveTurnState(original.SessionID); current != nil {
		t.Fatalf("selected original dispatch did not end: live handle=%p", current)
	}
	gate.mu.Lock()
	active := len(gate.active)
	queued := len(gate.queue)
	_, sessionReserved := gate.active[original.SessionID]
	gate.mu.Unlock()
	if active != 0 || queued != 0 || sessionReserved || gate.hasExecutionReservation(original) {
		t.Fatalf("original full admission was not released: active=%d queued=%d sessionReserved=%v", active, queued, sessionReserved)
	}
}

func q2DrainRequireFinal(t *testing.T, al *AgentLoop, original executionClaim, parentID, answer string) {
	t.Helper()
	record, err := al.GetSessionLifecycleStore().Load(original.SessionID)
	if err != nil {
		t.Fatalf("Load(actual completion): %v", err)
	}
	if record.State != session.LifecycleCompleted || record.Generation != original.Generation || record.ExecutionID == nil ||
		record.ExecutionID.RunID != original.RunID || record.ExecutionID.BootSeq != original.BootSeq {
		t.Fatalf("completed lifecycle owner = state:%q generation:%d execution:%+v, want original full producer %+v", record.State, record.Generation, record.ExecutionID, original)
	}
	finalID := fmt.Sprintf("%s:%d:final", original.SessionID, original.Generation)
	commit := record.FinalDelivery
	if commit == nil || commit.CommitID != original.RunID || commit.Generation != original.Generation ||
		commit.MessageID != finalID || commit.ParentSessionID != parentID || commit.Outcome != string(steer.OutcomeFinalAnswer) {
		t.Fatalf("actual protected outbox = %+v, want original run_id=%q generation=%d final=%q parent=%q outcome=%q", commit, original.RunID, original.Generation, finalID, parentID, steer.OutcomeFinalAnswer)
	}
	entries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(actual direct parent inbox): %v", err)
	}
	finals := 0
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil || messageIDOf(*entry.Message) != finalID {
			continue
		}
		finals++
		handback, decodeErr := entry.Message.AsSessionMessageHandback()
		if decodeErr != nil {
			t.Fatalf("actual committed direct-parent final is not a handback: %v", decodeErr)
		}
		if handback.SessionId != original.SessionID || handback.ResultSoFar != answer {
			t.Fatalf("actual direct-parent final = session:%q result:%q, want original session:%q exact last result:%q", handback.SessionId, handback.ResultSoFar, original.SessionID, answer)
		}
	}
	if finals != 1 {
		t.Fatalf("legitimate finals in actual direct-parent inbox = %d, want exactly 1", finals)
	}
}

func TestSteerDrainExecutionIdentityQ2_AcceptedLateSteeringRetainsOriginalFullAdmission(t *testing.T) {
	al, provider, observer, child, parentID, epoch := q2DrainHarness(t, true, 2)
	q2DrainDispatch(t, al, child)
	first := q2DrainAwaitRequest(t, provider, 1)
	original := q2DrainOriginal(t, first, child, epoch)
	provider.releaseRequest(0)
	q2DrainRequireAcceptance(t, observer, original)
	second := q2DrainAwaitRequest(t, provider, 2)
	q2DrainRequireOwner(t, second, original, "automatically drained second actual provider request", true)
	if second.handle == first.handle {
		t.Fatal("automatic drain reused the retired internal turn handle; require reconstructed handle carrying the SAME full claim")
	}
	q2DrainRequireInjection(t, al, child.SessionID, second)
	provider.releaseRequest(1)
	q2DrainRequireReleased(t, al, original)
	if calls := provider.requestCount(); calls != 2 {
		t.Fatalf("actual provider requests = %d, want exactly 2 (original plus accepted automatic drain)", calls)
	}
	q2DrainRequireFinal(t, al, original, parentID, q2DrainLastFinal)

	// The normal consumed-wake replay path must not run the model again,
	// overwrite the committed producer, or inject the same text twice.
	if _, err := al.processSteeredSystemWake(context.Background(), wakeMessage(child.SessionID, q2DrainLateID, child.Generation)); err != nil {
		t.Fatalf("normal consumed-wake replay: %v", err)
	}
	if calls := provider.requestCount(); calls != 2 {
		t.Fatalf("actual model requests after consumed accepted-item replay = %d, want unchanged 2", calls)
	}
	q2DrainRequireInjection(t, al, child.SessionID, second)
	q2DrainRequireFinal(t, al, original, parentID, q2DrainLastFinal)
}

func TestSteerDrainExecutionIdentityQ2_NoAcceptedLateSteeringRunsOnlyOriginalRequest(t *testing.T) {
	al, provider, _, child, parentID, epoch := q2DrainHarness(t, false, 1)
	q2DrainDispatch(t, al, child)
	first := q2DrainAwaitRequest(t, provider, 1)
	original := q2DrainOriginal(t, first, child, epoch)
	provider.releaseRequest(0)
	q2DrainRequireReleased(t, al, original)
	if calls := provider.requestCount(); calls != 1 {
		t.Fatalf("actual provider requests without accepted late steering = %d, want exactly 1", calls)
	}
	if pending := al.pendingSteeringCountForScope(child.SessionID); pending != 0 {
		t.Fatalf("pending queue without a late acceptance = %d, want 0", pending)
	}
	q2DrainRequireFinal(t, al, original, parentID, q2DrainFirstFinal)
}

func TestSteerDrainExecutionIdentityQ2_OrdinaryWakeAfterReleaseIsFreshAdmission(t *testing.T) {
	al, provider, observer, child, _, epoch := q2DrainHarness(t, true, 3)
	// A normally launched pending descendant keeps this helper working after
	// its turn exits. Do not forge a running record or re-open a terminal one.
	_, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: child.SessionID, TargetAgentID: testDefaultAgentID,
		Task:   "Q2 pending descendant keeps the original helper working",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2-pending-descendant"},
	})
	if err != nil {
		t.Fatalf("SETUP normally launch pending descendant: %v", err)
	}
	q2DrainDispatch(t, al, child)
	first := q2DrainAwaitRequest(t, provider, 1)
	original := q2DrainOriginal(t, first, child, epoch)
	provider.releaseRequest(0)
	q2DrainRequireAcceptance(t, observer, original)
	second := q2DrainAwaitRequest(t, provider, 2)
	q2DrainRequireOwner(t, second, original, "accepted bounded drain before separate later admission", true)
	q2DrainRequireInjection(t, al, child.SessionID, second)
	provider.releaseRequest(1)
	q2DrainRequireReleased(t, al, original)
	beforeWake, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(working helper after bounded drain/release): %v", err)
	}
	if beforeWake.State != session.LifecycleRunning || beforeWake.FinalDelivery != nil || beforeWake.ExecutionID == nil ||
		beforeWake.ExecutionID.RunID != original.RunID || beforeWake.ExecutionID.BootSeq != epoch {
		t.Fatalf("separate later-wake premise: real helper did not remain working under original owner after release: %+v", beforeWake)
	}
	if calls := provider.requestCount(); calls != 2 {
		t.Fatalf("requests before genuine later admission = %d, want exactly 2", calls)
	}
	// This inbox append and ordinary wake happen ONLY AFTER the prior job's
	// complete release. It is not EnqueueSteeringWake accepted by that job.
	messageID := appendWakeInboxEntry(t, al, child.SessionID, q2DrainWakeText)
	wake := wakeMessage(child.SessionID, messageID, child.Generation)
	wake.Content = q2DrainWakeText
	type wakeResult struct {
		answer string
		err    error
	}
	done := make(chan wakeResult, 1)
	settled := make(chan struct{})
	go func() {
		defer close(settled)
		answer, wakeErr := al.processSteeredSystemWake(context.Background(), wake)
		done <- wakeResult{answer: answer, err: wakeErr}
	}()
	t.Cleanup(func() {
		provider.releaseAll()
		select {
		case <-settled:
		case <-time.After(10 * time.Second):
			t.Error("separate ordinary wake remained in flight during teardown")
		}
	})
	third := q2DrainAwaitRequest(t, provider, 3)
	fresh := third.claim
	if fresh.SessionID != original.SessionID || fresh.Generation != original.Generation || fresh.BootSeq != epoch ||
		fresh.RunID == "" || fresh.RunID == original.RunID || third.handle == first.handle || third.handle == second.handle {
		t.Fatalf("separate later ordinary wake reused old admission/handle: original=%+v new=%+v old handles=%p/%p new=%p", original, fresh, first.handle, second.handle, third.handle)
	}
	q2DrainRequireOwner(t, third, fresh, "genuine later ordinary working-session wake", true)
	provider.releaseRequest(2)
	select {
	case result := <-done:
		if result.err != nil || result.answer != q2DrainWakeFinal {
			t.Fatalf("genuine later ordinary wake result = %q/%v, want %q/nil", result.answer, result.err, q2DrainWakeFinal)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("genuine later ordinary wake did not finish its separate admission")
	}
	q2DrainRequireReleased(t, al, fresh)
	if calls := provider.requestCount(); calls != 3 {
		t.Fatalf("actual requests after original+drain+separate wake = %d, want exactly 3", calls)
	}
	entries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(later ordinary wake): %v", err)
	}
	consumed := 0
	for _, entry := range entries {
		if entry.ID == "consumed-"+messageID && entry.Content == "consumed "+messageID {
			consumed++
		}
	}
	if consumed != 1 {
		t.Fatalf("genuine later admission consumed marker count = %d, want exactly 1", consumed)
	}
}

func TestSteerDrainExecutionIdentityQ2_MalformedProducerClaimCannotRewriteRealAdmission(t *testing.T) {
	cases := []struct {
		name   string
		change func(executionClaim) executionClaim
		want   error
	}{
		{name: "missing_minted_boot_epoch", change: func(claim executionClaim) executionClaim { claim.BootSeq = 0; return claim }, want: errCompleteNoExecutionIdentity},
		{name: "different_run_id_on_same_admission", change: func(claim executionClaim) executionClaim { claim.RunID += "-invalid-producer"; return claim }, want: steer.ErrStaleGeneration},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			al, provider, observer, child, parentID, epoch := q2DrainHarness(t, true, 2)
			q2DrainDispatch(t, al, child)
			first := q2DrainAwaitRequest(t, provider, 1)
			original := q2DrainOriginal(t, first, child, epoch)
			provider.releaseRequest(0)
			q2DrainRequireAcceptance(t, observer, original)
			second := q2DrainAwaitRequest(t, provider, 2)
			q2DrainRequireOwner(t, second, original, "actual same-admission drain before malformed effect", true)
			malformed := test.change(original)
			// Supply malformed caller input to a real existing effect boundary.
			// Never stamp fake identities into a record/registry/queue entry.
			_, err := commitSteeredExecutionState(al.GetSessionLifecycleStore(), malformed, session.LifecycleQueued, "Q2 invalid producer must not inject this text")
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("malformed producer claim %+v refusal = %v, want exact %v", malformed, err, test.want)
			}
			after := q2DrainOwnership(al, child.SessionID)
			q2DrainRequireOwner(t, after, original, "after malformed same-admission effect refused", true)
			if after.handle != second.handle || after.record.FinalDelivery != nil || len(after.record.PendingUserMessages) != len(second.record.PendingUserMessages) {
				t.Fatalf("malformed producer changed actual drained handle/outbox/pending instructions: handle=%p want=%p record=%+v", after.handle, second.handle, after.record)
			}
			for index, message := range after.record.PendingUserMessages {
				if message != second.record.PendingUserMessages[index] {
					t.Fatalf("malformed producer changed pending instruction %d: %q, want %q", index, message, second.record.PendingUserMessages[index])
				}
			}
			if calls := provider.requestCount(); calls != 2 {
				t.Fatalf("malformed producer started another actual model request: %d, want unchanged 2", calls)
			}
			entries, readErr := al.GetSessionStore().ReadTranscript(child.SessionID)
			if readErr != nil {
				t.Fatalf("ReadTranscript(after refused malformed producer): %v", readErr)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Content, "Q2 invalid producer must not inject this text") {
					t.Fatal("refused malformed producer text was persisted into the actual child transcript")
				}
			}
			q2DrainRequireInjection(t, al, child.SessionID, second)
			provider.releaseRequest(1)
			q2DrainRequireReleased(t, al, original)
			q2DrainRequireFinal(t, al, original, parentID, q2DrainLastFinal)
		})
	}
}
