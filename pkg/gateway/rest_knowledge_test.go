// Tests for /api/v1/library/{workspace_id}/knowledge* (ADR-067 stage 2).
//
// Expected values here are derived from the SPEC and the CONTRACT, not from
// what the handlers happen to do: the wire field names and enum members come
// from contracts/components/schemas/Knowledge*.yaml, the behaviours from
// docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md §6 (US-4…
// US-11) and §14 (FR-030…FR-055).
//
// The load-bearing one is TestKnowledgeGraph_OtherWorkspaceNotAddressable_US9AS2:
// it is the P0 for this file, and it carries its own anti-vacuity half — the
// owning workspace's own graph query, over the SAME collection id, must find
// the real edge, or "zero results in the other workspace" would pass with the
// endpoint broken entirely. The human vault-search endpoint's own workspace
// isolation (US-9) is covered in rest_knowledge_find_test.go
// (TestVaultSearch_OutOfScopeCollectionIsEmptyNotError); the retired
// /knowledge/search REST endpoint's isolation test (ADR-081/US-5) went with it.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// --- fixtures ---------------------------------------------------------------

// makeKnowledgeBase turns dir into an Omnipus knowledge base by writing the
// marker the way pkg/knowledge itself addresses it (MarkerDir/MarkerPath), so a
// change to the marker's filename cannot leave this fixture writing to a name
// nothing reads.
func makeKnowledgeBase(t *testing.T, dir, displayName string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(knowledge.MarkerDir(dir), 0o700))
	raw, err := json.Marshal(knowledge.Marker{DisplayName: displayName})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(knowledge.MarkerPath(dir), raw, 0o600))
}

// makeObsidianVault turns dir into a knowledge base the Obsidian way: the
// .obsidian/ directory alone, which Omnipus reads as a detection signal and
// never creates itself (FR-020, FR-023).
func makeObsidianVault(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, knowledge.ObsidianMarkerDirName), 0o755))
}

func writeNote(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// indexKnowledgeBase runs a real index+manifest cycle over a collection, the
// same way the (future) mount-time indexer will: through SyncTracked, so the
// shared progress tracker this collection's searches read is the one that was
// driven. Without it every search would report an idle tracker as "complete".
func indexKnowledgeBase(t *testing.T, home, root string) {
	t.Helper()
	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	defer func() { _ = ix.Close() }()
	_, err = knowledge.SyncTracked(context.Background(), ix,
		knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)
}

// --- request helpers --------------------------------------------------------

func knowledgeGet(t *testing.T, api *restAPI, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	// Entry through HandleLibraryTree, not the individual handler: the shim is
	// the one edit this unit made outside its own files, so every test drives
	// the real dispatch rather than assuming it.
	api.HandleLibraryTree(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoErrorf(t, json.Unmarshal(w.Body.Bytes(), &out), "body: %s", w.Body.String())
	return out
}

// collectionIDOf asks the detect endpoint for a folder's collection id, which
// is how a real client learns one — never by computing a hash itself.
func collectionIDOf(t *testing.T, api *restAPI, workspaceID, relPath string) string {
	t.Helper()
	w := knowledgeGet(t, api, "/api/v1/library/"+workspaceID+"/knowledge?path="+relPath)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	info := decodeJSON[gen.KnowledgeBaseInfo](t, w)
	require.True(t, info.IsKnowledgeBase, "fixture is not detected as a knowledge base")
	require.NotNil(t, info.CollectionId)
	return *info.CollectionId
}

// --- US-4: detection --------------------------------------------------------

// TestKnowledgeInfo_DetectsBothMarkersAndPlainFolders_US4 is US-4's own
// independent test, verbatim: mount three folders — one with .obsidian/, one
// with .omnipus-vault/, one full of .md with neither. The first two are
// knowledge bases; the third is not.
func TestKnowledgeInfo_DetectsBothMarkersAndPlainFolders_US4(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	work := workDir(api, ws)

	makeObsidianVault(t, filepath.Join(work, "obsidian-vault"))
	makeKnowledgeBase(t, filepath.Join(work, "omnipus-vault"), "Research vault")
	writeNote(t, work, "plain/a.md", "# A\n")
	writeNote(t, work, "plain/b.md", "# B\n")

	cases := []struct {
		path       string
		wantKB     bool
		wantMarker gen.KnowledgeBaseInfoMarker
	}{
		{"obsidian-vault", true, gen.KnowledgeBaseInfoMarkerObsidian},
		{"omnipus-vault", true, gen.KnowledgeBaseInfoMarkerOmnipusVault},
		{"plain", false, gen.KnowledgeBaseInfoMarkerNone},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path="+tc.path)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			info := decodeJSON[gen.KnowledgeBaseInfo](t, w)

			assert.Equal(t, tc.wantKB, info.IsKnowledgeBase)
			assert.Equal(t, tc.wantMarker, info.Marker)
			assert.Equal(t, ws, info.WorkspaceId)
			assert.Equal(t, tc.path, info.RootPath)
			assert.Nil(t, info.DetectionError, "a decided answer carries no detection error")
			if tc.wantKB {
				require.NotNil(t, info.CollectionId, "a knowledge base must carry a collection id")
				assert.True(t, len(*info.CollectionId) > 3)
			} else {
				assert.Nil(t, info.CollectionId, "an ordinary folder has no collection id")
			}
		})
	}
}

