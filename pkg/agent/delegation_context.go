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
// from defaults.SubTurn.MaxDepth (0 = uncapped when called directly; the
// production caller (wireDelegationInjectors) always pre-resolves this via
// resolveEffectiveDelegationDepth, so a live turn's prompt never actually
// renders "uncapped").
//
// Advertisement == enforcement by construction: both read the graph, so the
// modes and targets shown to the agent are exactly what the gate allows.
//
// Tool ground-truth (verified against pkg/tools/delegate.go — ADR-036 merge of
// the former spawn / run_subagent / check_spawn_status trio into one tool):
//   - delegate (delegate.go): params task (required), agent_id (optional),
//     async (optional bool, default true), action (enum run|status, default
//     run), task_id (required for action=status).
//     async=true (the default, background mode): runs in the background —
//     poll delegate(action="status", task_id=...) for the result.
//     async=false (await mode): runs SYNCHRONOUSLY (blocks this turn) and
//     returns the result inline.
//   - create_task (task.go):  params agent_id + title + prompt (all required).
//     Task mode. Creates a durable tracked task.
func buildDelegationContext(targets []delegationTarget, globalDepthCap int) string {
	// No targets → cannot delegate.
	if len(targets) == 0 {
		return "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	}

	var sb strings.Builder
	sb.WriteString("## Delegation")
	sb.WriteString(
		"\n\nThis is your COMPLETE, authoritative delegation roster for THIS workspace. Answer any \"who can I delegate to\" question directly from this list — do NOT call list_agents or search memory to determine your delegation targets.",
	)

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
			// delegate: agent_id is optional but we supply the concrete id so
			// the agent can copy-paste the call. Background mode — async is
			// the default, so no async=... suffix is needed here.
			fmt.Fprintf(
				&sb,
				"\n- `delegate(agent_id=%q, task=\"…\")` — runs async by default; poll `delegate(action=\"status\", task_id=\"…\")` for the result.",
				tgt.ID,
			)
		}
		if activeTask {
			// create_task: all three params are required. NOT task_create (retired).
			fmt.Fprintf(
				&sb,
				"\n- `create_task(agent_id=%q, title=\"…\", prompt=\"…\")` — files a durable, tracked task in the task DAG.",
				tgt.ID,
			)
		}
		if activeAwait {
			// delegate(agent_id=…, async=false) runs the named agent synchronously (await mode).
			fmt.Fprintf(
				&sb,
				"\n- `delegate(agent_id=%q, task=\"…\", async=false)` — blocks this turn; runs %s synchronously and returns the result inline.",
				tgt.ID,
				tgt.Label,
			)
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
	// not listed above will be denied — both delegation tools are gated by
	// the same workspace trust set. This directly addresses the observed
	// misbehavior where an agent called list_agents and then attempted to
	// delegate to unlisted agents via create_task.
	sb.WriteString(
		"\n\nThese are your ONLY permitted delegation targets. delegate / create_task to any other agent WILL be denied — do not attempt it.",
	)

	// Footer: global depth ceiling. globalDepthCap <= 0 only occurs when this
	// function is called directly with 0 (e.g. from a test) — the production
	// caller pre-resolves through resolveEffectiveDelegationDepth, which never
	// returns 0, so this "uncapped" branch is unreachable on a live turn.
	if globalDepthCap <= 0 {
		sb.WriteString("\n\nmax chain depth: uncapped")
	} else {
		fmt.Fprintf(&sb, "\n\nmax chain depth: %d", globalDepthCap)
	}

	return sb.String()
}
