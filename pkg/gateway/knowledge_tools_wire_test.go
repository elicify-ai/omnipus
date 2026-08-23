// Omnipus — ADR-067 D7/D17: the knowledge-base tool family is actually
// REACHABLE by a real agent (FR-050–FR-055, FR-070/FR-071, US-9).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/gateway/
//
// ---------------------------------------------------------------------------
// WHAT THESE TESTS EXIST TO CATCH, AND WHY THE OBVIOUS ONES ARE NOT ENOUGH
//
// pkg/knowledge shipped complete, tested and race-clean while
// `go list -deps ./cmd/omnipus/` contained no reference to it at all: no
// agent had knowledge_search or knowledge_graph, and nothing said so. The
// existing knowledge_tool_policy_seed_test.go proves the POLICY seed is
// right, which is a statement about config data — it stays green whether or
// not a registry ever offers the tool the posture governs. This file is the
// other half: the tools are in an agent's execution registry, they survive
// the policy filter that decides what a turn may call, and when they run
// they see exactly the calling agent's own workspace and nothing else.
//
// Every assertion's expected value comes from ADR-067 (D7's tool pair, D17's
// posture matrix, FR-052/FR-053's isolation rule), never from what the code
// happens to produce.
// ---------------------------------------------------------------------------

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/routing"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// knowledgeRetrievalToolNames is ADR-067 D7's retrieval pair, written out as
// the SPEC states it rather than read back from pkg/knowledge. A rename in
// the implementation must fail a test here, not silently redefine what these
// tests are checking — the seeded D17 posture (pkg/config/defaults.go,
// pkg/coreagent/core.go) is keyed on these exact strings, and a tool whose
// name no longer matches its seeded entry ships DENIED with only a WARN.
var knowledgeRetrievalToolNames = []string{"knowledge_graph", "knowledge_search"}

// d17RetrievalAllowAgents is D17's seed posture for the retrieval pair:
// "allow for all four core agents". Stated as data, from the ADR.
var d17RetrievalAllowAgents = []string{
	string(coreagent.IDMia),
	string(coreagent.IDJim),
	string(coreagent.IDAva),
	string(coreagent.IDRay),
}

// ---------------------------------------------------------------------------
// Fixtures. Prefixed kw so they cannot collide with the other helpers this
// package's very large test suite already defines.
// ---------------------------------------------------------------------------

// kwReal resolves a path the way pkg/knowledge does. On macOS every t.TempDir()
// lives under /var, which is a symlink to /private/var; a collection root is
// identified by its REAL path, so an unresolved expectation fails for a reason
// that has nothing to do with the code under test.
func kwReal(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return filepath.Clean(resolved)
}

// kwLoop builds a real AgentLoop over the fresh-install seed (DefaultConfig +
// coreagent.SeedConfig — the same composition gateway boot performs), rooted
// at an isolated $OMNIPUS_HOME, and returns the home it used.
//
// Agents.Defaults.Home is redirected under that home on purpose: without it
// every agent instance would materialise its workspace in the developer's real
// ~/.omnipus/agents while the mount store this test writes lives in a temp dir,
// and the two halves of the scope resolution would be looking at different
// installations.
func kwLoop(t *testing.T) (*agent.AgentLoop, string, *config.Config) {
	t.Helper()
	home := kwReal(t, t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)

	cfg := seededBootConfig(t)
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return al, home, cfg
}

// kwWorkspace seeds a minimal valid workspace record and returns its id.
func kwWorkspace(t *testing.T, home, id string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: id, Name: id, Status: "active", CreatedAt: now, UpdatedAt: now,
	}))
	return id
}

// kwVault creates a knowledge base at dir whose marker names it displayName,
// and returns its real path.
//
// The marker file name is spelled out here rather than imported because it is
// unexported. That is deliberate rather than sloppy: if pkg/knowledge ever
// renames it, DisplayName() falls back to the FOLDER name, and every
// assertion below that names the display name fails loudly instead of
// quietly measuring something else.
func kwVault(t *testing.T, dir, displayName string) string {
	t.Helper()
	markerDir := filepath.Join(dir, knowledge.MarkerDirName)
	require.NoError(t, os.MkdirAll(markerDir, 0o700))
	raw, err := json.Marshal(knowledge.Marker{DisplayName: displayName})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(markerDir, "vault.json"), raw, 0o600))
	return kwReal(t, dir)
}

