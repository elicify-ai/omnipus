package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA MC/pass-through: rename projects an existing prior-boot execution and
// therefore must carry the current epoch, just like list/detail/agent-list.
func TestI1R3RenameSessionCarriesBootEpoch(t *testing.T) {
	f := newI1R1BootFixture(t)
	id := f.root(t, session.LifecycleRunning, f.oldEpoch)
	before := f.journal(t, id)
	body, err := json.Marshal(generated.SessionRenameRequest{Title: "Renamed old execution"})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+id, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	f.api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var row generated.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &row))
	assert.Equal(t, "Renamed old execution", row.Title)
	require.NotNil(t, row.LifecycleState)
	assert.Equal(t, generated.SessionLifecycleStateInterrupted, *row.LifecycleState)
	assert.Equal(t, before, f.journal(t, id), "rename/projection cannot alter the execution journal")
}

// Create always returns a fresh session with no lifecycle record yet. There is
// no old execution to project, so its epoch argument has no observable effect;
// pin the absent fields instead of inventing a stale create-time execution.
func TestI1R3CreateSessionHasNoSyntheticLifecycle(t *testing.T) {
	f := newI1R1BootFixture(t)
	id := createTestSession(t, f.api)
	assert.False(t, f.ls.Exists(id))
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Nil(t, detail.Session.LifecycleState)
	assert.Nil(t, detail.Session.StopNote)
}
