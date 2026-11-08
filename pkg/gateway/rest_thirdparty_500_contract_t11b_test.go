package gateway

// Architect F6, commissioned with founder Q12 A. This author does not edit
// contracts/generated files: the developer must declare the existing 500 path.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestThirdPartySettings_PutDeclaresInternalErrorContract_F6(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(file), "..", "..", "contracts", "openapi.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok, "instrument: the real OpenAPI document contains paths")
	settings, ok := paths["/settings/third-party-agents"].(map[string]any)
	require.True(t, ok, "instrument: locate the actual Settings operation, not an empty search")
	put, ok := settings["put"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "updateThirdPartyAgentSettings", put["operationId"])
	responses, ok := put["responses"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, responses, "200", "instrument: the same lookup sees the existing success response")
	failure, ok := responses["500"]
	if !ok {
		t.Fatal("BLOCKED: PUT /settings/third-party-agents 500 response not implemented — required by architect F6 / Q12 A")
	}
	response, ok := failure.(map[string]any)
	require.True(t, ok)
	// Architect F6 explicitly chooses the shared internal-error response.
	require.Equal(t, "#/components/responses/500InternalServerError", response["$ref"])
}