// kwNote writes a note inside a collection.
func kwNote(t *testing.T, root, relPath, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
}

// kwSearchResponse mirrors the JSON knowledge_search emits. Only the fields
// these tests assert on are declared; the rest are ignored by encoding/json.
type kwSearchResponse struct {
	Collection         string   `json:"collection"`
	Query              string   `json:"query"`
	ResultCount        int      `json:"result_count"`
	IndexState         string   `json:"index_state"`
	CollectionsInScope []string `json:"collections_in_scope"`
}

// kwGraphResponse mirrors the JSON knowledge_graph emits, same rule.
type kwGraphResponse struct {
	Collection         string   `json:"collection"`
	Operation          string   `json:"operation"`
	Count              int      `json:"count"`
	Nodes              []string `json:"nodes"`
	CollectionsInScope []string `json:"collections_in_scope"`
}

// kwExec runs one registered tool for one agent, in the given workspace, and
// returns the raw ForLLM payload. It fails the test if the tool is not
// registered — an unregistered tool is the whole defect this file exists for,
// so it must never degrade into a skipped assertion.
func kwExec(t *testing.T, al *agent.AgentLoop, agentID, toolName, workspaceID string, args map[string]any) *tools.ToolResult {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.Truef(t, ok, "agent %q must be registered", agentID)
	tool, ok := inst.Tools.Get(toolName)
	require.Truef(t, ok, "%q must be registered in agent %q's execution registry", toolName, agentID)

	ctx := tools.WithWorkspaceID(context.Background(), workspaceID)
	ctx = tools.WithAgentID(ctx, agentID)
	res := tool.Execute(ctx, args)
	require.NotNil(t, res)
	return res
}

func kwDecodeSearch(t *testing.T, res *tools.ToolResult) kwSearchResponse {
	t.Helper()
	require.Falsef(t, res.IsError,
		"knowledge_search must not error here — got %q", res.ForLLM)
	var out kwSearchResponse
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload: %s", res.ForLLM)
	return out
}

func kwDecodeGraph(t *testing.T, res *tools.ToolResult) kwGraphResponse {
	t.Helper()
	require.Falsef(t, res.IsError,
		"knowledge_graph must not error here — got %q", res.ForLLM)
	var out kwGraphResponse
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload: %s", res.ForLLM)
	return out
}

// ---------------------------------------------------------------------------
// 1. The name contract between pkg/knowledge and the D17 seed.
// ---------------------------------------------------------------------------

// TestKnowledgeRetrievalTools_AreTheD7Pair pins the two names everything else
// in this feature is keyed on.
//
// It is short and it is not a formality. The seeded posture in
// pkg/config/defaults.go and pkg/coreagent/core.go is a map keyed by literal
// tool name; Constraint #6 admits no wildcard for a static builtin. A tool
// renamed in pkg/knowledge alone therefore has NO policy entry, and the
// load-path repair backfills a missing entry to deny before validation ever
// runs (FR-071) — so the rename ships as a dead feature plus one WARN line,
// with the seed still reading exactly as D17 requires.
func TestKnowledgeRetrievalTools_AreTheD7Pair(t *testing.T) {
	assert.Equal(t, knowledgeRetrievalToolNames, knowledge.RetrievalToolNames(),
		"ADR-067 D7's retrieval pair is knowledge_search + knowledge_graph. Renaming or "+
			"adding one here without updating the D17 seed in pkg/config/defaults.go and "+
			"pkg/coreagent/core.go ships it DENIED, silently (FR-071)")
}

// TestKnowledgeBuiltinMetadata_ReachesCentralRegistry covers the capability
// reference behind GET /api/v1/tools.
//
// Registration into an agent's execution registry is what makes a tool
// callable; this registry is what makes it VISIBLE — in the tool picker and
// on the global tool-policy screen. request_mount shipped with exactly the
// inverse split (catalogued, never registered) and was unusable; the same
// split in this direction leaves a working tool that no operator can find or
// govern from the UI.
func TestKnowledgeBuiltinMetadata_ReachesCentralRegistry(t *testing.T) {
	reg := tools.NewBuiltinRegistry()
	registerKnowledgeBuiltinMetadata(reg)

	for _, name := range knowledgeRetrievalToolNames {
		got, ok := reg.Get(name)
		require.Truef(t, ok,
			"%q must be in the central builtin registry that backs GET /api/v1/tools", name)
		assert.NotEmptyf(t, got.Description(),
			"%q must carry a description — this registry exists to describe capabilities", name)
	}
}

