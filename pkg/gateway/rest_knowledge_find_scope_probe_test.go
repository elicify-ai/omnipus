// Regression test for a code-review finding (2026-09-07, G1):
//
// FR-053's invariant is that an out-of-scope collection_id must be
// INDISTINGUISHABLE from an in-scope-but-empty one — including the prose, not
// only the hit arrays (see the comment on vaultSearchCompleteStatement). The
// MV-9 honesty fields (notes_searched/notes_total_known, and the statement
// composed from them) broke that: an out-of-scope collection_id short-circuits
// to emptyVaultSearchResponse (no honesty fields, the generic "searched the
// whole of this knowledge base" sentence), while an in-scope collection with
// zero matches runs the real search and gets real notes_searched/
// notes_total_known plus a "Searched N of N notes known to the index."
// sentence. A caller can tell the two apart by those fields alone, without
// ever looking at the (identically empty) hit arrays.
package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestVaultSearch_OutOfScopeIndistinguishableFromInScopeEmpty is spec test
// FR-053: a probing caller must not be able to tell an out-of-scope
// collection_id apart from an in-scope collection that just has no matches —
// neither by the hit arrays (already covered) nor by the MV-9 honesty fields
// or the composed statement.
func TestVaultSearch_OutOfScopeIndistinguishableFromInScopeEmpty(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	const noMatchQuery = "zzz-nonexistent-term-zzz"

	inScopeEmpty := vaultFindPost(t, api, ws, map[string]any{
		"query": noMatchQuery, "collection_id": colID,
	})
	require.Equal(t, http.StatusOK, inScopeEmpty.Code, inScopeEmpty.Body.String())
	inResp := decodeJSON[gen.VaultSearchResponse](t, inScopeEmpty)

	outOfScope := vaultFindPost(t, api, ws, map[string]any{
		"query": noMatchQuery, "collection_id": "kb_0000000000000000",
	})
	require.Equal(t, http.StatusOK, outOfScope.Code, outOfScope.Body.String())
	outResp := decodeJSON[gen.VaultSearchResponse](t, outOfScope)

	// The hit arrays already match (both empty) — the finding is about what
	// else differs.
	assert.Equal(t, outResp.Notes, inResp.Notes)
	assert.Equal(t, outResp.Records, inResp.Records)
	assert.Equal(t, outResp.Views, inResp.Views)
	assert.Equal(t, outResp.Complete, inResp.Complete)

	assert.Equal(t, outResp.NotesSearched, inResp.NotesSearched,
		"notes_searched must not leak whether the collection is in scope")
	assert.Equal(t, outResp.NotesTotalKnown, inResp.NotesTotalKnown,
		"notes_total_known must not leak whether the collection is in scope")
	require.NotNil(t, outResp.Statement)
	require.NotNil(t, inResp.Statement)
	assert.Equal(t, *outResp.Statement, *inResp.Statement,
		"the composed statement must not leak whether the collection is in scope")
}

// TestVaultSearch_RateLimiterOrderingClosesTheScopeProbeOracle is F6
// (2026-09-08 code review): an out-of-scope collection_id used to
// short-circuit BEFORE ever consuming/observing allowKnowledgeRetrieval,
// while an in-scope one always went through it. That let a caller drain the
// workspace-keyed limiter and then probe: a 429 meant "in scope" (the
// request reached the limiter), a 200 with the empty-but-complete body meant
// "out of scope" (the request never touched the limiter at all) — a probe
// oracle on the STATUS CODE even though FR-053's body invariant (the test
// above) held throughout. This proves both cases now answer 429 identically
// once the limiter is drained, regardless of scope.
func TestVaultSearch_RateLimiterOrderingClosesTheScopeProbeOracle(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	for i := 0; i < knowledgeRESTLimiter.Limit(); i++ {
		knowledgeRESTLimiter.Allow(knowledgeRateKey(ws))
	}

	inScope := vaultFindPost(t, api, ws, map[string]any{
		"query": "seccomp", "collection_id": colID,
	})
	assert.Equalf(t, http.StatusTooManyRequests, inScope.Code,
		"an in-scope request must be rate-limited once the workspace's limiter is drained: %s",
		inScope.Body.String())

	outOfScope := vaultFindPost(t, api, ws, map[string]any{
		"query": "seccomp", "collection_id": "kb_0000000000000000",
	})
	assert.Equalf(t, http.StatusTooManyRequests, outOfScope.Code,
		"an out-of-scope collection_id must be rate-limited IDENTICALLY to an in-scope one — a 200 "+
			"here while the in-scope request above got 429 is a probe oracle: it tells a caller "+
			"draining the limiter and testing candidate collection_ids exactly which ones exist "+
			"in this workspace: %s", outOfScope.Body.String())
}

// TestVaultSearch_UndrainedLimiterStillServesBothScopesNormally is the
// negative control: with the limiter NOT drained, an in-scope query still
// gets its real search and an out-of-scope one still gets the empty-but-
// complete shape — proving the F6 fix only changes what happens once the
// limiter is exhausted, not ordinary traffic.
func TestVaultSearch_UndrainedLimiterStillServesBothScopesNormally(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	inScope := vaultFindPost(t, api, ws, map[string]any{
		"query": "seccomp", "collection_id": colID,
	})
	require.Equal(t, http.StatusOK, inScope.Code, inScope.Body.String())
	inResp := decodeJSON[gen.VaultSearchResponse](t, inScope)
	assert.NotEmpty(t, inResp.Notes, "an in-scope query with a real match must still search for real")

	outOfScope := vaultFindPost(t, api, ws, map[string]any{
		"query": "seccomp", "collection_id": "kb_0000000000000001",
	})
	require.Equal(t, http.StatusOK, outOfScope.Code, outOfScope.Body.String())
	outResp := decodeJSON[gen.VaultSearchResponse](t, outOfScope)
	assert.Empty(t, outResp.Notes)
	assert.True(t, outResp.Complete)
}
