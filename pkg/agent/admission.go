// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ====================== FR-068: the live memory gate ======================
//
// Agent admission and the browser pool are ONE mechanism. They read the same
// accessor (config.MemoryPressureHigh), compare against the same threshold
// (config.memoryPressureRatioThreshold — there is exactly one, and nothing
// here defines a second), and carry the same reason code
// (config.ReasonMemoryPressure) when they refuse.
//
// What they do NOT share is the RESPONSE, and that difference is deliberate
// (FR-075). The browser pool refuses to GROW: it will not start a second
// Chrome. Agent admission also refuses to grow, but from a floor of two —
// because an agent turn is the product, and a host that cannot report its
// own memory must still be able to run one. Refusing to RUN on an
// unmeasurable host would make every Windows and gVisor deployment useless
// for the sake of a reading nobody can take.

// unmeasurableHostAgentFloor is how many concurrent agent turns are admitted
// on a host whose memory cannot be measured, or one already above the
// pressure threshold.
//
// TWO, not one and not zero. One would serialize the whole gateway on a
// Windows box; zero would make it refuse to work at all. Two lets a user's
// turn run while a background task or a delegated child runs alongside it,
// which is the smallest number at which the product is still recognisably
// itself. The third concurrent turn is refused, naming memory — refuse to
// GROW, never refuse to RUN.
//
// This is NOT a per-agent memory budget in disguise. It is a count, chosen
// for what it preserves, and nothing multiplies it by a byte figure.
const unmeasurableHostAgentFloor = 2

// memoryAdmissionCap reports the cap the live memory mechanism imposes on
// concurrent agent turns right now, and whether it imposes one at all.
//
// It is the ONLY memory-derived input to admission, and it reads
// config.MemoryPressureHigh — the same accessor and threshold the browser
// pool reads. There is no per-agent byte cost anywhere in this path; the old
// availableRAM/3.5-MB-per-agent formula is deleted, not relocated.
//
//   - measured, headroom available  -> (0, false): memory imposes no cap and
//     the operator's configured value (or the physical backstop) governs.
//   - measured, above the threshold -> (floor, true): refuse to grow.
//   - not measurable at all         -> (floor, true): refuse to grow.
//
// The last two collapse to the same answer on purpose. "I know this host is
// short of memory" and "I cannot tell whether this host is short of memory"
// are the same instruction to an admission gate: do not add load.
func memoryAdmissionCap() (int, bool) {
	high, ok := config.MemoryPressureHigh()
	if !ok || high {
		return unmeasurableHostAgentFloor, true
	}
	return 0, false
}

// applyMemoryCap folds the live memory cap into a configured cap, returning
// the cap to enforce and whether MEMORY is the binding constraint (which is
// what decides whether a refusal names memory).
//
// Memory can only ever LOWER the cap. An operator who configured 1 gets 1 on
// a healthy host and 1 on an unmeasurable one; the floor is a ceiling for
// the memory mechanism, never a floor that raises an operator's explicit
// choice above what they asked for.
func applyMemoryCap(configured int) (effective int, memoryBinding bool) {
	memCap, imposed := memoryAdmissionCap()
	if !imposed || memCap >= configured {
		return configured, false
	}
	return memCap, true
}

