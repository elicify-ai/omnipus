// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_engine.go implements PlanEngine, the single hybrid server-side plan
// coordinator (ADR-049 D4, spec Part B "Plan engine" US-7, FR-058..066,
// SD-B4/B5/B8/B9, R1, R5). It is deliberately placed in pkg/agent rather than
// pkg/plan: pkg/plan may NOT import pkg/agent (see plan.go's package doc —
// pkg/plan is a one-way leaf dependency of pkg/task only), but the engine
// needs pkg/agent's JudgeCriteria (judge.go), TaskExecutor.ExecuteTask
// (task_executor.go), and AsyncNotifier (async_notifier.go) — so it lives
// here instead, importing pkg/plan and pkg/task.
//
// Design summary:
//
//   - Mechanical work (dispatch, DAG-advance, admission, idle sweep) runs
//     server-side with NO owner involvement (D4 hybrid, FR-058).
//   - The owner agent is woken (async-notifier) ONLY at three decision
//     points (FR-059): a plan-judge round is UNMET (steering forwarded), the
//     plan-judge PASSES (synthesis requested), or a terminal brake fires
//     (rounds exhausted / idle expiry) — this mirrors task_executor.go's own
//     attempts-exhausted wake (SD-B9) at the plan level.
//   - The plan-level judge (SD-B8) is the SAME seeded Judge System Agent as
//     the task-level judge, invoked via the SAME JudgeCriteria entrypoint
//     with Scope=task.VerdictScopePlan — no second seeded agent, no parallel
//     adjudication path.
//   - Global admission (R5) co-locates with this singleton: Admit/Release are
//     the single-writer authority Wave 2-C's /goal and /loop commands call
//     into, computed FRESH from persisted state on every call (running plans
//     scanned directly; /goal and /loop counted via registered
//     ActiveCounterFunc callbacks Wave 2-C supplies) rather than an
//     incrementing/decrementing counter — this avoids the drift R5 warns
//     against.
//   - Every plan-mutating decision (dispatch, judge-round start, idle-expiry)
//     is serialized through one process-wide planDecisionMu (see its doc
//     comment) so the periodic Tick and the reactive task_status_changed
//     event handler never race on the same plan.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Test seams --------------------------------------------------------

// PlanEngineClock is the time source used for idle-expiry math (mirrors
// pkg/cron's Clock interface exactly, but is defined locally rather than
// imported so this package incurs no new dependency on pkg/cron — the real
// gateway-owned cron.CronService is Wave 2-C's concern, not this engine's).
// Production uses realPlanEngineClock (time.Now); tests inject a fake so
// idle-expiry boundary tests run with zero wall-clock sleeps.
type PlanEngineClock interface {
	Now() time.Time
}

type realPlanEngineClock struct{}

func (realPlanEngineClock) Now() time.Time { return time.Now() }

// planJudge is the narrow interface *AgentLoop.JudgeCriteria satisfies. A
// dedicated interface (rather than a concrete *AgentLoop field) lets tests
// inject a fake judge and assert the met/unmet/rounds-exhausted/unavailable
// paths deterministically without booting a real LLM provider or seeded
// Judge System Agent.
type planJudge interface {
	JudgeCriteria(ctx context.Context, in JudgeCriteriaInput) JudgeCriteriaResult
}

// planTaskDispatcher is the narrow interface *TaskExecutor.ExecuteTask
// satisfies, for the same reason as planJudge above.
type planTaskDispatcher interface {
	ExecuteTask(ctx context.Context, taskID string) error
}

// sessionCanceller is the narrow interface *AgentLoop.RequestCancelForSession
// (cancel.go) satisfies (mirrors planJudge/planTaskDispatcher's own
// narrow-interface test-seam pattern, ADR-052 §6.4/§6.9 "Stop = the existing
// chat cancel"). A dedicated interface — rather than a concrete *AgentLoop
// field — lets tests inject a fake and assert the Stop fan-out's session set
// deterministically (US-6/US-7, DS-4) without booting a real AgentLoop/
// session store.
type sessionCanceller interface {
	RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (bool, error)
}

// ActiveCounterFunc reports the current count of active units of some
// loop-shaped kind the plan store cannot itself enumerate (R5's counted
// set): a /goal session count or an enabled /loop job count. Wave 2-C
// registers one of these per kind via RegisterActiveCounter once its own
// session/cron wiring exists; until registered, that kind contributes 0 to
// the global cap (documented boot-ordering requirement — see this file's
// final wave report).
type ActiveCounterFunc func() (int, error)

const (
	// defaultPlanEngineTickInterval is the production ticker cadence — the
	// approved→running auto-tick (R1/O2), the dispatch/judge-trigger safety
	// net, and the idle-expiry sweep all run on this cadence in addition to
	// the reactive task_status_changed handler.
	defaultPlanEngineTickInterval = 30 * time.Second

	// defaultPlanJudgeConcurrency bounds how many plan-level judge rounds may
	// run concurrently across all plans — the "cron-style lane cap" this
	// wave's brief calls for (mirrors pkg/cron's maxConcurrentRuns/laneSem
	// pattern, service.go). Task-level judge calls need no separate cap: they
	// already run inside TaskExecutor's own per-task goroutine, itself
	// bounded by TaskExecutor's dispatchSema.
	defaultPlanJudgeConcurrency = 4

	// planJudgeRoundTimeout bounds one plan-level judge round's total wall
	// time. It is deliberately decoupled from the tick cycle (the round runs
	// in its own goroutine — see beginPlanJudgeRound) so a slow-but-alive
	// judge call is never truncated by a short tick-scoped deadline; a judge
	// that is genuinely stuck past this bound surfaces as Unavailable (0
	// rounds consumed, D7) and is retried on the next tick/event.
	planJudgeRoundTimeout = 10 * time.Minute

	// planEngineStopDrainTimeout bounds how long Stop() waits for in-flight
	// judge-round goroutines before proceeding (mirrors
	// cron.CronService.Stop's bounded drain, service.go ~L385-432).
	planEngineStopDrainTimeout = 30 * time.Second

	// pausedReasonOwnerDisabled is the PausedReason value set by
	// PausePlansOwnedBy (FR-065).
	pausedReasonOwnerDisabled = "owner_disabled"
)

