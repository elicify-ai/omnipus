package effortdialect

// effortdialect_red_test.go — RED tests for the encoder table and the served
// filter, against the scaffold's own API (CHECK-06: "the encoder-table
// scaffold can return no encoding/filter result without a direct table/filter
// test detecting it").
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-17, S-18, S-19 (the route set the table must know), S-45
// (FilterServed), FR-037a, FR-049.
//
// RED reason: the scaffold's Default table has no rows, Register stores
// nowhere, EncodingFor reports nothing, and FilterServed returns
// (nil, false) for every input — every table lookup and every served row
// fails until GREEN registers the real routes and implements the filter.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The route rows the spec's S-17/S-18 matrices need. GREEN registers exactly
// these encodings; the strings are this test's contract spelling.
var (
	routeOpenAIChat  = Route{Protocol: "openai-chat", ProviderID: "openai", ModelFamily: "gpt"}
	routeOpenRouter  = Route{Protocol: "openai-chat", ProviderID: "openrouter", ModelFamily: "deepseek-v4.1-flash"}
	routeOpenRtrSw   = Route{Protocol: "openai-chat", ProviderID: "openrouter", ModelFamily: "minimax-m3"}
	routeCodex       = Route{Protocol: "openai-responses", ProviderID: "openai-chatgpt", ModelFamily: "gpt"}
	routeAnthropic   = Route{Protocol: "anthropic-messages", ProviderID: "anthropic", ModelFamily: "claude"}
	routeDeepseekNat = Route{Protocol: "openai-chat", ProviderID: "deepseek", ModelFamily: "deepseek-native"}
	routeZaiNat      = Route{Protocol: "openai-chat", ProviderID: "z-ai", ModelFamily: "glm"}
	routeBedrockAnth = Route{Protocol: "bedrock-converse", ProviderID: "bedrock", ModelFamily: "claude"}
	routeBedrockNova = Route{Protocol: "bedrock-converse", ProviderID: "bedrock", ModelFamily: "nova"}
	routeBedrockOthr = Route{Protocol: "bedrock-converse", ProviderID: "bedrock", ModelFamily: "titan"}
	routeGeneric     = Route{Protocol: "openai-chat", ProviderID: "example-compat", ModelFamily: "generic-levels-m"}
)

// TestT_P1_S17_TableKnowsEachRoute pins the encoder table's route rows: every
// route of the S-17/S-18 matrices is registered with the encoding it can
// actually serialise, and an unregistered family reports no row.
// Traces to: S-17, S-18, FR-037a, T-P1 row 8.
func TestT_P1_S17_TableKnowsEachRoute(t *testing.T) {
	tbl := &Table{}
	tbl.Register(routeOpenAIChat, Encoding{Levels: true, Switch: false})
	tbl.Register(routeOpenRouter, Encoding{Levels: true, Switch: true})
	tbl.Register(routeOpenRtrSw, Encoding{Levels: false, Switch: true})
	tbl.Register(routeCodex, Encoding{Levels: true, Switch: false})
	tbl.Register(routeAnthropic, Encoding{Levels: true, Switch: true})
	tbl.Register(routeDeepseekNat, Encoding{Levels: true, Switch: true})
	tbl.Register(routeZaiNat, Encoding{Levels: true, Switch: true})
	tbl.Register(routeBedrockAnth, Encoding{Levels: true, Switch: false})
	tbl.Register(routeBedrockNova, Encoding{Levels: true, Switch: false})
	tbl.Register(routeGeneric, Encoding{Levels: true, Switch: false})

	rows := []struct {
		name string
		r    Route
		want Encoding
	}{
		{"openai chat flat", routeOpenAIChat, Encoding{Levels: true, Switch: false}},
		{"openrouter levels", routeOpenRouter, Encoding{Levels: true, Switch: true}},
		{"openrouter switch-only family", routeOpenRtrSw, Encoding{Levels: false, Switch: true}},
		{"codex responses", routeCodex, Encoding{Levels: true, Switch: false}},
		{"native anthropic", routeAnthropic, Encoding{Levels: true, Switch: true}},
		{"deepseek native", routeDeepseekNat, Encoding{Levels: true, Switch: true}},
		{"z.ai native", routeZaiNat, Encoding{Levels: true, Switch: true}},
		{"bedrock anthropic family", routeBedrockAnth, Encoding{Levels: true, Switch: false}},
		{"bedrock nova", routeBedrockNova, Encoding{Levels: true, Switch: false}},
		{"generic openai-compatible", routeGeneric, Encoding{Levels: true, Switch: false}},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			// Default must ship a real encoder; manual registration cannot hide an
			// empty process table. Also pin the registration API independently.
			shipped, available := Default().EncodingFor(tc.r)
			assert.True(t, available, "route is reachable in the shipped table")
			assert.Equal(t, tc.want, shipped)
			got, ok := tbl.EncodingFor(tc.r)
			require.True(t, ok, "%s: the table has an encoder row — RED: the scaffold registers nothing", tc.name)
			assert.Equal(t, tc.want, got, "%s: the row's encoding matches the route's serialiser (S-17/S-18)", tc.name)
		})
	}

	// A family with no row ("other Bedrock families", S-22 row 11) reports
	// no encoding at all.
	_, ok := tbl.EncodingFor(routeBedrockOthr)
	assert.False(t, ok, "an unregistered family has no encoder row (S-17 'other Bedrock families')")
}

// TestT_P1_S45_FilterServed pins the FR-049 filter the served catalog uses,
// row by row of the S-45 matrix. Served levels are non-nil even when empty —
// the served object carries "levels": [], never null.
// Traces to: S-45, FR-049, US-8 AS1-2.
func TestT_P1_S45_FilterServed(t *testing.T) {
	rows := []struct {
		name       string
		levels     []string
		hasSwitch  bool
		r          Route
		wantLevels []string
		wantNonNil bool
		wantSwitch bool
	}{{
		name:   "(a) levels+switch on OpenRouter serve as published",
		levels: []string{"low", "medium", "high"}, hasSwitch: true, r: routeOpenRouter,
		wantLevels: []string{"low", "medium", "high"}, wantNonNil: true, wantSwitch: true,
	}, {
		name:   "(b) switch-only serves empty levels and the switch",
		levels: []string{}, hasSwitch: true, r: routeOpenRtrSw,
		wantLevels: []string{}, wantNonNil: true, wantSwitch: true,
	}, {
		name:   "(c) budget-only serves the muted object",
		levels: nil, hasSwitch: false, r: routeOpenRouter,
		wantLevels: []string{}, wantNonNil: true, wantSwitch: false,
	}, {
		name:   "(d) flagged-no-control serves the muted object",
		levels: nil, hasSwitch: false, r: routeGeneric,
		wantLevels: []string{}, wantNonNil: true, wantSwitch: false,
	}, {
		name:   "(e) upstream switch filtered on a levels-only route",
		levels: []string{"low", "high"}, hasSwitch: true, r: routeGeneric,
		wantLevels: []string{"low", "high"}, wantNonNil: true, wantSwitch: false,
	}}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			gotLevels, gotSwitch := FilterServed(tc.levels, tc.hasSwitch, tc.r)
			if tc.wantNonNil {
				require.NotNil(t, gotLevels, "%s: served levels are [] never nil (S-45) — RED: the scaffold returns nil", tc.name)
			}
			assert.Equal(t, tc.wantLevels, gotLevels, "%s: served levels (S-45)", tc.name)
			assert.Equal(t, tc.wantSwitch, gotSwitch, "%s: served switch (S-45)", tc.name)
		})
	}
}
