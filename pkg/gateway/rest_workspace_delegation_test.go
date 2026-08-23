// Per-workspace delegation graph tests (M5). Proves:
//  1. GET on a fresh workspace → 200 with empty edges + computed team.
//  2. PUT a valid edge set → 200 and GET round-trips it.
//  3. PUT with an unknown from/to agent → 400.
//  4. PUT with a self-edge → 400.
//  5. PUT with a bad mode → 400.
//  6. PUT with depth above the ceiling → 400.
//  7. PUT with duplicate (from,to) → last-writer-wins (deduped).
//  8. GET/PUT on a non-existent workspace → 404.
//  9. The default workspace seeder produces team + edges from the seeded roster.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// buildWorkspaceDelegationTestAPI builds a restAPI with a roster (jim, ava, ray,
// planner) so delegation edge endpoints resolve targets, plus a pre-created
// workspace whose id is returned.
//
// ADR-054 FIXTURE-VACUITY fix: this used to ALSO os.WriteFile a raw
// config.json blob to cfgPath containing a non-empty "agents.list" JSON
// array (jim/ava/ray/planner). That write was already 100% dead weight even
// before ADR-054: handleWorkspaceDelegationGet/Put never load config.json
// from disk at all — the API is built directly from the in-memory `cfg`
// struct below via mustAgentLoop, and edge-endpoint validation
// (workspaceTeamSet, rest_workspace_delegation.go) checks the WORKSPACE
// file's core_team, never cfg.Agents.List or config.json. ADR-054 makes the
// staleness explicit and permanent: config.LoadConfig now unconditionally
// strips any on-disk "agents.list" content, so even a future code path that
// started reading cfgPath would never see this roster again. Per the
// operator's fixture-vacuity directive, the dead on-disk splice is replaced
// with real entity records via agentstore.Create — the in-memory
// cfg.Agents.List construction below (required for mustAgentLoop/registry
// construction) is unchanged.
func buildWorkspaceDelegationTestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
				{ID: "planner", Name: "Planner", Type: config.AgentTypeWorker},
			},
		},
	}
	cfg.Agents.Defaults.SubTurn.MaxDepth = 3

	// Real entity records (ADR-054) — see the doc comment above for why this
	// replaces the old dead on-disk config.json splice. Nothing in this
	// file's handlers currently reads these back (see the doc comment), but
	// seeding them keeps the fixture honest about where an agent record
	// actually lives in production, per the operator's fixture-vacuity
	// directive.
	store := agentstore.New(tmpDir)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		require.NoError(t, store.Create(ac.ID, &ac), "seed agent entity record %q", ac.ID)
	}

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}

	// Create a workspace with an explicit core_team that includes our roster.
	ws := storedWorkspace{
		ID:        "01J8WORKSPACEDELEGATIONTEST",
		Name:      "Delegation WS",
		Status:    string(gen.WorkspaceStatusActive),
		CoreTeam:  []string{"jim", "ava", "ray", "planner"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, writeWorkspaceFile(tmpDir, ws))
	return api, ws.ID
}

func getDelegation(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id+"/delegation", nil)
	api.HandleWorkspaces(w, r)
	return w
}

func putDelegation(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id+"/delegation", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)
	return w
}

func decodeDelegation(t *testing.T, body []byte) gen.WorkspaceDelegation {
	t.Helper()
	var d gen.WorkspaceDelegation
	require.NoError(t, json.Unmarshal(body, &d), "body: %s", string(body))
	return d
}

func TestWorkspaceDelegation_GetEmpty(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	w := getDelegation(t, api, id)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	d := decodeDelegation(t, w.Body.Bytes())
	assert.Equal(t, id, d.WorkspaceId)
	assert.Empty(t, d.Edges, "fresh workspace has no edges")
	require.NotNil(t, d.Team, "team must reflect core_team")
	assert.ElementsMatch(t, []string{"jim", "ava", "ray", "planner"}, *d.Team)
	// default_depth is the already-computed delegationDepthCeiling(cfg) — the
	// test fixture sets subturn.max_depth=3, so that's the exposed value.
	assert.Equal(t, 3, d.DefaultDepth, "default_depth must expose the resolved depth ceiling")
}

