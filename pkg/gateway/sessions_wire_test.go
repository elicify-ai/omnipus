// Wire-format conformance tests for the session REST handlers.
//
// These tests preserve the schema regression checks from 2026-05-21, when
// persisted tool/cancel records were missing from the public Message schema.
// Producers now use generated public Message and paged SessionDetail values,
// never raw TranscriptEntry serialization or a test-written detail envelope.
//
// Append canonical records through the real UnifiedStore, invoke the actual
// public handler/paged writer, then validate its bytes against current schemas
// AND available-history semantics. A metadata-only unavailable detail is not a
// successful empty transcript. Private canonical provenance stays off the wire.

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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

// Validate REAL producer bytes, not a hand-written expected wire object. Keep
// the historical full-schema check and require a genuinely available page;
// schema-only validation would also accept a metadata-only failed-history reply.
func requirePagedSessionDetailWire(t *testing.T, body []byte) gen.SessionDetail {
	t.Helper()
	thisFile := gatewayTestCallerFile(t)
	contractsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(newYAMLSchemaLoader(t))
	schema, err := compiler.Compile("file://" + filepath.Join(contractsDir, "SessionDetail.yaml"))
	require.NoError(t, err, "must compile the current SessionDetail contract")
	var envelope any
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.NoError(t, schema.Validate(envelope), "actual paged detail must satisfy SessionDetail.yaml; body=%s", body)
	var detail gen.SessionDetail
	require.NoError(t, json.Unmarshal(body, &detail))
	require.Equal(t, "available", string(detail.HistoryState.Status), "healthy canonical fixture must return a real page, not unavailable metadata")
	require.Nil(t, detail.HistoryState.Reason)
	require.Nil(t, detail.HistoryState.Message)
	require.NotNil(t, detail.Messages)
	require.NotNil(t, detail.PageInfo)
	require.NotNil(t, detail.HasEarlier)
	require.NotNil(t, detail.HasLater)
	require.EqualValues(t, 40, detail.PageInfo.Limit, "D10 default latest-page target")
	require.EqualValues(t, 20, detail.PageInfo.MinRows, "D10 default floor")
	require.NotContains(t, string(body), `"has_earlier_messages"`, "retired envelope field must not return")
	return detail
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
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
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
