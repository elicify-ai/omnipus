// Omnipus — DEFECT 1b: the view endpoint must never serve a vacuous or
// ineligible part silently (view-kinds-design-2026-09-03 §3 G1, ruling D6).
//
// design §3 puts all six gates "in the composer AND the renderer". The renderer
// carried G2, G3 and G4; G1 — "a kind is offered only when the collection has
// the properties it requires" — was nowhere on the read path. The 2026-09-05
// UAT is what that cost: a `kind: trend` file whose figures part named no
// number and whose chart named no number came back HTTP 200, `refusal: None`,
// `problems: []` — an empty figures row, an empty chart, 131 rows of table. A
// wrong VIEW that looks right, which is §1's stated worst case in a new form.
//
// D6 closes op=write_view so no agent authors that again. This file is the
// other half: a parts-bearing file that got in by ANY path — a hand-edited
// file, a .base import, a binary written before D6 — is caught at READ time.
//
// The expected values below come from the DESIGN's G1 rule and from D5's
// ruling on tiles, not from what the handler happens to do.
//
//	Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//	       -run '^TestKnowledgeView_Part' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// problemsWithCode returns every problem carrying one code, so an assertion
// can be made about the code that matters without being defeated by another
// problem the same result legitimately carries.
func problemsWithCode(res *gen.ViewResult, code gen.RecordProblemCode) []gen.RecordProblem {
	var out []gen.RecordProblem
	for _, p := range res.Problems {
		if p.Code == code {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The UAT's exact artefact, byte for byte
// ---------------------------------------------------------------------------

// viewTestUATTrend is the file the tester agent's tenth write_view call landed
// on disk after create_view had refused the same request twice: `kind: trend`
// with a figures part carrying NO `number:` binding at all, and a chart that
// cannot plot either.
//
// ONE DELIBERATE DEVIATION from the UAT's bytes. Its chart named `date:
// completed` on a record type that declares `completed` — this fixture's
// `invoice` type declares no date property, and a part binding a property the
// record type does not declare is rejected by the view LOADER before the
// renderer ever sees it (a different, already-covered answer: a refusal, not a
// problem). So the chart here binds `amount` (a real number) and `client` (a
// real property that is TEXT, not a date), which exercises the other branch of
// the same gate: a binding that exists and is INELIGIBLE, rather than absent.
// Between them the two parts cover both halves.
const viewTestUATTrend = "name: uat-trend\n" +
	"type: invoice\n" +
	"kind: trend\n" +
	"parts:\n" +
	"  - part: figures\n" +
	"    aggregate: count\n" +
	"  - part: chart\n" +
	"    aggregate: count\n" +
	"    number: amount\n" +
	"    date: client\n" +
	"  - part: table\n"

func TestKnowledgeView_PartWithNoNumberIsReportedNotServedEmpty(t *testing.T) {
	api, ws, col := buildViewTestVault(t, map[string]string{"uat-trend.yaml": viewTestUATTrend})

	res, code := getViewResult(t, api, ws, col, "uat-trend")
	require.Equal(t, 200, code)
	require.NotNil(t, res)

	// The UAT's finding, stated as the assertion that would have caught it:
	// this exact response came back with problems: [].
	require.NotEmpty(t, res.Problems,
		"a trend whose figures and chart bind no number must not be served clean")

	found := problemsWithCode(res, gen.ViewPartIneligible)
	require.Len(t, found, 2,
		"both the figures part and the chart part draw nothing; each is its own fault to fix")

	joined := found[0].Reason + "\n" + found[1].Reason
	assert.Contains(t, joined, "figures", "the problem must name the part at fault")
	assert.Contains(t, joined, "chart")
	assert.Contains(t, joined, "number", "the problem must name the FAILED REQUIREMENT, not just 'invalid'")
	assert.Contains(t, joined, "date", "and the chart's own failed requirement, separately")
	for _, p := range found {
		require.NotNil(t, p.Fix, "a problem without a remedy is half a problem")
		assert.Contains(t, *p.Fix, "create_view", "the remedy is the op that would have refused this at write time")
	}

	// complete: false is the wire's own "do not read this as a full answer".
	assert.False(t, res.Complete, "a view with a part that draws nothing is not a complete answer")

	// The rows are still shown — this is a report, never a blank panel.
	assert.NotEmpty(t, res.Rows, "the table part's rows must still be served")
	assert.Nil(t, res.Refusal, "a part-level fault refuses the PART, not the whole view")
}

// ---------------------------------------------------------------------------
// D5's tiles case, asserted through the read path
// ---------------------------------------------------------------------------

// viewTestTilesOnEnum is the UAT's directed probe: `tiles` bound to `currency`,
// an enum. D5 ruled that tiles "ships gated off" because no property type in
// records.PropertyTypes is image-capable, and rejected binding tiles to text
// because it "would bind a rendering behaviour to unvalidated strings". An
// enum is no more image-capable than text.
const viewTestTilesOnEnum = "name: uat-tiles\n" +
	"type: invoice\n" +
	"kind: tiles\n" +
	"parts:\n" +
	"  - part: tiles\n" +
	"    image: currency\n"

func TestKnowledgeView_TilesPartBoundToANonImagePropertyIsReported(t *testing.T) {
	api, ws, col := buildViewTestVault(t, map[string]string{"uat-tiles.yaml": viewTestTilesOnEnum})

	res, code := getViewResult(t, api, ws, col, "uat-tiles")
	require.Equal(t, 200, code)
	require.NotNil(t, res)

	found := problemsWithCode(res, gen.ViewPartIneligible)
	require.Len(t, found, 1, "the tiles part must be reported, not served with problems: []")
	// D5's own wording, the same string knowledge_describe and create_view both
	// give — there is one eligibility switch, so there is one reason.
	assert.Contains(t, found[0].Reason, "no image-capable property type exists yet")
}

// ---------------------------------------------------------------------------
// The gate does not fire on what it must not fire on
// ---------------------------------------------------------------------------

func TestKnowledgeView_PartGateStaysSilentOnAWellBoundStack(t *testing.T) {
	// viewTestSummaryView is the composer's own output: figures bound to
	// `amount` with `unit: currency`, and a grouped table with subtotals. If
	// the gate fires here it is not a gate, it is noise.
	api, ws, col := buildViewTestVault(t, map[string]string{"unpaid--by-client.yaml": viewTestSummaryView})

	res, code := getViewResult(t, api, ws, col, "unpaid--by-client")
	require.Equal(t, 200, code)
	require.NotNil(t, res)
	assert.Empty(t, problemsWithCode(res, gen.ViewPartIneligible),
		"a correctly bound part stack must produce no part-eligibility problem")
}

// viewTestLegacyBoardLayout is the shape 69 of the founder's own imported views
// have: a `layout`, no `parts` at all. EffectiveParts synthesises ONE part from
// the layout — a `columns` part with no `choice:`, because a legacy file never
// had one to give. Judging a synthesised part by a rule its file predates would
// put a problem on every imported board in the vault, which is why the gate is
// scoped to files that declare `parts` themselves.
const viewTestLegacyBoardLayout = "name: legacy-board\n" +
	"type: invoice\n" +
	"layout: board\n" +
	"grouping: [{property: client}]\n"

func TestKnowledgeView_PartGateDoesNotJudgeALegacyLayoutOnlyView(t *testing.T) {
	api, ws, col := buildViewTestVault(t, map[string]string{"legacy-board.yaml": viewTestLegacyBoardLayout})

	res, code := getViewResult(t, api, ws, col, "legacy-board")
	require.Equal(t, 200, code)
	require.NotNil(t, res)
	assert.Empty(t, problemsWithCode(res, gen.ViewPartIneligible),
		"a legacy layout-only view declares no parts; the D6 read-time gate is not about it")
	assert.Nil(t, res.Refusal)
	assert.NotEmpty(t, res.Rows)
}
