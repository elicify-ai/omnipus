// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills & Tools C-17 (ST-Q6 A): ToolSearch also finds the calling agent's
// granted installed skills whose C-11 outcome is not deny, whatever their
// level. A hit is one line naming the skill and how to load it; the search
// never loads a skill and never returns its instructions.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// skillSearchHit is one skill ToolSearch may name.
type skillSearchHit struct {
	slug, description string
	score             int
}

// skillSearchLine is the C-17 result line of one skill.
func skillSearchLine(h skillSearchHit) string {
	return fmt.Sprintf("[skill] %s — %s Load it with the Skill tool.", h.slug, strings.TrimSpace(h.description))
}

// SkillSearchInputs returns the skill settings and the agent's own skill
// values a search decides from: the same live values a Skill load reads.
// ok is false when no configuration is loaded.
type SkillSearchInputs func(agentID string) (settings config.PersistedSkillSettings, agentSkills *config.AgentSkillsCfg, ok bool)

// searchAgentSkills returns the granted, not-denied installed skills of the
// calling agent under home that match query, best first. With no home, no
// agent or no loaded configuration nothing is returned.
func searchAgentSkills(ctx context.Context, home string, inputs SkillSearchInputs, query string, limit int) []skillSearchHit {
	agentID := ToolAgentID(ctx)
	if home == "" || agentID == "" || inputs == nil {
		return nil
	}
	settings, agentSkills, ok := inputs(agentID)
	if !ok {
		return nil
	}
	return searchSkillsIn(home, agentID, settings, agentSkills, query, limit)
}

func searchSkillsIn(home, agentID string, settings config.PersistedSkillSettings, agentSkills *config.AgentSkillsCfg, query string, limit int) []skillSearchHit {
	if _, err := os.Stat(agentstore.RecordsDir(home)); err != nil {
		return nil
	}
	rec, err := agentstore.New(home).Get(agentID)
	if err != nil {
		if !errors.Is(err, entity.ErrNotFound) {
			logger.WarnCF("tools", "ToolSearch: agent record unreadable; no skill is searched",
				map[string]any{"agent_id": agentID, "error": err.Error()})
		}
		return nil
	}
	granted := map[string]bool{}
	for _, g := range rec.Skills {
		granted[config.NormalizeSkillKey(g)] = true
	}
	terms := strings.Fields(strings.ToLower(query))
	loader := skills.NewSkillsLoader("", filepath.Join(home, "skills"), "")
	var hits []skillSearchHit
	for _, s := range loader.ListSkills() {
		if !granted[config.NormalizeSkillKey(s.ID)] {
			continue
		}
		score := skillMatchScore(terms, s.ID+" "+s.Name+" "+s.Description)
		if score == 0 {
			continue
		}
		d, err := InstalledSkillDecision(home, settings, agentSkills, s.ID, true)
		if err != nil || d.Outcome == SkillOutcomeDeny {
			continue
		}
		hits = append(hits, skillSearchHit{slug: s.ID, description: s.Description, score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// skillMatchScore counts the query terms found in text.
func skillMatchScore(terms []string, text string) int {
	text = strings.ToLower(text)
	n := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			n++
		}
	}
	return n
}

// withSkillHits appends the skill lines to a ToolSearch query result.
func withSkillHits(res *ToolResult, hits []skillSearchHit) *ToolResult {
	if len(hits) == 0 {
		return res
	}
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, skillSearchLine(h))
	}
	text := strings.Join(lines, "\n")
	if res == nil || res.IsError || strings.HasPrefix(res.ForLLM, "No tools found") {
		return SilentResult(text)
	}
	res.ForLLM += "\n\n" + text
	return res
}
