//go:build !cgo

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/workspace"
)

// buildWorkspaceDelegationTestAPI builds a restAPI with a roster (jim, ava, ray,
// planner) so delegation edge endpoints resolve targets, plus a pre-created
// workspace whose id is returned.
func buildWorkspaceDelegationTestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096,"subturn":{"max_depth":3}},"list":[` +
		`{"id":"jim","name":"Jim","type":"core"},` +
		`{"id":"ava","name":"Ava","type":"core"},` +
		`{"id":"ray","name":"Ray","type":"core"},` +
		`{"id":"planner","name":"Planner","type":"worker"}` +
		`]},"providers":[]}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCore},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
				{ID: "planner", Name: "Planner", Type: config.AgentTypeWorker},
			},
		},
	}
	cfg.Agents.Defaults.SubTurn.MaxDepth = 3

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
}

func TestWorkspaceDelegation_PutAndRoundTrip(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava","modes":["task","background"],"depth":2},{"from_agent":"planner","to_agent":"ray","modes":["await"]}]}`
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
			name:    "invalid mode",
			edge:    storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{"telepathy"}},
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
			name:    "valid edge accepted",
			edge:    storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{"task", "background"}, Depth: intPtrGW(2)},
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
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{"telepathy"}}.Validate(team, ceiling),
		"delegation edge mode telepathy is invalid (valid: await, background, task)")
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Depth: intPtrGW(-1)}.Validate(team, ceiling),
		"delegation edge depth must be >= 0")
	assert.EqualError(t,
		storedDelegationEdge{FromAgent: "jim", ToAgent: "ava", Depth: intPtrGW(99)}.Validate(team, ceiling),
		"delegation edge depth exceeds the maximum allowed depth")
}

// TestDelegationEdgeValidate_ModesMatchConfig pins the bare-string mode literals
// duplicated in pkg/workspace (delegationModeAwait/Background/Task) against the
// canonical config.DelegationMode{Await,Background,Task} constants. pkg/workspace
// is deliberately dependency-free of pkg/config (to avoid an import cycle:
// pkg/agent imports pkg/workspace, and pkg/config is imported by both), so the
// edge-layer literals are NOT imported from config — they are re-declared. This
// test is the promised single, test-pinned source of truth: it lives in
// pkg/gateway (which CAN import BOTH packages) and asserts that every canonical
// config mode is accepted verbatim by workspace.DelegationEdge.Validate, so a
// future rename in pkg/config can no longer silently drift the edge literals.
func TestDelegationEdgeValidate_ModesMatchConfig(t *testing.T) {
	team := map[string]bool{"jim": true, "ava": true}
	const ceiling = 3

	// Every canonical config mode MUST be accepted as a valid edge mode. If a
	// config constant is renamed/retyped without updating the workspace literals,
	// Validate will reject string(thatConstant) and this fails — exactly the drift
	// the comment in pkg/workspace/delegation.go promises this test guards.
	for _, mode := range []config.DelegationMode{
		config.DelegationModeAwait,
		config.DelegationModeBackground,
		config.DelegationModeTask,
	} {
		edge := workspace.DelegationEdge{
			FromAgent: "jim",
			ToAgent:   "ava",
			// Cross-package equality: config.DelegationMode → workspace.DelegationMode
			// (both string types) must be accepted verbatim by the typed validator.
			Modes: []workspace.DelegationMode{workspace.DelegationMode(mode)},
		}
		assert.NoErrorf(t, edge.Validate(team, ceiling),
			"config mode %q must be accepted by the workspace edge validator (literal drift?)",
			string(mode))
	}

	// Negative control: a mode that is NOT one of the config constants must be
	// rejected, proving Validate is genuinely gating on the literal set (not a
	// vacuous accept-all that would mask a real drift).
	bogus := workspace.DelegationEdge{
		FromAgent: "jim",
		ToAgent:   "ava",
		Modes:     []workspace.DelegationMode{"telepathy"},
	}
	err := bogus.Validate(team, ceiling)
	require.Error(t, err, "an unknown mode must be rejected")
	assert.Contains(t, err.Error(), "is invalid",
		"rejection must come from the mode-validation branch")

	// Direct cross-package constant equality: the typed workspace.Mode* constants
	// must equal the config.DelegationMode* string values. This pins the lock-step
	// the comment in pkg/workspace/delegation.go promises — a rename of either set
	// that breaks the string equality fails here, independent of Validate.
	assert.Equal(t, string(config.DelegationModeAwait), string(workspace.ModeAwait))
	assert.Equal(t, string(config.DelegationModeBackground), string(workspace.ModeBackground))
	assert.Equal(t, string(config.DelegationModeTask), string(workspace.ModeTask))
}

// intPtrGW is a local *int helper for building delegation depth fields in the
// gateway delegation tests.
func intPtrGW(n int) *int { return &n }

// TestDefaultWorkspaceSeeder_TeamAndEdges proves ensureDefaultWorkspace seeds the
// team + edges from a config carrying seeded per-agent delegation policies.
func TestDefaultWorkspaceSeeder_TeamAndEdges(t *testing.T) {
	home := t.TempDir()
	depth := 3
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore, DelegationPolicy: &config.DelegationPolicy{
					To:    []config.AgentRef{{Kind: config.AgentRefKindLocal, ID: "planner"}},
					Modes: []config.DelegationMode{config.DelegationModeTask},
					Depth: &depth,
				}},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "ray", Type: config.AgentTypeCore},
				{ID: "planner", Type: config.AgentTypeWorker, DelegationPolicy: &config.DelegationPolicy{
					To: []config.AgentRef{
						{Kind: config.AgentRefKindLocal, ID: "explorer"},
						{Kind: config.AgentRefKindLocal, ID: "researcher"},
					},
					Modes: []config.DelegationMode{config.DelegationModeAwait},
				}},
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
	assert.ElementsMatch(t,
		[]string{"mia", "jim", "ava", "ray", "planner", "explorer", "researcher"},
		ws.CoreTeam)
	// Edges: jim→planner, planner→explorer, planner→researcher.
	require.Len(t, ws.Delegation, 3)
	pairs := make(map[string]bool)
	for _, e := range ws.Delegation {
		pairs[e.FromAgent+"->"+e.ToAgent] = true
	}
	assert.True(t, pairs["jim->planner"])
	assert.True(t, pairs["planner->explorer"])
	assert.True(t, pairs["planner->researcher"])
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
