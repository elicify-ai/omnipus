// REST create-by-type tests for the discriminated-union AgentCreateRequest
// (W1: AgentCreateRequestMain / AgentCreateRequestSubagent /
// AgentCreateRequestSubagent3p). Proves:
//  1. Omitted type → 400 (the historical omit-type→Main default is retired;
//     type is now a required, single-value discriminator on every variant).
//  2. type="Subagent" → persisted as "worker" (no default, not a chat
//     target), and is unlocked so the operator can edit it. Response echoes
//     "Subagent".
//  3. type="core" / "system" / anything else → 400.
//  4. updateAgent does NOT change Type (a custom stays custom).
//  5. type="subagent_3p" with executor.kind=external-cli persists directly
//     (Subagent has no executor property at all any more — there is no
//     "reclassify a Subagent create into subagent_3p" path; the caller must
//     choose the variant up front).
//  6. Main create with an `executor` key: the field does not exist on
//     AgentCreateRequestMain — it is unknown-field-ignored (ValidateInbound
//     off) rather than coerced-with-a-warning (the old runtime coercion
//     check is gone; see TestCreateAgent_ValidateInbound_MainWithExecutorRejected
//     in rest_inbound_validate_test.go for the ValidateInbound-on 400 case).
//  7. Worker create with a delegation_policy field at all → 400 (ADR-037:
//     delegation_policy is retired from the wire entirely; delegation is
//     configured exclusively via the per-workspace Team tab now).
//
// The test scaffolding is the same as rest_agent_executor_test.go:
// buildExecutorTestAPI() gives a fresh temp config.json + restAPI handle so
// the write gate (safeUpdateConfigJSON) has a real file to mutate.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// findTypeTestAgentInStore returns the persisted config.AgentConfig with the
// given name (case-sensitive) from the entity store (entities/agents/<id>.json),
// or nil when not found.
//
// ADR-054 + the config.AgentsConfig.List = `json:"-"` follow-up: agents are
// per-entity records now, and agents.list can NEVER be marshaled into
// config.json by any code path — createAgent/updateAgent persist exclusively
// via agentstore.Store. Tests must therefore assert on "the persisted type"
// by reading the entity store directly, not config.json (which was the
// historical, now-obsolete, fixture assertion this helper replaces).
func findTypeTestAgentInStore(t *testing.T, homePath, name string) *config.AgentConfig {
	t.Helper()
	agents, _, err := agentstore.New(homePath).List()
	require.NoError(t, err)
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

// TestCreateAgent_TypeOmitted_Rejected is the W1 behavior-change guard: the
// historical omit-type→Main default is RETIRED. type is now a required,
// single-value discriminator on every create variant — a body without it
// must 400 rather than silently defaulting to Main. Supersedes the old
// TestCreateAgent_TypeOmitted_DefaultsToCustom.
func TestCreateAgent_TypeOmitted_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Typed Omit","soul":"omitted-type-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "type is required")

	entry := findTypeTestAgentInStore(t, api.homePath, "Typed Omit")
	assert.Nil(t, entry, "a rejected create must not persist anything")
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

	entry := findTypeTestAgentInStore(t, api.homePath, "Typed Custom")
	require.NotNil(t, entry)
	assert.Equal(t, config.AgentTypeCustom, entry.Type)
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
			// AgentCreateRequestSubagent has no executor property at all —
			// native is always server-derived when the block is absent.
			`{"name":"My Worker","type":"Subagent","description":"create-worker regression","soul":"my-worker-soul"}`,
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
	entry := findTypeTestAgentInStore(t, api.homePath, "My Worker")
	require.NotNil(t, entry, "worker must be persisted")
	assert.Equal(t, config.AgentTypeWorker, entry.Type)
}

