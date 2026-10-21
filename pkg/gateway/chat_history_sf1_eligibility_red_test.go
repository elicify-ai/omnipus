package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Security F1: a valid token must actually reach the TWO separate main-session
// eligibility paths. The auth-only negative remains useful but cannot kill
// either gate-bypass mutant. No gate or authorization outcome is faked here.
func TestSF1_AuthenticatedIneligibleUnreadableMainRefusesBothReadGates(t *testing.T) {
	env := u1NewEnv(t, false)
	const workspaceID = "01JSF1GATE000000000000000H"
	env.seedWorkspace(t, workspaceID, false, "mia")
	sid := u1MainID(workspaceID, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+sid, "")
	require.Equal(t, http.StatusOK, code, "fixture main exists and is eligible")
	require.NoError(t, env.store(t).AppendTranscript(sid, session.TranscriptEntry{
		ID: "private-eligibility-row", Role: "user", Content: "PRIVATE_ELIGIBILITY_HISTORY", AgentID: "mia", Timestamp: time.Now().UTC(),
	}))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sf1-eligibility-token")
	sf1BlockParent(t, filepath.Join(env.store(t).BaseDir(), sid, "transcript.jsonl"))
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer sf1-eligibility-token")
		w := httptest.NewRecorder()
		env.api.withAuth(env.api.HandleSessions)(w, r)
		return w
	}
	// This exact token, main and unreadable-history fixture must first reach
	// authorized metadata. A universal401/404/storage failure cannot satisfy F1.
	eligible := request("/api/v1/sessions/" + sid)
	require.Equal(t, http.StatusOK, eligible.Code, "valid auth+eligible main keeps metadata reachable with unreadable history: %s", eligible.Body.String())
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(eligible.Body.Bytes(), &detail))
	require.Equal(t, sid, detail.Session.Id)
	require.Equal(t, "mia", detail.Session.AgentId)
	require.Equal(t, "unavailable", detail.HistoryState.Status)
	require.Nil(t, detail.Messages)
	require.Nil(t, detail.PageInfo)
	require.Nil(t, detail.HasEarlier)
	require.Nil(t, detail.HasLater)
	require.Nil(t, detail.NextBefore)
	require.Nil(t, detail.NextAfter)

	// Same retained main, still cached metadata/history fault and valid token;
	// only actual workspace-team eligibility is removed.
	env.seedWorkspace(t, workspaceID, false)
	for _, tc := range []struct{ name, path string }{
		{"messages_gateMainRead", "/api/v1/sessions/" + sid + "/messages"},
		{"detail_resolveMainSessionForRead", "/api/v1/sessions/" + sid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused := request(tc.path)
			pagingRefusal(t, refused, http.StatusNotFound, "session not found")
			sf1AssertNoInternalDetail(t, env, refused.Body.String(), sid, workspaceID, "mia", "transcript", "PRIVATE_ELIGIBILITY_HISTORY")
			sf1AssertNoPhysicalStorageDetail(t, env, refused.Body.String())
			require.NotContains(t, refused.Body.String(), "history_state", "ineligible mains never receive the metadata-only bypass")
		})
	}
}
