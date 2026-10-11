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

var oldToNew = map[string]string{
	"#3B82F6": "#6E8BA8", "#38BDF8": "#7A8C9E", "#22D3EE": "#4F8A80", "#818CF8": "#5E6973",
	"#A78BFA": "#7B7290", "#E879F9": "#B76E79", "#FB923C": "#A97142", "#9CA3AF": "#8E9196",
	"#F472B6": "#CDBA96", "#C084FC": "#9C8A5B",
}

func TestPalette_CanonicalColorAcceptsTheNewSetAndMapsTheOldOne(t *testing.T) {
	for _, hex := range newPalette {
		got, ok := coreagent.CanonicalColor(hex)
		assert.True(t, ok, hex)
		assert.Equal(t, hex, got)
		low, ok := coreagent.CanonicalColor(toLower(hex))
		assert.True(t, ok)
		assert.Equal(t, hex, low, "letter-case is normalised")
	}
	for old, want := range oldToNew {
		got, ok := coreagent.CanonicalColor(old)
		assert.True(t, ok, old)
		assert.Equal(t, want, got, "write mapping for %s", old)
	}
	for _, bad := range []string{"", "#D4AF37", "#FF0000", "#6E8BA9", "6E8BA8", "nope"} {
		_, ok := coreagent.CanonicalColor(bad)
		assert.False(t, ok, "%q is in neither set", bad)
	}
}

func TestPalette_MappingIsOneToOneAndLandsInTheNewSet(t *testing.T) {
	seen := map[string]string{}
	for old, want := range oldToNew {
		got, _ := coreagent.CanonicalColor(old)
		if prior, dup := seen[got]; dup {
			t.Fatalf("%s and %s both map to %s: distinct agents would collapse", prior, old, got)
		}
		seen[got] = old
		assert.Contains(t, newPalette, got)
	}
	assert.Len(t, seen, 10)
}

func TestPalette_WireIdentityReadsOldAsNewAndUnknownAsDefault(t *testing.T) {
	for old, want := range oldToNew {
		_, _, c := coreagent.WireIdentity("", "", old)
		assert.Equal(t, want, string(c), "read of stored %s", old)
	}
	_, _, c := coreagent.WireIdentity("", "", "#D4AF37")
	assert.Equal(t, coreagent.DefaultColor, string(c))
	assert.Equal(t, "#8E9196", coreagent.DefaultColor, "the default is Pewter (the old Grey's equivalent)")
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
