// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 test plan, MAJ-001 characterisation: web Stop on a chat root that
// has delegated leaves the root record running, with a Stop marker for the
// current generation, and does not write state "cancelled".
//
// Oracle: docs/internal/architecture/ADR-093-open-conversation-must-keep-
// delegation.md, test-plan row "Web Stop on a chat root that has delegated."
// The expected state is that row, not whatever handleCancel does today.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adr093IdleProvider is present only because constructing an agent loop
// requires one. This test never runs a turn.
type adr093IdleProvider struct{}

func (adr093IdleProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "unused"}, nil
}

func (adr093IdleProvider) GetDefaultModel() string { return "adr093-idle" }

// TestAdr093WebStop_DelegatedChatStaysRunningWithCurrentStop drives the web
// Stop button (handleCancel) on a chat root that already has a child. The
// root stays running and carries a Stop for its current generation.
func TestAdr093WebStop_DelegatedChatStaysRunningWithCurrentStop(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, adr093IdleProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	// The production canceller, including the live-turn callback web Stop
	// installs at boot (gateway_boot.go::wireSteerDeps). A canceller without
	// that callback would only stamp and would hide a cancelled write.
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const workspaceID = "ws-adr093-web-stop"
	if setMetaErr := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(workspaceID)}); setMetaErr != nil {
		t.Fatalf("SetMeta workspace: %v", setMetaErr)
	}
	parent := &session.LifecycleRecord{
		SessionID:      meta.ID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    workspaceID,
		AgentID:        "mia",
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}
	if persistErr := lifecycle.Persist(parent); persistErr != nil {
		t.Fatalf("persist parent: %v", persistErr)
	}
	if _, launchErr := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID,
		TargetAgentID:     "mia",
		Task:              "Prepare the spreadsheet",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	}); launchErr != nil {
		t.Fatalf("delegate from the chat (the root must already have a child): %v", launchErr)
	}

	h := makeMinimalHandler()
	h.agentLoop = al
	h.msgBus = msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "user-adr093"
	h.handleCancel(wc, meta.ID)

	got, err := lifecycle.Load(meta.ID)
	if err != nil {
		t.Fatalf("Load parent after web Stop: %v", err)
	}
	if got.State != session.LifecycleRunning {
		t.Fatalf("parent state after web Stop = %q, want running — ADR-093 test plan: web Stop on a delegated chat root does not cancel the record", got.State)
	}
	if got.Stop == nil || got.Stop.Generation != got.Generation {
		t.Fatalf("parent Stop after web Stop = %+v, generation %d — want a Stop marker for the current generation", got.Stop, got.Generation)
	}
	// The delegation itself must still be on record: this is the "has
	// delegated" row, not a Stop on a chat that never launched anything.
	records, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: meta.ID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("children of the chat after web Stop = %d, want 1 (the root had delegated)", len(records))
	}
}

// u2ScopeProvider replaces only the external model. Each captured context is
// the REAL live turn's provider request, not a mocked cancellation result.
type u2ScopeProvider struct {
	entered chan context.Context
	release chan struct{}
}

func (p *u2ScopeProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	select {
	case p.entered <- ctx:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled
	}
}

func (*u2ScopeProvider) GetDefaultModel() string { return "u2-scope-provider" }

type u2ScopeFixture struct {
	al            *agent.AgentLoop
	lifecycle     *session.LifecycleStore
	provider      *u2ScopeProvider
	reader        *wsHandlerReadLoop
	parent        string
	descendants   []string
	unrelated     string
	contexts      map[string]context.Context
	before        map[string]*session.LifecycleRecord
	joinScheduled []func()
	closeLoop     func()
}

