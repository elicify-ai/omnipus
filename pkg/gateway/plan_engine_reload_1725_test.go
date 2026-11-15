// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// DESIGN.md sections 4/6 (#1725, option 2a) require a two-second reload,
// not a shorter Stop drain or cancellation. The watchdog is only for fixture
// setup, eventual persistence, and joining failed-test cleanup.
const planReload1725Bound = 2 * time.Second
const planReload1725Watchdog = 10 * time.Second
const planReload1725Owner = "reload-plan-owner"
const planReload1725Reply = "The held plan reply finished normally across the settings reload."
const planReload1725Verdict = `{"met":true,"criteria":[{"id":"reload-dod","met":true,"reason":"The member supplied the requested result."}]}`

type planReload1725Call struct {
	ctx      context.Context
	entryErr error
	model    string
}

// Fake only the external model boundary. Engine coordination, Judge parsing,
// real turns, events, and disk persistence are never replaced.
type planReload1725Provider struct {
	reply       string
	entered     chan planReload1725Call
	gate        chan struct{}
	finished    chan struct{}
	releaseOnce sync.Once
	finishOnce  sync.Once
	calls       atomic.Int32
}

func newPlanReload1725Provider(reply string, held bool) *planReload1725Provider {
	p := &planReload1725Provider{
		reply: reply, entered: make(chan planReload1725Call, 1),
		gate: make(chan struct{}), finished: make(chan struct{}),
	}
	if !held {
		p.release()
	}
	return p
}

func (p *planReload1725Provider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	if p.calls.Add(1) == 1 {
		p.entered <- planReload1725Call{ctx: ctx, entryErr: ctx.Err(), model: model}
	}
	defer p.finishOnce.Do(func() { close(p.finished) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.gate:
		return &providers.LLMResponse{Content: p.reply, FinishReason: "stop"}, nil
	}
}

func (*planReload1725Provider) GetDefaultModel() string { return "test-model" }

func (p *planReload1725Provider) release() {
	p.releaseOnce.Do(func() { close(p.gate) })
}

func (p *planReload1725Provider) awaitEntry(t *testing.T) planReload1725Call {
	t.Helper()
	select {
	case call := <-p.entered:
		require.NoError(t, call.entryErr, "setup: the real turn must enter with a live context")
		require.Equal(t, "test-model", call.model, "setup: the configured agent model must reach the external boundary")
		return call
	case <-time.After(planReload1725Watchdog):
		t.Fatal("setup: real plan work never reached its model provider")
		return planReload1725Call{}
	}
}

type planReload1725Result struct {
	done    chan struct{}
	err     error
	elapsed time.Duration
}

type planReload1725Harness struct {
	home      string
	cfg       *config.Config
	al        *agent.AgentLoop
	rs        *services
	msgBus    *bus.MessageBus
	engine    *agent.PlanEngine
	owner     *planReload1725Provider
	judge     *planReload1725Provider
	events    agent.EventSubscription
	seen      []agent.Event
	reloading *planReload1725Result
}

// Follow the TK-30 production-path fixture: real boot and swap-time loader,
// localhost port zero, isolated stores, and a genuine non-reload teardown.
func newPlanReload1725Harness(t *testing.T, heldOwner, heldJudge bool) *planReload1725Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	path := filepath.Join(home, "config.json")
	seed := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Home: filepath.Join(home, "agents"), MaxTokens: 4096,
		}},
	}
	// A named fake model has no catalog row; use the supported explicit global
	// window setting, rather than bypassing the real turn-admission checks.
	window := 128000
	seed.Context.DefaultContextWindow = &window
	require.NoError(t, config.SaveConfig(path, seed))
	creds := newUnlockedStore(t, home)
	cfg, err := config.LoadConfigWithStore(path, creds)
	require.NoError(t, err)
	memoryOff := false
	cfg.Agents.List = []config.AgentConfig{
		{ID: planReload1725Owner, Name: "Reload Plan Owner", Type: config.AgentTypeCustom,
			Model: &config.AgentModelConfig{Primary: "test-model"}, MemoryEnabled: &memoryOff},
		{ID: string(coreagent.IDJudge), Name: "Judge", Type: config.AgentTypeSystem,
			Model: &config.AgentModelConfig{Primary: "test-model"}, MemoryEnabled: &memoryOff},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// agents.list is deliberately stripped on config load. Persist the roster
	// in its real entity store so the swap-time loader reconstructs these agents.
	roster := agentstore.New(home)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		require.NoError(t, roster.Create(ac.ID, &ac), "setup: persist reload roster")
	}
	rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, msgBus, home, creds, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
	require.NoError(t, err)
	h := &planReload1725Harness{
		home: home, cfg: cfg, al: al, rs: rs, msgBus: msgBus, engine: rs.PlanEngine,
		owner: newPlanReload1725Provider(planReload1725Reply, heldOwner),
		judge: newPlanReload1725Provider(planReload1725Verdict, heldJudge),
	}
	t.Cleanup(func() {
		// Release first, then join the ORIGINAL reload, then drain whole turns.
		// A provider's return alone does not cover its transcript/meta writers.
		h.owner.release()
		h.judge.release()
		if h.reloading != nil {
			waitPlanReload1725(t, h.reloading.done, "cleanup: original reload")
		}
		stopAndCleanupServices(rs, 5*time.Second, false)
		al.WaitForActiveRequests()
		al.UnsubscribeEvents(h.events.ID)
	})
	require.NotNil(t, h.engine, "BLOCKED: boot did not install the plan engine — required by #1725 DESIGN.md section 6")
	require.Same(t, h.engine, agent.GetPlanEngine(al), "setup: boot must wire the engine into the AgentLoop")
	rs.loadConfigForSwap = newReloadConfigLoader(path, home, creds, rs)
	h.installProviders(t)
	// Large enough for this fixture's few turns; no WebSocket clients exist.
	h.events = al.SubscribeEvents(512)
	require.NotZero(t, h.events.ID, "instrument: observer must be a real subscription")
	return h
}