// AdmissionController is a soft-cap gate for concurrent session workers.
//
// Phase 1: gates inbound user-message dispatch only. The counter tracks unique
// active scopes (one per spawned session worker) — not per-turn, so a single
// chatty session cannot pin admission slots indefinitely. Subagent spawn and
// task-executor dispatch paths are gated separately by TaskExecutor's own
// dispatchSema (pkg/agent/dispatch_sema.go), the "single authority" for agent
// concurrency (concurrency-gate consolidation, 2026-08-04) — see resolveCap's
// doc comment below for how the two stay aligned.
type AdmissionController struct {
	// softCap is the fixed cap used when resolveCap is nil — the path taken
	// by direct-int test construction (newAdmissionController). Production
	// wiring never uses this field; see resolveCap.
	softCap int
	// resolveCap, when non-nil, is consulted FRESH on every effectiveCap()
	// call instead of softCap. Production wiring (NewAgentLoop) always sets
	// this to a closure reading al.GetConfig().Performance.
	// EffectiveMaxParallelAgents() live — the SAME central authority
	// TaskExecutor's dispatch semaphore uses (pkg/config's
	// PerformanceConfig.EffectiveMaxParallelAgents) — so this gate can never
	// silently impose an independent, smaller cap (the pre-fix defect: a
	// hardcoded runtime.NumCPU()*4 rejected sessions well under the
	// operator-advertised max_parallel_agents, with zero visibility — see
	// docs/internal/uat/parallelism-cost-browser-bash-2026-08-04.md finding
	// #1). Resolving live on every check (rather than caching a value
	// resolved once at construction) is also the fix for the auto-detected
	// default's own boot-time-read caveat: see
	// pkg/config's availableRAMBytes doc comment — a transient low reading
	// right after boot self-corrects the moment the host's real availability
	// changes, with no restart or explicit resize required.
	resolveCap   func() int
	mu           sync.Mutex
	activeScopes map[string]struct{}
}

// newAdmissionController returns a controller with a FIXED cap: softCap if
// positive, otherwise a defensive floor of 1 (never a hardcoded
// hardware-derived guess — see newAdmissionControllerWithResolver for the
// production, live-resolved path, which is what NewAgentLoop actually uses).
// This constructor exists for direct unit tests of TryAdmit's admission
// logic against a known, stable cap.
func newAdmissionController(softCap int) *AdmissionController {
	if softCap <= 0 {
		softCap = 1
	}
	return &AdmissionController{
		softCap:      softCap,
		activeScopes: make(map[string]struct{}),
	}
}

// newAdmissionControllerWithResolver returns a controller whose cap is
// resolved LIVE via resolveCap on every admission check, rather than fixed
// at construction. This is the production constructor (NewAgentLoop wires
// resolveCap to al.GetConfig().Performance.EffectiveMaxParallelAgents()) —
// see resolveCap's doc comment on the AdmissionController struct for why
// live resolution, rather than a cached value, is required.
func newAdmissionControllerWithResolver(resolveCap func() int) *AdmissionController {
	return &AdmissionController{
		resolveCap:   resolveCap,
		softCap:      1, // defensive floor, only reachable if resolveCap ever returns <= 0
		activeScopes: make(map[string]struct{}),
	}
}

// effectiveCap returns the CONFIGURED cap to enforce right now:
// resolveCap()'s current value when set and positive, otherwise the fixed
// softCap.
//
// It deliberately does NOT fold in the live memory gate. SoftCap() reports
// this value to tests and observability, and "the cap you configured" and
// "what memory will let you have this second" are two different facts: a
// panel or a log line that showed the second under the name of the first
// would make an operator think their setting had been silently lowered,
// which is the ADR-037 anti-pattern this project bans. The memory gate is
// applied where it is ACTED on — inside TryAdmitWithReason — and it names
// itself when it refuses.
func (a *AdmissionController) effectiveCap() int {
	if a.resolveCap != nil {
		if c := a.resolveCap(); c > 0 {
			return c
		}
	}
	return a.softCap
}

// admissionCapWithReason is the cap actually enforced on an admission
// decision: the configured cap, lowered by the live memory gate when memory
// is the tighter constraint, plus whether memory is what is binding.
func (a *AdmissionController) admissionCapWithReason() (int, bool) {
	return applyMemoryCap(a.effectiveCap())
}

// TryAdmit atomically claims a slot for scope. Returns (true, release) when
// the scope is admitted; release MUST be called (typically via defer) when
// the scope's worker exits.
//
// If scope is already active (follow-up turn in an existing session), the
// call always succeeds without consuming an additional slot — the slot was
// already claimed when the worker was first spawned.
//
// Returns (false, nil) when the effective cap (see effectiveCap) is reached
// and scope is a new scope.
func (a *AdmissionController) TryAdmit(scope string) (bool, func()) {
	ok, _, release := a.TryAdmitWithReason(scope)
	return ok, release
}