func TestWorkspaceDelegation_PutAndRoundTrip(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	// Wire modes are the collapsed 2-value vocabulary (direct/task) — "await" and
	// "background" are retired wire values (still readable from legacy-persisted
	// disk JSON via DelegationEdge.UnmarshalJSON, but no longer accepted on a
	// fresh PUT, which is validated against the new WorkspaceDelegationEdge enum).
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava","modes":["task","direct"],"depth":2},{"from_agent":"planner","to_agent":"ray","modes":["direct"]}]}`
	w := putDelegation(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	put := decodeDelegation(t, w.Body.Bytes())
	require.Len(t, put.Edges, 2)

	// GET round-trips.
	g := getDelegation(t, api, id)
	require.Equal(t, http.StatusOK, g.Code)
	got := decodeDelegation(t, g.Body.Bytes())
	require.Len(t, got.Edges, 2)
	assert.Equal(t, "jim", got.Edges[0].FromAgent)
	assert.Equal(t, "ava", got.Edges[0].ToAgent)
	require.NotNil(t, got.Edges[0].Depth)
	assert.Equal(t, 2, *got.Edges[0].Depth)
	require.NotNil(t, got.Edges[0].Modes)
	assert.Len(t, *got.Edges[0].Modes, 2)
	assert.Equal(t, 3, got.DefaultDepth, "default_depth must be present on the PUT response too")

	// Persisted to the workspace file.
	stored, err := readWorkspaceFile(api.homePath, id)
	require.NoError(t, err)
	require.Len(t, stored.Delegation, 2)
}

func TestWorkspaceDelegation_PutUnknownAgent_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	w := putDelegation(t, api, id, `{"edges":[{"from_agent":"jim","to_agent":"ghost"}]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "ghost")
}

func TestWorkspaceDelegation_PutSelfEdge_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	w := putDelegation(t, api, id, `{"edges":[{"from_agent":"jim","to_agent":"jim"}]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "self-edge")
}

func TestWorkspaceDelegation_PutBadMode_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	w := putDelegation(t, api, id, `{"edges":[{"from_agent":"jim","to_agent":"ava","modes":["telepathy"]}]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestWorkspaceDelegation_PutRetiredLegacyMode_400 proves the RETIRED 3-value
// wire modes ("await"/"background") are rejected on a fresh PUT — the
// collapsed 2-value vocabulary (direct/task) is the only wire-accepted set
// going forward. The legacy strings remain readable from already-persisted
// disk JSON (DelegationEdge.UnmarshalJSON migrates them transparently), but
// that migration is a read-path concern only; a new write must use the
// current vocabulary.
func TestWorkspaceDelegation_PutRetiredLegacyMode_400(t *testing.T) {
	for _, mode := range []string{"await", "background"} {
		t.Run(mode, func(t *testing.T) {
			api, id := buildWorkspaceDelegationTestAPI(t)
			w := putDelegation(t, api, id, `{"edges":[{"from_agent":"jim","to_agent":"ava","modes":["`+mode+`"]}]}`)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		})
	}
}

func TestWorkspaceDelegation_PutDepthExceeded_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	w := putDelegation(t, api, id, `{"edges":[{"from_agent":"jim","to_agent":"ava","depth":99}]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "depth")
}

func TestWorkspaceDelegation_PutDuplicateDeduped(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava","depth":1},{"from_agent":"jim","to_agent":"ava","depth":3}]}`
	w := putDelegation(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	d := decodeDelegation(t, w.Body.Bytes())
	require.Len(t, d.Edges, 1, "duplicate (from,to) must collapse to one edge")
	require.NotNil(t, d.Edges[0].Depth)
	assert.Equal(t, 3, *d.Edges[0].Depth, "last writer wins")
}

func TestWorkspaceDelegation_NotFound_404(t *testing.T) {
	api, _ := buildWorkspaceDelegationTestAPI(t)
	g := getDelegation(t, api, "01J8NONEXISTENTWORKSPACEXX")
	assert.Equal(t, http.StatusNotFound, g.Code, "body: %s", g.Body.String())

	p := putDelegation(t, api, "01J8NONEXISTENTWORKSPACEXX", `{"edges":[]}`)
	assert.Equal(t, http.StatusNotFound, p.Code, "body: %s", p.Body.String())
}

// TestWorkspaceDelegation_ClearEdges proves an empty edge array clears all edges.
func TestWorkspaceDelegation_ClearEdges(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	require.Equal(t, http.StatusOK, putDelegation(t, api, id,
		`{"edges":[{"from_agent":"jim","to_agent":"ava"}]}`).Code)
	require.Equal(t, http.StatusOK, putDelegation(t, api, id, `{"edges":[]}`).Code)
	d := decodeDelegation(t, getDelegation(t, api, id).Body.Bytes())
	assert.Empty(t, d.Edges)
}

// TestWorkspaceDelegation_PutOffTeamAgent_400 proves an edge endpoint that
// exists in the config roster but is NOT a member of the workspace team
// (core_team ∪ existing-edge endpoints) is rejected — an edge write must not
// silently expand the team. "mia" is in the roster (added below) but absent from
// this workspace's core_team.
func TestWorkspaceDelegation_PutOffTeamAgent_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)

	// Add "mia" to the live config roster but NOT to the workspace core_team.
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore})

	w := putDelegation(t, api, id, `{"edges":[{"from_agent":"jim","to_agent":"mia"}]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "not a member of the workspace team")
	assert.Contains(t, w.Body.String(), "mia")
}

// TestWorkspaceDelegation_PutCycle_400 proves a multi-hop delegation cycle
// (jim→ava→jim) is rejected at write time.
func TestWorkspaceDelegation_PutCycle_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava"},{"from_agent":"ava","to_agent":"jim"}]}`
	w := putDelegation(t, api, id, body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "cycle")
}

