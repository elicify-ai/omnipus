// Tests for how the view-result endpoint COLLECTS its rows: how many times it
// evaluates the query, how many rows it carries, what it says when it carries
// fewer than exist, and what a group is allowed to name.
//
// Three review findings meet here:
//
//	#5  collectRows paged through the engine's OFFSET cursor. Each page is a
//	    fresh Find() that re-runs the WHOLE evaluation — filter, sort,
//	    aggregate — and throws away everything before the offset. Ten pages
//	    therefore cost ten complete evaluations of the same query per HTTP
//	    request. The engine's per-response byte budget (4 kB, sized for an LLM
//	    reader) also trimmed each page, so a view over a few hundred records
//	    could not reach its own 2000-row cap no matter how many pages it took.
//
//	#6a the paging loop tested `len(rows) < cap` BEFORE fetching, so the last
//	    page could overshoot to cap+pageSize−1 rows — 2199 for a 2000 cap.
//
//	#3  a grouped part copied every group's FULL member-path list from the
//	    engine, so 100k matching records produced ~100k path strings per
//	    grouped part regardless of the 2000-row cap — and those paths pointed
//	    at rows the answer does not carry, which the contract says they index
//	    into.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// buildPagingTestVault seeds `n` invoices, all one currency (SGD) so the
// arithmetic under test is the ROW COLLECTION rather than G2's unit keying,
// alternating between two clients so a grouping has something to do.
func buildPagingTestVault(t *testing.T, n int, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", viewTestInvoiceSchema)

	for i := 0; i < n; i++ {
		client := "Acme"
		if i%2 == 1 {
			client = "Bolt"
		}
		// One dollar each, so the expected sum is the row count exactly and a
		// short read is visible as a wrong number rather than a plausible one.
		writeNote(t, vault, fmt.Sprintf("n%04d.md", i),
			viewTestInvoice(fmt.Sprintf("INV-%04d", i), client, "1.00", "SGD"))
	}
	for name, body := range views {
		writeNote(t, vault, ".omnipus-vault/views/"+name, body)
	}

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// countViewEvaluations swaps the Find seam for a counting wrapper for the
// duration of one call, and returns how many evaluations it took.
func countViewEvaluations(t *testing.T, run func()) int {
	t.Helper()
	var n int64
	prev := viewResultFind
	viewResultFind = func(ctx context.Context, d knowledgefind.Deps, req gen.VaultFindRequest) (gen.VaultFindResponse, error) {
		atomic.AddInt64(&n, 1)
		return prev(ctx, d, req)
	}
	t.Cleanup(func() { viewResultFind = prev })
	run()
	return int(atomic.LoadInt64(&n))
}

// withMaxViewResultRows lowers the row cap for one test, so the cap's
// EXACTNESS can be proven against a corpus one row past it rather than against
// a 2001-note fixture nobody would wait for.
func withMaxViewResultRows(t *testing.T, n int) {
	t.Helper()
	prev := maxViewResultRows
	maxViewResultRows = n
	t.Cleanup(func() { maxViewResultRows = prev })
}

const viewPagingSummary = "name: everything\n" +
	"type: invoice\n" +
	"kind: summary\n" +
	"parts:\n" +
	"  - part: figures\n" +
	"    number: amount\n" +
	"    aggregate: sum\n" +
	"  - part: table\n" +
	"    grouping: [{property: client, direction: asc}]\n" +
	"    subtotals: {amount: sum}\n"

// TestKnowledgeViewPaging_OneEvaluationCarriesEveryRow is finding #5. 250
// invoices is well past both bounds that used to cut the walk short — the
// engine's 200-row page cap and its 4 kB response budget — so a correct answer
// requires the collector to ask for the whole set in ONE evaluation and to be
// exempt from a byte budget written for a language model's context window.
func TestKnowledgeViewPaging_OneEvaluationCarriesEveryRow(t *testing.T) {
	const rows = 250
	api, ws, colID := buildPagingTestVault(t, rows, map[string]string{
		"everything.yaml": viewPagingSummary,
	})

	var res *gen.ViewResult
	evaluations := countViewEvaluations(t, func() {
		var code int
		res, code = getViewResult(t, api, ws, colID, "everything")
		require.Equal(t, 200, code)
	})

	require.NotNil(t, res)
	require.Nil(t, res.Refusal)

	// ONE evaluation. Every part of this view groups the way the view itself
	// does, so nothing needs a second grouping pass, and the row walk itself
	// must not need one either.
	assert.Equal(t, 1, evaluations,
		"a view whose parts all group the way the view does must cost ONE evaluation; "+
			"offset paging re-ran the whole query per page")

	assert.Len(t, res.Rows, rows, "every matching record is carried; the 4 kB LLM byte budget must not bind an HTTP renderer")
	assert.Nil(t, res.RowsTruncated, "250 rows is inside the cap, so nothing is truncated")
	assert.True(t, res.Complete)

	// The total proves the rows were all COUNTED, not merely all listed: each
	// invoice is 1.00, so the sum is the row count.
	require.Len(t, res.Parts, 2)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 1)
	assert.Equal(t, "250.00", totals[0].Value, "1.00 x 250, by hand")
	assert.Equal(t, rows, totals[0].Count)
}

