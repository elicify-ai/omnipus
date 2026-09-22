// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// CP-0's required consumer test (landing order §4): "one compiled consumer
// test per interface exercises it" — a fake implementation of every
// pkg/steer interface, driven from OUTSIDE pkg/agent, proving the shapes
// this package publishes are usable by a real caller (pkg/tools,
// pkg/channels, pkg/gateway) rather than only by their eventual pkg/agent
// implementation.

package steer_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeLauncher is a minimal, in-memory steer.SessionLauncher.
type fakeLauncher struct {
	launched []steer.LaunchRequest
	nextID   int
	dispatch map[string]steer.DispatchResult
}

var _ steer.SessionLauncher = (*fakeLauncher)(nil)

func (f *fakeLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	if req.Label == "" && req.Task == "" {
		return steer.LaunchResult{}, steer.ErrTitleRequired
	}
	f.launched = append(f.launched, req)
	f.nextID++
	id := "fake-session-" + string(rune('0'+f.nextID))
	return steer.LaunchResult{SessionID: id, Generation: 1}, nil
}

func (f *fakeLauncher) Dispatch(_ context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	if r, ok := f.dispatch[sessionID]; ok {
		return r, nil
	}
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}

// fakeAudienceResolver is a minimal steer.AudienceResolver.
type fakeAudienceResolver struct {
	audience map[string]steer.Audience
	class    map[string]steer.Class
}

var _ steer.AudienceResolver = (*fakeAudienceResolver)(nil)

func (f *fakeAudienceResolver) Audience(_ context.Context, sessionID string) (steer.Audience, steer.Class, error) {
	a, ok := f.audience[sessionID]
	if !ok {
		return steer.AudienceNone, steer.ClassUnreadable, errors.New("fake: unknown session")
	}
	return a, f.class[sessionID], nil
}

// fakeDeliverer is a minimal steer.UpwardDeliverer.
type fakeDeliverer struct {
	delivered []steer.UpwardEvent
}

var _ steer.UpwardDeliverer = (*fakeDeliverer)(nil)

func (f *fakeDeliverer) Deliver(_ context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	f.delivered = append(f.delivered, event)
	return steer.Delivery{MessageID: event.ChildSessionID + ":1:final", Outcome: steer.DeliveryWoke}, nil
}

// fakeCanceller is a minimal steer.Canceller.
type fakeCanceller struct {
	cancelled []string
	revived   map[string]int
}

var _ steer.Canceller = (*fakeCanceller)(nil)

func (f *fakeCanceller) CancelSubtree(_ context.Context, sessionID string, by steer.Principal) (steer.CancelReport, error) {
	f.cancelled = append(f.cancelled, sessionID)
	return steer.CancelReport{Reached: []string{sessionID}}, nil
}

func (f *fakeCanceller) Revive(_ context.Context, sessionID string, by steer.Principal) (int, error) {
	f.revived[sessionID]++
	return f.revived[sessionID] + 1, nil
}

// fakeClassifier is a minimal steer.RecordClassifier.
type fakeClassifier struct {
	class map[string]steer.Class
}

var _ steer.RecordClassifier = (*fakeClassifier)(nil)

func (f *fakeClassifier) Classify(_ context.Context, sessionID string) (steer.Class, error) {
	if c, ok := f.class[sessionID]; ok {
		return c, nil
	}
	return steer.ClassUnreadable, errors.New("fake: no record")
}

// recordingObserver is a minimal steer.BoundaryObserver that records every
// call — the shape WP-G's I-7 RecordingOutbound.AssertBoundaryInvoked will
// build on.
type recordingObserver struct {
	calls []struct {
		boundary steer.Boundary
		session  string
		audience steer.Audience
	}
}

var _ steer.BoundaryObserver = (*recordingObserver)(nil)

func (r *recordingObserver) Observe(b steer.Boundary, sessionID string, a steer.Audience) {
	r.calls = append(r.calls, struct {
		boundary steer.Boundary
		session  string
		audience steer.Audience
	}{b, sessionID, a})
}

