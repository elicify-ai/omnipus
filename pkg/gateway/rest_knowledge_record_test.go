// Tests for ADR-083 Step 5 (CW-4/CW-7): the record-schema, get-record and
// write-record REST endpoints.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// --- fixture -----------------------------------------------------------

const recordTestWidgetSchema = "schema_version: 1\n" +
	"type: widget\n" +
	"identity:\n" +
	"  prefix: WD\n" +
	"properties:\n" +
	"  name:    { type: text }\n" +
	"  status:  { type: enum, values: [open, closed] }\n" +
	"  owner:   { type: relation, to: widget }\n"

func recordTestWidgetNote(id, name, status string) string {
	return "---\n" +
		"type: widget\n" +
		"id: " + id + "\n" +
		"name: " + name + "\n" +
		"status: " + status + "\n" +
		"---\n# " + name + "\n"
}

// buildRecordTestVault seeds a workspace vault with the widget schema and one
// widget note, and returns the api, workspace id and vault directory.
func buildRecordTestVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Widgets vault")
	writeNote(t, vault, ".omnipus-vault/records/widget.yaml", recordTestWidgetSchema)
	writeNote(t, vault, "w1.md", recordTestWidgetNote("WD-0001", "Sprocket", "open"))
	return api, ws, vault
}

// buildRecordTestVaultWithAuditor is buildRecordTestVault plus a real
// audit.Logger wired to api.auditor, so a test can assert on the audit
// trail's refusal reasons.
func buildRecordTestVaultWithAuditor(t *testing.T) (*restAPI, string, string, string) {
	t.Helper()
	api, ws, vault := buildRecordTestVault(t)
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })
	api.auditor = logger
	return api, ws, vault, auditDir
}

// recordTestUsername is the authenticated principal every ordinary write test
// runs as. The record write door refuses an UNATTRIBUTABLE caller outright
// (ADR-083 §4.2b / founder ruling N4: "a request that reaches the handler with
// neither an authenticated user nor bypass active is still rejected"), so a
// test that posts with no identity at all is exercising the refusal, not the
// write. Tests that want the refusal ask for it explicitly — see
// TestKnowledgeRecordWrite_AuditActor.
const recordTestUsername = "daniela"

// knowledgePost posts as an AUTHENTICATED user, which is the ordinary case.
func knowledgePost(t *testing.T, api *restAPI, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return knowledgePostWithUser(t, api, target, body, &config.UserConfig{Username: recordTestUsername})
}

// knowledgePostWithUser posts as `user`, or as nobody at all when user is nil.
func knowledgePostWithUser(t *testing.T, api *restAPI, target string, body any, user *config.UserConfig) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if user != nil {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, user))
	}
	api.HandleLibraryTree(w, r)
	return w
}

// recordAuditEntries returns this door's audit rows, newest last.
func recordAuditEntries(t *testing.T, auditDir string) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, e := range readAuditEventsForTest(t, auditDir) {
		if e["event"] == recordWriteAuditEvent {
			out = append(out, e)
		}
	}
	return out
}

// recordAuditActors returns the `actor` detail of every entry this door wrote.
func recordAuditActors(t *testing.T, auditDir string) []string {
	t.Helper()
	out := []string{}
	for _, e := range recordAuditEntries(t, auditDir) {
		details, _ := e["details"].(map[string]any)
		if details == nil {
			continue
		}
		actor, ok := details["actor"].(string)
		if !ok {
			t.Fatalf("audit entry has no string actor: %#v", e)
		}
		out = append(out, actor)
	}
	return out
}

func widgetVersionToken(t *testing.T, api *restAPI, ws string) string {
	t.Helper()
	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/WD-0001")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	rec := decodeJSON[gen.VaultRecord](t, w)
	require.NotNil(t, rec.VersionToken)
	return *rec.VersionToken
}

