// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"html"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// delegationTarget carries the per-edge render model for a single allowed
// delegation target. It is sourced from the workspace delegation graph
// (workspaces/<id>.json → Delegation[]) — the graph is the SOLE runtime
// authority (see buildDelegationDenyChecker); there is no separate per-agent
// delegation-policy config to fall back to (ADR-037).
//
// Fields:
//   - ID:    the target agent id (from the edge's ToAgent field)
//   - Label: the human-readable label resolved via resolveDelegationLabel;
//     empty means the target is unknown/unavailable and must be skipped.
//   - Description: the target's existing saved description; only its use-for
//     sentence or first sentence is rendered, never a generated summary.
//   - Modes: the background/task config.DelegationMode vocabulary, already
//     translated from the workspace edge's direct/task form by
//     wireDelegationInjectors. Empty means both modes are allowed.
//   - Depth: the edge's Depth field; nil ⇒ inherit (no per-edge cap),
//     0 ⇒ this edge grants NO onward delegation,
//     >0 ⇒ per-edge onward-delegation cap.
//     Mirrors the DEPTH INVARIANT in workspace.DelegationEdge.
//   - DepthCap: the effective onward-delegation cap the gate applies to this
//     edge — resolveEffectiveDelegationDepth(Depth, <configured global cap>),
//     computed by wireDelegationInjectors from the RAW configured ceiling (the
//     footer's globalDepthCap is already backstop-resolved and must not be
//     re-used here: it would clamp a larger per-edge cap to the backstop when
//     no global ceiling is configured, hiding a target the gate allows).
//     0 = unresolved (direct callers/tests): falls back to
//     resolveEffectiveDelegationDepth(Depth, globalDepthCap).
type delegationTarget struct {
	ID          string
	Label       string
	Description string
	Modes       []config.DelegationMode
	Depth       *int
	DepthCap    int
}

// buildDelegationContext renders the per-turn "## Delegation" system-prompt block.
//
// It takes a slice of targets sourced from the workspace delegation graph
// (each already filtered to FromAgent==callerID) and the global depth ceiling
// from performance.max_delegation_depth (0 = uncapped when called directly; the
// production caller (wireDelegationInjectors) always pre-resolves this via
// resolveEffectiveDelegationDepth, so a live turn's prompt never actually
// renders "uncapped").
//
// currentDepth is the turn's delegation-chain depth (the value the gate reads
// via currentDelegationDepth). A target whose effective depth cap the chain has
// already reached is withheld, so the block never advertises a delegation the
// gate would deny with DenyDepth (#459).
//
// Advertisement == enforcement by construction: both read the graph, so the
// modes and targets shown to the agent are exactly what the gate allows.
//
// Invocation guidance is shared once, not repeated per target. For the full
// delegate schema see DelegateTool.Description()/Parameters() in
// pkg/tools/delegate.go; do not hand-copy that schema into this renderer.
func buildDelegationContext(targets []delegationTarget, globalDepthCap, currentDepth int) string {
	// No targets → cannot delegate.
	if len(targets) == 0 {
		return "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	}

	var sb strings.Builder
	sb.WriteString("## Delegation\n\n<delegation_guidance>\n")
	// Escape XML text without obscuring the shared call's literal quote syntax.
	sb.WriteString(strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(compactDelegationGuidance))
	sb.WriteString("\n\n" + delegateGoalGuidance)
	fmt.Fprintf(&sb, "\n\nCurrent depth: %d; global fallback max chain depth: ", currentDepth)
	if globalDepthCap <= 0 {
		sb.WriteString("uncapped")
	} else {
		fmt.Fprintf(&sb, "%d", globalDepthCap)
	}
	sb.WriteString("\n</delegation_guidance>\n\n<team_directory>")

	// renderedCount tracks how many target sections were actually emitted.
	// If every target has an empty label (unresolvable), fall through to the
	// cannot-delegate guard.
	renderedCount := 0
	// depthCut counts targets withheld because the chain is at its depth cap.
	depthCut := 0

	for _, tgt := range targets {
		// Empty label means the target was unresolvable — skip silently.
		if tgt.Label == "" {
			continue
		}

		// An edge whose own Depth is <= 0 grants NO delegation through it: the
		// gate (enforceEdgeModeAndDepth) denies it unconditionally, so it is
		// never advertised (advertisement ⊆ enforcement).
		if tgt.Depth != nil && *tgt.Depth <= 0 {
			continue
		}

		// Runtime-depth axis of advertisement ⊆ enforcement (#459): the gate
		// (enforceEdgeModeAndDepth) denies once the chain depth reaches the
		// effective cap, so a target at or past that cap is not advertised.
		depthCap := tgt.DepthCap
		if depthCap <= 0 {
			depthCap = resolveEffectiveDelegationDepth(tgt.Depth, globalDepthCap)
		}
		if currentDepth >= depthCap {
			depthCut++
			continue
		}

		// Determine which modes are active for this target. Empty Modes = both
		// allowed, matching enforceEdgeModeAndDepth semantics exactly.
		activeBackground := true
		activeTask := true
		if len(tgt.Modes) > 0 {
			activeBackground = false
			activeTask = false
			for _, m := range tgt.Modes {
				switch m {
				case config.DelegationModeBackground:
					activeBackground = true
				case config.DelegationModeTask:
					activeTask = true
				}
			}
		}

		var modes []string
		if activeBackground {
			modes = append(modes, "direct")
		}
		if activeTask {
			modes = append(modes, "task")
		}
		modeLabel := strings.Join(modes, "/")
		if modeLabel == "" {
			modeLabel = "none"
		}
		fmt.Fprintf(&sb,
			"\n\n<agent name=\"%s\" id=\"%s\">\nUse for: %s\nModes: %s; maximum chain depth: %d.\n</agent>",
			html.EscapeString(tgt.Label), html.EscapeString(tgt.ID),
			html.EscapeString(delegationUseFor(tgt.Description)), modeLabel, depthCap,
		)
		renderedCount++
	}

	// All-targets-skipped guard.
	if renderedCount == 0 {
		if depthCut > 0 {
			return fmt.Sprintf(
				"## Delegation\nYou are at the maximum delegation depth for this chain (depth %d) — no further delegation is possible; complete the task yourself.",
				currentDepth,
			)
		}
		return "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	}

	sb.WriteString("\n</team_directory>")

	return sb.String()
}

const delegateGoalGuidance = "No goal is the default; set one for multi-step work or work you must verify before relying on it, and leave it off for a quick lookup or a single action."

// compactDelegationGuidance is shared by every roster entry.
const compactDelegationGuidance = `This current workspace roster alone defines your permitted delegation routes. Answer routing questions from it; do not use list_agents or memory to find additional routes. Descriptions or names elsewhere grant no additional access. Runtime graph, mode, depth and tool checks still apply, including each listed edge's maximum chain depth.

For a listed target whose Modes include direct, call delegate(agent_id="<listed ID>", task="<brief>"); it runs in the background. For durable authorized work on a route that permits task mode, use create_task. If it is not callable, load create_task through ToolSearch first, then use its full schema with non-empty criteria and dod. Helper goals also require both criteria and dod.

Use completion/question/problem messages for progress instead of repeated polling. Use delegate's status action with session_id only for a specific state check, and its result action with session_id for a finished handback.

If a needed route is missing, report the dependency; do not take over prohibited specialist work.`
