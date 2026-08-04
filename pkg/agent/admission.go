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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

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

// effectiveCap returns the cap to enforce right now: resolveCap()'s current
// value when set and positive, otherwise the fixed softCap.
func (a *AdmissionController) effectiveCap() int {
	if a.resolveCap != nil {
		if c := a.resolveCap(); c > 0 {
			return c
		}
	}
	return a.softCap
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
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, alreadyActive := a.activeScopes[scope]; alreadyActive {
		// Existing scope — follow-up turn, always admitted, no new slot consumed.
		return true, func() {}
	}

	if len(a.activeScopes) >= a.effectiveCap() {
		return false, nil
	}

	a.activeScopes[scope] = struct{}{}
	release := func() {
		a.mu.Lock()
		delete(a.activeScopes, scope)
		a.mu.Unlock()
	}
	return true, release
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
// FR-069/FR-070/FR-095 (US-15). `turnState.concurrencySem` (pkg/agent/subturn.go)
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

// ErrRootDelegationCapMisconfigured is returned by ResolveRootDelegationCap
// when agents.defaults.subturn.max_concurrent resolves to <= 0. FR-095 is
// explicit that this MUST be treated as a boot-time configuration error,
// never silently reinterpreted as "no gate" (the exact ADR-037 anti-pattern
// this project bans) — U28 seeds the key to 16 precisely so a fresh install
// never reaches this branch; it can only fire from an operator's OWN
// config.json explicitly setting the key to <= 0.
var ErrRootDelegationCapMisconfigured = errors.New(
	"agents.defaults.subturn.max_concurrent must be > 0 for the root-delegation admission gate")

// ResolveRootDelegationCap reads agents.defaults.subturn.max_concurrent
// DIRECTLY off cfg — FR-095 requires this MUST NOT go through
// getSubTurnConfig() (pkg/agent/subturn.go) or
// Performance.EffectiveMaxParallelAgents(), because both apply a fallback
// (the latter additionally hard-clamped to 16 by clampParallelExplicit,
// pkg/config/config.go) that would silently defeat an operator's explicit,
// unclamped override (e.g. AC-10's 24-deep topology). A resolved value <= 0
// is returned as ErrRootDelegationCapMisconfigured rather than coerced into
// any default — U28's seed (DefaultSubTurnMaxConcurrent = 16,
// pkg/config/defaults.go) is the ONLY source of a default value, applied at
// config-load time, not here.
func ResolveRootDelegationCap(cfg *config.Config) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("resolve root-delegation cap: %w: nil config", ErrRootDelegationCapMisconfigured)
	}
	v := cfg.Agents.Defaults.SubTurn.MaxConcurrent
	if v <= 0 {
		return 0, fmt.Errorf("resolve root-delegation cap: %w (configured value %d)", ErrRootDelegationCapMisconfigured, v)
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
	mu     sync.Mutex
	cap    int
	active int
}

// NewRootDelegationAdmission constructs a gate with the given maxCap. Callers
// MUST resolve maxCap via ResolveRootDelegationCap first (or otherwise
// validate it is > 0) — NewRootDelegationAdmission itself does not
// re-validate, so that its own zero value cannot be mistaken for "no cap
// configured yet" versus "boot already rejected this config".
func NewRootDelegationAdmission(maxCap int) *RootDelegationAdmission {
	return &RootDelegationAdmission{cap: maxCap}
}

// TryAdmit atomically claims a root-delegation slot. Returns (true, release)
// when admitted; release MUST be called (typically via defer, or on the
// delegated child's terminal state) when the slot is no longer needed.
// Returns (false, nil) IMMEDIATELY — never blocking — when the cap is
// already reached (BDD-75/FR-069: refuse, don't queue).
func (r *RootDelegationAdmission) TryAdmit() (bool, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.active >= r.cap {
		return false, nil
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
	return true, release
}

// Active returns the current number of admitted, not-yet-released root
// delegations. Used by tests and observability.
func (r *RootDelegationAdmission) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// Cap returns the configured cap this gate was constructed with.
func (r *RootDelegationAdmission) Cap() int {
	return r.cap
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
	ok, release := s.gate.TryAdmit()
	if !ok {
		return RefuseRootDelegation(s.gate.Cap(), s.delegatingAgentID, cfg.TargetAgentID), nil
	}
	defer release()
	return s.inner.SpawnSubTurn(ctx, cfg)
}
