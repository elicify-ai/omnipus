package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// PaletteColors feeds refusal messages; every entry must be a canonical colour
// (AgentColor.yaml defines ten).
func TestPaletteColors_AreTheTenCanonicalValues(t *testing.T) {
	got := coreagent.PaletteColors()
	assert.Len(t, got, 10)
	seen := map[string]bool{}
	for _, c := range got {
		canon, ok := coreagent.CanonicalColor(c)
		assert.True(t, ok, c)
		assert.Equal(t, c, canon)
		seen[c] = true
	}
	assert.Len(t, seen, 10, "no duplicates")
}