// TestCreateAgent_TypeSubagent3p_ExternalExecutorPersists proves the
// discriminated-union create path for an External CLI worker: type must be
// "subagent_3p" directly (W1 retired the old "Subagent + executor.kind=
// external-cli reclassifies as subagent_3p" behavior — AgentCreateRequestSubagent
// has no executor property at all any more, so there is nothing to
// reclassify from). Supersedes the old
// TestCreateAgent_TypeWorker_AllowsNonNativeExecutor.
func TestCreateAgent_TypeSubagent3p_ExternalExecutorPersists(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Worker External","type":"subagent_3p","description":"external-cli worker","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"soul":"external-cli-soul"}`,
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
	assert.Equal(t, gen.ExternalCliToolCodex, *created.Executor.Cli)

	entry := findTypeTestAgentInStore(t, api.homePath, "Worker External")
	require.NotNil(t, entry)
	require.NotNil(t, entry.Subagents, "executor subagents must be persisted for a worker")
	exec := entry.Subagents.Executor
	require.NotNil(t, exec)
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "codex", exec.CLI)
}

// TestCreateAgent_NonWorker_ExecutorFieldRejected is the regression guard for
// the native-only-for-non-workers rule, updated for the unconditional
// strict-decode enforcement: AgentCreateRequestMain has no `executor`
// property at all, so a custom (Main) create that supplies one is rejected
// 400 by createAgent's strict decode — even with ValidateInbound OFF (the
// default in this harness). See TestCreateAgent_Main_ExecutorFieldRejected
// in rest_agent_executor_test.go for the full assertion; this test is the
// type-dispatch-focused sibling, and also confirms nothing is persisted.
func TestCreateAgent_NonWorker_ExecutorFieldRejected(t *testing.T) {
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

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "executor")

	entry := findTypeTestAgentInStore(t, api.homePath, "Bad Custom")
	assert.Nil(t, entry, "a rejected create must not persist anything")
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
	assert.Contains(t, w.Body.String(), "must be one of Main, Subagent, subagent_3p",
		"the rejection must name the type-validity rule")
}

// TestCreateAgent_TypeSystem_Rejected proves "system" is a seeded-only
// classification and is not creatable from the API. ADR-049 D3: System Agents
// (the Judge category) are seeded exclusively via coreagent.SeedConfig; the REST
// create path rejects type:system with a precise 400.
func TestCreateAgent_TypeSystem_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Fake System","type":"system"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "system agents are not creatable",
		"ADR-049 D3: the rejection must name the seed-only System Agent rule")
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

	entry := findTypeTestAgentInStore(t, api.homePath, "Sticky Custom")
	require.NotNil(t, entry)
	assert.Equal(t, config.AgentTypeCustom, entry.Type,
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
			`{"name":"Sticky Worker","type":"Subagent","description":"sticky worker regression","soul":"sticky-worker-soul"}`,
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

	entry := findTypeTestAgentInStore(t, api.homePath, "Sticky Worker")
	require.NotNil(t, entry)
	assert.Equal(t, config.AgentTypeWorker, entry.Type,
		"update must not change Type on a worker either")
}

// TestCreateAgent_Subagent_DelegationPolicyRejected proves ADR-037's retirement
// of delegation_policy from the wire holds for the Subagent (native worker)
// variant too, not just subagent_3p (see
// TestCreateAgent_Subagent3p_DelegationPolicyRejected in
// rest_agent_executor_test.go and TestContract_AgentCreateRequestSubagent3p_DelegationPolicyRejected
// in pkg/api/generated/contract_test.go). This test replaces the three
// pre-ADR-037 tests that exercised create-time to[]/depth validation
// (TestCreateAgent_Worker_AllowsNonEmptyToList,
// TestCreateAgent_Worker_DepthExceededRejected,
// TestCreateAgent_Worker_AllowsEmptyToList) — that validation no longer
// exists; delegation_policy is now an unconditionally-rejected unknown field
// regardless of its content.
func TestCreateAgent_Subagent_DelegationPolicyRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker With To","type":"Subagent","description":"delegation_policy retired","soul":"worker-with-to-soul","delegation_policy":{"to":[{"kind":"local","id":"test-agent"}],"modes":["task"],"depth":1}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "delegation_policy")
}
