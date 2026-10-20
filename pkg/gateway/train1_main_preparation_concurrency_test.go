package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func train1AwaitPreparationGate(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("detail request never reached the controlled preparation boundary")
	}
}

func train1AwaitDetailResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(10 * time.Second):
		t.Fatal("detail request deadlocked after the controlled boundary was released")
		return nil
	}
}

func train1DetailRequest(api *restAPI, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	api.HandleWorkspaces(w, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil))
	return w
}

func train1DeleteWorkspace(t *testing.T, env *u1Env, id, revision string) {
	t.Helper()
	w := httptest.NewRecorder()
	env.api.HandleWorkspaces(w, httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/"+id+"?revision="+revision, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	result := decodeMutationState(t, w.Body.Bytes())
	assert.Equal(t, "complete", string(result.PersistenceStatus), w.Body.String())
	assert.NoFileExists(t, filepath.Join(env.home, "workspaces", id+".json"))
}

// F1: force the old snapshot -> DELETE -> prepare ordering. The observer only
// controls scheduling: real GET, DELETE, workspace locks and session stores run.
func TestTrain1WorkspaceDetailDoesNotResurrectDeletedMains(t *testing.T) {
	t.Run("delete completes before preparation", func(t *testing.T) {
		env := u1NewEnv(t, false)
		const ws = "train1-delete-before-prepare"
		env.seedWorkspace(t, ws, false, "mia", "jim")
		for _, agent := range []string{"mia", "jim"} {
			_, err := env.store(t).GetOrCreateMainSession(ws, agent)
			require.NoError(t, err)
			require.DirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(ws, agent)))
		}
		state, err := workspace.ReadState(env.home, ws)
		require.NoError(t, err)
		entered, release := make(chan struct{}, 1), make(chan struct{})
		resume := sync.OnceFunc(func() { close(release) })
		var wg sync.WaitGroup
		defer func() { resume(); wg.Wait() }()
		env.api.beforeWorkspaceDetailPreparation = func() { entered <- struct{}{}; <-release }
		done := make(chan *httptest.ResponseRecorder, 1)
		wg.Go(func() { done <- train1DetailRequest(env.api, ws) })
		train1AwaitPreparationGate(t, entered)

		train1DeleteWorkspace(t, env, ws, state.Revision)
		for _, agent := range []string{"mia", "jim"} {
			require.NoDirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(ws, agent)), "instrument: deletion really removed the old main")
		}
		afterDelete := env.u1RawDirNames(t)
		resume()
		get := train1AwaitDetailResponse(t, done)
		assert.Equal(t, http.StatusNotFound, get.Code, get.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(get.Body.Bytes(), &body))
		assert.Equal(t, map[string]any{"error": "workspace not found"}, body)
		assert.Equal(t, afterDelete, env.u1RawDirNames(t), "a completed deletion must not be followed by any recreated or replacement session directory")
		for _, agent := range []string{"mia", "jim"} {
			assert.Empty(t, env.u1LocationsOf(t, u1MainID(ws, agent)))
		}
	})

	t.Run("preparation completes before delete", func(t *testing.T) {
		env := u1NewEnv(t, false)
		const ws = "train1-prepare-before-delete"
		env.seedWorkspace(t, ws, false, "mia")
		get := train1DetailRequest(env.api, ws)
		require.Equal(t, http.StatusOK, get.Code, get.Body.String())
		require.DirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(ws, "mia")))
		state, err := workspace.ReadState(env.home, ws)
		require.NoError(t, err)
		train1DeleteWorkspace(t, env, ws, state.Revision)
		assert.NoDirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(ws, "mia")))
		assert.Equal(t, http.StatusNotFound, train1DetailRequest(env.api, ws).Code)
		assert.Empty(t, env.u1LocationsOf(t, u1MainID(ws, "mia")))
	})
}