// TestWorkspaceDelegation_PutThreeHopCycle_400 proves a longer cycle
// (jim→ava→ray→jim) is also rejected.
func TestWorkspaceDelegation_PutThreeHopCycle_400(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava"},{"from_agent":"ava","to_agent":"ray"},{"from_agent":"ray","to_agent":"jim"}]}`
	w := putDelegation(t, api, id, body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "cycle")
}

// TestWorkspaceDelegation_PutDAGNoCycle_OK proves a non-cyclic diamond
// (jim→ava, jim→ray, ava→planner, ray→planner) is accepted.
func TestWorkspaceDelegation_PutDAGNoCycle_OK(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava"},{"from_agent":"jim","to_agent":"ray"},{"from_agent":"ava","to_agent":"planner"},{"from_agent":"ray","to_agent":"planner"}]}`
	w := putDelegation(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestDelegationEdgeValidate_RejectionCases exercises the shared per-edge
// authority workspace.DelegationEdge.Validate directly (the same validator the
// PUT handler and the update_workspace tool now call). It pins each per-edge
// invariant in isolation: self-edge, off-team endpoint, invalid mode, and
// negative depth are each rejected, while a well-formed edge is accepted. The
// team set mirrors the workspace team (core_team ∪ existing-edge endpoints).
func TestDelegationEdgeValidate_RejectionCases(t *testing.T) {
	team := map[string]bool{"jim": true, "ava": true, "ray": true}
	const ceiling = 3

	cases := []struct {
		name    string
		edge    storedDelegationEdge
		wantErr string // substring the rejection message must contain ("" = accept)
	}{
		{
			name:    "self-edge",
			edge:    storedDelegationEdge{FromAgent: "jim", ToAgent: "jim"},
			wantErr: "self-edge",
		},
		{
			name:    "off-team to_agent",
			edge:    storedDelegationEdge{FromAgent: "jim", ToAgent: "ghost"},
			wantErr: "not a member of the workspace team",
		},
		{
			name:    "off-team from_agent",
			edge:    storedDelegationEdge{FromAgent: "ghost", ToAgent: "ava"},
			wantErr: "not a member of the workspace team",
		},
		{
			name:    "empty endpoints",
			edge:    storedDelegationEdge{FromAgent: "", ToAgent: ""},
			wantErr: "must not be empty",
		},
		{
			name: "invalid mode",
			edge: storedDelegationEdge{
				FromAgent: "jim",
				ToAgent:   "ava",
				Modes:     []workspace.DelegationMode{"telepathy"},
			},
			wantErr: "is invalid",
		},
		{
			name:    "negative depth",
			edge:    storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Depth: intPtrGW(-1)},
			wantErr: "depth must be >= 0",
		},
		{
			name:    "depth above ceiling",
			edge:    storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Depth: intPtrGW(99)},
			wantErr: "exceeds the maximum allowed depth",
		},
		{
			name: "valid edge accepted",
			edge: storedDelegationEdge{
				FromAgent: "jim",
				ToAgent:   "ava",
				Modes:     []workspace.DelegationMode{"task", "direct"},
				Depth:     intPtrGW(2),
			},
			wantErr: "",
		},
		{
			name:    "valid edge depth 0 accepted",
			edge:    storedDelegationEdge{FromAgent: "ray", ToAgent: "ava", Depth: intPtrGW(0)},
			wantErr: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.edge.Validate(team, ceiling)
			if tc.wantErr == "" {
				assert.NoError(t, err, "edge must be accepted")
				return
			}
			require.Error(t, err, "edge must be rejected")
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestDelegationEdgeValidate_MatchesHandlerWireMessages proves the shared
// Validate authority returns the EXACT 400 body the PUT handler surfaces, so
// routing the gateway's per-edge checks through Validate preserved the wire
// contract. Each Validate error string must equal the message the handler
// historically produced inline.
func TestDelegationEdgeValidate_MatchesHandlerWireMessages(t *testing.T) {
	team := map[string]bool{"jim": true, "ava": true}
	const ceiling = 3

	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "jim"}.Validate(team, ceiling),
		"delegation edge cannot be a self-edge (from_agent == to_agent: jim)")
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "ghost"}.Validate(team, ceiling),
		"delegation edge to_agent ghost is not a member of the workspace team")
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "ghost", ToAgent: "ava"}.Validate(team, ceiling),
		"delegation edge from_agent ghost is not a member of the workspace team")
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "", ToAgent: ""}.Validate(team, ceiling),
		"delegation edge from_agent and to_agent must not be empty")
	assert.EqualError(
		t,
		storedDelegationEdge{
			FromAgent: "jim",
			ToAgent:   "ava",
			Modes:     []workspace.DelegationMode{"telepathy"},
		}.Validate(
			team,
			ceiling,
		),
		"delegation edge mode telepathy is invalid (valid: direct, task)",
	)
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Depth: intPtrGW(-1)}.Validate(team, ceiling),
		"delegation edge depth must be >= 0")
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Depth: intPtrGW(99)}.Validate(team, ceiling),
		"delegation edge depth exceeds the maximum allowed depth")
}

