// Tests for CW-2's three fields reaching the wire (ADR-083 spec, Step 1c):
// heading_found, block and unresolved_reason on gen.KnowledgeGraphEdge.
//
// heading_found and block already existed on knowledge.ResolvedLink and were
// never projected by knowledgeEdge(); unresolved_reason is new. The load-
// bearing risk this file guards against is named in the spec itself: a
// direct cast of knowledge.UnresolvedReason onto the wire enum would compile
// (both are named string types) and then emit "outside_collection" for
// knowledge.ReasonOutsideRoot — a value contracts/components/schemas/
// KnowledgeGraphEdge.yaml's unresolved_reason enum does not contain at all,
// which the SPA's generated Zod validator rejects, dropping the ENTIRE
// knowledge-graph response rather than just this field.

package gateway

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// edgeByLinkText finds the one edge in resp whose link_text matches want, or
// fails the test. link_text is Link.Target with any alias AND anchor already
// stripped (see pkg/knowledge/links.go's Link doc comment), so this is only
// safe to use when a fixture's edges have distinct targets — never when two
// edges share a target and differ only by heading or block fragment, which
// is exactly the shape the heading_found and block tests below need. Those
// use edgeWhere instead.
func edgeByLinkText(t *testing.T, resp gen.KnowledgeGraphResponse, want string) gen.KnowledgeGraphEdge {
	t.Helper()
	for _, e := range resp.Edges {
		if e.LinkText != nil && *e.LinkText == want {
			return e
		}
	}
	t.Fatalf("no edge with link_text %q in %d edges", want, len(resp.Edges))
	return gen.KnowledgeGraphEdge{}
}

// edgeWhere finds the exactly-one edge in resp satisfying match, or fails the
// test with a description of what was expected and everything that was
// actually present — used wherever link_text alone cannot distinguish two
// edges (two links to the same target, differing only by heading or block
// fragment).
func edgeWhere(t *testing.T, resp gen.KnowledgeGraphResponse, desc string, match func(gen.KnowledgeGraphEdge) bool) gen.KnowledgeGraphEdge {
	t.Helper()
	var got []gen.KnowledgeGraphEdge
	for _, e := range resp.Edges {
		if match(e) {
			got = append(got, e)
		}
	}
	require.Lenf(t, got, 1, "expected exactly one edge matching %q among %d edges: %#v", desc, len(resp.Edges), resp.Edges)
	return got[0]
}

// TestKnowledgeEdge_HeadingFoundTrueAndFalse — Test 39. The pair: a link to a
// heading that exists in the target and one to a heading that does not, from
// the SAME source note against the SAME target, so nothing but the anchor
// text differs between the two edges. A handler hardcoding heading_found to
// false passes the "does not exist" half and fails the "exists" half; a
// handler hardcoding it to true fails the reverse. Only projecting the
// resolver's own computed value passes both.
func TestKnowledgeEdge_HeadingFoundTrueAndFalse(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "Target.md", "# Target\n\n## Real Heading\n")
	writeNote(t, vault, "source.md", "[[Target#Real Heading]] and [[Target#Missing Heading]]\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+
		"/knowledge/graph?collection_id="+collectionIDOf(t, api, ws, "vault")+
		"&kind=links&path=source.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)
	require.Len(t, resp.Edges, 2)

	found := edgeWhere(t, resp, "heading=Real Heading", func(e gen.KnowledgeGraphEdge) bool {
		return e.Heading != nil && *e.Heading == "Real Heading"
	})
	require.NotNil(t, found.HeadingFound, "heading_found must always be set on an edge whose target resolved")
	assert.True(t, found.HeadingFound, "the heading exists in the target and must be reported found")

	missing := edgeWhere(t, resp, "heading=Missing Heading", func(e gen.KnowledgeGraphEdge) bool {
		return e.Heading != nil && *e.Heading == "Missing Heading"
	})
	require.NotNil(t, missing.HeadingFound)
	assert.False(t, missing.HeadingFound, "the heading does not exist in the target and must be reported not found")
}

