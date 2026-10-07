// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/config"
)

// failClosedBoundedDepth is the tightest positive bound, used where a setter
// treats 0 as "unset, use the default" and so cannot be told "none".
const failClosedBoundedDepth = 1

// configuredDelegationDepth reads the operator's depth limit through the one
// shared reader, config.ConfiguredMaxDelegationDepth: an invalid value logs
// ERROR with config_key and site and reports valid=false, and each caller fails
// closed in its own terms — the block renders cannot-delegate, the gate denies,
// the launch budget is 0, the bound-style setters take failClosedBoundedDepth,
// and the workspace edge validators use a ceiling no positive depth satisfies.
func configuredDelegationDepth(perf config.PerformanceConfig, site string) (limit int, valid bool) {
	return config.ConfiguredMaxDelegationDepth(perf, site)
}

// delegationDepthBound is the bound the bound-style consumers (the ownership
// walk, the task recursion ceiling) install: the shared effective depth when the
// limit is valid, failClosedBoundedDepth when it is invalid (see
// configuredDelegationDepth).
func delegationDepthBound(perf config.PerformanceConfig, site string) int {
	limit, valid := configuredDelegationDepth(perf, site)
	if !valid {
		return failClosedBoundedDepth
	}
	return resolveEffectiveDelegationDepth(nil, limit)
}

// resolveEffectiveDelegationDepth returns the effective onward-delegation depth
// cap: the tighter of the edge's own Depth (nil = inherit, no per-edge cap) and
// performance.max_delegation_depth (0 = unset, falls back to the safety-backstop
// default). Returns the safety-backstop default (defaultMaxSubTurnDepth) only
// when NEITHER source expresses an explicit value — an operator's explicit
// per-edge Depth (even when the global config is left unset) governs instead of
// being silently overridden by the backstop.
//
// This is the SOLE computation of this value anywhere in the codebase (#477,
// FR-D9/FR-D10, docs/internal/specs/agent-delegation-spec.md, User Story 3):
// delegation graph's authorization gate, the launcher, and the delegation
// system-prompt builder all call this one function rather than independently
// re-deriving the value.
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
