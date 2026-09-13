// Tests for POST /api/v1/library/{ws}/knowledge/records/{id}/relation
// (GAP-02 / #700, 2026-09-14 fix round): the web door for
// RelationWriteRequest's add/remove/replace verbs, previously served only by
// the agent's knowledge_edit tool. The handler composes the SAME exported
// knowledge-layer primitives the agent path uses — AddListValue /
// RemoveListValue / SetPropertyList / SetPropertyScalarChecked /
// RemoveProperty NoteEdits, the locked compare-and-swap EditNote, and the
// post-write index refresh — so every test here pins BOTH the wire behaviour
// and FR-045/FR-035's semantics that the agent door already carries.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// relationTestSchema gives `deal` a many-valued relation (`partners`), a
// person property (`contact`) and — for the refusal tests — a plain text
// property and a derived one.
const relationTestDealSchema = "schema_version: 1\n" +
	"type: deal\n" +
	"identity:\n" +
	"  prefix: DE\n" +
	"properties:\n" +
	"  name:      { type: text }\n" +
	"  partners:  { type: relation, to: deal, many: true }\n" +
	"  contact:   { type: person }\n" +
	"  source:    { type: text }\n"

func relationTestDealNote(id, name string) string {
	return "---\n" +
		"type: deal\n" +
		"id: " + id + "\n" +
		"name: " + name + "\n" +
		"---\n# " + name + "\n"
}

func buildRelationTestVault(t *testing.T) (*restAPI, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Deals vault")
	writeNote(t, vault, ".omnipus-vault/records/deal.yaml", relationTestDealSchema)
	writeNote(t, vault, "d1.md", relationTestDealNote("DE-0001", "Primary"))
	writeNote(t, vault, "d2.md", relationTestDealNote("DE-0002", "Acme"))
	writeNote(t, vault, "d3.md", relationTestDealNote("DE-0003", "Bolt"))
	return api, ws
}

func relationPost(t *testing.T, api *restAPI, ws string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return relationPostTo(t, api, ws, "DE-0001", body)
}

func relationPostTo(t *testing.T, api *restAPI, ws, recordID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records/"+recordID+"/relation", body)
}

func dealVersionToken(t *testing.T, api *restAPI, ws string) string {
	t.Helper()
	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/records/DE-0001")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	rec := decodeJSON[gen.VaultRecord](t, w)
	require.NotNil(t, rec.VersionToken)
	return *rec.VersionToken
}

func relationBody(id, token, property, op string, targets ...string) map[string]any {
	return map[string]any{
		"id": id, "version_token": token, "property": property, "op": op, "targets": targets,
	}
}

func storedTargetsOf(t *testing.T, rec gen.VaultRecord, property string) []string {
	t.Helper()
	for _, p := range rec.Properties {
		if p.Property != property {
			continue
		}
		out := []string{}
		for _, v := range p.Values {
			switch {
			case v.Relation != nil:
				out = append(out, v.Relation.Link)
			case v.Person != nil:
				out = append(out, v.Person.Link)
			}
		}
		return out
	}
	return nil
}

