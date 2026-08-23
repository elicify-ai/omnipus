// Tests for /api/v1/library/{workspace_id}/knowledge* (ADR-067 stage 2).
//
// Expected values here are derived from the SPEC and the CONTRACT, not from
// what the handlers happen to do: the wire field names and enum members come
// from contracts/components/schemas/Knowledge*.yaml, the behaviours from
// docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md §6 (US-4…
// US-11) and §14 (FR-030…FR-055).
//
// The load-bearing one is TestKnowledgeSearch_WorkspaceAIsolatedFromB_US9: it
// is the P0, and it carries its own anti-vacuity half — workspace B searching
// the SAME collection id for the SAME phrase must find the note, or "zero
// results in A" would pass with search broken entirely.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func knowledgeSearchPost(t *testing.T, api *restAPI, workspaceID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/library/"+workspaceID+"/knowledge/search", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleLibraryTree(w, r)
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
		{"obsidian-vault", true, gen.Obsidian},
		{"omnipus-vault", true, gen.OmnipusVault},
		{"plain", false, gen.None},
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

// TestKnowledgeSearch_WorkspaceAIsolatedFromB_US9 is the P0 requirement.
//
// A knowledge base exists only in workspace B and contains a phrase that
// appears nowhere else. An agent in workspace A, given B's own collection id,
// must receive ZERO results — and not a permission error, because a 403 would
// confirm the collection exists, which is itself the disclosure (FR-053).
//
// The second half is what stops this test passing vacuously: workspace B, with
// the same id and the same query, must FIND the note. Without it, a search that
// returned nothing to everybody would look like isolation.
func TestKnowledgeSearch_WorkspaceAIsolatedFromB_US9(t *testing.T) {
	api, wsA := buildLibraryTestAPI(t)
	wsB := seedLibraryWorkspace(t, api, "Workspace B")

	vaultB := filepath.Join(workDir(api, wsB), "vault")
	makeKnowledgeBase(t, vaultB, "B's vault")
	writeNote(t, vaultB, "secret.md", "# Secret\n\nThe passphrase is zarquon-seven, do not share it.\n")
	indexKnowledgeBase(t, api.homePath, vaultB)

	// Workspace A has a knowledge base of its own, so "A finds nothing" cannot
	// be explained by A having no knowledge bases at all.
	vaultA := filepath.Join(workDir(api, wsA), "vault")
	makeKnowledgeBase(t, vaultA, "A's vault")
	writeNote(t, vaultA, "own.md", "# Own\n\nNothing of interest here.\n")
	indexKnowledgeBase(t, api.homePath, vaultA)

	idB := collectionIDOf(t, api, wsB, "vault")

	t.Run("workspace B finds it", func(t *testing.T) {
		w := knowledgeSearchPost(t, api, wsB, map[string]any{
			"query": "zarquon-seven", "collection_id": idB,
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)
		require.Len(t, resp.Hits, 1, "the owning workspace must be able to find its own note")
		assert.Equal(t, "secret.md", resp.Hits[0].Path)
	})

	t.Run("workspace A finds nothing and is not told why", func(t *testing.T) {
		w := knowledgeSearchPost(t, api, wsA, map[string]any{
			"query": "zarquon-seven", "collection_id": idB,
		})
		require.Equal(t, http.StatusOK, w.Code,
			"an out-of-scope collection is an empty answer, never a permission error")
		resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)
		assert.Empty(t, resp.Hits, "workspace A must not see workspace B's note")
		assert.True(t, resp.Incompleteness.Complete,
			"the empty set IS the whole of what this workspace may see")
		assert.Contains(t, w.Body.String(), `"hits":[]`,
			"hits is always an array, never null — the client maps over it without a nil check")
		assert.NotEmpty(t, resp.Incompleteness.Statement)
		assert.NotContains(t, w.Body.String(), "secret.md")
		assert.NotContains(t, w.Body.String(), "zarquon")
	})
}

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

// --- US-6 / US-8: search honesty --------------------------------------------

// TestKnowledgeSearch_CompleteIndexAnswersWithTitleAndExcerpt_FR050 — a
// finished index returns ranked hits carrying path, title and a matched
// excerpt, and says the answer is complete.
func TestKnowledgeSearch_CompleteIndexAnswersWithTitleAndExcerpt_FR050(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "architecture/sandboxing.md",
		"# Sandboxing\n\nLandlock is per-thread and inherited, so the gateway is confined too.\n")
	writeNote(t, vault, "unrelated.md", "# Unrelated\n\nGardening notes.\n")
	indexKnowledgeBase(t, api.homePath, vault)

	w := knowledgeSearchPost(t, api, ws, map[string]any{
		"query": "landlock", "collection_id": collectionIDOf(t, api, ws, "vault"),
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)

	require.Len(t, resp.Hits, 1)
	hit := resp.Hits[0]
	assert.Equal(t, "architecture/sandboxing.md", hit.Path)
	assert.Equal(t, "Sandboxing", hit.Title, "the title is the note's first heading, not its filename")
	assert.Equal(t, gen.KnowledgeSearchHitKindNote, hit.Kind)
	require.NotNil(t, hit.Excerpt, "FR-050 requires a matched excerpt")
	assert.Contains(t, *hit.Excerpt, "Landlock")
	assert.Nil(t, hit.ExcerptUnavailable, "excerpt_unavailable accompanies an ABSENT excerpt only")

	assert.True(t, resp.Incompleteness.Complete)
	assert.True(t, resp.Incompleteness.TotalKnown)
	assert.NotEmpty(t, resp.Incompleteness.Statement, "the statement is required, never absent")
	assert.Equal(t, knowledge.SearchDefaultTopN, resp.LimitApplied)
	assert.False(t, resp.LimitClamped)
	assert.Nil(t, resp.LimitRequested, "limit_requested is present only when the limit was clamped")
}

