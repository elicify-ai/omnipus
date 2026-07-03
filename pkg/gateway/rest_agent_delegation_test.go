//go:build !cgo

// REST delegation_policy round-trip tests for the agent delegation policy field.
//
// Regression coverage for the finding that req.DelegationPolicy was never mapped
// to config.AgentConfig.DelegationPolicy: it was write-dropped on create/update
// and never echoed on GET, blocking the delegation-graph editor from saving.
//
// These tests prove the full path:
//  1. PUT /agents/{id} with a valid policy → 200 and stored config reflects it (GET round-trip)
//  2. PUT with an unknown LOCAL target → 400
//  3. PUT with a bad mode → 400
//  4. PUT with depth above the ceiling → 400
//  5. PUT that OMITS delegation_policy → existing seeded policy preserved (not wiped)
//  6. PUT with only to/modes/depth when stored policy had accept_from → accept_from preserved
//  7. POST /agents with a policy → persisted
//  8. PUT with a remote-a2a target → accepted (no roster resolution)

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// buildDelegationTestAPI builds a restAPI over a temp home with a roster of agents
// (mia, ava, ray, plus a target test-agent we mutate) so local delegation targets
// resolve and create/update/get operate on a real config.json.
func buildDelegationTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096,"subturn":{"max_depth":3}},"list":[` +
		`{"id":"test-agent","name":"Test Agent","type":"custom"},` +
		`{"id":"ava","name":"Ava","type":"custom"},` +
		`{"id":"ray","name":"Ray","type":"custom"},` +
		`{"id":"laborer","name":"Laborer","type":"worker"}` +
		`]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
				SubTurn:   config.SubTurnConfig{MaxDepth: 3},
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom},
				{ID: "ava", Name: "Ava", Type: config.AgentTypeCustom},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCustom},
				{ID: "laborer", Name: "Laborer", Type: config.AgentTypeWorker},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop: al,
		homePath:  tmpDir,
	}
}

// findDelegationPolicyInConfig digs agents.list[id].delegation_policy out of a
// parsed config.json map. Returns nil when absent.
func findDelegationPolicyInConfig(t *testing.T, m map[string]any, id string) map[string]any {
	t.Helper()
	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		return nil
	}
	list, _ := agents["list"].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		if entry == nil || entry["id"] != id {
			continue
		}
		dp, _ := entry["delegation_policy"].(map[string]any)
		return dp
	}
	return nil
}

// putAgent issues a PUT /api/v1/agents/{id} with the given JSON body.
func putAgent(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return w
}

// TestUpdateAgent_DelegationPolicyPersistsAndRoundTrips proves a valid policy is
// mapped, persisted, and echoed back on GET.
func TestUpdateAgent_DelegationPolicyPersistsAndRoundTrips(t *testing.T) {
	api := buildDelegationTestAPI(t)

	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"},{"kind":"local","id":"ray"}],"modes":["task","background"],"depth":2}}`
	w := putAgent(t, api, "test-agent", body)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())

	// Echoed in the PUT response.
	updated := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, updated.DelegationPolicy, "PUT response must echo the policy")
	require.NotNil(t, updated.DelegationPolicy.To)
	require.Len(t, *updated.DelegationPolicy.To, 2)
	assert.Equal(t, "ava", (*updated.DelegationPolicy.To)[0].Id)
	assert.Equal(t, "ray", (*updated.DelegationPolicy.To)[1].Id)
	require.NotNil(t, updated.DelegationPolicy.Depth)
	assert.Equal(t, 2, *updated.DelegationPolicy.Depth)
	require.NotNil(t, updated.DelegationPolicy.Modes)
	assert.Len(t, *updated.DelegationPolicy.Modes, 2)

	// Persisted to config.json.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp, "delegation_policy not persisted: %s", string(raw))
	to, _ := dp["to"].([]any)
	require.Len(t, to, 2)
	assert.EqualValues(t, 2, dp["depth"])
	modes, _ := dp["modes"].([]any)
	assert.Len(t, modes, 2)

	// GET round-trips the stored policy.
	gw := httptest.NewRecorder()
	gr := httptest.NewRequest(http.MethodGet, "/api/v1/agents/test-agent", nil)
	api.HandleAgents(gw, gr)
	require.Equal(t, http.StatusOK, gw.Code, "get body: %s", gw.Body.String())
	got := decodeAgentResp(t, gw.Body.Bytes())
	require.NotNil(t, got.DelegationPolicy, "GET must echo the persisted policy")
	require.NotNil(t, got.DelegationPolicy.To)
	assert.Len(t, *got.DelegationPolicy.To, 2)
	require.NotNil(t, got.DelegationPolicy.Depth)
	assert.Equal(t, 2, *got.DelegationPolicy.Depth)
}

