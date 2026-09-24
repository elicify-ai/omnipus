// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-1/I-2 — SessionLauncher.Launch and Dispatch,
// phase 2's real body. Built against a real *AgentLoop (newAL(t)) and its
// real session/lifecycle stores — never a spy standing in for either.

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type dispatchContextProvider struct {
	entered chan struct{}
	once    sync.Once
	release chan struct{}
}

func (p *dispatchContextProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "finished"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *dispatchContextProvider) GetDefaultModel() string { return "dispatch-context-test" }

// newSteerAL is newAL, plus wiring the lifecycle store SessionLauncher
// needs — newTestAgentLoop's minimal harness never calls
// SetSessionMessagingStores (that happens later, at gateway boot), so
// AgentLoop.GetSessionLifecycleStore() is nil by default.
func newSteerAL(t *testing.T) (*AgentLoop, func()) {
	t.Helper()
	al, cleanup := newAL(t)
	home := al.GetConfig().Agents.Defaults.Home
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	return al, cleanup
}

// newTestSteeringSession creates a real chat session (the target agent is
// testDefaultAgentID, "mia" — the one agent newAL(t) always registers) and
// returns its id, for use as a Launch request's SteeringSessionID.
func newTestSteeringSession(t *testing.T, al *AgentLoop, workspaceID string) string {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession(steerer): %v", err)
	}
	if workspaceID != "" {
		if err := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID}); err != nil {
			t.Fatalf("SetMeta(steerer).WorkspaceID: %v", err)
		}
	}
	return meta.ID
}

// newSteerALWithSkills is newSteerAL (steered-launch wiring), parameterized
// on testDefaultAgentID's skill grant list — ADR-072 D9's requested_skill
// gate (Launch, above) resolves against the TARGET agent's own
// ContextBuilder, and its allowlist is snapshotted from AgentConfig.Skills
// at AGENT CONSTRUCTION time (D5: prepareIdentity's WithSkillAllowlist
// call), not read live. Callers MUST create any skill fixture (writeSkill/
// writeSkillWithName) under home BEFORE calling this, mirroring
// loop_test.go's TestProcessMessage_SkillCommandLoadsRequestedSkill fixture
// order.
func newSteerALWithSkills(t *testing.T, home string, grantedSkills []string) (*AgentLoop, func()) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home, Skills: grantedSkills}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	return al, func() {}
}

func TestLaunch_TitleRequired(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	_, err := l.Launch(context.Background(), steer.LaunchRequest{TargetAgentID: testDefaultAgentID})
	if !errors.Is(err, steer.ErrTitleRequired) {
		t.Fatalf("Launch(no label, no task) = %v, want ErrTitleRequired", err)
	}
}

func TestLaunch_UnknownAgentRefused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	_, err := l.Launch(context.Background(), steer.LaunchRequest{TargetAgentID: "no-such-agent", Task: "do something"})
	if !errors.Is(err, steer.ErrAgentUnknown) {
		t.Fatalf("Launch(unknown agent) = %v, want ErrAgentUnknown", err)
	}
}

// TestLaunch_TaskOriginWithoutTaskID_Refused proves the launch-side fix for
// the task front: a task-origin launch with an empty Origin.TaskID is refused
// here — before any write — rather than failing later at Dispatch, which
// routes a task-origin session into task orchestration that needs the id
// (task_executor.go::dispatchLaunchedTask).
func TestLaunch_TaskOriginWithoutTaskID_Refused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	_, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "do a task",
		Origin: steer.Origin{Kind: steer.OriginKindTask}, WorkspaceID: "ws-1", Owner: "dan",
	})
	if !errors.Is(err, steer.ErrTaskIDRequired) {
		t.Fatalf("Launch(task origin, empty TaskID) = %v, want ErrTaskIDRequired", err)
	}
}

func TestLaunch_SteeredEmptyParentAgentHonorsFailClosedSwitch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		strict  bool
		wantErr bool
	}{
		{name: "strict default refuses", strict: true, wantErr: true},
		{name: "operator override permits", strict: false, wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			al.GetConfig().Tools.Delegate.RequireParentAgentID = &tc.strict
			steerer, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "")
			if err != nil {
				t.Fatalf("NewSession(steerer): %v", err)
			}

			result, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
				SteeringSessionID: steerer.ID,
				TargetAgentID:     testDefaultAgentID,
				Task:              "do something",
				Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-empty-parent"},
			})
			if tc.wantErr {
				if !errors.Is(err, steer.ErrInvalidEdge) || result.SessionID != "" {
					t.Fatalf("Launch() = %+v, %v; want no child and ErrInvalidEdge", result, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Launch() with operator override: %v", err)
			}
			record, err := al.GetSessionLifecycleStore().Load(result.SessionID)
			if err != nil {
				t.Fatalf("Load(child): %v", err)
			}
			if record.ParentAgentID != "" {
				t.Fatalf("ParentAgentID = %q, want empty under explicit override", record.ParentAgentID)
			}
		})
	}
}

