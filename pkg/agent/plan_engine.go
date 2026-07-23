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
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
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

// planTaskDispatcher is the narrow interface *TaskExecutor satisfies, for
// the same reason as planJudge above.
type planTaskDispatcher interface {
	ExecuteTask(ctx context.Context, taskID string) error
	// ClearEvidenceGateStreak resets taskID's in-memory evidence-marker-gate
	// rejection streak (ADR-052 Fix-Wave-2/fix-wave item ii). cancelMemberLocked
	// (this file, US-6/US-7 Stop) marks a task `failed` via a direct store
	// write, bypassing TaskExecutor's own completeTaskWithResult/failTask
	// terminal-write chokepoints that would otherwise have cleared it — see
	// TaskExecutor.ClearEvidenceGateStreak's doc comment for why a Stop needs
	// the same treatment those "ANY terminal disposition" call sites get.
	ClearEvidenceGateStreak(taskID string)
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

	// DefaultBootSweepBudgetSeconds is the default wall-clock budget for the
	// boot sweep (FR-118 "within N s") when boot_sweep_budget_seconds is not
	// configured. The sweep scans non-terminal sessions and persists
	// failed(interrupted) records — a bounded, local-only operation, so 30 s
	// is ample even for a large install while still bounding shutdown.
	DefaultBootSweepBudgetSeconds = 30

	// DefaultSnapshotMaxBytes is the default cap for the
	// isNeedsInputReconstructable predicate's clause (4) "retained snapshot
	// within snapshot_max_bytes" (R§8.6) when snapshot_max_bytes is not
	// configured. Mirrors the delegate curated-context hard cap convention.
	DefaultSnapshotMaxBytes = 256 * 1024

	// failedReasonInterrupted is the LifecycleRecord.FailedReason value the
	// boot sweep writes (FR-118: failed(interrupted)).
	failedReasonInterrupted = "interrupted"
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

	// lifecycleStore, when set via SetLifecycleStore, supplies the durable
	// 8-state session records the boot sweep (FR-118/G-13) reconciles at
	// Start: every persisted non-terminal session with no live runtime turn
	// becomes failed(interrupted) within BootSweepBudget, carrying its last
	// checkpoint + undelivered messages, EXCEPT the two INV-9 exemptions (a
	// reconstructable parked needs_input, and a paused plan-owner session
	// whose plan is durably awaiting_owner_correction). Nil in a bare
	// struct-literal test engine and in any deployment that has not yet wired
	// the store — Start's sweep step no-ops cleanly when it is absent.
	lifecycleStore *session.LifecycleStore
	// bootSweepBudget bounds the wall-clock time the boot sweep may take
	// (boot_sweep_budget_seconds, FR-118 "within N s"). Defaults to
	// DefaultBootSweepBudgetSeconds when zero (SetBootSweepBudget / gateway
	// wiring resolves the configured value).
	bootSweepBudget time.Duration
	// snapshotMaxBytes bounds a parked needs_input session's retained context
	// snapshot for the isNeedsInputReconstructable predicate (R§8.6 clause 4).
	snapshotMaxBytes int64
	// agentResolver reports whether agentID still resolves at boot
	// (R§8.6 clause 2 — "child identity still resolves"). Nil = treat all as
	// resolving (the degradation a deployment without an agent registry
	// supplies; the predicate's other clauses still hold).
	agentResolver func(agentID string) bool
	// sessionFailedHook is fired for every session swept to
	// failed(interrupted) so a caller layer (the gateway) can emit the
	// session.failed event / drive recovery (FR-118 deliverable 3). Best-effort:
	// a hook failure is logged, never blocks the sweep.
	sessionFailedHook func(sessionID, reason string)
	// goalSemanticsVersioner reports the recorded trigger-semantics version
	// of a goal-bearing session (N-15 live-upgrade re-baseline). Wired by
	// Phase-2-C; nil until then, which means "unversioned" -> no re-baseline.
	goalSemanticsVersioner func(sessionID string) int
	// currentSemanticsVersionOverride, when >0, overrides the build constant
	// currentTriggerSemanticsVersion for the N-15 comparison (test-only; lets
	// a test simulate a post-bump build without waiting for a real bump).
	currentSemanticsVersionOverride int
	// intentLog, when set via SetIntentLog, is the write-ahead intent-log
	// (FR-148/M4/INV-6) replayed at Start BEFORE bootReconcile so any
	// committed-but-not-applied tail-append is applied to the plan/task stores
	// before the engine reconciles plans against them. Nil = no replay (the
	// Phase-2 owner loop that writes intents wires this).
	intentLog *plan.IntentLog
	// commitResolver resolves the last boundary commit hash for a plan member
	// (the gitevidence checkpoint, D13/G-12). Optional; nil = fresh-attempt
	// fallback when Play resumes a member with no git evidence available.
	commitResolver commitResolver

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

	// lastUnmetTerminalSignature is the F2 round-burn gate (a live shipped
	// defect fixed standalone, ahead of and independent from the
	// goal/plan/subagent redesign; acceptance G-9): planID -> the
	// planTerminalSignature of the all-terminal member state most recently
	// judged UNMET by THIS engine instance. Before processPlan re-invokes
	// beginPlanJudgeRound on an all-terminal plan, it compares the CURRENT
	// signature against this map (unmetTerminalSignatureUnchanged) — a match
	// means the same unchanged evidence would just be re-judged for no
	// reason, so the round is skipped entirely rather than debited again.
	// "One round then wait; no re-judge of unchanged state." The entry is
	// cleared whenever the plan (re)enters running (tryStartApprovedPlan —
	// covers both a fresh admission and a restart/Play-resume), so a
	// material correction or an owner-initiated resume always gets a fresh
	// round regardless of whether the member outcomes end up identical.
	// This map is the RUNTIME gate (consulted on every processPlan pass) and
	// is guarded by mu, lazily initialized by its own accessors exactly like
	// verifierRegistry above — a bare struct-literal test engine never
	// nil-map-panics. It is NOT the whole story: C1 durability shadows it on
	// the plan record itself — pkg/plan/plan.go's Plan.LastUnmetTerminalSignature
	// — which applyJudgeRoundOutcomeLocked persists (mirroring the in-memory
	// entry) whenever a round ends UNMET, and bootReconcile re-seeds THIS map
	// from at boot for every plan still awaiting owner correction. So a process
	// restart does NOT drop the gate's authority: the durable field survives,
	// the in-memory map is rehydrated from it, and the unchanged-state skip
	// keeps holding across the restart (the very next tick does not need to
	// re-burn a round to relearn what the prior process already concluded).
	lastUnmetTerminalSignature map[string]string

	// supersededMembers tracks done members whose outcomes have been marked
	// ignored-by-Judge via a SUPERSEDE correction (FR-143/G-11). planID -> set
	// of member task IDs. Same lazy-init + mu-guard pattern as
	// lastUnmetTerminalSignature above. Reconstructed from the intent log's
	// revision entries at boot (reconstructCorrections).
	supersededMembers map[string]map[string]bool

	// planGenerations tracks the current owner-session generation per plan
	// (FR-144/D13/G-12). Generation 0 is the initial run; each Play
	// increments it. Reconstructed from the intent log at boot.
	planGenerations map[string]int

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
		judge:            al,
		clock:            realPlanEngineClock{},
		tickInterval:     defaultPlanEngineTickInterval,
		activeCounters:   make(map[string]ActiveCounterFunc),
		verifierRegistry: NewVerifierSessionRegistry(),
		judgeSema:        newDispatchSemaphore(defaultPlanJudgeConcurrency),
	}
	// Guard against the classic Go "typed nil inside an interface" footgun:
	// assigning a nil *TaskExecutor directly to the dispatcher interface
	// field (as the old `dispatcher: taskExecutor,` struct-literal line did)
	// leaves pe.dispatcher NON-nil at the interface level (it has a concrete
	// type, just a nil pointer) — every existing `pe.dispatcher != nil` guard
	// in this file (cancelMemberLocked's ClearEvidenceGateStreak call) would
	// then pass the nil check and panic calling a method on a nil receiver.
	// Test callers that legitimately pass nil (e.g. a bare-engine test that
	// never dispatches) now get a TRUE nil interface, so those guards work.
	if taskExecutor != nil {
		pe.dispatcher = taskExecutor
	}
	if al != nil {
		pe.notifier = al.asyncNotifier
		pe.canceller = al
	}
	// Point the package-wide publisher seam (verifier_adjudication.go) at
	// THIS engine's registry so runVerifierAdjudication publishes into the
	// same instance the Stop fan-out enumerates (ADR-052 FR-037 — one
	// registry, both sides).
	SetVerifierSessionRegistry(pe.verifierRegistry)
	return pe
}

