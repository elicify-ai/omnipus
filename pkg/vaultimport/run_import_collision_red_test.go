// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan test 6.
//
// Oracle: US-2 AS-2 ("Given an import whose target name collides with an
// existing view anywhere in the collection (D-DEDUP), When the importer
// runs, Then the conflicting view is rejected exactly as write_view/
// create_view would reject it (same RejectViewDuplicateName code, F7)").
//
// VERDICT: characterization — this passes today. See the test's own doc
// comment for why.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestVaultImport_DuplicateNameRejected$' ./pkg/vaultimport/
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

// TestVaultImport_DuplicateNameRejected is TDD Plan test 6.
//
// CHARACTERIZATION, not a gap: this ALREADY PASSES today. Run(root, true)
// writes its produced views into records.ViewsDir(root) and then reloads the
// WHOLE directory through records.LoadViews (rep.ViewReload) — the same
// shared reader write_view's own collision detection relies on — so a
// hand-authored file sharing the deterministic slug name is caught on
// reload via the EXISTING, unchanged RejectViewDuplicateName mechanism
// (F7), not a new one this spec adds. Pinned here so CHECK's mutation pass
// can prove the assertion actually watches something (kill the shared
// dedup and this test must die) rather than assume it from the spec's own
// framing. Do not read "characterization" as "untested" — it is exactly as
// tested as any other assertion in this file; the label only means the
// EXPECTED VALUE was not independently novel, it was already true.
//
// A hand-authored view, at a DIFFERENT file than the importer will
// produce, already declares the SAME name ("projects--open" — the
// deterministic slug rederiveBaseWithViews' own "Open" view produces, the
// same fixture rederive_test.go's own passing tests already rely on).
func TestVaultImport_DuplicateNameRejected(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(records.ViewsDir(root), "manual-collision.yaml"),
		[]byte("name: projects--open\nlabel: Hand-authored collision\n"), 0o600))

	rep, err := Run(root, true)
	require.NoError(t, err, "import failed")
	require.NotNil(t, rep.ViewReload, "the import did not reload the views it wrote")

	found := false
	for _, rej := range rep.ViewReload.Rejections {
		if rej.Code == records.RejectViewDuplicateName && rej.Name == "projects--open" {
			found = true
			if len(rej.Paths) != 2 {
				t.Errorf("duplicate rejection for %q must name BOTH colliding paths, got %d: %v",
					rej.Name, len(rej.Paths), rej.Paths)
			}
		}
	}
	if !found {
		t.Fatalf(
			"US-2 AS-2: importing a .base whose produced view name (\"projects--open\") collides with "+
				"an existing hand-authored view must reject BOTH files via %q, exactly as write_view's "+
				"own collision path does — got rejections: %+v",
			records.RejectViewDuplicateName, rep.ViewReload.Rejections,
		)
	}
}