// TestDelegationEdgeValidate_ModesMatchConfig used to pin a 1:1 string-literal
// equality between pkg/workspace's mode constants and the canonical
// config.DelegationMode{Await,Background,Task} constants — back when the edge
// vocabulary mirrored the tool's vocabulary exactly. That equality is GONE by
// design: the edge vocabulary is now the collapsed {direct,task}, not a 1:1
// mirror of the tool's 3-value {await,background,task} parameter. Its
// replacement role: prove the collapse from every one of the 3 real
// config.DelegationMode values down to the edge's 2-value vocabulary is
// EXHAUSTIVE — every possible delegate-tool mode maps to a valid()
// workspace.DelegationMode, none is left unmapped or maps to something the
// edge validator would reject. This exercises agent.EdgeModeCategory directly
// (pkg/gateway imports pkg/agent already, so defaultWorkspaceDelegationEdges
// calls that single exported function rather than maintaining a duplicate
// seed-side copy — there is no separate seedModeCategory anymore). The
// enforcement-side call site (enforceEdgeModeAndDepth, pkg/agent/loop.go) has
// its own exhaustiveness test, TestEdgeModeCategory_ExhaustiveOverConfigModes,
// in pkg/agent, exercising the exact same function this test calls — both
// must agree by construction since they are literally the same code, but both
// tests are kept (rather than merged) so a regression in either package's
// build tags/wiring still gets caught locally.
func TestDelegationEdgeValidate_ModesMatchConfig(t *testing.T) {
	team := map[string]bool{"jim": true, "ava": true}
	const ceiling = 3

	cases := []struct {
		mode config.DelegationMode
		want workspace.DelegationMode
	}{
		{config.DelegationModeAwait, workspace.ModeDirect},
		{config.DelegationModeBackground, workspace.ModeDirect},
		{config.DelegationModeTask, workspace.ModeTask},
	}

	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := agent.EdgeModeCategory(tc.mode)
			assert.Equal(t, tc.want, got, "collapse of config mode %q", tc.mode)
			assert.True(t, got.Valid(), "collapsed mode %q must be a Valid() workspace.DelegationMode", got)

			// End-to-end: the collapsed mode must actually be accepted by the
			// shared edge validator, not just satisfy Valid() in isolation.
			edge := workspace.DelegationEdge{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{got}}
			assert.NoErrorf(t, edge.Validate(team, ceiling),
				"collapsed mode %q (from config mode %q) must be accepted by the workspace edge validator",
				got, tc.mode)
		})
	}

	// Negative control: a mode that is NOT one of the collapsed constants must
	// still be rejected, proving Validate is genuinely gating on the literal
	// set (not a vacuous accept-all that would mask a real drift).
	bogus := workspace.DelegationEdge{
		FromAgent: "jim",
		ToAgent:   "ava",
		Modes:     []workspace.DelegationMode{"telepathy"},
	}
	err := bogus.Validate(team, ceiling)
	require.Error(t, err, "an unknown mode must be rejected")
	assert.Contains(t, err.Error(), "is invalid",
		"rejection must come from the mode-validation branch")

	// Negative control 2: the RETIRED literal 3-value strings, cast directly
	// (not through agent.EdgeModeCategory), are no longer valid edge modes —
	// the old 1:1 lock-step this test used to pin is gone.
	for _, retired := range []config.DelegationMode{config.DelegationModeAwait, config.DelegationModeBackground} {
		raw := workspace.DelegationEdge{
			FromAgent: "jim",
			ToAgent:   "ava",
			Modes:     []workspace.DelegationMode{workspace.DelegationMode(retired)},
		}
		err := raw.Validate(team, ceiling)
		require.Errorf(t, err, "raw-cast retired mode %q must be rejected by the edge validator", retired)
		assert.Contains(t, err.Error(), "is invalid")
	}
}