// TestKnowledgeInfo_NameSurvivesRelocation_US4AS6 — the display name is
// recorded in the marker, so moving the folder and re-detecting it elsewhere
// preserves the name with no migration step.
func TestKnowledgeInfo_NameSurvivesRelocation_US4AS6(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	work := workDir(api, ws)
	makeKnowledgeBase(t, filepath.Join(work, "before"), "Research vault")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=before")
	require.Equal(t, http.StatusOK, w.Code)
	before := decodeJSON[gen.KnowledgeBaseInfo](t, w)
	require.NotNil(t, before.DisplayName)
	assert.Equal(t, "Research vault", *before.DisplayName)

	require.NoError(t, os.MkdirAll(filepath.Join(work, "elsewhere"), 0o755))
	require.NoError(t, os.Rename(filepath.Join(work, "before"), filepath.Join(work, "elsewhere/after")))

	w = knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=elsewhere/after")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	after := decodeJSON[gen.KnowledgeBaseInfo](t, w)
	require.NotNil(t, after.DisplayName)
	assert.Equal(t, "Research vault", *after.DisplayName, "the name is the marker's, not the folder's")
	assert.True(t, after.IsKnowledgeBase)
}

// TestKnowledgeInfo_UndecidableFoldersFailLoudly_E9 — a folder that is missing,
// or is a file, is reported through detection_error rather than being silently
// downgraded to "an ordinary folder with no features".
func TestKnowledgeInfo_UndecidableFoldersFailLoudly_E9(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	writeNote(t, workDir(api, ws), "notes.md", "# Notes\n")

	t.Run("missing folder", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=nope")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		info := decodeJSON[gen.KnowledgeBaseInfo](t, w)
		require.NotNil(t, info.DetectionError, "a missing folder must be reported, not answered 'ordinary'")
		assert.Equal(t, gen.RootMissing, info.DetectionError.Code)
		assert.Contains(t, info.DetectionError.Message, "nope", "the refusal names the path")
	})

	t.Run("path is a file", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=notes.md")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		info := decodeJSON[gen.KnowledgeBaseInfo](t, w)
		require.NotNil(t, info.DetectionError)
		assert.Equal(t, gen.NotADirectory, info.DetectionError.Code)
	})

	t.Run("unknown workspace is the 404", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/no-such-ws/knowledge?path=.")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// --- US-9 (P0): workspace isolation -----------------------------------------

// TestKnowledgeGraph_OtherWorkspaceNotAddressable_US9AS2 — the same boundary on
// the graph endpoint: another workspace's knowledge base is not addressable at
// all, and asking produces an empty graph rather than an error.
func TestKnowledgeGraph_OtherWorkspaceNotAddressable_US9AS2(t *testing.T) {
	api, wsA := buildLibraryTestAPI(t)
	wsB := seedLibraryWorkspace(t, api, "Workspace B")

	vaultB := filepath.Join(workDir(api, wsB), "vault")
	makeKnowledgeBase(t, vaultB, "B's vault")
	writeNote(t, vaultB, "index.md", "# Index\n\n[[secret]]\n")
	writeNote(t, vaultB, "secret.md", "# Secret\n")
	idB := collectionIDOf(t, api, wsB, "vault")

	wB := knowledgeGet(t, api, "/api/v1/library/"+wsB+"/knowledge/graph?collection_id="+idB+"&kind=backlinks&path=secret.md")
	require.Equal(t, http.StatusOK, wB.Code, wB.Body.String())
	owner := decodeJSON[gen.KnowledgeGraphResponse](t, wB)
	require.Len(t, owner.Edges, 1, "the owning workspace must see its own backlink")

	wA := knowledgeGet(t, api, "/api/v1/library/"+wsA+"/knowledge/graph?collection_id="+idB+"&kind=backlinks&path=secret.md")
	require.Equal(t, http.StatusOK, wA.Code, "not addressable is an empty answer, not a 403")
	other := decodeJSON[gen.KnowledgeGraphResponse](t, wA)
	assert.Empty(t, other.Edges)
	assert.Empty(t, other.Nodes)
	assert.NotNil(t, other.Skipped, "skipped is always an array, never null")
	assert.NotContains(t, wA.Body.String(), "index.md")
}

// --- US-7 / US-8 / US-10: the graph -----------------------------------------

// TestKnowledgeGraph_BacklinksSeeAllFourLinkForms_US8AS2 — a note linked from
// four notes using the four different wikilink forms returns all four inbound
// links, whichever spelling was used.
func TestKnowledgeGraph_BacklinksSeeAllFourLinkForms_US8AS2(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "folder/Target.md", "# Target\n\n## Heading\n")
	writeNote(t, vault, "plain.md", "See [[Target]].\n")
	writeNote(t, vault, "aliased.md", "See [[Target|the target]].\n")
	writeNote(t, vault, "heading.md", "See [[Target#Heading]].\n")
	writeNote(t, vault, "pathed.md", "See [[folder/Target]].\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+
		"/knowledge/graph?collection_id="+collectionIDOf(t, api, ws, "vault")+
		"&kind=backlinks&path=folder/Target.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)

	require.Equal(t, gen.KnowledgeGraphResponseKindBacklinks, resp.Kind)
	require.NotNil(t, resp.SourcePath)
	assert.Equal(t, "folder/Target.md", *resp.SourcePath)
	require.Len(t, resp.Edges, 4, "all four link forms must be reported as inbound links")

	byFrom := map[string]gen.KnowledgeGraphEdge{}
	for _, e := range resp.Edges {
		byFrom[e.FromPath] = e
		assert.Equal(t, "folder/Target.md", e.ToPath)
	}
	require.Contains(t, byFrom, "plain.md")
	require.Contains(t, byFrom, "aliased.md")
	require.Contains(t, byFrom, "heading.md")
	require.Contains(t, byFrom, "pathed.md")

	require.NotNil(t, byFrom["aliased.md"].Alias)
	assert.Equal(t, "the target", *byFrom["aliased.md"].Alias)
	require.NotNil(t, byFrom["heading.md"].Heading)
	assert.Equal(t, "Heading", *byFrom["heading.md"].Heading)

	// FR-040's ladder, as reported on the wire: a bare name resolves by unique
	// basename, a path resolves by exact path.
	assert.Equal(t, gen.KnowledgeGraphEdgeResolutionUniqueBasename, byFrom["plain.md"].Resolution)
	assert.Equal(t, gen.KnowledgeGraphEdgeResolutionExactPath, byFrom["pathed.md"].Resolution)
	for _, e := range resp.Edges {
		assert.False(t, e.Ambiguous, "one Target.md in the collection is not ambiguous")
	}
}

// TestKnowledgeGraph_EscapingLinksAreUnresolved_US10 — a link that traverses
// upwards out of the collection, and one naming an absolute filesystem path,
// are both reported unresolved, and the response never names anything outside
// the collection as a resolved target.
func TestKnowledgeGraph_EscapingLinksAreUnresolved_US10(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "hostile.md", "[[../../../.ssh/id_rsa]] and [[/etc/passwd]]\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+
		"/knowledge/graph?collection_id="+collectionIDOf(t, api, ws, "vault")+"&kind=unresolved")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)

	require.Len(t, resp.Edges, 2)
	targets := map[string]bool{}
	for _, e := range resp.Edges {
		assert.Equal(t, gen.KnowledgeGraphEdgeResolutionUnresolved, e.Resolution,
			"an escaping link must never be reported as resolved")
		assert.Equal(t, "hostile.md", e.FromPath)
		targets[e.ToPath] = true
	}
	assert.True(t, targets["../../../.ssh/id_rsa"], "to_path is the link text, not a path that was read")
	assert.True(t, targets["/etc/passwd"])

	// Every node an unresolved edge names must be marked non-existent, so a
	// client cannot navigate to it (FR-065).
	for _, n := range resp.Nodes {
		if n.Path == "hostile.md" {
			assert.True(t, n.Exists)
			continue
		}
		assert.False(t, n.Exists, "an escaping target is not a node the client may open")
		assert.Nil(t, n.Title)
	}
}

