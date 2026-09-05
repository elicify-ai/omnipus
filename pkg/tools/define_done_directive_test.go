// Omnipus — ADR-074 D4 define-done directive tests (judgment-first spec
// test 5, FR-009): every criteria-authoring tool's Description() directs the
// calling agent to load the define-done skill before authoring criteria.
// Advisory-by-guidance — a hard gate is deliberately not built (ADR-074 D4),
// so the directive STRING is the whole mechanism and is asserted verbatim.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"strings"
	"testing"
)

// create_task_in_workspace (pkg/sysagent/tools) carries its own copy of this
// assertion since it is a separate package.
func TestCriteriaAuthoringTools_DescriptionsCarryDefineDoneDirective(t *testing.T) {
	for name, desc := range map[string]string{
		"create_task":  (&TaskCreateTool{}).Description(),
		"create_plan":  (&PlanCreateTool{}).Description(),
		"plan_correct": (&PlanCorrectTool{}).Description(),
	} {
		if !strings.Contains(desc, "define-done") {
			t.Errorf("%s: Description() must name the define-done skill (FR-009)", name)
		}
		if !strings.Contains(desc, "authoring") || !strings.Contains(desc, "Skill tool") {
			t.Errorf("%s: Description() must direct loading define-done via the Skill tool before authoring criteria; got %q", name, desc)
		}
	}
}