func (h *planReload1725Harness) installProviders(t *testing.T) {
	t.Helper()
	for id, provider := range map[string]providers.LLMProvider{
		planReload1725Owner: h.owner, string(coreagent.IDJudge): h.judge,
	} {
		inst, ok := h.al.GetRegistry().GetAgent(id)
		require.True(t, ok, "setup: agent %q must resolve in the live registry", id)
		inst.Provider = provider
	}
}

func waitPlanReload1725(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(planReload1725Watchdog):
		t.Fatalf("%s did not finish within the setup/cleanup watchdog", what)
	}
}

func (h *planReload1725Harness) beginReload() *planReload1725Result {
	result := &planReload1725Result{done: make(chan struct{})}
	h.reloading = result
	go func() {
		defer close(result.done)
		provider := providers.LLMProvider(&restMockProvider{})
		start := time.Now()
		result.err = handleConfigReload(context.Background(), h.al, h.cfg, &provider, h.rs, h.msgBus, true)
		result.elapsed = time.Since(start)
	}()
	return result
}

func (h *planReload1725Harness) assertSameEngine(t *testing.T) {
	t.Helper()
	require.Same(t, h.engine, h.rs.PlanEngine, "reload must retain the exact services.PlanEngine pointer")
	require.Same(t, h.engine, agent.GetPlanEngine(h.al), "reload must retain the exact AgentLoop plan-engine pointer")
}

// Record the bound failure BEFORE releasing blocked work. On RED we still join
// the same reload and inspect the real persisted result; recovery never erases
// the failed latency assertion. Do not read reload-mutated fields before join.
func (h *planReload1725Harness) reloadWhileHeld(t *testing.T, p *planReload1725Provider, call planReload1725Call, kind string) {
	t.Helper()
	result := h.beginReload()
	timer := time.NewTimer(planReload1725Bound)
	defer timer.Stop()
	select {
	case <-result.done:
		assert.NoError(t, result.err, "reload must succeed while %s remains held", kind)
		assert.LessOrEqual(t, result.elapsed, planReload1725Bound, "reload exceeded the ruling's two-second bound")
		h.assertSameEngine(t)
		t.Logf("reload completed in %v while %s was held", result.elapsed, kind)
	case <-timer.C:
		t.Errorf("reload did not return within 2s while %s provider was held (#1725 reload stall)", kind)
	}
	assert.NoError(t, call.ctx.Err(), "a settings reload must NOT cancel the held %s context", kind)
	select {
	case <-p.finished:
		t.Errorf("held %s call returned before its explicit release", kind)
	default:
	}
	p.release()
	waitPlanReload1725(t, result.done, "original reload after releasing held work")
	require.NoError(t, result.err, "reload must eventually succeed too; cleanup release is not a timely-reload PASS")
	h.assertSameEngine(t)
}