func TestLaunch_GoalWithNoCriteria_Refused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	_, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID,
		Task:          "do something",
		Goal:          &steer.GoalSpec{},
	})
	if err == nil {
		t.Fatal("Launch(goal with zero criteria) = nil error, want a refusal")
	}
	const want = "Add at least one acceptance criterion: say what must be true for this task to be done."
	if err.Error() != want {
		t.Fatalf("Launch(goal with zero criteria) error = %q, want %q (create_task's own message)", err.Error(), want)
	}
}

// TestLaunch_GoalWithNoDoD_Refused proves the same rule holds symmetrically
// however Launch is reached: a goal always has both acceptance criteria and
// a definition of done (founder decision). A Goal with criteria but no DoD
// must be refused here, cleanly, before createLaunchGoal ever calls
// goal.New — not left to fail deep inside pkg/goal.Goal.Validate's own
// "dod must contain at least one item" check.
func TestLaunch_GoalWithNoDoD_Refused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	_, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID,
		Task:          "do something",
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "The work is done"}},
		},
	})
	if err == nil {
		t.Fatal("Launch(goal with criteria but no dod) = nil error, want a refusal")
	}
	const want = "Add at least one Definition of Done item, distinct from the acceptance criteria."
	if err.Error() != want {
		t.Fatalf("Launch(goal with criteria but no dod) error = %q, want %q (create_task's own message)", err.Error(), want)
	}
}

// TestLaunch_OrdinaryRoot_WritesCompleteRecord is US-1/AS-1's ordinary-root
// half: a human/schedule-created launch (no SteeringSessionID) writes a
// complete, readable-after-reopen record.
func TestLaunch_OrdinaryRoot_WritesCompleteRecord(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID,
		Task:          "build the page",
		Origin:        steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-1"},
		WorkspaceID:   "ws-1",
		Owner:         "dan",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.SessionID == "" || res.Generation != 1 {
		t.Fatalf("Launch result = %+v, want a non-empty SessionID and Generation 1", res)
	}

	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.SteeredBy != nil {
		t.Errorf("rec.SteeredBy = %+v, want nil (ordinary root)", rec.SteeredBy)
	}
	if rec.Origin == nil || rec.Origin.Kind != steer.OriginKindTask || rec.Origin.TaskID != "task-1" {
		t.Errorf("rec.Origin = %+v, want {Kind: task, TaskID: task-1}", rec.Origin)
	}
	if rec.WorkspaceID != "ws-1" {
		t.Errorf("rec.WorkspaceID = %q, want ws-1", rec.WorkspaceID)
	}
	if rec.State != session.LifecycleQueued {
		t.Errorf("rec.State = %q, want queued", rec.State)
	}

	meta, err := al.GetSessionStore().GetMeta(res.SessionID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.Title != "build the page" {
		t.Errorf("meta.Title = %q, want %q (Task text fallback)", meta.Title, "build the page")
	}
	if meta.WorkspaceID != "ws-1" {
		t.Errorf("meta.WorkspaceID = %q, want ws-1", meta.WorkspaceID)
	}

	hist := al.GetSessionStore().GetHistory(res.SessionID)
	if len(hist) != 1 || hist[0].Content != "build the page" {
		t.Fatalf("GetHistory = %v, want exactly one message containing the task text", hist)
	}
}

// TestLaunch_Steered_WritesEdgeAndRootRecordForSteerer is US-1/AS-1 + AS-2:
// a steered launch under a steering session with NO lifecycle record of
// its own gets BOTH the child's edge and the steerer's own ordinary_root
// record, minted under the same launch.
func TestLaunch_Steered_WritesEdgeAndRootRecordForSteerer(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "research the topic",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	childRec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if childRec.SteeredBy == nil {
		t.Fatal("child record has no SteeredBy edge")
	}
	if childRec.SteeredBy.SteeringSessionID != steerer {
		t.Errorf("SteeringSessionID = %q, want %q", childRec.SteeredBy.SteeringSessionID, steerer)
	}
	if childRec.SteeredBy.RootSessionID != steerer {
		t.Errorf("RootSessionID = %q, want %q (steerer is the root at depth 1)", childRec.SteeredBy.RootSessionID, steerer)
	}
	if childRec.WorkspaceID != "ws-1" {
		t.Errorf("child WorkspaceID = %q, want inherited ws-1", childRec.WorkspaceID)
	}

	childMeta, err := al.GetSessionStore().GetMeta(res.SessionID)
	if err != nil {
		t.Fatalf("GetMeta(child): %v", err)
	}
	if childMeta.ParentSessionID != steerer {
		t.Errorf("child ParentSessionID = %q, want %q", childMeta.ParentSessionID, steerer)
	}
	if childMeta.Owner != "" {
		// steerer session was created with no Owner in this fixture.
		t.Errorf("child Owner = %q, want empty (inherited from steerer, which has none)", childMeta.Owner)
	}

	steererRec, err := al.GetSessionLifecycleStore().Load(steerer)
	if err != nil {
		t.Fatalf("Load(steerer): %v — I-1 round 9 requires a root record to have been minted", err)
	}
	if steererRec.SteeredBy != nil {
		t.Errorf("steerer SteeredBy = %+v, want nil (it is a root)", steererRec.SteeredBy)
	}
	if steererRec.Origin == nil || steererRec.Origin.Kind != steer.OriginKindChat {
		t.Errorf("steerer Origin = %+v, want {Kind: chat} (its own UnifiedMeta.Type)", steererRec.Origin)
	}
}

func TestLaunch_DepthBudgetUsesEdgeAndPerformancePrecedence(t *testing.T) {
	tests := []struct {
		name          string
		edgeDepth     int
		globalDepth   int
		wantRemaining int
	}{
		{name: "edge tighter", edgeDepth: 2, globalDepth: 7, wantRemaining: 1},
		{name: "performance tighter", edgeDepth: 10, globalDepth: 2, wantRemaining: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const workspaceID = "01JLAUNCHDEPTH00000000001"
			const parentAgentID = "depth-parent"
			seedWorkspaceGraph(t, workspaceID, true, []graphEdge{
				edge(parentAgentID, testDefaultAgentID, []string{"direct"}, intPtr(tc.edgeDepth)),
			})
			al, cleanup := newSteerAL(t)
			defer cleanup()
			al.GetConfig().Performance.MaxDelegationDepth = tc.globalDepth

			parentID := newTestSteeringSession(t, al, workspaceID)
			if err := al.GetSessionStore().SwitchAgent(parentID, parentAgentID); err != nil {
				t.Fatalf("SwitchAgent(parent): %v", err)
			}
			result, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
				SteeringSessionID: parentID,
				TargetAgentID:     testDefaultAgentID,
				Task:              "depth precedence",
				Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-depth"},
			})
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			rec, err := al.GetSessionLifecycleStore().Load(result.SessionID)
			if err != nil {
				t.Fatalf("Load(child): %v", err)
			}
			if got := rec.SteeredBy.Authorization.RemainingDepth; got != tc.wantRemaining {
				t.Fatalf("remaining depth = %d, want %d (edge=%d performance=%d)", got, tc.wantRemaining, tc.edgeDepth, tc.globalDepth)
			}
		})
	}
}

