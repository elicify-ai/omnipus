// Omnipus — ADR-081 D11 (unified-search-and-grep-spec.md FR-008/FR-009):
// the "grep" agent tool's gateway-side METADATA wiring — the catalog entry
// GET /api/v1/tools serves.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// grepBuiltinMetadata returns the metadata-only "grep" catalog entry
// (MV-7: grep must appear in GET /api/v1/tools so an operator can see and
// govern it in Settings, the per-agent tool picker and the create-agent
// modal — the same visibility guarantee TestCentralBuiltinRegistry_
// CarriesTheKnowledgeTools protects for the knowledge family).
//
// It mirrors knowledgeBuiltinMetadata's pattern exactly: construct the REAL
// tool type (pkg/tools.GrepTool, the same type pkg/agent/instance.go
// registers per-agent for execution) with zero-value deps. That is safe
// because the central BuiltinRegistry is a metadata catalog, never an
// execution registry (ADR-018 D-A1): its sole consumer, HandleToolsRegistry
// (rest_tool_registry.go), reads Name()/Description()/Scope()/Category()
// off each entry and never calls Parameters() or Execute(). An empty
// agentHome addresses nothing if Execute were ever reached anyway — the
// tool's own fs-policy resolution refuses before any walk starts.
//
// History, kept short: while the real implementation was landing on a
// sibling work track, this file carried a self-contained grepMetadataTool
// stub so the governance layer (ADR-071 tiering, ADR-077 policy coverage)
// could compile and be tested independently. Per that stub's own header,
// it is REPLACED — not kept alongside — now that the real type exists:
// registering both under the same "grep" name would silently drop one at
// RegisterBuiltin's log-and-skip-duplicate step, the exact silent-catalog-
// drift failure mode central_builtin_registry.go's header documents.
func grepBuiltinMetadata() []tools.Tool {
	return []tools.Tool{tools.NewGrepTool("", false)}
}
