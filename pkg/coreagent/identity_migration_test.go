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

func TestMigrateUnenforcedAgentIdentity_CustomOnceAndStable(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{
			ID:    "custom-code",
			Name:  "Legacy code",
			Type:  config.AgentTypeCustom,
			Icon:  "Code",
			Color: "#D4AF37",
		},
		{
			ID:     "custom-hyphen",
			Name:   "Hyphen icon",
			Type:   config.AgentTypeCustom,
			Icon:   "magnifying-glass",
			Color:  "",
			Figure: "",
			Role:   "",
		},
		{
			ID:    "custom-slug-icon",
			Name:  "Slug icon",
			Type:  config.AgentTypeCustom,
			Icon:  "security",
			Color: "#E2E8F0",
		},
		{
			ID:     "custom-already",
			Name:   "Already canonical",
			Type:   config.AgentTypeCustom,
			Icon:   "robot",
			Color:  "#9CA3AF",
			Figure: "Omnipus",
			Role:   "general",
		},
	}}}

	require.True(t, migrateUnenforcedAgentIdentity(cfg), "first pass must persist the custom migration")

	code := agentByID(cfg, "custom-code")
	require.NotNil(t, code)
	assert.Equal(t, "developer", code.Role, "I14 Code → Developer slug")
	assert.Equal(t, "#FB923C", code.Color, "I03 #D4AF37 → Orange")
	assert.Equal(t, "Omnipus", code.Figure, "missing figure becomes Omnipus")
	assert.Equal(t, "Code", code.Icon, "icon is never written")

	hyphen := agentByID(cfg, "custom-hyphen")
	require.NotNil(t, hyphen)
	assert.Equal(t, "general", hyphen.Role, "magnifying-glass keeps the hyphen and is not researcher")
	assert.Equal(t, "#9CA3AF", hyphen.Color, "I10 missing colour → Grey")
	assert.Equal(t, "Omnipus", hyphen.Figure)
	assert.Equal(t, "magnifying-glass", hyphen.Icon)

	slug := agentByID(cfg, "custom-slug-icon")
	require.NotNil(t, slug)
	assert.Equal(t, "security", slug.Role, "an icon that is already a role slug is kept")
	assert.Equal(t, "#9CA3AF", slug.Color, "I09 #E2E8F0 → Grey")
	assert.Equal(t, "security", slug.Icon)

	already := agentByID(cfg, "custom-already")
	require.NotNil(t, already)
	assert.Equal(t, "general", already.Role)
	assert.Equal(t, "Omnipus", already.Figure)
	assert.Equal(t, "#9CA3AF", already.Color)
	assert.Equal(t, "robot", already.Icon)

	require.False(t, migrateUnenforcedAgentIdentity(cfg), "second pass is a no-op")
	assert.Equal(t, "developer", agentByID(cfg, "custom-code").Role)
	assert.Equal(t, "#FB923C", agentByID(cfg, "custom-code").Color)
	assert.Equal(t, "Code", agentByID(cfg, "custom-code").Icon)
}

func TestMigrateUnenforcedAgentIdentity_KeepsValidRoleAndFigure(t *testing.T) {
	// An editor change must survive the next boot (ARCH 2.5).
	cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{
			ID:     "custom-edited",
			Name:   "Edited",
			Type:   config.AgentTypeCustom,
			Icon:   "Code",
			Role:   "writer",
			Figure: "Woman",
			Color:  "#fb923c",
		},
		{
			ID:     "custom-glass",
			Name:   "Glass",
			Type:   config.AgentTypeCustom,
			Icon:   "MagnifyingGlass",
			Role:   "general",
			Figure: "Robot",
			Color:  "#3B82F6",
		},
	}}}

	require.True(t, migrateUnenforcedAgentIdentity(cfg), "letter-case of a palette hex is a real write")

	edited := agentByID(cfg, "custom-edited")
	require.NotNil(t, edited)
	assert.Equal(t, "writer", edited.Role, "valid slug is not replaced by the icon map")
	assert.Equal(t, "Woman", edited.Figure, "valid figure is not replaced")
	assert.Equal(t, "#FB923C", edited.Color, "only letter-case changes")
	assert.Equal(t, "Code", edited.Icon)

	glass := agentByID(cfg, "custom-glass")
	require.NotNil(t, glass)
	assert.Equal(t, "general", glass.Role, "stored role wins over MagnifyingGlass → researcher")
	assert.Equal(t, "Robot", glass.Figure)
	assert.Equal(t, "#3B82F6", glass.Color)
	assert.Equal(t, "MagnifyingGlass", glass.Icon)

	require.False(t, migrateUnenforcedAgentIdentity(cfg))
}

