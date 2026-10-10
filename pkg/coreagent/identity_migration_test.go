// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Identity oracles are frozen in the navigation spec's dataset DS-I and in
// ARCH-DECISIONS.md (this squad) sections 1.3, 1.4, 2.4 and 2.5. Expected
// hexes are those published values, not values read off an implementation.
//
// I12 (exact hue-distance tie) has no #RRGGBB witness: a 24-bit scan finds
// no colour whose two closest non-Grey palette distances are equal (closest
// gap about 0.00036 degrees). The rounded Azure–Indigo midpoint #305EF2 is
// strictly closer to Azure (8.554 degrees) than to Indigo (8.681), so it
// does not exercise the strict-less-than tie branch. That gap is left for
// CHECK; the case below only pins the rounded midpoint.

func TestNearestPalette_MapsSpecFixturesI03ToI13(t *testing.T) {
	// DS-I I03–I11, I13, plus the rounded I12 midpoint. Grey is #9CA3AF.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "I03 old brand gold", in: "#D4AF37", want: "#FB923C"},
		{name: "I04 low contrast purple", in: "#7B1FA2", want: "#E879F9"},
		{name: "I05 low contrast crimson", in: "#AD1457", want: "#F472B6"},
		{name: "I06 saturation 0.24375 just under 0.25", in: "#A0A079", want: "#9CA3AF"},
		{name: "I07 saturation exactly 0.25", in: "#A0A078", want: "#FB923C"},
		{name: "I08 saturation 0.25625 just over 0.25", in: "#A0A077", want: "#FB923C"},
		{name: "I09 black", in: "#000000", want: "#9CA3AF"},
		{name: "I09 white", in: "#FFFFFF", want: "#9CA3AF"},
		{name: "I09 pale grey", in: "#E2E8F0", want: "#9CA3AF"},
		{name: "I11 azure stays azure", in: "#3B82F6", want: "#3B82F6"},
		{name: "I12 rounded azure-indigo midpoint stays azure", in: "#305EF2", want: "#3B82F6"},
		// I13: hue 357.9. Circular distance picks Orange. Linear distance
		// would pick Pink (#F472B6). SPEC "Mapping": shorter circular distance.
		{name: "I13 hue across 0/360", in: "#EB232A", want: "#FB923C"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NearestPalette(tc.in))
		})
	}
}

func TestNearestPalette_PaletteHexesMapToThemselves(t *testing.T) {
	// ARCH-DECISIONS 1.4 order. A second boot must not move a migrated colour.
	palette := []string{
		"#3B82F6", "#38BDF8", "#22D3EE", "#818CF8", "#A78BFA",
		"#C084FC", "#E879F9", "#F472B6", "#FB923C", "#9CA3AF",
	}
	require.Len(t, palette, 10)
	for _, hex := range palette {
		t.Run(hex, func(t *testing.T) {
			assert.Equal(t, hex, NearestPalette(hex), "canonical uppercase hex maps to itself")
			assert.Equal(t, hex, NearestPalette(strings.ToLower(hex)), "listed hex of any letter-case normalises to the uppercase enum")
		})
	}
}

func TestNearestPalette_InvalidOrMissingIsGrey(t *testing.T) {
	// DS-I I10. Exactly '#' plus six hex digits; anything else is Grey.
	// Do not trim. Three-digit CSS form is invalid.
	const grey = "#9CA3AF"
	for _, in := range []string{
		"",
		" ",
		"#",
		"#fff",
		"#FFF",
		"#3B82F",
		"#3B82F6F",
		"#3B82F6FF",
		"3B82F6",
		"#GGGGGG",
		"#3B82FG",
		" #3B82F6",
		"#3B82F6 ",
		"#3B82F6\n",
	} {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, grey, NearestPalette(in))
		})
	}
}