func TestLaunch_SteeredPersistsRequiredMetadataAndActiveGoal(t *testing.T) {
	goalHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", goalHome)
	al, cleanup := newSteerAL(t)
	defer cleanup()

	steererMeta, err := al.GetSessionStore().NewChannelSession(
		"webchat", "webchat.default", "chat-42", testDefaultAgentID, "Parent conversation")
	if err != nil {
		t.Fatalf("NewChannelSession(steerer): %v", err)
	}
	workspaceID := "ws-1"
	if setMetaErr := al.GetSessionStore().SetMeta(steererMeta.ID, session.MetaPatch{WorkspaceID: &workspaceID}); setMetaErr != nil {
		t.Fatalf("SetMeta(steerer).WorkspaceID: %v", setMetaErr)
	}

	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steererMeta.ID,
		TargetAgentID:     testDefaultAgentID,
		Label:             "Check the launch contract",
		Task:              "Verify every required launch field.",
		Origin:            steer.Origin{Kind: steer.OriginKindTask, CallID: "call-goal", TaskID: "task-goal"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "The launch metadata is complete"}},
			DoD:      []steer.Criterion{{Text: "The goal is active for the child session"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.Title != "Check the launch contract" {
		t.Errorf("Title = %q, want lifecycle title", rec.Title)
	}
	if rec.ParentAgentID != testDefaultAgentID {
		t.Errorf("ParentAgentID = %q, want %q", rec.ParentAgentID, testDefaultAgentID)
	}
	if rec.GoalRef == "" {
		t.Fatal("GoalRef is empty")
	}
	if rec.SteeredBy == nil {
		t.Fatal("SteeredBy is nil")
	}
	if rec.SteeredBy.ReportingTarget.ChatID != "chat-42" {
		t.Errorf("ReportingTarget.ChatID = %q, want chat-42", rec.SteeredBy.ReportingTarget.ChatID)
	}
	if rec.SteeredBy.Authorization.Mode != session.AuthorizationModeTask {
		t.Errorf("Authorization.Mode = %q, want task", rec.SteeredBy.Authorization.Mode)
	}

	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	if g.State != generated.GoalStateActive || g.ActiveSessionID != res.SessionID {
		t.Errorf("goal state/session = %q/%q, want active/%q", g.State, g.ActiveSessionID, res.SessionID)
	}
	if g.OwnerKind != generated.GoalOwnerKindSession || g.OwnerID != res.SessionID {
		t.Errorf("goal owner = %q/%q, want session/%q", g.OwnerKind, g.OwnerID, res.SessionID)
	}
	// The record a criteria+dod LaunchRequest.Goal produces must itself
	// satisfy pkg/goal.Goal's own invariants — proof that "both supplied"
	// never reaches the deep goal.New/Validate failure a criteria-only or
	// dod-only goal used to hit (see TestLaunch_GoalWithNoDoD_Refused for
	// the refused-early half of the same rule).
	if err := g.Validate(); err != nil {
		t.Errorf("the created goal record fails its own Validate(): %v", err)
	}
}

// TestLaunch_NoWorkspaceInherited is US-1/AS-3: a creator with no workspace
// yields a child with none — never invented.
func TestLaunch_NoWorkspaceInherited(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "") // no workspace

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do the thing",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.WorkspaceID != "" {
		t.Errorf("child WorkspaceID = %q, want empty", rec.WorkspaceID)
	}
}