// TestKnowledgeGraph_RejectsMalformedQueries — kind is required and closed;
// path is required for the three note-scoped kinds.
func TestKnowledgeGraph_RejectsMalformedQueries(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	id := collectionIDOf(t, api, ws, "vault")
	base := "/api/v1/library/" + ws + "/knowledge/graph?collection_id=" + id

	for name, target := range map[string]string{
		"unknown kind":            base + "&kind=everything",
		"missing kind":            base,
		"backlinks with-out path": base + "&kind=backlinks",
		"no collection":           "/api/v1/library/" + ws + "/knowledge/graph?kind=orphans",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, http.StatusBadRequest, knowledgeGet(t, api, target).Code)
		})
	}
}

// --- US-7 / FR-062: the outline ---------------------------------------------

// TestKnowledgeOutline_FlatHeadingsWithUniqueSlugs_US7AS5 — the outline is a
// flat list in document order, nesting carried by level, and a repeated
// heading text gets a distinct slug (a second identical anchor is one no
// client could scroll to).
func TestKnowledgeOutline_FlatHeadingsWithUniqueSlugs_US7AS5(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "note.md",
		"---\ntitle: Note\n---\n# Notes\n\ntext\n\n### Deep\n\n## Notes\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path=vault/note.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeOutline](t, w)

	assert.Equal(t, "vault/note.md", out.Path)
	assert.True(t, out.IsKnowledgeBase, "this file sits inside a detected knowledge base")
	assert.NotNil(t, out.CollectionId)
	require.NotNil(t, out.FrontmatterMalformed)
	assert.False(t, *out.FrontmatterMalformed)

	require.Len(t, out.Headings, 3, "frontmatter is not a heading, and H1→H3 invents no intermediate")
	assert.Equal(t, 1, out.Headings[0].Level)
	assert.Equal(t, "Notes", out.Headings[0].Text)
	assert.Equal(t, "notes", out.Headings[0].Slug)
	assert.Equal(t, 3, out.Headings[1].Level)
	assert.Equal(t, "deep", out.Headings[1].Slug)
	assert.Equal(t, 2, out.Headings[2].Level)
	assert.Equal(t, "notes-1", out.Headings[2].Slug, "a repeated heading gets a numeric suffix")

	require.NotNil(t, out.Headings[0].Line)
	assert.Positive(t, *out.Headings[0].Line)
}