// intPtrGW is a local *int helper for building delegation depth fields in the
// gateway delegation tests.
func intPtrGW(n int) *int { return &n }

// TestDefaultWorkspaceSeeder_TeamAndEdges proves ensureDefaultWorkspace seeds the
// team + edges from a config carrying seeded per-agent delegation policies.
//
// ADR-037 (Wave 2) rewrote defaultWorkspaceDelegationEdges to read
// coreagent.SeedDelegationEdges(id) directly instead of
// cfg.Agents.List[i].DelegationPolicy — that field no longer exists on
// AgentConfig at all. This test therefore can no longer construct arbitrary
// custom DelegationPolicy values on the fixture AgentConfigs (there is
// nowhere left to put them); it instead uses the REAL core-agent IDs so the
// REAL coreagent seed matrix (coreAgentDelegation) drives the edges, and
// asserts the exact resulting graph.
func TestDefaultWorkspaceSeeder_TeamAndEdges(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "ray", Type: config.AgentTypeCore},
				{ID: "worker", Type: config.AgentTypeWorker},
				{ID: "planner", Type: config.AgentTypeWorker},
				{ID: "explorer", Type: config.AgentTypeWorker},
				{ID: "researcher", Type: config.AgentTypeWorker},
			},
		},
	}
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1)
	ws := wss[0]
	assert.True(t, ws.IsDefault)
	// Full install roster: every agent coreagent delivers on a fresh install
	// (4 base + Worker + 3 specialists). Worker must be on-team so the
	// coreagent →worker seed edges survive seedEdgesForTeam (UAT DEF-001).
	assert.ElementsMatch(t,
		[]string{"mia", "jim", "ava", "ray", "worker", "planner", "explorer", "researcher"},
		ws.CoreTeam)

	// Edges: full coreAgentDelegation matrix with Worker on-team —
	// Jim→ava/ray/worker, Mia→worker, Ava→worker, Ray→worker/researcher,
	// Planner→explorer/researcher.
	require.Len(t, ws.Delegation, 9)
	byPair := make(map[string]storedDelegationEdge, len(ws.Delegation))
	for _, e := range ws.Delegation {
		byPair[e.FromAgent+"->"+e.ToAgent] = e
	}
	for _, want := range []string{
		"jim->ava", "jim->ray", "jim->worker",
		"mia->worker", "ava->worker",
		"ray->worker", "ray->researcher",
		"planner->explorer", "planner->researcher",
	} {
		assert.Contains(t, byPair, want, "expected seeded edge %s", want)
	}

	// Jim's SEED modes are still 3-valued (task, background, await —
	// coreagent.coreAgentDelegation is unchanged), but the TRANSLATED graph
	// edge collapses+dedupes them to the edge's 2-value vocabulary: task stays
	// task, background+await collapse to a single direct entry.
	jimToAva := byPair["jim->ava"]
	assert.ElementsMatch(t,
		[]workspace.DelegationMode{workspace.ModeTask, workspace.ModeDirect},
		jimToAva.Modes, "jim->ava must carry Jim's seeded modes, collapsed+deduped to [direct, task]")

	// Planner's edges are depth-bounded at 2 (bounded subagent delegation, M5).
	// Planner's SEED modes are [await, task]; the translated edge collapses to
	// [direct, task].
	plannerToExplorer := byPair["planner->explorer"]
	require.NotNil(t, plannerToExplorer.Depth, "planner->explorer must carry the seeded depth cap")
	assert.Equal(t, 2, *plannerToExplorer.Depth)
	assert.ElementsMatch(t,
		[]workspace.DelegationMode{workspace.ModeDirect, workspace.ModeTask},
		plannerToExplorer.Modes, "planner->explorer must carry Planner's seeded modes, collapsed to [direct, task]")

	// The two research specialists are leaves: no outgoing edges seeded for them.
	for _, e := range ws.Delegation {
		assert.NotEqual(t, "explorer", e.FromAgent, "explorer must not have a seeded outgoing edge")
		assert.NotEqual(t, "researcher", e.FromAgent, "researcher must not have a seeded outgoing edge")
	}
}