// TestCentralBuiltinRegistry_CarriesTheKnowledgeTools closes the loop the test
// above deliberately cannot.
//
// TestKnowledgeBuiltinMetadata_ReachesCentralRegistry calls
// registerKnowledgeBuiltinMetadata itself, so it stays green even if boot
// never uses it — and boot did not. gateway.go built the catalog TWICE: once
// before sysAgentDeps existed, where the knowledge metadata was registered,
// and once afterwards from scratch, where it was not. The second registry is
// the one handed to restAPI.builtinRegistry, so `GET /api/v1/tools` returned
// 89 entries with no knowledge tool among them while every test stayed green.
//
// This replaces the AST scan that used to stand here. That scan asserted the
// source contained the call expression, which is not the same claim: a call
// wrapped in `if false { ... }`, or writing into a registry that is discarded
// afterwards — which is exactly what happened — leaves it green. This test
// calls buildCentralBuiltinRegistry, the SINGLE function both boot sites now
// use, and asserts on what comes out.
func TestCentralBuiltinRegistry_CarriesTheKnowledgeTools(t *testing.T) {
	// nil deps is the pre-sysAgentDeps boot pass; a non-nil Deps is the
	// live-deps pass. Both must carry the family — the historical defect was
	// precisely that the two passes disagreed.
	for _, tc := range []struct {
		name string
		deps *systools.Deps
	}{
		{name: "pre-deps boot pass", deps: nil},
		{name: "live-deps boot pass", deps: &systools.Deps{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, counts := buildCentralBuiltinRegistry(tc.deps)

			for _, name := range knowledgeToolNames {
				got, ok := reg.Get(name)
				require.Truef(t, ok,
					"%q is absent from the catalog GET /api/v1/tools serves. An operator "+
						"cannot see or govern it in Settings, the per-agent tool picker or the "+
						"create-agent modal — while pkg/config/defaults.go seeds it an explicit "+
						"allow/ask posture, i.e. a granted posture over a tool no catalog offers "+
						"(ADR-067 D7)", name)
				assert.NotEmptyf(t, got.Description(),
					"%q must carry a description — this registry exists to describe capabilities", name)
			}

			assert.Equalf(t, len(knowledgeToolNames), counts.knowledge,
				"the knowledge family count must equal the tools pkg/knowledge builds; a "+
					"silently skipped duplicate would otherwise be invisible")
			assert.Greaterf(t, counts.system, 0,
				"the system.* family must still be registered — this test must fail if the "+
					"knowledge family were added by DELETING another one")
			assert.Greaterf(t, counts.general, 0, "the general builtin family must still be registered")
			assert.Greaterf(t, counts.browser, 0, "the browser.* family must still be registered")
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Registered, for every agent (FR-050/FR-051).
// ---------------------------------------------------------------------------

// TestKnowledgeTools_AllNineRegisteredForEveryAgent is the direct answer to
// the gap this unit was created for: before it, no agent's execution registry
// contained any of them and nothing anywhere reported that.
//
// ALL NINE, not the retrieval pair. This test used to check
// knowledgeRetrievalToolNames only, and that is precisely how the seven
// AUTHORING tools shipped fully implemented, fully unit-tested and
// constructed by nothing: knowledge.AuthoringTools had no non-test caller
// anywhere in the tree, while pkg/config/defaults.go and
// pkg/coreagent/core.go seeded each of the seven an explicit allow/ask
// posture — a granted posture over a tool no registry offered. A test that
// asserts registration of a NAMED SUBSET cannot notice the rest going
// missing, which is the same shape as a wildcard policy: it looks like
// coverage and it is a hole.
//
// Registration is unconditional for EVERY seeded agent, including the ones
// D17 denies. That is Constraint #6's shape, not an oversight: what an agent
// may do is decided by an explicit policy entry, never by whether the tool
// was registered at all. A conditionally-registered tool cannot be granted by
// an operator afterwards, because GET /agents/{id}/tools lists this very
// registry.
func TestKnowledgeTools_AllNineRegisteredForEveryAgent(t *testing.T) {
	al, _, _ := kwLoop(t)

	agentIDs := al.GetRegistry().ListAgentIDs()
	require.NotEmpty(t, agentIDs, "the seeded roster must register at least one agent")

	for _, agentID := range agentIDs {
		inst, ok := al.GetRegistry().GetAgent(agentID)
		require.Truef(t, ok, "agent %q listed but not resolvable", agentID)
		for _, name := range knowledgeToolNames {
			_, found := inst.Tools.Get(name)
			assert.Truef(t, found,
				"agent %q has no %q in its execution registry. That registry is the only "+
					"thing a turn dispatches through and the only thing GET /agents/{id}/tools "+
					"reads, so an absent tool is uncallable AND ungrantable — the exact state "+
					"pkg/knowledge shipped in (ADR-067 FR-050/FR-051, FR-100..FR-108)",
				agentID, name)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. Reachable at runtime, not merely present in a map (D17).
// ---------------------------------------------------------------------------

// TestKnowledgeRetrievalTools_SurviveTheTurnsPolicyFilter is the difference
// between "in a registry" and "callable".
//
// tools.FilterToolsByPolicy is the single primitive the turn engine uses to
// decide which tools are sent to the model and the gateway uses to gate
// execution (pkg/tools/compositor.go). A registered tool that this function
// drops is invisible to the model forever, and nothing logs it.
//
// Both halves are required:
//
//   - POSITIVE: D17 seeds retrieval "allow" for all four base agents, so both
//     tools must come through for each of them.
//   - NEGATIVE CONTROL: D17 seeds the Worker an explicit deny, so the same
//     tools must be REGISTERED for it and FILTERED OUT. Without this half the
//     positive half would still pass against a filter that returns its input
//     unchanged — i.e. against no filtering at all.
func TestKnowledgeRetrievalTools_SurviveTheTurnsPolicyFilter(t *testing.T) {
	al, _, _ := kwLoop(t)

	filtered := func(agentID string) map[string]bool {
		inst, ok := al.GetRegistry().GetAgent(agentID)
		require.Truef(t, ok, "agent %q must be registered", agentID)
		kept, _ := tools.FilterToolsByPolicy(
			inst.Tools.GetAll(), inst.AgentType, inst.LoadToolPolicy())
		out := make(map[string]bool, len(kept))
		for _, tool := range kept {
			out[tool.Name()] = true
		}
		return out
	}

	t.Run("D17 allow: the four base agents can call both", func(t *testing.T) {
		for _, agentID := range d17RetrievalAllowAgents {
			survivors := filtered(agentID)
			for _, name := range knowledgeRetrievalToolNames {
				assert.Truef(t, survivors[name],
					"agent %q registers %q but the turn's policy filter drops it, so the model "+
						"is never offered it. ADR-067 D17 seeds retrieval \"allow\" for all four "+
						"base agents; under strictest-wins a tighter global ceiling in "+
						"pkg/config/defaults.go overrules the per-agent seed and the grant is "+
						"dead on every install while the seed still reads correctly",
					agentID, name)
			}
		}
	})

	t.Run("negative control: the Worker's seeded deny actually bites", func(t *testing.T) {
		workerID := routing.NormalizeAgentID(string(coreagent.IDWorker))
		inst, ok := al.GetRegistry().GetAgent(workerID)
		require.Truef(t, ok,
			"the seeded Worker (%q) must exist — without it this control measures nothing",
			workerID)

		for _, name := range knowledgeRetrievalToolNames {
			_, registered := inst.Tools.Get(name)
			require.Truef(t, registered,
				"%q must still be REGISTERED for the Worker — Constraint #6 decides access by "+
					"policy, never by conditional registration", name)
		}

		survivors := filtered(workerID)
		for _, name := range knowledgeRetrievalToolNames {
			assert.Falsef(t, survivors[name],
				"the Worker's D17 deny for %q did not remove it from the filtered set. If this "+
					"fails while the positive half above passes, the filter is not filtering and "+
					"that half proves nothing", name)
		}
	})
}

// TestKnowledgeTools_EveryRegisteredKnowledgeToolHasAnExplicitPolicyEntry is
// Constraint #6 / D17 checked from the REGISTRY side rather than the seed
// side, which is the direction that catches the failure nobody sees.
//
// The seed-side test (knowledge_tool_policy_seed_test.go) asks "does the seed
// contain what D17 says?". This asks the inverse and stricter question: "for
// every knowledge tool an agent can actually reach, does an explicit,
// literal, wildcard-free entry exist for that (agent, tool) pair?".
//
// The inverse is the one that fails when a tool is ADDED. A newly registered
// knowledge tool with no seeded entry does not abort boot, whatever the ADR
// once claimed: repairAndValidateToolPolicyCoverage (gateway.go) repairs to
// DENY and only then validates, so the gateway starts normally, the tool is
// silently unavailable, and one WARN line in a log nobody is reading is the
// entire signal.
func TestKnowledgeTools_EveryRegisteredKnowledgeToolHasAnExplicitPolicyEntry(t *testing.T) {
	al, _, cfg := kwLoop(t)

	// Index the seeded per-agent policy maps by the key the registry keys
	// agents under, so a config id spelled differently from its registry id
	// cannot make a missing entry look present (or vice versa).
	seeded := make(map[string]map[string]config.ToolPolicy, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.Tools == nil {
			continue
		}
		seeded[routing.NormalizeAgentID(ac.ID)] = ac.Tools.Builtin.Policies
	}

	global := cfg.Sandbox.ToolPolicies
	require.NotEmpty(t, global, "the global tool-policy ceiling must be seeded")

	checked := 0
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		inst, ok := al.GetRegistry().GetAgent(agentID)
		require.True(t, ok)

		for _, tool := range inst.Tools.GetAll() {
			name := tool.Name()
			if !strings.HasPrefix(name, "knowledge_") {
				continue
			}
			checked++

			assert.NotContainsf(t, name, "*",
				"a static builtin's policy key must be literal; %q is not", name)

			_, hasAgentEntry := seeded[agentID][name]
			_, hasGlobalEntry := global[name]
			assert.Truef(t, hasAgentEntry || hasGlobalEntry,
				"agent %q can reach registered tool %q, but NEITHER its own policy map "+
					"(pkg/coreagent/core.go) NOR the global ceiling (pkg/config/defaults.go) "+
					"carries an explicit entry for it. Nothing fails loudly for this: the load "+
					"path repairs the gap to DENY and then validates, so boot succeeds and the "+
					"tool is simply dead (ADR-067 D17, FR-071; CLAUDE.md Constraint #6)",
				agentID, name)
		}
	}

	require.NotZerof(t, checked,
		"no knowledge_* tool was found in ANY agent's registry, so this test asserted "+
			"nothing at all — that is the ADR-067 gap itself, not a passing test")
}

// ---------------------------------------------------------------------------
// 4. US-9 (P0): scoped to the CALLING AGENT'S workspace, end to end.
// ---------------------------------------------------------------------------

// TestKnowledgeRetrieval_ScopedToCallingAgentsWorkspace is spec test 26's
// registration-layer half — FR-052 (retrieval is scoped to the calling
// agent's workspace) and FR-053 (an out-of-scope collection is an EMPTY
// RESULT, never a permission error, because a 403 confirms the collection
// exists).
//
// pkg/knowledge already proves ResolveScope isolates correctly. What can only
// be proven HERE is that the instance registered into a live agent's registry
// was handed a real $OMNIPUS_HOME, so that scoping runs against the actual
// installation. A tool wired with an empty Home resolves an empty scope and
// would satisfy every negative assertion below perfectly while being
// completely broken — which is why each negative comes paired with a positive
// control that only a correctly-wired Home can pass.
func TestKnowledgeRetrieval_ScopedToCallingAgentsWorkspace(t *testing.T) {
	al, home, _ := kwLoop(t)

	hostParent := kwReal(t, t.TempDir())
	vaultA := kwVault(t, filepath.Join(hostParent, "vault-a"), "Alpha Vault")
	vaultB := kwVault(t, filepath.Join(hostParent, "vault-b"), "Beta Vault")
	kwNote(t, vaultA, "Roadmap.md", "# Roadmap\n\nQuarterly roadmap for the alpha team.\n")
	kwNote(t, vaultB, "Roadmap.md", "# Roadmap\n\nQuarterly roadmap for the beta team.\n")

	wsA := kwWorkspace(t, home, "kw-ws-alpha")
	wsB := kwWorkspace(t, home, "kw-ws-beta")
	_, _, err := workspace.CreateMount(home, wsA, "notes", vaultA)
	require.NoError(t, err)
	_, _, err = workspace.CreateMount(home, wsB, "notes", vaultB)
	require.NoError(t, err)

	const caller = string(coreagent.IDMia)

	t.Run("search: each workspace sees its own collection", func(t *testing.T) {
		// Positive control for BOTH sides. If the registered instance had no
		// usable Home, both of these would come back empty and every negative
		// assertion in this test would still pass.
		gotA := kwDecodeSearch(t, kwExec(t, al, caller, "knowledge_search", wsA,
			map[string]any{"query": "roadmap"}))
		assert.Equal(t, "Alpha Vault", gotA.Collection,
			"an agent in workspace A must resolve A's own mounted knowledge base; an empty "+
				"collection here means the registered tool has no usable $OMNIPUS_HOME and "+
				"every isolation assertion below is vacuous")
		assert.Equal(t, []string{"Alpha Vault"}, gotA.CollectionsInScope)

		gotB := kwDecodeSearch(t, kwExec(t, al, caller, "knowledge_search", wsB,
			map[string]any{"query": "roadmap"}))
		assert.Equal(t, "Beta Vault", gotB.Collection)
		assert.Equal(t, []string{"Beta Vault"}, gotB.CollectionsInScope)
	})

	t.Run("search: another workspace's collection is not addressable by name", func(t *testing.T) {
		res := kwExec(t, al, caller, "knowledge_search", wsB,
			map[string]any{"query": "roadmap", "collection": "Alpha Vault"})

		// FR-053 / MV-12: the refusal must not be a refusal. An error — or a
		// 403-shaped answer — would confirm that "Alpha Vault" exists, which
		// is itself the disclosure US-9 forbids.
		assert.False(t, res.IsError,
			"an out-of-scope collection must produce an EMPTY RESULT, never an error: an "+
				"error confirms the collection exists (ADR-067 FR-053, MV-12)")

		got := kwDecodeSearch(t, res)
		assert.Empty(t, got.Collection,
			"workspace B resolved workspace A's knowledge base — this is US-9 (P0), the "+
				"cross-workspace read")
		assert.Zero(t, got.ResultCount)
		assert.Equal(t, "unavailable", got.IndexState)
		assert.NotContains(t, got.CollectionsInScope, "Alpha Vault",
			"the hint listing addressable collections must never name another workspace's")
	})

	t.Run("search: nor by its absolute path", func(t *testing.T) {
		got := kwDecodeSearch(t, kwExec(t, al, caller, "knowledge_search", wsB,
			map[string]any{"query": "roadmap", "collection": vaultA}))
		assert.Empty(t, got.Collection,
			"naming the collection by its real path bypassed the workspace scope (US-9 AS-2)")
		assert.Zero(t, got.ResultCount)
	})

	t.Run("search: an agent on no workspace can address nothing", func(t *testing.T) {
		got := kwDecodeSearch(t, kwExec(t, al, caller, "knowledge_search", "",
			map[string]any{"query": "roadmap"}))
		assert.Empty(t, got.Collection,
			"an empty workspace id must narrow to NO collection. Treating \"no workspace\" as "+
				"\"no restriction\" is the unscoped search US-9 forbids, and it looks like a "+
				"working feature")
		assert.Empty(t, got.CollectionsInScope)
	})

	t.Run("graph: scoped the same way as search", func(t *testing.T) {
		// Positive control: orphans needs no index, so it exercises the whole
		// scope → collection → link-graph path on a freshly mounted vault.
		gotA := kwDecodeGraph(t, kwExec(t, al, caller, "knowledge_graph", wsA,
			map[string]any{"operation": knowledge.GraphOpOrphans}))
		assert.Equal(t, "Alpha Vault", gotA.Collection)
		assert.Contains(t, gotA.Nodes, "Roadmap.md",
			"the only note in A's vault is linked from nothing, so it is an orphan; a missing "+
				"node here means the graph never reached the real collection")

		gotB := kwDecodeGraph(t, kwExec(t, al, caller, "knowledge_graph", wsB,
			map[string]any{"operation": knowledge.GraphOpOrphans, "collection": "Alpha Vault"}))
		assert.Empty(t, gotB.Collection,
			"knowledge_graph resolved another workspace's collection (FR-052/US-9)")
		assert.Zero(t, gotB.Count)
		assert.NotContains(t, gotB.CollectionsInScope, "Alpha Vault")
	})
}
