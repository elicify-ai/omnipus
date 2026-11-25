// Omnipus — CatalogDocumentLimits generated-contract round-trip (spec #1968, T5b)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// BUILD-FAIL FILE — spec #1968 (read-file native PDF, FR-4). Needs the
// generated contract types below, which do not exist on the base tree; the
// file stands alone so the package's other tests still compile.
//
// Expected new symbols (spec §4.2/§4.5; declared exactly as used):
//
//	// pkg/api/generated/openapi_types.gen.go — generated from
//	// contracts/components/schemas/CatalogDocumentLimits.yaml (all-optional
//	// object; integer fields are pointers because explicit 0 means "no
//	// limit" and must stay distinguishable from absent):
//	type CatalogDocumentLimits struct {
//		InlinePDF              *bool   `json:"inline_pdf,omitempty"`
//		MaxFileBytes           *int    `json:"max_file_bytes,omitempty"`
//		MaxTotalFileBytes      *int    `json:"max_total_file_bytes,omitempty"`
//		MaxRequestBytes        *int    `json:"max_request_bytes,omitempty"`
//		MaxPagesPerFile        *int    `json:"max_pages_per_file,omitempty"`
//		MaxPagesPerRequest     *int    `json:"max_pages_per_request,omitempty"`
//		MaxDocumentsPerMessage *int    `json:"max_documents_per_message,omitempty"`
//		MaxDocumentsPerRequest *int    `json:"max_documents_per_request,omitempty"`
//	}
//
//	// the three §4.2 "Places" carry the object through the served envelope
//	// (all optional pointers — absent stays absent):
//	//   ProvidersCatalog.DefaultDocumentLimits *CatalogDocumentLimits
//	//       json:"default_document_limits,omitempty"
//	//   CatalogProvider.DocumentLimits *CatalogDocumentLimits
//	//       json:"document_limits,omitempty"
//	//   CatalogModel.DocumentLimits *CatalogDocumentLimits
//	//       json:"document_limits,omitempty"
//
// (Naming follows the existing CatalogResizeLimits precedent.) If the
// generator emits `int64` instead of `int` for these fields, the one-line
// type in the fixture below is updated with the schema — the assertion that
// matters is the wire-name round-trip, not the Go integer width.
//
// The tests pin the wire contract: marshalling the generated type emits the
// exact snake_case member names the served catalogue publishes (§4.2
// "Places"), a JSON document carrying those members round-trips through the
// type with values and tri-states intact (absent stays absent, explicit 0
// stays explicit), and — F12 — a served catalogue (Catalog.Served) carries
// the members through the generated envelope types on decode and re-marshal.

package generated

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

func intField(v int) *int { return &v }

func boolField(v bool) *bool { return &v }