// PlanEngine is the single hybrid coordinator instance (ADR-049 D4). Exactly
// one instance should run per gateway process — enforced by the
// single-instance overlap guard on Tick (FR-063), not by this type itself
// (a second construction is harmless; a second concurrent Start's ticker
// would simply have its Tick calls no-op against the first's overlap guard
// IF they shared a mutex, which they do not across two instances — the
// gateway boot path MUST only construct one instance; see gateway.go's
// setupAndStartServices, the sole construction site of agent.NewPlanEngine.
// Start is also called by restartServices (gateway.go) on that SAME
// instance, on every reload, after stopAndCleanupServices Stop()'d it — see
// restartServices' own comment on why the engine is not reconstructed there).
type PlanEngine struct {
	agentLoop  *AgentLoop
	planStore  *plan.Store
	taskStore  *task.Store
	dispatcher planTaskDispatcher
	judge      planJudge
	notifier   AsyncNotifier
	// canceller issues the actual session-level cancel for the Stop fan-out
	// (US-6/US-7, ADR-052 §6.4/§6.9/§6.10). Set to al itself in
	// NewPlanEngine (which satisfies sessionCanceller trivially); nil in a
	// bare struct-literal test engine unless the test injects a fake —
	// cancelSessions nil-guards it the same way notifier/dispatcher calls do
	// elsewhere in this file.
	canceller sessionCanceller

	clock        PlanEngineClock
	tickInterval time.Duration

	// mu guards ticking, activeCounters, and verifierRegistry's lazy
	// initialization — all small, fast-to-touch bookkeeping. It is NEVER
	// held across a store call or the judge LLM call itself.
	mu             sync.Mutex
	ticking        bool
	activeCounters map[string]ActiveCounterFunc
	// verifierRegistry replaces the old inFlightJudge map[string]bool
	// (ADR-052 FR-037/R3-7 — see verifier_registry.go's package doc for the
	// full design). Accessed exclusively via the registry() accessor, which
	// lazily initializes it under mu — never read/written directly — so a
	// struct-literal-constructed test engine that omits this field never
	// nil-derefs.
	verifierRegistry VerifierSessionRegistry

	// planDecisionMu serializes every plan-mutating decision (dispatch,
	// judge-round start, idle-expiry) process-wide. It is coarse (one lock
	// for all plans, not per-plan) — a deliberate simplicity trade-off: the
	// two producers of a decision (Tick's periodic pass and the reactive
	// task_status_changed event handler) run on at most two goroutines, and
	// Plan counts in this product are expected to be small, so a single mutex
	// avoids the complexity of a striped/per-plan lock without a measurable
	// throughput cost. Held only for the synchronous portion of a decision
	// (never across the judge LLM call, which runs in its own goroutine after
	// the lock is released — see beginPlanJudgeRound).
	planDecisionMu sync.Mutex

	judgeSema *DispatchSemaphore
	judgeWG   sync.WaitGroup

	subID     uint64
	stopCh    chan struct{}
	stoppedWG sync.WaitGroup
	started   bool
}

// NewPlanEngine constructs the production PlanEngine. al must be non-nil in
// production (it supplies JudgeCriteria, config, the event bus, and the
// async-notifier); tests construct a *PlanEngine struct literal directly
// (same package) with fake judge/dispatcher/notifier/clock fields instead of
// calling this constructor. taskExecutor satisfies planTaskDispatcher; al
// satisfies planJudge; al.asyncNotifier satisfies AsyncNotifier.
func NewPlanEngine(al *AgentLoop, planStore *plan.Store, taskStore *task.Store, taskExecutor *TaskExecutor) *PlanEngine {
	pe := &PlanEngine{
		agentLoop:        al,
		planStore:        planStore,
		taskStore:        taskStore,
		dispatcher:       taskExecutor,
		judge:            al,
		clock:            realPlanEngineClock{},
		tickInterval:     defaultPlanEngineTickInterval,
		activeCounters:   make(map[string]ActiveCounterFunc),
		verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema:        newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	if al != nil {
		pe.notifier = al.asyncNotifier
		pe.canceller = al
	}
	return pe
}

// registry returns the verifier-session registry, lazily initializing it
// under mu on first use. This lets a bare struct-literal PlanEngine (the
// same-package test-construction pattern this file's tests use throughout —
// see plan_engine_test.go's newTestPlanEngine) omit verifierRegistry without
// risking a nil-map panic, exactly as NewPlanEngine's own construction does
// explicitly.
func (pe *PlanEngine) registry() VerifierSessionRegistry {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.verifierRegistry == nil {
		pe.verifierRegistry = NewVerifierSessionRegistry()
	}
	return pe.verifierRegistry
}

// VerifierRegistry exposes the engine's single verifier-session registry
// (FR-037/R3-7) to the adjudication callers that need to register their own
// verifier session BEFORE dispatch — judge.go's runVerifierAdjudication (all
// three JudgeCriteria scopes: task, plan, chat `/goal`) reaches it via
// GetPlanEngine(al).VerifierRegistry(). See verifier_registry.go's package
// doc for the full registration contract.
func (pe *PlanEngine) VerifierRegistry() VerifierSessionRegistry {
	return pe.registry()
}

// --- Lifecycle -----------------------------------------------------------

// Start runs boot reconciliation synchronously (FR-061/062/D4/MAJ-004 — task
// and plan statuses are authoritative, events are only an optimization; an
// already in_progress task is never blindly re-dispatched, a plan stuck at
// plan_phase=judging from a crash mid-round resumes its round), then
// launches the background tick loop and — when agentLoop is non-nil — the
// reactive task_status_changed event consumer. Safe to call once; a second
// call on an already-started engine is a no-op.
func (pe *PlanEngine) Start(ctx context.Context) error {
	pe.mu.Lock()
	if pe.started {
		pe.mu.Unlock()
		return nil
	}
	if pe.planStore == nil || pe.taskStore == nil || pe.dispatcher == nil || pe.judge == nil {
		pe.mu.Unlock()
		return fmt.Errorf("plan_engine: Start: planStore/taskStore/dispatcher/judge must all be set")
	}
	pe.started = true
	pe.stopCh = make(chan struct{})
	var sub EventSubscription
	if pe.agentLoop != nil {
		sub = pe.agentLoop.SubscribeEvents(64)
		pe.subID = sub.ID
	}
	pe.mu.Unlock()

	pe.bootReconcile(ctx)

	pe.stoppedWG.Add(1)
	go pe.runTickLoop()
	if sub.C != nil {
		pe.stoppedWG.Add(1)
		go pe.runEventLoop(sub.C)
	}
	return nil
}

// Stop signals both background goroutines to exit, unsubscribes from the
// event bus, waits for them, then bounds its wait on any still-in-flight
// judge-round goroutine (planEngineStopDrainTimeout) rather than blocking
// shutdown forever on a stuck judge call.
func (pe *PlanEngine) Stop() {
	pe.mu.Lock()
	if !pe.started {
		pe.mu.Unlock()
		return
	}
	pe.started = false
	close(pe.stopCh)
	subID := pe.subID
	pe.mu.Unlock()

	if pe.agentLoop != nil && subID != 0 {
		pe.agentLoop.UnsubscribeEvents(subID)
	}

	pe.stoppedWG.Wait()

	done := make(chan struct{})
	go func() {
		pe.judgeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(planEngineStopDrainTimeout):
		logger.WarnCF("plan_engine",
			"stop: in-flight plan-judge round(s) still tearing down after drain timeout", nil)
	}
}

func (pe *PlanEngine) runTickLoop() {
	defer pe.stoppedWG.Done()
	ticker := time.NewTicker(pe.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-pe.stopCh:
			return
		case <-ticker.C:
			pe.Tick(context.Background())
		}
	}
}

func (pe *PlanEngine) runEventLoop(ch <-chan Event) {
	defer pe.stoppedWG.Done()
	for {
		select {
		case <-pe.stopCh:
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if evt.Kind != EventKindTaskStatusChanged {
				continue
			}
			payload, ok := evt.Payload.(TaskStatusChangedPayload)
			if !ok || payload.TaskID == "" {
				continue
			}
			t, err := pe.taskStore.Get(payload.TaskID)
			if err != nil || t.PlanID == "" {
				continue
			}
			pe.processPlan(context.Background(), t.PlanID)
		}
	}
}

// --- Tick (single-instance overlap guard + one full pass) ----------------

