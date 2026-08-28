// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for decodeAndValidate — server-side inbound request body schema
// validation.
//
// Strategy: tests exercise decodeAndValidate directly (unit tests) and
// also drive the real handler entry points with validate_inbound=true to
// confirm HTTP 400 is returned for schema-invalid bodies. Each test pair
// covers (a) valid body → 200/201, (b) invalid body → 400.
//
// The flag defaults false so no existing test is affected — tests here
// explicitly set validateEnabled=true.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithValidation returns a restAPI with validate_inbound=true.
func newTestRestAPIWithValidation(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host:            "127.0.0.1",
			Port:            8080,
			ValidateInbound: true,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
	}
}

// ── decodeAndValidate unit tests ──────────────────────────────────────────────

// TestDecodeAndValidate_ValidBody asserts that a schema-valid body decodes successfully.
func TestDecodeAndValidate_ValidBody(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	// AgentCreateRequestMain requires type, name, AND soul (W1 discriminated
	// union — AgentCreateRequest.yaml no longer exists as a single flat
	// schema; the create contract is now Main/Subagent/subagent_3p). A
	// schema-valid body must carry all three; dst only captures name, which
	// is the field the assertion below checks.
	body := `{"type":"Main","name":"My Agent","soul":"You are a focused assistant."}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, true)

	require.True(t, ok)
	assert.Equal(t, "My Agent", dst.Name)
	assert.Equal(t, http.StatusOK, w.Code) // response not written on success
}

// TestDecodeAndValidate_EmptyBodyWithValidateEnabled asserts empty body → 400.
func TestDecodeAndValidate_EmptyBodyWithValidateEnabled(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, true)

	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDecodeAndValidate_EmptyBodyValidateDisabled asserts empty body with flag off → 400 from JSON decode.
func TestDecodeAndValidate_EmptyBodyValidateDisabled(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	w := httptest.NewRecorder()

	// With validation disabled, an empty body still fails json decode (EOF).
	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, false)

	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDecodeAndValidate_WrongTypeForField asserts that sending the wrong type
// for a required string field returns 400 when validation is enabled.
// Without schema validation this would silently decode to a zero-value string.
func TestDecodeAndValidate_WrongTypeForField(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	// "name" must be a string per AgentCreateRequestMain schema; sending a number.
	body := `{"name": 42}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, true)

	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
}

// TestDecodeAndValidate_WrongTypeValidateDisabled_GoUnmarshalRejects verifies that
// even with validation disabled, Go's json.Decoder rejects type mismatches
// (number → string). Verifies the legacy decode path still returns 400.
func TestDecodeAndValidate_WrongTypeValidateDisabled_GoUnmarshalRejects(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	body := `{"name": 42}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, false)

	// json.Decoder returns an error for number→string type mismatch;
	// decodeAndValidate returns false and 400 regardless of validate flag.
	assert.False(t, ok)
}

// TestDecodeAndValidate_MissingRequiredField asserts that `{}` for a schema
// with required fields → 400 when validation enabled.
func TestDecodeAndValidate_MissingRequiredField(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	// AgentCreateRequestMain requires "type"/"name"/"soul" — sending empty object.
	body := `{}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, true)

	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDecodeAndValidate_MissingRequiredFieldValidateDisabled asserts that `{}`
// with validation disabled succeeds (Go zero values, later business logic rejects).
func TestDecodeAndValidate_MissingRequiredFieldValidateDisabled(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	body := `{}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, false)

	assert.True(t, ok)
	assert.Equal(t, "", dst.Name) // zero value — upstream guard catches it
}

// TestDecodeAndValidate_MalformedJSON asserts malformed JSON → 400 regardless of flag.
func TestDecodeAndValidate_MalformedJSON(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		var dst struct{ Name string }
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{bad json`))
		w := httptest.NewRecorder()
		ok := decodeAndValidate(w, r, "AgentCreateRequestMain", &dst, enabled)
		assert.False(t, ok, "enabled=%v", enabled)
		assert.Equal(t, http.StatusBadRequest, w.Code, "enabled=%v", enabled)
	}
}

// ── Handler integration tests — createAgent ───────────────────────────────────

