//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// GREEN bindings for the Skills & Tools RED pack in this package. Each seam
// passes its inputs to the production entry point and returns what that
// entry point produced.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

func init() {
	skillOutcomeOf = func(in skillOutcomeInput) (string, string) {
		d := ResolveSkillDecision(SkillDecisionInput{
			Key: "skill-under-test", Granted: in.granted,
			Global: config.ToolPolicy(in.global), Agent: config.ToolPolicy(in.agentValue),
			Trust: config.SkillTrustLevel(in.trust), Verified: in.verified, GodMode: in.godMode,
		})
		// The test reads "refusal" as the grant-gate refusal of the entry
		// point (D1#14); the other denies are worded by the setting.
		refusal := ""
		if d.Reason == SkillDenyNotGranted {
			refusal = SkillRefusalText("skill-under-test", d.Reason)
		}
		return string(d.Outcome), refusal
	}

	seedSkillOutcomeForAgent = func(t testing.TB, home, agentID, skill, policy string, approved bool) {
		t.Helper()
		key := config.NormalizeSkillKey(skill)
		if _, err := agentstore.New(home).Update(agentID, func(cfg *config.AgentConfig) error {
			if cfg.Tools == nil {
				cfg.Tools = &config.AgentToolsCfg{}
			}
			if cfg.Tools.Skills == nil {
				cfg.Tools.Skills = &config.AgentSkillsCfg{Policies: map[string]config.ToolPolicy{}}
			}
			cfg.Tools.Skills.Policies[key] = config.ToolPolicy(policy)
			return nil
		}); err != nil {
			t.Fatalf("seed agent skill policy: %v", err)
		}
		if !approved {
			return
		}
		skillsDir := filepath.Join(home, "skills")
		_, revision, err := skills.SkillVerified(skillsDir, skill)
		if err != nil {
			t.Fatalf("read skill revision: %v", err)
		}
		if err := skills.RecordTrustApproval(skillsDir, skills.TrustApproval{
			Key: key, Revision: revision, ApprovedBy: "test-person", ApprovedAt: time.Now(),
		}); err != nil {
			t.Fatalf("record trust approval: %v", err)
		}
		store := security.NewApprovalGrantStore()
		if _, ok := store.RecordSkillAllowance(agentID, key, security.AllowFor1Hour); !ok {
			t.Fatal("record timed skill allowance refused")
		}
		SetSkillAllowanceStore(store)
		t.Cleanup(func() { SetSkillAllowanceStore(nil) })
	}

	readInstalledSkillCategory = func(_ testing.TB, globalSkillsDir, slug string) (string, bool) {
		rec, ok, err := skills.ReadInstallRecord(globalSkillsDir, slug)
		if err != nil || !ok {
			return "", false
		}
		return rec.Category, true
	}
}