// SetLifecycleStore installs the durable session-lifecycle store the boot
// sweep (FR-118/G-13) reconciles at Start. Optional: when unset, Start's
// sweep step is a logged no-op (the engine still reconciles plans via
// bootReconcile). The gateway wiring path calls this BEFORE Start so the
// first boot sweep runs synchronously inside Start, folded into the one boot
// reconciliation pass (ADR-053 §5 boot sweep / N-15).
func (pe *PlanEngine) SetLifecycleStore(ls *session.LifecycleStore) {
	pe.mu.Lock()
	pe.lifecycleStore = ls
	pe.mu.Unlock()
}

// SetBootSweepBudget sets the wall-clock budget for the boot sweep
// (boot_sweep_budget_seconds, FR-118). Must be called before Start.
func (pe *PlanEngine) SetBootSweepBudget(d time.Duration) {
	pe.mu.Lock()
	pe.bootSweepBudget = d
	pe.mu.Unlock()
}

// SetSnapshotMaxBytes sets the retained-snapshot cap for the
// isNeedsInputReconstructable predicate (R§8.6 clause 4).
func (pe *PlanEngine) SetSnapshotMaxBytes(n int64) {
	pe.mu.Lock()
	pe.snapshotMaxBytes = n
	pe.mu.Unlock()
}

// SetAgentResolver installs the "child identity still resolves at boot"
// predicate (R§8.6 clause 2) used by isNeedsInputReconstructable. Optional.
func (pe *PlanEngine) SetAgentResolver(fn func(agentID string) bool) {
	pe.mu.Lock()
	pe.agentResolver = fn
	pe.mu.Unlock()
}

// SetSessionFailedHook installs the callback fired for every session the boot
// sweep marks failed(interrupted) (FR-118 deliverable 3 — emit session.failed
// / drive recovery). Best-effort: a hook error is logged, never blocks.
func (pe *PlanEngine) SetSessionFailedHook(fn func(sessionID, reason string)) {
	pe.mu.Lock()
	pe.sessionFailedHook = fn
	pe.mu.Unlock()
}

// SetGoalSemanticsVersioner installs the per-session trigger-semantics-version
// resolver for the N-15 live-upgrade re-baseline. Wired by Phase-2-C.
func (pe *PlanEngine) SetGoalSemanticsVersioner(fn func(sessionID string) int) {
	pe.mu.Lock()
	pe.goalSemanticsVersioner = fn
	pe.mu.Unlock()
}

// SetIntentLog installs the write-ahead intent-log (FR-148/M4/INV-6) replayed
// at Start. Optional; nil = no replay.
func (pe *PlanEngine) SetIntentLog(il *plan.IntentLog) {
	pe.mu.Lock()
	pe.intentLog = il
	pe.mu.Unlock()
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

// --- F2 round-burn gate (lastUnmetTerminalSignature) ----------------------

// unmetTerminalSignatureUnchanged reports whether sig — the CURRENT
// all-terminal member-state signature for planID — matches the signature
// most recently recorded as judged UNMET for that plan. A true result means
// nothing has changed since that round: no member was added/removed and no
// member's terminal outcome changed, so processPlan must skip re-invoking
// beginPlanJudgeRound rather than burn another JudgeRound re-judging
// identical evidence. Uses the two-value map read (not a "" sentinel) so a
// plan that legitimately has zero members (signature == "") is still gated
// correctly once recorded.
func (pe *PlanEngine) unmetTerminalSignatureUnchanged(planID, sig string) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	last, ok := pe.lastUnmetTerminalSignature[planID]
	return ok && last == sig
}

// recordUnmetTerminalSignature saves sig as the most recently judged-UNMET
// all-terminal signature for planID. Lazily initializes the backing map
// (same pattern as registry() above) so a bare struct-literal test engine,
// which omits NewPlanEngine's construction, never nil-map-panics on write.
func (pe *PlanEngine) recordUnmetTerminalSignature(planID, sig string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.lastUnmetTerminalSignature == nil {
		pe.lastUnmetTerminalSignature = make(map[string]string)
	}
	pe.lastUnmetTerminalSignature[planID] = sig
}

// clearUnmetTerminalSignature drops any recorded signature for planID. Called
// whenever the plan (re)enters running (tryStartApprovedPlan), so a fresh
// dispatch cycle — a brand-new plan's first run, or an owner's restart/Play
// resume of a previously-failed plan — is never blocked by a signature
// recorded during a prior life of the same plan ID, even if the member
// outcomes happen to end up identical again.
func (pe *PlanEngine) clearUnmetTerminalSignature(planID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	delete(pe.lastUnmetTerminalSignature, planID)
}

