package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// F03 / K5 binding ruling: the instrument compiles the actual operation's
// declared response schema, including its existing component references. It
// never substitutes the proposed union or merely scans schema text.
func selectionK5OperationSchema(t *testing.T, method string, status int) (*jsonschema.Schema, error) {
	t.Helper()
	path, operation := "/secret-requests/{id}", "getSecretRequest"
	if method == http.MethodPost {
		path, operation = path+"/submit", "submitSecretRequest"
	}
	apiPath := filepath.Join(filepath.Dir(gatewayTestCallerFile(t)), "..", "..", "contracts", "openapi.yaml")
	raw, err := os.ReadFile(apiPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok)
	item, ok := paths[path].(map[string]any)
	require.True(t, ok, "K5 operation path exists")
	op, ok := item[strings.ToLower(method)].(map[string]any)
	require.True(t, ok)
	require.Equal(t, operation, op["operationId"], "instrument targets the actual operation, not a standalone schema")
	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)
	if _, declared := responses[fmt.Sprint(status)]; !declared {
		return nil, fmt.Errorf("operation %s does not declare its actual HTTP %d response", operation, status)
	}
	pointer := "#/paths/" + strings.ReplaceAll(strings.ReplaceAll(path, "~", "~0"), "/", "~1") +
		"/" + strings.ToLower(method) + "/responses/" + fmt.Sprint(status) + "/content/application~1json/schema"
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(newYAMLSchemaLoader(t))
	return compiler.Compile("file://" + filepath.ToSlash(apiPath) + pointer)
}

func selectionK5ValidateActualResponse(t *testing.T, method string, response *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, "application/json", response.Header().Get("Content-Type"))
	var payload any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload), "actual authenticated handler returned JSON")
	schema, err := selectionK5OperationSchema(t, method, response.Code)
	if assert.NoError(t, err, "actual authenticated %s HTTP %d must have an operation-declared response schema", method, response.Code) {
		assert.NoError(t, schema.Validate(payload), "actual authenticated %s HTTP %d body violates its operation contract: %s", method, response.Code, response.Body.String())
	}
}

func TestWU4Selection_K5ResponseValidatorRejectsMalformedControls(t *testing.T) {
	state, err := selectionK5OperationSchema(t, http.MethodPost, http.StatusOK)
	require.NoError(t, err)
	valid := map[string]any{"request_id": "validator-control", "state": "submitted", "updated_at": "2026-10-17T12:00:00Z", "reference_names": []any{"control.v1"}}
	require.NoError(t, state.Validate(valid), "instrument accepts a complete state")
	for _, required := range []string{"request_id", "state", "updated_at"} {
		invalid := make(map[string]any, len(valid))
		for key, value := range valid {
			if key != required {
				invalid[key] = value
			}
		}
		assert.Error(t, state.Validate(invalid), "component requirement %s cannot be weakened to admit unavailable responses", required)
	}
	invalid := map[string]any{"request_id": "validator-control", "state": "submitted", "updated_at": "not-a-date"}
	assert.Error(t, state.Validate(invalid), "instrument enforces date-time, not just string type")
	invalid = map[string]any{"request_id": "validator-control", "state": "submitted", "updated_at": "2026-10-17T12:00:00Z", "error": "mixed envelope"}
	assert.Error(t, state.Validate(invalid), "SecretRequestState forbids additional properties; variants stay disjoint")
	errorResponse, err := selectionK5OperationSchema(t, http.MethodPost, http.StatusInternalServerError)
	require.NoError(t, err)
	require.NoError(t, errorResponse.Validate(map[string]any{"error": "service unavailable"}))
	assert.Error(t, errorResponse.Validate(map[string]any{}), "ErrorResponse still requires error")
	assert.Error(t, state.Validate(map[string]any{"error": "service unavailable"}), "an unavailable body is not committed state")
	assert.Error(t, errorResponse.Validate(valid), "committed state is not the error-only alternative")
}

// This is a structural contract requirement, not the runtime verdict: the
// authenticated response tests separately execute and validate both variants.
func TestWU4Selection_K5BindingShapeUsesOnlyExistingInternalComponents(t *testing.T) {
	apiPath := filepath.Join(filepath.Dir(gatewayTestCallerFile(t)), "..", "..", "contracts", "openapi.yaml")
	raw, err := os.ReadFile(apiPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok, "K5 contract paths must be an object")
	for _, method := range []string{"post", "get"} {
		t.Run(method, func(t *testing.T) {
			path := "/secret-requests/{id}"
			if method == "post" {
				path += "/submit"
			}
			item, ok := paths[path].(map[string]any)
			require.True(t, ok, "K5 path %s must be an object", path)
			op, ok := item[method].(map[string]any)
			require.True(t, ok, "K5 operation %s %s must be an object", method, path)
			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok, "K5 operation responses must be an object")
			response, ok := responses["503"].(map[string]any)
			require.True(t, ok, "binding ruling declares GET and POST unavailable HTTP 503")
			content, ok := response["content"].(map[string]any)
			require.True(t, ok, "K5 503 response content must be an object")
			jsonContent, ok := content["application/json"].(map[string]any)
			require.True(t, ok, "K5 503 must declare application/json content")
			schema := jsonContent["schema"]
			if method == "post" {
				assert.Equal(t, map[string]any{"oneOf": []any{
					map[string]any{"$ref": "#/components/schemas/SecretRequestState"},
					map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
				}}, schema, "binding inline union: no wrapper, discriminator, new wire type or weakened component")
			} else {
				assert.Equal(t, map[string]any{"$ref": "#/components/schemas/ErrorResponse"}, schema)
			}
		})
	}
}
