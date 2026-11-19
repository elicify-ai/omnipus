// packages_b1_fields_red_test.go — CHECK F06.
//
// Dataset D1 rows 23, 24 and 29 (and the figure/role half of row 29) require
// element_not_creatable to name the offending field. The create rule already
// refuses these records, but createRefusal leaves Field empty and import
// forwards that empty field. REST create's wire stays the pre-field body:
// HTTP 400, the same sentence, no field and no code.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func workerRecord() map[string]any {
	return map[string]any{
		"id": specSourceID, "name": "Helper", "type": "worker",
		"model": map[string]any{"primary": "m-1", "provider": "openrouter"},
	}
}

func threeFallbackRecord() map[string]any {
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	rec["fallback_models"] = []any{
		map[string]any{"model": "a", "provider": "p"},
		map[string]any{"model": "b", "provider": "p"},
		map[string]any{"model": "c", "provider": "p"},
	}
	return rec
}

func visualRecord(key, value string) map[string]any {
	rec := mainRecord(specSourceID, "Writer", "Drafts")
	rec[key] = value
	return rec
}

func uploadCreateFieldCases(t *testing.T) {
	t.Helper()
	cases := []struct {
		name   string
		rec    map[string]any
		field  string
		reason string
	}{
		{"D1-23 blank worker description", workerRecord(), "description", "description is required for worker agents (Subagent, subagent_3p)"},
		{"D1-24 three fallback models", threeFallbackRecord(), "fallback_models", "fallback_models exceeds maxItems: 2"},
		{"D1-29 invalid colour", visualRecord("color", "not-a-colour"), "color", "color must be one of the ten identity colours"},
		{"D1-29 invalid figure", visualRecord("figure", "NotAFigure"), "figure", "figure must be Robot, Man, Woman, Omnipus, or Monogram"},
		{"D1-29 invalid role", visualRecord("role", "not-a-role"), "role", "role must be one of the curated role slugs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := packageAPI(t)
			agentsBefore := agentEntityNames(t, api.homePath)
			slotsBefore := packageStagingIDs(t, api.homePath)
			pkg := buildAgentPackage(t, specSourceID, tc.rec["name"].(string), tc.rec, []byte("Answer in rhyme.\n"), nil, 0)
			got := callPackageImport(t, api, pkg)
			er := requirePackageStatus(t, got, http.StatusUnprocessableEntity, omnpkg.CodeElementNotCreatable)
			require.Equal(t, tc.field, packageErrField(er), "D1 requires the offending field; body=%s", got.Body)
			require.Contains(t, er.Error, tc.reason, "body=%s", got.Body)
			require.Equal(t, agentsBefore, agentEntityNames(t, api.homePath), "no partial agent")
			require.Equal(t, slotsBefore, packageStagingIDs(t, api.homePath), "refused upload leaves no staging slot")
		})
	}
}

// TestCreateFieldRefusals_RESTWireUnchanged pins the REST create wire for the
// same five rules. Import may name the field; REST must keep today's body
// (400, the sentence, no field, no code). This is not a request to change REST.
func TestCreateFieldRefusals_RESTWireUnchanged(t *testing.T) {
	cases := []struct {
		name string
		body string
		msg  string
	}{
		{"blank worker description", `{"name":"Helper","type":"Subagent","soul":"soul text"}`, "description is required for worker agents (Subagent, subagent_3p)"},
		{"three fallback models", `{"name":"Fbs","type":"Main","soul":"soul text","fallback_models":[{"model":"a","provider":"p"},{"model":"b","provider":"p"},{"model":"c","provider":"p"}]}`, "fallback_models exceeds maxItems: 2"},
		{"invalid colour", `{"name":"Colored","type":"Main","soul":"soul text","color":"not-a-colour"}`, "color must be one of the ten identity colours"},
		{"invalid figure", `{"name":"Figured","type":"Main","soul":"soul text","figure":"NotAFigure"}`, "figure must be Robot, Man, Woman, Omnipus, or Monogram"},
		{"invalid role", `{"name":"Roled","type":"Main","soul":"soul text","role":"not-a-role"}`, "role must be one of the curated role slugs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code, "REST status is unchanged; body=%s", w.Body.String())
			var er gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &er), "body=%s", w.Body.String())
			require.Equal(t, tc.msg, er.Error, "REST sentence is unchanged")
			require.Nil(t, er.Field, "REST create does not name a field for this rule; body=%s", w.Body.String())
			require.Nil(t, er.Code, "REST create does not add a code for this rule; body=%s", w.Body.String())
			require.NotContains(t, w.Body.String(), `"field"`, "the REST JSON has no field member")
		})
	}
}
