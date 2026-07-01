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

// buildDelegationContext renders the per-turn "## Delegation" system-prompt block
// telling an agent who it may delegate to and how. policy is the agent's live
// DelegationPolicy (may be nil). resolveLabel maps a target agent id to a human
// label like "Ava (Builder: implementation & code)"; ok=false means the id is
// unknown/unavailable and that target is skipped.
//
// Tool ground-truth (verified against pkg/tools/ source):
//   - spawn (spawn.go):       params task (required) + agent_id (optional) + label.
//     Background mode. Poll check_spawn_status for the result.
//   - create_task (task.go):  params agent_id + title + prompt (all required).
//     Task mode. Creates a durable tracked task.
//   - run_subagent (subagent.go): params task (required) + agent_id (optional) + label.
//     Await mode — runs the named agent SYNCHRONOUSLY (blocks) and returns its
//     result inline. Omitting agent_id runs a generic helper under your own agent.
func buildDelegationContext(policy *config.DelegationPolicy, resolveLabel func(id string) (label string, ok bool)) string {
	// nil policy or explicitly empty To list → cannot delegate.
	if policy == nil || len(policy.To) == 0 {
		return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
	}

	// Use canonical config helpers (single source of truth for mode and depth
	// semantics): IsDelegationModeAllowed and ResolveDelegationDepth.
	activeAwait := config.IsDelegationModeAllowed(policy, config.DelegationModeAwait)
	activeBackground := config.IsDelegationModeAllowed(policy, config.DelegationModeBackground)
	activeTask := config.IsDelegationModeAllowed(policy, config.DelegationModeTask)

	var sb strings.Builder
	sb.WriteString("## Delegation")

	// renderedCount tracks how many target sections were actually emitted.
	// If To is non-empty but every entry is skipped, we fall through to the
	// cannot-delegate guard below.
	renderedCount := 0

	for _, ref := range policy.To {
		// Skip non-local kinds: remote-a2a refs are advertised in policy but are
		// not resolvable within this instance in v0.1.0 (mirrors registry.CanSpawnSubagent).
		if ref.Kind != "" && ref.Kind != "local" {
			continue
		}

		var label string
		if ref.ID == "*" {
			// Wildcard: render a general "any available agent" section with a
			// clarifier that the caller must substitute a concrete id. Emitting
			// agent_id="*" verbatim would produce an uncallable invocation.
			sb.WriteString("\n\n### → any available agent")
			if activeBackground {
				sb.WriteString("\n- `spawn(agent_id=\"<id>\", task=\"…\")` — runs async; replace `<id>` with a concrete agent id. Poll `check_spawn_status` for the result.")
			}
			if activeTask {
				sb.WriteString("\n- `create_task(agent_id=\"<id>\", title=\"…\", prompt=\"…\")` — creates a durable, tracked task; replace `<id>` with a concrete agent id.")
			}
			if activeAwait {
				sb.WriteString("\n- `run_subagent(agent_id=\"<id>\", task=\"…\")` — blocks this turn; runs the named agent synchronously and returns its result inline. Replace `<id>` with a concrete agent id.")
			}
			renderedCount++
			continue
		}

		var ok bool
		label, ok = resolveLabel(ref.ID)
		if !ok {
			// Unknown or unavailable target — skip.
			continue
		}

		fmt.Fprintf(&sb, "\n\n### → %s", label)

		if activeBackground {
			// spawn: agent_id is optional but we supply the concrete id so the
			// agent can copy-paste the call. Background mode — async.
			fmt.Fprintf(&sb, "\n- `spawn(agent_id=%q, task=\"…\")` — runs async; poll `check_spawn_status` for the result.", ref.ID)
		}
		if activeTask {
			// create_task: all three params are required. NOT task_create (retired).
			fmt.Fprintf(&sb, "\n- `create_task(agent_id=%q, title=\"…\", prompt=\"…\")` — files a durable, tracked task in the task DAG.", ref.ID)
		}
		if activeAwait {
			// run_subagent(agent_id=…) runs the named agent synchronously (await mode).
			fmt.Fprintf(&sb, "\n- `run_subagent(agent_id=%q, task=\"…\")` — blocks this turn; runs %s synchronously and returns the result inline.", ref.ID, label)
		}
		renderedCount++
	}

	// All-targets-skipped guard: if every entry in To was skipped (unknown,
	// non-local kind, etc.), render the clean cannot-delegate message instead
	// of a bare header with no content.
	if renderedCount == 0 {
		return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
	}

	// Footer: depth + mode restriction notes.
	// ResolveDelegationDepth returns 0 for uncapped (nil policy or nil/<=0 Depth).
	depth := config.ResolveDelegationDepth(policy)
	if depth <= 0 {
		sb.WriteString("\n\nmax chain depth: uncapped")
	} else {
		fmt.Fprintf(&sb, "\n\nmax chain depth: %d", depth)
	}

	// Only list modes that actually produced tool lines above. If no tool line
	// was rendered for a mode entry (e.g. an unrecognized mode name), omit it
	// from the footer so the footer and tool lines stay consistent.
	if len(policy.Modes) > 0 {
		var renderedModes []string
		for _, m := range policy.Modes {
			switch m {
			case config.DelegationModeAwait, config.DelegationModeBackground, config.DelegationModeTask:
				renderedModes = append(renderedModes, string(m))
				// Unrecognized/unknown mode values: silently omitted from the footer
				// because no tool line was emitted for them.
			}
		}
		if len(renderedModes) > 0 {
			fmt.Fprintf(&sb, "\nallowed modes: %s", strings.Join(renderedModes, ", "))
		}
	}

	return sb.String()
}
