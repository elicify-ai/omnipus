// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills & Tools C-1, C-11 E2, C-15: the skill menu of one agent's turn
// lists each skill at its stored level and never lists a skill whose
// outcome for the agent is deny. The answer is read from home once per menu
// build, like the tool levels.

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// SkillMenuView is one agent's stored skill levels and outcomes.
type SkillMenuView struct {
	home        string
	agentID     string
	workspaceID string
	levels      map[string]string
	settings    config.PersistedSkillSettings
	agentSkills *config.AgentSkillsCfg
	// decided is false before the permission upgrade (no skill values on
	// either side): the grant alone decides, as before.
	decided bool
	stamp   string
}

// ReadSkillMenuView reads the menu state of agentID under home. An agent
// with no stored record keeps only the stored levels.
func ReadSkillMenuView(home, agentID, workspaceID string) (SkillMenuView, error) {
	v := SkillMenuView{home: home, agentID: agentID, workspaceID: workspaceID}
	vis, err := config.ReadPersistedVisibility(home)
	if err != nil {
		return v, err
	}
	v.levels = vis.Skills
	if v.settings, err = config.ReadPersistedSkillSettings(home); err != nil {
		return v, err
	}
	var grants []string
	if agentID != "" {
		if _, statErr := os.Stat(agentstore.RecordsDir(home)); statErr == nil {
			rec, getErr := agentstore.New(home).Get(agentID)
			switch {
			case getErr == nil:
				grants = rec.Skills
				if rec.Tools != nil {
					v.agentSkills = rec.Tools.Skills
				}
			case !errors.Is(getErr, entity.ErrNotFound):
				return v, fmt.Errorf("agent record %s: %w", agentID, getErr)
			}
		}
	}
	v.decided = v.settings.Policies != nil || (v.agentSkills != nil && len(v.agentSkills.Policies) > 0)
	raw, err := json.Marshal(struct {
		Levels   map[string]string
		Settings config.PersistedSkillSettings
		Agent    *config.AgentSkillsCfg
		Grants   []string
		Approved string
	}{v.levels, v.settings, v.agentSkills, grants, skills.RecordsFingerprint(filepath.Join(home, "skills"))})
	if err != nil {
		return v, fmt.Errorf("skill menu stamp: %w", err)
	}
	h := fnv.New64a()
	_, _ = h.Write(raw)
	v.stamp = fmt.Sprintf("%x", h.Sum64())
	return v, nil
}

// Stamp fingerprints everything the menu depends on besides the installed
// skills themselves (those move skills.Generation).
func (v SkillMenuView) Stamp() string { return v.stamp }

// Level is the stored level of the skill key (name_and_description when
// none is stored, C-1).
func (v SkillMenuView) Level(key string) string {
	if level, ok := v.levels[config.NormalizeSkillKey(key)]; ok && level != "" {
		return level
	}
	return config.VisibilityNameAndDescription
}

// key returns the stored key of a menu candidate.
func (v SkillMenuView) key(s skills.SkillInfo, project bool) string {
	if project {
		return config.ProjectSkillKey(v.workspaceID, s.ID)
	}
	return config.NormalizeSkillKey(s.ID)
}

// Decision is the C-11 decision of a menu candidate. Before the permission
// upgrade every listed candidate is allowed (the grant already filtered it).
func (v SkillMenuView) Decision(s skills.SkillInfo, project bool) (SkillDecision, error) {
	if !v.decided {
		return SkillDecision{Outcome: SkillOutcomeAllow}, nil
	}
	if project {
		return projectSkillDecision(v.home, v.settings, v.agentSkills, v.key(s, true), filepath.Dir(s.Path))
	}
	return InstalledSkillDecision(v.home, v.settings, v.agentSkills, s.ID, true)
}

// MenuLevel answers skills.SkillMenuLevel for this view. An unreadable
// decision leaves the skill out of the menu (fail closed) and is logged.
func (v SkillMenuView) MenuLevel(s skills.SkillInfo, project bool) (string, bool) {
	level := v.Level(v.key(s, project))
	d, err := v.Decision(s, project)
	if err != nil {
		logger.ErrorCF("tools", "skill menu: skill decision unreadable; the skill is not listed this turn",
			map[string]any{"agent_id": v.agentID, "skill": s.ID, "error": err.Error()})
		return level, true
	}
	return level, d.Outcome == SkillOutcomeDeny
}

// FullInjected reports whether a menu candidate's whole instructions are
// injected (C-14): its level is full and its outcome for the agent is
// allowed. An unreadable decision is logged and injects nothing.
func (v SkillMenuView) FullInjected(s skills.SkillInfo, project bool) bool {
	if v.Level(v.key(s, project)) != config.VisibilityFull {
		return false
	}
	d, err := v.Decision(s, project)
	if err != nil {
		logger.ErrorCF("tools", "Full skill: skill decision unreadable; its instructions are not injected",
			map[string]any{"agent_id": v.agentID, "skill": s.ID, "error": err.Error()})
		return false
	}
	return d.Outcome == SkillOutcomeAllow
}
