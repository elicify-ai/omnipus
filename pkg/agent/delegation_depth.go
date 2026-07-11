// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// resolveEffectiveDelegationDepth returns the effective onward-delegation depth
// cap: the tighter of the edge's own Depth (nil = inherit, no per-edge cap) and
// the global SubTurn.MaxDepth (0 = unset, falls back to the safety-backstop
// default). Returns the safety-backstop default (defaultMaxSubTurnDepth) only
// when NEITHER source expresses an explicit value — an operator's explicit
// per-edge Depth (even when the global config is left unset) governs instead of
// being silently overridden by the backstop.
//
// This is the SOLE computation of this value anywhere in the codebase (#477,
// FR-D9/FR-D10, docs/internal/specs/agent-delegation-spec.md, User Story 3):
// the delegation graph's own authorization gate (enforceEdgeModeAndDepth), the
// spawn-time enforcement backstop (getSubTurnConfig / spawnSubTurn), and the
// delegation system-prompt builder (wireDelegationInjectors) all call this one
// function rather than each independently re-deriving the value — which is
// exactly what let an explicit per-edge Depth: 10 get silently cut off at the
// backstop's own default of 3 before this fix.
//
// Verified against the spec's "Effective depth cap resolution" dataset table:
//
//	edge=nil,  global=0  -> defaultMaxSubTurnDepth (3)  (both unset: backstop)
//	edge=nil,  global=7  -> 7                           (global governs)
//	edge=10,   global=0  -> 10                          (edge governs; the #477 bug)
//	edge=2,    global=7  -> 2                           (edge stricter)
//	edge=10,   global=2  -> 2                           (global stricter)
//
// A row where the edge's own Depth is <= 0 (forbidding ALL onward delegation)
// is NOT resolved by this function — enforceEdgeModeAndDepth denies that case
// unconditionally, before this function would ever be consulted.
func resolveEffectiveDelegationDepth(edgeDepth *int, globalMaxDepth int) int {
	hasEdge := edgeDepth != nil && *edgeDepth > 0
	hasGlobal := globalMaxDepth > 0

	switch {
	case hasEdge && hasGlobal:
		if *edgeDepth < globalMaxDepth {
			return *edgeDepth
		}
		return globalMaxDepth
	case hasEdge:
		return *edgeDepth
	case hasGlobal:
		return globalMaxDepth
	default:
		return defaultMaxSubTurnDepth
	}
}

// buildDelegationDepthResolver returns a per-agent resolver that computes the
// effective onward-delegation depth cap for one specific delegation call — the
// SAME cap enforceEdgeModeAndDepth already authorized the call against — so
// spawnSubTurn's own depth check can use that identical number (threaded via
// tools.SubTurnConfig.ResolvedMaxDepth) instead of independently re-deriving a
// possibly-different one from the raw global config alone (the #477 bug).
//
// Returns nil ("no override — fall back to spawnSubTurn's own, shared-function
// default resolution via getSubTurnConfig") for:
//   - self-assignment (targetAgentID == currentAgentID) — a DEFENSIVE no-op that
//     is UNREACHABLE for this resolver's only consumer, the delegate tool. The
//     deny checker (buildDelegationDenyChecker with selfAssignmentExempt=false),
//     consulted FIRST at the same call site (DelegateTool.executeRun runs the
//     deny checker and returns on denial BEFORE calling this resolver), DENIES a
//     self-targeted delegate() with trust_set — no self-edge can exist because
//     workspace.DelegationEdge.Validate forbids one. So a self-target never
//     reaches this branch; it is kept only as a fail-safe against a future
//     reordering of the two checks (a regression that TestDelegateTool_
//     SelfTargetDeniedBeforeDepthResolver guards). Do NOT treat this branch as a
//     live "self is allowed" decision — it is not, self-delegation is denied;
//   - the untargeted path (targetAgentID == "") — no single edge's Depth
//     uniquely applies, since evalUntargetedDelegation may be satisfied by any
//     one of several outgoing edges;
//   - any edge-lookup failure — defensive no-op. The deny checker
//     (buildDelegationDenyChecker, consulted first at the same call site) is
//     the sole authority for whether the call is permitted at all; this
//     resolver only ever refines the depth number for an already-authorized,
//     explicitly-targeted call.
func buildDelegationDepthResolver(
	currentAgentID string,
	defaults config.AgentDefaults,
) func(ctx context.Context, targetAgentID string) *int {
	globalDepthCap := defaults.SubTurn.MaxDepth

	return func(ctx context.Context, targetAgentID string) *int {
		// targetAgentID == currentAgentID is unreachable for the delegate tool:
		// the deny checker (run first) already denied self-delegation. Untargeted
		// ("") has no single edge whose Depth applies. Both are no-override no-ops.
		if targetAgentID == "" || targetAgentID == currentAgentID {
			return nil
		}
		edge, denial := findDelegationEdge(ctx, currentAgentID, targetAgentID, "")
		if denial != nil || edge == nil {
			// Should not happen in practice — the deny checker (called first)
			// already authorized this exact call — but fail safe: no override.
			return nil
		}
		if edge.Depth != nil && *edge.Depth <= 0 {
			// Edge forbids onward delegation entirely; the deny checker would
			// already have rejected this call. Defensive no-op.
			return nil
		}
		resolved := resolveEffectiveDelegationDepth(edge.Depth, globalDepthCap)
		return &resolved
	}
}
