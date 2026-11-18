// A duplicate reason must name the same exact relative files as its paths.
// The filesystem-root cases exercise the helper without creating global dirs;
// temporary roots, including control-folder names, use the real HTTP handlers.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Compare complete filename tokens, not suffix substrings: a relative suffix
// inside an absolute or prefixed name would not establish the required name.
// Sentence wording and file ordering are deliberately not part of the oracle.
func assertReasonMatchesRelativeFiles(t *testing.T, want, paths []string, reason string) {
	t.Helper()
	require.ElementsMatch(t, want, paths, "paths must name both independently specified relative files")
	tokens := strings.FieldsFunc(reason, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("()[],;\"'", r)
	})
	files := []string{}
	for _, token := range tokens {
		if strings.HasSuffix(token, ".yaml") || strings.HasSuffix(token, ".yml") {
			files = append(files, token)
		}
	}
	assert.ElementsMatch(t, want, files,
		"reason must name exactly the same vault-relative filenames as paths, without changing a directory component")
}

func TestKnowledgeRejections_SchemaReasonNamesMatchPaths(t *testing.T) {
	want := []string{".omnipus-vault/records/one.yaml", ".omnipus-vault/records/two.yml"}
	for _, rootName := range []string{"vault", "records"} {
		t.Run("http_"+rootName, func(t *testing.T) {
			api, ws := buildLibraryTestAPI(t)
			root := filepath.Join(workDir(api, ws), rootName)
			makeKnowledgeBase(t, root, "Schema fixture")
			writeNote(t, root, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
			for _, rel := range want {
				writeNote(t, root, rel, recordTestWidgetSchema)
			}
			f := relativePathVaultFixture{api: api, workspaceID: ws, root: root}
			problem := relativePathSchemaProblem(t, f, gen.RecordProblemCodeDuplicateTypeDeclaration)
			assertReasonMatchesRelativeFiles(t, want, *problem.Paths, problem.Reason)
		})
	}

	t.Run("filesystem_root", func(t *testing.T) {
		// Only /records, not <temporary directory>/records, reappears in
		// the newly relative name. Do not create a global root directory.
		root := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "records")
		names := newCollectionFileNames(root)
		paths := []string{
			filepath.Join(root, filepath.FromSlash(want[0])),
			filepath.Join(root, filepath.FromSlash(want[1])),
		}
		reason := "duplicate declarations in " + paths[0] + " and " + paths[1]
		assertReasonMatchesRelativeFiles(t, want, names.names(paths), names.text(reason, paths))
	})
}

func TestKnowledgeRejections_ViewReasonNamesMatchPaths(t *testing.T) {
	want := []string{".omnipus-vault/views/one.yaml", ".omnipus-vault/views/two.yml"}
	for _, rootName := range []string{"vault", "views"} {
		t.Run("http_"+rootName, func(t *testing.T) {
			api, ws := buildLibraryTestAPI(t)
			root := filepath.Join(workDir(api, ws), rootName)
			makeKnowledgeBase(t, root, "View fixture")
			writeNote(t, root, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
			writeNote(t, root, baseViewsTestBaseRel, "views: []\n")
			writeNote(t, root, ".omnipus-vault/views/invoices--valid.yaml",
				importedViewFile("invoices--valid", "Valid invoices", baseViewsTestBaseRel, ""))
			for _, rel := range want {
				writeNote(t, root, rel, importedViewFile("invoices--duplicate", "Duplicate invoices", baseViewsTestBaseRel, ""))
			}
			f := relativePathVaultFixture{
				api: api, workspaceID: ws, root: root,
				collectionID: collectionIDOf(t, api, ws, rootName),
			}
			rejections := relativePathViewRejections(t, f, "views", 2)
			require.Len(t, rejections, 1, "two duplicate files form one rejection")
			require.Equal(t, "view_duplicate_name", rejections[0].Code)
			require.NotNil(t, rejections[0].Name)
			require.Equal(t, "invoices--duplicate", *rejections[0].Name)
			assertReasonMatchesRelativeFiles(t, want, rejections[0].Paths, rejections[0].Reason)
		})
	}

	t.Run("filesystem_root", func(t *testing.T) {
		// The same permitted unit fallback represents /views without
		// requiring a privileged or shared filesystem-root fixture.
		root := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "views")
		names := newCollectionFileNames(root)
		paths := []string{
			filepath.Join(root, filepath.FromSlash(want[0])),
			filepath.Join(root, filepath.FromSlash(want[1])),
		}
		reason := "duplicate declarations in " + paths[0] + " and " + paths[1]
		assertReasonMatchesRelativeFiles(t, want, names.names(paths), names.text(reason, paths))
	})
}
