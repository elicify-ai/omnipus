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
func buildDelegationContext(policy *config.DelegationPolicy, resolveLabel func(id string) (label string, ok bool)) string {
	// nil policy or explicitly empty To list → cannot delegate.
	if policy == nil || len(policy.To) == 0 {
		return "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
	}

	// Determine which modes are active. Empty/nil Modes means all three are allowed.
	activeAwait := true
	activeBackground := true
	activeTask := true
	if len(policy.Modes) > 0 {
		activeAwait = false
		activeBackground = false
		activeTask = false
		for _, m := range policy.Modes {
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

	var sb strings.Builder
	sb.WriteString("## Delegation")

	for _, ref := range policy.To {
		var label string
		if ref.ID == "*" {
			label = "any available agent"
		} else {
			var ok bool
			label, ok = resolveLabel(ref.ID)
			if !ok {
				// Unknown or unavailable target — skip.
				continue
			}
		}

		fmt.Fprintf(&sb, "\n\n### → %s", label)

		if activeAwait {
			fmt.Fprintf(&sb, "\n- `run_subagent(agent=%q, task=\"…\")` — blocks this turn; returns the target's result inline.", ref.ID)
		}
		if activeBackground {
			fmt.Fprintf(&sb, "\n- `spawn(agent=%q, task=\"…\")` — runs async; poll `check_spawn_status` for the result.", ref.ID)
		}
		if activeTask {
			fmt.Fprintf(&sb, "\n- `task_create(agent=%q, title=\"…\")` — files a durable, tracked task in the task DAG.", ref.ID)
		}
	}

	// Footer: depth + mode restriction notes.
	var depthStr string
	if policy.Depth == nil {
		depthStr = "uncapped"
	} else {
		depthStr = fmt.Sprintf("%d", *policy.Depth)
	}
	fmt.Fprintf(&sb, "\n\nmax chain depth: %s", depthStr)

	if len(policy.Modes) > 0 {
		modeNames := make([]string, len(policy.Modes))
		for i, m := range policy.Modes {
			modeNames[i] = string(m)
		}
		fmt.Fprintf(&sb, "\nallowed modes: %s", strings.Join(modeNames, ", "))
	}

	return sb.String()
}
