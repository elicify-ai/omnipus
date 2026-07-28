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
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	// executeTaskPlanVerified is dispatchReadyMembers' OWN documented bypass
	// of TaskExecutor's plan-state gate (requirePlanExecuting, task_executor.go)
	// — unexported so only this package's real implementation
	// (*TaskExecutor.executeTaskPlanVerified) and its test double
	// (fakePlanDispatcher, plan_engine_test.go) can satisfy this interface at
	// all, which is exactly the point: nothing outside pkg/agent can ever
	// reach this bypass. See executeTaskPlanVerified's own doc comment for
	// why dispatchReadyMembers — and ONLY dispatchReadyMembers — is safe
	// calling this instead of ExecuteTask.
	executeTaskPlanVerified(ctx context.Context, taskID string) error
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

	// planSupervisorAgentID is the id of the PlanSupervisor System Agent —
	// the adjudicator every SUPERVISION wake is addressed to (ADR-055/FR-012:
	// the two decision wakes, stall and DoD-UNMET, moved off the plan's owner
	// onto this agent; the three OUTCOME wakes stayed on the owner).
	//
	// It is a local literal rather than coreagent.IDPlanSupervisor ONLY
	// because the coreagent seed lands in a sibling change; the value is the
	// contract-documented id (`plansupervisor`, see Plan.yaml's plan_phase
	// description) and MUST equal coreagent.IDPlanSupervisor once that
	// constant exists. dispatchPlanTurn's own agent-resolution check is what
	// keeps a mismatch loud rather than silent: an unresolvable supervisor is
	// a recorded wake error that escalates to failed(supervision_unavailable),
	// never a turn quietly run by whatever agent happens to be default.
	planSupervisorAgentID = "plansupervisor"

	// defaultSupervisionTurnTimeout is FR-021's observation deadline: how
	// long after a supervision wake the engine waits before concluding the
	// adjudication turn produced nothing. The wake is fire-and-forget and no
	// seam reports a turn's outcome back into the engine, so the deadline —
	// not a callback — is the observation mechanism.
	//
	// It deliberately does NOT reuse the 10 s notify timeout below, which
	// bounds a bus publish rather than an LLM turn. Spec default: 600 s.
	defaultSupervisionTurnTimeout = 600 * time.Second

	// defaultSupervisionMaxAttempts is FR-022's ceiling: supervision wakes
	// that may be issued for one park before the plan terminates
	// failed(supervision_unavailable). Without a ceiling a single unusable
	// adjudication turn strands the plan until idle expiry (days).
	defaultSupervisionMaxAttempts = 3

	// planWakeNotifyTimeout bounds the bus publish an OWNER wake performs.
	planWakeNotifyTimeout = 10 * time.Second
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
	// whose plan is durably awaiting_supervision). Nil in a bare
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
	// from at boot for every plan still awaiting supervision. So a process
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
	// wakeWG tracks in-flight plan WAKE turns (dispatchPlanTurn) so Stop can
	// drain them on the same bounded budget as judge rounds. A wake turn is a
	// real agent turn writing to a real session — letting it outlive shutdown
	// races its transcript/cost writes against teardown.
	wakeWG sync.WaitGroup

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
		// Plan wake turns (the supervision dispatch and the origin-less owner
		// dispatch) are real agent turns on their own goroutines; drain them
		// on the same bounded budget so shutdown does not race their
		// session/transcript writes.
		pe.wakeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(planEngineStopDrainTimeout):
		logger.WarnCF("plan_engine",
			"stop: in-flight plan-judge round(s) or plan wake turn(s) still tearing down after drain timeout", nil)
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
	// REGRESSION FIX (blocked-derivation event-latch, code review on
	// bc66345f): re-check every STANDALONE (no PlanID) `blocked` task on
	// every tick, unconditionally — i.e. even when plans is empty. Plan
	// MEMBER tasks already get an equivalent defensive re-check via
	// promoteReadyMembers (called per running plan, above); see
	// promoteReadyStandaloneTasks' doc comment for why a standalone task
	// needs its own, plan-independent sweep.
	pe.promoteReadyStandaloneTasks()
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
	// Round-1 UAT finding #5 fix: a member attached to the plan before/at
	// Execute lands in `inbox` (task.normalize()'s default) — promote it to
	// `next` immediately on admission so the very first dispatch wave below
	// actually reaches it, rather than waiting for the next tick's
	// processPlan pass to notice it (see promoteInboxMembers' doc comment).
	if pe.promoteInboxMembers(updated, tasks) {
		tasks, lerr = pe.taskStore.List(task.Filter{PlanID: updated.ID})
		if lerr != nil {
			logger.WarnCF("plan_engine", "could not re-list member tasks after inbox promotion on start",
				map[string]any{"plan_id": planID, "error": lerr.Error()})
			return
		}
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

	// Round-1 UAT finding #5 fix: promote every inbox member of THIS
	// dispatchable plan to `next` before the ready/blocked-cascade pass and
	// dispatch below run — see promoteInboxMembers' own doc comment. This is
	// the per-tick sweep (not a one-shot at Execute/approval): a member
	// attached to an already-running plan gets promoted here on the very
	// next pass, exactly like one attached before Execute.
	if pe.promoteInboxMembers(p, tasks) {
		tasks, err = pe.taskStore.List(task.Filter{PlanID: planID})
		if err != nil {
			logger.WarnCF("plan_engine", "processPlan: re-list member tasks after inbox promotion failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
			return
		}
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
		return
	}

	// Round-1 UAT finding #5, "ALSO" half: the DAG is not all-terminal (real
	// work remains) yet nothing was dispatched this pass — if that is because
	// nothing is dispatchable or in-flight at all (not a transient dispatch
	// error, which would still have found a `next` member to retry), the plan
	// cannot make progress and must say so rather than spin on "Running 0/N"
	// forever. See surfaceStallIfAny's doc comment.
	pe.surfaceStallIfAny(p, tasks)
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

// promoteInboxMembers promotes every member task of a DISPATCHABLE plan
// (p.PermitsMemberDispatch()) that is still sitting in the derived-nothing
// `inbox` status to `next` (round-1 UAT finding #5: "an executed plan
// silently stalls forever when its members are in inbox"). A task attached
// to a plan — whether at Execute time (attached before/at approval) or
// later, to an ALREADY-running plan (attached via the task detail panel) —
// lands in `inbox` by default (task.normalize()); NEITHER dispatchReadyMembers
// (only ever looks at `next`) NOR promoteReadyMembers above (only ever
// cascades a DONE member's blocked dependents) ever look at `inbox`, so
// without this an inbox member is invisible to the plan engine forever — the
// plan sits at "Running 0/N" with no self-heal and no signal.
//
// Being a member of a plan the engine considers dispatchable IS the
// commitment `next` represents for a standalone task (there is no separate
// per-member approval step in this product) — so promoting a dispatchable
// plan's inbox member to `next` is completing the state transition Execute
// already authorized, not a policy relaxation.
//
// Gated STRICTLY by p.PermitsMemberDispatch() — the plan-dispatch gate stays
// the sole authority on whether a plan may promote/dispatch anything at all;
// a draft, cap-waiting-approved, done, or failed plan, or a paused running
// plan, promotes NOTHING. Every caller of this function has already re-read
// the plan's live State/PausedReason under planDecisionMu in the same
// critical section (mirrors dispatchReadyMembers' own doc comment on why
// that re-check is sufficient) — this is the same "single source of truth,
// checked once more defensively at the point of use" pattern used throughout
// this file.
//
// Delegates the actual write to the ordinary task.Store.Update path — NOT a
// bespoke direct write — so every existing invariant keeps applying
// unconditionally: inbox -> next is already a legal lifecycle transition
// (validateTransition, store.go), and recomputeBlockedStateLocked (store.go)
// runs as the UNCONDITIONAL terminal step of every Update, so a member whose
// blocked_by set has an unmet dependency is immediately and correctly
// re-derived to `blocked` by this SAME write — this function never inspects
// BlockedBy itself, and therefore can never promote a member whose
// dependencies are unmet. That member then promotes later via the EXISTING
// cascade (AdvanceBlockedDependents / promoteReadyMembers above) exactly like
// any other blocked member, once its dependency completes — no new
// promotion path is introduced for that case.
//
// Runs on EVERY processPlan pass (the per-tick sweep AND the reactive
// task_status_changed handler both call processPlan) — not a one-shot at
// Execute/approval — so a member attached to an ALREADY-running plan gets
// the identical self-heal on the very next pass, not just a plan's first
// dispatch wave. tryStartApprovedPlan additionally calls this once,
// immediately on approved->running admission, so a plan's initial member
// wave does not have to wait for the next tick to be promoted and dispatched.
func (pe *PlanEngine) promoteInboxMembers(p *plan.Plan, tasks []task.Task) (promotedAny bool) {
	if !p.PermitsMemberDispatch() {
		return false
	}
	next := task.StatusNext
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusInbox {
			continue
		}
		if _, err := pe.taskStore.Update(t.ID, task.Patch{Status: &next}); err != nil {
			logger.WarnCF("plan_engine", "promote inbox member to next failed",
				map[string]any{"plan_id": p.ID, "task_id": t.ID, "error": err.Error()})
			continue
		}
		promotedAny = true
	}
	return promotedAny
}

// promoteReadyStandaloneTasks re-derives the `blocked`/`next` state of every
// STANDALONE (PlanID == "") task currently sitting in the derived `blocked`
// side-state, once per engine Tick — regardless of whether any plan exists
// or is running. Plan MEMBER tasks already get an equivalent defensive
// re-check via promoteReadyMembers (called per RUNNING plan from processPlan,
// scoped to that plan's own DONE members via a PlanID-filtered task.Filter);
// a standalone task, by definition, falls outside that scope entirely, so it
// needs its own plan-independent sweep. Tasks with a non-empty PlanID are
// skipped here unconditionally so this never double-promotes a plan member
// or fights promoteReadyMembers' own pass over the same task.
//
// This closes the regression the blocked-derivation persistence fix (S2 UAT
// finding A) introduced: `blocked` became a durable, EVENT-triggered latch —
// recomputeBlockedStateLocked only ever runs inside Create/updateLocked/
// RestartReset/AddDependency/SpawnReset (see that function's own doc
// comment), so a standalone dependent whose blocker completes via a path
// that does not also re-trigger AdvanceBlockedDependents (a crash between
// the blocker's `done` write and the cascade, or a direct-write path like
// DropOrphanEdges) is stuck in `blocked` forever with no self-heal and no
// user-facing escape — the store rejects a client PATCH back to `next`
// (ErrBlockedNotSettable). Before the blocked-derivation fix, a dependent
// simply stayed `next` and CheckQueuedTasks' own per-tick dependency check
// re-verified it every heartbeat; `blocked` tasks are invisible to that
// dispatch-side filter (task.Filter{Status: task.StatusNext}), so this sweep
// is the replacement self-heal for the STANDALONE half of that lost
// self-healing behavior.
//
// Reuses AdvanceUnblocked (pkg/task/store.go) rather than a third status
// derivation, per the review's explicit ask: AdvanceUnblocked forces the
// task to `next` under the internal allowBlockedSet hatch and then
// unconditionally re-runs recomputeBlockedStateLocked as the terminal step
// of updateLocked — which snaps the task straight back to `blocked` if a
// dependency is genuinely still unmet. Calling it on every blocked
// standalone task, every tick, is therefore a safe, idempotent no-op
// whenever nothing has actually changed (the exact "re-checked every tick"
// self-healing property the blocked-derivation fix took away), and a
// correct promotion the moment a dependency really has completed.
func (pe *PlanEngine) promoteReadyStandaloneTasks() {
	tasks, err := pe.taskStore.List(task.Filter{Status: task.StatusBlocked})
	if err != nil {
		logger.WarnCF("plan_engine", "promoteReadyStandaloneTasks: list blocked tasks failed",
			map[string]any{"error": err.Error()})
		return
	}
	for i := range tasks {
		t := &tasks[i]
		if t.PlanID != "" {
			// Plan members are promoteReadyMembers' concern (per running
			// plan); skip to avoid double-promoting or racing it.
			continue
		}
		if _, err := pe.taskStore.AdvanceUnblocked(t.ID); err != nil {
			logger.WarnCF("plan_engine", "promoteReadyStandaloneTasks: advance failed",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
	}
}

// dispatchReadyMembers dispatches every member task currently `next`
// (FR-058). A task in `next` should always have every blocked_by dependency
// satisfied already (the store's own recompute keeps that invariant), but
// ExecuteTask re-verifies it independently regardless (task_executor.go) —
// dispatchReadyMembers does not duplicate that check. ExecuteTask is itself
// bounded by TaskExecutor's own global dispatch semaphore; a transient
// ErrDispatchCapReached (or any other dispatch error) is logged and simply
// retried on the next tick/event, never treated as fatal to the plan.
//
// Calls pe.dispatcher.executeTaskPlanVerified — NOT ExecuteTask — the ONE
// documented bypass of TaskExecutor's plan-state gate (requirePlanExecuting).
// Every caller of THIS function (tryStartApprovedPlan, processPlan,
// AppendCorrection) has already re-read planID's live State (and
// PausedReason) under pe.planDecisionMu, in the SAME critical section that
// then calls this, immediately before doing so — re-verifying it a second
// time inside ExecuteTask would be redundant, and would also make this
// dispatch depend on TaskExecutor's OWN, independently-wired plan.Store
// agreeing (see executeTaskPlanVerified's doc for why that's a real boot-
// ordering risk, not just an optimization concern).
func (pe *PlanEngine) dispatchReadyMembers(ctx context.Context, planID string, tasks []task.Task) (dispatchedAny bool) {
	// NewPlanEngine leaves this a TRUE nil interface when constructed with no
	// TaskExecutor (see its "typed nil inside an interface" note). Every other
	// pe.dispatcher call site guards; this one did not, and reached the nil
	// only once corrections became applicable at all — an engine with no
	// executor crashed the whole turn instead of reporting it.
	//
	// Loud, not silent: with no dispatcher NO member will ever run, so the
	// plan sits at dispatching forever. That is a wiring defect, not a
	// condition to swallow.
	if pe.dispatcher == nil {
		logger.ErrorCF("plan_engine", "no task dispatcher wired; plan members cannot be dispatched",
			map[string]any{"plan_id": planID, "ready_members": len(tasks)})
		return false
	}
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusNext {
			continue
		}
		if err := pe.dispatcher.executeTaskPlanVerified(ctx, t.ID); err != nil {
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

// stallHandoverNotePrefix marks a Plan.HandoverText value written by
// surfaceStallIfAny below — distinguishes "this is our own stall note, safe
// to compare for de-dup/clear" from any OTHER text a different code path
// wrote into HandoverText (e.g. the plan-judge's UNMET steering text, or a
// terminal-brake wind-down summary), which must never be clobbered or
// mistaken for a stall note.
const stallHandoverNotePrefix = "[stalled] "

// planStallReason inspects a RUNNING, dispatchable plan's freshest member
// snapshot (taken by the caller AFTER this pass's own inbox-promotion and
// blocked-cascade attempts, immediately before dispatch) and reports a
// plain-language reason the plan is stuck, or "" when it is not. "Stuck"
// here means: the DAG is NOT all-terminal (the caller checks
// allMembersTerminal first — a genuinely finished DAG goes to the plan
// judge, not here) AND no member is currently dispatchable (`next`) or in
// flight (`in_progress`) — i.e. this pass's dispatchReadyMembers call is
// guaranteed to have been a complete no-op.
//
// This is the "ALSO: THE SILENT PART IS ITS OWN BUG" half of round-1 UAT
// finding #5: even with inbox members now self-promoting
// (promoteInboxMembers), a member can still be legitimately `blocked` on a
// dependency this plan's own dispatch loop will never itself resolve (e.g. a
// blocker outside this plan's member set that nobody is running), or an
// inbox member whose promotion attempt itself failed (already logged at the
// point of failure). Either is a genuine "no progress possible without help"
// condition, and must not render as an indefinitely-spinning "Running" chip
// with nothing to explain why.
func planStallReason(tasks []task.Task) string {
	var blockedIDs, inboxIDs []string
	for i := range tasks {
		switch tasks[i].Status {
		case task.StatusNext, task.StatusInProgress:
			return "" // something is dispatchable or already running - not stalled
		case task.StatusBlocked:
			blockedIDs = append(blockedIDs, tasks[i].ID)
		case task.StatusInbox:
			inboxIDs = append(inboxIDs, tasks[i].ID)
		}
	}
	if len(blockedIDs) == 0 && len(inboxIDs) == 0 {
		return "" // no non-terminal, non-dispatchable member found
	}
	var sb strings.Builder
	sb.WriteString("This plan has no dispatchable or in-flight members, so it cannot make progress right now.")
	if len(blockedIDs) > 0 {
		fmt.Fprintf(&sb, " %d member(s) are blocked on an unmet dependency this plan cannot itself resolve: %s.",
			len(blockedIDs), strings.Join(blockedIDs, ", "))
	}
	if len(inboxIDs) > 0 {
		fmt.Fprintf(&sb, " %d member(s) could not be promoted from inbox: %s.",
			len(inboxIDs), strings.Join(inboxIDs, ", "))
	}
	sb.WriteString(" A correction (adjust dependencies, or Stop and re-author) is needed to unstick it.")
	return sb.String()
}

// surfaceStallIfAny persists planStallReason's verdict onto p.HandoverText
// AND p.PlanPhase (PhaseStalled), and wakes the owner exactly once per
// distinct stall condition — mirroring this file's other owner-wake decision
// points (plan_judge_unmet/plan_judge_met/plan_<failed_reason>/
// plan_stopped_by_user) with a further one (plan_stalled, FR-059's intent
// extended to this case): a running plan that can make no progress at all is
// exactly the kind of condition that must be surfaced rather than silently
// spun on.
//
// Wire visibility (swimlane-board UAT fix): PlanPhase=stalled is exposed via
// contracts/components/schemas/Plan.yaml's plan_phase enum and read
// generically by pkg/gateway/rest_plans.go's toWirePlan (which already
// forwards EffectivePlanPhase() verbatim — no code change needed there) and
// by the plan_status WS frame (contracts/asyncapi.yaml, so a live push isn't
// dropped by the SPA's Zod validation). The frontend renders it via
// src/lib/planStateColors.ts's planPhaseChip/planPhaseExplanation, mirroring
// the awaiting_supervision pattern exactly. HandoverText itself stays
// server-only (never wire-exposed) — it names internal task IDs meant for
// the owner AGENT's chat turn, not for the chip.
//
// De-duped by comparing against the CURRENTLY persisted HandoverText/
// PlanPhase (as read by the caller at the top of processPlan, under
// planDecisionMu, so this is race-free within one pass) — an unchanged stall
// condition across ticks re-wakes no one; a materially different one (a
// different blocked/inbox member set) does. Clears a stale stall note
// (recognized via stallHandoverNotePrefix, so this never touches text any
// OTHER path wrote) and reverts PlanPhase to PhaseDispatching once the plan
// is no longer stalled.
//
// PRECEDENCE (see plan.PhaseStalled's doc comment for the full rationale):
// awaiting_supervision is a strictly MORE SPECIFIC condition than a
// generic stall and must never be masked by it. This is guaranteed
// structurally (processPlan's own allMembersTerminal check always intercepts
// before this call while genuinely parked at awaiting_supervision) AND
// enforced explicitly below as a belt-and-suspenders guard, so a future
// refactor of that call order cannot silently reintroduce the bug.
//
// Caller must hold planDecisionMu and must only call this once
// allMembersTerminal(tasks) and planStuckAfterMemberCancel(tasks) have both
// already been ruled out (processPlan's own call order guarantees this).
func (pe *PlanEngine) surfaceStallIfAny(p *plan.Plan, tasks []task.Task) {
	if p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision {
		// Never mask a judge dead end with a generic stall note — see the
		// PRECEDENCE doc above. Structurally unreachable in production (see
		// plan.PhaseStalled's doc comment) but kept as an explicit guard.
		return
	}

	reason := planStallReason(tasks)
	if reason == "" {
		if strings.HasPrefix(p.HandoverText, stallHandoverNotePrefix) || p.EffectivePlanPhase() == plan.PhaseStalled {
			cleared := ""
			dispatching := plan.PhaseDispatching
			// The plan is LEAVING the supervision-eligible phase set, so the
			// wake receipt, the wake error and the attempt counter reset
			// (FR-050) — a later re-stall must re-wake rather than inherit a
			// spent deadline. correction_rounds and session_id survive.
			patch := clearSupervisionWakePatch(plan.Patch{HandoverText: &cleared, PlanPhase: &dispatching})
			if _, err := pe.planStore.Update(p.ID, patch); err != nil {
				logger.WarnCF("plan_engine", "could not clear stale stall note/phase",
					map[string]any{"plan_id": p.ID, "error": err.Error()})
			}
		}
		return
	}
	note := stallHandoverNotePrefix + reason
	if p.HandoverText == note && p.EffectivePlanPhase() == plan.PhaseStalled {
		// Already surfaced this exact condition — no repeat FIRST wake. The
		// re-wake for a stalled plan whose adjudication turn produced nothing
		// goes through the supervision deadline instead (FR-029): this guard
		// is a first-wake dedup keyed on its own persisted side effect, not a
		// deadline, and routing the re-wake through it would either fight the
		// guard or require mutating HandoverText to defeat it.
		pe.evaluateSupervisionDeadlineLocked(p)
		return
	}
	stalled := plan.PhaseStalled
	if _, err := pe.planStore.Update(p.ID, plan.Patch{HandoverText: &note, PlanPhase: &stalled}); err != nil {
		logger.WarnCF("plan_engine", "could not persist stall note",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}
	p.HandoverText = note
	p.PlanPhase = stalled
	// FR-012: the stall wake asks PlanSupervisor for a stall DIAGNOSIS, not a
	// DoD verdict, and not the owner — the owner has no correction role.
	pe.wakeSupervisor(p, fmt.Sprintf(
		"Plan %q is stalled: it is running with real work remaining, but no member is "+
			"dispatchable or in flight.\n\n%s\n\nDiagnose the stall and, if it is correctable, "+
			"apply a correction. This is a stall diagnosis, not a Definition-of-Done verdict.",
		p.Title, reason,
	), "plan_stalled")
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
				"plan judge round skipped: all-terminal state unchanged since last UNMET verdict (awaiting supervision)",
				map[string]any{"plan_id": p.ID})
			// FR-023: the parked plan's per-tick supervision pass. It sits
			// AFTER the unconditional round-ceiling check above by design —
			// an exhausted parked plan must terminate rather than re-wake
			// (the ceiling check is the first thing this function does).
			pe.evaluateSupervisionDeadlineLocked(p)
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
	// FR-178: JudgeRounds (plan/goal scope) and AttemptCount (per member/task)
	// are TWO DISTINCT brakes, never conflated. This line — the only place
	// JudgeRounds is incremented — is the SOLE writer of the plan's rounds
	// counter; it never touches a member task's AttemptCount, symmetric to
	// TaskExecutor.consumeAttemptOrExhaust being the sole writer of
	// AttemptCount (which never touches JudgeRounds). Whichever trips first
	// stops its OWN scope locally. Pinned by TestAttemptsVsRounds_DistinctBrakes.
	newRounds := current.JudgeRounds + 1
	pe.touchActivity(current.ID)

	if verdict.Met {
		pe.synthesizeAndComplete(current, newRounds)
		return
	}

	steering := buildPlanSteeringText(verdict)
	// ADR-053 C1/FR-147 (INV-2/INV-7): an UNMET verdict on an all-terminal DAG
	// (the only kind of state the plan Judge ever fires on) durably parks the
	// plan at plan_phase=awaiting_supervision — NOT back to dispatching —
	// persisting the unmet terminal signature so the F2 round-burn gate
	// survives restart. The plan-owner session sits at lifecycle `paused`
	// (Phase-2 owner loop's responsibility); the boot sweep exempts that
	// paused session from the failed(interrupted) sweep via OwnsPlanID ->
	// plan.PlanPhase == awaiting_supervision (FR-118 exemption b).
	awaiting := plan.PhaseAwaitingSupervision
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
	// Keep the in-memory snapshot consistent with what was just persisted —
	// wakeSupervisor below reads the phase through it.
	current.JudgeRounds = newRounds
	current.PlanPhase = awaiting
	current.HandoverText = steering
	current.LastUnmetTerminalSignature = sig
	// FR-012: the UNMET wake goes to the ADJUDICATOR, not the owner. The
	// owner has no correction role, so the message no longer says "awaiting
	// YOUR correction".
	pe.wakeSupervisor(current, fmt.Sprintf(
		"Plan %q round %d: the plan judge found the Definition of Done UNMET.\n\n%s"+
			"\n\nThe plan is parked awaiting supervision. Adjudicate it: append tail "+
			"members, SUPERSEDE a done member whose outcome is wrong, TARGETED-RETRY a "+
			"failed member, or ABANDON the plan if its Definition of Done is unreachable.",
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
	// ⚠ REGRESSION ANCHOR (FR-012/FR-012b): this wake target and its ordering
	// are pinned. This is the ONLY wake on the plan's success path — do NOT
	// re-target it to the supervisor, or a plan that SUCCEEDS notifies nobody.
	// The owner is also the right author of a closing synthesis: it is the
	// agent accountable to the requester and the only one holding the
	// requester's conversational context.
	p.PlanPhase = synthesizing
	pe.wakeOwner(p, fmt.Sprintf(
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
	pe.wakeOwner(updated, fmt.Sprintf(
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
	// The plan's OWNER session — the owner agent's continuous context for
	// this plan (FR-016c). Stop cancels it like any session. Until ADR-055
	// this leg was a production no-op: the id it cancelled ("plan:<id>") named
	// a session nothing ever created, and because the fan-out discards
	// RequestCancelForSession's "did it fire" result, a leg that cancelled
	// nothing was indistinguishable from one that worked.
	if p.OwnerSessionID != "" {
		sessions = append(sessions, p.OwnerSessionID)
	}
	// FR-044 — the kill switch: an in-flight ADJUDICATION turn is working on
	// this plan too, and "stop the plan" must stop everything working on it.
	// Cancelling a supervision session whose turn already finished is a benign
	// no-op, which is exactly why the id is retained on the record rather than
	// cleared when the plan leaves the supervision-eligible phase set.
	if p.Supervision != nil && p.Supervision.SessionID != "" {
		sessions = append(sessions, p.Supervision.SessionID)
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
	pe.wakeOwner(updated, handover, "plan_stopped_by_user")
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

// --- Plan wakes (ADR-055 FR-012/FR-012c/FR-012d, two families) -------------
//
// A plan wake must DISPATCH AN AGENT TURN. Publishing an event that a
// downstream guard discards is not a wake, and until ADR-055 that is exactly
// what every plan wake did: wakeOwner hardcoded AsyncNotifyEvent.Channel =
// "system", Notify composes the bus ChatID as "<event.Channel>:<event.ChatID>"
// (async_notifier.go), and processSystemMessage parses that prefix back out and
// returns early because "system" is an internal channel (pkg/constants) — so
// all five wake sites dead-ended at one INFO log with no turn dispatched.
//
// ⚠ bus.InboundMessage.Channel STAYS "system". That value is the routing key
// loop.go matches to reach processSystemMessage at all, and
// processSystemMessage rejects any other channel at entry. Only the EVENT's
// origin channel was wrong. Two prior analyses of this defect proposed
// changing the bus channel; both were wrong. Do not "fix" it.
//
// The five sites fork into the two families FR-012 already splits them into,
// because their delivery requirements are opposites:
//
//	(A) SUPERVISION wakes — surfaceStallIfAny, applyJudgeRoundOutcomeLocked's
//	    UNMET limb. Target: PlanSupervisor. Seam: DIRECT dispatch to the agent
//	    loop, no bus, no notifier, SendResponse=false. The adjudicator's
//	    deliberation MUST NOT reach the owner's conversation (FR-016), and
//	    processSystemMessage hardcodes SendResponse=true with no suppression
//	    knob, so any origin it is handed receives that output.
//
//	(B) OWNER wakes — synthesizeAndComplete's MET synthesis, failPlanLocked,
//	    StopPlan. Target: p.OwnerAgentID. Seam: the notifier/bus, unchanged in
//	    shape, addressed to the plan's REAL chat origin, SendResponse=true.
//
// ⚠ The MET-synthesis wake stays on the OWNER. It is the only wake on the
// success path (failPlanLocked fires only on failure, StopPlan only on user
// stop), so re-targeting it would leave a plan that SUCCEEDS notifying nobody
// — neither the owner agent nor the human who authored it. Nothing wires a
// PlanSupervisor synthesis back: it is denied every write tool and the
// correction payload carries no synthesis field.

// originCanDeliver reports whether a plan's recorded chat origin can actually
// carry an owner wake to a human — the question FR-012c is really asking.
//
// Two ways it cannot, and only the first was previously handled:
//
//  1. The origin is EMPTY (a Plans-UI/REST-created plan). Notify rejects an
//     empty destination.
//
//  2. The origin is an INTERNAL channel — `cli`, `system`, `subagent`
//     (pkg/constants). A plan created inside a CLI turn records
//     SourceChannel="cli" (pkg/tools/plan.go), which is NON-EMPTY, so the
//     old populated-ness predicate sent it down the notifier leg. It then
//     died one layer downstream: processSystemMessage drops any internal
//     origin channel, so NO owner turn ran. That is precisely the silent-drop
//     defect FR-012c exists to close, and it stayed live for every cli-origin
//     plan because the predicate tested the wrong property.
//
// Matching AsyncNotifier.Notify's own both-fields-non-empty check is
// necessary but NOT sufficient: the drop that loses the wake happens BELOW
// Notify, so a predicate that only agrees with Notify still loses case 2.
// Both cases take the direct-dispatch leg, where the turn always runs.
func originCanDeliver(sourceChannel, sourceChatID string) bool {
	if sourceChannel == "" || sourceChatID == "" {
		return false
	}
	return !constants.IsInternalChannel(sourceChannel)
}

// wakeOwner delivers a plan OUTCOME to the plan's owner agent (family B) and
// guarantees it lands somewhere durable.
//
// The turn's transcript session is the plan's OWN owner session, minted here
// on first use (ensureOwnerSessionLocked, FR-016c) so the closing synthesis is
// persisted even when the origin channel's client is gone.
//
// Origin handling (FR-012d(4)): the chat leg is taken only when the plan's
// origin can ACTUALLY DELIVER. That is deliberately NOT the same as "both
// fields are populated" — see originCanDeliver. A plan created through the
// Plans UI legitimately has no origin, and passing its empty fields to Notify
// would return an error that the supervision escalation ladder reads as "the
// supervisor is unavailable" — terminating a perfectly healthy plan with a
// loud, false diagnosis. Instead the owner turn is dispatched DIRECTLY
// (SendResponse=false): "no chat to deliver to" must never mean "no turn ran".
//
// Caller must hold planDecisionMu (every call site already does).
func (pe *PlanEngine) wakeOwner(p *plan.Plan, content, sourceKind string) {
	sessionID := pe.ensureOwnerSessionLocked(p)

	if !originCanDeliver(p.SourceChannel, p.SourceChatID) {
		// FR-012d(5): a wake with no chat origin is NOT a failure. It is
		// logged distinguishably and MUST NOT be recorded as a wake error,
		// MUST NOT increment the supervision attempt count, and MUST NOT
		// contribute to failed_reason=supervision_unavailable.
		logger.InfoCF("plan_engine", "plan owner wake has no chat origin; dispatching the owner turn directly",
			map[string]any{
				"plan_id":        p.ID,
				"owner_agent_id": p.OwnerAgentID,
				"source_kind":    sourceKind,
				"reason":         "no_chat_origin",
			})
		if err := pe.dispatchPlanTurn(p.ID, p.OwnerAgentID, sessionID, content, sourceKind, pe.supervisionTurnTimeout(p)); err != nil {
			logger.ErrorCF("plan_engine", "could not dispatch origin-less plan owner turn",
				map[string]any{"plan_id": p.ID, "owner_agent_id": p.OwnerAgentID, "error": err.Error()})
		}
		return
	}

	if pe.notifier == nil {
		logger.ErrorCF("plan_engine", "no async notifier configured; plan owner wake not delivered",
			map[string]any{"plan_id": p.ID, "source_kind": sourceKind})
		return
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), planWakeNotifyTimeout)
	defer cancel()
	if err := pe.notifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:             p.SourceChannel,
		ChatID:              p.SourceChatID,
		AgentID:             p.OwnerAgentID,
		TranscriptSessionID: sessionID,
		SourceKind:          sourceKind,
		Content:             content,
	}); err != nil {
		logger.ErrorCF("plan_engine", "could not wake plan owner",
			map[string]any{
				"plan_id":     p.ID,
				"channel":     p.SourceChannel,
				"source_kind": sourceKind,
				"error":       err.Error(),
			})
	}
}

// wakeSupervisor issues a SUPERVISION wake (family A) for a plan sitting in
// the supervision-eligible phase set: it mints (or reuses) the park's
// adjudication session, stamps the durable wake receipt + attempt counter that
// arm FR-021's deadline, and dispatches PlanSupervisor's turn directly.
//
// No outbound message is published on this path at all — not to the plan's
// origin, not anywhere. That is a property of the seam (there is no publish to
// suppress), not a send that happens to fail.
//
// Caller must hold planDecisionMu.
func (pe *PlanEngine) wakeSupervisor(p *plan.Plan, content, sourceKind string) {
	sessionID := pe.ensureSupervisionSessionLocked(p)

	// ⚠ PERSIST THE HANDLE BEFORE DISPATCHING THE TURN. This is the same
	// assign-before-dispatch rule the verifier registry already follows, and
	// for the same reason: a Stop landing in the window between "the turn is
	// running" and "the record names its session" would find nothing to
	// cancel, and — because the cancel fan-out discards its own "did it fire"
	// result — would report success. Ordering the other way round leaves an
	// uncancellable turn for exactly as long as one store write takes.
	attempts := 1
	if p.Supervision != nil {
		attempts = p.Supervision.Attempts + 1
	}
	wakeAt := pe.clock.Now().UTC().Format(time.RFC3339)
	noError := ""
	patch := plan.Patch{
		SupervisionWakeAt:    &wakeAt,
		SupervisionAttempts:  &attempts,
		SupervisionWakeError: &noError,
	}
	if sessionID != "" {
		patch.SupervisionSessionID = &sessionID
	}
	updated, err := pe.planStore.Update(p.ID, patch)
	if err != nil {
		logger.ErrorCF("plan_engine", "could not persist supervision wake receipt; wake not dispatched",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}
	// Keep the caller's snapshot current: several call sites read
	// p.Supervision again in the same locked body.
	p.Supervision = updated.Supervision

	if dispatchErr := pe.dispatchPlanTurn(p.ID, planSupervisorAgentID, sessionID, content, sourceKind, pe.supervisionTurnTimeout(p)); dispatchErr != nil {
		// FR-024 / §20: an undelivered supervision wake is RECORDED on the
		// plan, not WARNed away — "parked" and "silently stuck" must be
		// distinguishable from the record alone. Unlike a missing chat origin
		// this IS a real failure, so escalating it to
		// failed(supervision_unavailable) at the ceiling is a TRUE diagnosis.
		logger.ErrorCF("plan_engine", "supervision wake could not dispatch an adjudication turn",
			map[string]any{
				"plan_id":     p.ID,
				"agent_id":    planSupervisorAgentID,
				"source_kind": sourceKind,
				"attempt":     attempts,
				"error":       dispatchErr.Error(),
			})
		wakeErr := dispatchErr.Error()
		if errored, uerr := pe.planStore.Update(p.ID, plan.Patch{SupervisionWakeError: &wakeErr}); uerr != nil {
			logger.ErrorCF("plan_engine", "could not record the supervision wake error",
				map[string]any{"plan_id": p.ID, "error": uerr.Error()})
		} else {
			p.Supervision = errored.Supervision
		}
		return
	}
	logger.InfoCF("plan_engine", "supervision wake dispatched",
		map[string]any{
			"plan_id":     p.ID,
			"session_id":  sessionID,
			"source_kind": sourceKind,
			"attempt":     attempts,
		})
}

// evaluateSupervisionDeadlineLocked is FR-021's observation seam and FR-022's
// post-turn state machine, run once per tick for a plan sitting in the
// supervision-eligible phase set.
//
// Why a deadline and not a callback: the wake is fire-and-forget and the turn
// path reports nothing back into this engine (N8). A callback would create a
// new coupling for a signal the engine can already infer — it owns the plan
// record, and whether the record MOVED is a complete proxy for whether the
// adjudication turn produced anything. It is also the shape every other brake
// here already has (round ceiling, idle expiry).
//
// The predicate, all limbs required:
//
//	phase ∈ {awaiting_supervision, stalled}   (the caller's own gate too)
//	supervision.wake_at is set                (a wake was actually issued)
//	now > wake_at + supervision_turn_timeout  (STRICTLY greater)
//	the unmet-terminal signature is unchanged
//
// The signature limb is VACUOUS on the stall path — no unmet signature is ever
// set there — so for a stalled plan the operative limbs are the phase, the
// wake receipt and the elapsed deadline. Reading "signature unchanged" as
// "a signature exists" would make the predicate unreachable for every stall.
//
// wake_at rehydrates from disk and is never re-armed at boot, so the deadline
// is honoured from its ORIGINAL stamp across a restart and a restart loop
// cannot reset the ceiling.
//
// Caller must hold planDecisionMu.
func (pe *PlanEngine) evaluateSupervisionDeadlineLocked(p *plan.Plan) {
	if !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase()) {
		return
	}
	if p.Supervision == nil || p.Supervision.WakeAt == "" {
		return // no wake issued for this park yet; nothing to time out
	}
	wakeAt, err := time.Parse(time.RFC3339, p.Supervision.WakeAt)
	if err != nil {
		logger.WarnCF("plan_engine", "supervision wake_at is unparseable; deadline not evaluated",
			map[string]any{"plan_id": p.ID, "wake_at": p.Supervision.WakeAt, "error": err.Error()})
		return
	}
	if p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision && p.LastUnmetTerminalSignature == "" {
		// The signature is written once on the UNMET verdict and cleared only
		// by an applied correction, so "still set" IS "unchanged". Cleared
		// means the record moved — the turn produced something.
		return
	}
	if !pe.clock.Now().UTC().After(wakeAt.Add(pe.supervisionTurnTimeout(p))) {
		return // strict >: at exactly wake_at+timeout the deadline does NOT fire
	}

	maxAttempts := pe.supervisionMaxAttempts(p)
	if p.Supervision.Attempts < maxAttempts {
		// FR-022(a): re-issue, WITHOUT waking the owner — there is nothing to
		// tell it yet. wakeSupervisor stamps a fresh receipt and increments
		// the attempt count.
		logger.InfoCF("plan_engine", "supervision turn produced nothing before its deadline; re-issuing the wake",
			map[string]any{
				"plan_id":      p.ID,
				"plan_phase":   string(p.EffectivePlanPhase()),
				"attempts":     p.Supervision.Attempts,
				"max_attempts": maxAttempts,
			})
		pe.wakeSupervisor(p, buildSupervisionRetryWakeText(p), "plan_supervision_retry")
		return
	}

	// FR-022(b): the ceiling is spent. Terminate with a reason distinct from
	// every other terminal cause, and wake the owner with a handover that says
	// adjudication was unavailable — NOT that the plan's work failed.
	logger.ErrorCF("plan_engine", "supervision attempt ceiling exhausted; terminating the plan",
		map[string]any{
			"plan_id":      p.ID,
			"plan_phase":   string(p.EffectivePlanPhase()),
			"attempts":     p.Supervision.Attempts,
			"max_attempts": maxAttempts,
			"wake_error":   p.Supervision.WakeError,
		})
	pe.failPlanLocked(p.ID, plan.FailedReasonSupervisionUnavailable,
		buildSupervisionUnavailableHandover(p, maxAttempts))
}

// supervisionTurnTimeout resolves FR-021's observation deadline for p:
// a per-plan Bounds override wins, else the global
// planning.supervision_turn_timeout_seconds, else the documented 600 s default.
//
// pkg/config returns SECONDS (it deliberately carries no scheduling
// semantics); the conversion to a Duration happens here, and this accessor is
// the ONLY place it happens.
func (pe *PlanEngine) supervisionTurnTimeout(p *plan.Plan) time.Duration {
	var override *int
	if p != nil && p.Bounds != nil {
		override = p.Bounds.SupervisionTurnTimeoutSeconds
	}
	secs := pe.planningConfig().EffectiveSupervisionTurnTimeoutSeconds(override)
	return time.Duration(secs) * time.Second
}

// supervisionMaxAttempts resolves FR-022's no-correction attempt ceiling for
// p: a per-plan Bounds override wins, else the global
// planning.supervision_max_attempts, else the documented default of 3.
func (pe *PlanEngine) supervisionMaxAttempts(p *plan.Plan) int {
	var override *int
	if p != nil && p.Bounds != nil {
		override = p.Bounds.SupervisionMaxAttempts
	}
	return pe.planningConfig().EffectiveSupervisionMaxAttempts(override)
}

// dispatchPlanTurn runs a plan wake as a REAL agent turn, bound to a REAL
// store-backed transcript session, with no outbound publish.
//
// This is the in-repo pattern the Judge's verifier dispatch already uses and
// which already works: mint a session, then hand its id to processTaskDirect
// as the transcript session id. processTaskDirect sets SendResponse=false and
// stamps TranscriptSessionID onto the turn state — the exact field
// RequestCancelForSession range-matches on, which is what makes a plan-scoped
// Stop able to halt the turn (a derived or composed id such as
// "plan:<id>" resolves to no store, leaves the turn with an empty
// transcriptSessionID, and is therefore uncancellable).
//
// The turn runs on its own goroutine: every caller holds planDecisionMu, the
// process-wide plan decision lock, and an LLM turn must never run under it.
// Returns an error only for a DISPATCH failure (no loop wired, agent does not
// resolve, no session) — never for the turn's own outcome, which the engine
// observes through FR-021's deadline rather than a callback.
// turnTimeout bounds a wedged provider and MUST be the SAME number the
// engine's own FR-021 observation deadline uses (supervisionTurnTimeout) —
// otherwise a per-plan bounds override moves the deadline the engine watches
// while leaving the turn itself running against the package default, and the
// override silently does not apply to the thing it names.
func (pe *PlanEngine) dispatchPlanTurn(planID, agentID, sessionID, prompt, sourceKind string, turnTimeout time.Duration) error {
	if pe.agentLoop == nil {
		return fmt.Errorf("plan_engine: no agent loop wired; cannot dispatch a plan turn for %q", planID)
	}
	if agentID == "" {
		return fmt.Errorf("plan_engine: plan %q names no agent to wake", planID)
	}
	if sessionID == "" {
		return fmt.Errorf("plan_engine: no transcript session for plan %q; refusing to dispatch an unpersisted turn", planID)
	}
	// Resolve the agent HERE rather than letting processTaskDirect fall back
	// to the default agent: a supervision turn silently run by whichever
	// agent happens to be default would leak the adjudication into an
	// unrelated roster member and report success.
	reg := pe.agentLoop.GetRegistry()
	if reg == nil {
		return fmt.Errorf("plan_engine: no agent registry; plan %q wake not dispatched", planID)
	}
	if _, ok := reg.GetAgent(agentID); !ok {
		return fmt.Errorf("plan_engine: agent %q does not resolve; plan %q wake not dispatched", agentID, planID)
	}

	al := pe.agentLoop
	sessionKey := fmt.Sprintf("agent:%s:session:%s", agentID, sessionID)
	pe.wakeWG.Add(1)
	go func() {
		defer pe.wakeWG.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("plan_engine", "panic in plan wake turn (recovered)",
					map[string]any{"plan_id": planID, "agent_id": agentID, "panic": fmt.Sprint(r)})
			}
		}()
		// Not derived from the engine's stop channel: the turn's cancellation
		// authority is RequestCancelForSession against sessionID (the Stop
		// fan-out), and the timeout bounds a wedged provider.
		turnCtx, cancel := context.WithTimeout(context.Background(), turnTimeout)
		defer cancel()
		if _, err := al.processTaskDirect(turnCtx, agentID, prompt, sessionKey, sessionID); err != nil {
			logger.WarnCF("plan_engine", "plan wake turn ended with an error",
				map[string]any{
					"plan_id":     planID,
					"agent_id":    agentID,
					"session_id":  sessionID,
					"source_kind": sourceKind,
					"error":       err.Error(),
				})
		}
	}()
	return nil
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

// buildSupervisionRetryWakeText is the text of a RE-ISSUED supervision wake
// (FR-022(a)). It names the attempt so the adjudicator can tell a retry from a
// first wake, and re-states the plan's own diagnosis (the judge's steering, or
// the stall note) because the previous turn left no artefact behind.
func buildSupervisionRetryWakeText(p *plan.Plan) string {
	attempt := 0
	if p.Supervision != nil {
		attempt = p.Supervision.Attempts
	}
	return fmt.Sprintf(
		"Plan %q is still awaiting supervision after attempt %d produced no correction.\n\n%s\n\n"+
			"Adjudicate it now: append tail members, SUPERSEDE a done member whose outcome is "+
			"wrong, TARGETED-RETRY a failed member, or ABANDON the plan if its Definition of "+
			"Done is unreachable.",
		p.Title, attempt, p.HandoverText,
	)
}

// buildSupervisionUnavailableHandover is FR-022(b)'s terminal handover. It is
// deliberately distinct from every other terminal message: the plan's WORK did
// not fail and its round budget was not spent — adjudication never produced a
// usable outcome, which is an operator-facing condition, not a plan-facing one.
func buildSupervisionUnavailableHandover(p *plan.Plan, maxAttempts int) string {
	return fmt.Sprintf(
		"Plan %q could not be reviewed: %d supervision attempt(s) were issued and none produced "+
			"a correction, so the plan has been stopped.\n\n"+
			"This is an ADJUDICATION failure, not a verdict on the plan's work. The plan was "+
			"parked at %q with the following diagnosis:\n\n%s\n\n"+
			"No other agent inherits adjudication for this plan.",
		p.Title, maxAttempts, p.EffectivePlanPhase(), p.HandoverText,
	)
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
func (pe *PlanEngine) Admit(kind string) (ok bool, active, maxConcurrent int) {
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

// CorrectionVerb is the owner-correction verb (FR-143/G-11), re-exported as an
// alias of plan.RevisionVerb (FR-004) so the verb the engine switches on and
// the verb the plan_correct tool sends are one type, not two that convert.
type CorrectionVerb = plan.RevisionVerb

const (
	CorrectionAppend        CorrectionVerb = plan.RevisionAppend
	CorrectionSupersede     CorrectionVerb = plan.RevisionSupersede
	CorrectionTargetedRetry CorrectionVerb = plan.RevisionTargetedRetry
	CorrectionAbandon       CorrectionVerb = plan.RevisionAbandon
)

// ErrCorrectionNotAdjudicator is returned by AppendCorrection when the
// invoking principal is not the PlanSupervisor (ADR-055 D3/FR-6, sec-MAJOR-2).
//
// It replaces the retired ErrCorrectionNotOwner, whose rule was the exact
// inverse: the engine used to admit ONLY the plan's OwnerAgentID, while the
// plan_correct tool one layer above admits ONLY `plansupervisor`. Because a
// System Agent can never be a plan's owner (validatePlanOwnerAgentForTool
// rejects any owner that is not a chat target), the two admissible sets were
// disjoint and EVERY correction was denied — ADR-055's headline feature was
// inert. See requireCorrectionAuthority.
var ErrCorrectionNotAdjudicator = errors.New("plan_engine: correction caller is not the plan adjudicator")

// CorrectionCaller identifies the principal invoking AppendCorrection
// (sec-MAJOR-2). The engine gates every correction on AgentID alone, and only
// the PlanSupervisor System Agent is admitted (ADR-055 D3/FR-4/FR-6):
//
//   - AgentID must equal planSupervisorAgentID. Correction is the
//     adjudicator's verb; the plan's OWNER has no correction role whatsoever
//     (FR-4) and is denied by the same opaque message as any stranger, so
//     "it is my plan" is not a way in.
//   - SessionID is carried for the audit trail ONLY. It is deliberately NOT
//     gated: the retired session clause compared it against the plan's
//     OwnerSessionID, which locked even the owner out of its own plan from
//     its own chat session (ADR-055 decision 9). OwnerSessionID itself stays
//     on the plan record — ADR-055 D7 explicitly declines to delete it here.
//
// Callers (system tool or an internal supervision loop) MUST resolve the
// invoking principal's real agent identity — there is no "trusted internal
// caller" bypass.
//
// not-wire-format: engine-internal type; the REST/tool layer maps its
// authenticated principal to this.
//
// CorrectionCaller, CorrectionRequest and CorrectionResult are re-exported
// here as aliases of the pkg/plan declarations (FR-004) so callers importing
// pkg/agent still get a single-package API, while pkg/tools — which cannot
// import pkg/agent — names the identical types. IntentEdge below is the
// in-repo precedent this follows.
type CorrectionCaller = plan.CorrectionCaller

// CorrectionRequest is an owner correction to an unmet DoD (FR-143/G-11). The
// owner issues one verb; each records a revision entry committed
// transactionally via the intent-log (INV-6/N-8).
//
// not-wire-format: engine-internal type; the REST/tool layer maps its wire
// type to this.
type CorrectionRequest = plan.CorrectionRequest

// IntentEdge is re-exported here (alias of plan.IntentEdge) so callers importing
// pkg/agent get a single-package API for corrections. The authoritative type
// lives in pkg/plan (intent_log.go) where the intent-log store uses it.
type IntentEdge = plan.IntentEdge

// CorrectionResult is the outcome of processing a correction.
//
// not-wire-format: engine-internal type.
type CorrectionResult = plan.CorrectionResult

// commitResolver resolves the last boundary commit hash for a plan member
// (the gitevidence checkpoint) and materializes the member's resume working
// tree at that commit. Used by Play to resume failed/cancelled members from
// the last commit (D13/G-12, #537). nil = no git evidence available
// (Play falls back to fresh attempt, signalled).
type commitResolver interface {
	LastMemberCommit(planID, taskID string) (hash string, err error)
	// ResetMemberCheckout materializes the member's isolated working tree
	// at hash via the gitevidence isolation ladder (replacing any prior
	// resume tree for the member), returning the checkout directory.
	// hash == "" removes any stale resume tree and returns "" — the
	// fresh-attempt path leaves no tree behind. A materialization error
	// degrades the member to the shared-tree resume (the baseline hash is
	// still persisted); it is never fatal to Play.
	ResetMemberCheckout(planID, taskID, hash string) (dir string, err error)
}

// SetCommitResolver installs the gitevidence checkpoint resolver for
// Play-from-commit (D13/G-12). Optional; nil (the default) means Play falls
// back to fresh attempt for every failed/cancelled member.
func (pe *PlanEngine) SetCommitResolver(cr commitResolver) {
	pe.mu.Lock()
	pe.commitResolver = cr
	pe.mu.Unlock()
}

// --- Plan session management (ADR-055 FR-016b/FR-016c) ---------------------
//
// A plan owns TWO sessions and they are deliberately disjoint: the OWNER's
// (one per plan, the owner agent's continuous context for it) and the
// SUPERVISION session (one per park, PlanSupervisor's adjudication
// transcript). Keeping the adjudicator's reasoning out of the owner's
// transcript is a requirement, not a preference — either party's turn can read
// the other's history, and two minted sessions is how the separation is
// realised.
//
// Both MUST be REAL, store-backed sessions. A derived or composed id
// ("plan:<id>") is forbidden and is the defect this replaces: nothing in the
// tree ever CREATED that session, so processSystemMessage's transcript
// resolution (which resolves by GetMeta against a real store) dropped it, the
// turn ran with an empty transcriptSessionID, and RequestCancelForSession —
// which matches on exactly that value — found nothing to cancel. Every test
// of that cascade passed anyway, because the fake canceller records the string
// it was handed and returns success.

// mintPlanSession creates a fresh, store-backed session owned by agentID and
// returns its opaque id. Mirrors the engine-minted verifier session the Judge
// already uses (a session the Stop fan-out demonstrably cancels), reusing the
// task session type because a plan wake is exactly that shape: an
// engine-dispatched background turn for one agent.
func (pe *PlanEngine) mintPlanSession(agentID, title string) (string, error) {
	if pe.agentLoop == nil {
		return "", fmt.Errorf("plan_engine: no agent loop wired; cannot mint a session for agent %q", agentID)
	}
	if agentID == "" {
		return "", fmt.Errorf("plan_engine: cannot mint a session for an empty agent id")
	}
	if pe.agentLoop.GetRegistry() == nil {
		return "", fmt.Errorf("plan_engine: no agent registry; cannot mint a session for agent %q", agentID)
	}
	store := pe.agentLoop.GetAgentStore(agentID)
	if store == nil {
		return "", fmt.Errorf("plan_engine: agent %q has no resolvable session store", agentID)
	}
	meta, err := store.NewSession(session.SessionTypeTask, "system", agentID)
	if err != nil {
		return "", fmt.Errorf("plan_engine: mint session for agent %q: %w", agentID, err)
	}
	if err := store.SetMeta(meta.ID, session.MetaPatch{Title: &title}); err != nil {
		// Cosmetic only — the session exists and is fully usable.
		logger.WarnCF("plan_engine", "could not title a plan session",
			map[string]any{"session_id": meta.ID, "error": err.Error()})
	}
	return meta.ID, nil
}

// ensureOwnerSessionLocked returns the plan's owner session id, minting and
// persisting one on first use (FR-016c). One session per plan, for the plan's
// lifetime — never re-minted; it is the owner's continuous context for this
// plan, and it is what StopPlan's owner-session cancel leg names.
//
// Returns "" when the session could not be established, which is surfaced at
// ERROR rather than swallowed: an empty OwnerSessionID means the owner turn's
// output is not persisted anywhere and forfeits the boot-sweep exemption.
// Caller must hold planDecisionMu.
func (pe *PlanEngine) ensureOwnerSessionLocked(p *plan.Plan) string {
	if p.OwnerSessionID != "" {
		return p.OwnerSessionID
	}
	sessionID, err := pe.mintPlanSession(p.OwnerAgentID, "Plan: "+p.Title)
	if err != nil {
		logger.ErrorCF("plan_engine", "could not mint the plan owner session",
			map[string]any{"plan_id": p.ID, "owner_agent_id": p.OwnerAgentID, "error": err.Error()})
		return ""
	}
	if _, err := pe.planStore.Update(p.ID, plan.Patch{OwnerSessionID: &sessionID}); err != nil {
		logger.ErrorCF("plan_engine", "could not persist owner_session_id",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return ""
	}
	p.OwnerSessionID = sessionID
	logger.InfoCF("plan_engine", "owner session opened for plan",
		map[string]any{"plan_id": p.ID, "owner_session_id": sessionID})
	return sessionID
}

// ensureSupervisionSessionLocked returns the adjudication session id for the
// CURRENT park, minting one on the park's first wake (FR-016b).
//
// One session per park: re-wakes within the same park share it, so a Stop can
// cancel whichever attempt is in flight; a new park mints a new one. The park
// boundary is read off supervision.wake_at — the wake receipt, which is
// cleared exactly when the plan leaves the supervision-eligible phase set —
// rather than off session_id, which is deliberately NEVER cleared (an applied
// correction returns the plan to dispatching while the adjudication turn may
// still be running, and blanking the handle in that window would leave a Stop
// unable to name the turn it must cancel).
//
// Caller must hold planDecisionMu.
func (pe *PlanEngine) ensureSupervisionSessionLocked(p *plan.Plan) string {
	if p.Supervision != nil && p.Supervision.WakeAt != "" && p.Supervision.SessionID != "" {
		return p.Supervision.SessionID // same park, same session
	}
	sessionID, err := pe.mintPlanSession(planSupervisorAgentID, "Plan supervision: "+p.Title)
	if err != nil {
		logger.ErrorCF("plan_engine", "could not mint the plan supervision session",
			map[string]any{"plan_id": p.ID, "agent_id": planSupervisorAgentID, "error": err.Error()})
		return ""
	}
	return sessionID
}

// clearSupervisionWakePatch returns the per-field supervision patch applied
// when a plan LEAVES the supervision-eligible phase set (FR-050's lifecycle
// table). Three fields reset, two survive:
//
//   - wake_at    -> cleared. Disarms the deadline; a later re-park re-wakes,
//     and the next park mints its own session.
//   - wake_error -> cleared.
//   - attempts   -> reset to 0.
//   - correction_rounds -> UNTOUCHED. Cumulative for the life of the plan and
//     NEVER reset. A plan leaves this phase set on every applied correction,
//     so a blanket reset zeroes it immediately after every increment and every
//     terminal record reads 0 — which inverts the only thing it is read for
//     (telling "the round budget ran out with no correction ever applied"
//     apart from "corrections consumed the budget").
//   - session_id -> UNTOUCHED, never blanked, only overwritten by the next
//     mint. See ensureSupervisionSessionLocked.
func clearSupervisionWakePatch(patch plan.Patch) plan.Patch {
	empty := ""
	zero := 0
	patch.SupervisionWakeAt = &empty
	patch.SupervisionWakeError = &empty
	patch.SupervisionAttempts = &zero
	return patch
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
// The plan MUST be in awaiting_supervision. The caller MUST be the plan's
// owner (sec-MAJOR-2): the owner-authority gate runs BEFORE any state
// inspection or mutation, so a non-owner learns nothing about the plan and
// cannot change it. The correction commits transactionally via the intent-log
// (INV-6/N-8): AppendIntent → MarkCommitted → Apply → MarkDone. After the
// commit:
//   - For append/supersede: auto-reset ALL live-round failed members (excludes
//     frozen/done members — G-10).
//   - For targeted_retry: reset ONLY the specified failed member (no full
//     Stop/Play — D4).
//   - Tails depend only on done outcomes; an unreachable DoD takes the
//     honest-exit path (G-10).
//   - The durable unmet signature is cleared (INV-7: correction = new activity).
//   - The DoD stays immutable (G-11).
func (pe *PlanEngine) AppendCorrection(ctx context.Context, planID string, caller CorrectionCaller, req CorrectionRequest) (*CorrectionResult, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: get plan %q: %w", planID, err)
	}
	// Adjudicator-authority gate (ADR-055 D3, sec-MAJOR-2): only the
	// PlanSupervisor may correct a plan — the owner included in "only". Runs
	// before any state/phase inspection so an unauthorised caller cannot probe
	// plan state via error differentiation, and before any mutation.
	if err := pe.requireCorrectionAuthority(caller, p, planID); err != nil {
		return nil, err
	}
	if p.State != plan.StateRunning {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: plan %q is %s, not running", planID, p.State)
	}
	// FR-029: the gate is membership in the SUPERVISION-ELIGIBLE PHASE SET
	// {awaiting_supervision, stalled}, not equality with the parked phase. A
	// stall wake asks the adjudicator for a diagnosis and may well provoke a
	// correction; gating on the parked phase alone rejected 100% of those,
	// leaving a stalled plan with a wake, a rubric and no execution path — its
	// only exits Stop and idle expiry.
	//
	// It MUST NOT become "any phase": a plan at dispatching or judging is
	// still rejected.
	if !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase()) {
		return nil, fmt.Errorf(
			"plan_engine: AppendCorrection: plan %q is in phase %q, not a supervision-eligible phase (%s or %s)",
			planID, p.EffectivePlanPhase(), plan.PhaseAwaitingSupervision, plan.PhaseStalled)
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
		Verb:                req.Verb,
		FalsifiedAssumption: req.FalsifiedAssumption,
		TailAdds:            tailAddIDs,
		SupersededMemberID:  req.SupersededMemberID,
		RetriedMemberID:     req.RetriedMemberID,
		Reason:              req.Reason,
		CreatedAt:           now,
	}
	// abandon is the ONE verb that does not return the plan to dispatching: it
	// terminates it. Its record therefore carries no phase patch, and its
	// commit adds no members and wires no edges.
	recPatch := plan.IntentRecordPatch{
		ClearLastUnmetTerminalSignature: true,
		PlanPhase:                       plan.PhaseDispatching,
	}
	if req.Verb == CorrectionAbandon {
		recPatch = plan.IntentRecordPatch{}
	}
	rec := plan.IntentRecord{
		IntentID:  revID,
		PlanID:    planID,
		Members:   req.TailMembers,
		Edges:     req.TailEdges,
		Revision:  rev,
		Patch:     recPatch,
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

	// --- abandon: the adjudicated honest exit (ADR-055/FR-046b) -----------
	//
	// The adjudicator judges the Definition of Done unreachable from the
	// plan's current state and adds no corrective work at all. The revision is
	// now durably committed, so the falsified assumption is on the record;
	// terminate the plan rather than burn the remaining round budget on
	// corrections that cannot succeed.
	//
	// The reason is dod_unreachable, NOT judge_rounds_exhausted: rounds may
	// well remain when this fires, and "we ran out of rounds" and "more rounds
	// would not help" are different facts (see plan.FailedReasonDoDUnreachable).
	if req.Verb == CorrectionAbandon {
		pe.countCorrectionAndClearWake(planID, p, revID)
		pe.clearUnmetTerminalSignature(planID)
		pe.failPlanLocked(planID, plan.FailedReasonDoDUnreachable, buildAbandonHandover(p, req))
		return &CorrectionResult{
			RevisionID: revID, Generation: gen, RevisionEntry: rev,
			HonestExit: true,
		}, nil
	}

	pe.countCorrectionAndClearWake(planID, p, revID)

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
		// dod_unreachable, not judge_rounds_exhausted: the correction was
		// applied and rounds may well remain — "more rounds would not help" is
		// a different fact from "we ran out of rounds", and this is the
		// involuntary half of the pair plan.FailedReasonDoDUnreachable
		// documents (the adjudicated `abandon` above is the deliberate half).
		pe.failPlanLocked(planID, plan.FailedReasonDoDUnreachable, handover)
		return &CorrectionResult{
			RevisionID: revID, Generation: gen, RevisionEntry: rev,
			HonestExit: true,
		}, nil
	}
	// Re-dispatch ready members.
	pe.dispatchReadyMembers(ctx, planID, tasks)
	return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
}

// countCorrectionAndClearWake performs the post-commit supervision
// bookkeeping shared by every correction verb (FR-050 + FR-029(3)): the plan
// has just left the supervision-eligible phase set, so the wake receipt, wake
// error and attempt counter reset and the correction is counted — in ONE store
// write, so a concurrent REST Store.Update (whose callers do not hold
// planDecisionMu) cannot interleave between them.
//
// correction_rounds is INCREMENTED and never reset: it is an attribution
// counter for the life of the plan, and it is the only thing that tells "the
// round budget ran out with no correction ever applied" apart from
// "corrections consumed the budget".
//
// The stall note goes with it. A correction applied to a STALLED plan must
// clear that note, for the same reason a park does: the plan record is the
// adjudicator's primary input, and a stale stall diagnosis alongside a fresh
// wake is the input most likely to produce the wrong verb next time.
//
// p is the plan as read at the top of AppendCorrection; revID is used for the
// failure log only. Caller must hold planDecisionMu.
func (pe *PlanEngine) countCorrectionAndClearWake(planID string, p *plan.Plan, revID string) {
	rounds := 1
	if p.Supervision != nil {
		rounds = p.Supervision.CorrectionRounds + 1
	}
	postPatch := clearSupervisionWakePatch(plan.Patch{SupervisionCorrectionRounds: &rounds})
	if strings.HasPrefix(p.HandoverText, stallHandoverNotePrefix) {
		cleared := ""
		postPatch.HandoverText = &cleared
	}
	if _, err := pe.planStore.Update(planID, postPatch); err != nil {
		// The correction itself is durably committed by the caller; this write
		// only carries supervision bookkeeping. Surface it — a stuck wake
		// receipt would let the next tick's deadline fire against a plan that
		// has already moved.
		logger.ErrorCF("plan_engine", "AppendCorrection: could not reset supervision state after applying the correction",
			map[string]any{"plan_id": planID, "revision_id": revID, "error": err.Error()})
	}
}

// buildAbandonHandover renders the handover for an adjudicated abandon
// (ADR-055/FR-046b). It states the falsified assumption, because that — not
// the member outcomes — is what makes the exit honest rather than a giving-up.
func buildAbandonHandover(p *plan.Plan, req CorrectionRequest) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan %q was judged unable to reach its Definition of Done and has been "+
		"abandoned by the adjudicator rather than corrected further.\n\n", p.Title)
	fmt.Fprintf(&sb, "Falsified assumption: %s\n", strings.TrimSpace(req.FalsifiedAssumption))
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		fmt.Fprintf(&sb, "Reason: %s\n", reason)
	}
	sb.WriteString("\nNo corrective work was added: the adjudicator judged that no correction " +
		"could reach this Definition of Done from the plan's current state. Review the DoD itself.")
	return sb.String()
}

// validateCorrection is the ENGINE's own, complete precondition check for a
// correction — not a formality delegating to the tool that called it.
//
// It exists as a second, independent line of defence because AppendCorrection
// is an exported engine entrypoint: pkg/tools/plan_correct.go is one caller,
// but nothing structurally prevents another (a REST seam, an internal
// supervision loop, a future tool) from reaching it with a typed request that
// never passed through the tool's raw-argument parser. Every rule the tool
// enforces at its boundary is therefore re-enforced here on the TYPED request.
//
// The checks, in the order a bad request is cheapest to reject:
//
//  1. Payload caps — single-sourced from pkg/plan (MaxTailMembers,
//     MaxTailEdges, MaxMemberTitleBytes, MaxTextBytes), never re-declared
//     locally, so the tool and the engine cannot drift apart.
//  2. The verb/field compatibility matrix. A field that is merely MEANINGLESS
//     for a verb is rejected rather than silently ignored, because the engine
//     creates rec.Members/rec.Edges verb-independently — a targeted_retry
//     carrying 50 tail members would otherwise create all 50.
//  3. Verb-specific member references (exists, right status, belongs to THIS
//     plan).
//  4. FR-030/FR-030b, the supersede pairing rule — see requireSupersedePairing.
//  5. Tail-edge integrity: no dangling endpoints, no self-edges, no edge
//     touching the superseded member, and no cycle in the resulting DAG.
func (pe *PlanEngine) validateCorrection(planID string, p *plan.Plan, req CorrectionRequest) error {
	if err := validateCorrectionPayloadCaps(req); err != nil {
		return err
	}
	if err := validateCorrectionVerbFields(req); err != nil {
		return err
	}

	var supersededMember *task.Task
	switch req.Verb {
	case CorrectionSupersede:
		m, err := pe.validateMemberRef(planID, req.SupersededMemberID, "supersede", task.StatusDone, "done")
		if err != nil {
			return err
		}
		supersededMember = m
	case CorrectionTargetedRetry:
		if _, err := pe.validateMemberRef(planID, req.RetriedMemberID, "targeted_retry", task.StatusFailed, "failed"); err != nil {
			return err
		}
		// targeted_retry adds no work and wires no edges (enforced by the
		// field matrix above), so there is nothing further to validate.
		return nil
	case CorrectionAbandon:
		// The honest exit names no member and adds no work (enforced by the
		// field matrix above). Nothing to resolve against the store.
		return nil
	case CorrectionAppend:
		// Tail members are validated below, shared with supersede.
	default:
		return fmt.Errorf("plan_engine: unknown correction verb %q", req.Verb)
	}

	// FR-030b: replacement work must be held to the SAME standard as the work
	// it replaces. Checked before the edge graph because it is the rule the
	// whole verb's safety rests on.
	if req.Verb == CorrectionSupersede {
		if err := tools.RequireCriteriaInheritance(supersededMember.Criteria, req.TailMembers); err != nil {
			return fmt.Errorf("plan_engine: %w", err)
		}
	}

	members, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return fmt.Errorf("plan_engine: could not list members of plan %q: %w", planID, err)
	}
	return validateCorrectionTailEdges(members, req)
}

// validateCorrectionPayloadCaps bounds every unbounded field on the request.
// The caps live in pkg/plan so this and the plan_correct tool enforce the same
// numbers by construction (FR-004).
func validateCorrectionPayloadCaps(req CorrectionRequest) error {
	if len(req.TailMembers) > plan.MaxTailMembers {
		return fmt.Errorf("plan_engine: tail_members has %d entries; the maximum is %d",
			len(req.TailMembers), plan.MaxTailMembers)
	}
	if len(req.TailEdges) > plan.MaxTailEdges {
		return fmt.Errorf("plan_engine: tail_edges has %d entries; the maximum is %d",
			len(req.TailEdges), plan.MaxTailEdges)
	}
	if err := checkCorrectionTextBytes("falsified_assumption", req.FalsifiedAssumption); err != nil {
		return err
	}
	if err := checkCorrectionTextBytes("reason", req.Reason); err != nil {
		return err
	}
	for i := range req.TailMembers {
		m := &req.TailMembers[i]
		if len(m.Title) > plan.MaxMemberTitleBytes {
			return fmt.Errorf("plan_engine: tail_members[%d].title is %d bytes; the maximum is %d",
				i, len(m.Title), plan.MaxMemberTitleBytes)
		}
		if err := checkCorrectionTextBytes(fmt.Sprintf("tail_members[%d].description", i), m.Description); err != nil {
			return err
		}
	}
	return nil
}

// checkCorrectionTextBytes bounds one free-text correction field on BYTES
// (not runes), matching plan.MaxTextBytes' stated contract.
func checkCorrectionTextBytes(field, value string) error {
	if len(value) > plan.MaxTextBytes {
		return fmt.Errorf("plan_engine: %s is %d bytes; the maximum is %d",
			field, len(value), plan.MaxTextBytes)
	}
	return nil
}

// validateCorrectionVerbFields is the verb/field compatibility matrix: it
// states which fields each verb ACCEPTS, so a field that is meaningless for
// the verb is rejected instead of silently applied.
func validateCorrectionVerbFields(req CorrectionRequest) error {
	reject := func(verb string, offenders map[string]bool) error {
		named := make([]string, 0, len(offenders))
		for _, field := range []string{"superseded_member_id", "retried_member_id", "tail_members", "tail_edges"} {
			if offenders[field] {
				named = append(named, field)
			}
		}
		if len(named) == 0 {
			return nil
		}
		return fmt.Errorf("plan_engine: %s does not accept %s", verb, strings.Join(named, ", "))
	}

	switch req.Verb {
	case CorrectionAppend:
		if err := reject("append", map[string]bool{
			"superseded_member_id": req.SupersededMemberID != "",
			"retried_member_id":    req.RetriedMemberID != "",
		}); err != nil {
			return err
		}
		if len(req.TailMembers) == 0 {
			return fmt.Errorf("plan_engine: append requires at least one tail member — an append that adds no work is not a correction")
		}
	case CorrectionSupersede:
		if err := reject("supersede", map[string]bool{
			"retried_member_id": req.RetriedMemberID != "",
		}); err != nil {
			return err
		}
		if req.SupersededMemberID == "" {
			return fmt.Errorf("plan_engine: supersede requires superseded_member_id")
		}
		if err := requireSupersedePairing(req); err != nil {
			return err
		}
	case CorrectionTargetedRetry:
		if err := reject("targeted_retry", map[string]bool{
			"superseded_member_id": req.SupersededMemberID != "",
			"tail_members":         len(req.TailMembers) > 0,
			"tail_edges":           len(req.TailEdges) > 0,
		}); err != nil {
			return err
		}
		if req.RetriedMemberID == "" {
			return fmt.Errorf("plan_engine: targeted_retry requires retried_member_id")
		}
	case CorrectionAbandon:
		if err := reject("abandon", map[string]bool{
			"superseded_member_id": req.SupersededMemberID != "",
			"retried_member_id":    req.RetriedMemberID != "",
			"tail_members":         len(req.TailMembers) > 0,
			"tail_edges":           len(req.TailEdges) > 0,
		}); err != nil {
			return err
		}
		if strings.TrimSpace(req.FalsifiedAssumption) == "" {
			return fmt.Errorf(
				"plan_engine: abandon requires falsified_assumption — terminating a plan as unreachable " +
					"is only honest when the assumption that turned out to be wrong is on the record")
		}
	default:
		return fmt.Errorf("plan_engine: unknown correction verb %q", req.Verb)
	}
	return nil
}

// requireSupersedePairing enforces FR-030: a supersede MUST carry replacement
// work.
//
// supersede marks a done member's outcome ignored by the judge. Unpaired, it
// is a way to satisfy an unmet Definition of Done by DISCOUNTING the evidence
// that failed it instead of fixing the work — which is precisely the move the
// verb must not enable. Pairing composes atomically: the engine creates tail
// members verb-independently, so the discounting and the replacement land in
// the same transactional intent-log commit or neither does.
//
// The complementary half (FR-030b, requireCriteriaInheritance) is what stops
// the pairing being satisfied by one throwaway member: the replacement must
// carry EVERY acceptance criterion of the member it replaces.
func requireSupersedePairing(req CorrectionRequest) error {
	if len(req.TailMembers) > 0 {
		return nil
	}
	return fmt.Errorf(
		"plan_engine: supersede requires at least one tail member: discounting a member's outcome is only " +
			"a correction when it is paired with replacement work that addresses the same criteria")
}

// validateCorrectionTailEdges checks the edge list against the plan's real
// member set: both endpoints must resolve to an existing member of THIS plan
// or to a tail member created in this same request, self-edges are rejected,
// no endpoint may name the member being superseded (new work behind a
// discounted member cannot make progress), and the resulting graph must be
// acyclic.
//
// The acyclicity check matters most here: the engine wires edges INSIDE its
// transactional intent-log commit, so a cycle discovered there aborts
// mid-commit, and an unwired cycle is unresolvable by the dispatcher — which,
// combined with a once-per-park supervision wake, strands the plan permanently.
func validateCorrectionTailEdges(members []task.Task, req CorrectionRequest) error {
	if len(req.TailEdges) == 0 {
		return nil
	}
	known := make(map[string]bool, len(members)+len(req.TailMembers))
	for i := range members {
		known[members[i].ID] = true
	}
	for i := range req.TailMembers {
		if id := req.TailMembers[i].ID; id != "" {
			known[id] = true
		}
	}
	for i := range req.TailEdges {
		e := &req.TailEdges[i]
		for _, ep := range []struct{ field, id string }{{"from", e.FromTaskID}, {"to", e.ToTaskID}} {
			if ep.id == "" {
				return fmt.Errorf("plan_engine: tail_edges[%d]: %s is required", i, ep.field)
			}
			if !known[ep.id] {
				return fmt.Errorf(
					"plan_engine: tail_edges[%d]: %s %q is neither an existing member of this plan nor a tail member in this correction",
					i, ep.field, ep.id)
			}
		}
		if e.FromTaskID == e.ToTaskID {
			return fmt.Errorf("plan_engine: tail_edges[%d]: from and to name the same member (self-edge)", i)
		}
		if req.SupersededMemberID != "" &&
			(e.FromTaskID == req.SupersededMemberID || e.ToTaskID == req.SupersededMemberID) {
			return fmt.Errorf(
				"plan_engine: tail_edges[%d]: names member %q, whose outcome this correction is superseding — new work must not depend on it",
				i, req.SupersededMemberID)
		}
	}
	if err := tools.RequireAcyclic(members, req.TailMembers, req.TailEdges); err != nil {
		return fmt.Errorf("plan_engine: %w", err)
	}
	return nil
}

// validateMemberRef is the shared preflight for member-targeted corrections:
// resolve the member, require the expected status (verb-dependent), and
// confirm the task actually belongs to planID. Used by both CorrectionSupersede
// (wantStatus=done, statusMsg="done") and CorrectionTargetedRetry
// (wantStatus=failed, statusMsg="failed") so the only call-site difference is
// the verb label and the status check.
// Returns the resolved member so a caller that needs its body (supersede, for
// the FR-030b criteria-inheritance check) does not re-read it.
func (pe *PlanEngine) validateMemberRef(planID, memberID, verb string, wantStatus task.Status, statusMsg string) (*task.Task, error) {
	t, err := pe.taskStore.Get(memberID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: %s member %q: %w", verb, memberID, err)
	}
	if t.Status != wantStatus {
		return nil, fmt.Errorf("plan_engine: member %q is %s, not %s (only %s members can be %s)",
			memberID, t.Status, statusMsg, statusMsg, verb)
	}
	if t.PlanID != planID {
		return nil, fmt.Errorf("plan_engine: member %q belongs to plan %q, not %q",
			memberID, t.PlanID, planID)
	}
	return t, nil
}

// requireCorrectionAuthority is the adjudicator-authority gate for
// AppendCorrection (ADR-055 D3/FR-6, sec-MAJOR-2). It runs before any
// state/phase inspection so an unauthorised caller cannot probe plan state via
// error differentiation, and before any mutation.
//
// THE RULE, in one line: correction is PlanSupervisor's alone — matched on
// exact system-agent identity — and everyone else, the plan's own OWNER
// included, is denied.
//
// Matching on identity rather than on "is a System Agent" is deliberate
// (ADR-055 D3): a future System Agent must not silently inherit correction
// rights by virtue of its type.
//
// The denial stays OPAQUE (sec-MAJOR-2): the error names the plan the caller
// already named and nothing else. It does not echo the plan's OwnerAgentID,
// does not say who IS authorised, and does not vary with plan state — those
// details go to the server-side log only.
//
// Returns ErrCorrectionNotAdjudicator (wrapped) on every denial; callers
// propagate the wrapped sentinel so a calling seam can map it to HTTP 403.
func (pe *PlanEngine) requireCorrectionAuthority(caller CorrectionCaller, p *plan.Plan, planID string) error {
	if caller.AgentID == planSupervisorAgentID {
		return nil
	}
	// An empty AgentID lands here too — a caller with no identity is not the
	// adjudicator, and gets the same message as one with the wrong identity.
	logger.WarnCF("plan_engine", "AppendCorrection denied: caller is not the plan adjudicator",
		map[string]any{
			"plan_id":      planID,
			"caller_agent": caller.AgentID,
			"owner_agent":  p.OwnerAgentID,
			"adjudicator":  planSupervisorAgentID,
		})
	return fmt.Errorf("%w: plan %q", ErrCorrectionNotAdjudicator, planID)
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
		// abandon terminates the plan rather than returning it to work, so it
		// must NOT be patched back to dispatching here — AppendCorrection
		// fails it to dod_unreachable immediately after this commit.
		if req.Verb == CorrectionAbandon {
			return nil
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
// StillFailedMemberIDs lists the task IDs whose RestartReset failed (the
// per-member reset is logged-and-continued — see PlayPlan — so a partial
// failure is not fatal but the REST handler must surface it to the operator
// rather than returning an unqualified 200).
//
// not-wire-format: engine-internal type.
type PlayResult struct {
	NewGeneration        int      `json:"new_generation"`
	ResumedFrom          string   `json:"resumed_from,omitempty"`
	PlanID               string   `json:"plan_id"`
	StillFailedMemberIDs []string `json:"still_failed_member_ids,omitempty"`
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
		return nil, fmt.Errorf("%w: plan %q is %s", plan.ErrNotFailed, planID, p.State)
	}
	// The restart transition validates failed_reason == stopped_by_user
	// (plan.ValidateRestartTransition). This is the same gate the REST
	// /restart endpoint uses.
	approved := plan.StateApproved
	if _, updateErr := pe.planStore.Update(planID, plan.Patch{State: &approved}); updateErr != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: restart transition: %w", updateErr)
	}

	// Reset failed/cancelled members to `next`; preserve done members.
	// Resume from last git commit if available (D13); fresh attempt otherwise.
	// Track still-failed members so the REST handler can surface partial
	// resets (RestartReset is logged-and-continued, never fatal).
	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: list member tasks: %w", err)
	}
	var stillFailed []string
	for i := range tasks {
		t := &tasks[i]
		if task.IsTerminal(t.Status) && t.Status != task.StatusDone {
			// Failed/cancelled member — reset for re-dispatch.
			if _, rerr := pe.taskStore.RestartReset(t.ID); rerr != nil {
				logger.WarnCF("plan_engine", "PlayPlan: could not reset member",
					map[string]any{"plan_id": planID, "task_id": t.ID, "error": rerr.Error()})
				stillFailed = append(stillFailed, t.ID)
				continue
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
		NewGeneration:        newGen,
		ResumedFrom:          fmt.Sprintf("gen-%d", prevGen),
		PlanID:               planID,
		StillFailedMemberIDs: stillFailed,
	}, nil
}

// recordMemberResumePoint resolves the gitevidence checkpoint for a member
// being resumed via Play (D13: "resume from last git commit"), PERSISTS it
// on the task record as ResumeFromCommit — the resume baseline the worker turn
// and plan Judge consume (the next attempt's diff is measured from this hash)
// — and materializes the member's isolated resume working tree at that commit
// via the resolver's ResetMemberCheckout (#537: the D10 isolation ladder).
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

	// D13/#537: materialize the member's resume working tree at the resolved
	// commit via the isolation ladder (or clear any stale tree on the
	// fresh-attempt path). A materialization failure degrades the member to
	// the shared-tree resume — the baseline hash below is still persisted and
	// the committed work remains in the shared tree — so it is logged, never
	// fatal to Play.
	if cr != nil {
		dir, cerr := cr.ResetMemberCheckout(planID, taskID, hash)
		if cerr != nil {
			logger.WarnCF("plan_engine", "member resume: could not materialize resume checkout — shared-tree resume",
				map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "error": cerr.Error()})
		} else if dir != "" {
			logger.InfoCF("plan_engine", "member resume: working tree restored at commit",
				map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "dir": dir})
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
// after the plan entered awaiting_supervision (detected by comparing the
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
// on an awaiting-supervision plan would burn one spurious JudgeRound
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
		// C1 durable rehydration: a plan parked at awaiting_supervision
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