// TestUpdateAgent_DelegationUnknownTarget_400 rejects an unknown local target.
func TestUpdateAgent_DelegationUnknownTarget_400(t *testing.T) {
	api := buildDelegationTestAPI(t)
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"does-not-exist"}]}}`
	w := putAgent(t, api, "test-agent", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "want 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "does not exist")
}

// TestUpdateAgent_DelegationBadMode_400 rejects an unknown delegation mode.
func TestUpdateAgent_DelegationBadMode_400(t *testing.T) {
	api := buildDelegationTestAPI(t)
	// "sync" is not a valid mode; bypass inbound validation by leaving ValidateInbound
	// off (default in the test cfg) so the handler's own validation is exercised.
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["sync"]}}`
	w := putAgent(t, api, "test-agent", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "want 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "mode")
}

// TestUpdateAgent_DelegationDepthAboveCeiling_400 rejects depth above the cap.
func TestUpdateAgent_DelegationDepthAboveCeiling_400(t *testing.T) {
	api := buildDelegationTestAPI(t)
	// Ceiling is 3 (subturn.max_depth). 99 must be rejected.
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"depth":99}}`
	w := putAgent(t, api, "test-agent", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "want 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "depth")
}

// TestUpdateAgent_DelegationNegativeDepth_400 rejects a negative depth.
func TestUpdateAgent_DelegationNegativeDepth_400(t *testing.T) {
	api := buildDelegationTestAPI(t)
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"depth":-1}}`
	w := putAgent(t, api, "test-agent", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "want 400; body: %s", w.Body.String())
}

// TestUpdateAgent_OmitDelegationPreservesSeeded proves an update that omits
// delegation_policy leaves a previously-seeded policy untouched (not wiped).
func TestUpdateAgent_OmitDelegationPreservesSeeded(t *testing.T) {
	api := buildDelegationTestAPI(t)

	// Seed a policy.
	seed := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["task"],"depth":1}}`
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent", seed).Code)

	// PUT an UNRELATED field — must NOT wipe the policy.
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent", `{"description":"new desc"}`).Code)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp, "seeded policy wiped by unrelated PUT: %s", string(raw))
	to, _ := dp["to"].([]any)
	require.Len(t, to, 1)
	assert.EqualValues(t, 1, dp["depth"])
}

// TestUpdateAgent_DelegationPreservesInertAcceptFrom proves an edit that sends
// only to/modes/depth preserves a pre-existing inert accept_from on disk.
func TestUpdateAgent_DelegationPreservesInertAcceptFrom(t *testing.T) {
	api := buildDelegationTestAPI(t)

	// Inject a stored policy WITH an inert accept_from directly into config.json,
	// then reload, so we simulate a seeded/hand-authored policy.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		list, _ := agents["list"].([]any)
		for _, item := range list {
			entry, _ := item.(map[string]any)
			if entry != nil && entry["id"] == "test-agent" {
				entry["delegation_policy"] = map[string]any{
					"to":          []any{map[string]any{"kind": "local", "id": "ava"}},
					"accept_from": []any{map[string]any{"kind": "remote-a2a", "id": "future-peer"}},
				}
			}
		}
		return nil
	}))
	// safeUpdateConfigJSON already refreshed the in-memory config, so the next PUT's
	// foundAgent reads the just-injected policy (no TriggerReload needed — the test
	// agentLoop has no reload callback wired).

	// Now PUT only to/modes/depth — accept_from must be preserved.
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ray"}],"modes":["task"],"depth":2}}`
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent", body).Code)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp)
	// to was replaced with [ray].
	to, _ := dp["to"].([]any)
	require.Len(t, to, 1)
	toRef, _ := to[0].(map[string]any)
	assert.Equal(t, "ray", toRef["id"])
	// accept_from (inert) preserved.
	af, _ := dp["accept_from"].([]any)
	require.Len(t, af, 1, "inert accept_from was wiped: %s", string(raw))
	afRef, _ := af[0].(map[string]any)
	assert.Equal(t, "future-peer", afRef["id"])
}