// TryAdmitWithReason is TryAdmit plus the reason a refusal happened.
//
// reason is "" on success and on a refusal caused by the operator's own
// configured cap; it is config.ReasonMemoryPressure when the live memory
// gate is what refused. Callers that surface a refusal to a model or an
// operator MUST use this form — "the cap is reached" and "this machine is
// out of memory" send a caller to two completely different remedies, and
// only one of them exists on an unmeasurable host (there is no cap to raise).
func (a *AdmissionController) TryAdmitWithReason(scope string) (bool, string, func()) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, alreadyActive := a.activeScopes[scope]; alreadyActive {
		// Existing scope — follow-up turn, always admitted, no new slot consumed.
		return true, "", func() {}
	}

	cap, memoryBinding := a.admissionCapWithReason()
	if len(a.activeScopes) >= cap {
		if memoryBinding {
			logMemoryAdmissionRefusalOnce(cap)
			return false, config.ReasonMemoryPressure, nil
		}
		return false, "", nil
	}

	a.activeScopes[scope] = struct{}{}
	release := func() {
		a.mu.Lock()
		delete(a.activeScopes, scope)
		a.mu.Unlock()
	}
	return true, "", release
}

// ActiveScopes returns the current count of active scopes (worker goroutines
// that hold an admission slot). Used in tests and observability.
func (a *AdmissionController) ActiveScopes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.activeScopes)
}

// SoftCap returns the cap currently being enforced — the live-resolved value
// when a resolver is configured (production wiring), otherwise the fixed
// softCap. Safe to call without holding a.mu (effectiveCap only reads
// resolveCap/softCap, never activeScopes).
func (a *AdmissionController) SoftCap() int {
	return a.effectiveCap()
}

// ====================== ADR-057 W17: root-level delegation admission ======================
//
// FR-069/FR-070 (US-15). `turnState.concurrencySem` (pkg/agent/subturn.go)
// is set only on a CHILD turnState — the sole assignment is subturn.go:1051,
// guarded at subturn.go:607 — so it gates a delegated child's OWN further
// fan-out but has nothing to guard a ROOT turn's first delegate call with: a
// root turnState's concurrencySem is nil, so a wide `delegate` fan-out
// straight from a chat root sails through completely ungated. This is a
// SEPARATE, deliberately independent process-global gate — it does not read,
// write or otherwise interact with concurrencySem, and FR-070 requires that
// nested (child-level) gating stay byte-identical (see
// TestNestedDelegationGating_Unchanged, admission_adr057_test.go).
//
// This file supplies the gate PRIMITIVE (cap resolution + a non-blocking
// admit/release counter) and the BDD-77 operator-visible refusal shape.
// Wiring it into the live `delegate action=run` dispatch path is a call the
// TARGET AGENT of that dispatch (pkg/tools/delegate.go, owned by U14) or its
// spawner (pkg/agent/subturn.go, explicitly out of this unit's file
// ownership) must make — see this unit's final report for the specific,
// as-yet-unwired call sites this blocks on.
//
// CONSOLIDATION UPDATE (2026-08-04, commit 536b7340's follow-up fix):
// FR-095's original text required this gate to read
// agents.defaults.subturn.max_concurrent DIRECTLY and forbade sourcing it
// from Performance.EffectiveMaxParallelAgents(), on the premise that the
// latter was hard-clamped to 16 by clampParallelExplicit while the former
// was honored unclamped — two genuinely different numbers. 536b7340 removed
// that ceiling (clampParallelExplicit now only floors at 1), which
// invalidated the premise: with a fixed 16 seed, this gate silently
// disagreed with an operator's own max_parallel_agents setting the instant
// the two diverged — the exact "control that moves, persists and governs
// nothing" anti-pattern (ADR-037) this project bans. Performance.
// EffectiveMaxParallelAgents() is now the single, central authority for
// agent concurrency; ResolveRootDelegationCap resolves to it whenever
// subturn.max_concurrent is unset, and only an explicit positive override
// diverges from it deliberately. See docs/internal/specs/
// adr-057-session-unification-spec.md's 2026-08-04 amendment note on FR-095.

