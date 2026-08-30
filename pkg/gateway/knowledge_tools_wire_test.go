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
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
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

// kwIndex opens and fully syncs the text index for a collection root, the
// same OpenIndex + SyncWith pair KnowledgeLifecycle.AttachMount performs in
// production. Tests that exercise a tool's own text search need this — a
// mount alone does not index anything.
func kwIndex(t *testing.T, home, collectionRoot string, notePaths ...string) {
	t.Helper()
	ix, err := knowledge.OpenIndex(home, collectionRoot)
	require.NoError(t, err)
	defer func() { require.NoError(t, ix.Close()) }()
	_, err = ix.SyncWith(context.Background(), knowledge.SyncOptions{})
	require.NoError(t, err)

	// The properties index (pkg/records/propindex) is a SEPARATE store from
	// the bleve text index SyncWith just built, and knowledge_find's
	// findRecords requires it to be open and non-empty even for a bare
	// `words` query with no typed filter — it is what enumerates the
	// candidate population to text-search within, not only typed metadata.
	// Nothing in production wires a writer for it yet (propindex.IndexNote/
	// Store.UpsertNote currently have no caller outside pkg/vaultprops'
	// readers and this test file — flagged separately as a real gap, not
	// something this test works around by lowering its own bar). Until that
	// orchestrator exists, a test that wants to exercise knowledge_find's
	// CONTENT path has to populate it directly, the same shape a real
	// indexer will eventually write.
	dir, err := knowledge.IndexDirFor(home, collectionRoot)
	require.NoError(t, err)
	propPath, err := knowledge.PropertiesIndexPath(home, collectionRoot)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	store, err := propindex.Open(context.Background(), propPath, propindex.Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	for _, p := range notePaths {
		require.NoError(t, store.UpsertNote(context.Background(), propindex.NoteRows{
			Path: p, Kind: propindex.KindNote,
		}))
	}
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

	for _, name := range knowledgeToolNames {
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

// TestKnowledgeTools_AllSixRegisteredForEveryAgent is the direct answer to
// the gap this unit was created for: before ADR-067's tools were registered,
// no agent's execution registry contained any of them and nothing anywhere
// reported that. ADR-068 D15.3 supersedes that family with six tools, and
// this asserts the same reachability guarantee for the six that now ship.
//
// ALL SIX, not a subset. A test that asserts registration of a NAMED SUBSET
// cannot notice the rest going missing, which is the same shape as a
// wildcard policy: it looks like coverage and it is a hole — exactly how
// ADR-067's seven authoring tools shipped fully implemented, fully
// unit-tested and constructed by nothing (knowledge.AuthoringTools had no
// non-test caller anywhere in the tree) while carrying a seeded allow/ask
// posture nobody could ever exercise.
//
// Registration is unconditional for EVERY seeded agent, including the ones
// the seed denies. That is Constraint #6's shape, not an oversight: what an
// agent may do is decided by an explicit policy entry, never by whether the
// tool was registered at all. A conditionally-registered tool cannot be
// granted by an operator afterwards, because GET /agents/{id}/tools lists
// this very registry.
func TestKnowledgeTools_AllSixRegisteredForEveryAgent(t *testing.T) {
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

// knowledgeReadToolNames / knowledgeWriteToolNames split ADR-068 D15.3's six
// by blast radius, matching pkg/coreagent/core.go's actual seed axis —
// superseding this file's old retrieval/authoring split.
var (
	knowledgeReadToolNames  = []string{"knowledge_describe", "knowledge_find", "knowledge_read"}
	knowledgeWriteToolNames = []string{"knowledge_edit", "knowledge_restructure", "knowledge_configure"}
)

// d15AllBaseAgents is every base agent — the read tier is "allow" for all
// four under ADR-068 D15.3.
var d15AllBaseAgents = []string{
	string(coreagent.IDMia),
	string(coreagent.IDJim),
	string(coreagent.IDAva),
	string(coreagent.IDRay),
}

// TestKnowledgeTools_SurviveTheTurnsPolicyFilter is the difference between
// "in a registry" and "callable".
//
// tools.FilterToolsByPolicy is the single primitive the turn engine uses to
// decide which tools are sent to the model and the gateway uses to gate
// execution (pkg/tools/compositor.go). A registered tool that this function
// drops is invisible to the model forever, and nothing logs it.
//
// Three halves:
//
//   - POSITIVE (read): D15.3 seeds the read tier "allow" for all four base
//     agents, so all three read tools must come through for each of them.
//   - POSITIVE (write, Jim only): Jim is the one agent seeded "allow" on the
//     three write tools — his deliberate exception (see
//     pkg/coreagent/core.go's IDJim case).
//   - NEGATIVE CONTROL: the Worker is seeded an explicit deny on all six, so
//     every one of them must be REGISTERED for it and FILTERED OUT. Without
//     this half the positive halves would still pass against a filter that
//     returns its input unchanged — i.e. against no filtering at all.
func TestKnowledgeTools_SurviveTheTurnsPolicyFilter(t *testing.T) {
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

	t.Run("D15.3 read allow: every base agent can call all three", func(t *testing.T) {
		for _, agentID := range d15AllBaseAgents {
			survivors := filtered(agentID)
			for _, name := range knowledgeReadToolNames {
				assert.Truef(t, survivors[name],
					"agent %q registers %q but the turn's policy filter drops it, so the model "+
						"is never offered it. ADR-068 D15.3 seeds the read tier \"allow\" for all "+
						"four base agents; under strictest-wins a tighter global ceiling in "+
						"pkg/config/defaults.go overrules the per-agent seed and the grant is "+
						"dead on every install while the seed still reads correctly",
					agentID, name)
			}
		}
	})

	t.Run("D15.3 write allow: Jim's deliberate exception actually resolves", func(t *testing.T) {
		survivors := filtered(string(coreagent.IDJim))
		for _, name := range knowledgeWriteToolNames {
			assert.Truef(t, survivors[name],
				"Jim registers %q but the turn's policy filter drops it. He is seeded \"allow\" "+
					"on the three write tools (pkg/coreagent/core.go's IDJim case) precisely "+
					"because an \"ask\" gate would protect nothing for an agent who already holds "+
					"unprompted bash — if this fails, that deliberate exception is dead", name)
		}
	})

	t.Run("negative control: the Worker's seeded deny actually bites", func(t *testing.T) {
		workerID := routing.NormalizeAgentID(string(coreagent.IDWorker))
		inst, ok := al.GetRegistry().GetAgent(workerID)
		require.Truef(t, ok,
			"the seeded Worker (%q) must exist — without it this control measures nothing",
			workerID)

		for _, name := range knowledgeToolNames {
			_, registered := inst.Tools.Get(name)
			require.Truef(t, registered,
				"%q must still be REGISTERED for the Worker — Constraint #6 decides access by "+
					"policy, never by conditional registration", name)
		}

		survivors := filtered(workerID)
		for _, name := range knowledgeToolNames {
			assert.Falsef(t, survivors[name],
				"the Worker's D15.3 deny for %q did not remove it from the filtered set. If "+
					"this fails while the positive halves above pass, the filter is not "+
					"filtering and those halves prove nothing", name)
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

// TestKnowledgeTools_ScopedToCallingAgentsWorkspace is spec test 26's
// registration-layer half, re-run against ADR-068's six — FR-052 (retrieval
// is scoped to the calling agent's workspace) and FR-053 (an out-of-scope
// collection must never CONFIRM the collection exists elsewhere).
//
// pkg/knowledge already proves ResolveScope isolates correctly. What can
// only be proven HERE is that the instance registered into a live agent's
// registry was handed a real $OMNIPUS_HOME, so that scoping runs against the
// actual installation. A tool wired with an empty Home resolves an empty
// scope and would satisfy every negative assertion below perfectly while
// being completely broken — which is why each negative comes paired with a
// positive control that only a correctly-wired Home can pass.
//
// knowledge_describe carries the SAME `collection` argument SearchTool did,
// so it is the direct successor for the name/path bypass assertions below.
// Unlike SearchTool, an out-of-scope collection is a refusal (IsError=true)
// rather than a 200-with-empty-result — DescribeTool's own FR-024 posture —
// but the SAME non-disclosure property holds: the refusal text is identical
// whether "Alpha Vault" exists in another workspace or does not exist at
// all, so nothing is confirmed either way. knowledge_find carries no
// `collection` argument at all (it always resolves the workspace's one
// mounted collection), so it is exercised separately below for the
// simpler "which workspace's notes does a real query actually reach"
// question.
func TestKnowledgeTools_ScopedToCallingAgentsWorkspace(t *testing.T) {
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

	// kwLoop builds a plain AgentLoop, not a full gateway boot — there is no
	// KnowledgeLifecycle here to index a vault on mount the way the real
	// mount-creation REST handler does. knowledge_find answers from the text
	// index, so without this the "find" sub-test below would see an
	// honestly-empty, never-built index and prove nothing about content
	// actually being reached.
	kwIndex(t, home, vaultA, "Roadmap.md")
	kwIndex(t, home, vaultB, "Roadmap.md")

	const caller = string(coreagent.IDMia)

	t.Run("describe: each workspace sees its own collection", func(t *testing.T) {
		// Positive control for BOTH sides. If the registered instance had no
		// usable Home, both of these would come back empty and every negative
		// assertion below would still pass.
		resA := kwExec(t, al, caller, "knowledge_describe", wsA, map[string]any{})
		require.Falsef(t, resA.IsError, "workspace A's own describe must not error — got %q", resA.ForLLM)
		assert.Containsf(t, resA.ForLLM, "Alpha Vault",
			"an agent in workspace A must resolve A's own mounted knowledge base; its absence "+
				"here means the registered tool has no usable $OMNIPUS_HOME and every isolation "+
				"assertion below is vacuous")
		assert.NotContains(t, resA.ForLLM, "Beta Vault")

		resB := kwExec(t, al, caller, "knowledge_describe", wsB, map[string]any{})
		require.False(t, resB.IsError)
		assert.Contains(t, resB.ForLLM, "Beta Vault")
		assert.NotContains(t, resB.ForLLM, "Alpha Vault")
	})

	t.Run("describe: another workspace's collection is not addressable by name", func(t *testing.T) {
		gotAlpha := kwExec(t, al, caller, "knowledge_describe", wsB, map[string]any{"collection": "Alpha Vault"})
		require.True(t, gotAlpha.IsError, "an unresolvable collection name must be refused")

		// FR-053's non-disclosure property, re-expressed for DescribeTool's
		// refusal shape: the message must be the SAME whether the requested
		// name exists in another workspace or does not exist anywhere at
		// all, so an out-of-scope name is never confirmed to exist. Compared
		// against a name that is guaranteed not to exist ANYWHERE, rather
		// than merely asserting "Alpha Vault" is absent from the text —
		// which the earlier draft of this assertion got wrong: the refusal
		// legitimately names workspace B's OWN addressable collections
		// ("in scope: Beta Vault") as the remedy, and that is not a leak.
		gotNonexistent := kwExec(t, al, caller, "knowledge_describe", wsB,
			map[string]any{"collection": "Definitely Nonexistent Vault Qwerty"})
		require.True(t, gotNonexistent.IsError)
		wantAlpha := strings.Replace(gotNonexistent.ForLLM, "Definitely Nonexistent Vault Qwerty", "Alpha Vault", 1)
		assert.Equal(t, wantAlpha, gotAlpha.ForLLM,
			"the refusal for a real collection mounted in ANOTHER workspace must read "+
				"identically to the refusal for a name that exists nowhere at all — any "+
				"difference is a disclosure that Alpha Vault exists somewhere")
	})

	t.Run("describe: nor by its absolute path", func(t *testing.T) {
		res := kwExec(t, al, caller, "knowledge_describe", wsB, map[string]any{"collection": vaultA})
		require.True(t, res.IsError,
			"naming the collection by its real path must not bypass the workspace scope (US-9 AS-2)")
	})

	t.Run("describe: an agent on no workspace can address nothing", func(t *testing.T) {
		res := kwExec(t, al, caller, "knowledge_describe", "", map[string]any{})
		require.True(t, res.IsError,
			"an empty workspace id must narrow to NO collection. Treating \"no workspace\" as "+
				"\"no restriction\" is the unscoped access US-9 forbids, and it looks like a "+
				"working feature")
		assert.NotContains(t, res.ForLLM, "Alpha Vault")
		assert.NotContains(t, res.ForLLM, "Beta Vault")
	})

	t.Run("find: scoped the same way, with no collection argument to bypass at all", func(t *testing.T) {
		resA := kwExec(t, al, caller, "knowledge_find", wsA, map[string]any{"words": "roadmap"})
		require.Falsef(t, resA.IsError, "workspace A's own find must not error — got %q", resA.ForLLM)
		assert.Containsf(t, resA.ForLLM, "Roadmap.md",
			"the only note in A's vault must be found from A's own workspace; its absence "+
				"here means knowledge_find never reached the real collection")

		resB := kwExec(t, al, caller, "knowledge_find", wsB, map[string]any{"words": "roadmap"})
		require.False(t, resB.IsError)
		assert.Contains(t, resB.ForLLM, "Roadmap.md")

		resEmpty := kwExec(t, al, caller, "knowledge_find", "", map[string]any{"words": "roadmap"})
		require.True(t, resEmpty.IsError,
			"an agent on no workspace can address no collection at all for knowledge_find")
	})
}
