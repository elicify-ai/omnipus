// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// AdmissionController is a soft-cap gate for concurrent session workers.
//
// Phase 1: gates inbound user-message dispatch only. The counter tracks unique
// active scopes (one per spawned session worker) — not per-turn, so a single
// chatty session cannot pin admission slots indefinitely. Subagent spawn and
// task-executor dispatch paths are NOT gated; see the v0.2 follow-up issue for
// resource-aware admission that covers those paths as well.
type AdmissionController struct {
	softCap      int
	mu           sync.Mutex
	activeScopes map[string]struct{}
}

// newAdmissionController returns a controller with softCap = NumCPU() * 4.
// If softCap is provided and positive, it overrides the default.
func newAdmissionController(softCap int) *AdmissionController {
	if softCap <= 0 {
		softCap = runtime.NumCPU() * 4
	}
	return &AdmissionController{
		softCap:      softCap,
		activeScopes: make(map[string]struct{}),
	}
}

// TryAdmit atomically claims a slot for scope. Returns (true, release) when
// the scope is admitted; release MUST be called (typically via defer) when
// the scope's worker exits.
//
// If scope is already active (follow-up turn in an existing session), the
// call always succeeds without consuming an additional slot — the slot was
// already claimed when the worker was first spawned.
//
// Returns (false, nil) when the softCap is reached and scope is a new scope.
func (a *AdmissionController) TryAdmit(scope string) (bool, func()) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, alreadyActive := a.activeScopes[scope]; alreadyActive {
		// Existing scope — follow-up turn, always admitted, no new slot consumed.
		return true, func() {}
	}

	if len(a.activeScopes) >= a.softCap {
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

// SoftCap returns the configured soft cap value.
func (a *AdmissionController) SoftCap() int {
	return a.softCap
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

// NewRootDelegationAdmission constructs a gate with the given cap. Callers
// MUST resolve cap via ResolveRootDelegationCap first (or otherwise validate
// it is > 0) — NewRootDelegationAdmission itself does not re-validate, so
// that its own zero value cannot be mistaken for "no cap configured yet"
// versus "boot already rejected this config".
func NewRootDelegationAdmission(cap int) *RootDelegationAdmission {
	return &RootDelegationAdmission{cap: cap}
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
// slog.Error record naming the cap, the delegating agent and the target
// agent (mirroring pkg/tools/delegate.go:1150-1159's existing shape for the
// sibling FR-015 refusal), plus the *tools.ToolResult a caller returns to the
// calling agent. No separate user-facing notification is required
// (operator decision 6) — the tool error is the whole contract.
func RefuseRootDelegation(cap int, delegatingAgentID, targetAgentID string) *tools.ToolResult {
	slog.Error("delegate: refusing root-level delegation — concurrent root-delegation cap reached",
		"cap", cap,
		"delegating_agent_id", delegatingAgentID,
		"target_agent_id", targetAgentID)
	return tools.ErrorResult(fmt.Sprintf(
		"delegate: refusing to start a new root-level delegation — the concurrent root-delegation cap (%d) has been reached; retry once an in-flight root delegation completes",
		cap))
}