// ErrRootDelegationCapMisconfigured is returned by ResolveRootDelegationCap
// when agents.defaults.subturn.max_concurrent resolves to a NEGATIVE value
// (or cfg itself is nil). This MUST be treated as a boot-time configuration
// error, never silently reinterpreted as "no gate" (the ADR-037
// anti-pattern this project bans). A value of exactly 0 (unset — the shipped
// default) is NOT an error: it means "inherit the central
// Performance.EffectiveMaxParallelAgents() authority", see
// ResolveRootDelegationCap.
var ErrRootDelegationCapMisconfigured = errors.New(
	"agents.defaults.subturn.max_concurrent must be >= 0 for the root-delegation admission gate")

// ResolveRootDelegationCap reads agents.defaults.subturn.max_concurrent off
// cfg and resolves the effective root-level delegation admission cap:
//
//   - == 0 (unset — the shipped default, see DefaultConfig/defaults.go): the
//     cap IS cfg.Performance.EffectiveMaxParallelAgents(), the SAME central,
//     UI-configurable authority getSubTurnConfig(), TaskExecutor's dispatch
//     semaphore, and AdmissionController's session gate all resolve to. This
//     is what makes max_parallel_agents the single authority for agent
//     concurrency (concurrency-gate consolidation, 2026-08-04): an operator
//     raising it in the UI raises the root-delegation cap too, with no
//     second knob to also remember to change.
//   - > 0: an EXPLICIT, deliberate per-delegation override, honored exactly
//     as configured — it may differ from the central value in either
//     direction (an operator's own choice, e.g. to constrain delegation
//     fan-out specifically), and is never silently coerced towards it.
//   - < 0: ErrRootDelegationCapMisconfigured — a genuine configuration
//     error, surfaced rather than coerced into any default.
func ResolveRootDelegationCap(cfg *config.Config) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("resolve root-delegation cap: %w: nil config", ErrRootDelegationCapMisconfigured)
	}
	v := cfg.Agents.Defaults.SubTurn.MaxConcurrent
	if v < 0 {
		return 0, fmt.Errorf("resolve root-delegation cap: %w (configured value %d)", ErrRootDelegationCapMisconfigured, v)
	}
	if v == 0 {
		// The second return value (capped) is deliberately discarded here:
		// this resolver's contract is "a number to gate against", and the
		// unset case's number IS the physical backstop. Whether that number
		// came from an operator or from the backstop changes what a UI should
		// SAY about it, not what this gate should enforce — and the live
		// memory gate (applyMemoryCap) is what actually bounds admission long
		// before a backstop-sized cap is approached.
		n, _ := cfg.Performance.EffectiveMaxParallelAgents()
		return n, nil
	}
	return v, nil
}

// RootDelegationAdmission is the FR-069 process-global admission gate for
// ROOT-level delegation fan-out: one shared counter for the whole running
// gateway process (contrasted with concurrencySem's per-parent-turn scope —
// FR-095's "two scopes share one number intentionally" note), refusing
// immediately rather than blocking/queueing (BDD-75's "But it is not queued
// behind the session-store lock").
type RootDelegationAdmission struct {
	// cap is the fixed cap used when resolveCap is nil — the path taken by
	// direct-int test construction (NewRootDelegationAdmission). Production
	// wiring never uses this field; see resolveCap.
	cap int
	// resolveCap, when non-nil, is consulted FRESH on every TryAdmit/Cap call
	// instead of cap. Production wiring (NewAgentLoop) sets this to a closure
	// resolving ResolveRootDelegationCap(al.GetConfig()) live — mirroring
	// AdmissionController.resolveCap (concurrency-gate consolidation,
	// 2026-08-04): this gate must never freeze the cap at boot, or an
	// operator's PUT /api/v1/performance write (or the auto-detected
	// default's own boot-time-read self-correction, see pkg/config's
	// availableRAMBytes doc comment) would silently fail to reach
	// root-level delegation admission until a restart.
	resolveCap func() int
	mu         sync.Mutex
	active     int
}

