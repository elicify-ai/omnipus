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
)

// ---------------------------------------------------------------------------
// WHERE THE KNOWLEDGE TOOLS ARE REGISTERED, AND WHY IN TWO PLACES
//
// There are two registries, they answer two different questions, and a tool
// present in one and absent from the other is a real, silent defect this
// codebase has already shipped once (see request_mount's note in
// pkg/agent/instance.go — catalogued but unregistered, so it appeared in
// Settings while no agent could ever call it).
//
//  1. EXECUTION. Each agent's own *tools.ToolRegistry, built in
//     pkg/agent/instance.go's NewAgentInstance. That is the registry a turn
//     dispatches through and the one GET /agents/{id}/tools reads, so it is
//     the only one that makes a tool CALLABLE. knowledge_search and
//     knowledge_graph are registered there, per agent, with the agent's real
//     $OMNIPUS_HOME so the workspace scope (FR-052/FR-053) resolves.
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
// (gateway.go) unions all nine knowledge tool names in explicitly and is the
// authority for Constraint #6's (agent × tool) universe. It deliberately does
// not derive that list from this file, so that a tool dropped from the
// catalog still carries an explicit seeded posture rather than silently
// vanishing from the coverage universe as well.
// ---------------------------------------------------------------------------

// knowledgeBuiltinMetadata returns metadata-only instances of every knowledge
// tool that has a real implementation today: ADR-067 stage 2's retrieval pair
// (knowledge_search, knowledge_graph) AND stage 3's seven authoring tools
// (knowledge_create, knowledge_link, knowledge_set_property,
// knowledge_append_section, knowledge_tasks, knowledge_move,
// knowledge_rename).
//
// All nine, because the D17 seed enumerates all nine. The authoring seven were
// omitted here while they were also absent from every agent's execution
// registry, which made a granted seeded posture govern tools that no registry
// offered and no catalog described — invisible from both directions at once.
//
// The instances are constructed with a ZERO ToolDeps, which is safe for the
// same reason the general-builtin catalog's dummy-workspace instances are:
// only Name(), Description(), Category() and Scope() are ever called on them.
// It is also safe if that invariant is ever broken by accident — an empty
// ToolDeps.Home resolves to an EMPTY knowledge scope (see
// knowledge.ResolveScope), so a metadata instance that somehow reached
// Execute() would read nothing at all rather than reading across a workspace
// boundary.
//
// The list is taken from knowledge.RetrievalTools rather than restated as a
// name literal, so a tool renamed or added in pkg/knowledge cannot go missing
// from the capability reference without anyone noticing.
func knowledgeBuiltinMetadata() []tools.Tool {
	out := knowledge.RetrievalTools(knowledge.ToolDeps{})
	// AuthoringDeps' zero value is safe for metadata for the same reason
	// ToolDeps' is: an empty Home resolves to an EMPTY scope, so a metadata
	// instance that somehow reached Execute() would address nothing at all
	// rather than reach across a workspace boundary.
	return append(out, knowledge.AuthoringTools(knowledge.AuthoringDeps{})...)
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