// TestLaunch_SteeredLaunch_ExplicitWorkspaceOwnerRefused is I-2's LaunchRequest
// contract: WorkspaceID/Owner must be empty for a steered launch (they are
// inherited, never independently supplied).
func TestLaunch_SteeredLaunch_ExplicitWorkspaceOwnerRefused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")

	_, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do the thing",
		WorkspaceID:       "ws-should-not-be-set",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("Launch(steered + explicit WorkspaceID) = %v, want ErrInvalidEdge", err)
	}
}

// TestLaunch_UnderStampedParent_ChildStampedAtLaunch is US-4/AS-6: a launch
// under a parent carrying a Stop marker for its CURRENT generation is
// stamped at launch and never starts.
func TestLaunch_UnderStampedParent_ChildStampedAtLaunch(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")

	// Seed the steerer with its own root record, already carrying a Stop
	// marker for its current generation (Generation 1).
	if err := al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID:      steerer,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    "ws-1",
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: steer.OriginKindChat},
		Stop:           &session.Stop{Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"}},
	}); err != nil {
		t.Fatalf("seed steerer with Stop: %v", err)
	}

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do the thing",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	childRec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if childRec.Stop == nil {
		t.Fatal("child record has no Stop marker, want it stamped at launch (parent carried a current-generation Stop)")
	}
	if childRec.Stop.Generation != childRec.Generation {
		t.Errorf("child Stop.Generation = %d, want %d (its own current generation)", childRec.Stop.Generation, childRec.Generation)
	}
}

// TestLaunch_BothFrontDoors_DifferOnlyInOrigin is US-1/AS-6.
func TestLaunch_BothFrontDoors_DifferOnlyInOrigin(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")

	delegateRes, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "task text",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-1"},
	})
	if err != nil {
		t.Fatalf("Launch(delegate): %v", err)
	}
	taskRes, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "task text",
		Origin: steer.Origin{Kind: steer.OriginKindTask, CallID: "call-2", TaskID: "task-9"},
	})
	if err != nil {
		t.Fatalf("Launch(task): %v", err)
	}

	delegateRec, _ := al.GetSessionLifecycleStore().Load(delegateRes.SessionID)
	taskRec, _ := al.GetSessionLifecycleStore().Load(taskRes.SessionID)

	if delegateRec.SteeredBy.SteeringSessionID != taskRec.SteeredBy.SteeringSessionID ||
		delegateRec.SteeredBy.RootSessionID != taskRec.SteeredBy.RootSessionID ||
		delegateRec.WorkspaceID != taskRec.WorkspaceID ||
		delegateRec.AgentID != taskRec.AgentID {
		t.Fatalf("records differ outside Origin: delegate=%+v task=%+v", delegateRec, taskRec)
	}
	if delegateRec.Origin.Kind == taskRec.Origin.Kind {
		t.Fatal("both records have the same Origin.Kind — the two front doors should differ exactly here")
	}
}

// TestLaunch_NestedSameShardDoesNotDeadlock proves that resolving a nested
// launch's root never re-enters a lifecycle shard lock already held for the
// direct parent. LifecycleStore deliberately stripes records across a fixed
// lock pool, so distinct session IDs can share the same mutex.
func TestLaunch_NestedSameShardDoesNotDeadlock(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	lifecycle := al.GetSessionLifecycleStore()

	rootID := newTestSteeringSession(t, al, "ws-1")
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: rootID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: testDefaultAgentID,
		Origin: &session.Origin{Kind: steer.OriginKindChat},
	}); err != nil {
		t.Fatalf("seed root lifecycle: %v", err)
	}

	var parentID string
	for i := 0; i < 512; i++ {
		candidate := newTestSteeringSession(t, al, "ws-1")
		if lifecycle.Lock(candidate) == lifecycle.Lock(rootID) {
			parentID = candidate
			break
		}
	}
	if parentID == "" {
		t.Fatal("could not find two session IDs sharing a lifecycle shard")
	}
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: parentID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: rootID,
		WorkspaceID: "ws-1", AgentID: testDefaultAgentID, ParentAgentID: testDefaultAgentID,
		Origin: &session.Origin{Kind: steer.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: rootID,
			RootSessionID:     rootID,
			Authorization: session.Authorization{
				Mode:           session.AuthorizationModeDirect,
				RemainingDepth: 3,
			},
		},
	}); err != nil {
		t.Fatalf("seed nested parent lifecycle: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := l.Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: parentID,
			TargetAgentID:     testDefaultAgentID,
			Task:              "nested work",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-nested"},
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nested Launch: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nested Launch deadlocked while loading an ancestor on the direct parent's lifecycle shard")
	}
}

