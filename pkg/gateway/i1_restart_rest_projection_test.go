package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: I1 option B / founder sixth display value, through the real Session
// list/detail producers. Existing human Stop and genuine failure remain distinct.
func TestI1RestartRESTProjection(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	ls := session.NewLifecycleStore(t.TempDir())
	api.agentLoop.SetSessionMessagingStores(nil, ls)
	cases := []struct {
		name   string
		state  session.LifecycleState
		cause  session.StopCause
		reason string
		want   generated.SessionLifecycleState
	}{
		{"restart", session.LifecycleStopped, session.StopCauseRestart, "", generated.SessionLifecycleStateInterrupted},
		{"human_stop", session.LifecycleStopped, session.StopCauseStop, "", generated.SessionLifecycleStateStopped},
		{"genuine_failure", session.LifecycleFailed, "", "provider_error", generated.SessionLifecycleStateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := createTestSession(t, api)
			rec := &session.LifecycleRecord{SessionID: id, Generation: 1, State: tc.state, FailedReason: tc.reason, AgentID: "mia", WorkspaceID: "ws", OwnerScopeKind: session.OwnerScopeHuman}
			if tc.cause != "" {
				rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: session.StopActorHumanUser("owner"), Seq: 1, Cause: tc.cause}
				if tc.cause == session.StopCauseRestart {
					rec.StopNote.By = session.StopActorRestart
					rec.StopNote.BootSeq = 1
				}
			}
			require.NoError(t, ls.Persist(rec))
			w := httptest.NewRecorder()
			api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions?flat=true", nil))
			require.Equal(t, http.StatusOK, w.Code, "real session list must load after recovery: %s", w.Body.String())
			var page generated.SessionPage
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
			found := false
			for _, row := range page.Sessions {
				if row.Id != id {
					continue
				}
				found = true
				require.NotNil(t, row.LifecycleState)
				assert.Equal(t, tc.want, *row.LifecycleState, "sidebar list must project the saved stop cause")
				assert.True(t, row.LifecycleState.Valid(), "only existing generated lifecycle values")
				assert.True(t, row.Status.Valid(), "do not remove or hand-write coarse status values")
				if tc.cause != "" {
					require.NotNil(t, row.StopNote)
					assert.Equal(t, generated.SessionStopNoteCause(tc.cause), row.StopNote.Cause)
					assert.Equal(t, generated.SessionStatusActive, row.Status, "option B leaves a stopped conversation usable")
				}
			}
			assert.True(t, found, "the original conversation must remain reachable in the real list")
			w = httptest.NewRecorder()
			api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil))
			require.Equal(t, http.StatusOK, w.Code, "real chat detail must load after recovery: %s", w.Body.String())
			var detail generated.SessionDetail
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
			require.NotNil(t, detail.Session.LifecycleState)
			assert.Equal(t, string(tc.want), string(*detail.Session.LifecycleState), "chat detail and sidebar must agree")
			assert.True(t, detail.Session.LifecycleState.Valid())
		})
	}
}