func newU2ScopeFixture(t *testing.T) *u2ScopeFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "u2-scope-provider"}},
			List:     []config.AgentConfig{{ID: "mia", Home: home}},
		},
		// Three real workers: two direct children and one grandchild.
		Performance: config.PerformanceConfig{MaxParallelAgents: 3, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	// Reuse mustAgentLoop's isolation/membership setup, but own Close so the
	// goals test can join detached workers before reading their final writes.
	ensureTestWorkspaceMembership(t, cfg)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	p := &u2ScopeProvider{entered: make(chan context.Context, 8), release: make(chan struct{})}
	al, err := agent.NewAgentLoop(cfg, msgBus, p)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeLoop := func() { closeOnce.Do(al.Close) }
	t.Cleanup(closeLoop)
	t.Cleanup(func() { close(p.release) })
	// Mirror bootstrap before helpers use the real launcher and admission gate.
	boot := session.NewBootEpochStore(home)
	epoch, err := boot.Mint()
	require.NoError(t, err, "SETUP Mint genuine boot epoch")
	require.NotZero(t, epoch, "SETUP requires one genuine nonzero boot epoch")
	require.Equal(t, epoch, boot.Current(), "SETUP must wire the epoch minted by this store")
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	h := makeMinimalHandler()
	h.agentLoop, h.msgBus = al, msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "u2-scope-user"
	f := &u2ScopeFixture{
		al: al, lifecycle: lifecycle, provider: p, closeLoop: closeLoop,
		reader:   &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "u2-scope-chat"},
		contexts: make(map[string]context.Context), before: make(map[string]*session.LifecycleRecord),
	}
	f.parent = f.startRoot(t)
	child := f.startHelper(t, f.parent)
	grandchild := f.startHelper(t, child)
	sibling := f.startHelper(t, f.parent)
	f.descendants = []string{child, grandchild, sibling}
	f.unrelated = f.startRoot(t)
	for _, id := range append([]string{f.parent, f.unrelated}, f.descendants...) {
		f.before[id], err = lifecycle.Load(id)
		require.NoError(t, err, "snapshot session %s", id)
		require.Equal(t, session.LifecycleRunning, f.before[id].State)
		require.Nil(t, f.before[id].Stop, "fixture must start unstopped: %s", id)
		key := id // Dispatch registers a worker under its own session ID.
		if f.before[id].SteeredBy == nil {
			key = "agent:mia:session:" + id // ProcessScheduled's root key.
		}
		// GetActiveTurnHookForSession takes a routing ROOT, not a helper's
		// own ID. Query the exact registry key so every helper is checked.
		active := al.GetActiveTurnBySession(key)
		require.NotNil(t, active, "fixture must have a real registered turn: %s", id)
		require.Equal(t, key, active.SessionKey, "fixture must register the correct session: %s", id)
		require.Equal(t, agent.TurnPhaseRunning, active.Phase, "fixture turn must be running in the provider: %s", id)
		require.NoError(t, f.contexts[id].Err(), "fixture provider must not already be cancelled: %s", id)
	}
	return f
}

func (f *u2ScopeFixture) startRoot(t *testing.T) string {
	t.Helper()
	meta, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, f.al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(testHarnessWorkspaceMembershipID)}))
	require.NoError(t, f.lifecycle.Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: "mia", Origin: &session.Origin{Kind: session.OriginKindChat},
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = f.al.ProcessScheduled(ctx, "mia", meta.ID, "keep working until stopped", "webchat", meta.ID)
	}()
	join := func() {
		cancel()
		select {
		case <-done:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Errorf("scheduled fixture turn %s failed: %v", meta.ID, runErr)
			}
		case <-time.After(cancelTestTurnStartDeadline):
			t.Errorf("fixture turn %s did not finish during cleanup", meta.ID)
		}
	}
	t.Cleanup(join)
	f.joinScheduled = append(f.joinScheduled, join)
	contexts := f.contexts
	select {
	case providerCtx := <-f.provider.entered:
		contexts[meta.ID] = providerCtx
	case <-done:
		t.Fatalf("root fixture %s exited before entering the provider: %v", meta.ID, runErr)
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("root fixture never entered the provider")
	}
	return meta.ID
}

func (f *u2ScopeFixture) startHelper(t *testing.T, parent string) string {
	t.Helper()
	launcher := agent.NewSteerLauncher(f.al)
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent, TargetAgentID: "mia", Task: "keep helper working until stopped",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate},
	})
	require.NoError(t, err, "launch a real helper under %s", parent)
	dispatched, err := launcher.Dispatch(context.Background(), launched.SessionID, launched.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State, "fixture helper must actually run, not queue")
	contexts := f.contexts
	select {
	case providerCtx := <-f.provider.entered:
		contexts[launched.SessionID] = providerCtx
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatalf("helper fixture %s never entered the provider", launched.SessionID)
	}
	return launched.SessionID
}

func (f *u2ScopeFixture) cancelFrame(t *testing.T, scope *string) {
	t.Helper()
	data, err := json.Marshal(generated.CancelFrame{
		Type: string(generated.WsFrameTypeCancel), SessionId: f.parent, Scope: scope,
	})
	require.NoError(t, err)
	require.Equal(t, wsHandlerReadLoopNext,
		f.reader.dispatchFrame(data, wsTypeOnly{Type: string(generated.WsFrameTypeCancel)}),
		"exercise the production CancelFrame decoder/dispatcher, not handleCancel directly")
}

func (f *u2ScopeFixture) requireStopped(t *testing.T, id string) {
	t.Helper()
	require.Equal(t, context.Canceled, f.contexts[id].Err(), "tree must cancel the real live turn for %s", id)
	rec, err := f.lifecycle.Load(id)
	require.NoError(t, err)
	require.NotNil(t, rec.Stop, "tree must persist a Stop for %s", id)
	assert.Equal(t, 1, rec.Stop.Generation, "tree stops the current generation (fixture generation 1): %s", id)
	assert.Equal(t, session.PrincipalKindHuman, rec.Stop.By.Kind, "Stop must retain the human principal: %s", id)
	assert.Equal(t, "u2-scope-user", rec.Stop.By.ID, "Stop must retain the requesting user: %s", id)
}

