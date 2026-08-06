// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// TestSanitizeFilename covers pkg/utils/media.go's SanitizeFilename — the
// one call site (used by Discord/Feishu inbound media downloads) that
// genuinely cannot reject a bad name, so it must always return SOMETHING
// safe rather than erroring, per pkg/pathsafe.SanitizeComponent's contract.
func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"plain name", "report.txt"},
		{"real-world UAT name 1", "Copy of My Deck.pptx"},
		{"real-world UAT name 2 (unicode/emoji)", "My Report (final) — résumé 测试 🎉.txt"},
		{"embedded path traversal", "../../etc/passwd"},
		{"embedded backslash traversal", `..\..\windows\system32\config`},
		// The exact adversarial case this whole feature targets: the OLD
		// implementation stripped ".." via iterative strings.ReplaceAll,
		// which left a bare "//" behind for four dots followed by two
		// slashes (removing two non-overlapping ".." matches from "...."
		// leaves nothing, but the residual "//" then depended on a
		// SEPARATE later pass to catch). The new implementation never does
		// substring surgery, so there's nothing to reconstitute.
		{"four dots then slashes", "....//"},
		{"reserved windows device name", "CON"},
		{"reserved windows device name with extension", "nul.txt"},
		{"illegal windows characters", `bad<>:"|?*name.txt`},
		{"control characters", "bad\x00\nname.txt"},
		{"trailing dot", "report."},
		{"trailing space", "report "},
		{"over-long name", strings.Repeat("a", 400) + ".txt"},
		{"empty", ""},
		{"only dots", "...."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFilename(tc.in)
			assert.NotEmpty(t, got, "SanitizeFilename must never return empty, in=%q", tc.in)
			assert.NoError(t, pathsafe.ValidateComponent(got),
				"SanitizeFilename's output must itself be a valid component: in=%q got=%q", tc.in, got)
			assert.NotContains(t, got, "/", "must not contain a path separator: got=%q", got)
			assert.NotContains(t, got, `\`, "must not contain a path separator: got=%q", got)
		})
	}
}

// TestSanitizeFilename_UnchangedForSafeNames confirms an already-safe name
// passes through byte-for-byte, so ordinary uploads are never mangled.
func TestSanitizeFilename_UnchangedForSafeNames(t *testing.T) {
	for _, in := range []string{
		"report.txt",
		"Copy of My Deck.pptx",
		"My Report (final) — résumé 测试 🎉.txt",
		"archive.tar.gz",
	} {
		assert.Equal(t, in, SanitizeFilename(in))
	}
}
