//go:build !cgo

// REST create-by-type tests for the AgentCreateRequest.type enum
// (custom | worker). Proves:
//   1. Omitted type / type="custom" → persisted as "custom" (regression guard
//      for the pre-existing behavior).
//   2. type="worker" → persisted as "worker" (no default, not a chat target),
//      and is unlocked so the operator can edit it. Response echoes "worker".
//   3. type="core" / "system" / anything else → 400.
//   4. updateAgent does NOT change Type (a custom stays custom).
//   5. Worker create with a non-native executor (external-cli) → 200, and
//      the wire response type is subagent_3p.
//   6. Non-worker create with a non-native executor → coerced to native with
//      a warning rather than a 400.
//   7. Worker create with a delegation_policy that has a non-empty to[] within
//      depth → 201 (bounded subagent delegation, M5); depth above the ceiling
//      → 400 (depth is the safety boundary, owned by buildDelegationPolicy).
//
// The test scaffolding is the same as rest_agent_executor_test.go:
// buildExecutorTestAPI() gives a fresh temp config.json + restAPI handle so
// the write gate (safeUpdateConfigJSON) has a real file to mutate.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// readTypeTestConfigMap parses the current config.json on disk into a
// map[string]any. Tests use it to assert the on-disk type after a create
// (the load-bearing assertion, since the response is an echo and the
// in-memory config is reloaded post-write).
func readTypeTestConfigMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read config.json: %s", string(raw))
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// findTypeTestAgentInConfig returns the agents.list[*] entry with the
// given name (case-sensitive). Returns nil when not found.
func findTypeTestAgentInConfig(t *testing.T, m map[string]any, name string) map[string]any {
	t.Helper()
	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		return nil
	}
	list, _ := agents["list"].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		if entry == nil {
			continue
		}
		if entry["name"] == name {
			return entry
		}
	}
	return nil
}

// TestCreateAgent_TypeOmitted_DefaultsToCustom is the regression guard:
// omitting the `type` field must preserve the pre-existing behavior (the
// new field is purely additive). The on-disk type is "custom".
func TestCreateAgent_TypeOmitted_DefaultsToCustom(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Typed Omit","soul":"omitted-type-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)

	raw := readTypeTestConfigMap(t, api.configPath())
	entry := findTypeTestAgentInConfig(t, raw, "Typed Omit")
	require.NotNil(t, entry, "new agent must be persisted")
	assert.Equal(t, "custom", entry["type"])
}

// TestCreateAgent_TypeCustom_Explicit verifies an explicit "custom" type
// still works (the case where the SPA always sends the field).
func TestCreateAgent_TypeCustom_Explicit(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Typed Custom","type":"Main","soul":"typed-custom-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)

	raw := readTypeTestConfigMap(t, api.configPath())
	entry := findTypeTestAgentInConfig(t, raw, "Typed Custom")
	require.NotNil(t, entry)
	assert.Equal(t, "custom", entry["type"])
}

// TestCreateAgent_TypeWorker_PersistsAndEchoes proves the worker create
// path: the on-disk type is "worker", the response echoes "worker", and
// the agent is NOT marked default (the single-default invariant applies;
// a freshly-created worker is just a delegation leaf).
func TestCreateAgent_TypeWorker_PersistsAndEchoes(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"My Worker","type":"Subagent","description":"create-worker regression","executor":{"kind":"native"},"soul":"my-worker-soul"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
	// Locked must be false: a freshly-created worker is editable. The seeded
	// default general-purpose worker is locked by coreagent.SeedConfig, but
	// that path is not the REST create path.
	assert.False(t, created.Locked, "newly-created worker must not be locked")
	assert.False(t, created.Default != nil && *created.Default,
		"newly-created worker must not be the default")

	// Persisted to config.json with type=worker and no default flag.
	raw := readTypeTestConfigMap(t, api.configPath())
	entry := findTypeTestAgentInConfig(t, raw, "My Worker")
	require.NotNil(t, entry, "worker must be persisted")
	assert.Equal(t, "worker", entry["type"])
}

// TestCreateAgent_TypeWorker_AllowsNonNativeExecutor proves the worker
// property-model correction: workers may carry an external executor. The
// pre-existing 400 guard against external-cli on a non-worker still fires
// (covered separately below).
func TestCreateAgent_TypeWorker_AllowsNonNativeExecutor(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Worker External","type":"Subagent","description":"external-cli worker","executor":{"kind":"external-cli","cli":"codex"},"soul":"external-cli-soul"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent3p, created.Type)
	require.NotNil(t, created.Executor, "subagent_3p response must echo executor")
	require.NotNil(t, created.Executor.Kind, "executor.kind must be present")
	assert.Equal(t, gen.AgentExecutorKindExternalCli, *created.Executor.Kind)
	require.NotNil(t, created.Executor.Cli, "executor.cli must be present")
	assert.Equal(t, gen.AgentExecutorCliCodex, *created.Executor.Cli)

	raw := readTypeTestConfigMap(t, api.configPath())
	entry := findTypeTestAgentInConfig(t, raw, "Worker External")
	require.NotNil(t, entry)
	sub, _ := entry["subagents"].(map[string]any)
	require.NotNil(t, sub, "executor subagents must be persisted for a worker")
	exec, _ := sub["executor"].(map[string]any)
	require.NotNil(t, exec)
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "codex", exec["cli"])
}

