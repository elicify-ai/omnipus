// Wire-format conformance tests for the session REST handlers.
//
// These tests close the bug class that shipped to the public IP build on
// 2026-05-21: getSessionMessages and jsonSessionDetail emit
// []session.TranscriptEntry into JSON fields typed as []gen.Message on the
// wire. A jim session with 44 tool_call entries failed SPA Zod validation
// because the Message.yaml type enum was missing "tool_call" and
// "turn_canceled". The fix in contracts/components/schemas/Message.yaml
// added the missing enum values plus the cancel-specific fields.
//
// These tests pin the round-trip: append a TranscriptEntry of every
// EntryType through the real UnifiedStore, call the real handler, and
// validate the response JSON against the compiled Message.yaml schema.
// If a new EntryType is added without a schema update, CI fails here.

package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// loadMessageSchema compiles the Message.yaml component schema once per test
// binary. We resolve the schema file path relative to this test file's
// location so the test works regardless of the cwd a test runner uses.
func loadMessageSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	thisFile := gatewayTestCallerFile(t)
	contractsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")
	loader := newYAMLSchemaLoader(t)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(loader)

	schemaPath := filepath.Join(contractsDir, "Message.yaml")
	schemaURL := "file://" + schemaPath
	schema, err := compiler.Compile(schemaURL)
	require.NoError(t, err, "must be able to compile Message.yaml")
	return schema
}

func gatewayTestCallerFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(1)
	require.True(t, ok, "runtime.Caller must resolve the calling test file")
	return file
}

// newYAMLSchemaLoader returns a jsonschema.URLLoader that resolves file://
// URLs by parsing YAML files into map[string]any. The default loader only
// reads JSON; this wrapper makes the test work with our YAML schemas.
//
// Mirrors the pattern in pkg/api/generated/contract_test.go::yamlLoader.
func newYAMLSchemaLoader(t *testing.T) jsonschema.URLLoader {
	t.Helper()
	return &yamlURLLoader{t: t}
}

type yamlURLLoader struct{ t *testing.T }

func (l *yamlURLLoader) Load(url string) (any, error) {
	path := strings.TrimPrefix(url, "file://")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("yamlURLLoader.Load: %w", err)
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("yamlURLLoader.Load: %w", err)
	}
	return jsonifyYAML(v), nil
}

// jsonifyYAML converts the map[interface{}]interface{} that yaml.v3 produces
// into map[string]interface{} that jsonschema/v6 expects. Recursive.
func jsonifyYAML(v any) any {
	switch v := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			ks, _ := k.(string)
			out[ks] = jsonifyYAML(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = jsonifyYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = jsonifyYAML(val)
		}
		return out
	default:
		return v
	}
}