func TestKnowledgeRelation_AddToList(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	w := relationPost(t, api, ws, relationBody("DE-0001", token, "partners", "add", "Acme"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.RelationWriteResponse](t, w)
	assert.True(t, resp.Changed)
	assert.Equal(t, []string{"[[Acme]]"}, storedTargetsOf(t, resp.Record, "partners"))
	require.NotNil(t, resp.Record.VersionToken, "response must carry the fresh token")

	// A second, DIFFERENT target leaves the first in place (FR-045's whole
	// point: no read-then-write that discards concurrent edges).
	w2 := relationPost(t, api, ws, relationBody("DE-0001", *resp.Record.VersionToken, "partners", "add", "Bolt"))
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	resp2 := decodeJSON[gen.RelationWriteResponse](t, w2)
	assert.Equal(t, []string{"[[Acme]]", "[[Bolt]]"}, storedTargetsOf(t, resp2.Record, "partners"))

	// Adding an already-present target is a defined NO-OP: changed=false,
	// never an error, never a duplicate.
	w3 := relationPost(t, api, ws, relationBody("DE-0001", *resp2.Record.VersionToken, "partners", "add", "Acme"))
	require.Equal(t, http.StatusOK, w3.Code, w3.Body.String())
	resp3 := decodeJSON[gen.RelationWriteResponse](t, w3)
	assert.False(t, resp3.Changed)
	assert.Equal(t, []string{"[[Acme]]", "[[Bolt]]"}, storedTargetsOf(t, resp3.Record, "partners"))
}

func TestKnowledgeRelation_RemoveFromList(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	w := relationPost(t, api, ws, relationBody("DE-0001", token, "partners", "add", "Acme", "Bolt"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	first := decodeJSON[gen.RelationWriteResponse](t, w)

	w2 := relationPost(t, api, ws, relationBody("DE-0001", *first.Record.VersionToken, "partners", "remove", "Acme"))
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	resp2 := decodeJSON[gen.RelationWriteResponse](t, w2)
	assert.True(t, resp2.Changed)
	assert.Equal(t, []string{"[[Bolt]]"}, storedTargetsOf(t, resp2.Record, "partners"))

	// Removing an absent target is a no-op, not an error.
	w3 := relationPost(t, api, ws, relationBody("DE-0001", *resp2.Record.VersionToken, "partners", "remove", "Acme"))
	require.Equal(t, http.StatusOK, w3.Code, w3.Body.String())
	resp3 := decodeJSON[gen.RelationWriteResponse](t, w3)
	assert.False(t, resp3.Changed)
}

func TestKnowledgeRelation_ReplaceAndClear(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	w := relationPost(t, api, ws, relationBody("DE-0001", token, "partners", "add", "Acme"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	first := decodeJSON[gen.RelationWriteResponse](t, w)

	w2 := relationPost(t, api, ws, relationBody("DE-0001", *first.Record.VersionToken, "partners", "replace", "Bolt"))
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())
	resp2 := decodeJSON[gen.RelationWriteResponse](t, w2)
	assert.True(t, resp2.Changed)
	assert.Equal(t, []string{"[[Bolt]]"}, storedTargetsOf(t, resp2.Record, "partners"))

	// replace with an EMPTY list clears the property — the only op that
	// accepts one.
	w3 := relationPost(t, api, ws, relationBody("DE-0001", *resp2.Record.VersionToken, "partners", "replace"))
	require.Equal(t, http.StatusOK, w3.Code, w3.Body.String())
	resp3 := decodeJSON[gen.RelationWriteResponse](t, w3)
	assert.True(t, resp3.Changed)
	assert.Empty(t, storedTargetsOf(t, resp3.Record, "partners"))
}

func TestKnowledgeRelation_PersonPropertyUsesSameVerbs(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	w := relationPost(t, api, ws, relationBody("DE-0001", token, "contact", "add", "Dana"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.RelationWriteResponse](t, w)
	assert.True(t, resp.Changed)
	assert.Equal(t, []string{"[[Dana]]"}, storedTargetsOf(t, resp.Record, "contact"))
}

func TestKnowledgeRelation_ScalarSecondTargetRefused_FR035(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	w := relationPost(t, api, ws, relationBody("DE-0001", token, "contact", "add", "Dana"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	first := decodeJSON[gen.RelationWriteResponse](t, w)

	w2 := relationPost(t, api, ws, relationBody("DE-0001", *first.Record.VersionToken, "contact", "add", "Lee"))
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assert.Contains(t, w2.Body.String(), "replace")

	// The refusal left the slot untouched.
	assert.Equal(t, []string{"[[Dana]]"}, storedTargetsOf(t, first.Record, "contact"))
}

func TestKnowledgeRelation_Refusals(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	cases := []struct {
		name string
		/** path record id — defaults to DE-0001 via the empty string. */
		path string
		body map[string]any
		code int
		want string
	}{
		{
			name: "a text property is not a relation",
			body: relationBody("DE-0001", token, "name", "add", "X"),
			code: http.StatusBadRequest,
			want: "not a relation",
		},
		{
			name: "an unknown property names the declared set",
			body: relationBody("DE-0001", token, "banana", "add", "X"),
			code: http.StatusBadRequest,
			want: "declares no property",
		},
		{
			name: "empty targets accepted only with replace",
			body: relationBody("DE-0001", token, "partners", "add"),
			code: http.StatusBadRequest,
			want: "replace",
		},
		{
			name: "stale version token is a typed conflict",
			body: relationBody("DE-0001", "v1:00000000000000000000000000000000", "partners", "add", "Acme"),
			code: http.StatusConflict,
			want: "knowledge_version_conflict",
		},
		{
			name: "unknown record is 404",
			path: "DE-9999",
			body: relationBody("DE-9999", token, "partners", "add", "Acme"),
			code: http.StatusNotFound,
			want: "record not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if tc.path != "" {
				w = relationPostTo(t, api, ws, tc.path, tc.body)
			} else {
				w = relationPost(t, api, ws, tc.body)
			}
			assert.Equal(t, tc.code, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want)
		})
	}
}

// The body's id and the path's id must agree — a disagreement is a client
// bug that must never write one record while claiming another.
func TestKnowledgeRelation_PathAndBodyIDDisagree(t *testing.T) {
	api, ws := buildRelationTestVault(t)
	token := dealVersionToken(t, api, ws)

	w := relationPost(t, api, ws, relationBody("DE-0002", token, "partners", "add", "Acme"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "DE-0002")
}
