// Omnipus — the tool-policy pin for op="embed" (EMB-096, Hard Constraint #6,
// adr-083-embedded-content-spec.md test 38:
// TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp).
//
// Embed authoring is an OPERATION on the existing knowledge_edit tool, never
// a ninth tool name (EMB-095). The expected diff to tool policy is ZERO
// LINES in both layers — the global ceiling and every per-agent seed already
// carry an explicit "knowledge_edit" entry, and an operation needs no policy
// entry of its own. This is pinned by SET EQUALITY over the eight
// knowledge_* names, never by membership: a membership check
// ("knowledge_edit is present") would still pass if a ninth tool
// (e.g. "knowledge_embed") were added beside it — exactly the regression
// this test exists to catch.
//
// This is a THIRD net, not the only one: TestCatalog_MatchesGlobalCeilingEntryForEntry
// (pkg/coreagent) already asserts the ceiling matches the static catalog
// entry for entry, and pkg/coreagent/constructor_seed_test.go already
// asserts every per-agent seed's key set matches it too. A regression here
// would already fail those first — this test's own job is narrower and more
// legible: it names the eight knowledge_* tools explicitly, so a failure
// here says exactly which knowledge surface drifted.
//
// External test package (knowledge_test), not internal: this assertion is
// fundamentally about pkg/config and pkg/coreagent state, not about
// anything in pkg/knowledge's own internals — importing both from an
// internal `package knowledge` test file would risk a build if either ever
// gained a (transitive) import of pkg/knowledge itself; an external test
// package sidesteps that regardless of which direction any future import
// runs.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// wantKnowledgeTools is the CLOSED set EMB-096 requires: exactly the eight
// knowledge_* tool names that exist today. knowledge_link and
// knowledge_set_property are retired (zero ceiling entries) and are
// deliberately NOT listed here — their absence is itself part of what this
// test pins.
var wantKnowledgeTools = map[string]bool{
	"knowledge_describe":    true,
	"knowledge_find":        true,
	"knowledge_read":        true,
	"knowledge_list":        true,
	"knowledge_edit":        true,
	"knowledge_restructure": true,
	"knowledge_configure":   true,
	"knowledge_base_create": true,
}

func knowledgeKeysFromStringMap(m map[string]string) map[string]bool {
	out := make(map[string]bool)
	for k := range m {
		if strings.HasPrefix(k, "knowledge_") {
			out[k] = true
		}
	}
	return out
}

func knowledgeKeysFromPolicyMap(m map[string]config.ToolPolicy) map[string]bool {
	out := make(map[string]bool)
	for k := range m {
		if strings.HasPrefix(k, "knowledge_") {
			out[k] = true
		}
	}
	return out
}

func TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp(t *testing.T) {
	// (1) The global ceiling's knowledge_* key set is EXACTLY the eight
	// names — set equality, never membership.
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig must seed a fresh install")
	assert.Equal(t, wantKnowledgeTools, knowledgeKeysFromStringMap(cfg.Sandbox.ToolPolicies),
		"the global ceiling's knowledge_* keys must be exactly the eight names op=\"embed\" was added inside")

	// (2) No SEEDED core agent's key set gains or loses a knowledge_*
	// entry.
	require.NotEmpty(t, cfg.Agents.List, "SeedConfig must have seeded at least one core agent")
	for _, a := range cfg.Agents.List {
		got := knowledgeKeysFromPolicyMap(a.Tools.Builtin.Policies)
		assert.Equalf(t, wantKnowledgeTools, got,
			"seeded agent %q's knowledge_* policy keys must stay the eight names", a.ID)
	}

	// (3) The NEW-CUSTOM-AGENT template (what a freshly created agent
	// starts from) carries the same eight, no more, no fewer.
	customPolicies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	assert.Equal(t, wantKnowledgeTools, knowledgeKeysFromPolicyMap(customPolicies),
		"the custom-agent policy template's knowledge_* keys must stay the eight names")

	// (4) Reconciling a config that predates this work — every knowledge_*
	// entry stripped from the ceiling, exactly like an install whose
	// config.json was last written before op="embed" shipped — restores
	// EXACTLY the eight names, never a ninth. ReconcileToolPolicyCeiling
	// only ever ADDS a name already in the static catalog (coreagent's own
	// allStaticToolNames), so this also proves no "knowledge_embed" (or
	// similar) entry was ever added to that catalog.
	pre := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(pre))
	for k := range pre.Sandbox.ToolPolicies {
		if strings.HasPrefix(k, "knowledge_") {
			delete(pre.Sandbox.ToolPolicies, k)
		}
	}
	known := make(map[string]struct{})
	for _, n := range coreagent.AllStaticToolNames() {
		known[n] = struct{}{}
	}
	// Each entry is "name=value" (ReconcileToolPolicyCeiling's own format,
	// e.g. "knowledge_edit=allow") — the bare name is what this test pins.
	added := config.ReconcileToolPolicyCeiling(pre, known)
	addedKnowledge := make(map[string]bool)
	for _, n := range added {
		name, _, _ := strings.Cut(n, "=")
		if strings.HasPrefix(name, "knowledge_") {
			addedKnowledge[name] = true
		}
	}
	assert.Equal(t, wantKnowledgeTools, addedKnowledge,
		"reconciling a pre-existing install must restore exactly the eight knowledge_* names, never a ninth")
	assert.Equal(t, wantKnowledgeTools, knowledgeKeysFromStringMap(pre.Sandbox.ToolPolicies),
		"after reconciliation the ceiling's knowledge_* key set must again be exactly the eight names")
}
