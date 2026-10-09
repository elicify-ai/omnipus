package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The casing-fix dispatch requires every decoder-recognised explicit identity
// null to reject the whole request, without changing the record or its revision.
// Use raw requests: the revision fixture re-marshals JSON and would erase key
// escapes and duplicates, which are part of this boundary's input.
func TestAgentIdentity_UpdateRejectsCaseFoldedNull(t *testing.T) {
	cases := []struct {
		name, fields, errorText string
	}{
		{"uppercase figure", `"FIGURE":null,"role":"writer","color":"#22D3EE"`, "figure must be Robot, Man, Woman, or Omnipus"},
		{"uppercase role", `"figure":"Woman","ROLE":null,"color":"#22D3EE"`, "role must be one of the curated role slugs"},
		{"uppercase color", `"figure":"Woman","role":"writer","COLOR":null`, "color must be one of the ten identity colours"},
		{"mixed case figure", `"FiGuRe":null,"role":"writer","color":"#22D3EE"`, "figure must be Robot, Man, Woman, or Omnipus"},
		{"mixed case role", `"figure":"Woman","RoLe":null,"color":"#22D3EE"`, "role must be one of the curated role slugs"},
		{"mixed case color", `"figure":"Woman","role":"writer","CoLoR":null`, "color must be one of the ten identity colours"},
		{"escaped figure", `"FIGURE":null,"role":"writer","color":"#22D3EE"`, "figure must be Robot, Man, Woman, or Omnipus"},
		{"escaped role", `"figure":"Woman","ROLE":null,"color":"#22D3EE"`, "role must be one of the curated role slugs"},
		{"escaped color", `"figure":"Woman","role":"writer","COLOR":null`, "color must be one of the ten identity colours"},
		{"duplicate figure null first", `"figure":null,"figure":"Woman","role":"writer","color":"#22D3EE"`, "figure must be Robot, Man, Woman, or Omnipus"},
		{"duplicate role null first", `"figure":"Woman","role":null,"role":"writer","color":"#22D3EE"`, "role must be one of the curated role slugs"},
		{"duplicate color null first", `"figure":"Woman","role":"writer","color":null,"color":"#22D3EE"`, "color must be one of the ten identity colours"},
		{"case duplicate figure null first", `"FIGURE":null,"figure":"Woman","role":"writer","color":"#22D3EE"`, "figure must be Robot, Man, Woman, or Omnipus"},
		{"case duplicate role null first", `"figure":"Woman","ROLE":null,"role":"writer","color":"#22D3EE"`, "role must be one of the curated role slugs"},
		{"case duplicate color null first", `"figure":"Woman","role":"writer","COLOR":null,"color":"#22D3EE"`, "color must be one of the ten identity colours"},
		{"duplicate figure null last", `"figure":"Woman","figure":null,"role":"writer","color":"#22D3EE"`, "figure must be Robot, Man, Woman, or Omnipus"},
		{"duplicate role null last", `"figure":"Woman","role":"writer","ROLE":null,"color":"#22D3EE"`, "role must be one of the curated role slugs"},
		{"duplicate color null last", `"figure":"Woman","role":"writer","color":"#22D3EE","COLOR":null`, "color must be one of the ten identity colours"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			created := postAgent(t, api, `{"name":"Boundary Editable","type":"Main","soul":"editable-soul","figure":"Man","role":"developer","color":"#3B82F6","icon":"magnifying-glass"}`)
			require.Equal(t, http.StatusCreated, created.Code, "body: %s", created.Body.String())
			id, ok := decodeObject(t, created.Body.Bytes())["id"].(string)
			require.True(t, ok, "created identity id must be a string")
			storedBefore := savedAgent(t, api, id)
			store := agentstore.New(api.homePath)
			stateBefore, err := store.ReadState(id)
			require.NoError(t, err)
			require.False(t, api.agentLoop.GetConfig().Gateway.ValidateInbound, "exercise the default non-strict path")

			body := fmt.Sprintf(`{"revision":%q,%s,"description":"must not save"}`, stateBefore.Revision, tc.fields)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.HandleAgents(rec, req)
			stateAfter, err := store.ReadState(id)
			require.NoError(t, err)
			t.Logf("CASE=%s status=%d revision_before=%s revision_after=%s", tc.name, rec.Code, stateBefore.Revision, stateAfter.Revision)

			assert.Equal(t, http.StatusBadRequest, rec.Code, "explicit identity null must reject the entire PUT; body: %s", rec.Body.String())
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.Equal(t, map[string]any{"error": tc.errorText}, decodeObject(t, rec.Body.Bytes()), "preserve the exact field-specific error envelope")
			assert.Equal(t, storedBefore, savedAgent(t, api, id), "rejection must not persist valid sibling changes or a display timestamp")
			assert.Equal(t, stateBefore.Revision, stateAfter.Revision, "rejection must not bump the configuration revision")
			assert.Equal(t, stateBefore, stateAfter, "rejection is zero-write across the stored configuration state")
		})
	}
}