// TestCreateAgent_ValidateInbound_MissingType asserts POST /agents returns 400
// for a body with no "type" field at all — type is now a required
// discriminator (W1) and createAgent's peek-the-type dispatch rejects this
// BEFORE it would even reach schema validation. Replaces the old
// TestCreateAgent_ValidateInbound_InvalidBody (which asserted a
// missing-"name" 400 against the retired flat "AgentCreateRequest" schema).
func TestCreateAgent_ValidateInbound_MissingType(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "type is required")
}

// TestCreateAgent_ValidateInbound_InvalidBody asserts POST /agents with
// validate_inbound=true returns 400 for a body missing the required "name"
// and "soul" fields, once type dispatch has already resolved to the Main
// variant schema (contracts/components/schemas/AgentCreateRequestMain.yaml).
func TestCreateAgent_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(`{"type":"Main"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
}

// TestCreateAgent_ValidateInbound_ValidBody asserts POST /agents with
// validate_inbound=true accepts a body with the required "type"/"name"/"soul" fields.
func TestCreateAgent_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"Main","name":"Test Agent","description":"desc","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	// 200 or 201 — agent created successfully
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusCreated,
		"expected 200 or 201, got %d: %s", w.Code, w.Body.String())
}

// TestCreateAgent_ValidateInbound_MainWithExecutorRejected asserts that a
// Main create carrying an `executor` property is rejected 400 at the schema
// gate when ValidateInbound is enabled: AgentCreateRequestMain has no
// executor property at all (additionalProperties: false) — Main agents
// always run native and cannot express one.
func TestCreateAgent_ValidateInbound_MainWithExecutorRejected(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"Main","name":"Bad Main","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
}

// TestCreateAgent_ValidateInbound_SubagentWithExecutorRejected asserts that a
// Subagent (native worker) create carrying an `executor` property is
// rejected 400 at the schema gate when ValidateInbound is enabled:
// AgentCreateRequestSubagent has no executor property either — kind=native
// is always server-derived for this variant.
func TestCreateAgent_ValidateInbound_SubagentWithExecutorRejected(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"Subagent","name":"Bad Subagent","description":"d","soul":"s","executor":{"kind":"native"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestSubagent")
}

// TestCreateAgent_ValidateInbound_Subagent3pMaxToolIterationsRejected asserts
// that a subagent_3p create carrying `max_tool_iterations` is rejected 400 at
// the schema gate when ValidateInbound is enabled: AgentCreateRequestSubagent3p
// has no max_tool_iterations property (the field matrix's "exclude" decision
// for this variant — the external CLI runs its own turn loop, so Omnipus
// cannot cap its per-turn tool-call budget).
func TestCreateAgent_ValidateInbound_Subagent3pMaxToolIterationsRejected(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"subagent_3p","name":"Bad 3p","soul":"s","executor":{"cli":"codex","cli_path":"/usr/local/bin/codex"},"max_tool_iterations":50}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestSubagent3p")
}

// ── Handler integration tests — putSandboxConfig ─────────────────────────────

// TestPutSandboxConfig_ValidateInbound_InvalidBody asserts that an empty body
// to PUT /security/sandbox-config returns 400 when validate_inbound=true.
func TestPutSandboxConfig_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)

	api.putSandboxConfig(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPutSandboxConfig_ValidateInbound_NullBody asserts null JSON body → 400.
func TestPutSandboxConfig_ValidateInbound_NullBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader("null"))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)

	api.putSandboxConfig(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── Handler integration tests — HandleExecAllowlist ──────────────────────────

// TestExecAllowlist_ValidateInbound_WrongFieldType asserts that sending wrong
// type for allowed_binaries → 400 when validate_inbound=true.
func TestExecAllowlist_ValidateInbound_WrongFieldType(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// allowed_binaries must be array of strings; sending a string instead.
	body := `{"allowed_binaries": "not-an-array"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/exec-allowlist", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleExecAllowlist(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestExecAllowlist_ValidateInbound_ValidBody asserts valid body passes.
func TestExecAllowlist_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"allowed_binaries":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/exec-allowlist", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleExecAllowlist(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ── SchemaLoader unit test ────────────────────────────────────────────────────

// TestInboundSchemaLoader_ReadsEmbeddedFile asserts the inboundSchemaLoader
// can read AgentCreateRequestMain.yaml from the embedded FS. AgentCreateRequest
// (the pre-W1 flat schema) no longer exists — the discriminated union split it
// into AgentCreateRequestMain / AgentCreateRequestSubagent /
// AgentCreateRequestSubagent3p.
func TestInboundSchemaLoader_ReadsEmbeddedFile(t *testing.T) {
	loader := inboundSchemaLoader{}
	doc, err := loader.Load("file:///AgentCreateRequestMain.yaml")
	require.NoError(t, err, "loader must read AgentCreateRequestMain.yaml from embedded FS")
	assert.NotNil(t, doc)
}

// TestCompileInboundSchema_AgentCreateRequestVariants asserts all three
// discriminated-union create-request schemas compile cleanly.
func TestCompileInboundSchema_AgentCreateRequestVariants(t *testing.T) {
	for _, name := range []string{"AgentCreateRequestMain", "AgentCreateRequestSubagent", "AgentCreateRequestSubagent3p"} {
		t.Run(name, func(t *testing.T) {
			s, err := compileInboundSchema(name)
			require.NoError(t, err)
			assert.NotNil(t, s)
		})
	}
}

// TestCompileInboundSchema_UnknownSchema asserts that an unknown schema name
// returns an error (not a panic or nil pointer).
func TestCompileInboundSchema_UnknownSchema(t *testing.T) {
	// Reset compiler state for this test to avoid cache pollution.
	// (We test with an obviously bogus name that will never match a real schema.)
	_, err := compileInboundSchema("NonExistentSchemaXYZ9999")
	assert.Error(t, err)
}

// newTestRestAPIWithValidationAndAgent is like newTestRestAPIWithValidation but
// also seeds a custom agent ("test-agent-001") into the config so that
// updateAgent handler tests can exercise the schema-validation path (the
// agent-not-found guard runs before decodeAndValidate, so we must have a real
// agent in the list).
func newTestRestAPIWithValidationAndAgent(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host:            "127.0.0.1",
			Port:            8080,
			ValidateInbound: true,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:   "test-agent-001",
					Name: "Test Agent",
					Type: config.AgentTypeCustom,
				},
			},
		},
	}
	// ADR-054: agents.list is json:"-" and is silently stripped on load — the
	// on-disk splice below no longer seeds anything; the roster now lives in
	// the entity store (seedAgentEntities below). updateAgent's existence
	// pre-check still reads cfg.Agents.List (in-memory, kept above), but its
	// persist step (store.Update) resolves the agent store — a fixture with
	// no real entity record for "test-agent-001" would 404/error there.
	minimalCfg := []byte(
		`{"version":1,"agents":{"defaults":{}},"providers":[]}`,
	)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
	}
}