// planTerminalSignature builds a deterministic signature of tasks' terminal
// state: each member's id + status + cancel reason, sorted by id and joined
// with ASCII field/record separators (never appearing in an id/status/reason
// value) so no delimiter collision can alias two distinct member sets onto
// the same string. Two calls return an identical signature iff the member
// id set and every member's terminal outcome are identical — this is
// intentionally narrower than "the evidence text is unchanged": editing a
// done task's Result/Prompt without changing its id set or status does NOT
// change the signature (per this fix's spec: "member ids + their terminal
// outcomes + DAG generation" — evidence content is not part of the DAG
// shape). An owner who wants a genuine re-judge without adding/removing a
// member resets that member's status (through a non-terminal state and back)
// or restarts/resumes the plan (which clears the gate outright via
// clearUnmetTerminalSignature).
func planTerminalSignature(tasks []task.Task) string {
	type entry struct {
		id     string
		status task.Status
		reason task.CancelReason
	}
	entries := make([]entry, 0, len(tasks))
	for i := range tasks {
		entries = append(entries, entry{tasks[i].ID, tasks[i].Status, tasks[i].CancelReason})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.id)
		b.WriteByte('\x1f') // unit separator
		b.WriteString(string(e.status))
		b.WriteByte('\x1f')
		b.WriteString(string(e.reason))
		b.WriteByte('\x1e') // record separator
	}
	return b.String()
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

	// ADR-053 M4/FR-148/INV-6: replay the write-ahead intent-log BEFORE
	// bootReconcile so any committed-but-not-applied tail-append is applied
	// to the plan/task stores before the engine reconciles plans against them
	// (an all-or-nothing correction must land before the engine observes plan
	// state). No-ops cleanly when no intent log is wired or the log is empty.
	pe.replayIntentLogs()

	pe.bootReconcile(ctx)
	// ADR-053 §5 boot sweep (FR-118/G-13/INV-9): reconcile every persisted
	// non-terminal session with no live runtime turn to failed(interrupted)
	// within the configured budget, folding the live-upgrade re-baseline
	// (N-15) into the same single boot pass. Runs AFTER bootReconcile so the
	// durable F2 gate is already rehydrated (an awaiting-correction plan's
	// owner session is then correctly EXEMPTED by exemption b). No-ops
	// cleanly when no lifecycle store is wired.
	pe.runBootSweep(ctx)

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
	// F2 round-burn gate (acceptance G-9): a fresh admission, and equally an
	// owner's restart/Play resume of a previously-failed plan (ADR-052 §6.7,
	// same planID), must always get a genuinely fresh judge round rather than
	// being silently gated by a signature recorded during a prior life of
	// this plan ID — clear it here, the sole approved->running transition
	// point. Clears BOTH the in-memory map and the persisted
	// LastUnmetTerminalSignature field (C1 — the durable gate must not
	// outlive the dispatch cycle it was meant to gate), so a restart/Play
	// resume after a C1 park gets a genuinely fresh round even if the member
	// outcomes end up identical again.
	pe.clearUnmetTerminalSignature(planID)
	clearSig := ""
	if _, cerr := pe.planStore.Update(planID, plan.Patch{LastUnmetTerminalSignature: &clearSig}); cerr != nil {
		logger.WarnCF("plan_engine", "could not clear durable unmet signature on start",
			map[string]any{"plan_id": planID, "error": cerr.Error()})
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

	// silent-M1 (Phase-2 review): ADR-053 D12/R§8.3c/FR-174 graceful wind-down
	// for the plan/task scope. The ONE app-level OVERALL token pool is debited
	// by ALL workloads (member turns via dispatchReadyMembers, the plan-level
	// Judge via beginPlanJudgeRound) but only the GOAL loop surfaced
	// failed(budget_exhausted); without this brake the plan engine + its
	// members would drain the pool without ever hitting the terminal. Mirror
	// the goal loop's boundary gate (goal_loop.go) exactly: we are at the
	// dispatch/adjudication boundary (NOT mid-turn — the current turn already
	// finished), so this is a graceful wind-down, not a hard-fail. Checked here
	// at the single dispatch chokepoint so it covers BOTH member dispatch AND
	// plan-level judge rounds. No new debit — just the brake at the boundary;
	// TokenBudget() nil-guards a nil agentLoop (the struct-literal test harness
	// leaves agentLoop nil), so existing tests are unaffected.
	if tb := pe.agentLoop.TokenBudget(); tb != nil && tb.Exhausted() {
		handover := fmt.Sprintf(
			"Plan %q stopped: the overall token budget is exhausted (consumed %d tokens).",
			p.Title, tb.Consumed())
		pe.failPlanLocked(p.ID, plan.FailedReasonBudgetExhausted, handover)
		return
	}

	switch p.EffectivePlanPhase() {
	case plan.PhaseJudging:
		_, inFlight := pe.registry().Lookup(verifierUnitForPlan(p.ID))
		if inFlight {
			return // a goroutine is already adjudicating this round
		}
		// plan_phase=judging with no in-flight goroutine in THIS process can
		// only mean a prior process died mid-round (FR-062 boot case) — no
		// round was actually consumed (JudgeCriteria's own "0 rounds on
		// Unavailable/crash" contract), so resuming from scratch is safe.
		logger.InfoCF("plan_engine", "resuming plan judge round interrupted by a prior crash/restart",
			map[string]any{"plan_id": p.ID})
		resumeTasks, terr := pe.taskStore.List(task.Filter{PlanID: p.ID})
		if terr != nil {
			logger.WarnCF("plan_engine", "processPlan: list member tasks for judge-round resume failed",
				map[string]any{"plan_id": p.ID, "error": terr.Error()})
			resumeTasks = nil
		}
		pe.beginPlanJudgeRound(p, resumeTasks)
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
		pe.beginPlanJudgeRound(p, tasks)
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
// exhausted) — that check always runs, unconditionally, even on an unchanged
// all-terminal state: an already-exhausted plan must still terminate, this
// is not itself a "re-judge" (no judge call happens, no round is debited).
// Only once the ceiling has NOT been reached does the F2 round-burn gate
// (acceptance G-9) apply: if tasks is the plan's current all-terminal member
// snapshot and its signature matches the one already recorded as judged
// UNMET for this plan, the round is skipped entirely — "one round then
// wait; no re-judge of unchanged state." Otherwise claims a judgeSema lane
// (skips this tick — retried next pass — if the lane is full) and launches
// the actual judge call in its own goroutine, decoupled from the tick/event
// cycle (planJudgeRoundTimeout) so a slow-but-alive judge call never blocks
// dispatch of other plans.
//
// tasks is the caller's already-fetched member-task snapshot (processPlan's
// normal path) or the crash-resume snapshot (may be nil on a resume-time
// list failure — allMembersTerminal(nil) is vacuously true with signature
// "", but the in-memory gate map is guaranteed unpopulated for p.ID at that
// point in a freshly-booted process, so this can never spuriously block a
// genuine crash-resume; see this file's package doc on lastUnmetTerminalSignature).
func (pe *PlanEngine) beginPlanJudgeRound(p *plan.Plan, tasks []task.Task) {
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

	if allMembersTerminal(tasks) {
		sig := planTerminalSignature(tasks)
		if pe.unmetTerminalSignatureUnchanged(p.ID, sig) {
			logger.DebugCF("plan_engine",
				"plan judge round skipped: all-terminal state unchanged since last UNMET verdict (awaiting owner correction)",
				map[string]any{"plan_id": p.ID})
			return
		}
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
	// registers an empty-session placeholder under the SAME
	// verifierUnitForPlan key runVerifierAdjudication upserts the real id
	// through (F1: both sides must agree on this key) before dispatching the
	// verifier's turn.
	if regErr := pe.registry().Register(verifierUnitForPlan(p.ID), ""); regErr != nil {
		// CAS guard (corr-MAJOR-3, G-1): a LIVE verifier session already holds
		// this plan unit — a judge round is already in flight. Release the
		// round slot and bail rather than launching a duplicate round whose
		// verifier turn would race the in-flight one (exactly-once violation).
		// The phase=judging set above is owned by the in-flight round's own
		// lifecycle from here.
		release()
		logger.WarnCF("plan_engine", "plan judge: round already in-flight; CAS rejected placeholder registration",
			map[string]any{"plan_id": p.ID})
		return
	}

	pe.judgeWG.Add(1)
	go pe.runPlanJudgeRound(p.ID, release)
}

// runPlanJudgeRound performs ONE plan-level judge round. It ALWAYS clears
// its judgeSema slot, verifier-registry entry, and judgeWG count on return
// (defers below), regardless of outcome.
func (pe *PlanEngine) runPlanJudgeRound(planID string, release func()) {
	defer pe.judgeWG.Done()
	defer release()
	defer pe.registry().Unregister(verifierUnitForPlan(planID))

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
	// F2 round-burn gate (acceptance G-9): the signature of the EXACT
	// all-terminal member state this round is about to judge. If the round
	// ends UNMET, applyJudgeRoundOutcomeLocked records this so a later
	// unchanged idle tick's processPlan skips re-judging it.
	terminalSig := planTerminalSignature(tasks)

	criteria := p.DoD
	if len(criteria) == 0 {
		if soft := SoftTierCriterion(p.Title, p.Description, p.Goal); soft != nil {
			criteria = []task.AcceptanceCriterion{*soft}
		}
	}
	if len(criteria) == 0 {
		// SD-A7 soft tier: title/description/goal all empty too — nothing to
		// judge at all. Trust completion directly rather than looping
		// forever. Still routed through applyJudgeRoundOutcomeLocked's own
		// fresh State==running re-check (FR-014) — a Stop can land during the
		// taskStore.List call above just as easily as during a real judge
		// call, so this short-circuit gets the SAME atomicity guarantee as
		// the real-verdict path below, not a bespoke unlocked shortcut.
		// nothingToJudge always trusts completion (never UNMET), so
		// terminalSig is passed through but never recorded.
		pe.applyJudgeRoundOutcomeLocked(planID, JudgeCriteriaResult{}, true, terminalSig)
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
		// Product-blocker fix (ADR-052 FR-011/012 x ADR-046 P1): the plan's
		// own workspace (plan.go:264) — same rationale as task_executor.go's
		// task-scope call. See JudgeCriteriaInput.WorkspaceID.
		WorkspaceID: p.WorkspaceID,
	})

	// FR-014 (US-6 acceptance 3, Test 7): JudgeCriteria runs OUTSIDE
	// planDecisionMu by design (this whole goroutine is decoupled from the
	// lock precisely so a slow-but-alive judge call never blocks other
	// plans' dispatch — see beginPlanJudgeRound's doc comment), so a Stop
	// can land on this exact plan at any point up to and including the
	// instant this call returns. Delegate to applyJudgeRoundOutcomeLocked,
	// which re-checks State==running and applies the outcome as ONE atomic
	// critical section under planDecisionMu.
	pe.applyJudgeRoundOutcomeLocked(planID, result, false, terminalSig)
}

// applyJudgeRoundOutcomeLocked applies a just-computed plan-level judge
// round result to planID — but ONLY after acquiring planDecisionMu and
// re-confirming, from a FRESH read, that the plan is still `running`, and it
// keeps holding that lock across every write the outcome requires.
//
// ADR-052 FR-014/§6.4(b) TOCTOU fix (7-reviewer + architect gate,
// "plan-scope variant"): PRE-FIX, the equivalent re-check
// (verdictStillApplicable) ran under its OWN separate, momentary lock
// acquisition and then RELEASED it — the outcome writes that followed
// (PlanPhase revert on Unavailable, JudgeRounds/PlanPhase/HandoverText on an
// unmet verdict, the two synthesizeAndComplete writes on a met verdict, and
// the wakeOwner side effects any of those trigger) were entirely
// UNPROTECTED plain store.Update calls. plan.Store's own transition guard
// rejects a stale State write (failed->done is illegal), but it has no
// opinion on the OTHER fields — so a Stop landing in the gap between that
// released lock and these writes could still see its own HandoverText
// clobbered by the round's steering text, and could still fire a spurious
// plan_judge_unmet/plan_judge_met wakeOwner notification for a plan the
// user had just stopped. Collapsing recheck+apply into ONE lock acquisition
// closes that gap: every OTHER real plan-state mutator
// (StopPlan/StopTask/tryStartApprovedPlan/idleExpireOneLocked) also takes
// planDecisionMu before touching plan state, so nothing can interleave here
// once this function has re-read and confirmed `running`.
//
// nothingToJudge is true for the SD-A7 soft-tier-empty short-circuit
// (completePlan), in which case result is ignored.
//
// terminalSig is the planTerminalSignature (F2 round-burn gate, acceptance
// G-9) of the all-terminal member state runPlanJudgeRound actually judged —
// on an UNMET verdict it is recorded as this plan's "already judged this,
// don't re-judge until it changes" marker so a later unchanged idle tick's
// processPlan skips re-invoking beginPlanJudgeRound.
func (pe *PlanEngine) applyJudgeRoundOutcomeLocked(planID string, result JudgeCriteriaResult, nothingToJudge bool, terminalSig string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	current, err := pe.planStore.Get(planID)
	if err != nil || current.State != plan.StateRunning {
		logger.InfoCF("plan_engine",
			"plan judge round outcome dropped: plan left running during adjudication (Stop landed concurrently)",
			map[string]any{"plan_id": planID})
		return
	}

	if nothingToJudge {
		pe.completePlan(current)
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
		if _, uerr := pe.planStore.Update(current.ID, plan.Patch{PlanPhase: &dispatching}); uerr != nil {
			logger.WarnCF("plan_engine", "judge round: could not revert plan_phase after unavailability",
				map[string]any{"plan_id": current.ID, "error": uerr.Error()})
		}
		logger.WarnCF("plan_engine", "plan judge round abandoned (judge unavailable)",
			map[string]any{"plan_id": current.ID, "reason": result.Reason})
		return
	}

	verdict := result.Verdict
	newRounds := current.JudgeRounds + 1
	pe.touchActivity(current.ID)

	if verdict.Met {
		pe.synthesizeAndComplete(current, newRounds)
		return
	}

	steering := buildPlanSteeringText(verdict)
	// ADR-053 C1/FR-147 (INV-2/INV-7): an UNMET verdict on an all-terminal DAG
	// (the only kind of state the plan Judge ever fires on) durably parks the
	// plan at plan_phase=awaiting_owner_correction — NOT back to dispatching —
	// persisting the unmet terminal signature so the F2 round-burn gate
	// survives restart. The plan-owner session sits at lifecycle `paused`
	// (Phase-2 owner loop's responsibility); the boot sweep exempts that
	// paused session from the failed(interrupted) sweep via OwnsPlanID ->
	// plan.PlanPhase == awaiting_owner_correction (FR-118 exemption b).
	awaiting := plan.PhaseAwaitingOwnerCorrection
	sig := terminalSig
	if _, uerr := pe.planStore.Update(current.ID, plan.Patch{
		JudgeRounds:                &newRounds,
		PlanPhase:                  &awaiting,
		HandoverText:               &steering,
		LastUnmetTerminalSignature: &sig,
	}); uerr != nil {
		logger.ErrorCF("plan_engine", "judge round: could not persist unmet verdict",
			map[string]any{"plan_id": current.ID, "error": uerr.Error()})
		return
	}
	// F2 round-burn gate (acceptance G-9): this exact all-terminal state has
	// now been judged UNMET — record it so processPlan's next idle tick(s)
	// wait instead of re-judging the same evidence again. Recorded only
	// after the store write above actually succeeds, so a persist failure
	// (round effectively didn't happen) never blocks a legitimate retry.
	// Persisted above (LastUnmetTerminalSignature) AND mirrored into the
	// in-memory map here: the in-memory map is the in-process authority (it is
	// cleared on tryStartApprovedPlan), the persisted field is the
	// across-restart authority (rehydrated by bootReconcile) — together they
	// close the standalone-F2 restart gap (C1).
	pe.recordUnmetTerminalSignature(current.ID, terminalSig)
	// ADR-053 Phase-2 (D4 superseded): record the durable owner-session
	// linkage so the boot sweep can exempt this plan's paused owner session
	// from the failed(interrupted) sweep (FR-118 exemption b, via
	// OwnerSessionID). The owner session is the persistent plan:<id> chat
	// context — the same one wakeOwner notifies — re-opened on purpose for
	// each correction round rather than a fresh one-shot wake.
	pe.ensureOwnerSessionLocked(current)
	pe.wakeOwner(current.ID, current.OwnerAgentID, fmt.Sprintf(
		"Plan %q round %d: the plan judge found the Definition of Done UNMET.\n\n%s"+
			"\n\nThe plan is now awaiting your correction. Use the plan skill to re-plan: "+
			"append tail members, SUPERSEDE a done member whose outcome is wrong, or "+
			"TARGETED-RETRY a failed member.",
		current.Title, newRounds, steering,
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
// historical marker of how the plan finished. Caller must hold
// planDecisionMu (see applyJudgeRoundOutcomeLocked/completePlan).
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
// branch. Caller must hold planDecisionMu (applyJudgeRoundOutcomeLocked's
// own re-checked lock, or FR-041/idle-expiry's — every call site already
// holds it before reaching here).
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
// PlanEngine.StopPlan/StopTask cancels — a human-readable echo of the
// authoritative task.CancelReason field (ADR-052 US-8's orange "Cancelled"
// marker reads the FIELD; this text is display/log context only).
const memberCancelReasonMarker = "[reason:stopped_by_user]"

// isCancelledMember reports whether t was terminated by a user Stop (as
// opposed to a genuine judge/attempt-exhaustion failure) — see
// memberCancelReasonMarker.
func isCancelledMember(t *task.Task) bool {
	if t.Status != task.StatusFailed {
		return false
	}
	// task.CancelReason is the authoritative discriminator (ADR-052 FR-015/
	// FR-028); the Result-prefix check is kept as a defensive fallback for
	// records written before the field landed in the same wave.
	return t.CancelReason == task.CancelReasonStoppedByUser ||
		strings.HasPrefix(t.Result, memberCancelReasonMarker)
}

// StopPlan implements US-6: the plan-level Stop fan-out. Cancels {every
// `in_progress` member's worker session} + {every REGISTERED verifier
// session for the plan itself and every one of its members, via the
// verifier registry}, marks every `in_progress` member `failed`+cancel-
// marker (US-8), and — unconditionally, regardless of whether other members
// could still independently progress (that conditional case is FR-041,
// member-level Stop only) — transitions the plan itself to
// `failed`(stopped_by_user). Returns the updated plan on success.
//
// Also accepts a cap-queued `approved` plan (ADR-052 spec, Edge Case "Stop
// wins" — the SPA ships a Stop button for approved plans same as running
// ones). For approved, the fan-out below is naturally a no-op: nothing has
// been dispatched yet (Admit/tryStartApprovedPlan hasn't fired), so there
// are no in_progress members and no registered verifier sessions to cancel
// — only the plan's own state write (approved -> failed(stopped_by_user))
// happens. This is race-free against a concurrent admission: both StopPlan
// and tryStartApprovedPlan (the only path that promotes approved -> running)
// take planDecisionMu, so an approved plan can never be admitted between
// this check and the state write below.
func (pe *PlanEngine) StopPlan(ctx context.Context, planID, userID, channel string) (*plan.Plan, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: StopPlan: get plan %q: %w", planID, err)
	}
	if p.State != plan.StateRunning && p.State != plan.StateApproved {
		return nil, fmt.Errorf("plan_engine: StopPlan: plan %q is %s, not running or approved", planID, p.State)
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
	// every member id costs nothing and misses nothing}. Unit keys MUST go
	// through verifierUnitForPlan/verifierUnitForTask (F1) — the exact same
	// helpers runVerifierAdjudication/beginPlanJudgeRound register under —
	// never the raw plan/task id.
	units := make([]string, 0, len(tasks)+1)
	units = append(units, verifierUnitForPlan(planID))
	var sessions []string
	for i := range tasks {
		t := &tasks[i]
		units = append(units, verifierUnitForTask(t.ID))
		if t.Status == task.StatusInProgress && t.SessionID != "" {
			sessions = append(sessions, t.SessionID)
		}
	}
	sessions = append(sessions, pe.registry().SessionsFor(units...)...)
	// ADR-053 Phase-2 (D4 superseded): the persistent owner session (the
	// plan:<id> correction-loop chat) is cancelled as part of the Stop
	// fan-out — Stop cancels it like any session. The owner session ID is
	// the durable OwnerSessionID on the plan record (set when the plan
	// first entered awaiting-owner-correction).
	if p.OwnerSessionID != "" {
		sessions = append(sessions, p.OwnerSessionID)
	}
	pe.cancelSessions(ctx, sessions, userID, channel)

	// Item 5: aggregate (never silently discard) any per-member store-write
	// failure while still completing the REST of the fan-out — a single
	// member's write failure must not abort cancelling its siblings, and
	// must not report an unqualified success either (an orphaned
	// still-in_progress member is exactly the outcome ADR-052 §6.4 warns
	// about).
	var failedMemberIDs []string
	for i := range tasks {
		t := &tasks[i]
		if t.Status == task.StatusInProgress {
			if _, cerr := pe.cancelMemberLocked(t.ID, userID); cerr != nil {
				failedMemberIDs = append(failedMemberIDs, t.ID)
			}
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
	return updated, aggregateMemberCancelErrors(planID, failedMemberIDs)
}

// aggregateMemberCancelErrors renders a partial-stop error listing every
// member task id whose cancelMemberLocked store write failed during
// StopPlan's fan-out, or nil when none did. The plan's own state transition
// to failed(stopped_by_user) — and every session-level cancel — has ALREADY
// completed by the time this is called (StopPlan never short-circuits the
// fan-out on a single member's failure); this only reports that the
// resulting state may contain an orphaned still-in_progress member so the
// caller (and the operator) know to investigate rather than reading Stop as
// an unqualified success.
func aggregateMemberCancelErrors(planID string, failedMemberIDs []string) error {
	if len(failedMemberIDs) == 0 {
		return nil
	}
	return fmt.Errorf(
		"plan_engine: StopPlan: plan %q was stopped, but %d member task(s) could not be marked "+
			"cancelled (store write failed) and may remain in_progress: %s",
		planID, len(failedMemberIDs), strings.Join(failedMemberIDs, ", "),
	)
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
	sessions = append(sessions, pe.registry().SessionsFor(verifierUnitForTask(taskID))...)
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
// (US-8, task.CancelReason) and emits task_status_changed so
// PlanEngine's own reactive event loop re-evaluates the owning plan (FR-041)
// on its next pass. Caller must hold planDecisionMu (both StopPlan and
// StopTask call this while holding it). A store-write failure is returned
// to the caller (StopTask propagates it; StopPlan logs and continues the
// fan-out for the plan's other members — a single member's write failure
// must not abort cancelling the rest).
func (pe *PlanEngine) cancelMemberLocked(taskID, userID string) (*task.Task, error) {
	failed := task.StatusFailed
	cancelReason := task.CancelReasonStoppedByUser
	result := fmt.Sprintf("%s Cancelled by %s via Stop.", memberCancelReasonMarker, userID)
	now := time.Now().UTC().Format(time.RFC3339)
	updated, err := pe.taskStore.Update(taskID, task.Patch{
		Status:       &failed,
		CancelReason: &cancelReason,
		Result:       &result,
		CompletedAt:  &now,
	})
	if err != nil {
		logger.WarnCF("plan_engine", "stop: could not mark member task cancelled",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return nil, fmt.Errorf("plan_engine: cancel task %q: %w", taskID, err)
	}
	// Fix-wave item ii: this write is a terminal disposition for taskID (like
	// completeTaskWithResult/failTask) that bypasses both of TaskExecutor's
	// own chokepoints — clear its evidence-marker-gate streak directly so it
	// does not leak for the process lifetime. dispatcher is nil-guarded the
	// same way agentLoop/canceller/notifier are elsewhere in this file (a
	// bare struct-literal test engine may omit it).
	if pe.dispatcher != nil {
		pe.dispatcher.ClearEvidenceGateStreak(taskID)
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
	allBlockersDone := true
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
		allBlockersDone = false
		if !memberIsDeadEnd(dep, byID, visiting) {
			return false
		}
	}
	// Defensive: if EVERY blocker in this snapshot already reads `done`, t is
	// not actually stuck — it is a stale-snapshot artifact (recomputeBlockedStateLocked
	// / AdvanceBlockedDependents will promote t to `next` on the very next
	// pass), never a genuine dead end. Without this, a plan-member snapshot
	// caught between "its last blocker just completed" and "the dependent's
	// own blocked->next promotion" would be misreported as terminally stuck.
	if allBlockersDone {
		return false
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

// --- ADR-053 Phase 2: owner loop + correction (§3/§3b/§3c, G-9..G-12) -----

// CorrectionVerb is the owner-correction verb (FR-143/G-11).
type CorrectionVerb string

const (
	CorrectionAppend        CorrectionVerb = CorrectionVerb(plan.RevisionAppend)
	CorrectionSupersede     CorrectionVerb = CorrectionVerb(plan.RevisionSupersede)
	CorrectionTargetedRetry CorrectionVerb = CorrectionVerb(plan.RevisionTargetedRetry)
)

// CorrectionRequest is an owner correction to an unmet DoD (FR-143/G-11). The
// owner issues one of three verbs; each records a revision entry committed
// transactionally via the intent-log (INV-6/N-8).
//
// not-wire-format: engine-internal type; the REST/tool layer maps its wire
// type to this.
type CorrectionRequest struct {
	Verb                CorrectionVerb `json:"verb"`
	FalsifiedAssumption string         `json:"falsified_assumption,omitempty"`
	TailMembers         []task.Task    `json:"tail_members,omitempty"`
	TailEdges           []IntentEdge   `json:"tail_edges,omitempty"`
	SupersededMemberID  string         `json:"superseded_member_id,omitempty"`
	RetriedMemberID     string         `json:"retried_member_id,omitempty"`
	Reason              string         `json:"reason,omitempty"`
}

// IntentEdge is re-exported here (alias of plan.IntentEdge) so callers importing
// pkg/agent get a single-package API for corrections. The authoritative type
// lives in pkg/plan (intent_log.go) where the intent-log store uses it.
type IntentEdge = plan.IntentEdge

// CorrectionResult is the outcome of processing a correction.
//
// not-wire-format: engine-internal type.
type CorrectionResult struct {
	RevisionID    string             `json:"revision_id"`
	Generation    int                `json:"generation"`
	RevisionEntry plan.RevisionEntry `json:"revision_entry"`
	HonestExit    bool               `json:"honest_exit,omitempty"`
}

// commitResolver resolves the last boundary commit hash for a plan member
// (the gitevidence checkpoint). Used by Play to resume failed/cancelled
// members from the last commit (D13/G-12). nil = no git evidence available
// (Play falls back to fresh attempt, signalled).
type commitResolver interface {
	LastMemberCommit(planID, taskID string) (hash string, err error)
}

// SetCommitResolver installs the gitevidence checkpoint resolver for
// Play-from-commit (D13/G-12). Optional; nil (the default) means Play falls
// back to fresh attempt for every failed/cancelled member.
func (pe *PlanEngine) SetCommitResolver(cr commitResolver) {
	pe.mu.Lock()
	pe.commitResolver = cr
	pe.mu.Unlock()
}

// --- Owner-session management (D4 superseded) ------------------------------

// ensureOwnerSessionLocked records the durable owner-session linkage on the
// plan record (OwnerSessionID, m-3/FR-147) if none is set yet. The owner
// session is the persistent plan:<id> chat context — the same one wakeOwner
// notifies — re-opened on purpose for each correction round. The boot sweep
// resolves the awaiting-correction exemption through this field (FR-118
// exemption b). Caller must hold planDecisionMu.
func (pe *PlanEngine) ensureOwnerSessionLocked(p *plan.Plan) {
	if p.OwnerSessionID != "" {
		return
	}
	sessionID := "plan:" + p.ID
	if _, err := pe.planStore.Update(p.ID, plan.Patch{OwnerSessionID: &sessionID}); err != nil {
		logger.WarnCF("plan_engine", "could not persist owner_session_id",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}
	logger.InfoCF("plan_engine", "owner session opened for plan",
		map[string]any{"plan_id": p.ID, "owner_session_id": sessionID})
}

// --- Superseded-member tracking (FR-143 SUPERSEDE) -------------------------

// markMemberSuperseded records that memberID's outcome is ignored-by-Judge
// (SUPERSEDE verb). The member's task record stays immutable; only the
// Judge-weighting changes. Same lazy-init + mu pattern as
// recordUnmetTerminalSignature.
func (pe *PlanEngine) markMemberSuperseded(planID, memberID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.supersededMembers == nil {
		pe.supersededMembers = make(map[string]map[string]bool)
	}
	set := pe.supersededMembers[planID]
	if set == nil {
		set = make(map[string]bool)
		pe.supersededMembers[planID] = set
	}
	set[memberID] = true
}

// isMemberSuperseded reports whether memberID's outcome has been marked
// ignored-by-Judge via a SUPERSEDE correction.
func (pe *PlanEngine) isMemberSuperseded(planID, memberID string) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.supersededMembers[planID][memberID]
}

// supersededMemberSet returns a copy of the superseded-member set for planID
// (nil if none). Used by evidence-building to exclude superseded done members
// from the Judge's view.
func (pe *PlanEngine) supersededMemberSet(planID string) map[string]bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	src := pe.supersededMembers[planID]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// --- Plan-generation tracking (D13/G-12) -----------------------------------

// planGeneration returns the current generation number for planID (0 on first
// run, incremented by each Play). Same lazy-init pattern.
func (pe *PlanEngine) planGeneration(planID string) int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.planGenerations == nil {
		pe.planGenerations = make(map[string]int)
	}
	return pe.planGenerations[planID]
}

// incrementPlanGeneration bumps planID's generation and returns the new value.
func (pe *PlanEngine) incrementPlanGeneration(planID string) int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.planGenerations == nil {
		pe.planGenerations = make(map[string]int)
	}
	pe.planGenerations[planID]++
	return pe.planGenerations[planID]
}

// newRevisionID mints a deterministic-enough revision identifier.
func (pe *PlanEngine) newRevisionID(planID string) string {
	return fmt.Sprintf("rev-%s-%d-%d", planID, pe.clock.Now().UnixNano(), pe.planGeneration(planID))
}

// --- AppendCorrection (FR-143/G-11, the main correction handler) -----------

// AppendCorrection processes an owner correction to an unmet DoD (FR-143/G-11).
// The plan MUST be in awaiting_owner_correction. The correction commits
// transactionally via the intent-log (INV-6/N-8): AppendIntent →
// MarkCommitted → Apply → MarkDone. After the commit:
//   - For append/supersede: auto-reset ALL live-round failed members (excludes
//     frozen/done members — G-10).
//   - For targeted_retry: reset ONLY the specified failed member (no full
//     Stop/Play — D4).
//   - Tails depend only on done outcomes; an unreachable DoD takes the
//     honest-exit path (G-10).
//   - The durable unmet signature is cleared (INV-7: correction = new activity).
//   - The DoD stays immutable (G-11).
func (pe *PlanEngine) AppendCorrection(ctx context.Context, planID string, req CorrectionRequest) (*CorrectionResult, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: get plan %q: %w", planID, err)
	}
	if p.State != plan.StateRunning {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: plan %q is %s, not running", planID, p.State)
	}
	if p.EffectivePlanPhase() != plan.PhaseAwaitingOwnerCorrection {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: plan %q is in phase %q, not awaiting_owner_correction",
			planID, p.EffectivePlanPhase())
	}
	if err := pe.validateCorrection(planID, p, req); err != nil {
		return nil, err
	}

	gen := pe.planGeneration(planID)
	revID := pe.newRevisionID(planID)
	tailAddIDs := make([]string, 0, len(req.TailMembers))
	for i := range req.TailMembers {
		tailAddIDs = append(tailAddIDs, req.TailMembers[i].ID)
	}
	now := pe.clock.Now().UTC()
	rev := plan.RevisionEntry{
		RevisionID:          revID,
		PlanID:              planID,
		Generation:          gen,
		Verb:                plan.RevisionVerb(req.Verb),
		FalsifiedAssumption: req.FalsifiedAssumption,
		TailAdds:            tailAddIDs,
		SupersededMemberID:  req.SupersededMemberID,
		RetriedMemberID:     req.RetriedMemberID,
		Reason:              req.Reason,
		CreatedAt:           now,
	}
	rec := plan.IntentRecord{
		IntentID: revID,
		PlanID:   planID,
		Members:  req.TailMembers,
		Edges:    req.TailEdges,
		Revision: rev,
		Patch: plan.IntentRecordPatch{
			ClearLastUnmetTerminalSignature: true,
			PlanPhase:                       plan.PhaseDispatching,
		},
		CreatedAt: now,
	}
	apply := pe.buildCorrectionApplyFunc(planID, req)

	// Transactional commit via the intent-log (INV-6/N-8). When no intent log
	// is wired (tests/degraded), apply directly with no transactional guarantee.
	if pe.intentLog != nil {
		if err := pe.intentLog.CommitCorrection(rec, apply); err != nil {
			return nil, fmt.Errorf("plan_engine: AppendCorrection: commit: %w", err)
		}
	} else if err := apply(rec); err != nil {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: apply (no intent log): %w", err)
	}

	// Record supersession in-memory (for Judge evidence building).
	if req.Verb == CorrectionSupersede {
		pe.markMemberSuperseded(planID, req.SupersededMemberID)
	}
	// Clear the in-memory durable unmet signature (correction = new activity,
	// INV-7). The persisted field was cleared by the apply func's plan patch.
	pe.clearUnmetTerminalSignature(planID)
	pe.touchActivity(planID)

	// Auto-reset + honest-exit check (G-10).
	tasks, lerr := pe.taskStore.List(task.Filter{PlanID: planID})
	if lerr != nil {
		// B1 (review BLOCKER): do NOT mask a store-read error as "plan cannot
		// progress" — planCannotProgress(nil) returns true vacuously, which would
		// failPlanLocked a HEALTHY plan with a misleading
		// judge_rounds_exhausted reason. The correction is durably committed
		// above; surface the read error and skip the post-correction honest-exit
		// assessment this cycle (the next processPlan tick re-evaluates against
		// the authoritative store).
		logger.ErrorCF("plan_engine", "AppendCorrection: post-correction task list failed; correction committed, skipping honest-exit assessment",
			map[string]any{"plan_id": planID, "error": lerr.Error()})
		return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
	}
	if req.Verb != CorrectionTargetedRetry {
		// append/supersede: auto-reset ALL live-round failed members
		// (excludes frozen/done members).
		pe.autoResetLiveRoundFailedMembers(planID, tasks)
		tasks, lerr = pe.taskStore.List(task.Filter{PlanID: planID})
		if lerr != nil {
			logger.ErrorCF("plan_engine", "AppendCorrection: post-auto-reset task list failed; correction committed, skipping honest-exit assessment",
				map[string]any{"plan_id": planID, "error": lerr.Error()})
			return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
		}
	}
	// Honest exit: if after the correction + auto-reset the plan still cannot
	// make progress, fail it honestly (no livelock, G-10).
	if planCannotProgress(tasks) {
		handover := buildUnreachableDoDHandover(p, tasks)
		pe.failPlanLocked(planID, plan.FailedReasonJudgeRoundsExhausted, handover)
		return &CorrectionResult{
			RevisionID: revID, Generation: gen, RevisionEntry: rev,
			HonestExit: true,
		}, nil
	}
	// Re-dispatch ready members.
	pe.dispatchReadyMembers(ctx, planID, tasks)
	return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
}

// validateCorrection checks the verb-specific preconditions (member exists,
// correct status, belongs to this plan).
func (pe *PlanEngine) validateCorrection(planID string, p *plan.Plan, req CorrectionRequest) error {
	switch req.Verb {
	case CorrectionSupersede:
		if req.SupersededMemberID == "" {
			return fmt.Errorf("plan_engine: supersede requires superseded_member_id")
		}
		t, err := pe.taskStore.Get(req.SupersededMemberID)
		if err != nil {
			return fmt.Errorf("plan_engine: superseded member %q: %w", req.SupersededMemberID, err)
		}
		if t.Status != task.StatusDone {
			return fmt.Errorf("plan_engine: member %q is %s, not done (only done members can be superseded)",
				req.SupersededMemberID, t.Status)
		}
		if t.PlanID != planID {
			return fmt.Errorf("plan_engine: member %q belongs to plan %q, not %q",
				req.SupersededMemberID, t.PlanID, planID)
		}
	case CorrectionTargetedRetry:
		if req.RetriedMemberID == "" {
			return fmt.Errorf("plan_engine: targeted_retry requires retried_member_id")
		}
		t, err := pe.taskStore.Get(req.RetriedMemberID)
		if err != nil {
			return fmt.Errorf("plan_engine: retried member %q: %w", req.RetriedMemberID, err)
		}
		if t.Status != task.StatusFailed {
			return fmt.Errorf("plan_engine: member %q is %s, not failed (only failed members can be targeted-retried)",
				req.RetriedMemberID, t.Status)
		}
		if t.PlanID != planID {
			return fmt.Errorf("plan_engine: member %q belongs to plan %q, not %q",
				req.RetriedMemberID, t.PlanID, planID)
		}
	case CorrectionAppend:
		if len(req.TailMembers) == 0 {
			return fmt.Errorf("plan_engine: append requires at least one tail member")
		}
	default:
		return fmt.Errorf("plan_engine: unknown correction verb %q", req.Verb)
	}
	return nil
}

// buildCorrectionApplyFunc returns the idempotent ApplyFunc for a correction.
// It performs the per-file writes: create tail-member tasks, wire edges, reset
// the targeted-retry member, apply auto-reset for targeted_retry, and patch
// the plan record (clear unmet signature, set phase to dispatching). Every
// operation is idempotent — boot's replay-forward may call it on a plan whose
// writes partially landed.
func (pe *PlanEngine) buildCorrectionApplyFunc(planID string, req CorrectionRequest) plan.ApplyFunc {
	return func(rec plan.IntentRecord) error {
		// Create tail-member tasks (idempotent: skip if the task already exists).
		for i := range rec.Members {
			m := &rec.Members[i]
			if m.ID == "" {
				continue
			}
			if existing, err := pe.taskStore.Get(m.ID); err == nil && existing != nil {
				continue // already created (idempotent replay)
			}
			if m.PlanID == "" {
				m.PlanID = planID
			}
			if m.WorkspaceID == "" {
				m.WorkspaceID = ""
			}
			if err := pe.taskStore.Create(m); err != nil {
				return fmt.Errorf("create tail member %q: %w", m.ID, err)
			}
		}
		// Wire tail edges (AddDependency is idempotent: returns added=false,
		// nil when the edge already exists — safe for replay).
		for _, e := range rec.Edges {
			if _, _, err := pe.taskStore.AddDependency(e.ToTaskID, e.FromTaskID); err != nil {
				return fmt.Errorf("wire edge %s->%s: %w", e.FromTaskID, e.ToTaskID, err)
			}
		}
		// Targeted-retry: reset the specific failed member (idempotent:
		// RestartReset errors on non-failed, which is the correct no-op).
		if req.Verb == CorrectionTargetedRetry && req.RetriedMemberID != "" {
			if _, err := pe.taskStore.RestartReset(req.RetriedMemberID); err != nil {
				// Already reset (idempotent replay) — not fatal.
				logger.DebugCF("plan_engine", "targeted-retry reset (may be idempotent no-op)",
					map[string]any{"task_id": req.RetriedMemberID, "error": err.Error()})
			}
		}
		// Patch the plan record (clear unmet signature, set phase).
		dispatching := plan.PhaseDispatching
		clearSig := ""
		if _, err := pe.planStore.Update(planID, plan.Patch{
			PlanPhase:                  &dispatching,
			LastUnmetTerminalSignature: &clearSig,
		}); err != nil {
			return fmt.Errorf("patch plan phase: %w", err)
		}
		return nil
	}
}

// --- Auto-reset + honest exit (G-10) ---------------------------------------

// autoResetLiveRoundFailedMembers resets every failed member back to `next`
// (via RestartReset) EXCEPT done members (frozen — preserved) and members
// explicitly superseded (handled by the correction verb). This gives the
// failed members another chance after the owner's correction landed.
// Caller must hold planDecisionMu.
func (pe *PlanEngine) autoResetLiveRoundFailedMembers(planID string, tasks []task.Task) {
	superseeded := pe.supersededMemberSet(planID)
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusFailed {
			continue // only reset failed members; done/next/blocked/in_progress untouched
		}
		if superseeded[t.ID] {
			continue // superseded members are not auto-reset
		}
		if _, err := pe.taskStore.RestartReset(t.ID); err != nil {
			logger.WarnCF("plan_engine", "auto-reset: could not reset failed member",
				map[string]any{"plan_id": planID, "task_id": t.ID, "error": err.Error()})
		}
	}
}

// planCannotProgress reports whether the plan can make NO further progress
// after a correction + auto-reset (the honest-exit condition, G-10). True when:
//   - No member is `next` or `in_progress` (nothing to dispatch/run).
//   - Every remaining non-done member is `failed` (auto-reset already tried
//     and they re-failed, or were not reset) or `blocked` behind a dependency
//     that is itself a dead-end (failed, not done).
//
// This mirrors planStuckAfterMemberCancel's dead-end analysis but is
// correction-scoped: it evaluates the post-correction, post-auto-reset state.
func planCannotProgress(tasks []task.Task) bool {
	hasDispatchable := false
	for i := range tasks {
		s := tasks[i].Status
		if s == task.StatusNext || s == task.StatusInProgress {
			hasDispatchable = true
			break
		}
	}
	if hasDispatchable {
		return false
	}
	// No dispatchable members — check if every non-done member is a dead-end.
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
			return false // at least one non-done member could still progress
		}
	}
	return true
}

// buildUnreachableDoDHandover renders the honest-exit handover for a plan
// whose DoD is structurally unreachable after correction + auto-reset.
func buildUnreachableDoDHandover(p *plan.Plan, tasks []task.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan %q cannot reach its Definition of Done: after the latest correction "+
		"and auto-reset, no member can make further progress (every non-done member is "+
		"failed or blocked behind a failed member).\n\nMember outcomes:\n", p.Title)
	for i := range tasks {
		t := &tasks[i]
		fmt.Fprintf(&sb, "- %s (%s)\n", t.Title, t.Status)
	}
	sb.WriteString("\nThe plan has been marked failed. Review the DoD and member outcomes.")
	return sb.String()
}

// --- Play = resumed_from generation (D13/G-12) -----------------------------

// PlayResult is the outcome of a Play (resume) operation.
//
// not-wire-format: engine-internal type.
type PlayResult struct {
	NewGeneration int    `json:"new_generation"`
	ResumedFrom   string `json:"resumed_from,omitempty"`
	PlanID        string `json:"plan_id"`
}

// PlayPlan resumes a stopped/failed plan as a new owner-session generation
// (FR-144/D13/G-12). Transitions the plan cancelled/failed → approved (the
// restart transition, which zeroes JudgeRounds), preserves done members,
// resets failed/cancelled members to `next` (resumed from the last git commit
// if a commitResolver is wired; fresh attempt otherwise), clears the durable
// unmet signature, and increments the generation. The plan then re-enters
// the normal approved→running admission path on the next tick.
func (pe *PlanEngine) PlayPlan(ctx context.Context, planID string) (*PlayResult, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: get plan %q: %w", planID, err)
	}
	if p.State != plan.StateFailed {
		return nil, fmt.Errorf("plan_engine: PlayPlan: plan %q is %s, not failed (Play resumes a stopped/failed plan)",
			planID, p.State)
	}
	// The restart transition validates failed_reason == stopped_by_user
	// (plan.ValidateRestartTransition). This is the same gate the REST
	// /restart endpoint uses.
	approved := plan.StateApproved
	if _, err := pe.planStore.Update(planID, plan.Patch{State: &approved}); err != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: restart transition: %w", err)
	}

	// Reset failed/cancelled members to `next`; preserve done members.
	// Resume from last git commit if available (D13); fresh attempt otherwise.
	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: list member tasks: %w", err)
	}
	for i := range tasks {
		t := &tasks[i]
		if task.IsTerminal(t.Status) && t.Status != task.StatusDone {
			// Failed/cancelled member — reset for re-dispatch.
			if _, rerr := pe.taskStore.RestartReset(t.ID); rerr != nil {
				logger.WarnCF("plan_engine", "PlayPlan: could not reset member",
					map[string]any{"plan_id": planID, "task_id": t.ID, "error": rerr.Error()})
			}
			pe.recordMemberResumePoint(planID, t.ID)
		}
	}

	// Clear the durable unmet signature (fresh round on the new generation).
	pe.clearUnmetTerminalSignature(planID)
	clearSig := ""
	if _, err := pe.planStore.Update(planID, plan.Patch{LastUnmetTerminalSignature: &clearSig}); err != nil {
		logger.WarnCF("plan_engine", "PlayPlan: could not clear unmet signature",
			map[string]any{"plan_id": planID, "error": err.Error()})
	}

	// Mint a new generation (D13/G-12).
	prevGen := pe.planGeneration(planID)
	newGen := pe.incrementPlanGeneration(planID)

	logger.InfoCF("plan_engine", "plan played: new generation",
		map[string]any{"plan_id": planID, "generation": newGen, "resumed_from": prevGen})

	return &PlayResult{
		NewGeneration: newGen,
		ResumedFrom:   fmt.Sprintf("gen-%d", prevGen),
		PlanID:        planID,
	}, nil
}

