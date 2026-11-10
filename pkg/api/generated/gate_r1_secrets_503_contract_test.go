package generated

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestContract_SecretsOperationsDeclareUnavailable is Train 2 gate r1
// architect F06 (Hard Constraint #8): the K6 listing and the K49 options
// handlers answer 503 ErrorResponse when the credential store cannot be
// opened, read or saved (pkg/gateway/rest_secrets.go), so both operations
// must declare that response. getSecretRequest's 503 is the positive control.
func TestContract_SecretsOperationsDeclareUnavailable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(contractsDir(), "openapi.yaml"))
	require.NoError(t, err)
	var doc struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Responses map[string]any `yaml:"responses"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	responses := map[string]map[string]any{}
	for _, item := range doc.Paths {
		for _, raw := range item {
			op, isOperation := raw.(map[string]any) // path-level parameters are a list
			if !isOperation {
				continue
			}
			id, _ := op["operationId"].(string)
			declared, _ := op["responses"].(map[string]any)
			if id != "" {
				responses[id] = declared
			}
		}
	}
	for _, id := range []string{"getSecretRequest", "listSecrets", "updateSecretOptions"} {
		ops, found := responses[id]
		require.True(t, found, "instrument: operation %s must exist in openapi.yaml", id)
		resp, declared := ops["503"]
		require.True(t, declared, "operation %s returns 503 ErrorResponse when the store is unavailable and must declare it", id)
		// gate r2 CHECK-r2: the declared 503 must BE the ErrorResponse body
		// the handlers write, not merely exist.
		require.Equal(t, "#/components/schemas/ErrorResponse", errorSchemaRef(t, resp, doc.Components.Responses),
			"operation %s must declare its 503 body as application/json ErrorResponse", id)
	}
}

// errorSchemaRef resolves a response (inline or a components/responses $ref)
// to its application/json schema $ref.
func errorSchemaRef(t *testing.T, resp any, shared map[string]any) string {
	t.Helper()
	body, ok := resp.(map[string]any)
	require.True(t, ok, "a response is an object")
	if ref, isRef := body["$ref"].(string); isRef {
		const prefix = "#/components/responses/"
		require.True(t, strings.HasPrefix(ref, prefix), "unexpected response ref %q", ref)
		body, ok = shared[strings.TrimPrefix(ref, prefix)].(map[string]any)
		require.True(t, ok, "response ref %q must resolve", ref)
	}
	content, _ := body["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	ref, _ := schema["$ref"].(string)
	return ref
}