// NewRootDelegationAdmission constructs a gate with a FIXED cap: maxCap if
// positive, otherwise a defensive floor of 1 (never a hardcoded guess — see
// newRootDelegationAdmissionWithResolver for the production, live-resolved
// path NewAgentLoop actually uses). This constructor exists for direct unit
// tests of TryAdmit's admission logic against a known, stable cap.
func NewRootDelegationAdmission(maxCap int) *RootDelegationAdmission {
	if maxCap <= 0 {
		maxCap = 1
	}
	return &RootDelegationAdmission{cap: maxCap}
}

// newRootDelegationAdmissionWithResolver returns a gate whose cap is
// resolved LIVE via resolveCap on every TryAdmit/Cap call, rather than fixed
// at construction. This is the production constructor (NewAgentLoop wires
// resolveCap to re-run ResolveRootDelegationCap against the live config on
// every call) — see resolveCap's doc comment on the RootDelegationAdmission
// struct for why live resolution, rather than a value cached once at
// construction, is required.
func newRootDelegationAdmissionWithResolver(resolveCap func() int) *RootDelegationAdmission {
	return &RootDelegationAdmission{
		resolveCap: resolveCap,
		cap:        1, // defensive floor, only reachable if resolveCap ever returns <= 0
	}
}

// effectiveCap returns the cap to enforce right now: resolveCap()'s current
// value when set and positive, otherwise the fixed cap. Safe to call without
// holding r.mu — resolveCap/cap are set once at construction and never
// mutated afterward (mirrors AdmissionController.effectiveCap).
func (r *RootDelegationAdmission) effectiveCap() int {
	if r.resolveCap != nil {
		if c := r.resolveCap(); c > 0 {
			return c
		}
	}
	return r.cap
}

// admissionCapWithReason is the cap actually enforced on an admission
// decision: the configured cap lowered by the live memory gate, plus whether
// memory is what is binding (FR-068 — the same accessor, the same threshold
// and the same reason code the browser pool uses). Cap() keeps reporting the
// CONFIGURED value, for the reason spelled out on
// AdmissionController.effectiveCap.
func (r *RootDelegationAdmission) admissionCapWithReason() (int, bool) {
	return applyMemoryCap(r.effectiveCap())
}

// TryAdmit atomically claims a root-delegation slot. Returns (true, release)
// when admitted; release MUST be called (typically via defer, or on the
// delegated child's terminal state) when the slot is no longer needed.
// Returns (false, nil) IMMEDIATELY — never blocking — when the cap is
// already reached (BDD-75/FR-069: refuse, don't queue).
func (r *RootDelegationAdmission) TryAdmit() (bool, func()) {
	ok, _, release := r.TryAdmitWithReason()
	return ok, release
}

// TryAdmitWithReason is TryAdmit plus the reason a refusal happened: "" for
// the operator's own configured cap, config.ReasonMemoryPressure when the
// live memory gate refused. See AdmissionController.TryAdmitWithReason for
// why the two must not be reported as one thing.
func (r *RootDelegationAdmission) TryAdmitWithReason() (bool, string, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cap, memoryBinding := r.admissionCapWithReason()
	if r.active >= cap {
		if memoryBinding {
			logMemoryAdmissionRefusalOnce(cap)
			return false, config.ReasonMemoryPressure, nil
		}
		return false, "", nil
	}
	r.active++
	released := false
	release := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if released {
			return // idempotent: a double-release must never under-count active
		}
		released = true
		r.active--
	}
	return true, "", release
}

// Active returns the current number of admitted, not-yet-released root
// delegations. Used by tests and observability.
func (r *RootDelegationAdmission) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// Cap returns the cap currently being enforced — the live-resolved value
// when a resolver is configured (production wiring), otherwise the fixed
// cap.
func (r *RootDelegationAdmission) Cap() int {
	return r.effectiveCap()
}