// TestLaunch_DefaultTimeoutIsThirtyMinutes proves D9's founder-set default: a
// steered launch with no per-call timeout_seconds and no configured
// performance.delegation_timeout_minutes records a 30-minute lifetime on the
// edge (SteeredBy.Limits.TimeoutSeconds == 1800).
func TestLaunch_DefaultTimeoutIsThirtyMinutes(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "long work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-default-timeout"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.SteeredBy == nil {
		t.Fatal("SteeredBy = nil, want the edge carrying the resolved timeout")
	}
	if got := rec.SteeredBy.Limits.TimeoutSeconds; got != 30*60 {
		t.Fatalf("Limits.TimeoutSeconds = %d, want %d (30 minutes)", got, 30*60)
	}
}

// TestLaunch_TimeoutPrecedence proves that an explicit call-level timeout and
// a configured performance.delegation_timeout_minutes each override the
// 30-minute default, with the explicit per-call value winning.
func TestLaunch_TimeoutPrecedence(t *testing.T) {
	t.Run("explicit call-level timeout wins", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		steerer := newTestSteeringSession(t, al, "ws-1")
		res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "quick work",
			Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-explicit-timeout"},
			Limits: steer.Limits{TimeoutSeconds: 90},
		})
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := rec.SteeredBy.Limits.TimeoutSeconds; got != 90 {
			t.Fatalf("Limits.TimeoutSeconds = %d, want 90 (explicit call-level)", got)
		}
	})

	t.Run("configured value wins over the default", func(t *testing.T) {
		al, cleanup := newSteerAL(t)
		defer cleanup()
		al.GetConfig().Performance.DelegationTimeoutMinutes = 10
		steerer := newTestSteeringSession(t, al, "ws-1")
		res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "configured work",
			Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-configured-timeout"},
		})
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := rec.SteeredBy.Limits.TimeoutSeconds; got != 10*60 {
			t.Fatalf("Limits.TimeoutSeconds = %d, want %d (configured 10 minutes)", got, 10*60)
		}
	})
}

// ============================== Dispatch ==============================

func TestDispatch_TerminalSessionRefused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	if err := al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: "sess-terminal", Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: testDefaultAgentID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := l.Dispatch(context.Background(), "sess-terminal", 1)
	if !errors.Is(err, steer.ErrTerminal) {
		t.Fatalf("Dispatch(terminal) = %v, want ErrTerminal", err)
	}
}

func TestDispatch_StopMarkerForCurrentGeneration_Refused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	if err := al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: "sess-stopped", Generation: 1, State: session.LifecycleQueued,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: testDefaultAgentID,
		Stop: &session.Stop{Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := l.Dispatch(context.Background(), "sess-stopped", 1)
	if !errors.Is(err, steer.ErrDispatchCancelled) {
		t.Fatalf("Dispatch(stopped, current gen) = %v, want ErrDispatchCancelled", err)
	}
}

// TestDispatch_AtCap_Queued proves I-2/D9: at the effective cap, Dispatch
// returns `queued` at once rather than blocking or refusing.
func TestDispatch_AtCap_Queued(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	// Pin the effective cap to 1 — an unconfigured test AgentLoop otherwise
	// resolves EffectiveMaxParallelAgents() to the physical safety backstop
	// (double digits), so occupying one slot alone would never saturate it.
	al.GetConfig().Performance.MaxParallelAgents = 1

	// Saturate the gate directly (unit-level control over the admission
	// primitive, isolated from turn execution).
	al.steerAdmission().tryAdmit("occupying-session", 1) //nolint:dogsled // only the reservation side effect matters

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "queued task",
		Origin: steer.Origin{Kind: steer.OriginKindChat}, WorkspaceID: "ws-1", Owner: "dan",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	dr, err := l.Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dr.State != steer.DispatchQueued {
		t.Fatalf("Dispatch at cap = %+v, want State=queued", dr)
	}
	if dr.QueuePosition != 1 {
		t.Fatalf("QueuePosition = %d, want 1", dr.QueuePosition)
	}

	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != session.LifecycleQueued {
		t.Errorf("persisted State = %q, want queued", rec.State)
	}
}