func auditReasonsFor(t *testing.T, auditDir string) []string {
	t.Helper()
	out := []string{}
	for _, e := range recordAuditEntries(t, auditDir) {
		details, _ := e["details"].(map[string]any)
		if details == nil {
			continue
		}
		if reason, ok := details["reason"].(string); ok {
			out = append(out, reason)
		}
	}
	return out
}

// --- GET record-schema ---------------------------------------------------

func TestKnowledgeRecordSchema_ListsDeclaredTypes(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/record-schema")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.RecordSchema](t, w)

	require.Len(t, out.Types, 1)
	require.Empty(t, out.Problems)
	rt := out.Types[0]
	assert.Equal(t, "widget", rt.Type)
	require.NotNil(t, rt.IdentityPrefix)
	assert.Equal(t, "WD", *rt.IdentityPrefix)
	require.Len(t, rt.Properties, 3)

	byName := map[string]gen.PropertyDef{}
	for _, p := range rt.Properties {
		byName[p.Name] = p
	}
	assert.Equal(t, gen.PropertyDefTypeText, byName["name"].Type)
	assert.Equal(t, gen.PropertyDefTypeEnum, byName["status"].Type)
	assert.Equal(t, gen.PropertyDefTypeRelation, byName["owner"].Type)
}

func TestKnowledgeRecordSchema_NoRecordTypesIsEmptyNotError(t *testing.T) {
	// ADR-068 D0: an empty types array is the correct answer, never a
	// broken installation.
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Empty vault")
	writeNote(t, vault, "note.md", "# just a note\n")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/record-schema")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.RecordSchema](t, w)
	assert.Empty(t, out.Types)
	assert.Empty(t, out.Problems)
}

// --- GET records/{id} ------------------------------------------------------

func TestKnowledgeRecordGet_ReturnsDeclaredProperties(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/WD-0001")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	rec := decodeJSON[gen.VaultRecord](t, w)

	assert.Equal(t, "WD-0001", rec.Id)
	assert.Equal(t, "widget", rec.Type)
	assert.Equal(t, "w1.md", rec.Path)
	require.NotNil(t, rec.VersionToken)
	assert.NotEmpty(t, *rec.VersionToken)

	byName := map[string]gen.RecordPropertyValue{}
	for _, p := range rec.Properties {
		byName[p.Property] = p
	}
	require.Len(t, byName["name"].Values, 1)
	require.NotNil(t, byName["name"].Values[0].Text)
	assert.Equal(t, "Sprocket", *byName["name"].Values[0].Text)
	require.Len(t, byName["status"].Values, 1)
	require.NotNil(t, byName["status"].Values[0].Enum)
	assert.Equal(t, "open", *byName["status"].Values[0].Enum)
	// owner was never set on the fixture note — D3.2 absence.
	assert.Empty(t, byName["owner"].Values)
}

func TestKnowledgeRecordGet_UnknownIDIs404(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)
	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/WD-9999")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- POST records: the write door ------------------------------------------

// TestKnowledgeRecordWrite_SuccessfulUpdate is the round-trip proof: write a
// new value under the CURRENT version token, then read the record back and
// confirm the new value persisted and the version token changed.
func TestKnowledgeRecordWrite_SuccessfulUpdate(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)
	oldToken := widgetVersionToken(t, api, ws)

	body := map[string]any{
		"type":          "widget",
		"id":            "WD-0001",
		"version_token": oldToken,
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Widget Renamed"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.VaultRecord](t, w)
	require.NotNil(t, out.VersionToken)
	assert.NotEqual(t, oldToken, *out.VersionToken, "version token must change after a real write")

	// Persisted on disk, not just in the response.
	onDisk, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Contains(t, string(onDisk), "name: Widget Renamed")

	// Read-back agrees.
	getW := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/WD-0001")
	require.Equal(t, http.StatusOK, getW.Code)
	rec := decodeJSON[gen.VaultRecord](t, getW)
	for _, p := range rec.Properties {
		if p.Property == "name" {
			require.Len(t, p.Values, 1)
			assert.Equal(t, "Widget Renamed", *p.Values[0].Text)
		}
	}
}

