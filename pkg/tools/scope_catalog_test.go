// Omnipus — Builtin tool scope catalog coverage.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools_test (external test package — see the import cycle note
// below) guards the invariant the tool-permission scope gate
// (pkg/tools/compositor.go's passesScopeGate) depends on: every SHIPPED
// builtin tool must return a known, non-zero ToolScope.
//
// Before this file, every real tool's Scope() method was at 0% direct
// coverage in this package — only email_test.go:306 and load_tool_test.go:282
// happened to touch a real tool's Scope() indirectly, and neither is a
// dedicated scope assertion. Nothing verified that a shipped tool declares
// the scope the design says it should. A tool with a mistyped, unset, or
// new-and-unwired scope constant is invisible to that gap until it ships —
// and once it ships, compositor_test.go's
// TestFilterToolsByPolicy_UnknownScope_DeniedEvenWithMatchingAllowPolicy
// shows exactly what protects it: the scope gate denies it regardless of any
// operator wildcard (e.g. "browser_*: allow") that happens to match its
// name. This file is the other half — catching the mistake before it ships,
// not just containing it after.
//
// Package choice: this file is `tools_test` (an external test package), not
// `tools`, so it can import pkg/sysagent/tools (which itself imports
// pkg/tools) without an import cycle. pkg/tools/provider_defs_shape_test.go
// already establishes this exact pattern in this directory.
//
// Expected values below are NOT read off any tool's Scope() return
// statement. They are derived from textual sources that predate and do not
// depend on any individual tool's implementation:
//
//  1. pkg/tools/base.go's ToolScope doc comment, which names concrete tool
//     examples for each of the two scopes: "exec, browser.*, write_file,
//     edit_file, spawn, subagent, and all system.* tools" → ScopeCore;
//     "read_file, list_directory, search_web, fetch_url, message,
//     create_task, list_tasks" → ScopeGeneral. ADR-036 renamed exec→bash and
//     merged spawn/subagent→delegate; the mapping below follows those
//     documented renames (grep-verified against each tool's Name() method,
//     not assumed) — see explicitGeneralScopeExamples.
//  2. The same doc comment's blanket statement that "all system.* tools ...
//     return this [ScopeCore] scope via Scope()" — an explicit, catalog-wide
//     invariant for pkg/sysagent/tools, independent of any single tool's
//     source. See TestSysagentCatalog_AllToolsAreScopeCore.
//  3. pkg/sysagent/tools/registry.go's own doc comment on BuildRegistry:
//     "creates a ToolRegistry containing all 35 system tools" — an
//     independently-stated count used as the catalog-membership sanity
//     check in TestSysagentCatalog_AllToolsAreScopeCore, not derived by
//     counting AllTools' literal entries.
//
// Known gap (declared, not silent): the ~30 general builtin tools NOT named
// in base.go's example list have no independent textual source in this
// repository for their individual expected scope. For those,
// TestGeneralBuiltinMetadata_AllToolsHaveKnownScope asserts only the weaker
// — but still real — invariant ToolScope's own type definition guarantees:
// the value must be one of the two declared constants. A tool returning ""
// or a stray new value fails that check regardless of which tool it is.
package tools_test

import (
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// knownScopes is the complete, closed set of valid ToolScope values per
// pkg/tools/base.go's type definition — exactly two constants are declared
// there, no more.
var knownScopes = map[tools.ToolScope]bool{
	tools.ScopeCore:    true,
	tools.ScopeGeneral: true,
}

// explicitGeneralScopeExamples is the per-tool table derived from base.go's
// ToolScope doc comment (see file header for the independent-source
// rationale). Keyed by Name(), not by Go type, so a rename that forgets to
// update this table fails loudly (missing key below) rather than silently
// no-oping.
var explicitGeneralScopeExamples = map[string]tools.ToolScope{
	// ScopeCore examples named in base.go (post-ADR-036 renames).
	"bash":       tools.ScopeCore, // base.go names "exec"; ADR-036 renamed exec -> bash
	"write_file": tools.ScopeCore,
	"edit_file":  tools.ScopeCore,
	"delegate":   tools.ScopeCore, // base.go names "spawn, subagent"; ADR-036 merged both into delegate

	// ScopeGeneral examples named in base.go.
	"read_file":      tools.ScopeGeneral,
	"list_directory": tools.ScopeGeneral,
	"search_web":     tools.ScopeGeneral,
	"fetch_url":      tools.ScopeGeneral,
	"send_message":   tools.ScopeGeneral, // base.go names "message"; the real tool's Name() is send_message
	"create_task":    tools.ScopeGeneral,
	"list_tasks":     tools.ScopeGeneral,
}

// TestGeneralBuiltinMetadata_ExplicitlyNamedTools_MatchBaseGoDocExamples pins
// the per-tool expected scope for every tool base.go's doc comment names by
// example. A mismatch here means either the doc comment is stale or the
// tool's Scope() changed without the design source being updated — either
// way a real finding, not a false positive.
//
// Traces to: pkg/tools/base.go ToolScope doc comment (ScopeCore/ScopeGeneral
// const block).
func TestGeneralBuiltinMetadata_ExplicitlyNamedTools_MatchBaseGoDocExamples(t *testing.T) {
	byName := make(map[string]tools.Tool)
	for _, tl := range tools.GeneralBuiltinMetadata() {
		byName[tl.Name()] = tl
	}

	checked := 0
	for name, want := range explicitGeneralScopeExamples {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("expected tool %q (named in base.go's ToolScope doc comment) to be present in GeneralBuiltinMetadata(), but it was not found", name)
			continue
		}
		checked++
		if got := tl.Scope(); got != want {
			t.Errorf("tool %q: expected scope %q per base.go's ToolScope doc comment, got %q", name, want, got)
		}
	}
	if checked != len(explicitGeneralScopeExamples) {
		t.Fatalf("expected to check all %d explicitly-named tools, only found %d in the catalog — catalog membership changed", len(explicitGeneralScopeExamples), checked)
	}
}