// TestGeneratedCatalogDocumentLimitsType (T5b, B) round-trips a fixture
// through the generated CatalogDocumentLimits type: every §4.2 member keeps
// its exact wire name, absent members stay absent, and an explicit 0
// survives as an explicit 0 (the "no limit" statement, not a missing one).
func TestGeneratedCatalogDocumentLimitsType(t *testing.T) {
	fixture := CatalogDocumentLimits{
		InlinePDF:              boolField(false),
		MaxFileBytes:           intField(50000000),
		MaxTotalFileBytes:      intField(50000000),
		MaxRequestBytes:        intField(20000000),
		MaxPagesPerFile:        intField(100),
		MaxPagesPerRequest:     intField(100),
		MaxDocumentsPerMessage: intField(5),
		MaxDocumentsPerRequest: intField(30),
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var wire map[string]any
	if decodeErr := json.Unmarshal(data, &wire); decodeErr != nil {
		t.Fatalf("unmarshal marshalled fixture: %v", decodeErr)
	}

	// Exact wire member names per §4.2 (the served catalogue publishes
	// these; the publisher override data uses the same names).
	wantMembers := map[string]float64{
		"inline_pdf":                -1, // boolean; value checked below
		"max_file_bytes":            50000000,
		"max_total_file_bytes":      50000000,
		"max_request_bytes":         20000000,
		"max_pages_per_file":        100,
		"max_pages_per_request":     100,
		"max_documents_per_message": 5,
		"max_documents_per_request": 30,
	}
	if len(wire) != len(wantMembers) {
		t.Fatalf("marshalled members = %d (%v), want exactly %d §4.2 members", len(wire), wire, len(wantMembers))
	}
	for name, want := range wantMembers {
		got, ok := wire[name]
		if !ok {
			t.Fatalf("member %q missing from the marshalled fixture (wire names are the published contract)", name)
		}
		if name == "inline_pdf" {
			b, isBool := got.(bool)
			if !isBool || b {
				t.Fatalf("inline_pdf = %v, want false", got)
			}
			continue
		}
		f, isNum := got.(float64)
		if !isNum || f != want {
			t.Fatalf("member %q = %v, want %v", name, got, want)
		}
	}

	// Round-trip back through the type.
	var back CatalogDocumentLimits
	if decodeErr := json.Unmarshal(data, &back); decodeErr != nil {
		t.Fatalf("unmarshal into type: %v", decodeErr)
	}
	if back.InlinePDF == nil || *back.InlinePDF {
		t.Fatalf("inline_pdf round-tripped as %v, want false", back.InlinePDF)
	}
	if back.MaxFileBytes == nil || *back.MaxFileBytes != 50000000 {
		t.Fatalf("max_file_bytes round-tripped as %v, want 50000000", back.MaxFileBytes)
	}
	if back.MaxDocumentsPerMessage == nil || *back.MaxDocumentsPerMessage != 5 {
		t.Fatalf("max_documents_per_message round-tripped as %v, want 5", back.MaxDocumentsPerMessage)
	}

	// Tri-states: absent members stay absent, explicit 0 stays explicit.
	partial := CatalogDocumentLimits{MaxFileBytes: intField(0)}
	data2, err := json.Marshal(partial)
	if err != nil {
		t.Fatalf("marshal partial: %v", err)
	}
	var wire2 map[string]any
	if err := json.Unmarshal(data2, &wire2); err != nil {
		t.Fatalf("unmarshal partial: %v", err)
	}
	if _, present := wire2["max_file_bytes"]; !present {
		t.Fatal("an explicit 0 must marshal as a present member (0 = no limit is a statement, §4.2)")
	}
	if f, _ := wire2["max_file_bytes"].(float64); f != 0 {
		t.Fatalf("max_file_bytes marshalled as %v, want 0", wire2["max_file_bytes"])
	}
	if len(wire2) != 1 {
		t.Fatalf("partial fixture marshalled %d members (%v), want exactly the one stated field", len(wire2), wire2)
	}
	var back2 CatalogDocumentLimits
	if err := json.Unmarshal(data2, &back2); err != nil {
		t.Fatalf("unmarshal partial into type: %v", err)
	}
	if back2.MaxFileBytes == nil || *back2.MaxFileBytes != 0 {
		t.Fatalf("explicit 0 round-tripped as %v, want a pointer to 0", back2.MaxFileBytes)
	}
	if back2.InlinePDF != nil {
		t.Fatalf("absent inline_pdf round-tripped as %v, want nil", back2.InlinePDF)
	}
}

// servedLimitsFixture builds a catalogue whose document states
// document_limits at all three §4.2 places (and leaves specific fields
// absent at each place to exercise the tri-state through the envelope).
func servedLimitsFixture(t *testing.T) *catalog.Catalog {
	t.Helper()
	doc := map[string]any{
		"schema_version": "2.0.0",
		"version":        "v2026.11.22.1",
		"updated_at":     "2026-11-22T00:00:00Z",
		"source":         "test",
		"default_resize_limits": map[string]any{
			"long_edge_px": 7680, "max_bytes": 10485760,
		},
		"default_document_limits": map[string]any{
			"max_file_bytes":       50000000,
			"max_total_file_bytes": 50000000,
			"max_pages_per_file":   100,
		},
		"providers": []map[string]any{{
			"id":              "fx/served-limits-provider",
			"name":            "Fixture Served Limits Provider",
			"api":             "https://api.example.test/v1",
			"protocol":        "openai-compatible",
			"tier":            "standard",
			"auth_methods":    []string{"api_key"},
			"document_limits": map[string]any{"max_request_bytes": 20000000, "inline_pdf": false},
			"models": []map[string]any{{
				"id":               "fx/served-limits-model",
				"name":             "Fixture Served Limits Model",
				"context_window":   128000,
				"input_modalities": []string{"text", "pdf"},
				"tool_call":        true,
				"status":           "active",
				"document_limits": map[string]any{
					"max_documents_per_message": 5,
					"max_documents_per_request": 30,
					"max_pages_per_request":     0, // explicit no-limit statement
				},
			}},
		}},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	c, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	return c
}

// envelopeLimits reads a *CatalogDocumentLimits off an envelope member and
// returns it flattened to (inlinePDF *bool, members map[string]float64 with
// inline_pdf excluded). A nil pointer reports ok=false.
func envelopeLimits(t *testing.T, lim *CatalogDocumentLimits) (*bool, map[string]float64, bool) {
	t.Helper()
	if lim == nil {
		return nil, nil, false
	}
	raw, err := json.Marshal(lim)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	members := make(map[string]float64, len(wire))
	var inline *bool
	for name, got := range wire {
		if name == "inline_pdf" {
			b, isBool := got.(bool)
			require.True(t, isBool, "inline_pdf must marshal as a boolean, got %#v", got)
			inline = &b
			continue
		}
		f, ok := got.(float64)
		require.True(t, ok, "member %q must marshal as a number, got %#v", name, got)
		members[name] = f
	}
	return inline, members, true
}

// TestGeneratedCatalogDocumentLimitsServedEnvelope (T5b, B; F12) — the
// SERVED catalogue document carries document_limits through the generated
// envelope: Catalog.Served's body decodes into ProvidersCatalog with all
// three places intact (values, wire names, tri-states), and the decoded
// envelope re-marshals to the same members — the SPA-side round-trip.
func TestGeneratedCatalogDocumentLimitsServedEnvelope(t *testing.T) {
	c := servedLimitsFixture(t)
	served, ok := c.Served()
	require.True(t, ok, "NewCatalog must publish a served snapshot")

	var envelope ProvidersCatalog
	require.NoError(t, json.Unmarshal(served.Body, &envelope),
		"the served body must decode into the generated envelope")

	// Default place: values and tri-states intact.
	inline, members, present := envelopeLimits(t, envelope.DefaultDocumentLimits)
	require.True(t, present, "the served envelope must carry default_document_limits")
	require.Nil(t, inline, "the default row states no inline_pdf — absent stays absent")
	require.Equal(t, map[string]float64{
		"max_file_bytes":       50000000,
		"max_total_file_bytes": 50000000,
		"max_pages_per_file":   100,
	}, members)

	// Provider place.
	require.NotEmpty(t, envelope.Providers)
	providerInline, providerMembers, providerPresent := envelopeLimits(t, envelope.Providers[0].DocumentLimits)
	require.True(t, providerPresent, "the served envelope must carry the provider's document_limits")
	require.NotNil(t, providerInline)
	require.False(t, *providerInline, "inline_pdf false must survive the envelope")
	require.Equal(t, map[string]float64{"max_request_bytes": 20000000}, providerMembers)

	// Model place, including the explicit 0 that must stay explicit.
	require.NotEmpty(t, envelope.Providers[0].Models)
	modelInline, modelMembers, modelPresent := envelopeLimits(t, envelope.Providers[0].Models[0].DocumentLimits)
	require.True(t, modelPresent, "the served envelope must carry the model's document_limits")
	require.Nil(t, modelInline)
	require.Equal(t, map[string]float64{
		"max_documents_per_message": 5,
		"max_documents_per_request": 30,
		"max_pages_per_request":     0,
	}, modelMembers, "the explicit 0 must survive as a stated 0 (no-limit statement)")

	// Re-marshal the decoded envelope and compare the document_limits
	// members semantically against the served body — names, values and
	// tri-states must survive the SPA-side round-trip.
	remarshalled, err := json.Marshal(envelope)
	require.NoError(t, err)
	var servedMap, envelopeMap map[string]any
	require.NoError(t, json.Unmarshal(served.Body, &servedMap))
	require.NoError(t, json.Unmarshal(remarshalled, &envelopeMap))
	for _, path := range []string{"default_document_limits"} {
		require.Equal(t, servedMap[path], envelopeMap[path],
			"remarshalled envelope must keep %s identical", path)
	}
	servedProviders, ok := servedMap["providers"].([]any)
	require.True(t, ok, "served providers must be an array")
	envelopeProviders, ok := envelopeMap["providers"].([]any)
	require.True(t, ok, "envelope providers must be an array")
	require.NotEmpty(t, servedProviders, "served fixture must contain its provider")
	require.Len(t, envelopeProviders, len(servedProviders))
	servedProvider, ok := servedProviders[0].(map[string]any)
	require.True(t, ok, "served provider must be an object")
	envelopeProvider, ok := envelopeProviders[0].(map[string]any)
	require.True(t, ok, "envelope provider must be an object")
	require.Equal(t, servedProvider["document_limits"], envelopeProvider["document_limits"],
		"remarshalled envelope must keep the provider document_limits identical")
	servedModels, ok := servedProvider["models"].([]any)
	require.True(t, ok, "served models must be an array")
	envelopeModels, ok := envelopeProvider["models"].([]any)
	require.True(t, ok, "envelope models must be an array")
	require.NotEmpty(t, servedModels, "served fixture must contain its model")
	require.Len(t, envelopeModels, len(servedModels))
	servedModel, ok := servedModels[0].(map[string]any)
	require.True(t, ok, "served model must be an object")
	envelopeModel, ok := envelopeModels[0].(map[string]any)
	require.True(t, ok, "envelope model must be an object")
	require.Equal(t, servedModel["document_limits"], envelopeModel["document_limits"],
		"remarshalled envelope must keep the model document_limits identical")
}