// Tick performs one full engine pass: admit approved→running plans (R1/O2),
// dispatch+judge-check every running plan, then sweep for idle expiry
// (FR-064). Guarded by a cron-style single-instance overlap guard (FR-063,
// mirrors CronJobState.Running, pkg/cron/service.go:140) — an overlapping
// call (a slow previous tick still running when the ticker fires again) is a
// silent no-op, never a second concurrent pass. Exported so tests can drive
// it directly and deterministically (mirrors cron.RunDueJobs).
func (pe *PlanEngine) Tick(ctx context.Context) {
	if !pe.claimTick() {
		logger.DebugCF("plan_engine", "tick skipped: previous tick still running (overlap guard)", nil)
		return
	}
	defer pe.releaseTick()

	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.WarnCF("plan_engine", "tick: list plans failed", map[string]any{"error": err.Error()})
		return
	}
	for i := range plans {
		p := &plans[i]
		switch p.State {
		case plan.StateApproved:
			pe.tryStartApprovedPlan(ctx, p.ID)
		case plan.StateRunning:
			pe.processPlan(ctx, p.ID)
		}
	}
	pe.idleExpirySweep()
	pe.goalAndLoopIdleExpirySweep()
}

func (pe *PlanEngine) claimTick() bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.ticking {
		return false
	}
	pe.ticking = true
	return true
}

func (pe *PlanEngine) releaseTick() {
	pe.mu.Lock()
	pe.ticking = false
	pe.mu.Unlock()
}

// --- approved -> running auto-tick (R1/O2) --------------------------------

