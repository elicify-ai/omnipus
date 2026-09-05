// Omnipus — ADR-074 D4 define-done directive test (judgment-first spec
// test 5, FR-009): create_task_in_workspace's Description() directs the
// calling agent to load the define-done skill before authoring criteria.
// The general/core-scope tools (create_task / create_plan / plan_correct)
// carry the same assertion in pkg/tools.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"strings"
	"testing"
)

func TestCreateTaskInWorkspace_DescriptionCarriesDefineDoneDirective(t *testing.T) {
	desc := (&TaskCreateTool{}).Description()
	if !strings.Contains(desc, "define-done") {
		t.Error("create_task_in_workspace: Description() must name the define-done skill (FR-009)")
	}
	if !strings.Contains(desc, "authoring") || !strings.Contains(desc, "Skill tool") {
		t.Errorf("create_task_in_workspace: Description() must direct loading define-done via the Skill tool before authoring criteria; got %q", desc)
	}
}