func TestMigrateUnenforcedAgentIdentity_DoesNotTouchBuiltInsOrIcon(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, SeedConfig(cfg))

	jim := agentByID(cfg, string(IDJim))
	require.NotNil(t, jim)
	jim.Color = "#FFFFFF"
	jim.Icon = "skull"
	jim.Role = "researcher"
	jim.Figure = "Woman"

	require.False(t, migrateUnenforcedAgentIdentity(cfg), "built-ins are not the custom path")
	assert.Equal(t, "#FFFFFF", jim.Color)
	assert.Equal(t, "skull", jim.Icon)
	assert.Equal(t, "researcher", jim.Role)
	assert.Equal(t, "Woman", jim.Figure)
}

func TestSeedConfig_BuiltInsEnforceCanonicalTriplesAndSecondCallIsStable(t *testing.T) {
	// ARCH-DECISIONS 2.4. Figure is Omnipus for every row. Role is the icon
	// map, not the agent's name (Researcher stays general). Icon strings stay
	// the compiled phosphor names.
	want := []struct {
		id    string
		role  string
		color string
		icon  string
	}{
		{id: string(IDMia), role: "general", color: "#3B82F6", icon: "lightbulb"},
		{id: string(IDJim), role: "general", color: "#22D3EE", icon: "graph"},
		{id: string(IDAva), role: "general", color: "#FB923C", icon: "wrench"},
		{id: string(IDAdmin), role: "security", color: "#FB923C", icon: "shield"},
		{id: string(IDPlanner), role: "general", color: "#38BDF8", icon: "tree-structure"},
		{id: string(IDResearcher), role: "general", color: "#A78BFA", icon: "books"},
		{id: string(IDWorker), role: "general", color: "#9CA3AF", icon: "robot"},
		{id: string(IDJudge), role: "general", color: "#3B82F6", icon: "gavel"},
		{id: string(IDPlanSupervisor), role: "general", color: "#22D3EE", icon: "compass-tool"},
	}

	cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{
			ID:    "custom-once",
			Name:  "Once",
			Type:  config.AgentTypeCustom,
			Icon:  "PencilSimple",
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
			assert.Equal(t, row.icon, got.Icon, "enforcement restores the compiled icon; it does not write a role slug into icon")
		})
	}

	custom := agentByID(cfg, "custom-once")
	require.NotNil(t, custom)
	assert.Equal(t, "writer", custom.Role, "I14 PencilSimple → Writer, via SeedConfig")
	assert.Equal(t, "#E879F9", custom.Color, "I04 #7B1FA2 → Fuchsia")
	assert.Equal(t, "Omnipus", custom.Figure)
	assert.Equal(t, "PencilSimple", custom.Icon)

	// A valid custom slug on a built-in must NOT stick. Custom migration would
	// keep role "researcher"; enforcement puts the canonical triple back.
	researcher := agentByID(cfg, string(IDResearcher))
	require.NotNil(t, researcher)
	researcher.Role = "researcher"
	researcher.Figure = "Woman"
	researcher.Icon = "Code"
	researcher.Color = "#FFFFFF"
	require.True(t, SeedConfig(cfg), "tampered built-in identity is rewritten")
	assert.Equal(t, "general", researcher.Role, "books is not a mapped icon; do not correct Researcher by name")
	assert.Equal(t, "Omnipus", researcher.Figure)
	assert.Equal(t, "books", researcher.Icon)
	assert.Equal(t, "#A78BFA", researcher.Color)
	assert.Equal(t, "PencilSimple", agentByID(cfg, "custom-once").Icon, "enforcement must not rewrite the custom icon while repairing a built-in")

	require.False(t, SeedConfig(cfg), "second SeedConfig call performs no identity write")
	assert.Equal(t, "writer", agentByID(cfg, "custom-once").Role)
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
