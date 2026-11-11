// Knowledge rejection filenames follow the vault-relative paths declared by
// KnowledgeBaseUnloadableView.paths and RecordProblem.paths. Expected paths
// are fixture-relative literals, never taken from a loader report.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

type relativePathVaultFixture struct {
	api          *restAPI
	workspaceID  string
	root         string
	collectionID string
}

// buildRelativePathVault leaves one valid view/type as a positive control.
// Scope discovery stops at an outer vault, so the nested case mounts ONLY the
// inner vault explicitly; it is then the unambiguous owning collection.
func buildRelativePathVault(t *testing.T, nested bool) relativePathVaultFixture {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	if nested {
		outer := filepath.Join(t.TempDir(), "outer")
		makeKnowledgeBase(t, outer, "Outer vault")
		writeNote(t, outer, ".omnipus-vault/records/outer-only.yaml", "type: outer-only\n")
		writeNote(t, outer, ".omnipus-vault/views/outer-only.yaml", "name: outer-only\ngroup-by: client\n")
		vault = filepath.Join(outer, "inner")
		makeKnowledgeBase(t, vault, "Inner vault")
		_, _, err := workspace.CreateMount(api.homePath, ws, "vault", vault)
		require.NoError(t, err, "nested vault mount is fixture setup")
	} else {
		makeKnowledgeBase(t, vault, "Finance vault")
	}
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
	writeNote(t, vault, baseViewsTestBaseRel, "views: []\n")
	writeNote(t, vault, ".omnipus-vault/views/invoices--valid.yaml",
		importedViewFile("invoices--valid", "Valid invoices", baseViewsTestBaseRel, ""))
	return relativePathVaultFixture{
		api: api, workspaceID: ws, root: vault,
		collectionID: collectionIDOf(t, api, ws, "vault"),
	}
}

// relativePathViewRejections exercises both real listings and checks that the
// rejected fixtures did not hide the valid view or change base ownership.
func relativePathViewRejections(t *testing.T, f relativePathVaultFixture, endpoint string, fileCount int) []gen.KnowledgeBaseUnloadableView {
	t.Helper()
	base := "/api/v1/library/" + f.workspaceID + "/knowledge/"
	if endpoint == "views" {
		w := knowledgeGet(t, f.api, base+"views?collection_id="+url.QueryEscape(f.collectionID))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		out := decodeJSON[gen.KnowledgeCollectionViews](t, w)
		require.Equal(t, f.collectionID, out.CollectionId)
		require.Len(t, out.Views, 1, "the valid view must still load")
		require.Equal(t, "invoices--valid", out.Views[0].Name)
		require.Equal(t, fileCount, out.UnloadableCount)
		require.NotNil(t, out.Unloadable)
		return *out.Unloadable
	}
	w := knowledgeGet(t, f.api, base+"base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)
	require.True(t, out.IsKnowledgeBase)
	require.Equal(t, "vault/"+baseViewsTestBaseRel, out.BasePath)
	require.NotNil(t, out.CollectionId)
	require.Equal(t, f.collectionID, *out.CollectionId)
	require.NotNil(t, out.CollectionRoot)
	require.Equal(t, "vault", *out.CollectionRoot, "collection_root uses the workspace's spelling")
	require.NotNil(t, out.Source)
	require.Equal(t, baseViewsTestBaseRel, *out.Source, "source uses the owning vault's spelling")
	require.Len(t, out.Views, 1, "the valid view must still load")
	require.Equal(t, "invoices--valid", out.Views[0].Name)
	require.Equal(t, fileCount, out.UnloadableCount)
	require.NotNil(t, out.Unloadable)
	return *out.Unloadable
}

func assertVaultRelativePaths(t *testing.T, f relativePathVaultFixture, want, got []string) {
	t.Helper()
	assert.ElementsMatch(t, want, got, "rejection paths must name every file relative to its owning vault")
	for _, p := range got {
		assert.Falsef(t, filepath.IsAbs(p), "rejection path must be vault-relative, got absolute path %q", p)
		assertVaultRelativeText(t, f, p)
	}
}

// Check both lexical and resolved host roots, since temporary directories can
// be reached through a symlink on supported platforms. Relative suffixes alone
// would also occur in an absolute filename and would not prove this property.
func assertVaultRelativeText(t *testing.T, f relativePathVaultFixture, text string) {
	t.Helper()
	assert.NotEmpty(t, text, "a rejection must retain its filename or explanation")
	for _, root := range []string{f.api.homePath, workDir(f.api, f.workspaceID), f.root} {
		resolved, err := filepath.EvalSymlinks(root)
		require.NoError(t, err, "host-root comparison is fixture setup")
		for _, hostPath := range []string{root, resolved} {
			assert.NotContains(t, text, hostPath, "rejection text must use vault-relative names, not a host directory")
			assert.NotContains(t, text, filepath.ToSlash(hostPath), "rejection text must not carry a slash-normalized host directory")
		}
	}
}

func TestKnowledgeRejections_ViewPathsAreVaultRelative(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		for _, endpoint := range []string{"views", "base-views"} {
			t.Run(location+"/"+endpoint, func(t *testing.T) {
				f := buildRelativePathVault(t, location == "nested")
				const rel = ".omnipus-vault/views/invoices--broken.yaml"
				writeNote(t, f.root, rel, "name: invoices--broken\ntype: invoice\ngroup-by: client\nsource: "+baseViewsTestBaseRel+"\n")

				rejections := relativePathViewRejections(t, f, endpoint, 1)
				require.Len(t, rejections, 1)
				rejection := rejections[0]
				require.Equal(t, "view_unknown_key", rejection.Code)
				require.NotNil(t, rejection.Name)
				require.Equal(t, "invoices--broken", *rejection.Name)
				assertVaultRelativePaths(t, f, []string{rel}, rejection.Paths)
			})
		}
	}
}