// ── Handler integration tests — createSession ─────────────────────────────────

// TestCreateSession_ValidateInbound_InvalidBody asserts that POST /sessions
// with validate_inbound=true rejects a body whose "type" field is not in the
// allowed enum (chat|task|channel).
//
// BDD:
//
//	Given validate_inbound=true,
//	When POST /sessions body contains {"type":"INVALID"},
//	Then the handler returns 400 with a schema error referencing SessionCreateRequest.
//
// Traces to: fix-Q / fix-Y — handler integration test for SessionCreateRequest validation.
func TestCreateSession_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// "type" field must be one of: chat, task, channel — "INVALID" fails enum.
	body := `{"type":"INVALID"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createSessionHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"invalid enum value for 'type' must return 400")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "SessionCreateRequest",
		"error message must reference the schema name")
}

// TestCreateSession_ValidateInbound_ValidBody asserts that POST /sessions
// with validate_inbound=true accepts a body with valid optional fields.
//
// BDD:
//
//	Given validate_inbound=true and a "main" agent exists,
//	When POST /sessions body contains {"type":"chat"},
//	Then the handler does NOT return 400 (schema is valid).
//
// Note: the handler may still 400 for business-logic reasons (agent not found)
// because the test config has no agents seeded — we assert it is NOT a schema
// validation failure (no "SessionCreateRequest" in the error body).
//
// Traces to: fix-Q / fix-Y — handler integration test for SessionCreateRequest validation.
func TestCreateSession_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// Valid body — both fields conform to the schema.
	body := `{"type":"chat"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createSessionHTTP(w, r)

	// Schema validation must not reject this body — if we get 400 it must
	// not be a schema error.
	if w.Code == http.StatusBadRequest {
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
			assert.NotContains(t, resp["error"], "SessionCreateRequest",
				"valid body must not produce a schema validation 400")
		}
	}
}

