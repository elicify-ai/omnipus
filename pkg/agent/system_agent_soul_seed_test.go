// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// system_agent_soul_seed_test.go covers SeedSystemAgentSoulFile's id-generic
// half (ADR-055 / plan-supervisor-spec FR-005). The Judge-specific cases live
// in verifier_adjudication_test.go; these assert the behaviour that only
// became possible once the helper stopped hardcoding
// coreagent.JudgeDefaultRubric — writing the PlanSupervisor's adjudication
// rubric to ITS workspace, and refusing (loudly, not silently) to write an
// empty soul for an id that has no compiled default.
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestSeedSystemAgentSoulFile_WritesPlanSupervisorRubric proves the helper
// sources its content from coreagent.SystemAgentDefaultSoul rather than one
// hardcoded constant: the same call that writes the Judge's rubric for
// IDJudge must write the PlanSupervisor's for IDPlanSupervisor. Before this,
// PlanSupervisorDefaultRubric had no write path at all and the adjudicator
// would have woken with an empty prompt.
func TestSeedSystemAgentSoulFile_WritesPlanSupervisorRubric(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "plansupervisor")

	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDPlanSupervisor); err != nil {
		t.Fatalf("SeedSystemAgentSoulFile(plansupervisor): %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	if err != nil {
		t.Fatalf("read seeded SOUL.md: %v", err)
	}
	if string(got) != coreagent.PlanSupervisorDefaultRubric {
		t.Errorf("PlanSupervisor SOUL.md must contain PlanSupervisorDefaultRubric, got %q", string(got))
	}
	if strings.TrimSpace(string(got)) == coreagent.JudgeDefaultRubric {
		t.Error("PlanSupervisor SOUL.md must not carry the Judge's rubric")
	}
}

// TestSeedSystemAgentSoulFile_NeverOverwritesEditedPlanSupervisorSoul locks
// the operator-editable contract for the PlanSupervisor specifically: the
// gateway re-runs this seed on EVERY boot, so an overwrite would revert a
// tuned rubric on the next restart.
func TestSeedSystemAgentSoulFile_NeverOverwritesEditedPlanSupervisorSoul(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "plansupervisor")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	const edited = "You are the Plan Supervisor. House rule: prefer APPEND over SUPERSEDE."
	soulPath := filepath.Join(workspace, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("write operator-edited soul: %v", err)
	}

	if err := SeedSystemAgentSoulFile(workspace, coreagent.IDPlanSupervisor); err != nil {
		t.Fatalf("SeedSystemAgentSoulFile(plansupervisor): %v", err)
	}

	got, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("read SOUL.md: %v", err)
	}
	if string(got) != edited {
		t.Errorf("an operator-edited PlanSupervisor soul must survive a re-seed, got %q", string(got))
	}
}

// TestSeedSystemAgentSoulFile_NoDefaultSoulIsAnError proves the helper fails
// loudly instead of writing an empty SOUL.md for an id with no compiled
// default. An empty file would be worse than no file: the next boot's
// backfill-only-when-empty rule would rewrite it anyway, but any reader in
// between would see a System Agent with a blank prompt and no error anywhere.
func TestSeedSystemAgentSoulFile_NoDefaultSoulIsAnError(t *testing.T) {
	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, "not-an-agent"} {
		workspace := filepath.Join(t.TempDir(), string(id))
		err := SeedSystemAgentSoulFile(workspace, id)
		if err == nil {
			t.Errorf("SeedSystemAgentSoulFile(%q) must return an error, got nil", id)
		}
		if _, statErr := os.Stat(filepath.Join(workspace, "SOUL.md")); !os.IsNotExist(statErr) {
			t.Errorf("SeedSystemAgentSoulFile(%q) must not create a SOUL.md, stat err = %v", id, statErr)
		}
	}
}