// TestFakeSessionLauncher_LaunchThenDispatch drives steer.SessionLauncher
// end to end through a fake, from outside pkg/agent.
func TestFakeSessionLauncher_LaunchThenDispatch(t *testing.T) {
	l := &fakeLauncher{dispatch: map[string]steer.DispatchResult{}}
	ctx := context.Background()

	res, err := l.Launch(ctx, steer.LaunchRequest{
		TargetAgentID: "worker",
		Task:          "build the page",
		Origin:        steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.SessionID == "" || res.Generation != 1 {
		t.Fatalf("Launch result = %+v, want a non-empty SessionID and Generation 1", res)
	}

	dr, err := l.Dispatch(ctx, res.SessionID, res.Generation)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dr.State != steer.DispatchRunning {
		t.Fatalf("Dispatch state = %q, want %q", dr.State, steer.DispatchRunning)
	}
}

// TestFakeSessionLauncher_EmptyTitleRefused proves ErrTitleRequired is a
// comparable sentinel a caller outside pkg/agent can branch on.
func TestFakeSessionLauncher_EmptyTitleRefused(t *testing.T) {
	l := &fakeLauncher{}
	_, err := l.Launch(context.Background(), steer.LaunchRequest{TargetAgentID: "worker"})
	if !errors.Is(err, steer.ErrTitleRequired) {
		t.Fatalf("Launch with no label/task = %v, want ErrTitleRequired", err)
	}
}

// TestFakeAudienceResolver_ClassifiesSteeredAndRoot drives
// steer.AudienceResolver through a fake, proving Audience/Class travel
// together as the interface promises.
func TestFakeAudienceResolver_ClassifiesSteeredAndRoot(t *testing.T) {
	r := &fakeAudienceResolver{
		audience: map[string]steer.Audience{
			"root-1":  steer.AudienceUser,
			"child-1": steer.AudienceSteeringSession,
		},
		class: map[string]steer.Class{
			"root-1":  steer.ClassOrdinaryRoot,
			"child-1": steer.ClassSteered,
		},
	}
	a, c, err := r.Audience(context.Background(), "child-1")
	if err != nil {
		t.Fatalf("Audience: %v", err)
	}
	if a != steer.AudienceSteeringSession || c != steer.ClassSteered {
		t.Fatalf("Audience(child-1) = (%q, %q), want (steering_session, steered)", a, c)
	}
	if !c.Runnable() {
		t.Fatal("ClassSteered.Runnable() = false, want true")
	}
	if steer.ClassInvalidEdge.Runnable() {
		t.Fatal("ClassInvalidEdge.Runnable() = true, want false")
	}
}

// TestFakeUpwardDeliverer_DeliversSessionMessage drives
// steer.UpwardDeliverer through a fake, proving UpwardEvent's Message field
// really is the generated wire type (not a parallel struct).
func TestFakeUpwardDeliverer_DeliversSessionMessage(t *testing.T) {
	d := &fakeDeliverer{}
	var msg generated.SessionMessage
	delivery, err := d.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: "child-1",
		Outcome:        steer.OutcomeFinalAnswer,
		Message:        msg,
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryWoke {
		t.Fatalf("Delivery.Outcome = %q, want %q", delivery.Outcome, steer.DeliveryWoke)
	}
	if len(d.delivered) != 1 || d.delivered[0].ChildSessionID != "child-1" {
		t.Fatalf("delivered = %+v, want exactly one event for child-1", d.delivered)
	}
}

// TestFakeCanceller_CancelSubtreeThenRevive drives steer.Canceller through
// a fake.
func TestFakeCanceller_CancelSubtreeThenRevive(t *testing.T) {
	c := &fakeCanceller{revived: map[string]int{}}
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}

	report, err := c.CancelSubtree(context.Background(), "root-1", by)
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	if len(report.Reached) != 1 || report.Reached[0] != "root-1" {
		t.Fatalf("CancelSubtree report = %+v, want Reached=[root-1]", report)
	}

	gen, err := c.Revive(context.Background(), "root-1", by)
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if gen != 2 {
		t.Fatalf("Revive generation = %d, want 2", gen)
	}
}

// TestFakeRecordClassifier_Classify drives steer.RecordClassifier through a
// fake.
func TestFakeRecordClassifier_Classify(t *testing.T) {
	c := &fakeClassifier{class: map[string]steer.Class{"legacy-1": steer.ClassLegacyDelegate}}
	class, err := c.Classify(context.Background(), "legacy-1")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if class != steer.ClassLegacyDelegate {
		t.Fatalf("Classify = %q, want legacy_delegate", class)
	}
	if class.Runnable() {
		t.Fatal("ClassLegacyDelegate.Runnable() = true, want false")
	}
}

// TestNopBoundaryObserver_IsANoOp proves the production BoundaryObserver
// compiles against the interface and genuinely does nothing observable.
func TestNopBoundaryObserver_IsANoOp(t *testing.T) {
	var obs steer.BoundaryObserver = steer.NopBoundaryObserver{}
	obs.Observe(steer.BoundaryFinalReply, "child-1", steer.AudienceSteeringSession)
	// No panic, no return value to assert on — the contract is "does
	// nothing observable", proven by reaching this line.
}

// TestRecordingObserver_ObservesEveryBoundary drives a BoundaryObserver
// implementation through every reachable landing-order boundary and pins the
// exact inventory so a dead boundary cannot make the coverage loop vacuous.
func TestRecordingObserver_ObservesEveryBoundary(t *testing.T) {
	want := []steer.Boundary{
		steer.BoundarySyncToolText,
		steer.BoundaryAsyncToolFeedback,
		steer.BoundaryFinalReply,
		steer.BoundaryMedia,
		steer.BoundaryRetryNotice,
		steer.BoundaryWebchatStreaming,
		steer.BoundaryExternalChannelStreaming,
		steer.BoundaryAgentRequestedMessage,
		steer.BoundaryTaskResultNotification,
		steer.BoundaryTypedErrorFrame,
		steer.BoundaryQuestionCard,
	}
	if !reflect.DeepEqual(steer.Boundaries, want) {
		t.Fatalf("steer.Boundaries = %v, want reachable inventory %v", steer.Boundaries, want)
	}
	obs := &recordingObserver{}
	for _, b := range steer.Boundaries {
		obs.Observe(b, "child-1", steer.AudienceSteeringSession)
	}
	if len(obs.calls) != len(want) {
		t.Fatalf("observed %d boundary calls, want %d", len(obs.calls), len(want))
	}
	seen := map[steer.Boundary]bool{}
	for _, call := range obs.calls {
		seen[call.boundary] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("observed %d DISTINCT boundaries, want %d (a duplicate or collision)", len(seen), len(want))
	}
}

// TestDeps_BundlesEveryImplementation proves steer.Deps compiles from
// outside pkg/agent with every field populated by a fake — the exact shape
// WP-G's I-7 DelegationTree fixture will build against.
func TestDeps_BundlesEveryImplementation(t *testing.T) {
	deps := steer.Deps{
		Launcher:   &fakeLauncher{dispatch: map[string]steer.DispatchResult{}},
		Canceller:  &fakeCanceller{revived: map[string]int{}},
		Deliverer:  &fakeDeliverer{},
		Classifier: &fakeClassifier{class: map[string]steer.Class{}},
		BootHook: func(ctx context.Context) error {
			return nil
		},
	}
	if err := deps.BootHook(context.Background()); err != nil {
		t.Fatalf("BootHook: %v", err)
	}
	if deps.Launcher == nil || deps.Canceller == nil || deps.Deliverer == nil || deps.Classifier == nil {
		t.Fatal("Deps: an interface field is nil after construction")
	}
}

// TestGoalSpec_CompiledShape proves the create_task-mirroring GoalSpec/
// Criterion shape (pkg/steer may not import pkg/tools or pkg/task) is
// usable from outside pkg/agent.
func TestGoalSpec_CompiledShape(t *testing.T) {
	goal := &steer.GoalSpec{
		Criteria: []steer.Criterion{{
			Kind:     steer.CriterionKindCheck,
			Judgment: steer.JudgmentBoolean,
			Text:     "the build passes",
			Check:    &steer.CriterionCheck{Command: "make build", ExpectedExitCode: 0},
		}},
		DoD: []steer.Criterion{{
			Kind:     steer.CriterionKindProse,
			Judgment: steer.JudgmentBoolean,
			Text:     "a human confirmed the page renders",
		}},
	}
	if len(goal.Criteria) != 1 || len(goal.DoD) != 1 {
		t.Fatalf("GoalSpec = %+v, want exactly one criterion and one DoD item", goal)
	}
}