// RefuseRootDelegation performs the BDD-77 operator-visible refusal: an
// slog.Error record naming the maxCap, the delegating agent and the target
// agent (mirroring pkg/tools/delegate.go:1150-1159's existing shape for the
// sibling FR-015 refusal), plus the *tools.ToolResult a caller returns to the
// calling agent. No separate user-facing notification is required
// (operator decision 6) — the tool error is the whole contract.
func RefuseRootDelegation(maxCap int, delegatingAgentID, targetAgentID string) *tools.ToolResult {
	slog.Error("delegate: refusing root-level delegation — concurrent root-delegation cap reached",
		"cap", maxCap,
		"delegating_agent_id", delegatingAgentID,
		"target_agent_id", targetAgentID)
	return tools.ErrorResult(fmt.Sprintf(
		"delegate: refusing to start a new root-level delegation — the concurrent root-delegation cap (%d) has been reached; retry once an in-flight root delegation completes",
		maxCap))
}

// RefuseRootDelegationForMemory is the FR-068a refusal: the live memory gate,
// not a configured cap, is what stopped this delegation.
//
// It names MEMORY and names a remedy that EXISTS. It deliberately does not
// mention a cap or advise raising one, because on the host this fires on
// there may be no cap to raise: an unmeasurable host holds at
// unmeasurableHostAgentFloor no matter what performance.max_parallel_agents
// says, and telling an operator to raise a number that will not move the
// behaviour is worse than saying nothing. It carries
// config.ReasonMemoryPressure, the same code the browser pool's refusals
// carry, so one grep finds every memory refusal in the process.
func RefuseRootDelegationForMemory(delegatingAgentID, targetAgentID string) *tools.ToolResult {
	slog.Error("delegate: refusing root-level delegation — the host is under memory pressure or its memory cannot be measured",
		"reason", config.ReasonMemoryPressure,
		"concurrent_floor", unmeasurableHostAgentFloor,
		"delegating_agent_id", delegatingAgentID,
		"target_agent_id", targetAgentID)
	return tools.ErrorResult(
		"delegate: refusing to start a new root-level delegation — this host is short of memory, or its available memory cannot be measured at all (no memory reader exists for Windows, and a Linux host with an unreadable /proc/meminfo reports the same). Work already in flight is unaffected; retry once an in-flight delegation completes, or run this on a host with more free memory.")
}

// lastMemoryAdmissionRefusalLogged makes the memory refusal's operator-facing
// WARN a LOG-ONCE, matching the browser pool's discipline for the same
// condition. The condition is static for long stretches — an unmeasurable
// host stays unmeasurable — and this path is hit on every admission check, so
// an unthrottled line would bury the log it is meant to make diagnosable. It
// is the same trade-off, and the same shape, as shouldLogExplicitCeilingWarn
// in pkg/config.
var lastMemoryAdmissionRefusalLogged atomic.Bool

// logMemoryAdmissionRefusalOnce emits exactly one WARN per process for the
// memory-bound admission condition. Exactly one, not one per refusal: a test
// that fails on zero lines AND on one-per-call is what holds this honest.
func logMemoryAdmissionRefusalOnce(effectiveCap int) {
	if lastMemoryAdmissionRefusalLogged.Swap(true) {
		return
	}
	slog.Warn("agent admission is bound by memory, not by configuration — concurrent agent turns are held at a floor while this host is under memory pressure or its memory cannot be measured",
		"reason", config.ReasonMemoryPressure,
		"effective_concurrent_cap", effectiveCap)
}

// resetMemoryAdmissionRefusalLogForTest clears the log-once latch. Tests
// only: the latch is process-global by design, so a test asserting the
// once-ness must be able to start from a known state.
func resetMemoryAdmissionRefusalLogForTest() {
	lastMemoryAdmissionRefusalLogged.Store(false)
}