func (h *planReload1725Harness) runningPlan(t *testing.T, id string) (*plan.Plan, *task.Task) {
	t.Helper()
	wsID, found := workspace.FindForAgent(h.home, planReload1725Owner)
	require.True(t, found, "setup: use the real owner's workspace, not an invented workspace id")
	p := &plan.Plan{
		ID: id, Title: "Reload survival", WorkspaceID: wsID,
		OwnerAgentID: planReload1725Owner, State: plan.StateRunning, PlanPhase: plan.PhaseDispatching,
		DoD: []task.AcceptanceCriterion{{
			ID: "reload-dod", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Text: "The member supplied the requested result.", Status: task.CritPending,
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "reload-test"}, ClauseCount: 1,
		}},
	}
	require.NoError(t, h.al.GetPlanStore().Create(p))
	member := &task.Task{
		Title: "Reload survival member", WorkspaceID: wsID, PlanID: p.ID,
		Status: task.StatusInProgress, Action: task.ActionLLM, AgentID: planReload1725Owner,
	}
	require.NoError(t, agent.GetTaskStore(h.al).Create(member))
	return p, member
}

func (h *planReload1725Harness) completeMember(t *testing.T, member *task.Task) {
	t.Helper()
	done := task.StatusDone
	_, err := agent.GetTaskStore(h.al).Update(member.ID, task.Patch{Status: &done})
	require.NoError(t, err)
	h.al.EmitTaskStatusChanged(agent.TaskStatusChangedPayload{
		TaskID: member.ID, Status: string(done), AgentID: planReload1725Owner,
	})
}

func (h *planReload1725Harness) awaitTurnEnd(t *testing.T, agentID, sessionID string) {
	t.Helper()
	timer := time.NewTimer(planReload1725Watchdog)
	defer timer.Stop()
	for {
		select {
		case evt, open := <-h.events.C:
			require.True(t, open, "the observer must survive reload")
			h.seen = append(h.seen, evt)
			if evt.Kind != agent.EventKindTurnEnd || evt.Meta.AgentID != agentID {
				continue
			}
			end, ok := evt.Payload.(agent.TurnEndPayload)
			require.True(t, ok, "instrument: typed turn-end payload must be present")
			if sessionID != "" && end.SessionID != sessionID {
				continue
			}
			require.Equal(t, agent.TurnEndStatusCompleted, end.Status, "the real turn must finish normally, not be cancelled or fail")
			return
		case <-timer.C:
			t.Fatalf("real turn for agent %q/session %q did not finish normally", agentID, sessionID)
		}
	}
}

func (h *planReload1725Harness) assertAppliedVerdict(t *testing.T, planID string) {
	t.Helper()
	// Wait for the real Judge result application, not merely the provider's
	// return or the verifier turn's completion (both precede this store write).
	require.Eventually(t, func() bool {
		p, err := h.al.GetPlanStore().Get(planID)
		return err == nil && p.State == plan.StateDone
	}, planReload1725Watchdog, time.Millisecond, "the released Judge's real PASS must complete the right plan")
	p, err := h.al.GetPlanStore().Get(planID)
	require.NoError(t, err)
	assert.Equal(t, plan.PhaseSynthesizing, p.PlanPhase, "real PASS follows the normal synthesis handoff")
	assert.Equal(t, 1, p.JudgeRounds, "reload must neither lose nor duplicate the consumed verdict")
	require.Len(t, p.DoD, 1)
	assert.Equal(t, "reload-dod", p.DoD[0].ID)
	assert.Equal(t, task.CritMet, p.DoD[0].Status, "real per-criterion verdict must be persisted, not only StateDone")
	assert.Equal(t, "", p.PausedReason, "reload must not leave a judge-unavailable pause")
	assert.Equal(t, "", p.HandoverText, "reload must not record an Unavailable handover")
	assert.Equal(t, int32(1), h.judge.calls.Load(), "exactly one real Judge provider call must decide this plan")
	h.awaitTurnEnd(t, string(coreagent.IDJudge), "")
	// Flush completed work's queued events so a transient pause cannot be hidden
	// by the round's normal final pause cleanup.
	for {
		select {
		case evt := <-h.events.C:
			h.seen = append(h.seen, evt)
		default:
			h.assertNoJudgeUnavailable(t, planID)
			return
		}
	}
}

func (h *planReload1725Harness) assertNoJudgeUnavailable(t *testing.T, planID string) {
	t.Helper()
	judgingSeen := false
	for _, evt := range h.seen {
		if evt.Kind != agent.EventKindPlanStatusChanged {
			continue
		}
		status, ok := evt.Payload.(agent.PlanStatusChangedPayload)
		require.True(t, ok, "instrument: typed plan-status payload must be present")
		if status.PlanID != planID {
			continue
		}
		assert.Equal(t, "", status.PausedReason, "no transient judge-unavailable pause may be stamped")
		if status.PlanPhase == string(plan.PhaseJudging) {
			judgingSeen = true
		}
		if judgingSeen {
			assert.Contains(t, []string{string(plan.PhaseJudging), string(plan.PhaseSynthesizing)}, status.PlanPhase,
				"a live Judge must not revert to dispatching/Unavailable or stall during reload")
		}
	}
	require.True(t, judgingSeen, "instrument: must observe this plan actually enter judging")
	assert.Zero(t, h.al.EventDrops(agent.EventKindPlanStatusChanged), "event assertions cannot hide a dropped pause")
}

