// Omnipus — ADR-074 D4 define-goal directive test (judgment-first spec
// test 5, FR-009): create_task_in_workspace's Description() directs the
// calling agent to load the define-goal skill (renamed from define-done by
// ADR-080 D-SKILL) before authoring criteria. The general/core-scope tools
// (create_task / create_plan / plan_correct) carry the same assertion in
// pkg/tools.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"strings"
	"testing"
)

func TestCreateTaskInWorkspace_DescriptionCarriesDefineGoalDirective(t *testing.T) {
	desc := (&TaskCreateTool{}).Description()
	if !strings.Contains(desc, "define-goal") {
		t.Error("create_task_in_workspace: Description() must name the define-goal skill (FR-009)")
	}
	if !strings.Contains(desc, "authoring") || !strings.Contains(desc, "Skill tool") {
		t.Errorf("create_task_in_workspace: Description() must direct loading define-goal via the Skill tool before authoring criteria; got %q", desc)
	}
}
