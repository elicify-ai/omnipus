// Omnipus — ADR-081 D11 (unified-search-and-grep-spec.md FR-008/FR-009):
// the "grep" agent tool's gateway-side METADATA wiring — the catalog entry
// GET /api/v1/tools serves, independent of the tool's own implementation
// package landing (a sibling work track, not yet merged at the time this
// file was written).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE HOLDS A SELF-CONTAINED STUB RATHER THAN CONSTRUCTING A REAL
// grep.Tool, AND WHY THAT IS THE CORRECT CHOICE HERE (not a shortcut)
//
// knowledge_tools_wire.go's own metadata instances (knowledgeBuiltinMetadata)
// construct the REAL tool types from pkg/knowledge / pkg/vaultprops with
// zero-value deps — safe because those packages, and those types, already
// exist; only their Execute() path is inert on an empty scope. This file
// cannot do the same: the grep tool's own implementation package is landing
// on a SIBLING work track (a different agent, in parallel) and does not
// exist in this tree yet. Importing a not-yet-existing type here would make
// this file — and the governance tests it exists to satisfy (ADR-071
// tiering, ADR-077 policy coverage, MV-7's `GET /api/v1/tools` requirement)
// — unable to compile standalone, which defeats the entire point of
// separating "the tool exists and is governed" (this file, seedable and
// testable today) from "the tool actually greps a filesystem" (the sibling
// track, merged independently, on its own schedule).
//
// TWO PATTERNS WERE AVAILABLE, AND THIS FILE PICKS THE FIRST:
//
//  1. A metadata-only stub that implements tools.Tool directly, with no
//     import of any sibling-track package. This is what grepMetadataTool
//     below does. It satisfies MV-7's requirement that grep actually appear
//     in the GET /api/v1/tools catalog (rest_tool_registry.go's
//     HandleToolsRegistry reads ONLY Name()/Description()/Scope()/Category()
//     off a builtin-registry entry — never Parameters(), never Execute() —
//     see toolCategoryFromTool and addTool in rest_tool_registry.go), while
//     compiling independently of whether or when the real tool lands.
//  2. The ADR-052/ADR-067 "names-without-instances" pattern: union the bare
//     string "grep" into gateway.go's buildKnownBuiltinToolNames (done — see
//     that function's ADR-081 D11 block) WITHOUT any metadata-catalog
//     registration at all. That closes the Constraint #6 tool-policy
//     COVERAGE universe (the thing buildKnownBuiltinToolNames actually
//     governs) but does NOT make grep appear in GET /api/v1/tools, because
//     that endpoint reads the BuiltinRegistry (central_builtin_registry.go),
//     a completely different data source buildKnownBuiltinToolNames never
//     feeds. Pattern 2 alone would leave MV-7 unmet.
//
// CHOSEN: pattern 1 (this file), for a mirror reason of both a) MV-7's
// explicit requirement and b) the ADR-052/ADR-067 precedent's own known
// limitation — grep's spec explicitly asks for the tool to be visible in the
// catalog UI, which requires a real BuiltinRegistry entry, not just a name in
// the coverage-only union.
//
// A CONSEQUENCE TO FLAG, NOT HIDE: grepBuiltinMetadata below is not yet
// CALLED from anywhere. knowledge_tools_wire.go's own registerKnowledgeBuiltinMetadata
// is invoked from central_builtin_registry.go's buildCentralBuiltinRegistry
// (register("knowledge", knowledgeBuiltinMetadata(), &counts.knowledge)) —
// the ONE place the process-wide builtin metadata catalog is assembled, per
// that file's own header ("boot builds this registry TWICE... one function
// means the two sites cannot disagree again"). Wiring
// registerGrepBuiltinMetadata into that same function needs exactly one more
// line there (mirroring the knowledge line above), which this file
// deliberately does NOT add: central_builtin_registry.go is outside this
// change's file scope. Until that one line lands, grepBuiltinMetadata is
// dead code — present, compiling, correctly written, and NOT yet reachable
// from GET /api/v1/tools. This is recorded here rather than silently landed
// as if MV-7 were already fully satisfied end-to-end.
//
// WHEN THE REAL grep TOOL LANDS from its own implementation package: this
// file's grepMetadataTool stub should be REPLACED (not kept alongside) with
// a real-type metadata instance, exactly mirroring knowledgeBuiltinMetadata's
// pattern (construct the real type with zero-value/empty deps so an
// accidental Execute() call addresses nothing). Keeping both registered
// under the same "grep" name would silently drop one at
// registry.RegisterBuiltin's log-and-skip-duplicate step, which is the same
// silent-catalog-drift failure mode this whole file's header warns about.
// ---------------------------------------------------------------------------

// grepMetadataTool is a metadata-only tools.Tool implementation for "grep"
// (ADR-081 FR-008/FR-019): NEVER Execute()d in production (see the header
// above and ADR-018 D-A1) — the central BuiltinRegistry this file feeds is
// the METADATA catalog behind GET /api/v1/tools, not an execution registry.
// A per-agent execution registry (the thing a turn actually dispatches
// through) is wired by the sibling track that lands the real implementation,
// under pkg/agent's per-agent tool registration, exactly as
// knowledge_tools_wire.go's header describes for the knowledge family.
type grepMetadataTool struct {
	tools.BaseTool
}