func TestKnowledgeRejections_DuplicateViewFilesAreVaultRelative(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		for _, endpoint := range []string{"views", "base-views"} {
			t.Run(location+"/"+endpoint, func(t *testing.T) {
				f := buildRelativePathVault(t, location == "nested")
				paths := []string{".omnipus-vault/views/invoices--first.yaml", ".omnipus-vault/views/invoices--second.yml"}
				for _, rel := range paths {
					writeNote(t, f.root, rel, importedViewFile("invoices--duplicate", "Duplicate invoices", baseViewsTestBaseRel, ""))
				}

				rejections := relativePathViewRejections(t, f, endpoint, 2)
				require.Len(t, rejections, 1, "two files form one duplicate-name conflict")
				rejection := rejections[0]
				require.Equal(t, "view_duplicate_name", rejection.Code)
				require.NotNil(t, rejection.Name)
				require.Equal(t, "invoices--duplicate", *rejection.Name)
				assertVaultRelativePaths(t, f, paths, rejection.Paths)
				assertVaultRelativeText(t, f, rejection.Reason)
			})
		}
	}
}

func TestKnowledgeRejections_ViewResultReasonUsesVaultRelativeFiles(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		t.Run(location, func(t *testing.T) {
			f := buildRelativePathVault(t, location == "nested")
			for _, rel := range []string{".omnipus-vault/views/invoices--first.yaml", ".omnipus-vault/views/invoices--second.yml"} {
				writeNote(t, f.root, rel, importedViewFile("invoices--duplicate", "Duplicate invoices", baseViewsTestBaseRel, ""))
			}

			w := knowledgeGet(t, f.api, "/api/v1/library/"+f.workspaceID+"/knowledge/view?collection_id="+
				url.QueryEscape(f.collectionID)+"&view=invoices--duplicate")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			out := decodeJSON[gen.ViewResult](t, w)
			require.NotNil(t, out.Refusal, "a duplicate view must remain a named refusal")
			require.Equal(t, "view_duplicate_name", out.Refusal.Code)
			assert.False(t, out.Complete)
			assert.Empty(t, out.Rows)
			assert.Empty(t, out.Parts)
			assertVaultRelativeText(t, f, out.Refusal.Reason)
			require.NotNil(t, out.CompleteReason)
			assertVaultRelativeText(t, f, *out.CompleteReason)
		})
	}
}