// TestKnowledgeRecordWrite_StaleTokenIs409 proves the compare half of the
// compare-and-swap: a version token that no longer matches the file is
// refused with 409 and the typed KnowledgeConflictError body naming both
// versions, and the file is left untouched.
func TestKnowledgeRecordWrite_StaleTokenIs409(t *testing.T) {
	api, ws, vault, auditDir := buildRecordTestVaultWithAuditor(t)
	before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)

	// A WELL-FORMED but stale token: "v1:" plus exactly 32 hex, the shape this
	// server actually mints. The fixture used to send "v1:" + 16 hex, which
	// this door now refuses as MALFORMED (400) rather than stale (409) — see
	// TestKnowledgeRecordWrite_QuotedTokenIs400NotConflict for why that
	// distinction exists. A token the server could not have issued cannot be a
	// stale one it issued, so a 16-hex fixture was testing the conflict path
	// with an input that can never reach it in production.
	const staleToken = "v1:0000000000000000000000000000dead"
	body := map[string]any{
		"type":          "widget",
		"id":            "WD-0001",
		"version_token": staleToken,
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Should Not Land"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var conflict gen.KnowledgeConflictError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &conflict))
	assert.Equal(t, gen.KnowledgeVersionConflict, conflict.Code)
	require.NotNil(t, conflict.ExpectedVersion)
	assert.Equal(t, staleToken, *conflict.ExpectedVersion)
	require.NotNil(t, conflict.ActualVersion)
	assert.NotEmpty(t, *conflict.ActualVersion)

	after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused write must leave the file byte-identical")

	assert.Contains(t, auditReasonsFor(t, auditDir), "version_conflict")
}

// TestKnowledgeRecordWrite_AbsentTokenIs400 proves the absent/empty case is a
// 400, never a silent bypass of the compare-and-swap.
func TestKnowledgeRecordWrite_AbsentTokenIs400(t *testing.T) {
	api, ws, vault, auditDir := buildRecordTestVaultWithAuditor(t)
	before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)

	body := map[string]any{
		"type": "widget",
		"id":   "WD-0001",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Should Not Land"}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after)

	assert.Contains(t, auditReasonsFor(t, auditDir), "expect_version_missing")
}