// tryStartApprovedPlan admits planID (via Admit, R5) and, if the global cap
// has room, transitions it approved→running and dispatches its initial
// ready wave. If the cap is full the plan is left `approved` (a legitimate
// cap-waiting state, O2 r3) and retried on the next tick.
func (pe *PlanEngine) tryStartApprovedPlan(ctx context.Context, planID string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		if !errors.Is(err, plan.ErrNotFound) {
			logger.WarnCF("plan_engine", "tryStartApprovedPlan: get plan failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
		}
		return
	}
	if p.State != plan.StateApproved {
		return // vanished or already moved on by another path
	}

	ok, active, loopCap := pe.Admit("plan")
	if !ok {
		logger.InfoCF("plan_engine", "approved plan is cap-waiting",
			map[string]any{"plan_id": planID, "active": active, "cap": loopCap})
		return
	}

	running := plan.StateRunning
	updated, err := pe.planStore.Update(planID, plan.Patch{State: &running})
	if err != nil {
		logger.WarnCF("plan_engine", "approved->running transition failed",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}
	logger.InfoCF("plan_engine", "plan admitted: approved->running", map[string]any{"plan_id": planID})

	tasks, lerr := pe.taskStore.List(task.Filter{PlanID: updated.ID})
	if lerr != nil {
		logger.WarnCF("plan_engine", "could not list member tasks after plan start",
			map[string]any{"plan_id": planID, "error": lerr.Error()})
		return
	}
	pe.dispatchReadyMembers(ctx, updated.ID, tasks)
	pe.touchActivity(updated.ID)
}

// --- Per-plan dispatch + judge-trigger pass -------------------------------

// processPlan is the core per-plan evaluation+action pass for one RUNNING
// plan, used by both Tick (for every running plan, each pass) and the
// reactive task_status_changed handler (for the specific plan a changed
// task belongs to). It is idempotent and safe to call redundantly.
func (pe *PlanEngine) processPlan(ctx context.Context, planID string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		if !errors.Is(err, plan.ErrNotFound) {
			logger.WarnCF("plan_engine", "processPlan: get plan failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
		}
		return
	}
	if p.State != plan.StateRunning {
		return
	}
	if p.PausedReason != "" {
		return // FR-065: a paused plan neither dispatches nor judges
	}

	switch p.EffectivePlanPhase() {
	case plan.PhaseJudging:
		_, inFlight := pe.registry().Lookup(p.ID)
		if inFlight {
			return // a goroutine is already adjudicating this round
		}
		// plan_phase=judging with no in-flight goroutine in THIS process can
		// only mean a prior process died mid-round (FR-062 boot case) — no
		// round was actually consumed (JudgeCriteria's own "0 rounds on
		// Unavailable/crash" contract), so resuming from scratch is safe.
		logger.InfoCF("plan_engine", "resuming plan judge round interrupted by a prior crash/restart",
			map[string]any{"plan_id": p.ID})
		pe.beginPlanJudgeRound(p)
		return
	case plan.PhaseSynthesizing:
		return // terminal hand-off already in progress
	}

	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		logger.WarnCF("plan_engine", "processPlan: list member tasks failed",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}

	if pe.promoteReadyMembers(tasks) {
		tasks, err = pe.taskStore.List(task.Filter{PlanID: planID})
		if err != nil {
			logger.WarnCF("plan_engine", "processPlan: re-list member tasks after promotion failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
			return
		}
	}

	dispatched := pe.dispatchReadyMembers(ctx, planID, tasks)
	if dispatched {
		pe.touchActivity(planID)
	}

	// FR-041/R2-04 (US-7 acceptance 3, DS-4, "member-cancel plan outcome"):
	// checked BEFORE allMembersTerminal, and takes priority over it. A
	// member left `blocked` behind a cancelled member is never terminal
	// (AdvanceBlockedDependents only ever promotes a blocked dependent on a
	// DONE dependency — blocked_by.go — never on a cancelled/failed one), so
	// allMembersTerminal would silently stay false forever and the plan
	// would otherwise rot until FR-064's (days-later, UNrestartable)
	// idle-expiry brake — exactly the outcome this rule exists to prevent.
	if planStuckAfterMemberCancel(tasks) {
		pe.failPlanLocked(p.ID, plan.FailedReasonStoppedByUser, buildMemberCancelHandover(p, tasks))
		return
	}

	if allMembersTerminal(tasks) {
		pe.beginPlanJudgeRound(p)
	}
}

// promoteReadyMembers re-runs AdvanceBlockedDependents (pkg/task/blocked_by.go)
// for every DONE member task's ID. task_executor.go's onTaskComplete already
// triggers this reactively on every individual task completion (it is not
// plan-scoped — it fires for any task, member or not); this is a defensive
// re-scan (FR-062's boot-reconciliation requirement: "blocked tasks whose
// deps are all done are advanced") catching any edge the reactive path might
// have missed, e.g. a process crash between a dependency completing and its
// dependent's promotion. AdvanceBlockedDependents is idempotent: a dependent
// already promoted, or not yet fully satisfied, is a no-op.
func (pe *PlanEngine) promoteReadyMembers(tasks []task.Task) (advancedAny bool) {
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusDone {
			continue
		}
		advanced, err := pe.taskStore.AdvanceBlockedDependents(t.ID)
		if err != nil {
			logger.WarnCF("plan_engine", "advance blocked dependents failed",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
		if len(advanced) > 0 {
			advancedAny = true
		}
	}
	return advancedAny
}

// dispatchReadyMembers dispatches every member task currently `next`
// (FR-058). A task in `next` should always have every blocked_by dependency
// satisfied already (the store's own recompute keeps that invariant), but
// ExecuteTask re-verifies it independently regardless (task_executor.go) —
// dispatchReadyMembers does not duplicate that check. ExecuteTask is itself
// bounded by TaskExecutor's own global dispatch semaphore; a transient
// ErrDispatchCapReached (or any other dispatch error) is logged and simply
// retried on the next tick/event, never treated as fatal to the plan.
func (pe *PlanEngine) dispatchReadyMembers(ctx context.Context, planID string, tasks []task.Task) (dispatchedAny bool) {
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusNext {
			continue
		}
		if err := pe.dispatcher.ExecuteTask(ctx, t.ID); err != nil {
			logger.WarnCF("plan_engine", "member task dispatch failed (will retry)",
				map[string]any{"plan_id": planID, "task_id": t.ID, "error": err.Error()})
			continue
		}
		dispatchedAny = true
	}
	return dispatchedAny
}

func allMembersTerminal(tasks []task.Task) bool {
	for i := range tasks {
		if !task.IsTerminal(tasks[i].Status) {
			return false
		}
	}
	return true
}

// --- Plan-level judge round (SD-B8) ---------------------------------------

// beginPlanJudgeRound starts (or, per the boot/crash-resume case above,
// restarts) one plan-level judge round for p. Caller must hold
// planDecisionMu. Checks the round ceiling first (fails the plan closed if
// exhausted); otherwise claims a judgeSema lane (skips this tick — retried
// next pass — if the lane is full) and launches the actual judge call in its
// own goroutine, decoupled from the tick/event cycle (planJudgeRoundTimeout)
// so a slow-but-alive judge call never blocks dispatch of other plans.
func (pe *PlanEngine) beginPlanJudgeRound(p *plan.Plan) {
	cfg := pe.planningConfig()
	var boundsOverride *int
	if p.Bounds != nil {
		boundsOverride = p.Bounds.PlanJudgeMaxRounds
	}
	maxRounds := cfg.EffectivePlanJudgeMaxRounds(boundsOverride)
	if p.JudgeRounds >= maxRounds {
		pe.failPlanLocked(p.ID, plan.FailedReasonJudgeRoundsExhausted, buildPlanRoundsExhaustedHandover(p, maxRounds))
		return
	}

	acquired, release := pe.judgeSema.TryAcquire()
	if !acquired {
		logger.DebugCF("plan_engine", "plan judge lane full; retrying next tick", map[string]any{"plan_id": p.ID})
		return
	}

	judging := plan.PhaseJudging
	if _, err := pe.planStore.Update(p.ID, plan.Patch{PlanPhase: &judging}); err != nil {
		release()
		logger.WarnCF("plan_engine", "could not set plan_phase=judging",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}

	// Register the round as in-flight BEFORE launching the goroutine —
	// exactly the same synchronous timing the old inFlightJudge[p.ID]=true
	// write had (FR-037 extends this "assign before dispatch" rule to
	// verifiers generally; here it doubles as the plan-round's own
	// crash-resume liveness marker — see verifier_registry.go's package
	// doc). The verifier session id is not known yet at this point (judge.go
	// creates it once the round actually runs JudgeCriteria), so this call
	// registers an empty-session placeholder; judge.go upserts the real id
	// via the SAME Register call before dispatching the verifier's turn.
	pe.registry().Register(p.ID, "")

	pe.judgeWG.Add(1)
	go pe.runPlanJudgeRound(p.ID, release)
}

// runPlanJudgeRound performs ONE plan-level judge round. It ALWAYS clears
// its judgeSema slot, verifier-registry entry, and judgeWG count on return
// (defers below), regardless of outcome.
func (pe *PlanEngine) runPlanJudgeRound(planID string, release func()) {
	defer pe.judgeWG.Done()
	defer release()
	defer pe.registry().Unregister(planID)

	ctx, cancel := context.WithTimeout(context.Background(), planJudgeRoundTimeout)
	defer cancel()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		logger.WarnCF("plan_engine", "judge round: could not reload plan",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}

	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		logger.WarnCF("plan_engine", "judge round: could not list member tasks",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}

	criteria := p.DoD
	if len(criteria) == 0 {
		if soft := SoftTierCriterion(p.Title, p.Description, p.Goal); soft != nil {
			criteria = []task.AcceptanceCriterion{*soft}
		}
	}
	if len(criteria) == 0 {
		// SD-A7 soft tier: title/description/goal all empty too — nothing to
		// judge at all. Trust completion directly rather than looping forever.
		pe.completePlan(p)
		return
	}

	result := pe.judge.JudgeCriteria(ctx, JudgeCriteriaInput{
		Scope:           task.VerdictScopePlan,
		PlanID:          p.ID,
		AssigneeAgentID: p.OwnerAgentID,
		Criteria:        criteria,
		Attempt:         p.JudgeRounds + 1,
		ClaimText:       buildPlanClaimText(tasks),
		ExtraContext:    buildPlanJudgeExtraContext(p),
	})

	// FR-014 (US-6 acceptance 3, Test 7): JudgeCriteria runs OUTSIDE
	// planDecisionMu by design (this whole goroutine is decoupled from the
	// lock precisely so a slow-but-alive judge call never blocks other
	// plans' dispatch — see beginPlanJudgeRound's doc comment), so a Stop
	// can land on this exact plan microseconds before the call returns.
	// Re-acquire the lock and re-check State==running before applying ANY
	// outcome below (Unavailable-revert, a real verdict, or the wakeOwner
	// side effects either path triggers) — a plan a concurrent Stop already
	// moved to `failed` must never have that outcome overwritten by a stale
	// in-flight round.
	if !pe.verdictStillApplicable(planID) {
		logger.InfoCF("plan_engine",
			"plan judge round outcome dropped: plan left running during adjudication (Stop landed concurrently)",
			map[string]any{"plan_id": planID})
		return
	}

	if result.Unavailable {
		// D7: 0 rounds consumed, idle clock NOT bumped (spec R9/m4: judge
		// unavailability pauses do not reset the idle clock). Revert
		// plan_phase to dispatching so the plan is picked up for a fresh
		// round attempt on the next tick/event rather than staying stuck at
		// "judging" with no goroutine watching it (only reached once ctx's
		// OWN timeout fires — JudgeCriteria itself retries forever otherwise,
		// respecting ctx).
		dispatching := plan.PhaseDispatching
		if _, uerr := pe.planStore.Update(p.ID, plan.Patch{PlanPhase: &dispatching}); uerr != nil {
			logger.WarnCF("plan_engine", "judge round: could not revert plan_phase after unavailability",
				map[string]any{"plan_id": p.ID, "error": uerr.Error()})
		}
		logger.WarnCF("plan_engine", "plan judge round abandoned (judge unavailable)",
			map[string]any{"plan_id": p.ID, "reason": result.Reason})
		return
	}

	verdict := result.Verdict
	newRounds := p.JudgeRounds + 1
	pe.touchActivity(p.ID)

	if verdict.Met {
		pe.synthesizeAndComplete(p, newRounds)
		return
	}

	steering := buildPlanSteeringText(verdict)
	dispatching := plan.PhaseDispatching
	if _, uerr := pe.planStore.Update(p.ID, plan.Patch{
		JudgeRounds:  &newRounds,
		PlanPhase:    &dispatching,
		HandoverText: &steering,
	}); uerr != nil {
		logger.ErrorCF("plan_engine", "judge round: could not persist unmet verdict",
			map[string]any{"plan_id": p.ID, "error": uerr.Error()})
		return
	}
	pe.wakeOwner(p.ID, p.OwnerAgentID, fmt.Sprintf(
		"Plan %q round %d: the plan judge found the Definition of Done UNMET.\n\n%s",
		p.Title, newRounds, steering,
	), "plan_judge_unmet")
}

// synthesizeAndComplete records the PASS round (or, for completePlan's
// nothing-to-judge case, leaves JudgeRounds unchanged), sets
// plan_phase=synthesizing, wakes the owner to write the closing synthesis,
// then transitions the plan terminal Done — "plan judge PASS" IS the
// definition of Done (plan.go's own StateDone doc comment); the engine does
// not block waiting for the owner's synthesis reply (which is an ordinary,
// possibly-never-answered chat turn) before finalizing the mechanical
// outcome. plan_phase is deliberately left at "synthesizing" after Done as a
// historical marker of how the plan finished.
func (pe *PlanEngine) synthesizeAndComplete(p *plan.Plan, newRounds int) {
	synthesizing := plan.PhaseSynthesizing
	if _, err := pe.planStore.Update(p.ID, plan.Patch{
		JudgeRounds: &newRounds,
		PlanPhase:   &synthesizing,
	}); err != nil {
		logger.ErrorCF("plan_engine", "could not set plan_phase=synthesizing",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}
	pe.wakeOwner(p.ID, p.OwnerAgentID, fmt.Sprintf(
		"Plan %q: the plan judge confirmed the Definition of Done is MET. "+
			"Please write a closing synthesis summarizing the outcome for the requester.",
		p.Title,
	), "plan_judge_met")

	done := plan.StateDone
	if _, err := pe.planStore.Update(p.ID, plan.Patch{State: &done}); err != nil {
		logger.ErrorCF("plan_engine", "could not transition plan to done after judge PASS",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
	}
}

// completePlan handles the SD-A7 soft-tier-empty case (no DoD, no
// title/description/goal text worth judging at all): nothing to adjudicate,
// so the plan is trusted complete directly, mirroring
// TaskExecutor.adjudicateClaim's identical "structurally empty, trust it"
// branch.
func (pe *PlanEngine) completePlan(p *plan.Plan) {
	pe.touchActivity(p.ID)
	pe.synthesizeAndComplete(p, p.JudgeRounds)
}

// failPlanLocked transitions planID to State=failed with reason and handover
// (SD-B9), then wakes the owner. Caller must hold planDecisionMu.
func (pe *PlanEngine) failPlanLocked(planID string, reason plan.FailedReason, handover string) {
	failed := plan.StateFailed
	updated, err := pe.planStore.Update(planID, plan.Patch{
		State:        &failed,
		FailedReason: &reason,
		HandoverText: &handover,
	})
	if err != nil {
		logger.ErrorCF("plan_engine", "could not transition plan to failed",
			map[string]any{"plan_id": planID, "reason": string(reason), "error": err.Error()})
		return
	}
	pe.wakeOwner(planID, updated.OwnerAgentID, fmt.Sprintf(
		"Plan %q has ended (%s).\n\n%s", updated.Title, reason, handover,
	), "plan_"+string(reason))
}

// verdictStillApplicable re-checks, under planDecisionMu, that planID is
// still State==running (FR-014/US-6 acceptance 3, Test 7) — see
// runPlanJudgeRound's call site for why this exists.
func (pe *PlanEngine) verdictStillApplicable(planID string) bool {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()
	p, err := pe.planStore.Get(planID)
	if err != nil {
		return false
	}
	return p.State == plan.StateRunning
}

// --- Stop (US-6/US-7, FR-009/010/013/025/029/037/041) ---------------------
//
// Stop reuses the SAME chat cancel primitive (RequestCancelForSession, A2)
// as every other cancel surface — no new cancel machinery. The ENTIRE
// fan-out (session cancels + the plan/member state writes) runs under
// planDecisionMu (FR-009, grill F3) so a member or verifier concurrently
// being dispatched cannot escape: M1 (task_executor.go) guarantees a
// member's SessionID is assigned+persisted BEFORE it ever leaves `next`, and
// FR-037 (verifier_registry.go) guarantees a verifier's session id is
// registered BEFORE its own turn is dispatched — both synchronously, so the
// snapshot this code takes while holding the lock is always complete.

// memberCancelReasonMarker prefixes Task.Result on a member/standalone task
// PlanEngine.StopPlan/StopTask cancels (US-8's orange "Cancelled" marker;
// FR-041's dead-end detection below). WAVE-MERGE: replace with
// task.CancelReason once that field lands (owned by a sibling wave) — this
// Result-text marker is the interim discriminator between a genuine
// judge/attempt-exhaustion failure and a user cancel, since pkg/task in this
// worktree does not yet carry a dedicated cancel-reason field.
const memberCancelReasonMarker = "[reason:stopped_by_user]"

// isCancelledMember reports whether t was terminated by a user Stop (as
// opposed to a genuine judge/attempt-exhaustion failure) — see
// memberCancelReasonMarker.
func isCancelledMember(t *task.Task) bool {
	return t.Status == task.StatusFailed && strings.HasPrefix(t.Result, memberCancelReasonMarker)
}

// StopPlan implements US-6: the plan-level Stop fan-out. Cancels {every
// `in_progress` member's worker session} + {every REGISTERED verifier
// session for the plan itself and every one of its members, via the
// verifier registry}, marks every `in_progress` member `failed`+cancel-
// marker (US-8), and — unconditionally, regardless of whether other members
// could still independently progress (that conditional case is FR-041,
// member-level Stop only) — transitions the plan itself to
// `failed`(stopped_by_user). Returns the updated plan on success.
func (pe *PlanEngine) StopPlan(ctx context.Context, planID, userID, channel string) (*plan.Plan, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: StopPlan: get plan %q: %w", planID, err)
	}
	if p.State != plan.StateRunning {
		return nil, fmt.Errorf("plan_engine: StopPlan: plan %q is %s, not running", planID, p.State)
	}

	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return nil, fmt.Errorf("plan_engine: StopPlan: list member tasks: %w", err)
	}

	// Canonical fan-out set (GS-04, FR-009): {each in_progress member
	// session} + {each registered verifier session for the plan AND every
	// member — regardless of that member's OWN status, since a plan-level
	// verifier session is registered under the plan's own unit (planID) and
	// a member's verifier session is only ever registered while that member
	// is itself in_progress (adjudication runs before the member's own
	// terminal write — see task_executor.go's finishTaskRun), so scanning
	// every member id costs nothing and misses nothing}.
	units := make([]string, 0, len(tasks)+1)
	units = append(units, planID)
	var sessions []string
	for i := range tasks {
		t := &tasks[i]
		units = append(units, t.ID)
		if t.Status == task.StatusInProgress && t.SessionID != "" {
			sessions = append(sessions, t.SessionID)
		}
	}
	sessions = append(sessions, pe.registry().SessionsFor(units...)...)
	pe.cancelSessions(ctx, sessions, userID, channel)

	for i := range tasks {
		t := &tasks[i]
		if t.Status == task.StatusInProgress {
			pe.cancelMemberLocked(t.ID, userID)
		}
	}

	failed := plan.StateFailed
	reason := plan.FailedReasonStoppedByUser
	handover := fmt.Sprintf("Plan %q was stopped by %s.", p.Title, userID)
	updated, err := pe.planStore.Update(planID, plan.Patch{
		State:        &failed,
		FailedReason: &reason,
		HandoverText: &handover,
	})
	if err != nil {
		return nil, fmt.Errorf("plan_engine: StopPlan: transition plan %q to failed: %w", planID, err)
	}
	pe.wakeOwner(planID, updated.OwnerAgentID, handover, "plan_stopped_by_user")
	return updated, nil
}

// StopTask implements US-7: Stop for a standalone `in_progress` task OR a
// SINGLE `in_progress` in-plan member (member-Stop, A5) — distinct from
// StopPlan (US-6/FR-025). Cancels the task's own worker session AND its
// registered verifier session (if adjudication is in flight) and marks the
// task `failed`+cancel-marker. Deliberately does NOT touch the task's plan
// (if any): the plan's other independent members keep running (FR-025).
// FR-041 (the "no further progress possible" immediate plan-fail) is
// evaluated by the engine's own dispatch loop (processPlan) on ITS next
// reactive pass, triggered by the EmitTaskStatusChanged this call fires
// below — never inline here, since StopTask already holds planDecisionMu
// and processPlan acquiring it too would deadlock (planDecisionMu is not
// reentrant). Returns the updated task on success.
func (pe *PlanEngine) StopTask(ctx context.Context, taskID, userID, channel string) (*task.Task, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	t, err := pe.taskStore.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: StopTask: get task %q: %w", taskID, err)
	}
	if t.Status != task.StatusInProgress {
		return nil, fmt.Errorf("plan_engine: StopTask: task %q is %s, not in_progress", taskID, t.Status)
	}

	var sessions []string
	if t.SessionID != "" {
		sessions = append(sessions, t.SessionID)
	}
	sessions = append(sessions, pe.registry().SessionsFor(taskID)...)
	pe.cancelSessions(ctx, sessions, userID, channel)

	return pe.cancelMemberLocked(taskID, userID)
}

// cancelSessions issues RequestCancelForSession (the SAME chat cancel every
// other surface uses, A2) for every session in sessions (deduped inline by
// the caller's use of registry.SessionsFor + the direct worker-session
// append, both of which already avoid duplicates in practice, but a
// belt-and-suspenders local dedupe costs nothing). A single session's cancel
// failure is logged, never escalated — the engine's OWN state transition
// (member/plan -> failed) must still be attempted regardless (US-6
// acceptance 1: the plan is marked `cancelled` even if one session's cancel
// call errored — the session-level cancel and the state transition are
// independent guarantees, not a single atomic unit).
func (pe *PlanEngine) cancelSessions(ctx context.Context, sessions []string, userID, channel string) {
	if pe.canceller == nil {
		if len(sessions) > 0 {
			logger.WarnCF("plan_engine",
				"stop fan-out: no sessionCanceller configured; session-level cancel skipped",
				map[string]any{"session_count": len(sessions)})
		}
		return
	}
	seen := make(map[string]bool, len(sessions))
	for _, sessionID := range sessions {
		if sessionID == "" || seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		if _, err := pe.canceller.RequestCancelForSession(ctx, sessionID, userID, channel); err != nil {
			logger.WarnCF("plan_engine", "stop fan-out: session cancel failed",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
	}
}

// cancelMemberLocked marks task taskID `failed` with the user-cancel marker
// (US-8; WAVE-MERGE: task.CancelReason) and emits task_status_changed so
// PlanEngine's own reactive event loop re-evaluates the owning plan (FR-041)
// on its next pass. Caller must hold planDecisionMu (both StopPlan and
// StopTask call this while holding it). A store-write failure is returned
// to the caller (StopTask propagates it; StopPlan logs and continues the
// fan-out for the plan's other members — a single member's write failure
// must not abort cancelling the rest).
func (pe *PlanEngine) cancelMemberLocked(taskID, userID string) (*task.Task, error) {
	failed := task.StatusFailed
	result := fmt.Sprintf("%s Cancelled by %s via Stop.", memberCancelReasonMarker, userID)
	now := time.Now().UTC().Format(time.RFC3339)
	updated, err := pe.taskStore.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &result,
		CompletedAt: &now,
	})
	if err != nil {
		logger.WarnCF("plan_engine", "stop: could not mark member task cancelled",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return nil, fmt.Errorf("plan_engine: cancel task %q: %w", taskID, err)
	}
	if pe.agentLoop != nil {
		sessionID := updated.SessionID
		if sessionID == "" {
			sessionID = "task:" + updated.ID
		}
		pe.agentLoop.EmitTaskStatusChanged(TaskStatusChangedPayload{
			TaskID:    updated.ID,
			Status:    string(updated.Status),
			SessionID: sessionID,
			AgentID:   updated.AgentID,
		})
	}
	return updated, nil
}

// planStuckAfterMemberCancel implements FR-041 (US-7 acceptance 3, DS-4,
// R2-04): "no further progress possible" — at least one member was
// user-cancelled AND every remaining non-`done` member is either terminal
// (done is excluded by the caller check, so this means `failed`, any
// reason — cancelled or a genuine judge/attempt-exhaustion failure) or
// `blocked` with its ENTIRE blocked_by chain (directly or transitively,
// within this plan's own member set) bottoming out exclusively in terminal
// members. Grounded: task.Store.AdvanceBlockedDependents only ever promotes
// a blocked dependent on a DONE dependency (blocked_by.go) — never on a
// cancelled/failed one — so without this check the plan would silently rot
// until FR-064's idle-expiry brake (days later, and idle_expired is NOT
// restartable, breaking the "re-run via plan restart" promise this rule
// exists to preserve).
func planStuckAfterMemberCancel(tasks []task.Task) bool {
	anyCancelled := false
	for i := range tasks {
		if isCancelledMember(&tasks[i]) {
			anyCancelled = true
			break
		}
	}
	if !anyCancelled {
		return false
	}

	byID := make(map[string]*task.Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	for i := range tasks {
		t := &tasks[i]
		if t.Status == task.StatusDone {
			continue
		}
		if !memberIsDeadEnd(t, byID, make(map[string]bool)) {
			return false // at least one non-done member can still make progress
		}
	}
	return true
}

// memberIsDeadEnd reports whether t can never reach `done` without an
// operator restart: t itself is terminal (`failed`, any reason), or t is
// `blocked` and EVERY one of its blocked_by dependencies (within this
// plan's member set, byID) is itself a dead end. visiting guards a chain
// re-visit within one top-level DFS (defensive, not load-bearing: the
// store's own write-time DAG validator, pkg/task/blocked_by.go, already
// rejects cycles — this scans a possibly-stale in-memory snapshot, not the
// live store, so the guard costs nothing and removes any doubt).
func memberIsDeadEnd(t *task.Task, byID map[string]*task.Task, visiting map[string]bool) bool {
	if visiting[t.ID] {
		return true
	}
	visiting[t.ID] = true
	if task.IsTerminal(t.Status) {
		return true // done was already excluded by the caller; this means failed
	}
	if t.Status != task.StatusBlocked || len(t.BlockedBy) == 0 {
		return false // next/in_progress/inbox, or blocked with no listed blocker: a live path forward exists
	}
	for _, depID := range t.BlockedBy {
		dep, ok := byID[depID]
		if !ok {
			// The blocker is outside this plan's member set (or was deleted) —
			// cannot be confirmed a dead end; fail safe (that dependency may
			// still resolve independently, so progress may still be possible).
			return false
		}
		if dep.Status == task.StatusDone {
			continue // this blocker is satisfied — not a reason t is stuck
		}
		if !memberIsDeadEnd(dep, byID, visiting) {
			return false
		}
	}
	return true
}

// buildMemberCancelHandover renders the graceful wind-down summary written
// when FR-041 fires — mirrors buildPlanRoundsExhaustedHandover's shape at
// the sibling idle/judge-exhaustion terminal brakes.
func buildMemberCancelHandover(p *plan.Plan, tasks []task.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan %q cannot make further progress: one or more member tasks were "+
		"cancelled by a user Stop, and every remaining member is either finished or blocked "+
		"exclusively behind a cancelled member.\n\nMember outcomes:\n", p.Title)
	for i := range tasks {
		t := &tasks[i]
		fmt.Fprintf(&sb, "- %s (%s)", t.Title, t.Status)
		if isCancelledMember(t) {
			sb.WriteString(" [cancelled]")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nRestart the plan (Play) to re-run the non-done members.")
	return sb.String()
}

// --- Idle-expiry sweep (FR-064) -------------------------------------------

// idleExpirySweep expires any running plan idle for longer than its
// effective IdleExpiryDays bound (FR-064/FR-9). "idle" = no attempt, state
// transition, or user interaction (touchActivity is the sole writer of
// LastActivityAt below the plan's own store-level stamping on the
// approved->running transition). Judge-unavailability pauses deliberately do
// NOT bump LastActivityAt (R9/m4), so a permanently-unavailable judge ends
// the loop via this calendar brake, never a fabricated verdict.
func (pe *PlanEngine) idleExpirySweep() {
	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.WarnCF("plan_engine", "idle sweep: list plans failed", map[string]any{"error": err.Error()})
		return
	}
	cfg := pe.planningConfig()
	now := pe.clock.Now()
	for i := range plans {
		if plans[i].State != plan.StateRunning {
			continue
		}
		id := plans[i].ID
		pe.planDecisionMu.Lock()
		pe.idleExpireOneLocked(id, cfg, now)
		pe.planDecisionMu.Unlock()
	}
}

// idleExpireOneLocked re-reads planID under planDecisionMu (the list snapshot
// above may be stale by the time the lock is acquired) and expires it if
// idle past its bound. Caller must hold planDecisionMu.
func (pe *PlanEngine) idleExpireOneLocked(planID string, cfg config.PlanningConfig, now time.Time) {
	p, err := pe.planStore.Get(planID)
	if err != nil || p.State != plan.StateRunning {
		return
	}
	var override *int
	if p.Bounds != nil {
		override = p.Bounds.IdleExpiryDays
	}
	maxDays := cfg.EffectiveIdleExpiryDays(override)
	last := effectiveLastActivity(p)
	if last.IsZero() {
		return // nothing to compare against; skip rather than guess
	}
	if now.Sub(last) < time.Duration(maxDays)*24*time.Hour {
		return
	}
	handover := fmt.Sprintf(
		"Plan %q idle-expired after %d day(s) with no activity (last activity: %s).",
		p.Title, maxDays, last.Format(time.RFC3339),
	)
	pe.failPlanLocked(p.ID, plan.FailedReasonIdleExpired, handover)
}

// goalAndLoopIdleExpirySweep drives the /goal and /loop idle-expiry calendar
// brakes (FR-064/D7, review r1 blocker) on the SAME tick cadence as the plan
// sweep above — one periodic driver for all three loop-shaped entity kinds
// rather than a second ticker. No-op when agentLoop is nil (a bare-struct-
// literal test PlanEngine that only exercises the plan-level sweep, or boot
// ordering before the engine is wired to a real AgentLoop) — mirrors this
// file's existing `if pe.agentLoop != nil` guard convention (see Start).
func (pe *PlanEngine) goalAndLoopIdleExpirySweep() {
	if pe.agentLoop == nil {
		return
	}
	cfg := pe.planningConfig()
	now := pe.clock.Now()
	pe.agentLoop.goalIdleExpirySweep(cfg, now)
	if ls := pe.agentLoop.loopScheduler(); ls != nil {
		ls.IdleExpirySweep(cfg, now)
	}
}

func effectiveLastActivity(p *plan.Plan) time.Time {
	for _, s := range []string{p.LastActivityAt, p.StartedAt, p.CreatedAt} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// touchActivity bumps LastActivityAt to "now" (per pe.clock) — see
// idleExpirySweep's doc comment for why this matters. Best-effort: a write
// failure here only delays idle-expiry, never blocks forward progress, so it
// is logged at Warn and swallowed.
func (pe *PlanEngine) touchActivity(planID string) {
	now := pe.clock.Now().UTC().Format(time.RFC3339)
	if _, err := pe.planStore.Update(planID, plan.Patch{LastActivityAt: &now}); err != nil {
		logger.WarnCF("plan_engine", "could not bump plan LastActivityAt",
			map[string]any{"plan_id": planID, "error": err.Error()})
	}
}

// --- Owner wake (SD-B9, async-notifier) -----------------------------------

// wakeOwner delivers content to ownerAgentID via the async-notifier, exactly
// mirroring task_executor.go's wakeOwnerAttemptsExhausted pattern at the
// plan level: Channel="system", ChatID="plan:<id>" (Plan has no dedicated
// SessionID field the way Task does — there is nothing to route to short of
// this synthetic per-plan destination). Best-effort: a notify failure is
// logged, never escalated (the mechanical plan-state transition it
// accompanies has already been persisted regardless).
func (pe *PlanEngine) wakeOwner(planID, ownerAgentID, content, sourceKind string) {
	if pe.notifier == nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pe.notifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:    "system",
		ChatID:     "plan:" + planID,
		AgentID:    ownerAgentID,
		SourceKind: sourceKind,
		Content:    content,
	}); err != nil {
		logger.WarnCF("plan_engine", "could not wake plan owner",
			map[string]any{"plan_id": planID, "error": err.Error()})
	}
}

// --- Text builders ---------------------------------------------------------

func buildPlanClaimText(tasks []task.Task) string {
	if len(tasks) == 0 {
		return "(this plan has no member tasks)"
	}
	var sb strings.Builder
	sb.WriteString("Member task outcomes:\n")
	for i := range tasks {
		t := &tasks[i]
		fmt.Fprintf(&sb, "- %s (%s): %s\n", t.Title, t.Status, truncateForClaim(t.Result))
	}
	return sb.String()
}

const planClaimTruncateLimit = 500

func truncateForClaim(s string) string {
	if len(s) <= planClaimTruncateLimit {
		return s
	}
	return s[:planClaimTruncateLimit] + "..."
}

func buildPlanJudgeExtraContext(p *plan.Plan) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Plan: %s\n", p.Title)
	if p.Goal != "" {
		fmt.Fprintf(&sb, "\nGoal: %s\n", p.Goal)
	}
	if p.Description != "" {
		fmt.Fprintf(&sb, "\nDescription: %s\n", p.Description)
	}
	return sb.String()
}

func buildPlanSteeringText(v *task.JudgeVerdict) string {
	var sb strings.Builder
	sb.WriteString("The plan judge reviewed this round and found the Definition of Done UNMET:\n")
	for _, c := range v.PerCriterion {
		if !c.Met {
			fmt.Fprintf(&sb, "- criterion %s: %s\n", c.CriterionID, c.Reason)
		}
	}
	return sb.String()
}

func buildPlanRoundsExhaustedHandover(p *plan.Plan, maxRounds int) string {
	return fmt.Sprintf(
		"Plan judge exhausted after %d round(s) (max %d) without a PASS verdict.\n\n"+
			"Last steering:\n%s\n\n"+
			"Review the plan's member tasks and judge history; the plan has been marked "+
			"failed and its owner notified.",
		p.JudgeRounds, maxRounds, p.HandoverText,
	)
}

// --- Global active-loop cap authority (R5) --------------------------------

// Admit is the single-writer authority for R5's global active-loop cap: it
// computes the current active count FRESH from persisted state (running
// plans scanned directly from pe.planStore; /goal and /loop counted via
// registered ActiveCounterFunc callbacks — see RegisterActiveCounter) and
// reports whether one more unit of kind may start. kind is "plan", "goal", or
// "loop" — informational only (the cap is global across all three, not
// per-kind); Wave 2-C's /goal and /loop admission paths call this exact
// method before starting a new loop, and this engine calls it itself (as
// "plan") before promoting an approved plan to running.
func (pe *PlanEngine) Admit(kind string) (ok bool, active, cap int) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.admitLocked(kind)
}

func (pe *PlanEngine) admitLocked(kind string) (ok bool, active, capOut int) {
	active, reliable := pe.computeActiveLocked()
	capOut = pe.resolveGlobalCap()
	if !reliable {
		// Fail CLOSED (review r1 silent-failure MEDIUM 3): the active count
		// could not be computed reliably (a List/counter error below means
		// `active` is a partial, possibly UNDER-count) — admitting on an
		// unreliable read risks silently blowing past R5's global brake.
		// Deny rather than risk it; the next Admit call (the caller always
		// retries on its own cadence — /goal and /loop admission checks run
		// per-command, the plan engine's own tryStartApprovedPlan runs every
		// tick) gets a fresh chance once the underlying fault clears.
		logger.WarnCF("plan_engine",
			"admission check: active count unreliable (list/counter error) — denying (fail-closed)",
			map[string]any{"kind": kind, "active": active, "cap": capOut})
		return false, active, capOut
	}
	ok = active < capOut
	logger.DebugCF("plan_engine", "admission check",
		map[string]any{"kind": kind, "active": active, "cap": capOut, "admitted": ok})
	return ok, active, capOut
}

// computeActiveLocked sums running plans (scanned directly) plus every
// registered ActiveCounterFunc's current count. Caller must hold pe.mu.
//
// reliable is false when the plan-store List call OR ANY registered
// ActiveCounterFunc call errored (review r1 silent-failure MEDIUM 3): the
// returned count is then a partial, possibly UNDER-count that admitLocked
// must never trust to admit past the cap — the pre-fix behavior counted a
// failed source as 0 and happily admitted on the (silently wrong) remainder,
// which could blow past R5's global active-loop brake during exactly the
// kind of storage fault the cap exists to be resilient against.
func (pe *PlanEngine) computeActiveLocked() (count int, reliable bool) {
	reliable = true
	runningPlans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.WarnCF("plan_engine", "admission: list plans failed",
			map[string]any{"error": err.Error()})
		reliable = false
	} else {
		for i := range runningPlans {
			if runningPlans[i].State == plan.StateRunning {
				count++
			}
		}
	}
	for kind, fn := range pe.activeCounters {
		n, err := fn()
		if err != nil {
			logger.WarnCF("plan_engine", "admission: active counter failed",
				map[string]any{"kind": kind, "error": err.Error()})
			reliable = false
			continue
		}
		count += n
	}
	return count, reliable
}