func (f *u2ScopeFixture) assertUntouched(t *testing.T, id string) {
	t.Helper()
	assert.NoError(t, f.contexts[id].Err(), "scope must leave this live turn untouched: %s", id)
	rec, err := f.lifecycle.Load(id)
	require.NoError(t, err)
	assert.Equal(t, f.before[id], rec, "scope must leave the entire descendant/unrelated lifecycle record unchanged: %s", id)
}

// Task U2 RED requirement 1 and CancelFrame.scope's default: these are
// baseline guards ONLY if session isolation already holds. Requirement 2
// requires both direct children and a transitive grandchild under tree.
func TestU2CancelFrameScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scope   *string
		stopAll bool
	}{
		{name: "omitted_defaults_to_this_turn"},
		{name: "this_turn_leaves_descendants_running", scope: strPtr("session")},
		{name: "stop_all_stops_direct_and_transitive_descendants", scope: strPtr("tree"), stopAll: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newU2ScopeFixture(t)
			f.cancelFrame(t, tc.scope)
			require.Equal(t, context.Canceled, f.contexts[f.parent].Err(), "every scope must cancel the target session's turn")
			for _, id := range f.descendants {
				if tc.stopAll {
					f.requireStopped(t, id)
				} else {
					f.assertUntouched(t, id)
				}
			}
			if tc.stopAll {
				f.requireStopped(t, f.parent)
			}
			f.assertUntouched(t, f.unrelated)
		})
	}

	// Task U2 RED requirement 3, Q14=A: a tree ends turns, not goals.
	t.Run("stop_all_keeps_parent_and_every_helper_goal_unchanged", func(t *testing.T) {
		f := newU2ScopeFixture(t)
		gstore := goal.NewStore(config.OmnipusHomeDir())
		ids := append([]string{f.parent}, f.descendants...)
		before := make(map[string]*goal.Goal)
		for _, id := range ids {
			before[id] = f.seedActiveGoal(t, gstore, id)
		}
		f.cancelFrame(t, strPtr("tree"))
		for _, id := range ids {
			require.Equal(t, context.Canceled, f.contexts[id].Err(), "tree must cancel the real turn before checking goal preservation: %s", id)
		}
		f.assertUntouched(t, f.unrelated)

		// Inspect AFTER the turns' disposal, not just after the Stop stamp.
		// Otherwise an asynchronous goal-ending write could escape this guard.
		for _, join := range f.joinScheduled {
			join()
		}
		f.closeLoop()
		goalCases := []string{"parent_goal", "direct_helper_goal", "transitive_helper_goal", "sibling_helper_goal"}
		for i, id := range ids {
			t.Run(goalCases[i], func(t *testing.T) {
				after, err := gstore.Get(before[id].GoalID)
				require.NoError(t, err, "tree must not remove the goal for %s", id)
				assert.Equal(t, generated.GoalStateActive, after.State, "Q14=A: goal stays open for %s", id)
				assert.Equal(t, before[id], after, "Q14=A: the entire active goal must be unchanged for %s", id)
				rec, err := f.lifecycle.Load(id)
				require.NoError(t, err)
				assert.Equal(t, before[id].GoalID, rec.GoalRef, "tree must not unbind the goal for %s", id)
			})
		}
		// Keep all durable-stop assertions, but run them after the separate
		// goal cases so a lost Stop marker cannot hide the Q14 results.
		for _, id := range ids {
			f.requireStopped(t, id)
		}
	})
}

func (f *u2ScopeFixture) seedActiveGoal(t *testing.T, store *goal.Store, id string) *goal.Goal {
	t.Helper()
	criteria := []task.AcceptanceCriterion{{
		ID: "u2-report-ready", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
		Text: "The report is delivered", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u2-scope-user"},
	}}
	dod := []task.AcceptanceCriterion{{
		ID: "u2-report-safe", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
		Text: "No secrets are leaked", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "mia"},
	}}
	// One available round is fixture input, not an observed output oracle.
	g, err := goal.New(generated.GoalOwnerKindSession, id, generated.GoalSourceChatCompiled,
		"Deliver the report", "Deliver the report", criteria, dod, 1, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, g.Activate(id, time.Now().UTC()))
	require.NoError(t, store.Create(g))
	rec, err := f.lifecycle.Load(id)
	require.NoError(t, err)
	rec.GoalRef = g.GoalID
	require.NoError(t, f.lifecycle.Persist(rec))
	before, err := store.Get(g.GoalID)
	require.NoError(t, err)
	require.Equal(t, generated.GoalStateActive, before.State)
	require.Equal(t, id, before.ActiveSessionID)
	return before
}