// TestDispatch_AdmitsAndRegistersATurn proves the `running` path registers
// a real turn before returning (registration is synchronous; the turn's
// own execution is fire-and-forget and not awaited by this test).
func TestDispatch_AdmitsAndRegistersATurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "run me",
		Origin: steer.Origin{Kind: steer.OriginKindChat}, WorkspaceID: "ws-1", Owner: "dan",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	dr, err := l.Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if dr.State != steer.DispatchRunning {
		t.Fatalf("Dispatch = %+v, want State=running", dr)
	}
	ts := al.getActiveTurnState(res.SessionID)
	if ts == nil {
		t.Fatal("getActiveTurnState after a `running` Dispatch = nil, want the registered turn")
	}
	// Wait for the fire-and-forget background turn (dispatchSteeredSession's
	// `go al.runTurn(...)`) to finish before this test returns — otherwise
	// its still-running goroutine can race t.TempDir()'s cleanup, deleting
	// files out from under an in-flight write ("directory not empty").
	select {
	case <-ts.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("background turn did not finish within 10s")
	}
}

func TestDispatch_ChildLifetimeIndependentOfCallerContext(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider := &dispatchContextProvider{entered: make(chan struct{}), release: make(chan struct{})}
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	agentInst.Provider = provider

	launcher := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              "keep running after the delegate tool returns",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	callerCtx, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	if _, err := launcher.Dispatch(callerCtx, launched.SessionID, launched.Generation); err != nil {
		t.Fatalf("Dispatch with canceled caller context: %v", err)
	}
	select {
	case <-provider.entered:
		// The application-owned child context reached the provider.
	case <-time.After(3 * time.Second):
		t.Fatal("child never reached its provider after the caller context was canceled")
	}
	close(provider.release)
	ts := al.getActiveTurnState(launched.SessionID)
	if ts != nil {
		select {
		case <-ts.Finished():
		case <-time.After(3 * time.Second):
			t.Fatal("child did not finish after the provider was released")
		}
	}
}

// TestSteeredTurnRunContext is ADR-091 fix lane 1's Finding E (MEDIUM) unit
// coverage: rec.SteeredBy.Limits.TimeoutSeconds used to be read only from
// runDispatchedSteeredTurn (the first-run/dispatch path); this is the ONE
// helper both that path and the wake/re-entry path
// (loop_inbound.go::processSteeredSystemWake) now share — D9's "the
// timeout's scope is the session's lifetime across re-entries" only holds if
// there is one function computing it.
func TestSteeredTurnRunContext(t *testing.T) {
	t.Run("no SteeredBy: no deadline", func(t *testing.T) {
		ctx, cancel := steeredTurnRunContext(context.Background(), &session.LifecycleRecord{})
		defer cancel()
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("an ordinary_root record (SteeredBy == nil) produced a deadline")
		}
	})
	t.Run("timeout_seconds=0: no deadline", func(t *testing.T) {
		rec := &session.LifecycleRecord{SteeredBy: &session.SteeredBy{Limits: session.Limits{TimeoutSeconds: 0}}}
		ctx, cancel := steeredTurnRunContext(context.Background(), rec)
		defer cancel()
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("timeout_seconds=0 produced a deadline")
		}
	})
	t.Run("configured timeout with a real CreatedAt: deadline anchored there", func(t *testing.T) {
		created := time.Now().Add(-90 * time.Second)
		rec := &session.LifecycleRecord{CreatedAt: created, SteeredBy: &session.SteeredBy{Limits: session.Limits{TimeoutSeconds: 120}}}
		ctx, cancel := steeredTurnRunContext(context.Background(), rec)
		defer cancel()
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected a deadline")
		}
		want := created.Add(120 * time.Second)
		if diff := dl.Sub(want); diff < -time.Second || diff > time.Second {
			t.Fatalf("deadline = %v, want ~%v (CreatedAt + TimeoutSeconds)", dl, want)
		}
	})
	// Finding E's second half: a zero CreatedAt used to turn the timeout OFF
	// entirely (the old inline check in runDispatchedSteeredTurn gated on
	// !rec.CreatedAt.IsZero()) — a missing/unset timestamp silently removed
	// a configured limit instead of just meaning "no better anchor is known
	// yet".
	t.Run("zero CreatedAt: deadline counts from now, never silently unlimited", func(t *testing.T) {
		rec := &session.LifecycleRecord{SteeredBy: &session.SteeredBy{Limits: session.Limits{TimeoutSeconds: 60}}}
		before := time.Now()
		ctx, cancel := steeredTurnRunContext(context.Background(), rec)
		defer cancel()
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("a missing CreatedAt silently removed the configured timeout (Finding E regression)")
		}
		if dl.Before(before.Add(59*time.Second)) || dl.After(time.Now().Add(61*time.Second)) {
			t.Fatalf("deadline = %v, want ~60s from now (%v..%v)", dl, before.Add(59*time.Second), time.Now().Add(61*time.Second))
		}
	})
}