func (pe *PlanEngine) resolveGlobalCap() int {
	c := pe.planningConfig()
	if c.GlobalActiveLoopCap >= 1 {
		return c.GlobalActiveLoopCap
	}
	return config.DefaultGlobalActiveLoopCap
}

// Release is the paired call to Admit for callers that started a unit of
// kind and later ended it. It is presently an advisory no-op: R5's counted
// set is deliberately computed FRESH from persisted state on every Admit
// call (never from an incrementing/decrementing counter — that is exactly
// the drift R5 warns against), so there is no counter here to decrement. The
// active count naturally drops once the caller's own persisted state change
// (plan State leaving running, the /goal session clearing, the /loop job
// being disabled/removed) is visible to the next Admit call. Release exists
// as an explicit, symmetric call so Wave 2-C's admission call sites have a
// paired release to make (and so this authority can grow a cache in the
// future without changing any caller).
func (pe *PlanEngine) Release(kind string) {
	logger.DebugCF("plan_engine",
		"admission release (advisory no-op; the cap is recomputed live from persisted state on the next Admit)",
		map[string]any{"kind": kind})
}

// RegisterActiveCounter installs (or replaces) the ActiveCounterFunc for
// kind ("goal" or "loop" — "plan" is built in and cannot be overridden by
// this call). Wave 2-C calls this once at gateway boot, before any /goal or
// /loop admission can occur, so the global cap correctly counts all three
// R5 sources from the very first Admit call.
func (pe *PlanEngine) RegisterActiveCounter(kind string, fn ActiveCounterFunc) {
	if fn == nil || kind == "" || kind == "plan" {
		return
	}
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.activeCounters[kind] = fn
}