// TestCreateAgent_DelegationPolicyPersists proves POST maps + persists the policy.
func TestCreateAgent_DelegationPolicyPersists(t *testing.T) {
	api := buildDelegationTestAPI(t)

	body := `{"name":"Delegator","type":"Main","soul":"delegator-soul","delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["await"],"depth":1}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, created.DelegationPolicy, "create response must echo the policy")
	require.NotNil(t, created.DelegationPolicy.To)
	require.Len(t, *created.DelegationPolicy.To, 1)
	assert.Equal(t, "ava", (*created.DelegationPolicy.To)[0].Id)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, created.Id)
	require.NotNil(t, dp, "delegation_policy not persisted on create: %s", string(raw))
	to, _ := dp["to"].([]any)
	require.Len(t, to, 1)
}

// TestCreateAgent_DelegationUnknownTarget_400 rejects an unknown local target on create.
func TestCreateAgent_DelegationUnknownTarget_400(t *testing.T) {
	api := buildDelegationTestAPI(t)
	body := `{"name":"Delegator","type":"Main","soul":"delegator-soul","delegation_policy":{"to":[{"kind":"local","id":"ghost"}]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "want 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "does not exist", "the 400 must be the delegation-target check, not an unrelated 400")
}

// TestUpdateAgent_DelegationRemoteA2AAccepted proves a remote-a2a target is
// accepted without roster resolution (reserved/future).
func TestUpdateAgent_DelegationRemoteA2AAccepted(t *testing.T) {
	api := buildDelegationTestAPI(t)
	body := `{"delegation_policy":{"to":[{"kind":"remote-a2a","id":"some-remote-id"}],"modes":["task"]}}`
	w := putAgent(t, api, "test-agent", body)
	require.Equal(t, http.StatusOK, w.Code, "want 200 for reserved remote-a2a; body: %s", w.Body.String())
}

// TestUpdateAgent_DelegationEmptyToDenyAll proves an explicit empty to-list
// (deny-all) persists as [] rather than being treated as "unset".
func TestUpdateAgent_DelegationEmptyToDenyAll(t *testing.T) {
	api := buildDelegationTestAPI(t)
	// Seed a non-empty policy first.
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}]}}`).Code)
	// Now set an explicit empty to-list — deny all.
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[]}}`).Code)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp, "policy missing after deny-all set: %s", string(raw))
	to, ok := dp["to"].([]any)
	require.True(t, ok, "to key must be present (deny-all), got: %v", dp["to"])
	assert.Len(t, to, 0, "deny-all to-list must be empty")
}

