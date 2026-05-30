// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestRegistry_AllSysagentToolsRequireAdminAsk verifies that every tool returned
// by AllTools() implements RequiresAdminAsk() == true and Category() == CategorySystem.
// This is the admin-ask fence (FR-061) and category-contract (FR-059).
//
// Rationale: all system.* tools are privileged operations (creating agents, editing
// config, managing channels). They must always require human approval before
// execution — RequiresAdminAsk() == true is the machine-readable gate.
//
// BDD: Given all 35+ tools returned by AllTools(),
//
//	When RequiresAdminAsk() is called on each,
//	Then it returns true for every tool.
//	When Category() is called on each,
//	Then it returns CategorySystem for every tool (FR-059).
//
// Traces to: pkg/sysagent/tools/admin_ask.go — RequiresAdminAsk (FR-061).
// Traces to: pkg/sysagent/tools/category.go — Category (FR-059).
func TestRegistry_AllSysagentToolsRequireAdminAsk(t *testing.T) {
	all := AllTools(nil, nil)

	if len(all) < 35 {
		t.Errorf("expected at least 35 system tools, got %d", len(all))
	}

	for _, tool := range all {
		name := tool.Name()

		// RequiresAdminAsk contract (FR-061).
		if adm, ok := tool.(interface{ RequiresAdminAsk() bool }); ok {
			if !adm.RequiresAdminAsk() {
				t.Errorf("tool %q: RequiresAdminAsk() must return true (FR-061 admin-ask fence)", name)
			}
		} else {
			t.Errorf(
				"tool %q: does not implement RequiresAdminAsk() — must embed BaseTool or implement it directly",
				name,
			)
		}

		// Category contract (FR-059): system tools use CategorySystem.
		if cat, ok := tool.(interface{ Category() tools.ToolCategory }); ok {
			if cat.Category() != tools.CategorySystem {
				t.Errorf("tool %q: Category() must return CategorySystem, got %q (FR-059)", name, cat.Category())
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

		// Naming convention: all system tools must use the "system." prefix.
		if !strings.HasPrefix(name, "system.") {
			t.Errorf("tool %q: name must start with \"system.\" prefix (naming convention)", name)
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
	for _, tool := range AllTools(nil, nil) {
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
	for _, tool := range AllTools(nil, nil) {
		name := tool.Name()
		if seen[name] {
			t.Errorf("duplicate tool name in AllTools(): %q", name)
		}
		seen[name] = true
	}
}

// TestRegistry_AllSysagentToolsRequireAdminAsk_CentralRegistry is the M4-spec
// variant of TestRegistry_AllSysagentToolsRequireAdminAsk: it populates the
// central BuiltinRegistry the same way production does (BuildRegistry) and
// asserts every registered builtin satisfies RequiresAdminAsk() and Category().
//
// This test walks the central registry rather than the package-local AllTools
// slice, ensuring that any future tool added via BuiltinRegistry.RegisterBuiltin
// (not AllTools) is also covered.
//
// BDD: Given a BuiltinRegistry populated via BuildRegistry,
//
//	When each tool is retrieved via All(),
//	Then every system.* tool has RequiresAdminAsk() == true (FR-061)
//	And every system.* tool has Category() == CategorySystem (FR-059).
//
// Traces to: pkg/tools/builtin_registry.go (central registry, FR-001).
// Traces to: pkg/sysagent/tools/registry.go — BuildRegistry.
func TestRegistry_AllSysagentToolsRequireAdminAsk_CentralRegistry(t *testing.T) {
	// Instantiate the registry exactly as production does at boot.
	reg := BuildRegistry(nil, nil)
	allTools := reg.GetAll()

	if len(allTools) < 35 {
		t.Errorf("central BuiltinRegistry has %d tools; want ≥35 (FR-001)", len(allTools))
	}

	for _, tool := range allTools {
		name := tool.Name()

		// All tools in this registry are system.* builtins — they must all
		// require admin-ask (FR-061).
		if adm, ok := tool.(interface{ RequiresAdminAsk() bool }); ok {
			if !adm.RequiresAdminAsk() {
				t.Errorf("central registry tool %q: RequiresAdminAsk() must be true (FR-061)", name)
			}
		} else {
			t.Errorf("central registry tool %q: does not implement RequiresAdminAsk()", name)
		}

		// Category must be CategorySystem (FR-059).
		if cat, ok := tool.(interface{ Category() tools.ToolCategory }); ok {
			if cat.Category() != tools.CategorySystem {
				t.Errorf(
					"central registry tool %q: Category() must be CategorySystem, got %q (FR-059)",
					name,
					cat.Category(),
				)
			}
		} else {
			t.Errorf("central registry tool %q: does not implement Category()", name)
		}
	}
}