// TestKnowledgeViewPaging_CapIsExactAndTruncationIsStated is finding #6a. With
// the cap at 5 and 12 records in scope, the answer carries EXACTLY 5 rows —
// never 5 plus whatever the last page happened to bring — says so, and
// computes no total over the truncated set.
func TestKnowledgeViewPaging_CapIsExactAndTruncationIsStated(t *testing.T) {
	withMaxViewResultRows(t, 5)
	api, ws, colID := buildPagingTestVault(t, 12, map[string]string{
		"everything.yaml": viewPagingSummary,
	})

	res, code := getViewResult(t, api, ws, colID, "everything")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)

	assert.Len(t, res.Rows, 5, "the cap is a cap, not a floor to overshoot from")
	require.NotNil(t, res.RowsTruncated)
	assert.True(t, *res.RowsTruncated)
	assert.False(t, res.Complete)
	require.NotNil(t, res.CompleteReason)
	assert.Contains(t, *res.CompleteReason, "5")

	require.Len(t, res.Parts, 2)
	assert.Nil(t, res.Parts[0].Totals, "no total is computed over a truncated set")
	if res.Parts[1].Totals != nil {
		assert.Empty(t, *res.Parts[1].Totals)
	}
}

// TestKnowledgeViewPaging_GroupPathsAreBoundedToCarriedRows is finding #3. A
// group's `paths` are documented as references INTO the result's own `rows`,
// so a path the answer does not carry is a dangling reference — and copying
// every group's full membership makes the payload grow with the corpus rather
// than with the cap. The omission is COUNTED, never silent.
func TestKnowledgeViewPaging_GroupPathsAreBoundedToCarriedRows(t *testing.T) {
	withMaxViewResultRows(t, 4)
	api, ws, colID := buildPagingTestVault(t, 12, map[string]string{
		"everything.yaml": viewPagingSummary,
	})

	res, code := getViewResult(t, api, ws, colID, "everything")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Rows, 4)

	carried := map[string]bool{}
	for _, row := range res.Rows {
		carried[row.Path] = true
	}

	require.Len(t, res.Parts, 2)
	require.NotNil(t, res.Parts[1].Groups)
	groups := *res.Parts[1].Groups
	require.Len(t, groups, 2, "Acme and Bolt")

	namedPaths, omitted := 0, 0
	for _, g := range groups {
		for _, p := range g.Paths {
			assert.True(t, carried[p],
				"group %q names %q, which is not among the rows this answer carries; "+
					"a group's paths index into `rows`", g.Key, p)
		}
		namedPaths += len(g.Paths)
		assert.Equal(t, 6, g.Count, "the group's own count still states the FULL evaluated size")
		require.NotNil(t, g.PathsOmitted,
			"group %q names %d of its %d members; the shortfall must be COUNTED, not silent",
			g.Key, len(g.Paths), g.Count)
		assert.Equal(t, g.Count-len(g.Paths), *g.PathsOmitted)
		omitted += *g.PathsOmitted
	}
	assert.Equal(t, 4, namedPaths, "the paths named across every group are bounded by the rows carried")
	assert.Equal(t, 8, omitted, "12 records, 4 carried, 8 unreferenced — and said so")
}

// TestKnowledgeViewPaging_UngroupedGroupsNameEveryCarriedRow is the
// over-bounding control: inside the cap, nothing is omitted and the omission
// counter stays absent rather than reporting a truthful zero, which a renderer
// would have to special-case.
func TestKnowledgeViewPaging_UngroupedGroupsNameEveryCarriedRow(t *testing.T) {
	api, ws, colID := buildPagingTestVault(t, 10, map[string]string{
		"everything.yaml": viewPagingSummary,
	})

	res, code := getViewResult(t, api, ws, colID, "everything")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 2)
	require.NotNil(t, res.Parts[1].Groups)

	for _, g := range *res.Parts[1].Groups {
		assert.Equal(t, 5, g.Count)
		assert.Len(t, g.Paths, 5, "every member is carried, so every member is named")
		assert.Nil(t, g.PathsOmitted, "nothing omitted means the field is absent, not zero")
	}
}