// TestKnowledgeOutline_ServedForAnyMarkdownFile_FR062 — an outline needs no
// index, so it is available for a markdown file that belongs to no knowledge
// base. is_knowledge_base false is an answer, not an error.
func TestKnowledgeOutline_ServedForAnyMarkdownFile_FR062(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	writeNote(t, workDir(api, ws), "loose/readme.md", "# Readme\n\n## Install\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path=loose/readme.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeOutline](t, w)

	assert.False(t, out.IsKnowledgeBase)
	assert.Nil(t, out.CollectionId, "collection_id is present only inside a knowledge base")
	require.Len(t, out.Headings, 2)
	assert.Equal(t, "Install", out.Headings[1].Text)
}

// TestKnowledgeOutline_MalformedFrontmatterIsReportedNotDropped_E17 — a
// frontmatter block that is not valid YAML is REPORTED; the file is still
// outlined either way.
func TestKnowledgeOutline_MalformedFrontmatterIsReportedNotDropped_E17(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	work := workDir(api, ws)
	writeNote(t, work, "bad.md", "---\ntitle: [unclosed\ntags: ,,,\n---\n# Body\n")
	writeNote(t, work, "good.md", "---\ntitle: Fine\n---\n# Body\n")
	writeNote(t, work, "none.md", "# Body\n")

	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"malformed":      {"bad.md", true},
		"valid":          {"good.md", false},
		"no frontmatter": {"none.md", false},
	} {
		t.Run(name, func(t *testing.T) {
			w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path="+tc.path)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			out := decodeJSON[gen.KnowledgeOutline](t, w)
			require.NotNil(t, out.FrontmatterMalformed)
			assert.Equal(t, tc.want, *out.FrontmatterMalformed)
			assert.Len(t, out.Headings, 1, "the file is outlined regardless of its frontmatter")
		})
	}
}