func TestRoleFromLegacyIcon_I14CaseFoldHyphenAndSlug(t *testing.T) {
	// DS-I I14 labels, stored as the slugs in ARCH-DECISIONS 1.3.
	// Case-fold is trim + lower case, hyphens kept (ARCH 2.5).
	// A value that is already one of the 31 slugs is kept.
	// "magnifying-glass" is not the five-key "magnifyingglass".
	cases := []struct {
		in   string
		want string
	}{
		{in: "Code", want: "developer"},
		{in: "code", want: "developer"},
		{in: "CODE", want: "developer"},
		{in: " Code ", want: "developer"},
		{in: "Chat", want: "general"},
		{in: "MagnifyingGlass", want: "researcher"},
		{in: "magnifyingglass", want: "researcher"},
		{in: "PencilSimple", want: "writer"},
		{in: "Shield", want: "security"},
		{in: "shield", want: "security"},
		{in: "magnifying-glass", want: "general"},
		{in: "pencil-simple", want: "general"},
		{in: "Robot", want: "general"},
		{in: "books", want: "general"},
		{in: "", want: "general"},
		{in: "Brain", want: "general"},
		{in: "General assistant", want: "general"},
		{in: "developer", want: "developer"},
		{in: "Writer", want: "writer"},
		{in: "security", want: "security"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, RoleFromLegacyIcon(tc.in))
		})
	}
}

func TestSeedConfig_BuiltInsEnforceCanonicalTriplesAndSecondCallIsStable(t *testing.T) {
	// ARCH-DECISIONS 2.4. Figure is Omnipus for every row. Role is the icon
	// map, not the agent's name (Researcher stays general). The agent `icon`
	// field was removed (agent-icon ruling), so only figure/role/color are
	// enforced here.
	want := []struct {
		id    string
		role  string
		color string
	}{
		{id: string(IDMia), role: "general", color: "#3B82F6"},
		{id: string(IDJim), role: "general", color: "#22D3EE"},
		{id: string(IDAva), role: "general", color: "#FB923C"},
		// Admin is Pink, not Orange, so the visible roster stays distinct.
		// Squad decision 2026-10-08. Ava stays Orange.
		{id: string(IDAdmin), role: "security", color: "#F472B6"},
		{id: string(IDPlanner), role: "general", color: "#38BDF8"},
		{id: string(IDResearcher), role: "general", color: "#A78BFA"},
		{id: string(IDWorker), role: "general", color: "#9CA3AF"},
		{id: string(IDJudge), role: "general", color: "#3B82F6"},
		{id: string(IDPlanSupervisor), role: "general", color: "#22D3EE"},
	}

	cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{
			ID:    "custom-once",
			Name:  "Once",
			Type:  config.AgentTypeCustom,
			Color: "#7B1FA2",
		},
	}}}
	require.True(t, SeedConfig(cfg), "first boot seeds built-ins and migrates the custom agent")

	for _, row := range want {
		t.Run(row.id, func(t *testing.T) {
			got := agentByID(cfg, row.id)
			require.NotNil(t, got, "SeedConfig must materialise %s", row.id)
			assert.Equal(t, "Omnipus", got.Figure)
			assert.Equal(t, row.role, got.Role)
			assert.Equal(t, row.color, got.Color)
		})
	}

	custom := agentByID(cfg, "custom-once")
	require.NotNil(t, custom)
	assert.Equal(t, "general", custom.Role, "the icon-derived role migration was deleted (greenfield: no migrations), so the default role stands")
	assert.Equal(t, "#E879F9", custom.Color, "I04 #7B1FA2 → Fuchsia")
	assert.Equal(t, "Omnipus", custom.Figure)

	// A valid custom slug on a built-in must NOT stick. Custom migration would
	// keep role "researcher"; enforcement puts the canonical triple back.
	researcher := agentByID(cfg, string(IDResearcher))
	require.NotNil(t, researcher)
	researcher.Role = "researcher"
	researcher.Figure = "Woman"
	researcher.Color = "#FFFFFF"
	require.True(t, SeedConfig(cfg), "tampered built-in identity is rewritten")
	assert.Equal(t, "general", researcher.Role, "books is not a mapped icon; do not correct Researcher by name")
	assert.Equal(t, "Omnipus", researcher.Figure)
	assert.Equal(t, "#A78BFA", researcher.Color)

	require.False(t, SeedConfig(cfg), "second SeedConfig call performs no identity write")
	assert.Equal(t, "general", agentByID(cfg, "custom-once").Role)
	assert.Equal(t, "#E879F9", agentByID(cfg, "custom-once").Color)
	assert.Equal(t, "general", agentByID(cfg, string(IDResearcher)).Role)
}

func agentByID(cfg *config.Config, id string) *config.AgentConfig {
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			return &cfg.Agents.List[i]
		}
	}
	return nil
}