// TestKnowledgeSearch_NeverIndexedIsNotAConfidentZero_US6 — a knowledge base
// nobody has indexed yet returns no hits, and MUST NOT describe that as the
// complete answer. "0 results" and "we have not read your notes yet" are
// different statements and the caller has to be able to tell them apart.
func TestKnowledgeSearch_NeverIndexedIsNotAConfidentZero_US6(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "note.md", "# Note\n\nzarquon-seven\n")
	// Deliberately NOT indexed.

	w := knowledgeSearchPost(t, api, ws, map[string]any{
		"query": "zarquon-seven", "collection_id": collectionIDOf(t, api, ws, "vault"),
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)

	assert.Empty(t, resp.Hits)
	assert.False(t, resp.Incompleteness.Complete,
		"an unindexed collection must never report a complete answer")
	assert.False(t, resp.Incompleteness.TotalKnown,
		"nothing has been enumerated, so no total is known")
	assert.Nil(t, resp.Incompleteness.TotalFiles,
		"FR-036: no denominator may be invented when the total is unknown")
	assert.Contains(t, resp.Incompleteness.Statement, "not been indexed")
}

// TestKnowledgeSearch_LimitAboveCapIsClampedAndReported_FR037 — a requested
// count above the server cap is CLAMPED, not rejected, and the clamping is
// reported rather than silently applied (US-8 AS-3).
func TestKnowledgeSearch_LimitAboveCapIsClampedAndReported_FR037(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "note.md", "# Note\n\nlandlock\n")
	indexKnowledgeBase(t, api.homePath, vault)

	const asked = 400
	w := knowledgeSearchPost(t, api, ws, map[string]any{
		"query": "landlock", "collection_id": collectionIDOf(t, api, ws, "vault"), "limit": asked,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)

	assert.True(t, resp.LimitClamped, "the clamp is reported, never silent")
	assert.Equal(t, knowledge.SearchMaxTopN, resp.LimitApplied)
	require.NotNil(t, resp.LimitRequested)
	assert.Equal(t, asked, *resp.LimitRequested, "the caller can see exactly what was refused")
	assert.Contains(t, resp.Incompleteness.Statement, fmt.Sprintf("%d", asked))
	assert.Contains(t, resp.Incompleteness.Statement, fmt.Sprintf("%d", knowledge.SearchMaxTopN))
}

// TestKnowledgeSearch_RejectsIncompleteRequests — the two required fields.
func TestKnowledgeSearch_RejectsIncompleteRequests(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	for name, body := range map[string]map[string]any{
		"no query":         {"query": "  ", "collection_id": "kb_whatever"},
		"no collection_id": {"query": "anything", "collection_id": ""},
	} {
		t.Run(name, func(t *testing.T) {
			w := knowledgeSearchPost(t, api, ws, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
}

// TestKnowledgeSearch_InboundSchemaValidationPath_FR080 — with
// gateway.validate_inbound on (the production-hardening posture), the request
// body is checked against the CONTRACT's own KnowledgeSearchRequest schema
// before it is decoded.
//
// This covers the path every other test in this file skips: the default test
// config leaves validation off, so a schema name that does not resolve would
// 500 in production and pass here forever. The valid half proves the schema is
// embedded and compiles; the invalid half proves a bad body is a 400 that names
// the schema, not a 500.
func TestKnowledgeSearch_InboundSchemaValidationPath_FR080(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	api.agentLoop.GetConfig().Gateway.ValidateInbound = true

	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "note.md", "# Note\n\nlandlock\n")
	indexKnowledgeBase(t, api.homePath, vault)
	id := collectionIDOf(t, api, ws, "vault")

	t.Run("a contract-valid body is served", func(t *testing.T) {
		w := knowledgeSearchPost(t, api, ws, map[string]any{"query": "landlock", "collection_id": id})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Len(t, decodeJSON[gen.KnowledgeSearchResponse](t, w).Hits, 1)
	})

	t.Run("a contract-invalid body is refused with 400", func(t *testing.T) {
		// limit has minimum 1 in the schema, and the schema is closed
		// (additionalProperties: false), so both of these are refusals.
		for _, body := range []map[string]any{
			{"query": "landlock", "collection_id": id, "limit": 0},
			{"query": "landlock", "collection_id": id, "not_a_field": true},
		} {
			w := knowledgeSearchPost(t, api, ws, body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "KnowledgeSearchRequest",
				"the refusal names the schema that refused it")
		}
	})
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
