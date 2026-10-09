// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U9 (task-family merge), sysagent catalog slice.
// Spec: docs/internal/specs/session-core-spec.md FR-046, BDD-08.10, DEL-23.
//
// FR-046: "Remove duplicate callables/active callers/catalog/default/role/prompt
// references." BDD-08.10: "Effective catalog has no old callables". DEL-23 names
// the four `*_in_workspace` tools (registered in registry.go::AllTools) as
// removal targets, replaced by the canonical create_task/update_task/
// delete_task/list_tasks with an optional workspace_id.
//
// Oracle: the registered catalog must contain NO name ending in `_in_workspace`.
// Today all four are registered, so this fails.

package systools_test

import (
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// DEL-23/FR-046/BDD-08.10: none of the four retired `*_in_workspace` callables
// may remain in the tool catalog.
func TestSessionCoreU9_NoInWorkspaceCallablesRemainInCatalog(t *testing.T) {
	all := systools.AllTools(nil)

	// Instrument: the enumeration must actually be non-empty, else the
	// absence assertion below would pass vacuously.
	if len(all) == 0 {
		t.Fatal("instrument: AllTools(nil) returned no tools — absence assertion would be vacuous")
	}

	for _, tool := range all {
		if name := tool.Name(); strings.HasSuffix(name, "_in_workspace") {
			t.Errorf("DEL-23/FR-046/BDD-08.10: retired callable %q must be removed from the catalog", name)
		}
	}
}

// DEL-23: the four retired names must not be individually grantable either.
// Checked through the same catalog the policy/default layers name.
func TestSessionCoreU9_RetiredInWorkspaceNamesAbsent(t *testing.T) {
	registered := make(map[string]bool)
	for _, tool := range systools.AllTools(nil) {
		registered[tool.Name()] = true
	}
	// Instrument: at least one non-retired tool must be registered, proving
	// the map is populated from a real catalog read.
	if !registered["create_agent"] {
		t.Fatalf("instrument: expected create_agent in the catalog; registered names = %v", keysOfRegistered(registered))
	}

	for _, retired := range []string{
		"create_task_in_workspace",
		"update_task_in_workspace",
		"delete_task_in_workspace",
		"list_tasks_in_workspace",
	} {
		if registered[retired] {
			t.Errorf("DEL-23: retired callable %q must not remain registered", retired)
		}
	}
}

func keysOfRegistered(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}