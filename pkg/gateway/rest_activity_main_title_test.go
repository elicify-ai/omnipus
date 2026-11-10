package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S2-05 (combined review, server part): the activity feed names a main
// session by its fixed title ("<Agent> - Main Session", derived on read), like
// every other read surface - never "New session" because the stored title of a
// main is empty. ORACLE: nav spec FR-008 title rule, quoted on withDisplayTitle.
func TestActivityFeed_MainSessionUsesItsDerivedTitle(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JACTIVITYMAINTITLE0001"
	env.seedWorkspace(t, ws, true, "mia")
	mainID := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+mainID, "")
	require.Equal(t, http.StatusOK, code, "precondition: the main is created")

	env.api.taskStore = task.New(env.home + "/tasks")
	w := httptest.NewRecorder()
	env.api.HandleActivity(w, httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Events []struct {
			ID      string  `json:"id"`
			Summary *string `json:"summary"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	var summary string
	found := false
	for _, ev := range resp.Events {
		if ev.ID == "session-"+mainID && ev.Summary != nil {
			summary, found = *ev.Summary, true
		}
	}
	require.True(t, found, "instrument check: the main session appears in the feed; body=%s", w.Body.String())
	assert.NotEqual(t, "New session", summary)
	assert.Regexp(t, ` - Main Session$`, summary)
}
