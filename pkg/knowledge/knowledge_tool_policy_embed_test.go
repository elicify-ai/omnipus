// Omnipus — the tool-policy pin for op="embed" (EMB-096, Hard Constraint #6,
// adr-083-embedded-content-spec.md test 38:
// TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp).
//
// Embed authoring is an OPERATION on the existing knowledge_edit tool, never
// a ninth tool name (EMB-095). The expected diff to tool policy is ZERO
// LINES — the global ceiling carries all eight knowledge_* names, and the
// per-agent layer realizes ADR-090 §5's role postures as the sparse delta
// from the allow ceiling (read tools ride the ceiling; write tools carry
// the role's ask/deny posture; the hidden system agents deny all eight).
// An operation needs no policy entry of its own. This is pinned by SET
// EQUALITY over the eight knowledge_* names at the ceiling and SET+VALUE
// equality per seeded role, never by membership: a membership check
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

// knowledgePoliciesFromPolicyMap filters a per-agent policy map to its
// knowledge_* entries, keeping the VALUES — the per-role posture is part of
// the ADR-090 contract the seed check pins (ask vs deny on the write tools),
// not just the key set.
func knowledgePoliciesFromPolicyMap(m map[string]config.ToolPolicy) map[string]config.ToolPolicy {
	out := make(map[string]config.ToolPolicy)
	for k, v := range m {
		if strings.HasPrefix(k, "knowledge_") {
			out[k] = v
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

	// (2) No SEEDED agent's knowledge_* delta drifts. ADR-090 §5 (founder
	// clarification, 2026-09-17/18) splits the eight names into four READ
	// tools and four WRITE tools with per-role postures, realized as the
	// ADR-077 sparse seed: a role's intended posture that equals the global
	// "allow" ceiling persists NO key (riding the ceiling is the normal,
	// intended state), so the shipped per-agent knowledge_* entries are:
	//
	//   - mia / jim / worker: write=ask (≠ ceiling) → exactly the four
	//     write keys as ask; read rides the allow ceiling.
	//   - ava / admin / planner / researcher: the §5 role table excludes
	//     knowledge writes → a deliberate deny override on the four write
	//     keys (omission alone is not denial); read rides the ceiling.
	//   - judge / plansupervisor: read AND write excluded; the dense
	//     system-agent seed carries all eight as explicit deny.
	//
	// Set AND value equality per role, never membership: a ninth tool name,
	// a lost role posture, or a read key that silently stopped riding the
	// ceiling all fail here.
	knowledgeWriteNames := []string{
		"knowledge_edit", "knowledge_restructure", "knowledge_configure", "knowledge_base_create",
	}
	knowledgeWriteAsk := map[string]config.ToolPolicy{}
	knowledgeWriteDeny := map[string]config.ToolPolicy{}
	for _, name := range knowledgeWriteNames {
		knowledgeWriteAsk[name] = config.ToolPolicyAsk
		knowledgeWriteDeny[name] = config.ToolPolicyDeny
	}
	knowledgeFullDeny := map[string]config.ToolPolicy{}
	for name := range wantKnowledgeTools {
		knowledgeFullDeny[name] = config.ToolPolicyDeny
	}
	expectedKnowledgeSeed := map[string]map[string]config.ToolPolicy{
		"mia": knowledgeWriteAsk, "jim": knowledgeWriteAsk, "worker": knowledgeWriteAsk,
		"ava": knowledgeWriteDeny, "admin": knowledgeWriteDeny,
		"planner": knowledgeWriteDeny, "researcher": knowledgeWriteDeny,
		"judge": knowledgeFullDeny, "plansupervisor": knowledgeFullDeny,
	}
	require.NotEmpty(t, cfg.Agents.List, "SeedConfig must have seeded at least one core agent")
	for _, a := range cfg.Agents.List {
		want, known := expectedKnowledgeSeed[a.ID]
		if !known {
			t.Fatalf("seeded agent %q is not in the ADR-090 knowledge matrix — extend expectedKnowledgeSeed consciously, do not inherit silently", a.ID)
		}
		got := knowledgePoliciesFromPolicyMap(a.Tools.Builtin.Policies)
		assert.Equalf(t, want, got,
			"seeded agent %q's knowledge_* policy must match the ADR-090 role posture", a.ID)
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