// Name is the registered tool name — the same literal
// pkg/coreagent/core.go's allStaticToolNames and pkg/config/defaults.go's
// global ceiling both carry.
func (t *grepMetadataTool) Name() string { return "grep" }

// Description is what the model (and the Settings tool-policy screen) reads
// for this catalog entry — grounded in unified-search-and-grep-spec.md
// FR-008/FR-019/US-3, kept in sync by hand with that spec until the real
// implementation supplies its own tuned copy (mirroring how
// pkg/records/knowledgefind.Description is the single source of truth for
// knowledge_find, reused rather than restated by its own tool adapter).
func (t *grepMetadataTool) Description() string {
	return "Recursively search file names and text content across your own workspace root and " +
		"mounted folders. Literal or full RE2 regex matching, smart/sensitive/insensitive case " +
		"modes, glob include/exclude filters, and per-hit context lines. Every result is bounded " +
		"(files scanned, bytes read, matches returned, wall-clock deadline) with an explicit " +
		"truncation reason when a bound is hit — never a silent partial result. Confined to the " +
		"calling agent's own workspace and mounts only; no cross-workspace scope exists."
}

// Parameters mirrors FR-008/FR-019's accepted argument surface (pattern,
// regex flag, case mode, scope path, glob include/exclude, context lines,
// per-file/total match caps) so the catalog entry's schema is representative
// even though this instance is never Execute()d. The real implementation's
// own tools.Tool.Parameters() is the actual source of truth once it lands
// (see the header's replacement note above); this is not a wire contract —
// no generated type depends on it — so it is a plain Go literal here rather
// than a contracts/ schema.
func (t *grepMetadataTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Text or regex pattern to search for.",
			},
			"regex": map[string]any{
				"type":        "boolean",
				"description": "Treat pattern as a full RE2 regular expression instead of a literal string.",
			},
			"case": map[string]any{
				"type":        "string",
				"enum":        []string{"smart", "sensitive", "insensitive"},
				"description": "Case-matching mode; default \"smart\" (insensitive unless pattern has an uppercase letter).",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Workspace-relative folder to search; defaults to the workspace root.",
			},
			"include_glob": map[string]any{
				"type":        "string",
				"description": "Only search files whose path matches this glob.",
			},
			"exclude_glob": map[string]any{
				"type":        "string",
				"description": "Skip files whose path matches this glob.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"description": "Lines of context before/after each match (0-5).",
			},
			"include_hidden": map[string]any{
				"type":        "boolean",
				"description": "Include dotfiles/dot-directories; .git, .library and .omnipus-vault are always excluded regardless.",
			},
		},
		"required": []string{"pattern"},
	}
}

// Scope classifies the tool for per-agent visibility filtering. ScopeGeneral
// — matching read_file/list_directory/knowledge_find — because ADR-081's
// founder ruling (FR-009) makes grep available to every agent tier, not a
// core-only capability.
func (t *grepMetadataTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI alongside the other filesystem
// read tools (read_file, list_directory) it most resembles.
func (t *grepMetadataTool) Category() tools.ToolCategory { return tools.CategoryFilesystem }

// Execute must never run in production: this instance is registered ONLY
// into the metadata-only central BuiltinRegistry (see the header above and
// ADR-018 D-A1), and that registry's sole consumer, HandleToolsRegistry
// (rest_tool_registry.go), reads Name()/Description()/Scope()/Category() and
// nothing else. If this is ever reached anyway — a future caller mistakenly
// treating the metadata catalog as an execution registry — it fails loudly
// and honestly rather than silently returning fabricated search results or
// panicking: this instance has no filesystem access and cannot grep
// anything, and says so.
func (t *grepMetadataTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.ErrorResult(
		"grep: this is the process-wide capability-catalog entry (GET /api/v1/tools), not an " +
			"executable instance — it carries no workspace and cannot search anything. The real, " +
			"callable grep tool is registered per-agent by the agent loop's own tool registry; " +
			"reaching this Execute() method at all is a caller bug, not a tool-policy denial.")
}

// grepBuiltinMetadata returns the metadata-only "grep" catalog entry. See
// this file's header for why it is a self-contained stub rather than the
// real implementation type, and for the one wiring line
// (central_builtin_registry.go) still needed to make it reachable from
// GET /api/v1/tools.
func grepBuiltinMetadata() []tools.Tool {
	return []tools.Tool{&grepMetadataTool{}}
}

// registerGrepBuiltinMetadata adds the grep tool to a builtin registry,
// mirroring registerKnowledgeBuiltinMetadata's log-and-skip-on-duplicate
// contract: a duplicate name is a registration-order problem, never a reason
// to fail boot, and a nil registry is a no-op so a caller in a
// partially-wired test cannot panic here.
func registerGrepBuiltinMetadata(reg *tools.BuiltinRegistry) {
	if reg == nil {
		return
	}
	for _, t := range grepBuiltinMetadata() {
		if err := reg.RegisterBuiltin(t); err != nil {
			slog.Warn("gateway: central builtin registry grep-builtin skipped",
				"tool", t.Name(), "error", err)
		}
	}
}
