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

// TestListAgentSessions_SharedStoreFailsLegacyOk_Returns500NotPartial200 is
// the CRITICAL regression this file's other tests didn't cover: ONE store
// errors while the OTHER still produces real data. Before this fix,
// listAgentSessions escalated to 500 only when len(metas)==0 AND len(errs)>0
// (see the pre-fix condition in listAgentSessions's git history) — so a
// legacy-store session alongside a shared-store failure produced a 200 with
// ONLY the legacy session, silently omitting whatever the (healthy-by-
// comparison but actually broken) shared store — the PRIMARY home for
// sessions minted after the shared-store migration — could not report. That
// re-introduces, one layer up, the exact "silent incomplete list presented
// as complete" bug this function's own doc comment says it was written to
// fix.
//
// The failure is injected the same ENOTDIR way as
// TestListAgentSessions_AllStoresFail_Returns500 (see its doc for why not
// chmod 0o000), applied to ONLY the shared store's directory. The legacy
// store is left healthy and seeded with one real session, so a pre-fix
// implementation would return 200 with that one session — this test asserts
// the caller instead gets an honest 500, never a partial list.
func TestListAgentSessions_SharedStoreFailsLegacyOk_Returns500NotPartial200(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	legacy := api.agentLoop.GetAgentStore(agent.DefaultAgentID)
	require.NotNil(t, legacy)

	// Seed the legacy store with a real, healthy session.
	_, err := legacy.NewSession(session.SessionTypeChat, "webchat", agent.DefaultAgentID)
	require.NoError(t, err)

	// Break ONLY the shared store's directory.
	require.NoError(t, os.RemoveAll(shared.BaseDir()))
	require.NoError(t, os.WriteFile(shared.BaseDir(), []byte("not a directory"), 0o600))
	t.Cleanup(func() { _ = os.Remove(shared.BaseDir()) })

	w := httptest.NewRecorder()
	api.listAgentSessions(w, agent.DefaultAgentID)

	require.Equal(t, 500, w.Code,
		"a shared-store read failure must escalate to 500 even though the legacy store "+
			"still has data — body: %s", w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"], "500 response must carry an attributable error message")
}

// TestListAgentSessions_LegacyStoreFailsSharedOk_Returns500NotPartial200 is
// the mirror of the test above: the legacy store errors while the shared
// store (the primary store for this agent's sessions) is healthy and has a
// real session. Even though the shared store alone would already be the
// "usually complete" answer, this endpoint has no wire-shape slot to mark a
// 200 response "may be missing legacy-store data" (see listAgentSessions's
// own escalation-check comment), so it must still 500 rather than guess.
func TestListAgentSessions_LegacyStoreFailsSharedOk_Returns500NotPartial200(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)
	legacy := api.agentLoop.GetAgentStore(agent.DefaultAgentID)
	require.NotNil(t, legacy)

	// Seed the shared store with a real, healthy session.
	_, err := shared.NewSession(session.SessionTypeChat, "webchat", agent.DefaultAgentID)
	require.NoError(t, err)

	// Break ONLY the legacy store's directory.
	require.NoError(t, os.RemoveAll(legacy.BaseDir()))
	require.NoError(t, os.WriteFile(legacy.BaseDir(), []byte("not a directory"), 0o600))
	t.Cleanup(func() { _ = os.Remove(legacy.BaseDir()) })

	w := httptest.NewRecorder()
	api.listAgentSessions(w, agent.DefaultAgentID)

	assert.Equal(t, 500, w.Code,
		"a legacy-store read failure must escalate to 500 even though the shared store "+
			"still has data — body: %s", w.Body.String())
}
