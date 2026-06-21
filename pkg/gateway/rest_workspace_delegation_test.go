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
