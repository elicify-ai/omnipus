// subturn_identity.go: Resolve the target agent's identity for a delegated sub-turn (ADR-032 - never inherit from the parent).

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// ====================== Helper Functions ======================

// resolveDelegateSoul returns the soul (system-prompt text) of the configured
// agent identified by agentID, or "" when the agent has no soul / is unknown.
//
// Resolution order:
//  1. The compiled core prompt (coreagent.GetPrompt) — for seeded base agents
//     and the seeded worker. As of the RC-6 fix (see coreagent.prompts'
//     "worker" entry), the seeded worker's compiled prompt is no longer
//     empty, so this step now resolves it to a real execution-discipline
//     prompt like any other seeded agent.
//  2. The agent's on-disk SOUL.md content — for custom (non-seeded) agents,
//     including a custom Type=worker agent, whose soul is genuinely
//     OPTIONAL: no on-disk SOUL.md resolves to "".
//
// An unknown agentID resolves to "" so an unresolved target never falls back
// to the legacy generic "You are a subagent" string. The sub-turn's true
// system role is then empty for that delegate, and the task is the only
// user-facing input.
//
// Used by spawnSubTurn and runExternalCLISubTurn to compose the
// (soul, task) prompt pair uniformly across the native and external-cli
// executors (worker property-model correction: soul is OPTIONAL and the
// composition is identical for both).
func resolveDelegateSoul(al *AgentLoop, agentID string) string {
	if al == nil || agentID == "" {
		return ""
	}
	// 1. Compiled base/worker prompt.
	if compiled := coreagent.GetPrompt(agentID); compiled != "" {
		return compiled
	}
	// 2. On-disk SOUL.md for the agent's workspace. We need the agent's
	//    config to resolve the workspace, then read <workspace>/SOUL.md.
	cfg := al.GetConfig()
	if cfg == nil {
		return ""
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != agentID {
			continue
		}
		ac := cfg.Agents.List[i]
		ws := ac.Home
		if ws == "" {
			ws = cfg.Agents.Defaults.Home
		}
		if ws == "" {
			return ""
		}
		// SOUL.md is the operator's optional persona text for a custom agent.
		// Read with os; an empty/missing file is a valid worker (soul-less) state.
		path := filepath.Join(ws, "SOUL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	return ""
}

// composeDelegateInput builds the prompt string the external-cli runner sees
// as its input, matching the native path's (system, user) split: the soul is
// prepended (when present) and the task follows. When the soul is empty —
// a soul-less custom agent (a seeded worker's compiled prompt is non-empty
// as of the RC-6 fix, so this now happens only for a custom agent with no
// on-disk SOUL.md, worker or otherwise) — the input is the task alone, with
// no persona text and no legacy "You are a subagent" wrapper.
//
// An explicit ActualSystemPrompt from the caller (legacy / future callers)
// takes precedence over resolveDelegateSoul so the dispatch site is a single
// composition point.
func composeDelegateInput(al *AgentLoop, task, actualSystem, targetAgentID string) string {
	// Prefer the explicitly-supplied system prompt when present.
	soul := strings.TrimSpace(actualSystem)
	if soul == "" && targetAgentID != "" {
		soul = strings.TrimSpace(resolveDelegateSoul(al, targetAgentID))
	}
	if soul == "" {
		return task
	}
	// Same shape as the native path: the soul is the system context, the
	// task is the user input. Keep it as a single string for the external
	// CLI; the child transcript records it as one user message.
	return fmt.Sprintf("## System\n\n%s\n\n## Task\n\n%s", soul, task)
}

// resolveRequestedSkillForChild resolves requested against cb — the CHILD's
// (execSource's) OWN ContextBuilder. This is ADR-072 D9's structural gate:
// "the receiver's grant is the real gate ... there is no code path from the
// parent's ContextBuilder into this decision" — cb here is ALWAYS
// execSource's builder, never the delegating parent's, so the parent's own
// grant list has no bearing on the outcome by construction, not convention.
//
// Distinguishes three outcomes (spec FR-053/FR-054, never conflated):
// granted (with the canonical slug the child may load), denied (the slug
// exists on some shelf visible to this agent but is not granted), and
// unresolvable (the slug matches nothing on any shelf visible to this agent
// at all).
//
// cb.ResolveSkillName already applies the full per-shelf grant model
// (D4/D4.1/D4.2) but, like the human "/<slug>" door it also gates, reports
// only a single ok bool — ResolveSkillName's own doc comment states an
// installed-but-ungranted slug "cannot be resolved", the same false a
// nowhere-installed slug produces. D9 requires the two to be
// distinguishable, so on a failed resolution this additionally checks
// whether the slug matches ANY registry/builtin entry regardless of grant
// (mirroring pkg/skills.ResolveSkillName's own first-pass match, minus the
// allowed() gate) to tell "installed, not granted" from "installed
// nowhere". The project shelf needs no separate check here: a project-shelf
// slug always resolves successfully in the first place (D4.1 — the mount
// IS the grant, no per-agent list applies), so a failed resolution already
// implies it is not on the project shelf either.
func resolveRequestedSkillForChild(cb *ContextBuilder, requested string) (canonical string, outcome requestedSkillOutcome) {
	trimmed := strings.TrimSpace(requested)
	if cb == nil || cb.skillsLoader == nil || trimmed == "" {
		return "", requestedSkillUnresolvable
	}
	// Fetch the installed-skill list ONCE (ADR-072 Finding D). ListSkills()
	// is an uncached, full-directory scan, and this function previously
	// triggered it TWICE on every denied/not-found outcome — once implicitly
	// inside cb.ResolveSkillName, and again explicitly in the fallback loop
	// below. resolveSkillNameWithList lets both the resolution attempt and
	// the fallback membership check share this single fetch.
	allSkills := cb.skillsLoader.ListSkills()
	if resolved, ok := cb.resolveSkillNameWithList(allSkills, trimmed); ok {
		return resolved.Slug, requestedSkillGranted
	}
	for _, s := range allSkills {
		if strings.EqualFold(s.ID, trimmed) || strings.EqualFold(s.Name, trimmed) {
			return "", requestedSkillDenied
		}
	}
	return "", requestedSkillUnresolvable
}
