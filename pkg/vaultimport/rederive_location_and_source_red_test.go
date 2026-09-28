// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 5 and 25.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestVaultImport_WritesViewBesideBaseFile$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestHandMadeView_NeverTouchedByRederivation$' ./pkg/vaultimport/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package vaultimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestVaultImport_WritesViewBesideBaseFile is TDD Plan test 5 (US-2 AS-1):
// the importer must write the resulting .view file in the .base file's OWN
// directory, not the collection root and not .omnipus-vault/views/.
//
// This tests the LOCATION half only — the derived_from half of this row is
// already covered as BLOCKED by TestRederive_IgnoresHandAddedDerivedFromOnForeignFile
// et al. (derived_from does not exist on the wire type at all).
//
// fileTranslatedBase (rederive.go) — the same writer RederiveBase uses on
// every save, including the first one — always writes to
// filepath.Join(records.ViewsDir(vaultRoot), filepath.Base(pv.RelPath)),
// never beside the .base file.
func TestVaultImport_WritesViewBesideBaseFile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "projects")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.MkdirAll(records.SchemaDir(root), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(records.SchemaDir(root), "widget.yaml"), []byte(rederiveWidgetSchema), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "Projects.base"), []byte(rederiveBaseWithViews), 0o644))

	res, err := RederiveBase(root, "projects/Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)
	require.NotEmpty(t, res.Written)

	besidePath := filepath.Join(sub, "projects--open.yaml")
	legacyPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	if _, err := os.Stat(besidePath); err != nil {
		t.Fatalf(
			"US-2 AS-1: the imported view must be written beside its .base file (%s), not the legacy "+
				"control directory — stat error: %v (legacy path exists: %v)",
			besidePath, err, fileExists(legacyPath),
		)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TestHandMadeView_NeverTouchedByRederivation is TDD Plan test 25
// (US-2 AS-7, FD-3): a hand-made view that happens to set a free-text
// `source` value naming a `.base` file, for its own descriptive reasons,
// must be completely untouched when that `.base` is saved — `source` must
// never be used as a re-derivation trigger.
//
// Today's "mine" computation in RederiveBase is EXACTLY `v.DeclaredSource()
// == baseRelPath` — a plain string match against `source:`, with no
// `derived_from`/membership-record distinction at all (that distinction is
// this spec's own point, FD-5/R2-MAJ-001). A hand-made view that merely
// SETS `source` for descriptive reasons is therefore treated as "mine" by
// today's mechanism and gets DELETED on the next save if the .base does
// not happen to declare a same-slug view — exactly the failure FD-3/D-
// PROVENANCE exists to prevent.
func TestHandMadeView_NeverTouchedByRederivation(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	// A hand-made view, never written by this pipeline, that merely
	// DESCRIBES itself as related to Projects.base via `source` — its own
	// name ("hand-made-descriptive") is NOT one Projects.base's translation
	// would ever produce.
	handMadePath := filepath.Join(records.ViewsDir(root), "hand-made-descriptive.yaml")
	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	require.NoError(t, os.WriteFile(handMadePath,
		[]byte("name: hand-made-descriptive\nlabel: My own dashboard\nsource: Projects.base\n"), 0o600))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	if _, statErr := os.Stat(handMadePath); statErr != nil {
		t.Fatalf(
			"US-2 AS-7/FD-3: a hand-made view (never written by the import/re-derivation pipeline) "+
				"that merely sets a descriptive `source` value must be COMPLETELY untouched — it was "+
				"deleted by this run (stat error: %v). Deleted=%v",
			statErr, res.Deleted,
		)
	}
	for _, slug := range res.Deleted {
		if slug == "hand-made-descriptive" {
			t.Fatalf("the hand-made view was reported Deleted=%v — source must never be used as a "+
				"re-derivation trigger", res.Deleted)
		}
	}
}