// wakeBlocksUntilCtxDoneProvider blocks in Chat until its context is done,
// then returns ctx.Err() — used to observe whether a run context actually
// carries a deadline, without depending on wall-clock timing beyond the
// test's own bounded wait.
type wakeBlocksUntilCtxDoneProvider struct{}

func (p *wakeBlocksUntilCtxDoneProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *wakeBlocksUntilCtxDoneProvider) GetDefaultModel() string { return "wake-timeout-test" }

// TestWake_AppliesConfiguredTimeout is ADR-091 fix lane 1's Finding E
// integration proof: the wake/re-entry path
// (loop_inbound.go::processSteeredSystemWake) must honor
// rec.SteeredBy.Limits.TimeoutSeconds exactly like the first-run/dispatch
// path does — before the fix, a woken turn ran with a bare, undeadlined
// context and could run forever regardless of its configured timeout.
//
// wakeBlocksUntilCtxDoneProvider blocks until ITS context is done; the test
// bounds the WHOLE call in a 5s select so a missing deadline fails this test
// deterministically (a timeout, not a hang) instead of blocking forever.
func TestWake_AppliesConfiguredTimeout(t *testing.T) {
	al, cleanup := newSteerALWithProvider(t, &wakeBlocksUntilCtxDoneProvider{})
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, rootID, "call-wake-timeout")
	if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.SteeredBy.Limits.TimeoutSeconds = 1
		r.CreatedAt = time.Now().Add(-500 * time.Millisecond) // half the budget already spent
		return nil
	}); err != nil {
		t.Fatalf("Mutate(timeout): %v", err)
	}

	wake := bus.InboundMessage{
		Channel:                  "system",
		ChatID:                   "system:steer:" + child.SessionID,
		Sender:                   bus.SenderInfo{CanonicalID: "test"},
		Content:                  "nudge",
		AsyncTranscriptSessionID: child.SessionID,
		AsyncOriginAgentID:       child.AgentID,
		Metadata:                 map[string]string{"steer_message_id": "wake-1", "steer_generation": "1"},
	}

	done := make(chan error, 1)
	go func() {
		_, err := al.processSystemMessage(context.Background(), wake)
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("processSteeredSystemWake did not return within 5s — Finding E: " +
			"the edge's configured timeout is not being applied on a wake/re-entry")
	}

	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if rec.State != session.LifecycleTimedOut {
		t.Fatalf("child state after a wake that exceeded its configured timeout = %q, want timed_out", rec.State)
	}
}

// --- ADR-072 D9 / FR-050/FR-053/FR-054: requested_skill gate on the real
// launch path (fix lane RX-SKILL, 2026-09-24) ---
//
// The delegate tool's own schema promises requested_skill is "a hard
// request, not a hint": if the target is not granted it, the whole
// delegation call fails instead of silently proceeding without it. Before
// this fix, steer.LaunchRequest carried no such field at all, so Launch
// could never enforce it — the promise was false. These three tests are the
// exit-proof red-then-green trio: each FAILED before RequestedSkill was
// threaded through and gated here, because Launch simply ignored it and
// proceeded every time.

// TestLaunch_RequestedSkillGranted_Proceeds proves the happy path is
// unaffected: a skill the target agent IS granted lets the launch proceed
// exactly as an unrequested launch would.
func TestLaunch_RequestedSkillGranted_Proceeds(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "finance-news")
	al, cleanup := newSteerALWithSkills(t, home, []string{"finance-news"})
	defer cleanup()
	l := NewSteerLauncher(al)

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID,
		Task:          "pull the latest finance headlines",
		// Origin is REQUIRED: persistLocked rejects an empty kind, and both
		// production callers always set one (delegate_run.go -> delegate,
		// task_executor.go -> task). This test predated that validation.
		Origin:         steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-skill-granted"},
		RequestedSkill: "finance-news",
	})
	if err != nil {
		t.Fatalf("Launch(granted requested_skill) = %v, want nil error", err)
	}
	if res.SessionID == "" || res.Generation != 1 {
		t.Fatalf("Launch(granted requested_skill) result = %+v, want a real session and Generation 1", res)
	}
}