func relativePathSchemaProblem(t *testing.T, f relativePathVaultFixture, code gen.RecordProblemCode) gen.RecordProblem {
	t.Helper()
	w := knowledgeGet(t, f.api, "/api/v1/library/"+f.workspaceID+"/knowledge/record-schema")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.RecordSchema](t, w)
	require.Len(t, out.Types, 1, "the valid invoice schema must still load")
	require.Equal(t, "invoice", out.Types[0].Type)
	require.Len(t, out.Problems, 1, "only the selected vault's rejected files belong in the answer")
	problem := out.Problems[0]
	require.Equal(t, code, problem.Code)
	require.NotNil(t, problem.Records)
	assert.Empty(t, problem.Records)
	require.NotNil(t, problem.Paths, "the problem must retain its implicated files")
	return problem
}

func TestKnowledgeRejections_SchemaPathsAreVaultRelative(t *testing.T) {
	cases := []struct {
		name string
		body string
		code gen.RecordProblemCode
	}{
		{"missing_version", "type: broken\nproperties:\n  name: { type: text }\n", gen.RecordProblemCodeMissingSchemaVersion},
		{"invalid_yaml", "schema_version: 1\ntype: broken\nproperties: [\n", gen.RecordProblemCodeSchemaInvalidYaml},
	}
	for _, location := range []string{"workspace", "nested"} {
		for _, tc := range cases {
			t.Run(location+"/"+tc.name, func(t *testing.T) {
				f := buildRelativePathVault(t, location == "nested")
				const rel = ".omnipus-vault/records/broken.yaml"
				writeNote(t, f.root, rel, tc.body)

				problem := relativePathSchemaProblem(t, f, tc.code)
				assertVaultRelativePaths(t, f, []string{rel}, *problem.Paths)
			})
		}
	}
}

func TestKnowledgeRejections_DuplicateSchemaFilesAreVaultRelative(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		t.Run(location, func(t *testing.T) {
			f := buildRelativePathVault(t, location == "nested")
			paths := []string{".omnipus-vault/records/widget.yaml", ".omnipus-vault/records/widgets.yml"}
			for _, rel := range paths {
				writeNote(t, f.root, rel, recordTestWidgetSchema)
			}

			problem := relativePathSchemaProblem(t, f, gen.RecordProblemCodeDuplicateTypeDeclaration)
			assertVaultRelativePaths(t, f, paths, *problem.Paths)
			assertVaultRelativeText(t, f, problem.Reason)
		})
	}
}

func TestKnowledgeRejections_UnreadableSchemaReasonUsesVaultRelativeFiles(t *testing.T) {
	for _, location := range []string{"workspace", "nested"} {
		t.Run(location, func(t *testing.T) {
			f := buildRelativePathVault(t, location == "nested")
			const rel = ".omnipus-vault/records/unreadable.yaml"
			// A dangling link deterministically produces a real read failure,
			// including under a privileged user; mode-bit fixtures would not.
			missing := filepath.Join(f.root, ".omnipus-vault", "records", "not-present.yaml")
			_, err := os.Lstat(missing)
			require.ErrorIs(t, err, os.ErrNotExist, "the link target must be absent")
			require.NoError(t, os.Symlink(missing, filepath.Join(f.root, filepath.FromSlash(rel))))

			problem := relativePathSchemaProblem(t, f, gen.RecordProblemCodeSchemaUnreadable)
			assertVaultRelativePaths(t, f, []string{rel}, *problem.Paths)
			assertVaultRelativeText(t, f, problem.Reason)
		})
	}
}