// TestUpdateAgent_NeedsReloadRebuildsLiveDelegationCheckers proves a
// delegation_policy edit triggers a reload that swaps the running config WITHOUT
// a process restart.
//
// SCOPE NOTE (per-workspace-graph migration): the per-agent config.DelegationPolicy
// is now SEED-ONLY — runtime delegation enforcement reads the per-workspace
// delegation GRAPH (workspace.ReadDelegation), not config. So this test now proves
// the CONFIG (seed source) persists and reloads in place; it asserts against
// config.ResolveDelegationTo + IsDelegationAllowed, which remain the config-layer
// resolution used to SEED a workspace graph. The runtime "edit takes effect with
// no rebuild" proof now lives in pkg/agent
// (TestDelegationGraphFlipsWithoutRebuild), which edits the graph and shows the
// SAME checker flips because it reads the graph per-call.
func TestUpdateAgent_NeedsReloadRebuildsLiveDelegationCheckers(t *testing.T) {
	api := buildDelegationTestAPI(t)
	al := api.agentLoop
	provider := &restMockProvider{}

	// Production-faithful reload: load the swapped on-disk config and rebuild the
	// registry + every agent instance's delegation deny-checkers, exactly as
	// executeReload does (gateway.go:1773 ReloadProviderAndConfig).
	al.SetReloadFunc(func() error {
		newCfg, err := config.LoadConfig(api.configPath())
		if err != nil {
			return err
		}
		al.SwapConfig(newCfg)
		return al.ReloadProviderAndConfig(context.Background(), provider, newCfg)
	})

	// liveAllows reports whether the running config (after any reload) permits
	// test-agent → target — the same check the rebuilt runtime deny-checker makes.
	liveAllows := func(target string) bool {
		cfg := al.GetConfig()
		var ac *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "test-agent" {
				ac = &cfg.Agents.List[i]
				break
			}
		}
		require.NotNil(t, ac, "test-agent missing from live config")
		toList := config.ResolveDelegationTo(ac, cfg.Agents.Defaults)
		return config.IsDelegationAllowed(toList, target)
	}

	// Seed test-agent to:[ava] via the handler (triggers a reload).
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["background"]}}`).Code)
	require.True(t, liveAllows("ava"), "after seeding to:[ava], running checker must allow ava")
	require.False(t, liveAllows("ray"), "after seeding to:[ava], running checker must deny ray")

	// Edit test-agent to:[ray] — a tightening edit that revokes ava and grants ray.
	// The fix makes this PUT trigger a reload that rebuilds the deny-checkers.
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ray"}],"modes":["background"]}}`).Code)

	// The gate FLIPPED with no restart: ray now allowed, ava now denied.
	require.True(t, liveAllows("ray"), "after edit to:[ray], running checker must now ALLOW ray (no restart)")
	require.False(t, liveAllows("ava"), "after edit to:[ray], running checker must now DENY ava (revoked, no restart)")
}

// TestUpdateAgent_WorkerOnwardDelegationAllowed proves the bounded-delegation
// unlock (S3 BE, M5 item 2): a worker/Subagent may now carry a non-empty to[]
// and delegate onward. The previous hard worker-leaf rejection is gone; depth is
// the safety boundary. A worker delegating to a valid in-roster target within the
// depth ceiling → 200, and the policy round-trips on GET.
func TestUpdateAgent_WorkerOnwardDelegationAllowed(t *testing.T) {
	api := buildDelegationTestAPI(t)
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["task"],"depth":2}}`
	w := putAgent(t, api, "laborer", body)
	require.Equal(
		t,
		http.StatusOK,
		w.Code,
		"worker onward delegation within depth must be allowed; body: %s",
		w.Body.String(),
	)

	// Round-trip: GET must echo the stored onward-delegation policy.
	gw := httptest.NewRecorder()
	gr := httptest.NewRequest(http.MethodGet, "/api/v1/agents/laborer", nil)
	api.HandleAgents(gw, gr)
	require.Equal(t, http.StatusOK, gw.Code, "get body: %s", gw.Body.String())
	got := decodeAgentResp(t, gw.Body.Bytes())
	require.NotNil(t, got.DelegationPolicy, "stored delegation_policy must be echoed on GET")
	require.NotNil(t, got.DelegationPolicy.To)
	require.Len(t, *got.DelegationPolicy.To, 1)
	assert.Equal(t, "ava", (*got.DelegationPolicy.To)[0].Id)
}

// TestUpdateAgent_WorkerDepthExceededRejected proves depth remains the safety cap
// for onward delegation: a worker with a non-empty to[] whose depth exceeds the
// global subturn ceiling (3) → 400, even though the to[] itself is now allowed.
func TestUpdateAgent_WorkerDepthExceededRejected(t *testing.T) {
	api := buildDelegationTestAPI(t)
	body := `{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"depth":99}}`
	w := putAgent(t, api, "laborer", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "depth above ceiling must be rejected; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "depth")
}

// TestUpdateAgent_WorkerEmptyToAllowed proves a worker may carry an explicit
// empty to[] ("deny all") — only a non-empty to[] is rejected.
func TestUpdateAgent_WorkerEmptyToAllowed(t *testing.T) {
	api := buildDelegationTestAPI(t)
	w := putAgent(t, api, "laborer", `{"delegation_policy":{"to":[]}}`)
	assert.Equal(
		t,
		http.StatusOK,
		w.Code,
		"worker with empty to[] (deny-all) must be allowed; body: %s",
		w.Body.String(),
	)
}

// TestUpdateAgent_GrandfatherDanglingTarget proves a pre-existing dangling to[]
// ref does not brick unrelated edits to the pointing agent: after the target is
// deleted, toggling only an unrelated field (modes) succeeds (the stale ref is
// grandfathered), while ADDING a brand-new unknown target is still rejected.
func TestUpdateAgent_GrandfatherDanglingTarget(t *testing.T) {
	api := buildDelegationTestAPI(t)

	// test-agent → ava.
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["task"]}}`).Code)

	// Delete ava from the roster directly in config.json + refresh live config so
	// the stored test-agent policy now points at a non-existent target.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		agents, _ := m["agents"].(map[string]any)
		list, _ := agents["list"].([]any)
		kept := make([]any, 0, len(list))
		for _, item := range list {
			entry, _ := item.(map[string]any)
			if entry != nil && entry["id"] == "ava" {
				continue
			}
			kept = append(kept, item)
		}
		agents["list"] = kept
		return nil
	}))

	// Toggling ONLY modes (the stale ava ref is unchanged) must NOT be bricked —
	// the dangling ref is grandfathered, not re-validated.
	w := putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["background"]}}`)
	assert.Equal(t, http.StatusOK, w.Code,
		"a pre-existing dangling ref must not brick an unrelated edit; body: %s", w.Body.String())

	// But ADDING a brand-new unknown target is still rejected (only newly-added
	// refs are roster-checked).
	w2 := putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"},{"kind":"local","id":"ghost"}]}}`)
	assert.Equal(t, http.StatusBadRequest, w2.Code,
		"a newly-added unknown target must still 400; body: %s", w2.Body.String())
	assert.Contains(t, w2.Body.String(), "does not exist")
}