// TestKnowledgeRecordWrite_RelationPropertyRefused proves FR-045 end to end,
// over a REAL schema file declaring a relation property (unlike a formula
// property, a relation is not refused at schema LOAD time, so this is the
// one of the two write-time guards reachable through a real vault).
func TestKnowledgeRecordWrite_RelationPropertyRefused(t *testing.T) {
	api, ws, vault, auditDir := buildRecordTestVaultWithAuditor(t)
	before, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	token := widgetVersionToken(t, api, ws)

	body := map[string]any{
		"type":          "widget",
		"id":            "WD-0001",
		"version_token": token,
		"properties": []map[string]any{
			{"property": "owner", "values": []map[string]any{{"type": "relation", "relation": map[string]any{"link": "Other Widget", "resolved": false}}}},
		},
	}
	w := knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	after, err := os.ReadFile(filepath.Join(vault, "w1.md"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused write must leave the file byte-identical")

	assert.Contains(t, auditReasonsFor(t, auditDir), "relation_property")
}

// TestKnowledgeRecordWrite_DerivedPropertyRefused is a white-box unit test of
// buildRecordPropertyEdits, not an HTTP round trip: a schema loaded through
// records.LoadSchemas can NEVER carry a Formula-bearing property (a schema
// file declaring `formula:` on a property is refused at load, before this
// endpoint ever sees it — see records.Property.Wire's own note), so there is
// no real vault fixture that reaches this refusal end to end. The guard is
// still real defence-in-depth server-side enforcement, and this test proves
// it directly against the exact function the write handlers call.
func TestKnowledgeRecordWrite_DerivedPropertyRefused(t *testing.T) {
	sc := &records.Schema{
		SchemaVersion: 1,
		Type:          "widget",
		Properties: map[string]*records.Property{
			"score": {Name: "score", Type: records.TypeDecimal, RecordType: "widget", Formula: "1 + 1"},
		},
		PropertyOrder: []string{"score"},
	}
	props := []gen.RecordPropertyValue{
		{Property: "score", Values: []gen.RecordValue{{Type: gen.RecordValueTypeDecimal, Decimal: strPtr("2")}}},
	}

	// nil `current`: a create has no stored record, and this refusal does not
	// depend on one — the schema's own Formula declaration is the whole basis.
	edits, refusal := buildRecordPropertyEdits(sc, props, nil)
	require.Nil(t, edits)
	require.NotNil(t, refusal)
	assert.Equal(t, http.StatusBadRequest, refusal.status)
	assert.Equal(t, recordRefusalDerivedProperty, refusal.reason)
}

// TestKnowledgeRecordWrite_ConcurrentUpdatesOnlyOneWins is the mutation-proof
// for "compare and write happen under ONE lock acquisition": two writers read
// the SAME version token and race to update the same record with different
// values. If the compare-and-swap were not atomic (a check followed by an
// unheld write), both could pass the compare and both would report success —
// a silent lost update. With it, exactly one succeeds and the other is
// refused with 409, and the surviving value on disk is the winner's.
func TestKnowledgeRecordWrite_ConcurrentUpdatesOnlyOneWins(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)
	token := widgetVersionToken(t, api, ws)

	race := func(name string) *httptest.ResponseRecorder {
		body := map[string]any{
			"type":          "widget",
			"id":            "WD-0001",
			"version_token": token,
			"properties": []map[string]any{
				{"property": "name", "values": []map[string]any{{"type": "text", "text": name}}},
			},
		}
		return knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", body)
	}

	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	names := []string{"Writer A", "Writer B"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = race(names[i])
		}(i)
	}
	wg.Wait()

	codes := []int{results[0].Code, results[1].Code}
	oks, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			oks++
		case http.StatusConflict:
			conflicts++
		}
	}
	assert.Equal(t, 1, oks, "exactly one concurrent writer under the same version token must succeed; codes=%v", codes)
	assert.Equal(t, 1, conflicts, "exactly one concurrent writer under the same version token must be refused as a conflict; codes=%v", codes)
}

// TestKnowledgeDispatch_OnlyRecordsTakesASecondSegment pins the dispatcher rule
// that "records/{id}" is the sole two-segment knowledge sub-path.
//
// The first version of the Step 5 dispatch widened the length check to "any two
// segments", which is the shape that looks harmless and is not: POST
// /knowledge/find/junk stopped being a 404 and started running a real vault
// search with the trailing segment silently dropped. A caller with a typo in
// the path would have been served a 200 for a URL that does not exist.
func TestKnowledgeDispatch_OnlyRecordsTakesASecondSegment(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)
	base := "/api/v1/library/" + ws + "/knowledge/"

	// The one path that legitimately carries an id.
	got := knowledgeGet(t, api, base+"records/WD-0001")
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())

	for _, sub := range []string{"graph", "outline", "view", "base-views", "record-schema"} {
		w := knowledgeGet(t, api, base+sub+"/junk")
		assert.Equal(t, http.StatusNotFound, w.Code, "GET %s/junk must 404, not ignore the segment", sub)
	}

	// find is POST-only, so its regression only shows through a POST.
	w := knowledgePost(t, api, base+"find/junk", map[string]any{"query": "anything"})
	assert.Equal(t, http.StatusNotFound, w.Code, "POST find/junk must 404, not run a search")
}
