// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// stubLiveActivity is a LiveTaskActivityReader test double standing in for the
// running turn's progress atomics (whose reasoning-driven advance is proven in
// pkg/agent's TestTaskLiveLastActivity_AdvancesWhileOnlyReasoningArrives).
type stubLiveActivity struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (s *stubLiveActivity) set(id string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at == nil {
		s.at = map[string]time.Time{}
	}
	s.at[id] = at
}

func (s *stubLiveActivity) LiveTaskLastActivity(id string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.at[id]
	return at, ok
}

// TestTaskWire_LastActivityAt pins the task wire half of the founder decision
// of 2026-09-14: while a task is in progress, GET /tasks/{id} carries
// last_activity_at = the LATER of the running turn's live progress stamp
// (which moves on streamed reasoning) and the last transcript write; the
// field advances as the live stamp advances; and it is absent whenever the
// task is not in progress.
func TestTaskWire_LastActivityAt(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	live := &stubLiveActivity{}
	api.liveTaskActivity = live
	wsID := ensureTestWorkspace(t, api)

	w := postTaskJSON(t, api, `{"title":"reconcile invoices","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON("every invoice is matched")+`,`+
		`"dod":`+oneCriterionJSON("no credentials appear in the output")+`}`)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	id := created.Id

	get := func() gen.Task {
		t.Helper()
		rec := getTaskRec(t, api, id)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var out gen.Task
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}
	setStatus := func(s task.Status) {
		t.Helper()
		_, err := api.taskStore.Update(id, task.Patch{Status: &s})
		require.NoError(t, err)
	}

	// Not in progress: no field, even though live evidence exists.
	live.set(id, time.Now().UTC().Add(-10*time.Second))
	assert.Nil(t, get().LastActivityAt, "a task that is not in progress must not carry last_activity_at")

	setStatus(task.StatusInProgress)

	// Live stamp only (no session yet): the field is that stamp.
	t0 := time.Now().UTC().Add(-10 * time.Second)
	live.set(id, t0)
	got := get().LastActivityAt
	require.NotNil(t, got, "an in-progress task with live activity must carry last_activity_at")
	assert.True(t, got.Equal(t0), "want %v, got %v", t0, *got)

	// Reasoning keeps arriving: the stamp advances, so does the field.
	t1 := time.Now().UTC().Add(-5 * time.Second)
	live.set(id, t1)
	got = get().LastActivityAt
	require.NotNil(t, got)
	assert.True(t, got.Equal(t1), "the wire field must advance with the live stamp: want %v, got %v", t1, *got)

	// A transcript write newer than the live stamp wins.
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "test agent loop must have a shared session store")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "jim")
	require.NoError(t, err)
	sid := meta.ID
	_, err = api.taskStore.Update(id, task.Patch{SessionID: &sid})
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	entryAt := time.Now().UTC()
	require.NoError(t, store.AppendTranscript(sid, session.TranscriptEntry{
		Role: "assistant", Content: "tool result received", Timestamp: entryAt, AgentID: "jim",
	}))
	got = get().LastActivityAt
	require.NotNil(t, got)
	assert.True(t, got.Equal(entryAt), "a newer transcript write must win: want %v, got %v", entryAt, *got)

	// Then live activity newer than the transcript wins again.
	time.Sleep(5 * time.Millisecond)
	t2 := time.Now().UTC()
	live.set(id, t2)
	got = get().LastActivityAt
	require.NotNil(t, got)
	assert.True(t, got.Equal(t2), "newer live activity must win over the transcript: want %v, got %v", t2, *got)

	// Finished: the field disappears.
	setStatus(task.StatusDone)
	assert.Nil(t, get().LastActivityAt, "a finished task must not carry last_activity_at")
}

// With no live evidence and no session, an in-progress task carries no
// last_activity_at at all — the server never fabricates a stamp.
func TestTaskWire_LastActivityAtAbsentWithoutEvidence(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.liveTaskActivity = &stubLiveActivity{}
	wsID := ensureTestWorkspace(t, api)

	w := postTaskJSON(t, api, `{"title":"quiet task","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON("it runs")+`,`+
		`"dod":`+oneCriterionJSON("it is recorded")+`}`)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	st := task.StatusInProgress
	_, err := api.taskStore.Update(created.Id, task.Patch{Status: &st})
	require.NoError(t, err)

	rec := getTaskRec(t, api, created.Id)
	require.Equal(t, http.StatusOK, rec.Code)
	var out gen.Task
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Nil(t, out.LastActivityAt)
}
