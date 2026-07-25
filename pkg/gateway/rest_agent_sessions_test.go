//go:build !cgo

// rest_agent_sessions_test.go — regression tests for listAgentSessions'
// shared-store merge.
//
// Before this fix, GET /api/v1/agents/{id}/sessions read
// AgentLoop.GetAgentStore(agentID) exclusively. Per GetSessionStore's own
// doc comment (pkg/agent/loop.go), the shared store is where "new sessions"
// are created ("joined session model"); GetAgentStore is "kept for legacy
// per-agent session access". Since ordinary chat sessions moved to the
// shared store, this endpoint silently omitted every session created after
// that move. These tests prove the shared store is now included and that a
// session present in BOTH stores (the shape a pre-fix duplicate-mint bug
// produced) is not double-counted.

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestListAgentSessions_IncludesSharedStoreSessions(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared, "test harness must wire a shared session store")

	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", agent.DefaultAgentID)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	api.listAgentSessions(w, agent.DefaultAgentID)

	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	var got []gen.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

	var found bool
	for _, s := range got {
		if s.Id == meta.ID {
			found = true
		}
	}
	assert.True(t, found, "a session created in the shared store must appear in GET /agents/{id}/sessions")
}

func TestListAgentSessions_DedupesSessionPresentInBothStores(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared, "test harness must wire a shared session store")
	legacy := api.agentLoop.GetAgentStore(agent.DefaultAgentID)
	require.NotNil(t, legacy, "test harness must wire a legacy per-agent session store")

	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", agent.DefaultAgentID)
	require.NoError(t, err)

	// Simulate the pre-fix duplicate-mint bug: the exact same session id also
	// has an on-disk meta.json in the legacy per-agent store. Written directly
	// to disk (not via NewSession, which always mints a fresh random id) so
	// both stores genuinely disagree-yet-agree about one session id, exactly
	// the shape the merge's dedup must collapse to one entry.
	dupDir := filepath.Join(legacy.BaseDir(), meta.ID)
	require.NoError(t, os.MkdirAll(dupDir, 0o700))
	dupMeta := *meta
	data, err := json.Marshal(dupMeta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dupDir, "meta.json"), data, 0o600))

	w := httptest.NewRecorder()
	api.listAgentSessions(w, agent.DefaultAgentID)

	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	var got []gen.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))

	count := 0
	for _, s := range got {
		if s.Id == meta.ID {
			count++
		}
	}
	assert.Equal(t, 1, count, "a session id present in both the shared and legacy stores must appear exactly once")
}

func TestListAgentSessions_UnknownAgent_ReturnsEmpty(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	api.listAgentSessions(w, "no-such-agent")

	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	var got []gen.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Empty(t, got)
}

// TestListAgentSessions_AllStoresFail_Returns500 proves a real backend
// failure is still surfaced as 500, not silently presented as "this agent
// has zero sessions". With no data recovered from either store,
// listAgentSessions must not collapse that into a lying 200+[].
//
// The failure is injected by replacing each store's base directory with a
// REGULAR FILE, so os.ReadDir fails with ENOTDIR. Do not switch this back to
// chmod 0o000: root holds CAP_DAC_OVERRIDE and walks straight through the
// permission bits, so the chmod form passes locally (unprivileged dev pod)
// and silently returns 200+[] under CI, which runs as root. ENOTDIR is
// privilege-independent and fails identically for both.
func TestListAgentSessions_AllStoresFail_Returns500(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	legacy := api.agentLoop.GetAgentStore(agent.DefaultAgentID)
	require.NotNil(t, legacy)

	for _, dir := range []string{shared.BaseDir(), legacy.BaseDir()} {
		require.NoError(t, os.RemoveAll(dir))
		require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
		t.Cleanup(func() { _ = os.Remove(dir) })
	}

	w := httptest.NewRecorder()
	api.listAgentSessions(w, agent.DefaultAgentID)

	assert.Equal(t, 500, w.Code, "body: %s", w.Body.String())
}