// ── Handler integration tests — updateAgent ───────────────────────────────────

// TestUpdateAgent_ValidateInbound_InvalidBody asserts that PATCH /agents/{id}
// with validate_inbound=true rejects a body where "name" is not a string.
//
// BDD:
//
//	Given validate_inbound=true and agent "test-agent-001" exists,
//	When PATCH /agents/test-agent-001 body contains {"name": 42},
//	Then the handler returns 400 with a schema error referencing AgentUpdateRequest.
//
// Traces to: fix-Q / fix-Y — handler integration test for AgentUpdateRequest validation.
func TestUpdateAgent_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidationAndAgent(t)

	// "name" must be a string — sending a number violates the type constraint.
	body := `{"name": 42}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent-001", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.updateAgent(w, r, "test-agent-001")

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"wrong type for 'name' must return 400")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentUpdateRequest",
		"error message must reference the schema name")
}

// TestUpdateAgent_ValidateInbound_ValidBody asserts that PATCH /agents/{id}
// with validate_inbound=true accepts a body with a valid field.
//
// BDD:
//
//	Given validate_inbound=true and agent "test-agent-001" exists,
//	When PATCH /agents/test-agent-001 body contains {"model":"gpt-4o"},
//	Then the schema validation passes (200 or business-logic response, not a 400 schema error).
//
// Traces to: fix-Q / fix-Y — handler integration test for AgentUpdateRequest validation.
func TestUpdateAgent_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidationAndAgent(t)

	body := `{"model":"gpt-4o"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent-001", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.updateAgent(w, r, "test-agent-001")

	// Schema validation must not reject this body.
	if w.Code == http.StatusBadRequest {
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
			assert.NotContains(t, resp["error"], "AgentUpdateRequest",
				"valid body must not produce a schema validation 400")
		}
	}
}

// TestUpdateAgent_ValidateInbound_EmptyPatchRejected asserts the minProperties:1
// invariant in the AgentUpdateRequest inbound schema (fix-V).
func TestUpdateAgent_ValidateInbound_EmptyPatchRejected(t *testing.T) {
	api := newTestRestAPIWithValidationAndAgent(t)

	body := `{}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent-001", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.updateAgent(w, r, "test-agent-001")

	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"empty patch body {} must be rejected 400 by minProperties:1 in AgentUpdateRequest inbound schema; body: %s",
		w.Body.String(),
	)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentUpdateRequest",
		"error message must reference the schema name")
	assert.Contains(t, resp["error"], "minProperties",
		"error message must reference the minProperties constraint")
}

// ── Handler integration tests — HandleOnboardingProbeProvider ─────────────────

// TestProbeProvider_ValidateInbound_InvalidBody asserts that POST /onboarding/probe-provider
// with validate_inbound=true rejects a body missing the required "id" and "auth" fields.
//
// BDD:
//
//	Given validate_inbound=true,
//	When POST /onboarding/probe-provider body contains {},
//	Then the handler returns 400 with a schema error referencing ProbeProviderRequest.
//
// Traces to: fix-Q / fix-Y — handler integration test for ProbeProviderRequest validation.
func TestProbeProvider_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// {} is missing the required "id" and "auth" fields (A-CONTRACT shape).
	body := `{}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleOnboardingProbeProvider(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"missing required fields must return 400")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "ProbeProviderRequest",
		"error message must reference the schema name")
}