// recordMemberResumePoint resolves the gitevidence checkpoint for a member
// being resumed via Play (D13: "resume from last git commit") and PERSISTS it
// on the task record as ResumeFromCommit — the resume baseline the worker turn
// and plan Judge consume (the next attempt's diff is measured from this hash).
//
// If no commitResolver is wired, or the resolver returns no commit (unborn
// repo / nested-repo degrade / member never committed), ResumeFromCommit is
// cleared to "" — the FR-155 fresh-attempt fallback, signalled in the log. The
// task was already RestartReset to `next` by the caller, which cleared any
// stale ResumeFromCommit; this call sets the value for the new generation.
func (pe *PlanEngine) recordMemberResumePoint(planID, taskID string) {
	pe.mu.Lock()
	cr := pe.commitResolver
	pe.mu.Unlock()

	var hash string
	switch {
	case cr == nil:
		logger.InfoCF("plan_engine", "member resume: fresh attempt (no commit resolver)",
			map[string]any{"plan_id": planID, "task_id": taskID})
	default:
		h, err := cr.LastMemberCommit(planID, taskID)
		if err != nil || h == "" {
			logger.InfoCF("plan_engine", "member resume: fresh attempt (no boundary commit)",
				map[string]any{"plan_id": planID, "task_id": taskID, "error": fmt.Sprintf("%v", err)})
		} else {
			hash = h
			logger.InfoCF("plan_engine", "member resume: from commit",
				map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash})
		}
	}

	// Persist the resolved baseline (hash, or "" for fresh attempt) on the
	// task so the worker turn / Judge start from it. Best-effort: a store
	// failure is logged, not fatal — the resume still proceeds (as a fresh
	// attempt if the hash couldn't be recorded).
	if _, err := pe.taskStore.Update(taskID, task.Patch{ResumeFromCommit: &hash}); err != nil {
		logger.WarnCF("plan_engine", "member resume: could not persist resume_from_commit",
			map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "error": err.Error()})
	}
}

