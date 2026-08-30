// Omnipus — ADR-067 D7/D17: the knowledge-base tool family's gateway-side
// wiring (FR-050–FR-055, FR-070/FR-071).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// ---------------------------------------------------------------------------
// WHERE THE KNOWLEDGE TOOLS ARE REGISTERED, AND WHY IN TWO PLACES
//
// There are two registries, they answer two different questions, and a tool
// present in one and absent from the other is a real, silent defect this
// codebase has already shipped once (see request_mount's note in
// pkg/agent/instance.go — catalogued but unregistered, so it appeared in
// Settings while no agent could ever call it, and the ADR-067 nine's own
// authoring half repeated it in the other direction: a seeded policy posture
// with no registry offering the tool at all).
//
//  1. EXECUTION. Each agent's own *tools.ToolRegistry, built in
//     pkg/agent/instance.go's NewAgentInstance (registerKnowledgeTools,
//     pkg/agent/knowledge_tools.go). That is the registry a turn dispatches
//     through and the one GET /agents/{id}/tools reads, so it is the only
//     one that makes a tool CALLABLE.
//
//  2. METADATA. The process-wide *tools.BuiltinRegistry this file contributes
//     to, which backs GET /api/v1/tools — the "everything the platform can
//     do" capability reference behind the tool picker and the global
//     tool-policy screen. Instances here are metadata-only and are NEVER
//     Execute()d (ADR-018 D-A1), exactly like the general-builtin and
//     browser-builtin metadata registered beside them in gateway.go.
//
// pkg/tools cannot supply (2) itself the way it supplies the general builtins:
// pkg/knowledge imports pkg/tools, so tools.GeneralBuiltinMetadata() importing
// pkg/knowledge back would be an import cycle. pkg/gateway already depends on
// both, which is why this lives here — the same reason browser.* metadata is
// registered from gateway.go rather than from pkg/tools.
//
// NOT registered here: policy COVERAGE. buildKnownBuiltinToolNames
// (gateway.go) unions all six knowledge tool names in explicitly and is the
// authority for Constraint #6's (agent × tool) universe. It deliberately does
// not derive that list from this file, so that a tool dropped from the
// catalog still carries an explicit seeded posture rather than silently
// vanishing from the coverage universe as well.
//
// ADR-067's nine (knowledge_search, knowledge_graph, knowledge_create,
// knowledge_link, knowledge_set_property, knowledge_append_section,
// knowledge_tasks, knowledge_move, knowledge_rename) are RETIRED from both
// registries below — ADR-068's six supersede them by blast radius rather
// than by read/write. Their Go implementations (pkg/knowledge/tools.go,
// authoring_tools.go) are NOT deleted: pkg/gateway/rest_knowledge.go still
// calls knowledge.RetrievalTools directly for the Library UI's own search/
// graph REST endpoints, which are independent of the agent tool-calling
// surface this file wires. Deleting them would break that surface for no
// reason connected to this retirement.
// ---------------------------------------------------------------------------

// knowledgeBuiltinMetadata returns metadata-only instances of ADR-068's six
// knowledge tools: the three READ-tier tools (knowledge_describe,
// knowledge_find, knowledge_read), knowledge_edit (one named file),
// knowledge_restructure (cascading rename/move/trash) and
// knowledge_configure (the schema/view control plane).
//
// The instances are constructed with ZERO deps — an empty ToolDeps.Home /
// AuthoringDeps.Home resolves to an EMPTY knowledge scope (see
// knowledge.ResolveScope), so a metadata instance that somehow reached
// Execute() would address nothing at all rather than reach across a
// workspace boundary — safe for the same reason the general-builtin
// catalog's dummy-workspace instances are: only Name(), Description(),
// Category() and Scope() are ever called on them here (ADR-018 D-A1).
// knowledge_describe's openIndex is nil for the same reason: metadata never
// executes, so there is nothing for it to open.
func knowledgeBuiltinMetadata() []tools.Tool {
	return []tools.Tool{
		knowledge.NewDescribeTool(knowledge.ToolDeps{}, nil),
		vaultprops.NewFindTool(""),
		knowledge.NewReadTool(knowledge.ToolDeps{}),
		knowledge.NewEditTool(knowledge.AuthoringDeps{}),
		knowledge.NewRestructureTool(knowledge.AuthoringDeps{}),
		knowledge.NewConfigureTool(knowledge.AuthoringDeps{}),
	}
}

// registerKnowledgeBuiltinMetadata adds the knowledge tools to a builtin
// registry.
//
// It is no longer called from boot, and that is the fix rather than an
// oversight: boot now builds the whole catalog through ONE function,
// buildCentralBuiltinRegistry (central_builtin_registry.go), so the pre-deps
// and live-deps passes cannot disagree about which families they contain. They
// did disagree — the knowledge metadata went into the first registry and the
// second one, the only one restAPI.builtinRegistry ever sees, replaced it
// wholesale. Registration succeeded, nothing warned, and GET /api/v1/tools
// simply had no knowledge tool in it.
//
// The helper is kept because it states the log-and-skip contract for this
// family on its own, and because a future host that needs to add the family to
// some other registry should not have to re-derive it.
//
// Log-and-skip on a duplicate, matching the general-builtin and
// browser-builtin loops it used to sit beside: a duplicate name is a
// registration-order problem, never a reason to fail boot. A nil registry is a
// no-op so a caller in a partially-wired test cannot panic here.
func registerKnowledgeBuiltinMetadata(reg *tools.BuiltinRegistry) {
	if reg == nil {
		return
	}
	for _, t := range knowledgeBuiltinMetadata() {
		if err := reg.RegisterBuiltin(t); err != nil {
			slog.Warn("gateway: central builtin registry knowledge-builtin skipped",
				"tool", t.Name(), "error", err)
		}
	}
}
