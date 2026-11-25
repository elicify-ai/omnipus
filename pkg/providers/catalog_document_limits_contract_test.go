package providers_test

// #1968 FR-4, spec §4.2/§4.5: the catalogue's private parse/served shapes
// must preserve the generated CatalogDocumentLimits contract. Keep this
// consumer-side test OUTSIDE pkg/providers/catalog: that domain package
// must not import generated wire types (TestCatalog_ImportBoundary).

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

func TestCatalogDocumentLimits_ServedMatchesGeneratedContract(t *testing.T) {
	// Names come from contracts/components/schemas/CatalogDocumentLimits.yaml,
	// not the handwritten DTO. Reflection catches generated additions too.
	wantFields := map[string]string{
		"inline_pdf":                "InlinePDF",
		"max_file_bytes":            "MaxFileBytes",
		"max_total_file_bytes":      "MaxTotalFileBytes",
		"max_request_bytes":         "MaxRequestBytes",
		"max_pages_per_file":        "MaxPagesPerFile",
		"max_pages_per_request":     "MaxPagesPerRequest",
		"max_documents_per_message": "MaxDocumentsPerMessage",
		"max_documents_per_request": "MaxDocumentsPerRequest",
	}
	typ := reflect.TypeOf(generated.CatalogDocumentLimits{})
	gotFields := make(map[string]string, typ.NumField())
	for i := range typ.NumField() {
		field := typ.Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		require.NotEmpty(t, key, "every generated member needs a JSON key")
		gotFields[key] = field.Name
	}
	require.Equal(t, wantFields, gotFields, "contract field/key coverage must remain complete")
	type fixture struct {
		name   string
		limits map[string]any
	}
	cases := []fixture{{name: "absent"}}
	positive := map[string]any{"inline_pdf": false}
	zeros := map[string]any{"inline_pdf": false}
	for i := range typ.NumField() {
		key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if key == "inline_pdf" {
			cases = append(cases, fixture{"inline-true", map[string]any{key: true}}, fixture{"inline-false", map[string]any{key: false}})
			continue
		}
		// Distinct, small positive fixture integers expose swapped members;
		// these are test data, never provider limits or pointer-type oracles.
		value := (i + 1) * 17
		positive[key], zeros[key] = value, 0
		cases = append(cases, fixture{key + "-positive", map[string]any{key: value}}, fixture{key + "-zero", map[string]any{key: 0}})
	}
	cases = append(cases, fixture{"all-positive-and-false", positive}, fixture{"all-zero-and-false", zeros})
	for _, level := range []string{"default", "provider", "model"} {
		for _, tc := range cases {
			t.Run(level+"/"+tc.name, func(t *testing.T) {
				// Serialize via the generated contract, exercise the catalogue's
				// handwritten parse AND served shapes, then decode back through
				// the generated contract. JSON avoids *int64 versus *int coupling.
				var input *generated.CatalogDocumentLimits
				if tc.limits != nil {
					raw, err := json.Marshal(tc.limits)
					require.NoError(t, err)
					input = &generated.CatalogDocumentLimits{}
					require.NoError(t, json.Unmarshal(raw, input))
					catalogLimitsContractMembers(t, tc.limits, input)
				}
				c := catalog.New()
				require.NoError(t, c.Apply(catalogLimitsContractFixture(t, level, input)))
				served, ok := c.Served()
				require.True(t, ok, "real Apply must publish the serving projection")
				var wire generated.ProvidersCatalog
				require.NoError(t, json.Unmarshal(served.Body, &wire))
				require.Len(t, wire.Providers, 1)
				require.Len(t, wire.Providers[0].Models, 1)
				levels := map[string]*generated.CatalogDocumentLimits{
					"default": wire.DefaultDocumentLimits, "provider": wire.Providers[0].DocumentLimits, "model": wire.Providers[0].Models[0].DocumentLimits,
				}
				rawLevels := catalogLimitsContractRawLevels(t, served.Body)
				for name, got := range levels {
					if name != level || tc.limits == nil {
						assert.Nil(t, got, "served raw levels must not acquire resolved/default values: %s", name)
						assert.Nil(t, rawLevels[name], "absent raw object must be omitted, not null or empty: %s", name)
						continue
					}
					require.NotNil(t, got, "a stated field cannot disappear from the served level")
					catalogLimitsContractMembers(t, tc.limits, got)
					wantJSON, err := json.Marshal(tc.limits)
					require.NoError(t, err)
					// Decoding into generated types alone would discard an extra
					// handwritten JSON key and falsely report parity.
					assert.JSONEq(t, string(wantJSON), string(rawLevels[name]), "actual served object has exactly the contract members")
				}
			})
		}
	}
}

func catalogLimitsContractMembers(t *testing.T, want map[string]any, got *generated.CatalogDocumentLimits) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	var wantMembers, gotMembers map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wantJSON, &wantMembers))
	require.NoError(t, json.Unmarshal(gotJSON, &gotMembers))
	assert.Equal(t, wantMembers, gotMembers, "exact JSON keys/values/presence, including explicit zero and false")
}

func catalogLimitsContractRawLevels(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &envelope))
	var providers []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["providers"], &providers))
	require.Len(t, providers, 1)
	var models []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(providers[0]["models"], &models))
	require.Len(t, models, 1)
	return map[string]json.RawMessage{
		"default": envelope["default_document_limits"], "provider": providers[0]["document_limits"], "model": models[0]["document_limits"],
	}
}

func catalogLimitsContractFixture(t *testing.T, level string, limits *generated.CatalogDocumentLimits) []byte {
	t.Helper()
	model := map[string]any{"id": "fx/contract-model", "name": "Contract Model", "context_window": 128000, "input_modalities": []string{"text", "pdf"}, "tool_call": true, "status": "active"}
	provider := map[string]any{"id": "fx/contract-provider", "name": "Contract Provider", "api": "https://api.example.test/v1", "protocol": "openai-compatible", "tier": "standard", "auth_methods": []string{"api_key"}, "models": []map[string]any{model}}
	doc := map[string]any{
		"schema_version": "2.0.0", "version": "v2026.11.22.1", "updated_at": "2026-11-22T00:00:00Z", "source": "test",
		"default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760}, "providers": []map[string]any{provider},
	}
	if limits != nil {
		switch level {
		case "default":
			doc["default_document_limits"] = limits
		case "provider":
			provider["document_limits"] = limits
		case "model":
			model["document_limits"] = limits
		default:
			t.Fatalf("unknown contract fixture level %q", level)
		}
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	return raw
}