// TestUpdateAgent_SelfReferenceRejected proves A→A self-delegation is rejected.
func TestUpdateAgent_SelfReferenceRejected(t *testing.T) {
	api := buildDelegationTestAPI(t)
	w := putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"test-agent"}]}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "self-ref must 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "itself")
}

// TestUpdateAgent_DuplicateToCollapsed proves duplicate to[] refs collapse to one.
func TestUpdateAgent_DuplicateToCollapsed(t *testing.T) {
	api := buildDelegationTestAPI(t)
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"},{"kind":"local","id":"ava"}]}}`).Code)

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp)
	to, _ := dp["to"].([]any)
	assert.Len(t, to, 1, "duplicate to[] refs must collapse to one: %s", string(raw))
}

// TestUpdateAgent_WildcardToPersists proves a "*" wildcard target is accepted.
func TestUpdateAgent_WildcardToPersists(t *testing.T) {
	api := buildDelegationTestAPI(t)
	w := putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"*"}]}}`)
	require.Equal(t, http.StatusOK, w.Code, "wildcard must be accepted; body: %s", w.Body.String())

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp)
	to, _ := dp["to"].([]any)
	require.Len(t, to, 1)
	ref, _ := to[0].(map[string]any)
	assert.Equal(t, "*", ref["id"])
}

// TestUpdateAgent_EmptyToIDRejected proves a to[] ref with an empty id → 400.
func TestUpdateAgent_EmptyToIDRejected(t *testing.T) {
	api := buildDelegationTestAPI(t)
	w := putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":""}]}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "empty id must 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "must not be empty")
}

// TestUpdateAgent_DelegationCycleRejected proves a multi-hop delegation cycle
// across agents (test-agent→ava→test-agent) is rejected at write time. First
// test-agent→ava is set; then ava→test-agent would close the loop → 400.
func TestUpdateAgent_DelegationCycleRejected(t *testing.T) {
	api := buildDelegationTestAPI(t)

	// test-agent → ava.
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["task"]}}`).Code)

	// ava → test-agent would form the cycle test-agent→ava→test-agent.
	w := putAgent(t, api, "ava",
		`{"delegation_policy":{"to":[{"kind":"local","id":"test-agent"}],"modes":["task"]}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "cycle must be rejected; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "cycle")
}

// TestUpdateAgent_DelegationNoCycleAcrossAgents proves a non-cyclic chain
// (test-agent→ava, ava→ray) is accepted — only true loops are rejected.
func TestUpdateAgent_DelegationNoCycleAcrossAgents(t *testing.T) {
	api := buildDelegationTestAPI(t)
	require.Equal(t, http.StatusOK, putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"modes":["task"]}}`).Code)
	w := putAgent(t, api, "ava",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ray"}],"modes":["task"]}}`)
	require.Equal(t, http.StatusOK, w.Code, "acyclic chain must be allowed; body: %s", w.Body.String())
}

