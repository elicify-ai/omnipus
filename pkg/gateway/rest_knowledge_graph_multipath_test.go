// Tests for the multi-path kind=links graph query (UAT D-135, fan-out round).
//
// A base view that must check the link state of many rows used to fire ONE
// GET .../knowledge/graph?kind=links request PER ROW; ten bases in half a
// minute produced 136 hidden 429s from the gateway's own rate limiter. The
// contract now carries a `paths` array for kind=links so the same caller
// makes ONE request. Expected values here come from the CONTRACT
// (contracts/openapi.yaml, getKnowledgeGraph's `paths` parameter): the answer
// is the UNION of the listed notes' outbound edges, source_path is ABSENT
// (the query is not about any single note), and the bound is refused up front
// with a 400 that names the cap.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// multipathVault builds a three-row shape: two notes whose relation cells
// resolve, one whose target is missing.
func multipathVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "People/Sofia Marchetti.md", "# Sofia\n")
	writeNote(t, vault, "People/Tobias Brandt-Larsen.md", "# Tobias\n")
	writeNote(t, vault, "rows/a.md", "owner: \"[[Sofia Marchetti]]\"\n")
	writeNote(t, vault, "rows/b.md", "owner: \"[[Tobias Brandt-Larsen]]\"\n")
	writeNote(t, vault, "rows/c.md", "owner: \"[[Missing Person]]\"\n")
	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestKnowledgeGraph_MultiPathLinksUnion_D135 — three paths in, one answer
// out: the union of every listed note's outbound edges, with source_path
// absent because the query is not about any single note.
func TestKnowledgeGraph_MultiPathLinksUnion_D135(t *testing.T) {
	api, ws, collectionID := multipathVault(t)

	q := url.Values{}
	q.Set("collection_id", collectionID)
	q.Set("kind", "links")
	for _, p := range []string{"rows/a.md", "rows/b.md", "rows/c.md"} {
		q.Add("paths", p)
	}
	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/graph?"+q.Encode())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)

	require.Equal(t, gen.KnowledgeGraphResponseKindLinks, resp.Kind)
	assert.Nil(t, resp.SourcePath, "a multi-path query is about no single note, so source_path is absent")
	require.Len(t, resp.Edges, 3, "the union of three rows' outbound links")

	byFrom := map[string]gen.KnowledgeGraphEdge{}
	for _, e := range resp.Edges {
		byFrom[e.FromPath] = e
	}
	require.Contains(t, byFrom, "rows/a.md")
	require.Contains(t, byFrom, "rows/b.md")
	require.Contains(t, byFrom, "rows/c.md")
	assert.Equal(t, "People/Sofia Marchetti.md", byFrom["rows/a.md"].ToPath)
	assert.Equal(t, gen.KnowledgeGraphEdgeResolutionUniqueBasename, byFrom["rows/a.md"].Resolution)
	assert.Equal(t, gen.KnowledgeGraphEdgeResolutionUnresolved, byFrom["rows/c.md"].Resolution,
		"the missing target is reported unresolved, never dropped")

	// Nodes cover every from and every to, so a client can draw each edge's
	// ends without a second request.
	paths := map[string]bool{}
	for _, n := range resp.Nodes {
		paths[n.Path] = true
	}
	for _, p := range []string{"rows/a.md", "rows/b.md", "rows/c.md", "People/Sofia Marchetti.md", "People/Tobias Brandt-Larsen.md"} {
		assert.True(t, paths[p], "nodes must include %s", p)
	}

	// A duplicate path in the list must not duplicate edges in the answer.
	q2 := url.Values{}
	q2.Set("collection_id", collectionID)
	q2.Set("kind", "links")
	q2.Add("paths", "rows/a.md")
	q2.Add("paths", "rows/a.md")
	w2 := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/graph?"+q2.Encode())
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	resp2 := decodeJSON[gen.KnowledgeGraphResponse](t, w2)
	assert.Len(t, resp2.Edges, 1, "a repeated path is one row, not two edges")
}

// TestKnowledgeGraph_MultiPathLinksRefusals_D135 — the parameter is refused,
// up front and with a sentence, in every shape that would silently mean
// something else.
func TestKnowledgeGraph_MultiPathLinksRefusals_D135(t *testing.T) {
	api, ws, collectionID := multipathVault(t)
	base := "/api/v1/library/" + ws + "/knowledge/graph"

	t.Run("paths with another kind is a 400", func(t *testing.T) {
		w := knowledgeGet(t, api, base+"?collection_id="+collectionID+"&kind=backlinks&paths=rows/a.md")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "kind=links")
	})

	t.Run("path and paths together is a 400", func(t *testing.T) {
		w := knowledgeGet(t, api, base+"?collection_id="+collectionID+"&kind=links&path=rows/a.md&paths=rows/b.md")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "one or the other")
	})

	t.Run("an empty member is a 400", func(t *testing.T) {
		w := knowledgeGet(t, api, base+"?collection_id="+collectionID+"&kind=links&paths=")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("beyond the cap is a 400 naming the cap and the remedy", func(t *testing.T) {
		q := url.Values{}
		q.Set("collection_id", collectionID)
		q.Set("kind", "links")
		for i := 0; i < knowledgeGraphMaxPaths+1; i++ {
			q.Add("paths", "rows/x"+string(rune('a'+i%26))+string(rune('a'+i/26))+".md")
		}
		w := knowledgeGet(t, api, base+"?"+q.Encode())
		assert.Equal(t, http.StatusBadRequest, w.Code)
		// The contract's promise: the bound is "refused up front with a 400
		// naming the cap when exceeded" — and this product's refusal culture
		// adds the remedy (split into batches).
		assert.Contains(t, w.Body.String(), "the limit is 64")
		assert.Contains(t, strings.ToLower(w.Body.String()), "split")
	})

	t.Run("kind=links with neither path nor paths still demands a path", func(t *testing.T) {
		w := knowledgeGet(t, api, base+"?collection_id="+collectionID+"&kind=links")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
