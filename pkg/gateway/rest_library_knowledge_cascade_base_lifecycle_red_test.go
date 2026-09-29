// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 50 and 51.
//
// Oracle: FD-7 / D-PROVENANCE's ".base lifecycle" subsection, resolving
// R2-MAJ-002. Renaming or moving a `.base` file through the Library's real
// rename/move handler MUST rewrite `derived_from` (and `source`) on every
// view in that `.base`'s membership record to the NEW path, in the same
// operation (FR-VA-008f, Dataset F-11). Deleting a `.base` file MUST clear
// `derived_from` on every view in its membership record — never trash or
// delete those views — and remove the record, releasing them to ordinary
// hand-made status (FR-VA-008g, Dataset F-12).
//
// UNBLOCKED for the field (2026-09-29, va-qa2 dispatch): generated.ViewDef.
// DerivedFrom now exists on the merged feature branch (contract commit
// 562acdc32). Both tests below are rewritten as real assertions against the
// real HTTP handlers (POST rename/move, DELETE entries) with view files
// planted directly on disk carrying `derived_from`/`source`. Verified by
// reading pkg/knowledge/rename.go and rest_library_knowledge_cascade.go: the
// Renamer still has NO `.base`-file branch at all (only wikilink/embed
// rewriting), and the delete/trash path still has no release step for a
// `.base`'s managed views — so BOTH real handlers today leave every managed
// view's `derived_from`/`source` exactly as planted, which is what each test
// below actually observes and fails on. There is still no pipeline-owned
// membership record anywhere (verified: zero hits for "ViewMembership" /
// "membership record" outside comments) — its own update/removal cannot be
// asserted, and is named as the remaining missing seam in each failure.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryKnowledgeCascade_RenameUpdatesDerivedFromOnManagedViews$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildBaseWithManagedViews seeds a knowledge base holding one `.base` file
// and two views whose `source`/`derived_from` both name it — the shape
// Dataset F-11/F-12 require, planted directly since neither the importer nor
// re-derivation has a sibling-path writer yet (test 5/47).
func buildBaseWithManagedViews(t *testing.T) (api *restAPI, ws, vault string) {
	t.Helper()
	api, ws = buildLibraryTestAPI(t)
	vault = filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Cascade base-lifecycle vault")
	require.NoError(t, os.WriteFile(filepath.Join(vault, "Projects.base"),
		[]byte("filters:\n  and:\n    - type == \"widget\"\nviews:\n  - type: table\n    name: Open\n"), 0o644))
	viewsDir := filepath.Join(vault, ".omnipus-vault", "views")
	require.NoError(t, os.MkdirAll(viewsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(viewsDir, "projects--open.yaml"),
		[]byte("name: projects--open\nlabel: Open\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(viewsDir, "projects--closed.yaml"),
		[]byte("name: projects--closed\nlabel: Closed\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))
	return api, ws, vault
}

// TestLibraryKnowledgeCascade_RenameUpdatesDerivedFromOnManagedViews is TDD
// Plan test 50 (FD-7, R2-MAJ-002, FR-VA-008f, Dataset F-11): renaming or
// moving a `.base` through the real Library rename handler
// (POST /api/v1/library/{ws}/rename, which drives knowledge.Renamer) must
// rewrite `derived_from` (and `source`) on every managed view to the .base's
// new path, in the same operation, and update the membership record's key.
func TestLibraryKnowledgeCascade_RenameUpdatesDerivedFromOnManagedViews(t *testing.T) {
	api, ws, vault := buildBaseWithManagedViews(t)

	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"vault/Projects.base","to":"vault/Projects-Renamed.base"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	for _, slug := range []string{"projects--open", "projects--closed"} {
		body, rerr := os.ReadFile(filepath.Join(vault, ".omnipus-vault", "views", slug+".yaml"))
		require.NoError(t, rerr, "the managed view file itself must not be moved or deleted by a .base rename")
		got := string(body)
		if !strings.Contains(got, "derived_from: Projects-Renamed.base") || !strings.Contains(got, "source: Projects-Renamed.base") {
			t.Fatalf(
				"FD-7/R2-MAJ-002/FR-VA-008f/Dataset F-11: renaming Projects.base to Projects-Renamed.base "+
					"through the real Library rename handler must rewrite derived_from AND source on every "+
					"managed view to the new path, in the SAME operation. %s.yaml still reads:\n%s\n"+
					"knowledge.Renamer (pkg/knowledge/rename.go) has no .base-file provenance branch at all "+
					"today — that missing branch, and the still-nonexistent membership record whose key it "+
					"would also need to update, are the seams this failure names.",
				slug, got,
			)
		}
	}
}

// TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews is TDD Plan test 51
// (FD-7, R2-MAJ-002, FR-VA-008g, Dataset F-12): deleting a `.base` through
// the real Library delete/trash path must clear `derived_from` on every view
// in its membership record (never trash or delete those views themselves)
// and remove the membership record — releasing each view to ordinary
// hand-made status.
func TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews(t *testing.T) {
	api, ws, vault := buildBaseWithManagedViews(t)

	w := libDelete(t, api, "/api/v1/library/"+ws+"/entries?path=vault/Projects.base")
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	for _, slug := range []string{"projects--open", "projects--closed"} {
		path := filepath.Join(vault, ".omnipus-vault", "views", slug+".yaml")
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf(
				"FD-7/R2-MAJ-002/FR-VA-008g/Dataset F-12: deleting Projects.base must NEVER trash or "+
					"delete its managed views — %s is gone (stat error: %v)",
				path, rerr,
			)
		}
		got := string(body)
		if strings.Contains(got, "derived_from:") {
			t.Fatalf(
				"FD-7/R2-MAJ-002/FR-VA-008g/Dataset F-12: deleting Projects.base through the real Library "+
					"delete path must clear derived_from on every managed view, releasing it to ordinary "+
					"hand-made status. %s.yaml still reads:\n%s\n"+
					"the delete/trash path (rest_library_knowledge_cascade.go) has no .base-aware release "+
					"step at all today, and the pipeline-owned membership record it would also need to "+
					"remove does not exist anywhere — those are the seams this failure names.",
				slug, got,
			)
		}
	}
}
