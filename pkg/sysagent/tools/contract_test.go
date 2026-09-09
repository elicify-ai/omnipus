// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestRegistry_AllSysagentToolsCategory verifies that every tool returned by
// AllTools() returns a domain category (NOT CategorySystem) after the §7 tool
// rename and follows the verb-first naming convention.
//
// Rationale: access control is now the per-agent tool policy (allow/ask/deny)
// plus destructive-op confirmation — not a role-based fence. The role-based
// admin-ask fence is retired (there is no admin role).
//
// BDD: Given all 33 tools returned by AllTools(),
//
//	When Category() is called on each,
//	Then it returns a domain category (NOT CategorySystem) for every tool (FR-059).
//	When Name() is called on each,
//	Then the name does NOT start with "system." and does NOT contain a dot.
//
// Traces to: pkg/sysagent/tools/category.go — Category (FR-059).
func TestRegistry_AllSysagentToolsCategory(t *testing.T) {
	all := AllTools(nil)

	if len(all) != 33 {
		t.Errorf("expected exactly 33 system tools, got %d", len(all))
	}

	for _, tool := range all {
		name := tool.Name()

		// Category contract (FR-059): after §7 rename, system tools must NOT return
		// CategorySystem. They must return a domain category.
		if cat, ok := tool.(interface{ Category() tools.ToolCategory }); ok {
			if cat.Category() == tools.CategorySystem {
				t.Errorf("tool %q: Category() must NOT return CategorySystem after §7 rename (FR-059)", name)
			}
			if cat.Category() == tools.CategoryCore {
				t.Errorf(
					"tool %q: Category() must NOT return CategoryCore (legacy default) — use a domain category",
					name,
				)
			}
		} else {
			t.Errorf("tool %q: does not implement Category()", name)
		}

		// Scope contract (FR-045): system tools use ScopeCore (ScopeSystem retired).
		if tool.Scope() != tools.ScopeCore {
			t.Errorf(
				"tool %q: Scope() must return ScopeCore (ScopeSystem is retired per FR-045), got %q",
				name,
				tool.Scope(),
			)
		}

		// Naming convention (§7): tool names must not start with "system." and must not
		// contain a dot (new verb-first naming convention).
		if strings.HasPrefix(name, "system.") {
			t.Errorf("tool %q: name must NOT start with \"system.\" prefix after §7 rename", name)
		}
		if strings.Contains(name, ".") {
			t.Errorf("tool %q: name must NOT contain a dot after §7 rename (use underscores)", name)
		}
	}
}

// TestRegistry_AllSysagentToolsHaveNonEmptyDescription verifies that every
// system tool has a non-empty Description() — required for LLM tool selection.
//
// BDD: Given all system tools,
//
//	When Description() is called on each,
//	Then none returns an empty string.
//
// Traces to: pkg/tools/base.go — Tool interface (FR-059 completeness).
func TestRegistry_AllSysagentToolsHaveNonEmptyDescription(t *testing.T) {
	for _, tool := range AllTools(nil) {
		if tool.Description() == "" {
			t.Errorf("tool %q has empty Description()", tool.Name())
		}
	}
}

// TestRegistry_NoDuplicateSysagentToolNames verifies that AllTools() contains
// no duplicate tool names.
//
// BDD: Given all system tools,
//
//	When their names are collected,
//	Then no name appears more than once.
//
// Traces to: pkg/sysagent/tools/registry.go — AllTools.
func TestRegistry_NoDuplicateSysagentToolNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, tool := range AllTools(nil) {
		name := tool.Name()
		if seen[name] {
			t.Errorf("duplicate tool name in AllTools(): %q", name)
		}
		seen[name] = true
	}
}

// TestRegistry_AllSysagentToolsCategory_CentralRegistry is the M4-spec
// variant: it populates the central BuiltinRegistry the same way production does
// (BuildRegistry) and asserts every registered builtin returns a domain
// Category().
//
// BDD: Given a BuiltinRegistry populated via BuildRegistry,
//
//	When each tool is retrieved via All(),
//	Then every tool has Category() != CategorySystem (FR-059).
//
// Traces to: pkg/tools/builtin_registry.go (central registry, FR-001).
// Traces to: pkg/sysagent/tools/registry.go — BuildRegistry.
func TestRegistry_AllSysagentToolsCategory_CentralRegistry(t *testing.T) {
	// Instantiate the registry exactly as production does at boot.
	reg := BuildRegistry(nil)
	allTools := reg.GetAll()

	if len(allTools) != 33 {
		t.Errorf("central BuiltinRegistry has %d tools; want == 33 (FR-001)", len(allTools))
	}

	for _, tool := range allTools {
		name := tool.Name()

		// Category must NOT be CategorySystem after §7 rename (FR-059).
		if cat, ok := tool.(interface{ Category() tools.ToolCategory }); ok {
			if cat.Category() == tools.CategorySystem {
				t.Errorf(
					"central registry tool %q: Category() must NOT return CategorySystem after §7 rename (FR-059)",
					name,
				)
			}
		} else {
			t.Errorf("central registry tool %q: does not implement Category()", name)
		}
	}
}
