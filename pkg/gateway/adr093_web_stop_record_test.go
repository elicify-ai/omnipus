// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 test plan, MAJ-001, as amended by ADR-20260928 D2/D9: web Stop on a
// chat root that has delegated and has no turn of its own in flight lands the
// root `stopped` (fence cleared, StopNote kept, never terminal) and leaves its
// delegated child untouched.
//
// Oracle: ADR-20260928 D2 table row "working, running waiting on descendants
// -> stopped" and D9 (a single Stop is session-scoped, no cascade).

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

// TestAdr093WebStop_DelegatedChatLandsStoppedAndChildIsUntouched drives the
// web Stop button (handleCancel, session scope) on a chat root that already
// has a child and has no turn of its own in flight (it is only waiting on its
// descendants).
//
// Oracle (ADR-20260928 D2 table, "working, running waiting on descendants"):
// the Stop lands the root `stopped`; the landing clears the in-flight fence and
// keeps the reason note in the same mutation; it is never terminal. D9 / D2
// scope: a single Stop reaches only the named session, so the delegated child
// is not touched. (Superseded: the earlier ADR-093 test-plan reading that the
// root stays `running` with a current fence; D2 replaced it.)
func TestAdr093WebStop_DelegatedChatLandsStoppedAndChildIsUntouched(t *testing.T) {
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
	launched, launchErr := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID,
		TargetAgentID:     "mia",
		Task:              "Prepare the spreadsheet",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	if launchErr != nil {
		t.Fatalf("delegate from the chat (the root must already have a child): %v", launchErr)
	}
	childBefore, err := lifecycle.Load(launched.SessionID)
	if err != nil {
		t.Fatalf("Load child before the Stop: %v", err)
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
	if got.State != session.LifecycleStopped {
		t.Fatalf("parent state after web Stop = %q, want stopped — D2: a running session waiting on descendants lands stopped", got.State)
	}
	if got.Terminal() {
		t.Fatalf("parent is terminal after web Stop (state %q) — a stop is never terminal", got.State)
	}
	if got.Stop != nil {
		t.Fatalf("parent still carries an in-flight fence %+v after landing stopped — D2: the landing clears the fence in the same mutation that keeps the note", got.Stop)
	}
	if got.StopNote == nil {
		t.Fatal("parent landed stopped with no StopNote — D2/CRIT-001: the note is kept on every stopped landing")
	}
	if got.StopNote.Cause != session.StopCauseStop || got.StopNote.By != session.StopActorHumanUser("user-adr093") {
		t.Fatalf("parent StopNote = %+v, want cause %q by %q (the human who pressed Stop)", got.StopNote, session.StopCauseStop, session.StopActorHumanUser("user-adr093"))
	}
	childAfter, err := lifecycle.Load(launched.SessionID)
	if err != nil {
		t.Fatalf("Load child after the Stop: %v", err)
	}
	if childAfter.State != childBefore.State || childAfter.Stop != nil || childAfter.StopNote != nil {
		t.Fatalf("child changed by the root's single Stop: before=%q after=%+v — D9: a single Stop never cascades", childBefore.State, childAfter)
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
	// Frozen sub-agent control-plane ADR D2 CRIT-001: a LANDED stopped
	// record spends its active fence and retains its original reason note.
	// Observe that published settlement, never stop unrelated work to get it.
	var rec *session.LifecycleRecord
	var loadErr error
	require.Eventually(t, func() bool {
		rec, loadErr = f.lifecycle.Load(id)
		if loadErr != nil || rec.State != session.LifecycleStopped || rec.Stop != nil || rec.StopNote == nil {
			return false
		}
		history, err := f.lifecycle.ListStoppedTransitions(id)
		if err != nil || len(history) != 1 {
			return false
		}
		key := id
		if f.before[id].SteeredBy == nil {
			key = "agent:mia:session:" + id
		}
		return f.al.GetActiveTurnBySession(key) == nil
	}, cancelTestTurnStartDeadline, 10*time.Millisecond, "actual selected turn must finish owned retirement and publish its landed history: %s", id)
	require.NoError(t, loadErr)
	assert.Equal(t, session.LifecycleStopped, rec.State, "tree must land stopped, not failed: %s", id)
	assert.False(t, rec.Terminal(), "stopped is resumable/nonterminal: %s", id)
	assert.Nil(t, rec.Stop, "landed stop must clear the active fence: %s", id)
	require.NotNil(t, rec.StopNote, "landed stop must retain its original reason: %s", id)
	assert.Equal(t, 1, rec.Generation, "tree stops the original fixture generation: %s", id)
	assert.Equal(t, "human:u2-scope-user", rec.StopNote.By, "lasting note retains the actual requesting human: %s", id)
	cause := session.StopCauseCascade
	if id == f.parent {
		cause = session.StopCauseStop
	}
	assert.Equal(t, cause, rec.StopNote.Cause, "target/descendant stop cause remains scoped: %s", id)
	assert.NotZero(t, rec.StopNote.Seq, "landed note retains a real stop sequence: %s", id)
	assert.False(t, rec.StopNote.At.IsZero(), "landed note retains the stop instant: %s", id)
	require.NotNil(t, f.before[id].ExecutionID, "fixture must have an original actual admission: %s", id)
	assert.Equal(t, f.before[id].ExecutionID, rec.ExecutionID, "landing retains original selected execution: %s", id)
	assert.Nil(t, rec.FinalDelivery, "Stop must not manufacture a terminal outbox: %s", id)
	effects, err := f.lifecycle.AcceptedStopEffects(id)
	require.NoError(t, err)
	require.Len(t, effects, 1, "one original selected control must remain historical: %s", id)
	require.NotNil(t, rec.StopEffect, "original accepted effect must remain: %s", id)
	assert.Equal(t, effects[0], *rec.StopEffect, "landing must not accept a new control: %s", id)
	assert.Equal(t, 1, effects[0].Target.Generation, "accepted control keeps original generation: %s", id)
	assert.Equal(t, f.before[id].ExecutionID.RunID, effects[0].Target.RunID, "accepted control keeps original run: %s", id)
	assert.Equal(t, f.before[id].ExecutionID.BootSeq, effects[0].Target.BootSeq, "accepted control keeps original admitting boot: %s", id)
	landed, err := f.lifecycle.ListStoppedTransitions(id)
	require.NoError(t, err)
	require.Len(t, landed, 1, "one selected stop landing must be recorded: %s", id)
	assert.Equal(t, effects[0].ControlID, landed[0].ControlID, "history belongs to the original control: %s", id)
	assert.Equal(t, landed[0].StopSeq, rec.StopNote.Seq, "lasting note belongs to the original control sequence: %s", id)
	assert.Equal(t, rec.StopNote.By, landed[0].Actor, "history retains requesting human: %s", id)
	assert.Equal(t, cause, landed[0].Cause, "history retains scoped cause: %s", id)
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