// TestUpdateAgent_DepthAtCeilingAccepted proves depth == ceiling (3) is the
// boundary that is accepted (above the ceiling is the rejecting case).
func TestUpdateAgent_DepthAtCeilingAccepted(t *testing.T) {
	api := buildDelegationTestAPI(t)
	w := putAgent(t, api, "test-agent",
		`{"delegation_policy":{"to":[{"kind":"local","id":"ava"}],"depth":3}}`)
	require.Equal(t, http.StatusOK, w.Code, "depth==ceiling(3) must be accepted; body: %s", w.Body.String())

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	dp := findDelegationPolicyInConfig(t, m, "test-agent")
	require.NotNil(t, dp)
	assert.EqualValues(t, 3, dp["depth"])
}

// TestDelegationInputExtractors_BehavioralIdentity pins the four
// //nolint:dupl delegation-input extractors
// (delegationInputFromCreateRequestMain / …Subagent / …Subagent3p /
// delegationInputFromUpdateRequest, rest_agent_delegation.go) against drift.
// The four are intentionally near-identical bodies — one per oapi-codegen
// inline DelegationPolicy struct, since Main/Subagent/Subagent3p/Update each
// get a distinct generated type even though the JSON shape is the same. This
// test unmarshals the SAME DelegationPolicy JSON into each of the four
// parent request types, runs each extractor, and asserts all four outputs
// are deep-equal — a future edit to one extractor that silently diverges
// from the other three fails here rather than shipping a variant-dependent
// delegation policy semantics bug.
func TestDelegationInputExtractors_BehavioralIdentity(t *testing.T) {
	const dpJSON = `{
		"to": [{"kind":"local","id":"ray"},{"kind":"remote-a2a","id":"peer-1"}],
		"accept_from": [{"kind":"local","id":"ava"}],
		"modes": ["await","task"],
		"depth": 2,
		"budget": {"max_cost_usd": 1.5, "max_tokens": 1000}
	}`
	body := []byte(`{"delegation_policy":` + dpJSON + `}`)

	var mainReq gen.AgentCreateRequestMain
	require.NoError(t, json.Unmarshal(body, &mainReq))
	var subReq gen.AgentCreateRequestSubagent
	require.NoError(t, json.Unmarshal(body, &subReq))
	var sub3pReq gen.AgentCreateRequestSubagent3p
	require.NoError(t, json.Unmarshal(body, &sub3pReq))
	var updReq gen.AgentUpdateRequest
	require.NoError(t, json.Unmarshal(body, &updReq))

	outMain := delegationInputFromCreateRequestMain(&mainReq)
	outSub := delegationInputFromCreateRequestSubagent(&subReq)
	outSub3p := delegationInputFromCreateRequestSubagent3p(&sub3pReq)
	outUpd := delegationInputFromUpdateRequest(&updReq)

	require.NotNil(t, outMain)
	require.NotNil(t, outSub)
	require.NotNil(t, outSub3p)
	require.NotNil(t, outUpd)

	assert.Equal(t, outMain, outSub,
		"delegationInputFromCreateRequestMain and …Subagent must produce identical output for identical input")
	assert.Equal(t, outMain, outSub3p,
		"delegationInputFromCreateRequestMain and …Subagent3p must produce identical output for identical input")
	assert.Equal(t, outMain, outUpd,
		"delegationInputFromCreateRequestMain and delegationInputFromUpdateRequest must produce identical output for identical input")
}
