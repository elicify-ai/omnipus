// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// central_builtin_registry.go — the ONE place the process-wide builtin
// metadata catalog behind GET /api/v1/tools is assembled.
//
// # Why this is a function rather than two inline loops
//
// Boot builds this registry TWICE: once before sysAgentDeps exists (metadata
// only) and once afterwards with live deps, replacing the first instance
// wholesale and handing the replacement to restAPI.builtinRegistry. When the
// two sites were two hand-maintained sequences of loops they drifted, and the
// drift was silent by construction: ADR-067's knowledge tools were registered
// into the FIRST registry and the second one — the only one the REST handler
// ever reads — never learned about them. Registration succeeded, no warning
// was logged, and `GET /api/v1/tools` simply returned 89 entries with no
// knowledge tool among them. An operator could then neither see nor govern
// knowledge_search, knowledge_graph or the seven authoring tools anywhere in
// the UI (Settings → tool policy, the per-agent tool picker, the create-agent
// modal all read that endpoint), while pkg/config/defaults.go seeded them an
// explicit allow/ask posture — a granted posture over a tool no catalog
// offered.
//
// One function means the two sites cannot disagree again: adding a family here
// adds it to both, and the test that asserts a family is present exercises the
// SAME function boot calls rather than a re-creation of it.
//
// Nothing registered here is ever Execute()d (ADR-018 D-A1). These instances
// answer Name/Description/Category and nothing else.

import (
	"log/slog"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// centralBuiltinCounts is what was actually admitted, per family, so boot can
// log a breakdown that is measured rather than inferred by subtraction.
type centralBuiltinCounts struct {
	system    int
	general   int
	browser   int
	knowledge int
}

// total is every builtin admitted across all families.
func (c centralBuiltinCounts) total() int {
	return c.system + c.general + c.browser + c.knowledge
}

// buildCentralBuiltinRegistry assembles the full builtin metadata catalog.
//
// sysDeps may be nil: the pre-deps boot pass passes nil deliberately, because
// only the tools' names, descriptions and categories are read from these
// instances. Duplicates are logged and skipped, never fatal — a duplicate name
// is a registration-order problem, not a reason to refuse to boot.
func buildCentralBuiltinRegistry(sysDeps *systools.Deps) (*tools.BuiltinRegistry, centralBuiltinCounts) {
	reg := tools.NewBuiltinRegistry()
	var counts centralBuiltinCounts

	register := func(family string, list []tools.Tool, into *int) {
		for _, t := range list {
			if err := reg.RegisterBuiltin(t); err != nil {
				slog.Warn("gateway: central builtin registry entry skipped",
					"family", family, "tool", t.Name(), "error", err)
				continue
			}
			*into++
		}
	}

	register("system", systools.AllTools(sysDeps, nil), &counts.system)
	register("general", tools.GeneralBuiltinMetadata(), &counts.general)
	register("browser", browser.BrowserBuiltinMetadata(), &counts.browser)
	register("knowledge", knowledgeBuiltinMetadata(), &counts.knowledge)

	return reg, counts
}
