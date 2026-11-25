package generated

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// #1968 R1: the generated catalogue contract retains PDF capability, but has no
// engine-owned PDF limit properties. Legacy feed members never reach the wire.
func TestCatalogPDFCapability_WithoutLimitContract(t *testing.T) {
	c, err := catalog.NewCatalog([]byte(`{
	  "schema_version":"2.0.0","version":"v2026.11.22.1",
	  "updated_at":"2026-11-22T00:00:00Z","source":"test",
	  "default_resize_limits":{"long_edge_px":7680,"max_bytes":10485760},
	  "default_document_limits":{"max_file_bytes":-1},
	  "providers":[{"id":"fixture","name":"Fixture","api":"https://api.example.test/v1",
	    "protocol":"openai-compatible","tier":"standard","auth_methods":["api_key"],
	    "document_limits":{"inline_pdf":false},
	    "models":[{"id":"pdf-reader","name":"PDF reader","input_modalities":["text","pdf"],
	      "status":"active","document_limits":"ignored"}]}]
	}`))
	require.NoError(t, err)
	served, ok := c.Served()
	require.True(t, ok)
	var wire ProvidersCatalog
	require.NoError(t, json.Unmarshal(served.Body, &wire))
	require.Len(t, wire.Providers, 1)
	require.Len(t, wire.Providers[0].Models, 1)
	assert.Equal(t, []CatalogModelInputModalities{CatalogModelInputModalitiesText, CatalogModelInputModalitiesPdf}, wire.Providers[0].Models[0].InputModalities)
	for _, shape := range []any{ProvidersCatalog{}, CatalogProvider{}, CatalogModel{}} {
		typ := reflect.TypeOf(shape)
		for i := range typ.NumField() {
			key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
			assert.NotEqual(t, "document_limits", key, typ.Name())
			assert.NotEqual(t, "default_document_limits", key, typ.Name())
		}
	}
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(served.Body, &raw))
	assert.NotContains(t, raw, "default_document_limits")
	var providers []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["providers"], &providers))
	assert.NotContains(t, providers[0], "document_limits")
	var models []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(providers[0]["models"], &models))
	assert.NotContains(t, models[0], "document_limits")
}