// TestKnowledgeEdge_HeadingFoundIsFalseForBaseAndBlockTargets — Test 110. The
// heading_found flag is computed by pkg/knowledge (graph.go) only when the
// target's headings were recorded AND the link carried a non-empty Heading —
// and headings are recorded for markdown files only. So the flag must read
// false BY CONSTRUCTION for a ".base" target (its fragment is a view label,
// not a heading) and for a link that carries a block anchor instead of a
// heading (Heading is empty; BlockID is not). Paired with a markdown target
// whose heading genuinely IS found, so a handler hardcoding false everywhere
// still fails.
func TestKnowledgeEdge_HeadingFoundIsFalseForBaseAndBlockTargets(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "Target.md", "# Target\n\n## Real Heading\n")
	// Contents are never read for a non-markdown collection member (graph.go's
	// own comment: "their contents are never read for any reason") — the file
	// only needs to exist so the wikilink resolves.
	writeNote(t, vault, "Dashboard.base", "views: []\n")
	writeNote(t, vault, "source.md",
		"![[Dashboard.base#My View]] and [[Target#^abc123]] and [[Target#Real Heading]]\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+
		"/knowledge/graph?collection_id="+collectionIDOf(t, api, ws, "vault")+
		"&kind=links&path=source.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)
	require.Len(t, resp.Edges, 3)

	baseEdge := edgeByLinkText(t, resp, "Dashboard.base")
	require.Equal(t, gen.KnowledgeGraphEdgeResolutionExactPath, baseEdge.Resolution, "fixture sanity: the .base target must resolve")
	require.NotNil(t, baseEdge.HeadingFound)
	assert.False(t, baseEdge.HeadingFound, "a .base target's fragment is a view label, never a heading")

	blockEdge := edgeWhere(t, resp, "block=abc123", func(e gen.KnowledgeGraphEdge) bool {
		return e.Block != nil && *e.Block == "abc123"
	})
	require.Equal(t, gen.KnowledgeGraphEdgeResolutionExactPath, blockEdge.Resolution, "fixture sanity: the block target must resolve")
	require.NotNil(t, blockEdge.HeadingFound)
	assert.False(t, blockEdge.HeadingFound, "a block reference carries no heading text to match")

	headingEdge := edgeWhere(t, resp, "heading=Real Heading", func(e gen.KnowledgeGraphEdge) bool {
		return e.Heading != nil && *e.Heading == "Real Heading"
	})
	require.NotNil(t, headingEdge.HeadingFound)
	assert.True(t, headingEdge.HeadingFound, "paired control: a genuine markdown heading match must still read true")
}

// TestKnowledgeEdge_BlockAnchorProjectedSeparately — Test 40. BlockID's first
// assertion ever (it was parsed by pkg/knowledge and read by nothing until
// this wave): the block anchor lands on its own "block" field, and "heading"
// stays empty for that same edge — a block reference is a distinct
// addressing form, never a heading fragment.
func TestKnowledgeEdge_BlockAnchorProjectedSeparately(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "Target.md", "# Target\n")
	writeNote(t, vault, "source.md", "[[Target#^abc123]]\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+
		"/knowledge/graph?collection_id="+collectionIDOf(t, api, ws, "vault")+
		"&kind=links&path=source.md")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)
	require.Len(t, resp.Edges, 1)

	e := resp.Edges[0]
	require.NotNil(t, e.Block, "the block anchor must be projected onto its own field")
	assert.Equal(t, "abc123", *e.Block)
	assert.Nil(t, e.Heading, "a block reference must never populate the heading field")
}

// TestKnowledgeEdge_UnresolvedReasonDistinguishesAbsenceFromContainment —
// Test 111. One link names a target nothing in the collection carries; a
// second, in the same note, traverses out of the collection root. Both are
// unresolved, and a handler that hardcodes unresolved_reason to either value
// fails one half of this test.
func TestKnowledgeEdge_UnresolvedReasonDistinguishesAbsenceFromContainment(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "source.md", "[[Nowhere]] and [[../../../.ssh/id_rsa]]\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+
		"/knowledge/graph?collection_id="+collectionIDOf(t, api, ws, "vault")+
		"&kind=unresolved")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeGraphResponse](t, w)
	require.Len(t, resp.Edges, 2)

	absent := edgeByLinkText(t, resp, "Nowhere")
	require.NotNil(t, absent.UnresolvedReason, "an unresolved edge must always carry a reason")
	assert.Equal(t, gen.KnowledgeGraphEdgeUnresolvedReasonNoMatch, *absent.UnresolvedReason)

	escaping := edgeByLinkText(t, resp, "../../../.ssh/id_rsa")
	require.NotNil(t, escaping.UnresolvedReason)
	assert.Equal(t, gen.KnowledgeGraphEdgeUnresolvedReasonOutsideRoot, *escaping.UnresolvedReason)

	assert.NotEqual(t, *absent.UnresolvedReason, *escaping.UnresolvedReason,
		"an ordinary broken link and a containment escape must report different reasons")
}

