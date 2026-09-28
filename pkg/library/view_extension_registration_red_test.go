// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 40 and 43.
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

// TestLibraryEntries_ViewRegisteredInContentTypeAndTextEditableTables is TDD
// Plan test 40 (FR-VA-012, corrected in round 2 per R2-MIN-003 — the
// original "is_hidden: false" assertion was vacuous since a non-dot-prefixed
// extension is never hidden by construction; this asserts the two REAL
// registration tables instead, which CAN fail).
//
// Confirmed by direct read: extMimeTypes and textExtensions (pkg/library/
// entries.go) both have entries for ".base" but neither has one for
// ".view" at all.
func TestLibraryEntries_ViewRegisteredInContentTypeAndTextEditableTables(t *testing.T) {
	dir := t.TempDir()
	body := "name: weekly-status\nlabel: Weekly status\n"
	p := filepath.Join(dir, "weekly-status.view")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	fi, err := os.Stat(p)
	require.NoError(t, err)

	entry := EntryFromInfo("weekly-status.view", fi)

	if entry.Mime == nil {
		t.Errorf("FR-VA-012: .view has no entry in extMimeTypes — entry.Mime is nil")
	}
	assert.True(t, entry.IsTextEditable,
		"FR-VA-012: .view must be registered in textExtensions so a view is offered for text "+
			"editing like .base; entry.IsTextEditable = %v", entry.IsTextEditable)
}

// TestLibraryEntries_BareDotViewIsHiddenNotAView is TDD Plan test 43
// (EC-9, MIN-010): a file literally named ".view" (no stem) is a dotfile by
// the Library's own definition — hidden by default, and NOT classified as
// a view (there is no distinct view content to classify; its "extension"
// is its entire name).
//
// CHARACTERIZATION (the hidden-dotfile half only): the dot-prefix hidden
// rule already applies to ANY dotfile, unchanged by this spec, and already
// hides a bare ".view" file exactly as it would ".gitignore" — pinned so
// CHECK's mutation pass can confirm the assertion watches something real.
// The "is not classified is_view" half of EC-9 is not asserted here: it
// requires the is_view field, which does not exist at all yet (TDD test
// 10's own finding) — asserting its absence today would be vacuously true
// for every entry, not a meaningful check of EC-9's actual carve-out.
func TestLibraryEntries_BareDotViewIsHiddenNotAView(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".view")
	require.NoError(t, os.WriteFile(p, []byte("not a real view, just a dotfile named .view\n"), 0o600))

	fi, statErr := os.Stat(p)
	require.NoError(t, statErr)
	entry := EntryFromInfo(".view", fi)
	assert.True(t, entry.IsHidden, "EC-9/MIN-010: a file literally named \".view\" is a dotfile and "+
		"must be hidden by default, exactly like any other dotfile")
}