// --- Owner-gaming-DoD guards (N-2) -----------------------------------------

// gamingGuardEvidence annotates a member-outcome snapshot for the Judge with
// gaming-guard metadata (N-2). The ladder weights deterministic rungs (check/
// behavior) over prose; artifacts produced AFTER the unmet verdict are flagged
// post-hoc so the Judge can weight them appropriately. This is a pure helper
// the Judge-input builder calls — it does NOT alter the verdict itself.
//
// postUnmetMemberIDs is the set of member IDs whose artifacts were produced
// after the plan entered awaiting_owner_correction (detected by comparing the
// member's CompletedAt against the plan's last unmet-verdict timestamp). The
// guard flags them in the extra-context text the Judge receives.
func gamingGuardEvidence(tasks []task.Task, superseded map[string]bool, postUnmetMemberIDs map[string]bool) string {
	if len(postUnmetMemberIDs) == 0 && len(superseded) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n## Gaming-guard (N-2)\n")
	sb.WriteString("The evidence ladder weights deterministic rungs (machine checks, behavior ")
	sb.WriteString("scans) over prose self-attestation. The following caveats apply:\n")
	if len(superseded) > 0 {
		sb.WriteString("- Superseded members (outcome ignored-by-Judge, record immutable): ")
		var ids []string
		for id := range superseded {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		sb.WriteString(strings.Join(ids, ", "))
		sb.WriteString("\n")
	}
	if len(postUnmetMemberIDs) > 0 {
		sb.WriteString("- Artifacts produced AFTER the unmet verdict (flagged post-hoc): ")
		var ids []string
		for id := range postUnmetMemberIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		sb.WriteString(strings.Join(ids, ", "))
		sb.WriteString("\n")
	}
	return sb.String()
}

// --- Boot reconstruction of corrections ------------------------------------

// reconstructCorrections rebuilds the in-memory superseded-member sets and
// plan-generation counters from the intent log's persisted revision entries.
// Called from bootReconcile after the intent-log replay but before
// processPlan, so the engine's correction state is consistent with the
// durable record. No-ops cleanly when no intent log is wired.
func (pe *PlanEngine) reconstructCorrections() {
	if pe.intentLog == nil {
		return
	}
	entries, err := os.ReadDir(pe.intentLog.Dir())
	if err != nil {
		if !os.IsNotExist(err) {
			logger.WarnCF("plan_engine", "reconstructCorrections: read intent dir failed",
				map[string]any{"error": err.Error()})
		}
		return
	}
	superCount := 0
	genCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		planID := strings.TrimSuffix(e.Name(), ".jsonl")
		records, err := pe.intentLog.List(planID)
		if err != nil {
			continue
		}
		maxGen := 0
		for _, rec := range records {
			if rec.Revision.Verb == plan.RevisionSupersede && rec.Revision.SupersededMemberID != "" {
				pe.markMemberSuperseded(planID, rec.Revision.SupersededMemberID)
				superCount++
			}
			if rec.Revision.Generation > maxGen {
				maxGen = rec.Revision.Generation
			}
		}
		if maxGen > 0 {
			pe.mu.Lock()
			if pe.planGenerations == nil {
				pe.planGenerations = make(map[string]int)
			}
			pe.planGenerations[planID] = maxGen
			pe.mu.Unlock()
			genCount++
		}
	}
	if superCount > 0 || genCount > 0 {
		logger.InfoCF("plan_engine", "correction state reconstructed",
			map[string]any{"superseded_members": superCount, "plans_with_generations": genCount})
	}
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
//
// ADR-053 C1/FR-147/FR-193 (INV-7 across restart): BEFORE re-running
// processPlan, the in-memory F2 round-burn gate is REHYDRATED from each
// running plan's persisted LastUnmetTerminalSignature. Without this, a
// restart would drop the in-memory map and the very first processPlan tick
// on an awaiting-owner-correction plan would burn one spurious JudgeRound
// re-judging identical all-terminal evidence — exactly the standalone-F2
// restart gap C1 closes. With it, processPlan -> beginPlanJudgeRound ->
// unmetTerminalSignatureUnchanged sees the rehydrated entry and skips,
// identical to the in-process behavior, and INV-7 holds across INV-9.
func (pe *PlanEngine) bootReconcile(ctx context.Context) {
	// ADR-053 Phase-2: reconstruct correction state (superseded members +
	// plan generations) from the intent log before reconciling plans.
	pe.reconstructCorrections()

	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.ErrorCF("plan_engine", "boot reconcile: list plans failed", map[string]any{"error": err.Error()})
		return
	}
	running := 0
	rehydrated := 0
	for i := range plans {
		if plans[i].State != plan.StateRunning {
			continue
		}
		running++
		// C1 durable rehydration: a plan parked at awaiting_owner_correction
		// with a persisted unmet signature re-arms the in-memory gate so the
		// unchanged all-terminal state is NOT re-judged on the first
		// post-restart tick. Plans not in that phase have an empty persisted
		// signature and skip this (their in-memory entry was never set).
		if plans[i].LastUnmetTerminalSignature != "" {
			pe.recordUnmetTerminalSignature(plans[i].ID, plans[i].LastUnmetTerminalSignature)
			rehydrated++
		}
		pe.processPlan(ctx, plans[i].ID)
	}
	logger.InfoCF("plan_engine", "boot reconciliation complete",
		map[string]any{"running_plans_scanned": running, "unmet_signatures_rehydrated": rehydrated})
}
