// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// reasoning_roundtrip_test.go is the RED test for C6/D25: the catalog's
// per-model `reasoning`/`reasoning_options` fields must survive Omnipus's
// typed catalog round-trip (parse.go DTO -> document.go::Model -> served.go
// envelope re-serialize) instead of being silently dropped (spec
// docs/internal/specs/thinking-reasoning-spec.md, Section 1 C6, Section 2.2
// row "pkg/providers/catalog/parse.go + document.go::Model + served.go",
// BDD scenario "Catalog fields survive Omnipus's typed catalog round-trip",
// Section 16 test 23; RED against pre-plumbing code: fields dropped before,
// present after).
//
// Oracle independence: the expected values are the document's own values —
// the round-trip is asserted to be identity for these two fields. The order
// fixture list ["minimal","low","medium","high"] is ascending EFFORT order as
// the catalog publishes it (C6: Omnipus preserves catalog order, never
// re-sorts); it is deliberately NOT ascending lexical order (h,i,l,m), so any
// re-sort scrambles it and dies here. The upstream schema is owned by the
// catalog repo (elicify-ai/omnipus-provider-catalog PR #45, D25 — the repo is
// ours); Omnipus adds no model metadata of its own (D7: no hardcoded list —
// this test invents no level names beyond what the fixture document carries).
package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCatalogRoundTrip_ReasoningFieldsSurvive asserts the C6 round-trip:
// document rows carrying reasoning/reasoning_options are served with the same
// values and the same order — nothing is dropped by the typed plumbing.
func TestCatalogRoundTrip_ReasoningFieldsSurvive(t *testing.T) {
	// Given: a catalog document whose model rows carry reasoning and
	// reasoning_options under schema_version "2.0.0" (no version bump), on
	// the shared FR-027 conformance fixture so every OTHER validation
	// (URLs, modalities, statuses) is guaranteed to pass — the test stays
	// focused on exactly the two fields.
	m := fixtureMap(t)
	provs := m["providers"].([]any)
	require.NotEmpty(t, provs)
	p0 := provs[0].(map[string]any)
	models := p0["models"].([]any)
	require.GreaterOrEqual(t, len(models), 2, "fixture must carry two model rows: one populated, one control")

	// Ascending effort order per the catalog's own publishing convention
	// (CatalogModel.yaml: "IN ASCENDING EFFORT ORDER — the catalog is the
	// source of order; Omnipus preserves it, does not re-sort").
	const wantOptions = `["minimal","low","medium","high"]`
	m0 := models[0].(map[string]any)
	m0["reasoning"] = true
	m0["reasoning_options"] = json.RawMessage(wantOptions)
	// models[1] stays untouched: the control row.

	raw, err := json.Marshal(m)
	require.NoError(t, err)

	// When: the document is parsed, the typed models are built, and the
	// served envelope is serialized — the real typed plumbing, no mocks.
	doc, err := ParseDocument(raw)
	require.NoError(t, err, "fixture + reasoning fields must still be a valid 2.0.0 document")
	pair, err := buildServed(doc, ServedEmbedded, time.Now())
	require.NoError(t, err)

	var env map[string]any
	require.NoError(t, json.Unmarshal(pair.body, &env))

	// Then: schema_version stays "2.0.0" — the fields are additive under the
	// existing version, no version bump (C6).
	assert.Equal(t, "2.0.0", env["schema_version"], "C6: additive fields, no version bump")

	// ...and the served response carries the same fields with the same
	// values and order — nothing is dropped by the typed plumbing.
	servedProviders, ok := env["providers"].([]any)
	require.True(t, ok, "served envelope must carry a providers array")
	require.NotEmpty(t, servedProviders)
	servedModels, ok := servedProviders[0].(map[string]any)["models"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(servedModels), 2)

	populated := servedModels[0].(map[string]any)
	control := servedModels[1].(map[string]any)

	// Exact-value assertions on the populated row.
	assert.Equal(t, true, populated["reasoning"],
		"reasoning must survive the typed round-trip unchanged (pre-plumbing code drops it)")
	assert.Equal(t, []any{"minimal", "low", "medium", "high"}, populated["reasoning_options"],
		"reasoning_options must survive unchanged AND in the catalog's ascending order (no re-sort)")

	// The control row (fields absent from the document): absence is
	// equivalent to false (CatalogModel.yaml) — the served row must not
	// claim reasoning true nor carry options. Whether the envelope emits
	// "reasoning": false or omits the key is the served shape's business
	// (both satisfy absence≡false); neither may claim a value.
	assert.NotEqual(t, true, control["reasoning"],
		"the control row must not claim reasoning true")
	opts, hasOpts := control["reasoning_options"]
	if hasOpts {
		assert.Empty(t, opts, "the control row must carry no reasoning options")
	}

	// Regression guard (Section 16 regression table, catalog row): fields
	// served when present, NOTHING ELSE changes — the pre-existing tool_call
	// field still survives the round-trip byte-identically.
	fixtureToolCall := m0["tool_call"]
	assert.Equal(t, fixtureToolCall, populated["tool_call"],
		"the additive reasoning fields must not disturb the existing tool_call round-trip")
}