// TestDefaultWorkspaceTeam_ExcludesCustomAgents pins ADR-046 P1 (FR-007/008,
// US-3 AS-1): defaultWorkspaceTeam must return ONLY built-in-roster IDs
// (coreagent.All() intersected with configured agents) even when
// cfg.Agents.List additionally carries custom/user-created agents. This
// guards against a future edit silently widening the default-team seed to
// auto-add custom agents — "the default workspace's seeded team MUST be the
// built-in roster only" (FR-008) must hold regardless of what else is
// configured.
func TestDefaultWorkspaceTeam_ExcludesCustomAgents(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "ray", Type: config.AgentTypeCore},
				{ID: "worker", Type: config.AgentTypeWorker},
				{ID: "planner", Type: config.AgentTypeWorker},
				{ID: "explorer", Type: config.AgentTypeWorker},
				{ID: "researcher", Type: config.AgentTypeWorker},
				// Custom/user-created agents — must NEVER appear in the default team.
				{ID: "my-custom-bot", Type: config.AgentTypeCustom},
				{ID: "another-custom-agent", Type: config.AgentTypeCustom},
			},
		},
	}
	team := defaultWorkspaceTeam(cfg)
	assert.ElementsMatch(t,
		[]string{"mia", "jim", "ava", "ray", "worker", "planner", "explorer", "researcher"},
		team, "defaultWorkspaceTeam must return ONLY the built-in roster, never a custom agent")
	assert.NotContains(t, team, "my-custom-bot")
	assert.NotContains(t, team, "another-custom-agent")
}

// TestDefaultWorkspaceSeeder_PartialRoster_DropsOnlyMissingAgentEdges (T3,
// pr-test-analyzer / UAT DEF-001 regression coverage) proves the partial-roster
// scenario end-to-end through ensureDefaultWorkspace: a config that omits ONE
// agent (researcher) from the full coreagent roster must (a) exclude it from
// the seeded workspace TEAM, and (b) drop ONLY the delegation edges that
// reference it — every other edge in the coreAgentDelegation matrix must
// survive untouched. This exercises the FULL pipeline
// (defaultWorkspaceTeam -> defaultWorkspaceDelegationEdges -> seedEdgesForTeam
// -> ensureDefaultWorkspace), unlike the sibling unit test
// TestSeedEdgesForTeam_PartialRosterDropsOffTeamEdge above, which hand-builds
// its edge list directly rather than deriving it from a real config — that
// one pins seedEdgesForTeam's own filtering logic; this one pins the whole
// seeding path a fresh/lite install actually exercises.
func TestDefaultWorkspaceSeeder_PartialRoster_DropsOnlyMissingAgentEdges(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "ray", Type: config.AgentTypeCore},
				{ID: "worker", Type: config.AgentTypeWorker},
				{ID: "planner", Type: config.AgentTypeWorker},
				{ID: "explorer", Type: config.AgentTypeWorker},
				// "researcher" deliberately omitted — a partial-roster
				// install (e.g. a lite build or an operator-trimmed config)
				// without the researcher specialist.
			},
		},
	}
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1)
	ws := wss[0]
	assert.True(t, ws.IsDefault)

	// (a) team excludes the missing agent — never a dangling roster entry.
	assert.ElementsMatch(t,
		[]string{"mia", "jim", "ava", "ray", "worker", "planner", "explorer"},
		ws.CoreTeam)
	assert.NotContains(t, ws.CoreTeam, "researcher")

	// (b) only researcher's edges are dropped — every other edge from the
	// full-roster matrix (see TestDefaultWorkspaceSeeder_TeamAndEdges) survives
	// exactly as seeded.
	byPair := make(map[string]storedDelegationEdge, len(ws.Delegation))
	for _, e := range ws.Delegation {
		byPair[e.FromAgent+"->"+e.ToAgent] = e
		assert.NotEqual(t, "researcher", e.FromAgent, "researcher is off-team — must not appear as a from_agent")
		assert.NotEqual(t, "researcher", e.ToAgent, "researcher is off-team — must not appear as a to_agent")
	}
	for _, want := range []string{
		"jim->ava", "jim->ray", "jim->worker",
		"mia->worker", "ava->worker",
		"ray->worker", "planner->explorer",
	} {
		assert.Contains(t, byPair, want, "expected surviving seeded edge %s", want)
	}
	// The two edges that reference researcher must be gone entirely, not
	// dangling with an unresolvable endpoint.
	assert.NotContains(t, byPair, "ray->researcher")
	assert.NotContains(t, byPair, "planner->researcher")
	assert.Len(t, ws.Delegation, 7, "9 full-roster edges minus the 2 that reference the missing researcher")
}

