// Omnipus — UAT 2026-09-13 D-69: a legacy `layout: calendar` view (the shape
// every imported Obsidian calendar arrives in) must reach the SPA with a
// `date:` binding on its synthesised calendar part, or say why it cannot.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// buildCalendarTestVault seeds a vault with the given record schema, three
// project notes (two dated, one with no start) and the given view files.
func buildCalendarTestVault(t *testing.T, schema string, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Projects vault")
	writeNote(t, vault, ".omnipus-vault/records/project.yaml", schema)
	writeNote(t, vault, "a.md", "---\ntype: project\nid: PRJ-1\nstatus: active\nstart: 2026-09-13\n---\n# A\n")
	writeNote(t, vault, "b.md", "---\ntype: project\nid: PRJ-2\nstatus: active\nstart: 2026-01-05\n---\n# B\n")
	writeNote(t, vault, "c.md", "---\ntype: project\nid: PRJ-3\nstatus: paused\n---\n# C\n")
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

const calendarTestSchemaWithDate = "schema_version: 1\n" +
	"type: project\n" +
	"properties:\n" +
	"  status: { type: enum, values: [active, paused] }\n" +
	"  start:  { type: date }\n"

const calendarTestSchemaNoDate = "schema_version: 1\n" +
	"type: project\n" +
	"properties:\n" +
	"  status: { type: enum, values: [active, paused] }\n"

func TestKnowledgeView_LegacyCalendarLayoutInfersDateBinding_D69(t *testing.T) {
	api, ws, colID := buildCalendarTestVault(t, calendarTestSchemaWithDate, map[string]string{
		// The importer's exact shape for an Obsidian `type: calendar` view:
		// legacy layout, a property list, no `parts`, no `date`.
		"projects--project-calendar.yaml": "name: projects--project-calendar\ntype: project\nlayout: calendar\nproperties:\n  - file.name\n  - start\n",
	})
	res, code := getViewResult(t, api, ws, colID, "projects--project-calendar")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	part := res.Parts[0]
	assert.Equal(t, gen.ViewResultPartPartCalendar, part.Part)
	// DIES ON the old code: Source.Date stayed nil, so the SPA's grid had
	// nothing to plot against and drew every month empty.
	require.NotNil(t, part.Source.Date, "a layout-only calendar must carry the date binding the grid plots on")
	assert.Equal(t, "start", *part.Source.Date)
	assert.Len(t, res.Rows, 3, "the record with no start is still a row — the SPA lists it as unscheduled")
}

func TestKnowledgeView_LegacyCalendarLayoutWithoutAnyDatePropertyReportsProblem_D69(t *testing.T) {
	api, ws, colID := buildCalendarTestVault(t, calendarTestSchemaNoDate, map[string]string{
		"cal.yaml": "name: cal\ntype: project\nlayout: calendar\n",
	})
	res, code := getViewResult(t, api, ws, colID, "cal")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	assert.Nil(t, res.Parts[0].Source.Date)
	found := false
	for _, p := range res.Problems {
		if p.Code == gen.ViewPartIneligible {
			found = true
			assert.Contains(t, p.Reason, "no date property")
			require.NotNil(t, p.Fix)
		}
	}
	assert.True(t, found, "an empty grid must be explained in problems, not served as fact")
}

func TestKnowledgeView_DeclaredCalendarPartKeepsItsOwnDate_D69(t *testing.T) {
	api, ws, colID := buildCalendarTestVault(t, calendarTestSchemaWithDate, map[string]string{
		"cal.yaml": "name: cal\ntype: project\nkind: calendar\nparts:\n  - part: calendar\n    date: start\n",
	})
	res, code := getViewResult(t, api, ws, colID, "cal")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Source.Date)
	assert.Equal(t, "start", *res.Parts[0].Source.Date)
}