// ====================== ADR-057 W17 wiring: the live dispatch site ======================
//
// The gate primitive and refusal shape above were, until this fix, wholly
// unreferenced from any production dispatch path (zero non-test callers of
// RootDelegationAdmission/RefuseRootDelegation/ResolveRootDelegationCap
// outside this file and its own unit test) — a root turn's `delegate` fan-out
// sailed through completely ungated regardless of
// agents.defaults.subturn.max_concurrent. rootDelegationAdmittingSpawner
// closes that gap by wrapping the tools.SubTurnSpawner every per-agent
// DelegateTool is given (pkg/agent/loop.go's registerSharedTools delegate
// block, SetSpawner call site).
//
// Why wrapping the spawner is the correct dispatch point (not a workaround):
// EVERY delegate() call, sync or async, for every agent, ultimately calls
// spawner.SpawnSubTurn (pkg/tools/delegate.go executeSync/executeAsync) —
// there is no other shared choke point. spawnSubTurn (pkg/agent/subturn.go)
// runs the child's ENTIRE turn synchronously inside itself regardless of
// cfg.Async — Async only changes how the result is delivered afterward
// (return value vs deliverSubTurnResult), never whether the call blocks.
// executeAsync's background goroutine calls SpawnSubTurn and blocks on it
// for the child's full lifetime before that goroutine exits; executeSync's
// await path blocks on the very same call on the delegating turn's own
// goroutine. So wrapping SpawnSubTurn and releasing the admission slot only
// after the wrapped call returns IS releasing on "the delegated child's
// terminal state" (TryAdmit's own contract), not merely on dispatch
// acknowledgement — true for both sync and async delegation alike.
//
// Root vs nested: parentTS.depth == 0 (turnState's own "0 for root turn"
// invariant, turn.go) distinguishes a root-level dispatch from a NESTED one
// (a child, itself mid-delegation, delegating further). Only root-level
// calls consult this gate — FR-070 requires nested (concurrencySem-gated)
// behaviour stay byte-identical, and this wrapper never reads, writes, or
// otherwise touches concurrencySem.

// rootDelegationAdmittingSpawner wraps a tools.SubTurnSpawner so a
// ROOT-level delegate dispatch (parentTS.depth == 0) is admitted through a
// shared, process-wide RootDelegationAdmission gate before being allowed to
// spawn; nested (parentTS.depth > 0) calls pass straight through unchanged.
type rootDelegationAdmittingSpawner struct {
	inner tools.SubTurnSpawner
	// gate may be nil (e.g. agents.defaults.subturn.max_concurrent resolved
	// to <= 0 at AgentLoop construction — see NewAgentLoop's
	// rootDelegationAdmission resolution, loop.go). A nil gate makes
	// SpawnSubTurn a pure pass-through, matching the pre-fix (ungated)
	// behavior rather than panicking on a nil dereference.
	gate *RootDelegationAdmission
	// delegatingAgentID is captured once at wiring time (the registering
	// agent's own id) purely to label the BDD-77 refusal log/result when the
	// gate is saturated; it does not affect admission decisions.
	delegatingAgentID string
}

// newRootDelegationAdmittingSpawner constructs the wrapper described above.
func newRootDelegationAdmittingSpawner(inner tools.SubTurnSpawner, gate *RootDelegationAdmission, delegatingAgentID string) *rootDelegationAdmittingSpawner {
	return &rootDelegationAdmittingSpawner{inner: inner, gate: gate, delegatingAgentID: delegatingAgentID}
}

// SpawnSubTurn implements tools.SubTurnSpawner.
func (s *rootDelegationAdmittingSpawner) SpawnSubTurn(ctx context.Context, cfg tools.SubTurnConfig) (*tools.ToolResult, error) {
	if s == nil || s.inner == nil {
		return nil, errors.New("rootDelegationAdmittingSpawner: nil spawner")
	}
	if s.gate == nil {
		return s.inner.SpawnSubTurn(ctx, cfg)
	}
	parentTS := turnStateFromContext(ctx)
	if parentTS == nil || parentTS.depth != 0 {
		// Not a root-level dispatch (or no turnState in context at all, e.g.
		// a bare unit-test call outside a real turn) — RootDelegationAdmission
		// gates root-level fan-out only; a nested child's own fan-out stays
		// governed exclusively by its concurrencySem (FR-070).
		return s.inner.SpawnSubTurn(ctx, cfg)
	}
	ok, reason, release := s.gate.TryAdmitWithReason()
	if !ok {
		if reason == config.ReasonMemoryPressure {
			return RefuseRootDelegationForMemory(s.delegatingAgentID, cfg.TargetAgentID), nil
		}
		return RefuseRootDelegation(s.gate.Cap(), s.delegatingAgentID, cfg.TargetAgentID), nil
	}
	defer release()
	return s.inner.SpawnSubTurn(ctx, cfg)
}
