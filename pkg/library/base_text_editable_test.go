// UAT 2026-09-13 D-119 (web half): a `.base` file is TEXT — YAML that
// declares views — so the Library must offer it for text editing whether it
// is healthy or malformed. Before this, `.base` was missing from
// textExtensions, so a healthy base reported is_text_editable:false and the
// SPA showed no raw/edit affordance at all; the raw editor appeared only
// once the base was already broken (behind the "no views" empty state).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUAT_D119_HealthyBaseFileIsTextEditable(t *testing.T) {
	dir := t.TempDir()
	healthy := "views:\n  - type: table\n    name: All\n"
	p := filepath.Join(dir, "Projects.base")
	require.NoError(t, os.WriteFile(p, []byte(healthy), 0o600))
	fi, err := os.Stat(p)
	require.NoError(t, err)

	entry := EntryFromInfo("Projects.base", fi)
	assert.True(t, entry.IsTextEditable, "a healthy .base must be offered for text editing")
	require.NotNil(t, entry.Mime, "a .base carries a MIME hint like every other text format")
	assert.Equal(t, "application/x-yaml", *entry.Mime, ".base is YAML and must say so")

	isText, mime := sniffText([]byte(healthy), "Projects.base")
	assert.True(t, isText, "GET .../content must report a healthy .base as text")
	assert.Equal(t, "application/x-yaml", mime)
}

func TestUAT_D119_MalformedBaseFileStaysTextEditable(t *testing.T) {
	// Broken YAML is still text: the whole point of the raw editor is
	// repairing exactly this file, so it must never fall back to a download
	// card because its content failed to parse.
	malformed := "views:\n  - [unterminated\n    name: \"oops\n"
	isText, _ := sniffText([]byte(malformed), "Broken.base")
	assert.True(t, isText, "a malformed .base must stay editable so it can be repaired")

	// Binary bytes under a .base name are still binary — the NUL check runs
	// before any extension override, unchanged from every other text kind.
	isText, _ = sniffText([]byte("views:\x00\x01\x02"), "Bin.base")
	assert.False(t, isText, "a NUL-bearing file is binary regardless of its extension")
}
