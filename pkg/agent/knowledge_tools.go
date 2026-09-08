// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// knowledge_tools.go — registering ADR-068's vault-records knowledge tool
// family into an agent's EXECUTION registry.
//
// # What this replaces, and why
//
// This used to register ADR-067's nine-tool family (knowledge_search,
// knowledge_graph, knowledge_create, knowledge_link, knowledge_set_property,
// knowledge_append_section, knowledge_tasks, knowledge_move,
// knowledge_rename). ADR-068 superseded that family with SIX tools split by
// blast radius rather than by read/write, and KB-1/KB-2
// (defect-list-knowledge-base-ux-2026-09-08.md, founder-ratified
// 2026-09-08) add two more — an agent could reach knowledge_describe,
// knowledge_find, knowledge_read, knowledge_edit, knowledge_restructure and
// knowledge_configure but had no verb that MADE a knowledge base and no way
// to discover which ones it could reach without already knowing one:
//
//	knowledge_describe     — read: orientation, schema, saved views, index state
//	knowledge_find         — read: the one retrieval surface (words, typed
//	                          filter, saved views, relations, tasks)
//	knowledge_read         — read: one note in full or one section
//	knowledge_list         — read: which knowledge bases this agent can reach,
//	                          with per-collection index freshness (KB-2a)
//	knowledge_edit         — write: ONE named file (create/set_property/
//	                          append_section/link/replace_body)
//	knowledge_restructure  — write: rename/move/trash/restore — CASCADES,
//	                          rewriting files the caller never named
//	knowledge_configure    — write: record-type schema and saved-view
//	                          control plane — changes what existing notes MEAN
//	knowledge_base_create  — write: makes a NEW knowledge base in the
//	                          workspace's own Library (KB-1) — distinct from
//	                          knowledge_edit's create op, which adds a NOTE
//	                          inside one that already exists
//
// The nine ADR-067 tools were fully implemented, unit-tested, and reachable
// through this registry — but they are retired now that ADR-068's family
// supersedes them; leaving both registered would let an agent silently keep
// using the superseded surface after the seeded policy stopped naming it
// (Constraint #6 requires an explicit posture per agent per tool, and the
// nine old names have none any more — see pkg/config/defaults.go and
// pkg/coreagent/core.go).
//
// Registration is UNCONDITIONAL for every agent, exactly like every sibling
// in NewAgentInstance. What an agent may actually do is its seeded tool
// POLICY (Constraint #6), never whether the tool was registered — a
// conditionally registered tool cannot be granted by an operator afterwards,
// because GET /agents/{id}/tools lists this very registry.
//
// # Where each tool is actually built
//
// knowledge_describe, knowledge_read, knowledge_edit, knowledge_restructure
// and knowledge_configure are tools.Tool implementations in pkg/knowledge
// (tools.go, knowledge_edit.go, knowledge_restructure.go,
// knowledge_configure.go), constructed here directly.
//
// knowledge_find is different: pkg/records/knowledgefind exposes a package
// function, not a tools.Tool, and assembling its dependencies needs both
// pkg/knowledge and pkg/records/propindex joined together — pkg/vaultprops's
// job, and the one place both this execution-registry call site and
// pkg/gateway/knowledge_tools_wire.go's metadata-catalog call site can reach
// without a cycle (pkg/vaultprops.FindTool's own header explains why it does
// not live in pkg/knowledge or pkg/gateway).
//
// # Where the audit wiring lives, and why not here
//
// FR-090 requires an audit record for every knowledge-base mutation AND
// every refusal. The sink that satisfies it is knowledge.AuthoringDeps.Audit,
// constructed HERE and passed to each mutating tool directly (NewEditTool,
// NewRestructureTool, NewConfigureTool, and now NewCreateBaseTool below) —
// unlike the retired ADR-067 authoring tools (authoring_audit_bridge.go),
// none of ADR-068's four implements pkg/tools' auditLoggerAware
// (SetAuditLogger), so a later call to ToolRegistry.SetAuditLogger (e.g. once
// `sandbox.audit_log` is turned on) does NOT reach them — they keep the sink
// built at construction for the lifetime of the agent instance. That is the
// live behaviour, not the aspiration: correct it here if it is ever changed,
// rather than leaving this comment newly wrong for a fourth tool.
//
// The sink installed at construction is NewAuthorAuditLogger(nil) — the
// structured-log fallback — and it is not a placeholder. `sandbox.audit_log`
// is false on a default install (nothing in pkg/config/defaults.go seeds
// it), so this fallback sink is the one that runs on most gateways. Leaving
// the field nil instead would register every mutating knowledge tool
// refusing EVERY call, because a nil Audit is a fail-closed refusal
// (knowledge/authoring_tools.go's begin, and knowledge_base_create.go's own
// mirror of that same precondition — the preamble knowledge_edit.go/
// knowledge_restructure.go/knowledge_configure.go share).
//
// # Manifest tier — unconditional registration does NOT mean unconditional
// # per-turn cost
//
// "Registered for every agent" (above) is a POLICY statement (Constraint
// #6): it says nothing about what ends up on the wire on a turn that never
// touches these tools. That is governed separately, by ADR-071's manifest
// tier (pkg/tools/manifest.go, ToolManifestTier/ToolManifestVisibility): none
// of the eight names is listed in fullManifestToolNames or
// previewedLazyToolNames, so all eight resolve to the deliberate default —
// ManifestLazy + ManifestSearchOnly — meaning they cost ZERO tokens on any
// turn until an agent calls ToolSearch for one by name or query (pinned by
// pkg/tools/manifest_test.go's TestVisibility_KnowledgeToolsAreSearchOnly).
// If a future change ever needs one of these eight to be always-visible
// (Full) or previewed (Tier 2), that is a deliberate manifest-tier decision
// belonging in pkg/tools/manifest.go, updating that same pinning test — not
// something to infer from this file's registration call.

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// registerKnowledgeTools registers all six ADR-068 knowledge tools into reg.
//
// Home is $OMNIPUS_HOME and nothing else. It is load-bearing rather than
// incidental: knowledge.ResolveScope reads the workspace MOUNT STORE under
// this home to decide what the caller may address, and an empty or wrong
// Home resolves to an EMPTY scope — every call would answer "no collections"
// forever, with no error logged anywhere. WHICH collection any individual
// call may address is resolved per call from the calling agent's workspace,
// never from a tool argument the model controls (FR-052/FR-053, US-9).
//
// One tool instance per agent means one FR-055 rate-limit bucket per agent,
// which is the bucket the requirement is written in terms of.
func registerKnowledgeTools(reg *tools.ToolRegistry) {
	home := config.OmnipusHomeDir()
	audit := knowledge.NewAuthorAuditLogger(nil)

	// READ TIER — touch nothing outside the file(s) the caller reads.
	reg.Register(knowledge.NewDescribeTool(knowledge.ToolDeps{Home: home}, vaultprops.Open))
	reg.Register(vaultprops.NewFindTool(home))
	reg.Register(knowledge.NewReadTool(knowledge.ToolDeps{Home: home}))
	// knowledge_list (KB-2a): which knowledge bases this agent can reach —
	// also read tier, touches nothing.
	reg.Register(knowledge.NewListTool(knowledge.ToolDeps{Home: home}))

	// EDIT — mutates exactly the ONE file the caller named.
	reg.Register(knowledge.NewEditTool(knowledge.AuthoringDeps{Home: home, Audit: audit}))

	// RESTRUCTURE — rename/move/trash/restore CASCADE: they rewrite inbound
	// links in every note that referenced the target, none of which the
	// caller named.
	reg.Register(knowledge.NewRestructureTool(knowledge.AuthoringDeps{Home: home, Audit: audit}))

	// CONFIGURE — the control plane: changes what EXISTING notes of a type
	// mean (a record-type schema edit reclassifies every record already on
	// disk) or what a saved view returns.
	reg.Register(knowledge.NewConfigureTool(knowledge.AuthoringDeps{Home: home, Audit: audit}))

	// knowledge_base_create (KB-1): makes a NEW knowledge base in the
	// workspace's own Library. Distinct blast radius from the others above —
	// it creates a folder+marker rather than touching an existing
	// collection's contents — but it is FR-090 audit-covered the same way,
	// so it shares the same Audit sink.
	reg.Register(knowledge.NewCreateBaseTool(knowledge.AuthoringDeps{Home: home, Audit: audit}))
}
