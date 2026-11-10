package systools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// Architect final-gate item 1 (DEL-10): the agent-delete cascade uses the ONE live
// session store its caller passes in - it never opens a store of its own - and it
// stops an agent's running work before it deletes the sessions.

// recordingSessionStore stands in for the loop's live store. Every list and delete
// goes through it, in order, so the test can see both which store was used and
// what happened before each delete.
type recordingSessionStore struct {
	metas  []*session.UnifiedMeta
	events []string
}

func (r *recordingSessionStore) ListSessionsFiltered(keep func(*session.UnifiedMeta) bool) ([]*session.UnifiedMeta, error) {
	r.events = append(r.events, "list")
	var out []*session.UnifiedMeta
	for _, m := range r.metas {
		if keep(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *recordingSessionStore) DeleteSession(id string) error {
	r.events = append(r.events, "delete:"+id)
	return nil
}

func meta(id string, agents ...string) *session.UnifiedMeta {
	m := &session.UnifiedMeta{}
	m.ID = id
	m.AgentIDs = agents
	return m
}

func createCascadeAgent(t *testing.T) (home, agentID, revision string) {
	t.Helper()
	deps, home := newTestDepsWithHome(t)
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Live Store Bot", "description": "to be deleted", "soul": "You help.", "model": "test/model", "color": "#6E8BA8",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", res.ForLLM)
	}
	m := parseSuccess(t, res.ForLLM)
	agentID, _ = m["id"].(string)
	revision, _ = m["revision"].(string)
	if agentID == "" || revision == "" {
		t.Fatalf("create result missing id/revision: %v", m)
	}
	return home, agentID, revision
}

func TestAgentDeleteCascade_UsesTheCallersStoreAndStopsFirst(t *testing.T) {
	home, agentID, revision := createCascadeAgent(t)
	live := &recordingSessionStore{metas: []*session.UnifiedMeta{
		meta("s-sole", agentID),
		meta("s-shared", agentID, "other"),
	}}
	// A directory on disk that a SECOND store would see and delete: it must stay.
	onDisk := filepath.Join(home, "sessions", "s-only-on-disk")
	if err := os.MkdirAll(onDisk, 0o700); err != nil {
		t.Fatal(err)
	}
	var order []string
	stop := func(id string) string {
		order = append(order, "stop:"+id)
		live.events = append(live.events, "stop:"+id)
		return ""
	}

	out := systools.RunAgentDeleteCascade(home, agentID, revision, live, stop)

	if out.CleanupFailed || out.DeletionErr != nil {
		t.Fatalf("cascade failed: cleanup=%v err=%v warnings=%v", out.CleanupFailed, out.DeletionErr, out.Warnings)
	}
	want := []string{"list", "stop:s-sole", "delete:s-sole"}
	if strings.Join(live.events, ",") != strings.Join(want, ",") {
		t.Fatalf("events on the caller's store = %v, want %v (stop BEFORE delete, shared session untouched)", live.events, want)
	}
	if out.SessionsDeleted != 1 || out.SessionsPreservedShared != 1 {
		t.Fatalf("deleted=%d shared=%d, want 1 and 1", out.SessionsDeleted, out.SessionsPreservedShared)
	}
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("a session only a second store would reach was removed: %v (the cascade opened its own store)", err)
	}
	if len(order) != 1 {
		t.Fatalf("stop calls = %v", order)
	}
}

// A session that will not stop is kept; the agent record stays so the delete retries.
func TestAgentDeleteCascade_ASessionThatWillNotStopKeepsTheRecord(t *testing.T) {
	home, agentID, revision := createCascadeAgent(t)
	live := &recordingSessionStore{metas: []*session.UnifiedMeta{meta("s-busy", agentID)}}
	stop := func(string) string { return "still running" }

	out := systools.RunAgentDeleteCascade(home, agentID, revision, live, stop)

	if !out.CleanupFailed {
		t.Fatal("a session that will not stop must fail the cleanup so the record stays and the delete retries")
	}
	for _, e := range live.events {
		if strings.HasPrefix(e, "delete:") {
			t.Fatalf("a session that did not stop was deleted: %v", live.events)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "entities", "agents", agentID+".json")); err != nil {
		t.Fatalf("the agent record must still exist: %v", err)
	}
}

// No store: the delete fails closed and is retryable; it never opens its own.
func TestAgentDeleteCascade_MissingStoreFailsClosedWithoutOpeningOne(t *testing.T) {
	home, agentID, revision := createCascadeAgent(t)
	if err := os.MkdirAll(filepath.Join(home, "sessions", "s-1"), 0o700); err != nil {
		t.Fatal(err)
	}

	out := systools.RunAgentDeleteCascade(home, agentID, revision, nil, nil)

	if !out.CleanupFailed {
		t.Fatalf("a missing store must fail the cleanup: %+v", out)
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "session store unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want a 'session store unavailable' warning", out.Warnings)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", "s-1")); errors.Is(err, os.ErrNotExist) {
		t.Fatal("the cascade opened its own store and deleted a session")
	}
}
