// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// delegationTarget carries the per-edge render model for a single allowed
// delegation target. It is sourced from the workspace delegation graph
// (workspaces/<id>.json → Delegation[]), NOT from config.DelegationPolicy —
// the graph is the SOLE runtime authority (see buildDelegationDenyChecker).
//
// Fields:
//   - ID:    the target agent id (from the edge's ToAgent field)
//   - Label: the human-readable label resolved via resolveDelegationLabel;
//     empty means the target is unknown/unavailable and must be skipped.
//   - Modes: the edge's Modes field; empty ⇒ all three modes (await/background/task)
//     are allowed, matching enforceEdgeModeAndDepth semantics exactly.
//   - Depth: the edge's Depth field; nil ⇒ inherit (no per-edge cap),
//     0 ⇒ this edge grants NO onward delegation,
//     >0 ⇒ per-edge onward-delegation cap.
//     Mirrors the DEPTH INVARIANT in workspace.DelegationEdge.
type delegationTarget struct {
	ID    string
	Label string
	Modes []config.DelegationMode
	Depth *int
}

// buildDelegationContext renders the per-turn "## Delegation" system-prompt block.
//
// It takes a slice of targets sourced from the workspace delegation graph
// (each already filtered to FromAgent==callerID) and the global depth ceiling
// from defaults.SubTurn.MaxDepth (0 = uncapped).
//
// Advertisement == enforcement by construction: both read the graph, so the
// modes and targets shown to the agent are exactly what the gate allows.
//
// Tool ground-truth (verified against pkg/tools/ source):
//   - run_subagent (subagent.go): params task (required) + agent_id (optional).
//     Await mode. Runs the named agent SYNCHRONOUSLY (blocks) and returns its
//     result inline. Omitting agent_id runs a generic helper.
//   - spawn (spawn.go):       params task (required) + agent_id (optional).
//     Background mode. Poll check_spawn_status for the result.
//   - create_task (task.go):  params agent_id + title + prompt (all required).
//     Task mode. Creates a durable tracked task.
func buildDelegationContext(targets []delegationTarget, globalDepthCap int) string {
	// No targets → cannot delegate.
	if len(targets) == 0 {
		return "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	}

	var sb strings.Builder
	sb.WriteString("## Delegation")
	sb.WriteString("\n\nThis is your COMPLETE, authoritative delegation roster for THIS workspace. Answer any \"who can I delegate to\" question directly from this list — do NOT call list_agents or search memory to determine your delegation targets.")

	// renderedCount tracks how many target sections were actually emitted.
	// If every target has an empty label (unresolvable), fall through to the
	// cannot-delegate guard.
	renderedCount := 0

	for _, tgt := range targets {
		// Empty label means the target was unresolvable — skip silently.
		if tgt.Label == "" {
			continue
		}

		// Determine which modes are active for this target. Empty Modes = all three
		// allowed, matching enforceEdgeModeAndDepth semantics exactly.
		activeAwait := true
		activeBackground := true
		activeTask := true
		if len(tgt.Modes) > 0 {
			activeAwait = false
			activeBackground = false
			activeTask = false
			for _, m := range tgt.Modes {
				switch m {
				case config.DelegationModeAwait:
					activeAwait = true
				case config.DelegationModeBackground:
					activeBackground = true
				case config.DelegationModeTask:
					activeTask = true
				}
			}
		}

		fmt.Fprintf(&sb, "\n\n### → %s", tgt.Label)

		if activeBackground {
			// spawn: agent_id is optional but we supply the concrete id so the
			// agent can copy-paste the call. Background mode — async.
			fmt.Fprintf(&sb, "\n- `spawn(agent_id=%q, task=\"…\")` — runs async; poll `check_spawn_status` for the result.", tgt.ID)
		}
		if activeTask {
			// create_task: all three params are required. NOT task_create (retired).
			fmt.Fprintf(&sb, "\n- `create_task(agent_id=%q, title=\"…\", prompt=\"…\")` — files a durable, tracked task in the task DAG.", tgt.ID)
		}
		if activeAwait {
			// run_subagent(agent_id=…) runs the named agent synchronously (await mode).
			fmt.Fprintf(&sb, "\n- `run_subagent(agent_id=%q, task=\"…\")` — blocks this turn; runs %s synchronously and returns the result inline.", tgt.ID, tgt.Label)
		}

		// Per-target note when the edge forbids onward delegation (Depth <= 0,
		// mirroring the DEPTH INVARIANT in enforceEdgeModeAndDepth).
		if tgt.Depth != nil && *tgt.Depth <= 0 {
			sb.WriteString("\n  _(this target cannot delegate onward)_")
		}

		renderedCount++
	}

	// All-targets-skipped guard.
	if renderedCount == 0 {
		return "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	}

	// Exclusivity footer: makes clear that attempting to delegate to any agent
	// not listed above will be denied — all three delegation tools are gated by
	// the same workspace trust set. This directly addresses the observed
	// misbehaviour where an agent called list_agents and then attempted to
	// delegate to unlisted agents via create_task.
	sb.WriteString("\n\nThese are your ONLY permitted delegation targets. spawn / create_task / run_subagent to any other agent WILL be denied — do not attempt it.")

	// Footer: global depth ceiling.
	if globalDepthCap <= 0 {
		sb.WriteString("\n\nmax chain depth: uncapped")
	} else {
		fmt.Fprintf(&sb, "\n\nmax chain depth: %d", globalDepthCap)
	}

	return sb.String()
}