// TestKnowledgeEdgeUnresolvedReason_MapsEveryGoConstant — Test 41's Go-side
// proof. knowledge.AllUnresolvedReasons() is the SAME list pkg/knowledge
// itself uses to enumerate its closed set (rather than a list this test
// author remembers), so adding a reason to links.go's const block without
// adding it to AllUnresolvedReasons() is the only way to silently escape this
// test — and that omission is one file, a few lines from the const block it
// must track. gen.KnowledgeGraphEdgeUnresolvedReason.Valid() is generated
// FROM contracts/components/schemas/KnowledgeGraphEdge.yaml's enum, so a
// passing assertion here is a real claim about what the contract permits,
// not a value compared to itself.
//
// Validity alone is NOT enough: the wire enum has only two members
// (no_match, outside_root) for four Go reasons, so the mapper necessarily
// groups two-and-two — and a mapper that swapped which pair goes where
// (e.g. ReasonEmptyTarget <-> ReasonAbsoluteTarget) would still return a
// VALID wire value for every input, so a Valid()-only assertion cannot see
// the swap. The `want` table below pins the exact expected value per
// reason, derived from the semantics both links.go's doc comments and
// KnowledgeGraphEdge.yaml's `unresolved_reason` description state
// independently of this mapper's current code: "no_match" is an ordinary
// broken link that never attempted to leave the collection at all
// (ReasonNoMatch — nothing in the collection carries that path/name;
// ReasonEmptyTarget — "[[]]" names nothing, so there was never a path to
// escape with); "outside_root" is a link that DID try to leave the
// collection root (ReasonAbsoluteTarget — an absolute filesystem path is
// itself an attempt to leave the collection, e.g. [[/etc/passwd]];
// ReasonOutsideRoot — literal ../ traversal past the root). Swapping
// Empty<->Absolute inverts exactly the containment signal the contract
// calls out by name — the scenario this test exists to catch.
func TestKnowledgeEdgeUnresolvedReason_MapsEveryGoConstant(t *testing.T) {
	reasons := knowledge.AllUnresolvedReasons()
	require.NotEmpty(t, reasons, "fixture sanity: the source-of-truth list must not be empty")

	want := map[knowledge.UnresolvedReason]gen.KnowledgeGraphEdgeUnresolvedReason{
		knowledge.ReasonNoMatch:        gen.KnowledgeGraphEdgeUnresolvedReasonNoMatch,
		knowledge.ReasonEmptyTarget:    gen.KnowledgeGraphEdgeUnresolvedReasonNoMatch,
		knowledge.ReasonAbsoluteTarget: gen.KnowledgeGraphEdgeUnresolvedReasonOutsideRoot,
		knowledge.ReasonOutsideRoot:    gen.KnowledgeGraphEdgeUnresolvedReasonOutsideRoot,
	}

	for _, r := range reasons {
		t.Run(string(r), func(t *testing.T) {
			wantWire, known := want[r]
			require.True(t, known,
				"no expected wire value pinned for knowledge.UnresolvedReason %q — add it to "+
					"this test's `want` table (checked against KnowledgeGraphEdge.yaml's "+
					"containment-signal semantics, not against whatever the mapper currently "+
					"does) before trusting knowledgeEdgeUnresolvedReason for it", string(r))

			wire := knowledgeEdgeUnresolvedReason(r)
			assert.True(t, wire.Valid(),
				"knowledgeEdgeUnresolvedReason(%q) = %q is not a value the generated "+
					"KnowledgeGraphEdgeUnresolvedReason enum permits", string(r), string(wire))
			assert.Equal(t, wantWire, wire,
				"knowledgeEdgeUnresolvedReason(%q) = %q, want %q — a mapper that swaps two "+
					"reasons into each other's group still emits a VALID value, so exact "+
					"equality (not mere validity) is required to catch that", string(r), string(wire), string(wantWire))
		})
	}
}

// TestKnowledgeEdgeUnresolvedReason_PanicsOnUnmappedReason proves the mapper
// has no silent default: a knowledge.UnresolvedReason value it does not
// recognise must panic rather than fall through to a plausible-looking wire
// value nobody decided was correct. net/http recovers a panicking request
// handler per-connection, so this fails loudly without taking the process
// down — the same trade already made by rest.go's ensureMap panic.
func TestKnowledgeEdgeUnresolvedReason_PanicsOnUnmappedReason(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "an unmapped UnresolvedReason must panic, not silently map to a value")
	}()
	_ = knowledgeEdgeUnresolvedReason(knowledge.UnresolvedReason("some_future_reason"))
	t.Fatal("unreachable: knowledgeEdgeUnresolvedReason must panic on an unmapped reason")
}

// TestKnowledgeEdgeUnresolvedReason_DirectCastWouldEmitInvalidValue documents
// and proves the exact trap the task brief named: knowledge.ReasonOutsideRoot
// is the Go string "outside_collection", not "outside_root" — a direct cast
// compiles (both are named string types) and produces a value the wire enum
// forbids. If this test's first assertion ever starts failing, the Go
// constant and the wire value have been reconciled and knowledgeEdge no
// longer needs an explicit mapper for this member — until then, the mapper
// is load-bearing.
func TestKnowledgeEdgeUnresolvedReason_DirectCastWouldEmitInvalidValue(t *testing.T) {
	direct := gen.KnowledgeGraphEdgeUnresolvedReason(knowledge.ReasonOutsideRoot)
	require.False(t, direct.Valid(),
		"a direct cast of knowledge.ReasonOutsideRoot (%q) is expected to be wire-invalid; "+
			"if it is now valid, the trap this test guards no longer applies", string(direct))

	mapped := knowledgeEdgeUnresolvedReason(knowledge.ReasonOutsideRoot)
	assert.True(t, mapped.Valid(), "the mapper must produce a wire-valid value where a direct cast would not")
	assert.NotEqual(t, direct, mapped, "the mapper must not simply reproduce the invalid direct cast")
}
