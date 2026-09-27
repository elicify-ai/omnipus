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
	"github.com/elicify-ai/omnipus/pkg/constants"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
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
	ExecuteTask(ctx context.Context, taskID string, occurrenceMs *int64) error
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
//
// Three return values, mirroring RequestCancelForSession's own widened
// signature (cancel.go, widened so CancelOutcome.Armed is structurally
// observable to callers instead of being discarded at this exact adapter
// boundary): fired, armed, err. armed distinguishes "the cancel found no live
// turn, and nothing is pending" from "the cancel found no live turn because
// none was registered yet, and a pre-registration latch (cancel_prearm.go)
// now stands in for it — the next turn to register under this session id
// WILL still be cancelled, within cancelPreArmTTL." cancelSessions buckets
// these separately (sessionCancelReport.armed vs. .notFired) rather than
// folding armed into notFired, which is exactly the gap that let a Stop
// report an unqualified success while a turn about to register kept running,
// uncancelled, past the point the user was told the plan had stopped.
type sessionCanceller interface {
	RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (fired bool, armed bool, err error)
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
	// Fix-wave finding 6(c): now sourced from coreagent.IDPlanSupervisor (the
	// seeded System Agent constant) instead of a local literal — the sibling
	// coreagent seed this comment used to wait on has landed. Kept as a
	// package-level string constant (not a bare reference at each call site)
	// so every usage below is unchanged. dispatchPlanTurn's own
	// agent-resolution check is what keeps a mismatch loud rather than
	// silent: an unresolvable supervisor is a recorded wake error that
	// escalates to failed(supervision_unavailable), never a turn quietly run
	// by whatever agent happens to be default.
	planSupervisorAgentID = string(coreagent.IDPlanSupervisor)

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
	// — which applyJudgeRoundOutcome persists (mirroring the in-memory
	// entry) whenever a round ends UNMET, and bootReconcile re-seeds THIS map
	// from at boot for every plan still awaiting supervision. So a process
	// restart does NOT drop the gate's authority: the durable field survives,
	// the in-memory map is rehydrated from it, and the unchanged-state skip
	// keeps holding across the restart (the very next tick does not need to
	// re-burn a round to relearn what the prior process already concluded).
	lastUnmetTerminalSignature map[string]string

	// unmetVerdictAt is lastUnmetTerminalSignature's timestamp companion:
	// planID -> when THIS engine instance most recently recorded an UNMET
	// plan-judge verdict. Written/cleared at exactly the same call sites and
	// under the same mu; consumed only by postUnmetMemberIDs to decide which
	// member artifacts are post-hoc for the N-2 gaming guard. Deliberately not
	// persisted — see recordUnmetVerdictAt's doc comment.
	unmetVerdictAt map[string]time.Time

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

	// supervisionSuppressStreak counts CONSECUTIVE times wakeSupervisor's CAS
	// claim (verifierRegistry.Register on supervisionUnitForPlan) was refused
	// for a given plan ID — i.e. a supervision turn was ALREADY in flight for
	// this plan (code review finding, LOW): without this, every suppression
	// logs an identical line with no way to tell "routine overlap, the
	// existing turn will settle shortly" from "this claim has been held
	// through N consecutive wake attempts", which is exactly what a
	// genuinely wedged claim looks like (and which would also block the
	// plan's own stall-abandon path). In-memory only, deliberately, same
	// posture as evidenceRejectStreak (task_executor.go): a soft diagnostic
	// signal, not a durability contract — a process restart clears it, which
	// is safe because the registry itself is process-local too (a restart
	// cannot leave a stale claim behind to misreport). Same lazy-init + mu
	// pattern as lastUnmetTerminalSignature above.
	supervisionSuppressStreak map[string]int

	// judgeUnavailableStreak counts CONSECUTIVE plan-level judge rounds
	// abandoned as UNAVAILABLE for a given plan ID (UAT defect B). It is the
	// counter that makes plan.MaxConsecutiveJudgeUnavailable enforceable: see
	// that constant's doc comment for why an unbounded retry here produced a
	// plan that oscillated dispatching -> judging -> dispatching forever at
	// progress=1.0 while rendering as an ordinary "Running" chip.
	//
	// Reset to zero the moment a round produces a REAL verdict (met or
	// unmet), and whenever the plan (re)enters running — so it always
	// measures the current unbroken run of unavailability, never a stale
	// count from an earlier, already-recovered episode.
	//
	// In-memory only, deliberately, matching supervisionSuppressStreak and
	// unmetVerdictAt above. A process restart clearing it is correct rather
	// than merely tolerable: a restart is a genuinely fresh attempt against a
	// possibly-recovered provider, and the durable evidence that something
	// went wrong is the PhaseStalled park and its handover text, which DO
	// survive. Same lazy-init + mu pattern as the maps above.
	judgeUnavailableStreak map[string]int

	// judgeUnavailableParks records, per plan id, a judge-unavailability park
	// that has been DECIDED (the streak above reached
	// plan.MaxConsecutiveJudgeUnavailable) but may not have TAKEN EFFECT on
	// disk — see judgeUnavailablePark's own doc comment, and
	// plan.MaxJudgeUnavailableParkAttempts for why a decided-but-ineffective
	// park is the hole that made the streak bound unenforceable.
	//
	// Same in-memory posture and lazy-init + mu pattern as the maps above,
	// and cleared by exactly the same events (clearJudgeUnavailableStreak
	// deletes both): a real verdict, a fresh admission, or a new generation
	// all mean the judge is reachable and the park history is spent.
	judgeUnavailableParks map[string]*judgeUnavailablePark

	// memberExecuting reports whether the TaskExecutor currently holds a
	// dispatch slot for a member task id — i.e. whether a goroutine is running
	// it right now, or is about to (a reserved slot). It is the engine's only
	// way to tell a member that is WORKING from a member that merely SAYS it is
	// (Status == in_progress on disk), and it is what planStallReason's
	// stranded-member term is built on. See strandedSince.
	//
	// Nil in every struct-literal test engine and on a boot that passes no task
	// executor; a nil reader is treated as "cannot tell", which suppresses the
	// stranded-member term entirely and leaves stall diagnosis exactly as it
	// was before it existed.
	memberExecuting func(taskID string) bool

	// strandedSince records, per member task id, the first tick at which the
	// member was observed in_progress with NO dispatch slot, plus the most
	// recent tick at which it was observed at all (used only to evict entries
	// for members nobody is looking at any more, so the map cannot grow with
	// deleted plans).
	//
	// WHY A DWELL AT ALL, given the slot test is binary: there are narrow, real
	// windows in which a member is legitimately in_progress with no slot yet —
	// executeTask writes next->in_progress via ClaimForRun BEFORE inserting the
	// slot (createTaskSessionSync, an fsync-bound session mint, sits between the
	// two), StartTaskNow's REST caller PATCHes in_progress before calling it at
	// all, and a fresh boot has an empty slot map until reconciliation runs. The
	// dwell exists to outlast those windows and NOTHING ELSE. It is deliberately
	// not a slowness judgement: a member that is genuinely working holds its
	// slot for the whole run (the slot is deleted in runTask's OUTERMOST defer,
	// after adjudication and after any redispatch), so a 40-minute member never
	// accumulates a single stranded observation no matter how long it takes.
	//
	// In-memory only, same lazy-init + mu posture as the maps above.
	strandedSince map[string]strandedMemberObservation

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

	// stopDrainTimeout overrides Stop's bounded-drain budget. Zero (the
	// production value — NewPlanEngine never sets it) means
	// planEngineStopDrainTimeout. Read through stopDrainBudget().
	stopDrainTimeout time.Duration

	// supervisionClaimSeq mints a unique, non-empty discriminator for
	// wakeSupervisor's "at most one live PlanSupervisor turn per plan" CAS
	// claim (see supervisionUnitForPlan). This is NOT cosmetic: the shared
	// VerifierSessionRegistry's CAS treats an EMPTY registered value as an
	// unclaimed PLACEHOLDER (by design, for the judge round's own
	// register-empty-then-upgrade-to-the-real-session-id pattern — see
	// verifier_registry.go's Register doc) — so two Register(unit, "") calls
	// for the SAME unit never conflict with each other; the second silently
	// "succeeds" as if it were the legitimate placeholder-upgrade case. A
	// wakeSupervisor claim has no real session id to upgrade to at
	// registration time (ensureSupervisionSessionLocked hasn't run yet), so
	// without a unique token every second concurrent claim attempt would pass
	// this gate — reopening the exact race it exists to close. Read/written
	// only via atomic ops (nextSupervisionClaimToken); needs no mutex, and a
	// zero value is a valid starting point for a bare struct-literal test
	// engine.
	supervisionClaimSeq uint64
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
	// type, just a nil pointer) — every `pe.dispatcher != nil` guard
	// in this file would then pass the nil check and panic calling a method on a nil receiver.
	// Test callers that legitimately pass nil (e.g. a bare-engine test that
	// never dispatches) now get a TRUE nil interface, so those guards work.
	if taskExecutor != nil {
		pe.dispatcher = taskExecutor
		pe.memberExecuting = func(taskID string) bool {
			return taskExecutorHoldsDispatchSlot(taskExecutor, taskID)
		}
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

// judgeUnavailablePark is the in-memory record of a judge-unavailability park
// that has been DECIDED for a plan. It exists because the decision and its
// persistence are two different things: surfaceJudgeUnavailableStall decides
// the park in memory (where the streak lives) and then writes it to the plan
// store, and that write can fail. The record is what lets processPlan notice
// on a later tick that the park never took effect, re-attempt it, and — past
// plan.MaxJudgeUnavailableParkAttempts — end the plan instead of re-entering
// the judge round the bound exists to stop.
type judgeUnavailablePark struct {
	// reason is the judge's own last failure reason, kept so a re-attempted
	// park reproduces the SAME handover note the first attempt would have
	// written rather than degrading to a vaguer one.
	reason string
	// attempts counts consecutive re-park attempts made from processPlan
	// since the last time the judge was reachable. It is NOT a count of
	// failed store writes: it counts the engine finding this plan still at
	// PhaseJudging while its streak is at the bound, which is the observable
	// "the park did not take effect" — true whether the write errored or the
	// phase was written and then reverted by something else.
	attempts int
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
// judge-round or plan-wake goroutine (planEngineStopDrainTimeout) rather than
// blocking shutdown forever on a stuck judge call.
//
// ⚠ REGRESSION ANCHOR — the drain is UNCONDITIONAL; only the *teardown of what
// Start created* is gated on `started`. The two halves are not the same thing
// and must not be re-merged behind one early return:
//
//   - `stopCh`/`subID`/`stoppedWG` are created by Start, so touching them on a
//     never-started (or already-stopped) engine is a real crash: `pe.stopCh` is
//     nil until Start runs and `close(nil)` panics, a second Stop would
//     double-close the same channel, and `subID` is never zeroed on stop so an
//     unguarded UnsubscribeEvents would fire twice with a stale id. That half
//     legitimately stays behind the `started` check.
//   - `judgeWG`/`wakeWG` are NOT Start's. beginPlanJudgeRound and
//     dispatchPlanTurn are reached from Tick/processPlan/StopPlan/failPlanLocked
//     — every one of them callable, and called, on an engine nobody ever
//     started (StopPlan on a never-started engine dispatches a real owner wake
//     turn through wakeOwner). Returning early there left those goroutines
//     running past Stop: in tests, a wake turn writing its session/transcript
//     into an already-removed temp dir; in production, a wake racing gateway
//     teardown. Draining them costs nothing on an engine that dispatched
//     nothing (both counters are zero, Wait returns immediately).
func (pe *PlanEngine) Stop() {
	pe.mu.Lock()
	wasStarted := pe.started
	if wasStarted {
		pe.started = false
		close(pe.stopCh)
	}
	subID := pe.subID
	pe.mu.Unlock()

	if wasStarted {
		if pe.agentLoop != nil && subID != 0 {
			pe.agentLoop.UnsubscribeEvents(subID)
		}
		// stoppedWG counts only runTickLoop/runEventLoop, both Add-ed inside
		// Start under mu. Waiting on it from the never-started path would be a
		// zero-counter Wait racing a concurrent Start's Add — the one
		// WaitGroup misuse the package is documented against — for no benefit,
		// since there is nothing to wait for.
		pe.stoppedWG.Wait()
	}

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
	case <-time.After(pe.stopDrainBudget()):
		logger.WarnCF("plan_engine",
			"stop: in-flight plan-judge round(s) or plan wake turn(s) still tearing down after drain timeout", nil)
	}
}

// stopDrainBudget is Stop's bounded-drain budget: planEngineStopDrainTimeout in
// production, overridable only by an in-package test that needs to observe the
// timeout limb without burning 30 s of wall clock. Mirrors the tickInterval /
// clock / judge test seams already on this struct; nothing outside _test.go
// sets stopDrainTimeout, so production always reads the constant.
func (pe *PlanEngine) stopDrainBudget() time.Duration {
	if pe.stopDrainTimeout > 0 {
		return pe.stopDrainTimeout
	}
	return planEngineStopDrainTimeout
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

// stallHandoverNotePrefix marks a Plan.HandoverText value written by
// surfaceStallIfAny below — distinguishes "this is our own stall note, safe
// to compare for de-dup/clear" from any OTHER text a different code path
// wrote into HandoverText (e.g. the plan-judge's UNMET steering text, or a
// terminal-brake wind-down summary), which must never be clobbered or
// mistaken for a stall note.
const stallHandoverNotePrefix = "[stalled] "

// strandedMemberObservation is one member's stranded-observation window: when
// the current unbroken run of "in_progress with no dispatch slot" started, and
// when it was last confirmed. Any observation WITH a slot deletes the entry
// outright, so `first` is always the start of an unbroken run.
type strandedMemberObservation struct {
	first    time.Time
	lastSeen time.Time
}

// failPlanLocked transitions planID to State=failed with reason and handover
// (SD-B9), then wakes the owner. Caller must hold planDecisionMu.
//
// It ALSO returns plan_phase to idle, in the SAME patch (ADR-055 fix wave,
// finding 4). plan_phase describes what a RUNNING plan is currently doing;
// pkg/plan's own contract is that it reads idle whenever State != running. A
// terminal plan left at awaiting_supervision is not merely cosmetic:
//   - the boot sweep's FR-118 exemption (b) keys off
//     plan.PlanPhase == awaiting_supervision to spare the owner session from
//     the failed(interrupted) sweep, so a dead plan would keep exempting a
//     session that will never be resumed;
//   - IsSupervisionEligiblePhase would keep reporting the plan correctable,
//     and AppendCorrection's own State != running gate is then the only thing
//     standing between a terminated plan and a correction attempt.
//
// The `abandon` verb is the case that made this visible — it fails a plan
// directly out of the parked phase — but every failure path routes through
// here and every one of them wants the same thing.
//
// This is deliberately NOT symmetric with the SUCCESS path:
// synthesizeAndComplete leaves plan_phase at "synthesizing" on purpose, as a
// historical marker of how the plan finished (see its doc comment). Failure
// records HOW it ended in FailedReason, which is a better marker than a stale
// phase, so no information is lost here.
func (pe *PlanEngine) failPlanLocked(planID string, reason plan.FailedReason, handover string) {
	failed := plan.StateFailed
	idle := plan.PhaseIdle
	updated, err := pe.planStore.Update(planID, plan.Patch{
		State:        &failed,
		FailedReason: &reason,
		HandoverText: &handover,
		PlanPhase:    &idle,
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
	// terminal write — see task_run_loop.go's adjudicateRunClaim), so scanning
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
	// a session nothing ever created. That half was fixed by minting a real
	// session; the OTHER half named in the same sentence — "the fan-out
	// discards RequestCancelForSession's 'did it fire' result, so a leg that
	// cancelled nothing is indistinguishable from one that worked" — stayed
	// live until the G3 fix wave and is now closed by cancelSessions'
	// sessionCancelReport (see its doc comment for what each half of that
	// result does and does NOT prove).
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
	cancelReport := pe.cancelSessions(ctx, sessions, userID, channel)

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
	return updated, aggregateStopFanoutErrors(planID, failedMemberIDs, cancelReport.failed)
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

// aggregateSessionCancelErrors is aggregateMemberCancelErrors' sibling for the
// OTHER half of the Stop fan-out: the session-level cancels. It lists every
// session whose RequestCancelForSession call returned an ERROR — i.e. the
// cancel request could not even be issued, so a live turn for that session may
// still be running against a plan the caller has been told is stopped.
//
// ⚠ It deliberately does NOT list sessions whose cancel was issued cleanly and
// reported fired=false — NEITHER of the two ways that can happen
// (sessionCancelReport.notFired or .armed). notFired is genuinely ambiguous
// and is BENIGN in several documented, routine cases — a supervision session
// whose adjudication turn already finished (StopPlan's own fan-out comment
// says so explicitly), a verifier session whose round returned while Stop
// held planDecisionMu, an in_progress member sitting between its worker turn
// ending and its terminal write, an idle owner session. armed is not merely
// benign — per CancelOutcome.Armed's contract the cancel WILL still fire,
// against the next turn to register for that session, so escalating it would
// report an active, working control path as a failure. Escalating either to
// an error would 500 the overwhelmingly common "Stop a parked plan" case
// (handlePlanStop maps ANY non-nil StopPlan error to HTTP 500), which is a
// worse lie than the one it would be fixing. Both are instead RECORDED, kept
// apart from each other — see cancelSessions and sessionCancelReport.
func aggregateSessionCancelErrors(planID string, failedSessionIDs []string) error {
	if len(failedSessionIDs) == 0 {
		return nil
	}
	return fmt.Errorf(
		"plan_engine: StopPlan: plan %q was stopped, but %d session cancel(s) could not be issued "+
			"and their turns may still be running: %s",
		planID, len(failedSessionIDs), strings.Join(failedSessionIDs, ", "),
	)
}

// aggregateStopFanoutErrors is StopPlan's single partial-stop verdict across
// BOTH legs of the fan-out (member state writes + session cancels). nil means
// every leg completed; anything else means the plan itself DID transition to
// failed(stopped_by_user) but some part of the fan-out did not land, which the
// caller must not read as an unqualified success (handlePlanStop maps it to a
// 500 carrying this text, after recording that the stop happened).
func aggregateStopFanoutErrors(planID string, failedMemberIDs, failedSessionIDs []string) error {
	return errors.Join(
		aggregateMemberCancelErrors(planID, failedMemberIDs),
		aggregateSessionCancelErrors(planID, failedSessionIDs),
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
	// Unlike StopPlan, a session-cancel failure is NOT folded into this
	// function's returned error, and that is a deliberate boundary decision
	// rather than an oversight: handleTaskStop (pkg/gateway/rest_tasks.go) has
	// no partial-fan-out branch — it discards `updated` on any non-nil error
	// and then re-reads the task, which by that point is `failed`, so it would
	// answer a SUCCESSFUL stop with a 409 "only an in-progress task can be
	// stopped". Reporting the failure that way would be less honest, not more.
	// cancelSessions logs every failed leg at Warn with its session id, which
	// is where a task-scope partial stop is observable until the REST layer
	// grows the same partial-success shape handlePlanStop already has.
	pe.cancelSessions(ctx, sessions, userID, channel)

	return pe.cancelMemberLocked(taskID, userID)
}

// sessionCancelReport is what the session-level leg of a Stop fan-out actually
// DID, as opposed to what it was asked to do. Before the G3 fix wave
// cancelSessions returned nothing at all and dropped every part of
// RequestCancelForSession's result on the floor, so "the cancel errored",
// "the cancel fired" and "the cancel found no live turn" were one
// indistinguishable outcome to every caller — the exact property StopPlan's own
// fan-out comment complains about, in the past tense, while doing it.
//
// The three buckets are NOT interchangeable and are kept apart on purpose:
//
//   - failed: RequestCancelForSession returned an error. The cancel was never
//     issued. Unambiguously a failure; StopPlan aggregates these into its
//     returned error (aggregateSessionCancelErrors).
//   - armed: the call succeeded, interrupted no LIVE turn, but armed a
//     pre-registration cancel latch (CancelOutcome.Armed, cancel.go) in its
//     place — the next turn to register under that session id WILL still be
//     cancelled, within cancelPreArmTTL. Kept apart from notFired because it is
//     a DEFERRED cancel, not a no-op one: before this bucket existed, an armed
//     session was indistinguishable from a genuinely benign notFired, which is
//     exactly what let a Stop report an unqualified success while a turn about
//     to register kept running — uncancelled, past the point the user was
//     told the plan had stopped — if the latch's TTL happened to expire before
//     that turn registered.
//   - notFired: the call succeeded, interrupted no live turn, AND armed no
//     latch either. Ambiguous by construction — see aggregateSessionCancelErrors
//     for why this must not be escalated to an error — so it is recorded and
//     logged, never returned as a failure.
//
// Neither armed nor notFired is ever escalated to an error: per
// CancelOutcome.Armed's own contract an armed latch WILL still fire, so it is,
// if anything, a WEAKER signal than a failure — but it is a strictly more
// informative signal than notFired, which is the whole reason it gets its own
// field instead of being folded into it.
type sessionCancelReport struct {
	// attempted counts distinct, non-empty session ids the fan-out reached.
	attempted int
	// fired counts cancels that actually interrupted a live turn.
	fired int
	// failed lists session ids whose cancel call returned an error.
	failed []string
	// armed lists session ids whose cancel found no live turn but armed a
	// pre-registration cancel latch in its place (see the struct doc above).
	armed []string
	// notFired lists session ids whose cancel was issued, interrupted
	// nothing, and armed no latch either. Advisory: consumed by the summary
	// log, not by error reporting.
	notFired []string
}

// cancelSessions issues RequestCancelForSession (the SAME chat cancel every
// other surface uses, A2) for every session in sessions (deduped inline by
// the caller's use of registry.SessionsFor + the direct worker-session
// append, both of which already avoid duplicates in practice, but a
// belt-and-suspenders local dedupe costs nothing) and returns what happened.
//
// A single session's cancel failure never aborts the fan-out — the engine's OWN
// state transition (member/plan -> failed) must still be attempted regardless
// (US-6 acceptance 1: the plan is marked `cancelled` even if one session's
// cancel call errored — the session-level cancel and the state transition are
// independent guarantees, not a single atomic unit). It is no longer SWALLOWED
// either: it is both logged and returned in the report, which is how StopPlan
// stops reporting a partly-failed stop as an unqualified success.
//
// This fan-out does NOT inject its own CancelHooks.OnLatchExpired. It reaches
// the canceller through the RequestCancelForSession PRIMITIVE adapter
// (cancel.go), which has no per-call hook injection point of its own — it
// wires one generic OnLatchExpired unconditionally, identically, for every
// caller that reaches it (Tier A /cancel, goal_loop.go's `/goal clear`, and
// this Stop fan-out alike): an unconditional slog.Warn logging the session id
// the instant that session's armed latch ages out (cancelPreArmTTL) unconsumed.
// That already closes the SILENT half of the failure the armed bucket above
// exists for: an operator investigating a plan that kept running past its
// reported Stop finds both this fan-out's own "armed" INFO log (session id,
// below) and the adapter's later "latch expired" Warn (same session id) —
// correlatable by session id, if not pre-joined into a single line. Giving
// THIS caller its own richer OnLatchExpired (e.g. one that names the plan, not
// just the session) would mean routing this leg through the fuller
// RequestCancel(scope, canceller, hooks) entrypoint instead of the primitive
// adapter (mirroring pkg/gateway/schedules.go's watchDeadline) — a separately
// -scoped, larger change (it would also mean rebuilding CancelScope /
// CancelCanceller / CancelHooks per session here instead of forwarding three
// strings, and reproducing the KillBackgroundSessions cascade the adapter
// currently provides for free), not what closes the reporting gap this fix is
// for. Left as a candidate follow-up, not done here.
func (pe *PlanEngine) cancelSessions(ctx context.Context, sessions []string, userID, channel string) sessionCancelReport {
	var report sessionCancelReport
	if pe.canceller == nil {
		if len(sessions) > 0 {
			logger.WarnCF("plan_engine",
				"stop fan-out: no sessionCanceller configured; session-level cancel skipped",
				map[string]any{"session_count": len(sessions)})
		}
		return report
	}
	seen := make(map[string]bool, len(sessions))
	for _, sessionID := range sessions {
		if sessionID == "" || seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		report.attempted++
		fired, armed, err := pe.canceller.RequestCancelForSession(ctx, sessionID, userID, channel)
		switch {
		case err != nil:
			report.failed = append(report.failed, sessionID)
			logger.WarnCF("plan_engine", "stop fan-out: session cancel failed",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		case fired:
			report.fired++
		case armed:
			report.armed = append(report.armed, sessionID)
			// Mirrors the WS handleCancel / cron watchDeadline callers' own
			// per-session INFO log for this same signal (buildCancelHooks /
			// watchDeadline): an armed latch is not a no-op, it is a
			// cancellation deferred to the next turn to register for this
			// session, bounded by cancelPreArmTTL.
			logger.InfoCF("plan_engine",
				"stop fan-out: session cancel armed a pre-registration latch — no turn had registered yet for this session; the next turn to register will be cancelled the instant it does, unless the latch's TTL expires first",
				map[string]any{"session_id": sessionID})
		default:
			report.notFired = append(report.notFired, sessionID)
		}
	}
	if report.attempted > 0 {
		// The record that makes "this leg cancelled nothing" readable after the
		// fact. Deliberately INFO on the whole fan-out rather than one line per
		// session: a fan-out of ten sessions in which none fired is the shape
		// worth seeing, and it is not per se an error.
		logger.InfoCF("plan_engine", "stop fan-out: session cancel leg complete",
			map[string]any{
				"attempted":  report.attempted,
				"fired":      report.fired,
				"failed":     len(report.failed),
				"armed":      report.armed,
				"not_fired":  report.notFired,
				"all_missed": report.fired == 0,
			})
	}
	return report
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
	// GOAL-FR-015/FR-027/FR-028: the same reasoning as the streak clear above,
	// for the paired goal record — this is a terminal disposition for taskID
	// that bypasses TaskExecutor's own chokepoints, so it must end the goal
	// record itself or a user Stop leaves it ACTIVE forever. A user Stop maps
	// to `cleared`, not `exhausted` (goalStateForTerminalTask), matching what
	// `/goal clear` writes for the chat equivalent of the same action.
	terminateTaskGoalRecord(taskID, updated.Status, updated.CancelReason, result)
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
//	(A) SUPERVISION wakes — surfaceStallIfAny, applyJudgeRoundOutcome's
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
// A DELIVERY FAILURE IS NOT AN OUTCOME (G3 fix wave, finding 3). The chat leg
// can fail two ways — no notifier wired, or Notify returning an error — and
// both used to log and return. That silently produced the worst outcome the
// wake path has: synthesizeAndComplete transitions the plan to `done`
// unconditionally right after this call, so the closing synthesis was never
// written, the human was never told, and the board read Done. It is the exact
// inverse of the rule this function's own origin-handling doc states — "'no chat
// to deliver to' must never mean 'no turn ran'" — since a FAILED delivery
// does mean no turn ran. Both failures therefore fall through to the same
// direct dispatch the origin-less case uses, on the SAME already-minted owner
// session, so the synthesis at least lands durably in the plan's transcript.
//
// Caller must hold planDecisionMu (every call site already does).
func (pe *PlanEngine) wakeOwner(p *plan.Plan, content, sourceKind string) {
	sessionID := pe.ensureOwnerSessionLocked(p)

	// G3 fix wave, finding 7: resolve the owner BEFORE choosing a delivery leg.
	// The direct-dispatch leg already refuses to run a turn for an unresolvable
	// agent (dispatchPlanTurn pre-resolves precisely so a wake is never
	// silently run by whichever agent happens to be default), and the notifier
	// leg originally had no such guard: processSystemMessage used to fall back
	// to GetDefaultAgent() when the named AsyncOriginAgentID did not resolve,
	// so a plan whose owner agent was deleted got its closing synthesis
	// authored by an unrelated roster member, in that member's own persona.
	// Since commit e830a4d2 (UAT E-3) processSystemMessage (loop.go) no longer
	// re-homes such a message: it discards it with a WARN and a system note in
	// the originating session. This pre-check still earns its place — it
	// refuses before a notify is queued at all and says so at ERROR with the
	// plan id, instead of the wake surviving only as a generic "background
	// update discarded" note. Agent deletion is guarded for the owners of
	// RUNNING plans, but failPlanLocked and StopPlan both move the plan to
	// `failed` before waking, so that guard does not cover this call.
	//
	// Losing the wake entirely is the better failure: it is loud (ERROR), it is
	// what the direct leg would do anyway, and a synthesis in the wrong voice is
	// worse than no synthesis.
	if !pe.ownerAgentResolves(p.OwnerAgentID) {
		logger.ErrorCF("plan_engine", "plan owner agent does not resolve; wake not delivered",
			map[string]any{"plan_id": p.ID, "owner_agent_id": p.OwnerAgentID, "source_kind": sourceKind})
		return
	}

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
		if err := pe.dispatchPlanTurn(p.ID, p.OwnerAgentID, sessionID, content, sourceKind, pe.supervisionTurnTimeout(p), nil); err != nil {
			logger.ErrorCF("plan_engine", "could not dispatch origin-less plan owner turn",
				map[string]any{"plan_id": p.ID, "owner_agent_id": p.OwnerAgentID, "error": err.Error()})
		}
		return
	}

	if pe.notifier != nil {
		notifyCtx, cancel := context.WithTimeout(context.Background(), planWakeNotifyTimeout)
		defer cancel()
		err := pe.notifier.Notify(notifyCtx, AsyncNotifyEvent{
			Channel:             p.SourceChannel,
			ChatID:              p.SourceChatID,
			AgentID:             p.OwnerAgentID,
			TranscriptSessionID: sessionID,
			SourceKind:          sourceKind,
			Content:             content,
		})
		if err == nil {
			return // delivered: the bus will route this into a real owner turn
		}
		logger.ErrorCF("plan_engine", "could not wake plan owner over its origin chat; dispatching the owner turn directly",
			map[string]any{
				"plan_id":     p.ID,
				"channel":     p.SourceChannel,
				"source_kind": sourceKind,
				"error":       err.Error(),
			})
	} else {
		logger.ErrorCF("plan_engine", "no async notifier configured; dispatching the plan owner turn directly",
			map[string]any{"plan_id": p.ID, "source_kind": sourceKind})
	}

	// Finding 3's fallback: the chat leg did not deliver, so no turn ran. Run
	// one on the plan's own owner session — the outcome is then durable even
	// though the requester's conversation never received it, and the ERROR
	// above is the record that the chat delivery itself was lost.
	if err := pe.dispatchPlanTurn(p.ID, p.OwnerAgentID, sessionID, content, sourceKind, pe.supervisionTurnTimeout(p), nil); err != nil {
		logger.ErrorCF("plan_engine", "plan owner wake could not be delivered OR dispatched directly",
			map[string]any{"plan_id": p.ID, "owner_agent_id": p.OwnerAgentID, "source_kind": sourceKind, "error": err.Error()})
	}
}

// ownerAgentResolves reports whether agentID names an agent this process can
// actually run a turn as — the guard wakeOwner applies before handing a wake to
// EITHER delivery leg (finding 7).
//
// It answers "unknown" as TRUE, deliberately. A PlanEngine with no agent loop or
// no registry cannot run any turn at all, so there is no wrong-persona risk to
// prevent there; the two legs each fail on their own terms in that case (Notify
// with whatever is wired, dispatchPlanTurn with an explicit error). Production
// always has both, which is where the guard bites.
func (pe *PlanEngine) ownerAgentResolves(agentID string) bool {
	if agentID == "" {
		// Nothing to resolve, and an empty AsyncOriginAgentID is exactly what
		// sends processSystemMessage to GetDefaultAgent().
		return false
	}
	if pe.agentLoop == nil {
		return true
	}
	reg := pe.agentLoop.GetRegistry()
	if reg == nil {
		return true
	}
	_, ok := reg.GetAgent(agentID)
	return ok
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
//
// onTurnSettled, when non-nil, is called EXACTLY ONCE — either synchronously,
// before this function returns, on every DISPATCH-failure path below (no
// goroutine ever ran, so nothing else would ever call it), or from the
// dispatched goroutine's own defer chain once the turn truly settles
// (success, error, or panic-recovered). This is wakeSupervisor's
// supervision-turn-in-flight CAS claim release (see its own doc comment) —
// releasing it at DISPATCH rather than at SETTLEMENT would reopen the exact
// race this exists to close, since a second wake could then claim and
// dispatch a concurrent turn while the first is still running. wakeOwner's two
// call sites below pass nil: owner (outcome) wakes are not part of that
// invariant.
func (pe *PlanEngine) dispatchPlanTurn(planID, agentID, sessionID, prompt, sourceKind string, turnTimeout time.Duration, onTurnSettled func()) error {
	settled := func() {
		if onTurnSettled != nil {
			onTurnSettled()
		}
	}
	if pe.agentLoop == nil {
		settled()
		return fmt.Errorf("plan_engine: no agent loop wired; cannot dispatch a plan turn for %q", planID)
	}
	if agentID == "" {
		settled()
		return fmt.Errorf("plan_engine: plan %q names no agent to wake", planID)
	}
	if sessionID == "" {
		settled()
		return fmt.Errorf("plan_engine: no transcript session for plan %q; refusing to dispatch an unpersisted turn", planID)
	}
	// Resolve the agent HERE rather than letting processTaskDirect fall back
	// to the default agent: a supervision turn silently run by whichever
	// agent happens to be default would leak the adjudication into an
	// unrelated roster member and report success.
	reg := pe.agentLoop.GetRegistry()
	if reg == nil {
		settled()
		return fmt.Errorf("plan_engine: no agent registry; plan %q wake not dispatched", planID)
	}
	if _, ok := reg.GetAgent(agentID); !ok {
		settled()
		return fmt.Errorf("plan_engine: agent %q does not resolve; plan %q wake not dispatched", agentID, planID)
	}

	al := pe.agentLoop
	sessionKey := fmt.Sprintf("agent:%s:session:%s", agentID, sessionID)
	pe.wakeWG.Add(1)
	go func() {
		defer pe.wakeWG.Done()
		// Released once the turn SETTLES (this goroutine's own end), not at
		// dispatch — see this function's doc comment on onTurnSettled. Ordered
		// so it still runs on the panic-recovered path (a panicking turn must
		// not leave wakeSupervisor's CAS claim held forever).
		defer settled()
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
		// D-08/FR-057: every caller of dispatchPlanTurn (wakeOwner's two call
		// sites, the plan supervisor's own wake) is the engine reacting to a
		// plan-state transition on its own — never a person clicking anything
		// — so this supervision/outcome turn must always auto-deny an
		// ask-policy tool rather than open a live approval card nobody is
		// watching.
		turnCtx = tools.WithAutoDenyAsk(turnCtx, true)
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
//
// Fails CLOSED (fix-wave finding 1): a plan-store List() error is returned to
// the caller rather than silently folded into a bare `false`. Prior to this
// fix, a transient store-read failure (permission error, disk issue, etc.)
// reported "no active plans" — which the delete-guard read as "safe to
// delete" — even though the true answer was unknown, not "none". Callers
// MUST check err before trusting the bool; on error the correct behavior is
// to refuse the delete (fail closed), never to fall back to this return
// value.
func (pe *PlanEngine) HasActivePlansOwnedBy(agentID string) (bool, error) {
	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.WarnCF("plan_engine", "HasActivePlansOwnedBy: list failed",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return false, fmt.Errorf("plan_engine: list plans: %w", err)
	}
	for i := range plans {
		if plans[i].OwnerAgentID == agentID && plans[i].State == plan.StateRunning {
			return true, nil
		}
	}
	return false, nil
}

// IntentEdge is re-exported here (alias of plan.IntentEdge) so callers importing
// pkg/agent get a single-package API for corrections. The authoritative type
// lives in pkg/plan (intent_log.go) where the intent-log store uses it.
type IntentEdge = plan.IntentEdge

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
		//
		// G3 fix wave, finding 4: the phase limb is not redundant with the
		// non-empty limb. A plan with zero members has an EMPTY signature that
		// is nonetheless its real, recorded one, so keying rehydration on
		// non-emptiness alone dropped exactly those plans' gate at boot — and
		// with it the "was a signature ever recorded" fact that
		// evaluateSupervisionDeadlineLocked needs to keep such a plan on the
		// escalation ladder across a restart.
		if plans[i].LastUnmetTerminalSignature != "" ||
			plans[i].EffectivePlanPhase() == plan.PhaseAwaitingSupervision {
			pe.recordUnmetTerminalSignature(plans[i].ID, plans[i].LastUnmetTerminalSignature)
			rehydrated++
		}
		pe.processPlan(ctx, plans[i].ID)
	}
	logger.InfoCF("plan_engine", "boot reconciliation complete",
		map[string]any{"running_plans_scanned": running, "unmet_signatures_rehydrated": rehydrated})
}
