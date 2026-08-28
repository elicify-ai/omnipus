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
	out := make([]Tool, 0, 42)

	// --- bash (CategoryShell, ScopeCore) — ADR-036 merge of
	// exec/workspace_shell/workspace_shell_bg into one universally-registered
	// tool. Name() returns "bash". ---
	execTool, err := NewExecToolWithConfig("", false, nil)
	if err != nil {
		slog.Warn("general-builtin-catalog: bash constructor failed; skipping", "error", err)
	} else {
		out = append(out, execTool)
	}

	// --- File system tools (CategoryFilesystem) ---
	out = append(out, NewReadFileTool("", false, 0))
	out = append(out, NewWriteFileTool("", false))
	out = append(out, NewListDirTool("", false))
	out = append(out, NewEditFileTool("", false))
	out = append(out, NewAppendFileTool("", false))
	// library_list / library_read (D3, library-spec): scoped facades over
	// the workspace's .library/ dual-write directory where chat uploads
	// land (D-1) — see pkg/agent.LibraryDirName.
	out = append(out, NewLibraryListTool("", false))
	out = append(out, NewLibraryReadTool("", false, 0))
	// request_mount (ADR-063 FR-7.2): the agent's way to ASK for write access
	// to a real folder. Catalogued with empty home/workspace because this list
	// is the static catalog used for policy enumeration and the tool picker;
	// the per-turn instance is built with a real workspace when registered.
	out = append(out, NewRequestMountTool(""))
	// list_mounts (ADR-068 §4): the read-only counterpart to request_mount —
	// which folders are mounted into this workspace right now. Catalogued for
	// the same reason request_mount is, and it MUST be here rather than only
	// in the per-agent registry: this catalog is what pkg/gateway's
	// buildKnownBuiltinToolNames walks to build the Constraint #6 coverage
	// universe, and a tool missing from it ships denied-by-default on every
	// install even though every agent registers it (see
	// recall_conversation_meta.go, which exists because that happened).
	out = append(out, NewListMountsTool(""))

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
	out = append(out, NewSwitchAgentTool(nil, nil, nil, nil, nil))
	out = append(out, NewSendFileTool("", false, 0, nil))

	// --- Skill tools (CategorySkills) ---
	out = append(out, NewFindSkillsTool(nil, nil))
	out = append(out, NewInstallSkillTool(nil, ""))

	// --- delegate tool (CategoryDelegation) — ADR-036 merge of the former
	// spawn / run_subagent / check_spawn_status trio into one tool. ---
	out = append(out, NewDelegateTool("", 0, 0))

	// --- message_parent tool (CategoryDelegation) — ADR-053 §5.1: the
	// child-side tool a delegated session uses to push a typed message into
	// its parent's durable inbox. Metadata-only instance (nil inbox/lifecycle
	// stores; never Execute()d here) so the central registry and the
	// Constraint #6 tool-policy-coverage universe (buildKnownBuiltinToolNames)
	// see it, mirroring delegate's registration exactly. ---
	out = append(out, NewMessageParentTool(nil, nil))

	// --- Task tools (CategoryTasks) ---
	out = append(out, NewTaskListTool(nil))
	out = append(out, NewTaskCreateTool(nil))
	out = append(out, NewTaskUpdateTool(nil))
	out = append(out, NewTaskDeleteTool(nil))
	out = append(out, NewAgentListTool(nil))

	// --- Plan tools (CategoryTasks) — ADR-052 autonomous agent plan
	// authoring & execution. Seeded allow for Jim (Orchestrator) only; every
	// other seeded agent and the global ceiling resolve explicit `ask`
	// (Constraint #6) — that policy resolution happens entirely outside this
	// metadata catalog, at the per-agent tool-dispatch gate. ---
	out = append(out, NewPlanCreateTool(nil))
	out = append(out, NewPlanExecuteTool(nil, nil))
	out = append(out, NewTaskRunTool(nil))
	// inspect_session: verifier-role-only (ceiling-deny for every ordinary
	// agent); listed here purely as a capability-reference entry.
	out = append(out, NewInspectSessionTool(nil))

	// --- ADR-055 (PlanSupervisor) supervision + containment (CategoryTasks) ---
	// plan_correct is PlanSupervisor's entire grant; stop_plan is the plan
	// OWNER's containment control (deliberately inverse authorities — the
	// adjudicator corrects, the owner contains).
	//
	// Listing them here is not cosmetic: pkg/gateway's
	// buildKnownBuiltinToolNames() derives the Constraint #6 tool-policy
	// COVERAGE UNIVERSE by walking this catalog, and both names are already in
	// pkg/coreagent's allStaticToolNames seed literal and in pkg/config's
	// global ceiling. A name present on those two surfaces but absent from the
	// live catalog is exactly the drift
	// TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog exists
	// to catch — and it leaves the tools invisible to GET /api/v1/tools.
	//
	// Nil stores and no engine hook: metadata only. Both tools FAIL CLOSED on
	// an unwired hook rather than reporting a correction/stop that never
	// happened, so a stray Execute() in this mode errors instead of lying.
	out = append(out, NewPlanCorrectTool(nil, nil))
	out = append(out, NewPlanStopTool(nil))

	// --- ADR-056 list_jobs (read-only background-job roster) ---
	// The one tool that answers "what background work of mine is outstanding,
	// and what is its handle?" across plans owned, subagents delegated and
	// standalone tasks. Strictly read-only and fail-closed on an unresolvable
	// caller identity, so the metadata instance below (nil plan/task/lifecycle
	// listers) can neither read nor leak anything.
	//
	// It is the DISCOVERY half of containment: stop_plan needs a plan id, and
	// this is where an agent gets one — which is why its global ceiling is
	// "allow" (pkg/config/defaults.go) rather than mirroring execute_plan.
	out = append(out, NewListJobsTool(nil, nil, nil))

	// --- Memory tools (CategoryMemory) ---
	out = append(out, NewRememberTool(nil, nil))
	out = append(out, NewRecallMemoryTool(nil))
	out = append(out, NewRetrospectiveTool(nil, nil))
	// recall_conversation's executable impl lives in pkg/agent (it needs per-turn
	// session context); this is its metadata-only mirror so the catalog + the
	// Constraint #6 coverage universe see it — see recall_conversation_meta.go.
	out = append(out, recallConversationMeta{})

	// --- serve_web (CategoryWeb — Tier 1 static + Tier 3 dev server) ---
	// Constructed with nil ServedSubdirs and nil getConfig (metadata only;
	// never executed) — preview-on-main-listener v5 replaced the constructor-
	// frozen preview base URL string with a live *config.Config accessor.
	out = append(out, NewWebServeTool("", "", nil, nil, nil, WebServeDevConfig{}, nil, nil, 0, 0))

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

	// Unified tool-discovery + load infra (CategoryToolDiscovery): the `ToolSearch`
	// infra tool is registered per-agent whenever compressed manifest mode is enabled
	// OR MCP discovery is enabled (ManifestInfra tier — never appears in the
	// manifest block; always callable when registered). The metadata instance
	// has no resolver wired; the loop wires SetResolver before any Execute call.
	out = append(out, NewToolsTool(nil, 0, 0))

	return out
}