// TestCreateAgent_NonWorker_RejectsNonNativeExecutor is the regression guard
// for the native-only-for-non-workers rule: a custom (non-worker) create
// that supplies external-cli or remote-a2a is coerced to native with a
// warning in the response.
func TestCreateAgent_NonWorker_CoercesExternalExecutorWithWarning(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Bad Custom","type":"Main","executor":{"kind":"external-cli","cli":"codex"},"soul":"soul-text"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)
	assert.Nil(t, created.Executor, "Main agents must not persist an external executor")
	require.NotNil(t, created.Warning, "expected a coercion warning")
	assert.Contains(t, *created.Warning, "coerced")
}

// TestCreateAgent_TypeCore_Rejected proves "core" is a seeded-only
// classification and is not creatable from the API.
func TestCreateAgent_TypeCore_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Fake Core","type":"core"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "type must be",
		"the rejection must name the type-validity rule")
}

// TestCreateAgent_TypeSystem_Rejected proves "system" is a seeded-only
// classification and is not creatable from the API. SeedConfig does not
// seed any system agents in v0.1.0; only the legacy wire contract honors
// them if config.json already contains one.
func TestCreateAgent_TypeSystem_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Fake System","type":"system"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "type must be",
		"the rejection must name the type-validity rule")
}

// TestCreateAgent_TypeUnknown_Rejected proves any non-enum value is 400.
// The Zod/openapi-typescript validation should normally catch this before
// the handler runs, but the gateway still re-validates the enum and
// returns 400 if the JSON somehow reaches the handler unvalidated.
func TestCreateAgent_TypeUnknown_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Bad Type","type":"bot"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestUpdateAgent_DoesNotChangeType proves Type is create-only. The
// AgentUpdateRequest contract has no `type` field, so a PUT cannot
// convert a custom into a worker or vice versa. The on-disk type is
// stable across updates.
func TestUpdateAgent_DoesNotChangeType(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a custom agent.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Sticky Custom","type":"Main","soul":"sticky-custom-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())

	// PUT an unrelated field. The on-disk type must stay "custom".
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"soul":"sticky-custom-soul","description":"updated description"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())

	raw := readTypeTestConfigMap(t, api.configPath())
	entry := findTypeTestAgentInConfig(t, raw, "Sticky Custom")
	require.NotNil(t, entry)
	assert.Equal(t, "custom", entry["type"],
		"update must not change Type — it is create-only")
}

// TestUpdateAgent_DoesNotChangeTypeOnWorker mirrors the above for a
// worker: PUT cannot change a worker into a custom (or vice versa).
func TestUpdateAgent_DoesNotChangeTypeOnWorker(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a worker.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Sticky Worker","type":"Subagent","description":"sticky worker regression","executor":{"kind":"native"},"soul":"sticky-worker-soul"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)

	// PUT an unrelated field.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"soul":"sticky-worker-soul","description":"updated description"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())

	raw := readTypeTestConfigMap(t, api.configPath())
	entry := findTypeTestAgentInConfig(t, raw, "Sticky Worker")
	require.NotNil(t, entry)
	assert.Equal(t, "worker", entry["type"],
		"update must not change Type on a worker either")
}

// TestCreateAgent_Worker_AllowsNonEmptyToList proves the bounded-delegation
// unlock at create time: a worker/Subagent created with a delegation_policy
// that has a non-empty `to` list pointing at a valid in-roster target (within
// depth) is now accepted (201). The previous worker-leaf rejection is removed;
// depth is the safety boundary, enforced in buildDelegationPolicy.
func TestCreateAgent_Worker_AllowsNonEmptyToList(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker With To","type":"Subagent","description":"non-empty to regression","executor":{"kind":"native"},"soul":"worker-with-to-soul","delegation_policy":{"to":[{"kind":"local","id":"test-agent"}],"modes":["task"],"depth":1}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(
		t,
		http.StatusCreated,
		w.Code,
		"worker onward delegation within depth must be allowed; body: %s",
		w.Body.String(),
	)
	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, created.DelegationPolicy)
	require.NotNil(t, created.DelegationPolicy.To)
	require.Len(t, *created.DelegationPolicy.To, 1)
	assert.Equal(t, "test-agent", (*created.DelegationPolicy.To)[0].Id)
}

// TestCreateAgent_Worker_DepthExceededRejected proves depth remains the cap:
// a worker created with a non-empty to[] whose depth exceeds the global subturn
// ceiling is rejected (400), naming the depth rule.
func TestCreateAgent_Worker_DepthExceededRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker Deep","type":"Subagent","description":"depth regression","executor":{"kind":"native"},"soul":"worker-deep-soul","delegation_policy":{"to":[{"kind":"local","id":"test-agent"}],"depth":99}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "depth",
		"the rejection must name the depth rule")
}

// TestCreateAgent_Worker_AllowsEmptyToList is the control: a worker
// created with an explicitly-empty `to` list (deny-all delegation) is
// permitted — the worker-leaf rule only blocks a non-empty to[].
func TestCreateAgent_Worker_AllowsEmptyToList(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker No To","type":"Subagent","description":"empty to regression","executor":{"kind":"native"},"soul":"worker-no-to-soul","delegation_policy":{"to":[]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
}
