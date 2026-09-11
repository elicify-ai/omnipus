// Omnipus — the create/update split on the record write door.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS ARE FOR
//
// The write door used to decide what a caller meant by looking at which
// optional fields were set: `id` present meant update, `id` absent meant
// create. Under that shape THE DEFECT WAS UNOBSERVABLE — an update that lost
// its `id` was not a malformed request, it was a valid create, so there was
// no status code to assert and no refusal to audit. It wrote a duplicate
// note, discarded the version token, and returned 201.
//
// Every test below asserts something that could not be asserted before the
// split, because before the split the server had no way to tell the two
// operations apart.
// ---------------------------------------------------------------------------

// recordsPath is the write door under test.
func recordsPath(ws string) string { return "/api/v1/library/" + ws + "/knowledge/records" }

// TestRecordWrite_ModeIsRequired is the headline case: the exact body an
// update-that-lost-its-id produces.
//
// It carries a real `version_token` and real properties and names no `id` —
// which under the old contract was a perfectly valid create. It must now be
// refused, and refused as a BAD REQUEST rather than quietly succeeding as
// something the caller did not ask for.
func TestRecordWrite_ModeIsRequired(t *testing.T) {
	api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)
	token := widgetVersionToken(t, api, ws)

	w := knowledgePost(t, api, recordsPath(ws), map[string]any{
		"type":          "widget",
		"version_token": token,
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Lost Its ID"}}},
		},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "mode is required",
		"the refusal must name the field that is missing, not just say the body is invalid")
	assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalInvalidRequest,
		"a refused write that records nothing leaves the audit log claiming nothing was attempted")
}

// TestRecordWrite_UnknownModeIsRefused pins the closed set. A caller that
// invents a third operation is told so, rather than falling through to
// whichever branch a default arm happens to name.
func TestRecordWrite_UnknownModeIsRefused(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)

	w := knowledgePost(t, api, recordsPath(ws), map[string]any{
		"mode": "upsert",
		"type": "widget",
		"path": "x.md",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}},
		},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	// Asserted on the unquoted fragments: the body is JSON, so the message's
	// own double quotes around create/update arrive backslash-escaped and a
	// literal `must be "create" or "update"` would never match however
	// correct the server was.
	body := w.Body.String()
	assert.Contains(t, body, "mode is required")
	assert.Contains(t, body, "create")
	assert.Contains(t, body, "update")
}

// TestRecordWrite_VersionTokenOnCreateIsRefused is the field-crossing case
// that matters most.
//
// A caller sending a version token believes it is protected against a
// concurrent write. On a create there is no prior version, so the token can
// only ever be ignored — and a server that ignores it leaves the caller
// believing a guarantee it is not getting. The strict decode makes that a
// named 400 instead.
//
// It runs regardless of gateway.validate_inbound (default false), which is
// the whole point of doing this with DisallowUnknownFields rather than only
// in the JSON Schema layer.
func TestRecordWrite_VersionTokenOnCreateIsRefused(t *testing.T) {
	api, ws, _, auditDir := buildRecordTestVaultWithAuditor(t)
	require.False(t, api.agentLoop.GetConfig().Gateway.ValidateInbound,
		"this test's value is that it holds with inbound schema validation OFF")

	w := knowledgePost(t, api, recordsPath(ws), map[string]any{
		"mode":          "create",
		"type":          "widget",
		"path":          "brand-new.md",
		"version_token": "v1:9f2a7c4081e3b5d6a0c1f2e3b4d5a6c7",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}},
		},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "version_token",
		"the refusal must name the offending field so the caller knows what to drop")
	assert.Contains(t, auditReasonsFor(t, auditDir), recordRefusalInvalidRequest)
}

// TestRecordWrite_PathOnUpdateIsRefused is the mirror case. A `path` sent
// with an `id` reads as "move this note", which this door has never done
// (EMB-089) and would have silently ignored.
func TestRecordWrite_PathOnUpdateIsRefused(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)
	token := widgetVersionToken(t, api, ws)

	w := knowledgePost(t, api, recordsPath(ws), map[string]any{
		"mode":          "update",
		"type":          "widget",
		"id":            "WD-0001",
		"version_token": token,
		"path":          "somewhere/else.md",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}},
		},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "path",
		"a caller that thinks it is moving the note must be told it is not")
}

// TestRecordWrite_UpdateWithoutIdIsRefusedNotTreatedAsCreate is the defect
// stated as directly as it can be.
//
// The body says `mode: update` and omits `id`. The ONLY correct outcome is a
// refusal naming `id`. A 201 here would mean the door had created a record —
// exactly the silent reinterpretation the split exists to make impossible —
// so the assertion checks the status is not 201 explicitly rather than only
// checking it is 400, to keep the failure message truthful if the behaviour
// ever regresses in that particular direction.
func TestRecordWrite_UpdateWithoutIdIsRefusedNotTreatedAsCreate(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)
	token := widgetVersionToken(t, api, ws)

	w := knowledgePost(t, api, recordsPath(ws), map[string]any{
		"mode":          "update",
		"type":          "widget",
		"version_token": token,
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "Should Not Create"}}},
		},
	})
	require.NotEqual(t, http.StatusCreated, w.Code,
		"an update missing its id must never be reinterpreted as a create")
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "id is required")
}

// TestRecordWrite_BothModesStillWorkEndToEnd is the positive control.
//
// Without it every assertion above could pass on a door that refused every
// write. It also pins the two DIFFERENT success codes, which is the caller-
// visible half of the split: 201 says an identifier was minted that the
// caller could not have predicted, 200 says an existing record moved forward.
func TestRecordWrite_BothModesStillWorkEndToEnd(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)

	t.Run("create answers 201 with a server-minted id", func(t *testing.T) {
		w := knowledgePost(t, api, recordsPath(ws), map[string]any{
			"mode": "create",
			"type": "widget",
			"path": "split-created.md",
			"properties": []map[string]any{
				{"property": "name", "values": []map[string]any{{"type": "text", "text": "Created By Mode"}}},
			},
		})
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		rec := decodeJSON[gen.VaultRecord](t, w)
		assert.NotEmpty(t, rec.Id, "a create must return the identifier it minted")
		assert.NotEmpty(t, rec.VersionToken, "a create must return a first version token")
	})

	t.Run("update answers 200 and advances the token", func(t *testing.T) {
		token := widgetVersionToken(t, api, ws)
		w := knowledgePost(t, api, recordsPath(ws), map[string]any{
			"mode":          "update",
			"type":          "widget",
			"id":            "WD-0001",
			"version_token": token,
			"properties": []map[string]any{
				{"property": "name", "values": []map[string]any{{"type": "text", "text": "Updated By Mode"}}},
			},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		rec := decodeJSON[gen.VaultRecord](t, w)
		assert.Equal(t, "WD-0001", rec.Id)
		assert.NotEqual(t, token, rec.VersionToken,
			"the write changed the file, so the token it returns must not be the one that was sent")
	})
}