// TestGeneralBuiltinMetadata_AllToolsHaveKnownScope verifies every tool the
// central registry catalog produces returns a scope from ToolScope's closed
// set of two declared constants. This is the minimum bar the scope gate
// depends on: passesScopeGate fail-closes (denies) any third value, so a
// tool shipped with "" or a typo'd literal must still be caught here, before
// it ships, not only contained by the gate after.
//
// The >=35 floor is a sanity check on catalog construction (not a scope
// assertion): GeneralBuiltinMetadata's own constructor-error-skips-tool
// design (see its doc comment) means a mass constructor failure could
// silently shrink the catalog to near-zero without any individual
// assertion below firing.
//
// Traces to: pkg/tools/base.go ToolScope type + const block (exactly two
// values are ever declared); pkg/tools/compositor.go passesScopeGate doc
// comment ("Unknown/zero-value scopes: denied (fail-closed...)").
func TestGeneralBuiltinMetadata_AllToolsHaveKnownScope(t *testing.T) {
	catalog := tools.GeneralBuiltinMetadata()
	if len(catalog) < 35 {
		t.Fatalf("expected GeneralBuiltinMetadata() to return at least 35 tools (sanity floor — constructor failures may be silently skipping tools), got %d", len(catalog))
	}

	for _, tl := range catalog {
		if !knownScopes[tl.Scope()] {
			t.Errorf("tool %q returns scope %q, which is not one of the declared ToolScope constants (ScopeCore/ScopeGeneral) — this tool is invisible to any policy wildcard and fails closed by the scope gate; if intentional, verify passesScopeGate handles it, and if not, fix Scope()", tl.Name(), tl.Scope())
		}
	}
}

// TestSysagentCatalog_AllToolsAreScopeCore pins the blanket invariant stated
// in base.go's doc comment: "and all system.* tools (which return this scope
// via Scope())" — every tool systools.AllTools produces must be ScopeCore,
// with no exceptions.
//
// nil Deps/NavigateCallback are safe here: only Name()/Scope() are called,
// never Execute() — see pkg/gateway/gateway.go's buildKnownBuiltinToolNames
// doc comment, which documents and relies on the identical safety property
// for the identical call (every sysagent constructor only stores the *Deps
// pointer it is given; Name()/Scope() are static and never dereference it).
//
// The exact-35 count comes from pkg/sysagent/tools/registry.go's own
// BuildRegistry doc comment ("creates a ToolRegistry containing all 35
// system tools"), not from counting AllTools' literal entries — an
// independent textual source, so a silent catalog-membership drift (a tool
// added/removed without updating that comment) is itself a finding.
//
// Traces to: pkg/tools/base.go ToolScope doc comment;
// pkg/sysagent/tools/registry.go BuildRegistry doc comment.
func TestSysagentCatalog_AllToolsAreScopeCore(t *testing.T) {
	catalog := systools.AllTools(nil, nil)
	if len(catalog) != 35 {
		t.Fatalf("expected systools.AllTools(nil, nil) to return exactly 35 tools (per registry.go's BuildRegistry doc comment: \"all 35 system tools\"), got %d — catalog membership drifted from its own documentation", len(catalog))
	}

	for _, tl := range catalog {
		if got := tl.Scope(); got != tools.ScopeCore {
			t.Errorf("system.* tool %q returns scope %q, expected ScopeCore per base.go's doc comment (\"all system.* tools ... return this scope\") — a non-core sysagent tool would defer to the global×agent merge instead of requiring an explicit core-agent-or-policy grant", tl.Name(), got)
		}
	}
}

// TestSysagentCatalog_ToolNamesAreUnique is a sanity check the other
// assertions in this file depend on: if two constructors silently collided
// on the same Name(), a by-name lookup would mask one of them.
func TestSysagentCatalog_ToolNamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, tl := range systools.AllTools(nil, nil) {
		if seen[tl.Name()] {
			t.Errorf("duplicate tool name %q in systools.AllTools(nil, nil) — catalog is not name-unique", tl.Name())
		}
		seen[tl.Name()] = true
	}
}
