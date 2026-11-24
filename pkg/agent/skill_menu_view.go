// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-1, C-11 E2, C-15: the skill menu lists each skill at its
// stored level and leaves out a skill whose outcome for this agent is deny.
// The cached system prompt and the frozen session menu are keyed by a
// generation that moves with the installed skills (skills.Generation) AND
// with the stored levels and outcomes, so a change applies from the next
// turn.

import (
	"fmt"
	"hash/fnv"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// skillMenuView reads this agent's stored skill levels and outcomes. An
// unreadable state is logged and reported as not ok.
func (cb *ContextBuilder) skillMenuView(workspaceID string) (tools.SkillMenuView, error) {
	view, err := tools.ReadSkillMenuView(omnipusHome(), cb.agentID, workspaceID)
	if err != nil {
		logger.ErrorCF("agent", "skill menu: stored skill levels unreadable; no skill is listed this turn",
			map[string]any{"agent_id": cb.agentID, "error": err.Error()})
	}
	return view, err
}

// skillMenuLevel returns the menu level function for this agent. When the
// stored state is unreadable no skill is listed (fail closed).
func (cb *ContextBuilder) skillMenuLevel(workspaceID string) skills.SkillMenuLevel {
	view, err := cb.skillMenuView(workspaceID)
	if err != nil {
		return denyEverySkill
	}
	return view.MenuLevel
}

// denyEverySkill lists no skill.
func denyEverySkill(skills.SkillInfo, bool) (string, bool) { return "", true }

// skillMenuGeneration is skills.Generation combined with the stamp of the
// stored skill levels and outcomes.
func (cb *ContextBuilder) skillMenuGeneration(workspaceID string) uint64 {
	generation := skills.Generation()
	stamp := ""
	if view, err := tools.ReadSkillMenuView(omnipusHome(), cb.agentID, workspaceID); err != nil {
		stamp = "unreadable: " + err.Error()
	} else {
		stamp = view.Stamp()
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d|%s", generation, stamp)
	return h.Sum64()
}