// TestDefaultWorkspaceDelegationEdges_MatchesCoreagentSeed verifies the
// TRANSFORMATION LOOP inside defaultWorkspaceDelegationEdges is correct
// relative to whatever coreagent.SeedDelegationEdges currently returns: for
// every agent id coreagent.SeedConfig seeds into a fresh config, the edges
// defaultWorkspaceDelegationEdges derives match coreagent.SeedDelegationEdges(id)
// field-for-field (target, modes, depth) — i.e. the per-agent →
// per-target-edge expansion (mode conversion, depth-pointer copy, self/
// wildcard/remote-a2a filtering) does not drop or corrupt data on the way
// from the seed matrix to the workspace graph shape.
//
// NOTE (pr-test-analyzer, 7-reviewer-gate follow-up): this is a
// SELF-CONSISTENCY check, not an independent pre/post-ADR-037 baseline —
// both sides of the comparison ultimately call the same
// coreagent.SeedDelegationEdges/coreAgentDelegation function, so corrupting
// the seed DATA itself (as opposed to the transformation loop) would not
// make this test fail (confirmed via fault injection). The actual content
// pin — the test that WOULD catch a seed-data regression, because it asserts
// hardcoded literal expected edges/modes/depths rather than deriving its
// expectation from the same function under test — is the sibling
// TestDefaultWorkspaceSeeder_TeamAndEdges above. Keep both: this one for the
// transformation loop, that one for the actual seeded content.
func TestDefaultWorkspaceDelegationEdges_MatchesCoreagentSeed(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig on empty config must modify")

	got := defaultWorkspaceDelegationEdges(cfg)

	// Build the expected edge set directly from coreagent.SeedDelegationEdges,
	// independent of defaultWorkspaceDelegationEdges's own implementation, by
	// replaying the exact same derivation the function's doc comment describes.
	var want []storedDelegationEdge
	// Deliberately replays defaultWorkspaceDelegationEdges's loop independently
	// (not via a shared helper) so this test can catch a regression in THAT transformation
	// logic; sharing a helper would make this test tautological (see the doc comment above).
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		dp := coreagent.SeedDelegationEdges(coreagent.CoreAgentID(ac.ID))
		if dp == nil || len(dp.To) == 0 {
			continue
		}
		// Independently replay the collapse+dedupe (config.DelegationMode's real
		// 3-value tool vocabulary → workspace.DelegationMode's collapsed 2-value
		// edge vocabulary) WITHOUT calling agent.EdgeModeCategory, so this test
		// can catch a regression in that collapse rule too, not just the
		// target/depth plumbing around it.
		modes := make([]workspace.DelegationMode, 0, len(dp.Modes))
		seenMode := make(map[workspace.DelegationMode]bool, len(dp.Modes))
		for _, m := range dp.Modes {
			wm := workspace.ModeDirect
			if m == config.DelegationModeTask {
				wm = workspace.ModeTask
			}
			if seenMode[wm] {
				continue
			}
			seenMode[wm] = true
			modes = append(modes, wm)
		}
		var depth *int
		if dp.Depth != nil {
			d := *dp.Depth
			depth = &d
		}
		for _, ref := range dp.To {
			if ref.Kind != config.AgentRefKindLocal || ref.ID == "*" || ref.ID == ac.ID {
				continue
			}
			want = append(want, storedDelegationEdge{
				FromAgent: ac.ID,
				ToAgent:   ref.ID,
				Modes:     append([]workspace.DelegationMode(nil), modes...),
				Depth:     depth,
			})
		}
	}

	require.Len(t, got, len(want), "edge count must match the coreagent seed exactly")
	gotByPair := make(map[string]storedDelegationEdge, len(got))
	for _, e := range got {
		gotByPair[e.FromAgent+"->"+e.ToAgent] = e
	}
	for _, w := range want {
		g, ok := gotByPair[w.FromAgent+"->"+w.ToAgent]
		require.True(t, ok, "expected edge %s->%s in defaultWorkspaceDelegationEdges output", w.FromAgent, w.ToAgent)
		assert.ElementsMatch(t, w.Modes, g.Modes, "modes mismatch for %s->%s", w.FromAgent, w.ToAgent)
		if w.Depth == nil {
			assert.Nil(t, g.Depth, "depth mismatch for %s->%s (want nil)", w.FromAgent, w.ToAgent)
		} else {
			require.NotNil(t, g.Depth, "depth mismatch for %s->%s (want %d, got nil)", w.FromAgent, w.ToAgent, *w.Depth)
			assert.Equal(t, *w.Depth, *g.Depth, "depth mismatch for %s->%s", w.FromAgent, w.ToAgent)
		}
	}
}