// TestLaunch_RequestedSkillDenied_RefusesAndPersistsNoSession is the core
// defect fix: the slug exists on disk but testDefaultAgentID's own grant
// list does not include it. The call must fail with
// tools.ErrRequestedSkillDenied (the sentinel pkg/tools/delegate_run.go
// discriminates on via errors.Is), AND no child lifecycle record may be
// persisted for it — the gate runs before launchOrdinaryRoot/launchSteered
// ever mint or write one.
func TestLaunch_RequestedSkillDenied_RefusesAndPersistsNoSession(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "finance-news")
	// Installed on disk, but the agent's OWN grant list names a different
	// skill — ADR-072 D9: "the receiver's grant is the real gate", the
	// caller's intent to request it is not enough.
	al, cleanup := newSteerALWithSkills(t, home, []string{"some-other-skill"})
	defer cleanup()
	l := NewSteerLauncher(al)

	before, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List(before): %v", err)
	}

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID:  testDefaultAgentID,
		Task:           "pull the latest finance headlines",
		RequestedSkill: "finance-news",
	})
	if !errors.Is(err, tools.ErrRequestedSkillDenied) {
		t.Fatalf("Launch(denied requested_skill) error = %v, want errors.Is(..., tools.ErrRequestedSkillDenied)", err)
	}
	if res != (steer.LaunchResult{}) {
		t.Fatalf("Launch(denied requested_skill) result = %+v, want the zero value (no session)", res)
	}

	after, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List(after): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("lifecycle record count = %d after a denied requested_skill launch, want %d unchanged — "+
			"NO child session may be persisted for a refused launch", len(after), len(before))
	}
}

// TestLaunch_RequestedSkillUnknown_DistinctFailure is FR-054: a slug that
// resolves to nothing on any shelf visible to the target agent at all is a
// DIFFERENT failure from a denial (installed-but-ungranted) — never
// conflated. The two sentinels (tools.ErrRequestedSkillNotFound vs.
// tools.ErrRequestedSkillDenied) must not both match.
func TestLaunch_RequestedSkillUnknown_DistinctFailure(t *testing.T) {
	home := t.TempDir()
	// Nothing installed at all, nothing granted.
	al, cleanup := newSteerALWithSkills(t, home, nil)
	defer cleanup()
	l := NewSteerLauncher(al)

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID:  testDefaultAgentID,
		Task:           "pull the latest finance headlines",
		RequestedSkill: "totally-unresolvable-zzz",
	})
	if !errors.Is(err, tools.ErrRequestedSkillNotFound) {
		t.Fatalf("Launch(unresolvable requested_skill) error = %v, want errors.Is(..., tools.ErrRequestedSkillNotFound)", err)
	}
	if errors.Is(err, tools.ErrRequestedSkillDenied) {
		t.Fatalf("Launch(unresolvable requested_skill) error = %v must NOT also satisfy ErrRequestedSkillDenied "+
			"(FR-054: not-found and denied are distinct outcomes)", err)
	}
	if res != (steer.LaunchResult{}) {
		t.Fatalf("Launch(unresolvable requested_skill) result = %+v, want the zero value (no session)", res)
	}
}

// --- Reviewer finding, same landing-order gap (fix lane RX-SKILL,
// 2026-09-24): the live subagent_start frame's task_label was never
// truncated ---
//
// The deleted subturn.go truncated the task-text label fallback to 60
// runes; the ADR-091 rewrite of Launch dropped that truncation while
// pkg/gateway/replay.go::resolveTaskLabel kept its own copy, so live and
// replay silently disagreed. contracts/components/schemas/
// SubagentStartFrame.yaml caps task_label at maxLength 100 and
// src/lib/ws.ts DROPS (not just warns on) any frame that fails safeParse —
// so delegate(task="<a task longer than 100 chars>") with no label, the
// common case, silently discarded the live frame client-side: the parent's
// side panel showed no row at all until the next page reload.
func TestLaunch_LongTaskNoLabel_SubagentStartTaskLabelSurvivesContractCap(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")

	longTask := strings.Repeat("a", 300) // far past the 100-char contract cap
	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              longTask,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-long-task"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if res.SessionID == "" {
		t.Fatal("Launch: empty SessionID")
	}

	entries, err := al.GetSessionStore().ReadTranscript(steerer)
	if err != nil {
		t.Fatalf("ReadTranscript(steerer): %v", err)
	}
	var frame *generated.SubagentStartFrame
	for i := range entries {
		if entries[i].SystemSubtype == session.SystemSubtypeSubagentStart {
			frame = entries[i].SubagentStart
			break
		}
	}
	if frame == nil {
		t.Fatal("no subagent_start frame was persisted for the steering session")
	}
	if n := len([]rune(frame.TaskLabel)); n > 100 {
		t.Fatalf("task_label = %d runes, want <= 100 (SubagentStartFrame.yaml maxLength: 100) — an "+
			"over-cap label fails Zod validation client-side and src/lib/ws.ts silently drops the live frame", n)
	}
	wantLabel := string([]rune(longTask)[:60])
	if frame.TaskLabel != wantLabel {
		t.Fatalf("task_label = %q, want %q (60-rune truncation matching pkg/gateway/replay.go::resolveTaskLabel, "+
			"so live and replay agree)", frame.TaskLabel, wantLabel)
	}
}