// TestKnowledgeOutline_NoHeadingsIsAnEmptyArrayNotNull — a file with no
// headings is an ordinary file, and its outline is an empty array. Null would
// force every client into a nil check the contract explicitly removes.
func TestKnowledgeOutline_NoHeadingsIsAnEmptyArrayNotNull(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	writeNote(t, workDir(api, ws), "flat.md", "just a paragraph, no headings at all\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path=flat.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"headings":[]`)
	out := decodeJSON[gen.KnowledgeOutline](t, w)
	assert.Empty(t, out.Headings)
}

// TestKnowledgeOutline_RejectsNonMarkdownAndMissingPaths.
func TestKnowledgeOutline_RejectsNonMarkdownAndMissingPaths(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	writeNote(t, workDir(api, ws), "image.png", "not really a png")

	assert.Equal(t, http.StatusBadRequest,
		knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline").Code)
	assert.Equal(t, http.StatusBadRequest,
		knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path=image.png").Code)
	assert.Equal(t, http.StatusBadRequest,
		knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path=../escape.md").Code)
	assert.Equal(t, http.StatusNotFound,
		knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/outline?path=absent.md").Code)
}

// --- the dispatch shim ------------------------------------------------------

// TestHandleLibraryTree_LeavesTheLibraryAlone guards the one edit this unit
// made outside its own files: the /api/v1/library/ subtree now enters
// HandleLibraryTree, and everything that is not a knowledge path must still
// reach HandleLibrary unchanged.
func TestHandleLibraryTree_LeavesTheLibraryAlone(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	writeNote(t, workDir(api, ws), "note.md", "# Note\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/entries?path=")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	entries := decodeJSON[[]gen.LibraryEntry](t, w)
	require.Len(t, entries, 1)
	assert.Equal(t, "note.md", entries[0].Name)

	// And a folder literally named "knowledge" inside the work tree is still
	// reachable through the Library, because the shim keys on the SEGMENT
	// AFTER the workspace id, not on the word appearing anywhere in the path.
	writeNote(t, workDir(api, ws), "knowledge/inner.md", "# Inner\n")
	w = knowledgeGet(t, api, "/api/v1/library/"+ws+"/entries?path=knowledge")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	inner := decodeJSON[[]gen.LibraryEntry](t, w)
	require.Len(t, inner, 1)
	assert.Equal(t, "inner.md", inner[0].Name)
}
