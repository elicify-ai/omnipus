// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// SelfEdgeSeedRow returns the ordinary self-delegation seed candidate for
// agentID, or (zero, false) when agentID is empty or listed in excluded.
//
// Session-core C-DELEGATE (FR-014/015, BDD-05.7) makes {from_agent==to_agent} an
// ORDINARY workspace delegation row, so a freshly introduced eligible agent
// receives one just like any other member. The row carries the collapsed
// ordinary modes direct|task and a fresh self-edge depth of
// min(FreshSelfDelegationMaxDepth, ceiling) — the same pin coreagent applies to
// every self-edge, computed through coreagent.SeededEdgeDepth so there is
// exactly one depth rule.
//
// excluded is the OPERATOR seed data (config.workspace_seed_defaults.self_edge.
// exclude_agent_ids, resolved by the caller) — the exclusion is DATA, never a
// Go identity predicate: an operator adding or removing an id changes future
// seed output with no code change. The caller supplies it as a set so this
// package imports no config type.
//
// This is the SINGLE seed computation every writer shares; keeping it here (the
// workspace graph package) rather than in each writer is what stops the
// install-seed, team-growth and create-in-agent join paths from drifting.
func SelfEdgeSeedRow(agentID string, excluded map[string]bool, ceiling int) (DelegationEdge, bool) {
	id := strings.TrimSpace(agentID)
	if id == "" || excluded[id] {
		return DelegationEdge{}, false
	}
	return DelegationEdge{
		FromAgent: id,
		ToAgent:   id,
		Modes:     []DelegationMode{ModeDirect, ModeTask},
		Depth:     coreagent.SeededEdgeDepth(id, id, nil, ceiling),
	}, true
}

// SelfEdgeSeedRows returns the ordinary self-row seed candidates for every id in
// introduced that is eligible and not already self-edged in existing.
//
// It is the shared computation the three seed writers call, wired to the
// bridge inputs the spec names (C-DELEGATE ::Shared seed owner / producer
// scope): introduced is the normalized set of NEWLY introduced members (an
// authoritative before/after core_team diff, or the whole roster for a fresh
// workspace), existing is the current graph (so a self-row an operator REMOVED
// is never resurrected for a continuing member — deletion is authoritative),
// excluded is the operator seed data, and ceiling is the live depth ceiling.
//
// Ordering follows introduced, and duplicate ids yield one row. A nil/empty
// introduced or an all-excluded set yields nil.
func SelfEdgeSeedRows(introduced []string, existing []DelegationEdge, excluded map[string]bool, ceiling int) []DelegationEdge {
	if len(introduced) == 0 {
		return nil
	}
	have := make(map[string]bool, len(existing))
	for _, e := range existing {
		if e.FromAgent == e.ToAgent {
			have[strings.TrimSpace(e.FromAgent)] = true
		}
	}
	seen := make(map[string]bool, len(introduced))
	var out []DelegationEdge
	for _, id := range introduced {
		row, ok := SelfEdgeSeedRow(id, excluded, ceiling)
		if !ok || seen[row.FromAgent] || have[row.FromAgent] {
			continue
		}
		seen[row.FromAgent] = true
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
