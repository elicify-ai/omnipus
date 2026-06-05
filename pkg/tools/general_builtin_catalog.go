// Omnipus — General Builtin Metadata Catalog
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — GeneralBuiltinMetadata returns metadata-only (never executed)
// instances of every general builtin tool so they can be registered in the central
// BuiltinRegistry for /api/v1/tools exposure.
//
// Design invariants (binding — from Issue #350, ADR-018 D-A1):
//   - Instances returned here are NEVER Execute()d. The per-agent registry
//     (registerSharedTools / wireExecToolDeps in pkg/agent/loop.go) remains the
//     sole execution source, with workspace-specific, security-wired instances.
//   - Constructors are called with a dummy workspace (""), restrict=false, and
//     nil deps. This is safe because only Name(), Description(), and Category()
//     are ever called on these instances.
//   - Constructor errors are logged and skipped (never fatal). A tool whose
//     constructor fails is simply absent from the metadata catalog.
//   - The workspace_shell and workspace.shell_bg tools are omitted: they exist
//     only when experimental.workspace_shell_enabled=true (config-gated), and
//     the central catalog reflects the fixed set of tools that are always present.

package tools

import (
	"log/slog"
)

// GeneralBuiltinMetadata constructs metadata-only instances of every general
// builtin tool and returns them as a slice. Each instance exposes correct
// Name(), Description(), and Category() values for the central BuiltinRegistry.
//
// The returned tools MUST NOT be Execute()d. All instances are constructed with
// a dummy workspace, restrict=false, and nil deps — safe for metadata only.
//
// Constructor errors are logged at Warn level and the corresponding tool is
// skipped. The caller must not treat a shorter-than-expected slice as fatal.
func GeneralBuiltinMetadata() []Tool {
	out := make([]Tool, 0, 28)

	// --- exec (CategoryCode, ScopeCore) ---
	execTool, err := NewExecToolWithConfig("", false, nil)
	if err != nil {
		slog.Warn("general-builtin-catalog: exec constructor failed; skipping", "error", err)
	} else {
		out = append(out, execTool)
	}

	// --- File system tools (CategoryFile) ---
	out = append(out, NewReadFileTool("", false, 0))
	out = append(out, NewWriteFileTool("", false))
	out = append(out, NewListDirTool("", false))
	out = append(out, NewEditFileTool("", false))
	out = append(out, NewAppendFileTool("", false))

	// --- Web tools (CategorySearch / CategoryWeb) ---
	// web_search: use DuckDuckGoEnabled to satisfy the at-least-one-provider
	// requirement without any API key. The DuckDuckGo provider only needs an
	// HTTP client (no keys), so it succeeds unconditionally in metadata mode.
	// The returned instance is NEVER Execute()d; it exists only to expose
	// Name/Description/Category on the central registry.
	webSearch, wsErr := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true})
	if wsErr != nil {
		slog.Warn("general-builtin-catalog: web_search constructor failed; skipping", "error", wsErr)
	} else {
		out = append(out, webSearch)
	}

	// web_fetch can be constructed with empty args (defaults to DuckDuckGo HTTP client).
	webFetch, wfErr := NewWebFetchTool(0, "", 0)
	if wfErr != nil {
		slog.Warn("general-builtin-catalog: web_fetch constructor failed; skipping", "error", wfErr)
	} else {
		out = append(out, webFetch)
	}

	// --- Communication / delegation tools (CategoryCore) ---
	out = append(out, NewMessageTool())
	out = append(out, NewHandoffTool(nil, nil, nil, nil))
	out = append(out, NewReturnToDefaultTool(nil, nil, nil))
	out = append(out, NewSendFileTool("", false, 0, nil))

	// --- Skill tools (CategorySkills / CategoryCore) ---
	out = append(out, NewFindSkillsTool(nil, nil))
	out = append(out, NewInstallSkillTool(nil, ""))

	// --- Spawn / subagent tools (CategoryCore) ---
	out = append(out, NewSpawnTool(nil))
	out = append(out, NewSubagentTool(nil))
	out = append(out, NewSpawnStatusTool(nil))

	// --- Task tools (CategoryTask) ---
	out = append(out, NewTaskListTool(nil))
	out = append(out, NewTaskCreateTool(nil))
	out = append(out, NewTaskUpdateTool(nil))
	out = append(out, NewTaskDeleteTool(nil))
	out = append(out, NewAgentListTool(nil))

	// --- Memory tools (CategoryCore) ---
	out = append(out, NewRememberTool(nil, nil))
	out = append(out, NewRecallMemoryTool(nil))
	out = append(out, NewRetrospectiveTool(nil, nil))

	// --- web_serve (CategoryCore — Tier 1 static + Tier 3 dev server) ---
	// Constructed with nil ServedSubdirs (metadata only; never executed).
	out = append(out, NewWebServeTool("", "", "", nil, nil, WebServeDevConfig{}, nil, nil, 0, 0))

	return out
}
