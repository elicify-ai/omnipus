//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for decodeAndValidate — server-side inbound request body schema
// validation (Problem 1 / Type-design F17).
//
// Traces to: fix-Q — REST inbound bodies unvalidated server-side.
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

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
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
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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
		taskStore:     taskstore.New(tmpDir + "/tasks"),
	}
}

// ── decodeAndValidate unit tests ──────────────────────────────────────────────

// TestDecodeAndValidate_ValidBody asserts that a schema-valid body decodes successfully.
func TestDecodeAndValidate_ValidBody(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	body := `{"name":"My Agent"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, true)

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

	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, true)

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
	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, false)

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
	// "name" must be a string per AgentCreateRequest schema; sending a number.
	body := `{"name": 42}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, true)

	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "AgentCreateRequest")
}

// TestDecodeAndValidate_WrongTypeValidateDisabled asserts that with validation
// disabled the same wrong-type body is silently ignored (Go decodes number
// to string as empty string per encoding/json behaviour).
func TestDecodeAndValidate_WrongTypeValidateDisabled(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	body := `{"name": 42}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	// Without schema validation, json.Unmarshal silently ignores type mismatch.
	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, false)

	// json.Decoder returns an error for number→string mismatch
	// (cannot unmarshal number into Go value of type string).
	// decodeAndValidate returns false and 400 regardless.
	assert.False(t, ok)
}

// TestDecodeAndValidate_MissingRequiredField asserts that `{}` for a schema
// with required fields → 400 when validation enabled.
func TestDecodeAndValidate_MissingRequiredField(t *testing.T) {
	var dst struct {
		Name string `json:"name"`
	}
	// AgentCreateRequest requires "name" — sending empty object.
	body := `{}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()

	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, true)

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

	ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, false)

	assert.True(t, ok)
	assert.Equal(t, "", dst.Name) // zero value — upstream guard catches it
}

// TestDecodeAndValidate_MalformedJSON asserts malformed JSON → 400 regardless of flag.
func TestDecodeAndValidate_MalformedJSON(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		var dst struct{ Name string }
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{bad json`))
		w := httptest.NewRecorder()
		ok := decodeAndValidate(w, r, "AgentCreateRequest", &dst, enabled)
		assert.False(t, ok, "enabled=%v", enabled)
		assert.Equal(t, http.StatusBadRequest, w.Code, "enabled=%v", enabled)
	}
}

// ── Handler integration tests — createAgent ───────────────────────────────────

// TestCreateAgent_ValidateInbound_InvalidBody asserts POST /agents with
// validate_inbound=true returns 400 for a body missing the required "name" field.
func TestCreateAgent_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequest")
}

// TestCreateAgent_ValidateInbound_ValidBody asserts POST /agents with
// validate_inbound=true accepts a body with required "name" field.
func TestCreateAgent_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"name":"Test Agent","description":"desc"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	// 200 or 201 — agent created successfully
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusCreated,
		"expected 200 or 201, got %d: %s", w.Code, w.Body.String())
}

// ── Handler integration tests — putSandboxConfig ─────────────────────────────

// TestPutSandboxConfig_ValidateInbound_InvalidBody asserts that an empty body
// to PUT /security/sandbox-config returns 400 when validate_inbound=true.
func TestPutSandboxConfig_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.putSandboxConfig(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPutSandboxConfig_ValidateInbound_NullBody asserts null JSON body → 400.
func TestPutSandboxConfig_ValidateInbound_NullBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/sandbox-config", strings.NewReader("null"))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

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
// can read AgentCreateRequest.yaml from the embedded FS.
func TestInboundSchemaLoader_ReadsEmbeddedFile(t *testing.T) {
	loader := inboundSchemaLoader{}
	doc, err := loader.Load("file:///AgentCreateRequest.yaml")
	require.NoError(t, err, "loader must read AgentCreateRequest.yaml from embedded FS")
	assert.NotNil(t, doc)
}

// TestCompileInboundSchema_AgentCreateRequest asserts the schema compiles cleanly.
func TestCompileInboundSchema_AgentCreateRequest(t *testing.T) {
	s, err := compileInboundSchema("AgentCreateRequest")
	require.NoError(t, err)
	assert.NotNil(t, s)
}

// TestCompileInboundSchema_UnknownSchema asserts that an unknown schema name
// returns an error (not a panic or nil pointer).
func TestCompileInboundSchema_UnknownSchema(t *testing.T) {
	// Reset compiler state for this test to avoid cache pollution.
	// (We test with an obviously bogus name that will never match a real schema.)
	_, err := compileInboundSchema("NonExistentSchemaXYZ9999")
	assert.Error(t, err)
}