// T2: both requests are in flight before either is admitted to preparation.
// Assert physical identities and bytes, not elapsed time or creator call count.
func TestTrain1WorkspaceDetailConcurrentPreparationPreservesIdentity(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "train1-overlapping-details"
	env.seedWorkspace(t, ws, false, "mia", "jim", "worker1")
	_, err := env.store(t).GetOrCreateMainSession(ws, "jim")
	require.NoError(t, err)
	jimID := u1MainID(ws, "jim")
	require.NoError(t, env.store(t).AppendTranscript(jimID, session.TranscriptEntry{
		ID: "preserved-history", Role: "user", Content: "Keep this existing history.", Timestamp: time.Now().UTC(),
	}))
	metaPath := filepath.Join(env.store(t).BaseDir(), jimID, "meta.json")
	historyPath := filepath.Join(env.store(t).BaseDir(), jimID, "transcript.jsonl")
	metaBefore, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	historyBefore, err := os.ReadFile(historyPath)
	require.NoError(t, err)
	unlock := workspace.LockID(ws)
	err = workspace.SaveDelegation(env.home, ws, []workspace.DelegationEdge{{FromAgent: "jim", ToAgent: "mia", Modes: []workspace.DelegationMode{workspace.ModeDirect}}})
	unlock()
	require.NoError(t, err)
	state, err := workspace.ReadState(env.home, ws)
	require.NoError(t, err)
	recordPath := filepath.Join(env.home, "workspaces", ws+".json")
	graphPath := filepath.Join(env.home, "entities", "delegation", ws+".json")
	recordBefore, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	graphBefore, err := os.ReadFile(graphPath)
	require.NoError(t, err)
	rawWant := append(env.u1RawDirNames(t), u1MainID(ws, "mia"))
	sort.Strings(rawWant)

	entered, release := make(chan struct{}, 2), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	defer func() { resume(); wg.Wait() }()
	env.api.beforeWorkspaceDetailPreparation = func() { entered <- struct{}{}; <-release }
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wg.Go(func() { done <- train1DetailRequest(env.api, ws) })
	}
	for range 2 {
		train1AwaitPreparationGate(t, entered)
	}
	resume()
	for range 2 {
		w := train1AwaitDetailResponse(t, done)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, state.Revision, response["revision"])
		for _, agent := range []string{"mia", "jim"} {
			member := u1MemberConfig(response, agent)
			require.NotNil(t, member)
			assert.Equal(t, u1MainID(ws, agent), member["main_session_id"])
			assert.NotContains(t, member, "main_session_error")
		}
		assert.NotContains(t, u1MemberConfig(response, "worker1"), "main_session_id")
	}
	assert.Equal(t, rawWant, env.u1RawDirNames(t), "exact raw inventory detects duplicate, random or malformed replacement directories")
	for _, agent := range []string{"mia", "jim"} {
		assert.Equal(t, []string{env.store(t).BaseDir()}, env.u1LocationsOf(t, u1MainID(ws, agent)))
	}
	assert.NoDirExists(t, filepath.Join(env.store(t).BaseDir(), u1MainID(ws, "worker1")))
	for _, pair := range []struct {
		path   string
		before []byte
	}{{metaPath, metaBefore}, {historyPath, historyBefore}, {recordPath, recordBefore}, {graphPath, graphBefore}} {
		after, err := os.ReadFile(pair.path)
		require.NoError(t, err)
		assert.Equal(t, pair.before, after, "concurrent detail preparation must preserve %s", pair.path)
	}
}

// T1: a user may deliberately retry before the operator has repaired storage.
// Every response must remain unavailable with the same safe cause.
func TestTrain1WorkspaceDetailPersistentFailureRemainsUnavailable(t *testing.T) {
	logs := sf1CaptureSlog(t)
	env := u1NewEnv(t, false)
	const ws = "train1-persistent-preparation-fault"
	env.seedWorkspace(t, ws, false, "mia", "jim")
	blocker := filepath.Join(env.store(t).BaseDir(), u1MainID(ws, "mia"))
	const marker = "unreadable identity must remain untouched"
	require.NoError(t, os.WriteFile(blocker, []byte(marker), 0o600))
	_, err := os.ReadFile(filepath.Join(blocker, "meta.json"))
	require.Error(t, err, "instrument: storage must really fail")
	require.NotErrorIs(t, err, os.ErrNotExist)
	state, err := workspace.ReadState(env.home, ws)
	require.NoError(t, err)
	var admissions atomic.Int64
	env.api.beforeWorkspaceDetailPreparation = func() { admissions.Add(1) }
	const reason = "The main chat could not be prepared because session storage has a file where a folder is required. Ask the server operator to fix it, then retry."
	for attempt := range 3 {
		w := train1DetailRequest(env.api, ws)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		failed := u1MemberConfig(response, "mia")
		require.NotNil(t, failed)
		assert.Equal(t, reason, failed["main_session_error"], "retry %d must not discard the persisting cause", attempt)
		assert.NotContains(t, failed, "main_session_id")
		assert.Equal(t, u1MainID(ws, "jim"), u1MemberConfig(response, "jim")["main_session_id"])
		assert.Equal(t, state.Revision, response["revision"])
		assert.Equal(t, int64(attempt+1), admissions.Load(), "one admission per deliberate GET")
		assert.Equal(t, attempt+1, strings.Count(logs(), "rest: workspace detail: some team mains could not be prepared"), "one reported failure per deliberate request, no retry amplification")
	}
	after, err := os.ReadFile(blocker)
	require.NoError(t, err)
	assert.Equal(t, marker, string(after))
	assert.Equal(t, []string{u1MainID(ws, "jim")}, env.u1RawDirNames(t))
}
