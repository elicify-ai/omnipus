// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// knowledge_tools.go — registering ADR-067's knowledge tool family into an
// agent's EXECUTION registry.
//
// # What was wrong
//
// Only the retrieval pair (knowledge_search, knowledge_graph) was registered.
// The seven authoring tools — knowledge_create, knowledge_link,
// knowledge_set_property, knowledge_append_section, knowledge_tasks,
// knowledge_move, knowledge_rename — were fully implemented and unit-tested in
// pkg/knowledge and constructed by nothing: knowledge.AuthoringTools had no
// non-test caller anywhere in the tree. So no agent could call them, they were
// absent from GET /agents/{id}/tools, and pkg/config/defaults.go +
// pkg/coreagent/core.go seeded them an explicit allow/ask posture — a granted
// posture over a tool no registry offered. That is precisely the request_mount
// defect this repository has already shipped once, in the other direction.
//
// Registration is UNCONDITIONAL for every agent, exactly like every sibling in
// NewAgentInstance. What an agent may actually do is its seeded tool POLICY
// (Constraint #6), never whether the tool was registered — a conditionally
// registered tool cannot be granted by an operator afterwards, because
// GET /agents/{id}/tools lists this very registry.
//
// # Where the audit wiring lives, and why not here
//
// FR-090 requires an audit record for every knowledge-base mutation AND every
// refusal. The sink that satisfies it is knowledge.AuthoringDeps.Audit, and
// the adapter onto the process audit logger lives in pkg/knowledge
// (authoring_audit_bridge.go) rather than in this package: the tools belong to
// pkg/knowledge, so their audit contract belongs beside them, and pkg/agent
// should not have to know how a knowledge tool audits. Each tool implements
// SetAuditLogger there, which is pkg/tools' auditLoggerAware contract, so the
// propagation the rest of the tree already performs
// (AgentLoop.wireMemoryAuditLoggerOn → ToolRegistry.SetAuditLogger) reaches
// them with no decorator and no change here.
//
// The sink installed at construction is NewAuthorAuditLogger(nil) — the
// structured-log fallback — and it is not a placeholder. `sandbox.audit_log`
// is false on a default install (nothing in pkg/config/defaults.go seeds it),
// so al.auditLogger is nil, SetAuditLogger is never called, and the sink set
// HERE is the one that runs on most gateways. Leaving the field nil instead
// would register seven tools that refuse every call, because a nil Audit is a
// fail-closed refusal (knowledge/authoring_tools.go's begin).

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// registerKnowledgeTools registers all nine knowledge tools into reg.
//
// Home is $OMNIPUS_HOME and nothing else. It is load-bearing rather than
// incidental: knowledge.ResolveScope reads the workspace MOUNT STORE under
// this home to decide what the caller may address, and an empty or wrong Home
// resolves to an EMPTY scope — every search would answer "no collections"
// forever, with no error logged anywhere. WHICH collection any individual call
// may address is resolved per call from the calling agent's workspace, never
// from a tool argument the model controls (FR-052/FR-053, US-9).
//
// One tool instance per agent means one FR-055 rate-limit bucket per agent,
// which is the bucket the requirement is written in terms of.
func registerKnowledgeTools(reg *tools.ToolRegistry) {
	home := config.OmnipusHomeDir()

	for _, t := range knowledge.RetrievalTools(knowledge.ToolDeps{Home: home}) {
		reg.Register(t)
	}
	for _, t := range knowledge.AuthoringTools(knowledge.AuthoringDeps{
		Home:  home,
		Audit: knowledge.NewAuthorAuditLogger(nil),
	}) {
		reg.Register(t)
	}
}
