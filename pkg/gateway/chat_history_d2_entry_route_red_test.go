package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Actual ServeMux -> registered auth wrapper -> existing sessions dispatcher.
// No direct detail helper, injected user context or fake authorization result.
func d2EntryRequest(api *restAPI, sid, position, authorization string, ctx context.Context) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/sessions/", api.withAuth(api.HandleSessions))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages/"+position, nil)
	if ctx != nil {
		r = r.WithContext(ctx)
	}
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func d2EntryRefusal(t *testing.T, w *httptest.ResponseRecorder, status int, message string, canaries ...string) {
	t.Helper()
	require.Equal(t, status, w.Code, "B76 access/refusal precedence: %s", w.Body.String())
	var body generated.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	if message != "" {
		require.Equal(t, message, body.Error)
	} else {
		require.NotEmpty(t, body.Error)
	}
	for _, canary := range canaries {
		require.NotContains(t, w.Body.String(), canary, "refusals never expose source identity/content/physical paths")
	}
}
func d2EntryFixture(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "d2-route-test-token")
	sid := createTestSession(t, api)
	addr, err := api.agentLoop.GetSessionStore().AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "d2-full-source", Role: "assistant", AgentID: "mia", Content: "FULL_PUBLIC_CANARY", ViewMembership: session.ViewMembershipChat})
	require.NoError(t, err)
	return api, sid, pagingPositionOracle(sid, addr)
}
func TestHistoryEntryRead_AccessAndErrors(t *testing.T) {
	t.Run("legacy_bearer_full_public_projection", func(t *testing.T) {
		api, sid, position := d2EntryFixture(t)
		w := d2EntryRequest(api, sid, position, "Bearer d2-route-test-token", nil)
		require.Equal(t, http.StatusOK, w.Code, "BLOCKED: position-addressed full-entry route not implemented — required by D2 A2.2/T65")
		var m generated.Message
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
		require.Equal(t, "d2-full-source", m.Id)
		require.NotNil(t, m.Content)
		require.Equal(t, "FULL_PUBLIC_CANARY", *m.Content)
		require.NotNil(t, m.HistoryPosition)
		require.Equal(t, position, *m.HistoryPosition)
		require.Nil(t, m.ContentOriginalSizeBytes)
		// Message.yaml exposes the effective chat/both membership. This is
		// a normal chat append, never a private model-only archive record.
		require.NotNil(t, m.ViewMembership)
		require.Equal(t, generated.MessageViewMembershipChat, *m.ViewMembership)
		for _, private := range []string{"model_message", "source", "byte_offset", "partition_key"} {
			require.NotContains(t, w.Body.String(), `"`+private+`"`)
		}
	})
	for _, credential := range []string{"", "Bearer invalid", "Bearer unrecognized-agent-scope"} {
		t.Run("unauthorized_"+credential, func(t *testing.T) {
			api, _, _ := d2EntryFixture(t)
			d2EntryRefusal(t, d2EntryRequest(api, "missing-session", "malformed", credential, nil), 401, "", "FULL_PUBLIC_CANARY", "missing-session")
		})
	}
	cases := []struct {
		name, sid, position string
		status              int
		message             string
	}{
		{"session_before_bad_position", "missing-session", "malformed", 404, "session not found"},
		{"ineligible_main", "main-session-missing-workspace+mia", "malformed", 404, "session not found"},
		{"invalid_session", "bad..id", "malformed", 400, ""}, // Existing validateEntityID rejects any '..'.
		{"bad_position", "", "hp1_short", 400, "invalid history position"},
		{"unknown_position", "", "hp1_" + strings.Repeat("A", 43), 404, "history position not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, sid, _ := d2EntryFixture(t)
			if tc.sid != "" {
				sid = tc.sid
			}
			d2EntryRefusal(t, d2EntryRequest(api, sid, tc.position, "Bearer d2-route-test-token", nil), tc.status, tc.message, sid, tc.position, "FULL_PUBLIC_CANARY")
		})
	}
	t.Run("foreign_valid_position_is_not_looked_up", func(t *testing.T) {
		api, _, foreign := d2EntryFixture(t)
		other := createTestSession(t, api)
		d2EntryRefusal(t, d2EntryRequest(api, other, foreign, "Bearer d2-route-test-token", nil), 404, "history position not found", foreign, "FULL_PUBLIC_CANARY")
	})
	t.Run("corrupt_metadata_precedes_position", func(t *testing.T) {
		api, sid, _ := d2EntryFixture(t)
		store := api.agentLoop.GetSessionStore()
		require.NoError(t, store.Close())
		meta := filepath.Join(store.BaseDir(), sid, "meta.json")
		// Remove the real on-disk record and evict cached meta via the existing
		// public cache boundary; no fake store error or altered handler.
		store.EvictSessionMeta(sid)
		require.NoError(t, os.WriteFile(meta, []byte("{not valid metadata"), 0600))
		d2EntryRefusal(t, d2EntryRequest(api, sid, "malformed", "Bearer d2-route-test-token", nil), 500, historyStorageCopy, sid, meta, "not valid metadata")
	})
	for _, damage := range []string{"missing_index", "corrupt_index"} {
		t.Run(damage, func(t *testing.T) {
			api, sid, position := d2EntryFixture(t)
			store := api.agentLoop.GetSessionStore()
			index := filepath.Join(store.BaseDir(), sid, session.ChatHistoryIndexDirectory)
			if damage == "missing_index" {
				require.NoError(t, os.RemoveAll(index))
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(index, "checkpoint.json"), []byte("{broken"), 0600))
			}
			w := d2EntryRequest(api, sid, position, "Bearer d2-route-test-token", nil)
			d2EntryRefusal(t, w, 500, "", sid, position, index, "FULL_PUBLIC_CANARY")
			require.NotContains(t, w.Body.String(), "history entry expired")
		})
	}
}
