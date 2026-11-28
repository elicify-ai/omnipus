// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent

// Office-document preview — registration and per-role policy seeds
// (office-preview spec AD-13, FR-016; TDD rows T-20 policy entries,
// T-21/T-22 policy-inventory leg, T-27 seed leg of the skills move).
//
// Oracles come from the spec (AD-13), never from the code:
//   - "preview_document" is a member of allStaticToolNames;
//   - Allow via commonWork for Mia, Jim, Planner, Researcher and General
//     Purpose (Worker) in ADR090RolePolicyInventory;
//   - Allow in NewCustomAgentToolsCfg (custom agents, main and worker);
//   - Omnipus explicit Deny; hidden system agents keep Deny;
//   - coreAgentSkills seeds "elicify-office" (AD-14), never the four old
//     skill ids (FR-023, B-30).
//
// RED reason on the pre-change code: none of the four registration sites
// carries the tool yet, and the four old skill ids are still seeded.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const officePreviewToolName = "preview_document"

// TestOfficePreview_InAllStaticToolNames (AD-13): the tool is part of the
// static catalog literal; validateOverrideKeys panics on unknown override
// keys, so a policy seed for the tool without this membership cannot boot.
func TestOfficePreview_InAllStaticToolNames(t *testing.T) {
	for _, name := range allStaticToolNames {
		if name == officePreviewToolName {
			return
		}
	}
	t.Fatalf("BLOCKED: %q absent from allStaticToolNames — required by office-preview spec AD-13 (T-20)", officePreviewToolName)
}

// TestOfficePreview_RoleInventoryOrdinaryRolesAllow (AD-13): every ordinary
// role's inventory carries preview_document at allow (commonWork).
func TestOfficePreview_RoleInventoryOrdinaryRolesAllow(t *testing.T) {
	roles := []CoreAgentID{IDMia, IDJim, IDPlanner, IDResearcher, IDWorker}
	for _, id := range roles {
		got := ADR090RolePolicyInventory(id)[officePreviewToolName]
		if got != config.ToolPolicyAllow {
			t.Fatalf("BLOCKED: ADR090RolePolicyInventory(%q)[%q] = %q, want allow — required by spec AD-13 (T-20, B-29)", id, officePreviewToolName, got)
		}
	}
}

// TestOfficePreview_RoleInventoryOmnipusDeny (AD-13): Omnipus is explicit
// Deny — it never previews documents.
func TestOfficePreview_RoleInventoryOmnipusDeny(t *testing.T) {
	got := ADR090RolePolicyInventory(IDOmnipus)[officePreviewToolName]
	if got != config.ToolPolicyDeny {
		t.Fatalf("BLOCKED: ADR090RolePolicyInventory(omnipus)[%q] = %q, want deny — required by spec AD-13 (T-20, B-29)", officePreviewToolName, got)
	}
}

// TestOfficePreview_HiddenSystemAgentsDeny (AD-13): Judge and Plan
// Supervisor keep Deny (their denyAllThenOverride seeds never grant it).
func TestOfficePreview_HiddenSystemAgentsDeny(t *testing.T) {
	for _, id := range []CoreAgentID{IDJudge, IDPlanSupervisor} {
		got := systemAgentSeed(id)[officePreviewToolName]
		if got != config.ToolPolicyDeny {
			t.Fatalf("BLOCKED: systemAgentSeed(%q)[%q] = %q, want deny — required by spec AD-13 (T-20, B-29)", id, officePreviewToolName, got)
		}
	}
}

// TestOfficePreview_CustomAgentToolsCfgAllow (AD-13): custom agents (main
// and, after the worker-tier restriction, worker) hold allow.
func TestOfficePreview_CustomAgentToolsCfgAllow(t *testing.T) {
	cfg := NewCustomAgentToolsCfg()
	got := cfg.Builtin.Policies[officePreviewToolName]
	if got != config.ToolPolicyAllow {
		t.Fatalf("BLOCKED: NewCustomAgentToolsCfg()[%q] = %q, want allow — required by spec AD-13 (T-20)", officePreviewToolName, got)
	}

	// The worker tier must keep the grant: previewing documents is exactly
	// the General Purpose / native-worker document workflow.
	workerCore := NewCustomAgentToolsCfg()
	RestrictForWorkerTier(workerCore)
	if got := workerCore.Builtin.Policies[officePreviewToolName]; got != config.ToolPolicyAllow {
		t.Fatalf("BLOCKED: worker-tier custom agents lose preview_document (%q), want allow — spec AD-13 grants main and worker (T-20)", got)
	}
}

// TestOfficePreview_SeededSkills_ElicifyOfficeOnly (AD-14, FR-023, B-30):
// fresh-install skill seeds carry the merged office skill, never the four
// old per-format skills.
func TestOfficePreview_SeededSkills_ElicifyOfficeOnly(t *testing.T) {
	oldSkills := []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"}
	for _, id := range []CoreAgentID{IDMia, IDWorker} {
		skills := coreAgentSkills(id)
		joined := make(map[string]bool, len(skills))
		for _, s := range skills {
			joined[s] = true
		}
		if !joined["elicify-office"] {
			t.Fatalf("BLOCKED: coreAgentSkills(%q) lacks \"elicify-office\" — required by spec AD-14 (T-27, B-30); got %v", id, skills)
		}
		for _, old := range oldSkills {
			if joined[old] {
				t.Fatalf("coreAgentSkills(%q) still seeds retired skill %q — FR-023 deletes the four old skills, no aliases (T-27)", id, old)
			}
		}
	}
}