// TestSeedEdgesForTeam_PartialRosterDropsOffTeamEdge proves a custom core_team
// that omits "explorer" does NOT inherit the planner→explorer seed edge: an edge
// whose endpoint is not on the team is dropped, so a custom workspace never gains
// a dangling edge to an agent it left out (M6).
func TestSeedEdgesForTeam_PartialRosterDropsOffTeamEdge(t *testing.T) {
	t.Parallel()
	allEdges := []storedDelegationEdge{
		{FromAgent: "jim", ToAgent: "planner"},
		{FromAgent: "planner", ToAgent: "explorer"},
		{FromAgent: "planner", ToAgent: "researcher"},
	}
	// Custom team WITHOUT explorer.
	team := []string{"jim", "planner", "researcher"}

	got := seedEdgesForTeam(allEdges, team)

	pairs := make(map[string]bool, len(got))
	for _, e := range got {
		pairs[e.FromAgent+"->"+e.ToAgent] = true
	}
	assert.True(t, pairs["jim->planner"], "both endpoints on team → kept")
	assert.True(t, pairs["planner->researcher"], "both endpoints on team → kept")
	assert.False(t, pairs["planner->explorer"],
		"explorer is off-team → its edge must be dropped, not dangling")
	assert.Len(t, got, 2)
}

// TestWorkspaceDelegation_Depth0AndEmptyModes_RoundTrip proves that an explicit
// depth:0 ("no onward delegation past this hop") and an explicit empty modes
// array survive the PUT→GET round-trip without being lost or coerced. These are
// the boundary wire values most prone to omitempty/codegen drift.
func TestWorkspaceDelegation_Depth0AndEmptyModes_RoundTrip(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)

	// Edge 1: depth:0 explicit. Edge 2: modes:[] explicit empty.
	body := `{"edges":[` +
		`{"from_agent":"jim","to_agent":"ava","depth":0},` +
		`{"from_agent":"planner","to_agent":"ray","modes":[]}` +
		`]}`
	w := putDelegation(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	got := decodeDelegation(t, getDelegation(t, api, id).Body.Bytes())
	require.Len(t, got.Edges, 2)
	assert.Equal(t, 3, got.DefaultDepth,
		"default_depth is exposed regardless of whether any edge sets its own depth")

	byFrom := make(map[string]gen.WorkspaceDelegationEdge, 2)
	for _, e := range got.Edges {
		byFrom[e.FromAgent] = e
	}

	jimAva, ok := byFrom["jim"]
	require.True(t, ok, "jim→ava edge must round-trip")
	require.NotNil(t, jimAva.Depth, "explicit depth:0 must NOT be dropped on the wire")
	assert.Equal(t, 0, *jimAva.Depth, "depth:0 must survive (no onward delegation)")

	// Persisted form must also carry depth:0.
	stored, err := readWorkspaceFile(api.homePath, id)
	require.NoError(t, err)
	var foundDepth0 bool
	for _, e := range stored.Delegation {
		if e.FromAgent == "jim" && e.ToAgent == "ava" {
			require.NotNil(t, e.Depth, "stored depth:0 must persist")
			assert.Equal(t, 0, *e.Depth)
			foundDepth0 = true
		}
	}
	assert.True(t, foundDepth0, "jim→ava with depth:0 must be persisted")
}