func (pe *PlanEngine) planningConfig() config.PlanningConfig {
	if pe.agentLoop != nil {
		if c := pe.agentLoop.GetConfig(); c != nil {
			return c.Planning
		}
	}
	return config.PlanningConfig{}
}

// --- Owner lifecycle (FR-065) ----------------------------------------------

// PausePlansOwnedBy pauses (PausedReason=owner_disabled) every running plan
// owned by agentID — called by the gateway on agent disable. A plan already
// paused for any reason is left untouched (its existing PausedReason wins).
func (pe *PlanEngine) PausePlansOwnedBy(agentID string) error {
	return pe.setPausedForOwner(agentID, pausedReasonOwnerDisabled, true)
}

// ResumePlansOwnedBy clears PausedReason on every running plan owned by
// agentID that was paused specifically for owner_disabled — called by the
// gateway on agent re-enable. A plan paused for a DIFFERENT reason (a future
// pause cause) is left untouched, so re-enabling the owner never
// accidentally resumes a plan paused for an unrelated reason.
func (pe *PlanEngine) ResumePlansOwnedBy(agentID string) error {
	return pe.setPausedForOwner(agentID, "", false)
}

// setPausedForOwner is the shared body of PausePlansOwnedBy/ResumePlansOwnedBy.
// When pausing (setting a non-empty reason), every running plan owned by
// agentID with no existing pause is updated. When resuming (clearing to
// ""), only plans currently paused for pausedReasonOwnerDisabled are
// touched — see ResumePlansOwnedBy's doc comment.
func (pe *PlanEngine) setPausedForOwner(agentID, newReason string, pausing bool) error {
	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		return fmt.Errorf("plan_engine: setPausedForOwner: list plans: %w", err)
	}
	var errs []string
	for i := range plans {
		p := &plans[i]
		if p.OwnerAgentID != agentID || p.State != plan.StateRunning {
			continue
		}
		if pausing {
			if p.PausedReason != "" {
				continue // already paused for some reason — do not clobber it
			}
		} else {
			if p.PausedReason != pausedReasonOwnerDisabled {
				continue // not paused (or paused for an unrelated reason)
			}
		}
		if _, uerr := pe.planStore.Update(p.ID, plan.Patch{PausedReason: &newReason}); uerr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.ID, uerr))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("plan_engine: could not update pause state for %d plan(s): %s",
			len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// HasActivePlansOwnedBy reports whether agentID owns at least one running
// (State=running, paused or not) plan — the gateway's delete-guard (400
// while owning active loops) calls this before allowing an agent delete.
func (pe *PlanEngine) HasActivePlansOwnedBy(agentID string) bool {
	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.WarnCF("plan_engine", "HasActivePlansOwnedBy: list failed",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return false
	}
	for i := range plans {
		if plans[i].OwnerAgentID == agentID && plans[i].State == plan.StateRunning {
			return true
		}
	}
	return false
}

// --- Boot reconciliation (FR-061/062) ---------------------------------------

// bootReconcile rebuilds in-flight state from the plan+task stores at Start:
// task/plan statuses are authoritative, events are only an optimization. It
// simply re-runs processPlan for every running plan — processPlan's own
// plan_phase switch already resumes an interrupted judge round (the
// PhaseJudging + !inFlightJudge case, which is unconditionally true right
// after Start since no goroutine can possibly be in flight yet in a fresh
// process) and its promoteReadyMembers/dispatchReadyMembers calls already
// implement "blocked tasks whose deps are all done are advanced" and never
// blindly re-dispatch an already in_progress task (ExecuteTask itself
// guards on Status != next).
func (pe *PlanEngine) bootReconcile(ctx context.Context) {
	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.ErrorCF("plan_engine", "boot reconcile: list plans failed", map[string]any{"error": err.Error()})
		return
	}
	running := 0
	for i := range plans {
		if plans[i].State != plan.StateRunning {
			continue
		}
		running++
		pe.processPlan(ctx, plans[i].ID)
	}
	logger.InfoCF("plan_engine", "boot reconciliation complete", map[string]any{"running_plans_scanned": running})
}