func TestPlanEngine_ReloadKeepsLiveEngine_WakeTurnSurvives(t *testing.T) {
	h := newPlanReload1725Harness(t, true, false)
	p, _ := h.runningPlan(t, "reload-wake-1725")
	stopped, err := h.engine.StopPlan(context.Background(), p.ID, "reload-test", "cli")
	require.NoError(t, err)
	require.Equal(t, plan.StateFailed, stopped.State, "setup: real StopPlan must dispatch the origin-less owner wake")
	call := h.owner.awaitEntry(t)
	current, err := h.al.GetPlanStore().Get(p.ID)
	require.NoError(t, err)
	require.NotEmpty(t, current.OwnerSessionID, "setup: wake must own a durable plan session")

	h.reloadWhileHeld(t, h.owner, call, "plan wake turn")
	h.awaitTurnEnd(t, planReload1725Owner, current.OwnerSessionID)
	store := h.al.ResolveSessionStore(current.OwnerSessionID)
	require.NotNil(t, store, "the same plan session must still resolve after reload")
	entries, err := store.ReadTranscript(current.OwnerSessionID)
	require.NoError(t, err)
	var replies []string
	for _, entry := range entries {
		if entry.Role == "assistant" {
			replies = append(replies, entry.Content)
			assert.Equal(t, planReload1725Owner, entry.AgentID, "reply must be written by the real owner")
			assert.Equal(t, "", entry.ErrorCode, "the persisted reply must not be an error")
			assert.NotEqual(t, "interrupted", entry.Status, "reload must not persist an interrupted reply")
		}
	}
	assert.Equal(t, []string{planReload1725Reply}, replies, "exactly one intact held reply must land in the plan transcript")
	assert.Equal(t, int32(1), h.owner.calls.Load(), "reload must not replay the owner turn")
	t.Log("released owner turn completed and its exact reply persisted in the plan session")
}

func TestPlanEngine_ReloadKeepsLiveEngine_JudgeRoundSurvives(t *testing.T) {
	h := newPlanReload1725Harness(t, false, true)
	p, member := h.runningPlan(t, "reload-judge-1725")
	h.completeMember(t, member)
	call := h.judge.awaitEntry(t)
	before, err := h.al.GetPlanStore().Get(p.ID)
	require.NoError(t, err)
	require.Equal(t, plan.PhaseJudging, before.PlanPhase, "setup: hold a real admitted Judge round")
	require.Equal(t, 0, before.JudgeRounds, "setup: a held call has not consumed a verdict")
	require.Equal(t, "", before.PausedReason, "setup: no pre-existing judge-unavailable pause")

	h.reloadWhileHeld(t, h.judge, call, "plan Judge round")
	h.assertAppliedVerdict(t, p.ID)
	t.Log("released Judge round applied one real PASS without an Unavailable transition or pause")
}

func TestPlanEngine_ReloadKeepsLiveEngine_StillReacts(t *testing.T) {
	h := newPlanReload1725Harness(t, false, false)
	result := h.beginReload()
	waitPlanReload1725(t, result.done, "idle configuration reload")
	require.NoError(t, result.err)
	h.assertSameEngine(t)

	// Public monotonic IDs expose extra subscriptions even when the Judge's CAS
	// guard masks duplicate consumers. There are no WebSocket connections, and
	// no other production subscriber is created by this reload fixture.
	probe := h.al.SubscribeEvents(1)
	h.al.UnsubscribeEvents(probe.ID)
	require.NotZero(t, probe.ID, "instrument: allocate a real post-reload probe")
	assert.Equal(t, h.events.ID+1, probe.ID, "reload must keep the live subscription, not restart or double-subscribe the plan engine")

	// Reload replaces the registry, not the external-provider test boundary.
	// Reinstall only those model doubles, before creating any post-reload work.
	h.installProviders(t)
	p, member := h.runningPlan(t, "reload-reactive-1725")
	h.completeMember(t, member)
	h.judge.awaitEntry(t)
	h.assertAppliedVerdict(t, p.ID)
	t.Log("post-reload member status event drove one real Judge round and persisted its verdict; no direct Tick")
}
