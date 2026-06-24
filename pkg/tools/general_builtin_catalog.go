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
//     only when experimental.workspace_shell_enabled=true (config-gated, experimental).
//
// Conditional tools (set_todos, email.*, tool_search_tool_*): these are
// included as metadata even though they only register per-agent under certain
// conditions (set_todos = core agents; email.* = agents that
// own a configured mailbox; tool_search_tool_* = when the MCP search cache is
// enabled). The global catalog is a CAPABILITY REFERENCE — "everything the
// platform can do" — while the per-agent view (GET /agents/{id}/tools, which
// reads the real runtime registry) remains the authority for what a given agent
// actually has. Listing a conditional tool here does not make it appear for an
// agent that hasn't met its condition; that gating is unchanged at the per-agent
// registration layer. Metadata instances are constructed with nil deps.

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
	out := make([]Tool, 0, 38)

	// --- exec (CategoryShell, ScopeCore) ---
	execTool, err := NewExecToolWithConfig("", false, nil)
	if err != nil {
		slog.Warn("general-builtin-catalog: exec constructor failed; skipping", "error", err)
	} else {
		out = append(out, execTool)
	}

	// --- File system tools (CategoryFilesystem) ---
	out = append(out, NewReadFileTool("", false, 0))
	out = append(out, NewWriteFileTool("", false))
	out = append(out, NewListDirTool("", false))
	out = append(out, NewEditFileTool("", false))
	out = append(out, NewAppendFileTool("", false))

	// --- Web tools (CategoryWeb) ---
	// search_web: use DuckDuckGoEnabled to satisfy the at-least-one-provider
	// requirement without any API key. The DuckDuckGo provider only needs an
	// HTTP client (no keys), so it succeeds unconditionally in metadata mode.
	// The returned instance is NEVER Execute()d; it exists only to expose
	// Name/Description/Category on the central registry.
	webSearch, wsErr := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true})
	if wsErr != nil {
		slog.Warn("general-builtin-catalog: search_web constructor failed; skipping", "error", wsErr)
	} else {
		out = append(out, webSearch)
	}

	// fetch_url can be constructed with empty args (defaults to DuckDuckGo HTTP client).
	webFetch, wfErr := NewWebFetchTool(0, "", 0)
	if wfErr != nil {
		slog.Warn("general-builtin-catalog: fetch_url constructor failed; skipping", "error", wfErr)
	} else {
		out = append(out, webFetch)
	}

	// --- Communication / delegation tools (CategoryCommunication / CategoryDelegation) ---
	out = append(out, NewMessageTool())
	out = append(out, NewHandoffTool(nil, nil, nil, nil))
	out = append(out, NewReturnToDefaultTool(nil, nil, nil))
	out = append(out, NewSendFileTool("", false, 0, nil))

	// --- Skill tools (CategorySkills) ---
	out = append(out, NewFindSkillsTool(nil, nil))
	out = append(out, NewInstallSkillTool(nil, ""))

	// --- Spawn / subagent tools (CategoryDelegation) ---
	out = append(out, NewSpawnTool(nil))
	out = append(out, NewSubagentTool(nil))
	out = append(out, NewSpawnStatusTool(nil))

	// --- Task tools (CategoryTasks) ---
	out = append(out, NewTaskListTool(nil))
	out = append(out, NewTaskCreateTool(nil))
	out = append(out, NewTaskUpdateTool(nil))
	out = append(out, NewTaskDeleteTool(nil))
	out = append(out, NewAgentListTool(nil))

	// --- Memory tools (CategoryMemory) ---
	out = append(out, NewRememberTool(nil, nil))
	out = append(out, NewRecallMemoryTool(nil))
	out = append(out, NewRetrospectiveTool(nil, nil))

	// --- serve_web (CategoryWeb — Tier 1 static + Tier 3 dev server) ---
	// Constructed with nil ServedSubdirs (metadata only; never executed).
	out = append(out, NewWebServeTool("", "", "", nil, nil, WebServeDevConfig{}, nil, nil, 0, 0))

	// --- Conditional tools (metadata-only; see package doc) ---
	// These register per-agent only under certain conditions, but are listed here
	// so the global catalog is a complete capability reference. Nil deps — never
	// executed; the per-agent registry supplies live instances.

	// set_todos (CategoryTasks): the agent-facing scratchpad facade, registered
	// alongside the task tools for core agents. Nil store — metadata only.
	out = append(out, NewSetTodosTool(nil))

	// Email tools (CategoryCommunication): registered only for agents that own a
	// configured mailbox (pkg/agent/email_tools.go). EmailToolset(nil) returns
	// all five; nil transport is safe (Execute guards tp==nil; Description static).
	out = append(out, EmailToolset(nil)...)

	// On-demand tool-discovery search (CategoryToolDiscovery): registered
	// only when the MCP search cache is enabled (pkg/agent/loop_mcp.go). Nil
	// registry — metadata only.
	out = append(out, NewRegexSearchTool(nil, 0, 0))
	out = append(out, NewBM25SearchTool(nil, 0, 0))

	return out
}
