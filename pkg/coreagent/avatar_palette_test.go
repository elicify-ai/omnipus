// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// The ten canonical values of the metallic palette, in the founder's order.
var newPalette = []string{
	"#6E8BA8", "#7A8C9E", "#4F8A80", "#5E6973", "#7B7290",
	"#B76E79", "#A97142", "#9C8A5B", "#CDBA96", "#8E9196",
}

// The ten colours of the REMOVED previous palette (founder ruling 2026-10-12,
// greenfield: only the new palette exists). They are invalid like any unknown
// colour: refused on write, read back as the default.
var removedPalette = []string{
	"#3B82F6", "#38BDF8", "#22D3EE", "#818CF8", "#A78BFA",
	"#E879F9", "#FB923C", "#9CA3AF", "#F472B6", "#C084FC",
}

func TestPalette_CanonicalColorAcceptsOnlyTheNewSet(t *testing.T) {
	for _, hex := range newPalette {
		got, ok := coreagent.CanonicalColor(hex)
		assert.True(t, ok, hex)
		assert.Equal(t, hex, got)
		low, ok := coreagent.CanonicalColor(toLower(hex))
		assert.True(t, ok)
		assert.Equal(t, hex, low, "letter-case is normalised")
	}
	for _, old := range removedPalette {
		_, ok := coreagent.CanonicalColor(old)
		assert.False(t, ok, "%s is from the removed previous palette", old)
	}
	for _, bad := range []string{"", "#D4AF37", "#FF0000", "#6E8BA9", "6E8BA8", "nope"} {
		_, ok := coreagent.CanonicalColor(bad)
		assert.False(t, ok, "%q is not in the palette", bad)
	}
}

func TestPalette_WireIdentityReadsAnythingOutsideTheNewSetAsDefault(t *testing.T) {
	for _, old := range append([]string{"#D4AF37"}, removedPalette...) {
		_, _, c := coreagent.WireIdentity("", "", old)
		assert.Equal(t, coreagent.DefaultColor, string(c), "read of stored %s", old)
	}
	_, _, c := coreagent.WireIdentity("", "", "#a97142")
	assert.Equal(t, "#A97142", string(c), "letter-case of a palette value is normalised on read")
	assert.Equal(t, "#8E9196", coreagent.DefaultColor, "the default is Pewter")
}

// The seeded built-in agents default to values of the new set, and the visible
// roster stays distinct.
func TestPalette_SeededAgentsUseTheNewSet(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	visible := map[string]string{}
	for _, a := range coreagent.All() {
		assert.Contains(t, newPalette, a.Color, "%s is seeded with a colour outside the new set", a.ID)
		if prior, dup := visible[a.Color]; dup {
			t.Errorf("%s and %s share %s", prior, a.ID, a.Color)
		}
		visible[a.Color] = string(a.ID)
	}
	for _, sa := range coreagent.SystemAgents() {
		assert.Contains(t, newPalette, sa.Color, "system agent %s", sa.ID)
	}
	for i := range cfg.Agents.List {
		a := cfg.Agents.List[i]
		if a.Color != "" {
			assert.Contains(t, newPalette, a.Color, "seeded config agent %s", a.ID)
		}
	}
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + 32
		}
	}
	return string(b)
}
