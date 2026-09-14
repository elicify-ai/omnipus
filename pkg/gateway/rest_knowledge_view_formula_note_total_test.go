// Tests that D-61's formula note (a negative date difference explained in the
// cell) never costs a view total a row.
//
// The view renderer totals a column by RE-READING the engine's rendered cell
// text (viewNumberValues): it splits the cell on ", " and silently skips any
// segment that does not parse as a decimal. knowledgefind appends the note to
// the cell as `<number>, note: <explanation>`, so the number stays its own
// parseable segment. A separator glued to the number (`-4 — negative …`) would
// turn the whole first segment into prose and drop the row from every sum, with
// no problem, no exclusion count and no visible sign.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// The expected total is derived from the dates, not from the renderer:
// a.md spans 2026-01-10 minus 2026-01-01 = 9 days; b.md spans 2026-01-01 minus
// 2026-01-05 = -4 days (the negative difference that carries the note).
// 9 + (-4) = 5. A total that lost b.md would answer 9 over one row.
func TestKnowledgeView_FormulaTotalIncludesARowWhoseCellCarriesANote(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Spans vault")
	writeNote(t, vault, ".omnipus-vault/records/job.yaml", "schema_version: 1\n"+
		"type: job\n"+
		"properties:\n"+
		"  opened: { type: date }\n"+
		"  closed: { type: date }\n")
	writeNote(t, vault, "a.md", "---\ntype: job\nid: J-1\nopened: 2026-01-10\nclosed: 2026-01-01\n---\n# J-1\n")
	writeNote(t, vault, "b.md", "---\ntype: job\nid: J-2\nopened: 2026-01-01\nclosed: 2026-01-05\n---\n# J-2\n")
	writeNote(t, vault, ".omnipus-vault/views/spans.yaml", "name: spans\n"+
		"type: job\n"+
		"kind: summary\n"+
		"formulas:\n"+
		"  span: (date(opened) - date(closed)).days\n"+
		"properties:\n"+
		"  - formula.span\n"+
		"parts:\n"+
		"  - part: figures\n"+
		"    number: formula.span\n"+
		"    aggregate: sum\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)
	colID := collectionIDOf(t, api, ws, "vault")

	res, code := getViewResult(t, api, ws, colID, "spans")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal, "a servable formula view must not refuse")
	require.Len(t, res.Rows, 2)

	// Precondition: the interaction under test is really present. b.md's cell
	// must carry the note, or this test proves nothing about it.
	var noted string
	for _, row := range res.Rows {
		if row.Path == "b.md" {
			noted = viewCellValue(&row, "formula.span")
		}
	}
	require.True(t, strings.HasPrefix(noted, "-4") && strings.Contains(noted, "negative because"),
		"b.md's formula.span cell should be -4 with its explanation; got %q", noted)

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 1)
	assert.Equal(t, "5", totals[0].Value,
		"9 + (-4) = 5; a total of 9 means the note-bearing row was silently dropped")
	assert.Equal(t, 2, totals[0].Count, "both rows must be in the total")
}
