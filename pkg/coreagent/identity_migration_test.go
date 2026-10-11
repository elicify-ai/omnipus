// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
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
		{id: string(IDMia), role: "general", color: "#6E8BA8"},
		{id: string(IDJim), role: "general", color: "#4F8A80"},
		{id: string(IDAva), role: "general", color: "#A97142"},
		// Metallic palette (founder, 2026-10-11): each seed keeps its colour
		// family's equivalent, so the visible roster stays distinct.
		{id: string(IDAdmin), role: "security", color: "#CDBA96"},
		{id: string(IDPlanner), role: "general", color: "#7A8C9E"},
		{id: string(IDResearcher), role: "general", color: "#7B7290"},
		{id: string(IDWorker), role: "general", color: "#8E9196"},
		{id: string(IDJudge), role: "general", color: "#6E8BA8"},
		{id: string(IDPlanSupervisor), role: "general", color: "#4F8A80"},
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
	assert.Equal(t, "#7B1FA2", custom.Color, "a custom agent's stored colour is never rewritten (read normalises it)")
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
	assert.Equal(t, "#7B7290", researcher.Color)

	require.False(t, SeedConfig(cfg), "second SeedConfig call performs no identity write")
	assert.Equal(t, "general", agentByID(cfg, "custom-once").Role)
	assert.Equal(t, "#7B1FA2", agentByID(cfg, "custom-once").Color)
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
