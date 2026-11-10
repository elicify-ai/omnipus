// rest_clear_all_incomplete_walk_test.go: S2-02 (combined review). Delete-all
// protects a main's helper sessions through the durable lifecycle edge. When
// that walk cannot be completed the request must fail closed: nothing is
// deleted and the person is not told "cleared". ORACLE: founder ruling S2 #6
// (a main's helper sessions are part of its history and are never deleted by
// delete-all).

package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearAllWalkFixture: a main, plus an ordinary-looking chat that belongs to
// it ONLY through the durable lifecycle edge (no parent link on the chat).
func clearAllWalkFixture(t *testing.T) (env *u1Env, helper string, lc *session.LifecycleStore) {
	t.Helper()
	env = u1NewEnv(t, false)
	const ws = "01JCLEARWALKWS000000000001"
	env.seedWorkspace(t, ws, true, "mia")
	main := u1MainID(ws, "mia")
	code, _ := sf1Do(env, http.MethodGet, "/api/v1/sessions/"+main, "")
	require.Equal(t, http.StatusOK, code, "precondition: the main opens")

	helper = sf1NewChat(t, env)
	lc = session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle"))
	env.api.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: helper, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: main,
		WorkspaceID: ws, AgentID: "mia",
		SteeredBy: &session.SteeredBy{SteeringSessionID: main, RootSessionID: main},
	}))
	return env, helper, lc
}

func TestClearAll_HelperKeptByLifecycleEdge(t *testing.T) {
	env, helper, _ := clearAllWalkFixture(t)
	code, body := clearAllDo(env)
	require.Equal(t, http.StatusOK, code, body)
	_, err := env.store(t).GetMeta(helper)
	assert.NoError(t, err, "instrument check: the lifecycle edge alone protects the helper")
}

func TestClearAll_IncompleteHelperWalkDeletesNothing(t *testing.T) {
	env, helper, lc := clearAllWalkFixture(t)
	// Warm the index while the record is healthy, then corrupt the record so
	// the walk's Load of the child fails with a non-NotFound error.
	_, err := lc.List(session.LifecycleFilter{SteeringSessionID: "warm"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(lc.Dir(), helper+".jsonl"), []byte("not json\n"), 0o600))

	code, body := clearAllDo(env)

	assert.Equal(t, http.StatusInternalServerError, code,
		"an incomplete walk must refuse the whole request, not report cleared; body=%s", body)
	_, gerr := env.store(t).GetMeta(helper)
	assert.NoError(t, gerr, "a chat that may belong to a main must not be deleted when the walk is incomplete")
}