// TestProbeProvider_ValidateInbound_ValidBody asserts that POST /onboarding/probe-provider
// with validate_inbound=true passes schema validation for a body with required fields.
//
// BDD:
//
//	Given validate_inbound=true,
//	When POST /onboarding/probe-provider body contains {"id":"openai","auth":"api_key","api_key":"sk-test"},
//	Then the schema validation passes (not a 400 schema error).
//
// Traces to: fix-Q / fix-Y — handler integration test for ProbeProviderRequest validation.
func TestProbeProvider_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	// Required fields present and valid types — schema must accept this.
	body := `{"id":"openai","auth":"api_key","api_key":"sk-test-key-value"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.HandleOnboardingProbeProvider(w, r)

	// Schema validation must not reject this body.
	if w.Code == http.StatusBadRequest {
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
			assert.NotContains(t, resp["error"], "ProbeProviderRequest",
				"valid body must not produce a schema validation 400")
		}
	}
}

// ── PreCompileAllInboundSchemas tests ─────────────────────────────────────────

// TestPreCompileAllInboundSchemas_AllSchemasCompile verifies that every embedded
// YAML schema in inboundschemas.FS compiles without error. This exercises the
// gateway boot path where PreCompileAllInboundSchemas is called before the HTTP
// listener starts.
//
// BDD:
//
//	Given the inboundschemas.FS embed contains all component schemas,
//	When PreCompileAllInboundSchemas is called,
//	Then it returns nil (no compile errors) and InboundSchemaCompileFailures is 0.
//
// Traces to: rest_inbound_validate.go PreCompileAllInboundSchemas — gateway boot pre-compile.
func TestPreCompileAllInboundSchemas_AllSchemasCompile(t *testing.T) {
	// Reset failure counter so this test is not affected by previous test runs.
	_inboundSchemaCompileFailures.Store(0)

	err := PreCompileAllInboundSchemas()
	assert.NoError(t, err, "all embedded schemas must compile without error")

	// After a successful pre-compile, the failure counter must still be 0
	// (no compile error incremented it during the call).
	assert.Equal(t, uint64(0), InboundSchemaCompileFailures(),
		"InboundSchemaCompileFailures must be 0 after successful pre-compile")
}

// TestPreCompileAllInboundSchemas_IsDeterministic verifies that calling
// PreCompileAllInboundSchemas twice returns nil both times (idempotent due to
// the per-schema compile cache).
//
// Traces to: rest_inbound_validate.go — compileInboundSchema caches results; second call is a cache hit.
func TestPreCompileAllInboundSchemas_IsDeterministic(t *testing.T) {
	err1 := PreCompileAllInboundSchemas()
	err2 := PreCompileAllInboundSchemas()
	assert.NoError(t, err1, "first call must succeed")
	assert.NoError(t, err2, "second call (cached) must also succeed")
}

// ── Fail-closed 500 path tests ─────────────────────────────────────────────────

// TestDecodeAndValidate_SchemaCompileFailure_Returns500 asserts the fail-closed
// behavior when a handler calls decodeAndValidate with a schema name that does
// not exist in the embedded FS (simulating a server misconfiguration).
//
// BDD:
//
//	Given validateEnabled=true,
//	When decodeAndValidate is called with a schemaName that has no matching .yaml file,
//	Then it returns false, writes HTTP 500, and the body contains "inbound schema unavailable".
//	And InboundSchemaCompileFailures() is incremented.
//
// Traces to: rest_inbound_validate.go validateBodyAgainstSchema — server-side 500 fail-closed path.
func TestDecodeAndValidate_SchemaCompileFailure_Returns500(t *testing.T) {
	// Record the current failure count so we can assert it increments.
	before := InboundSchemaCompileFailures()

	var dst map[string]any
	body := `{"some":"data"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	// Use a schema name that is guaranteed to not exist in the embedded FS.
	ok := decodeAndValidate(w, r, "NonExistentSchemaXYZ_AF_9999", &dst, true)

	assert.False(t, ok, "decodeAndValidate must return false on schema compile failure")
	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"schema compile failure must produce HTTP 500 (fail-closed)")

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "inbound schema unavailable", resp["error"],
		"error body must say 'inbound schema unavailable'")

	after := InboundSchemaCompileFailures()
	assert.Greater(t, after, before,
		"InboundSchemaCompileFailures must be incremented when schema compile fails")
}
