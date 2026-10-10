// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: shared fixtures for the main-session identity
// tests (FR-002, FR-003, DEL-01, DEL-11; BDD-01.x, BDD-12.3).
//
// Everything here drives EXISTING seams only: REST handlers through httptest
// (HandleWorkspaces, HandleSessions, createSessionHTTP), the real store, and the
// real inbound schema validator. Helpers are prefixed u1 so deleting the retired
// heartbeat tests never takes them away.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// u1Env is one gateway REST harness with a fixed roster.
type u1Env struct {
	api      *restAPI
	home     string
	agentIDs []string
}

// u1Roster: mia and jim are eligible native chat targets (custom); admin is the
// core operator (never a workspace member); worker1 is a worker (no main);
// judge is a hidden system agent (no main).
func u1Roster(extra ...config.AgentConfig) []config.AgentConfig {
	l := []config.AgentConfig{
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
		{ID: "jim", Name: "Jim", Type: config.AgentTypeCustom},
		{ID: "admin", Name: "Admin", Type: config.AgentTypeCore},
		{ID: "worker1", Name: "Worker", Type: config.AgentTypeWorker},
		{ID: "judge", Name: "Judge", Type: config.AgentTypeSystem},
	}
	return append(l, extra...)
}

func u1NewEnv(t *testing.T, validateInbound bool, extra ...config.AgentConfig) *u1Env {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, ValidateInbound: validateInbound},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: u1Roster(extra...),
		},
	}
	seedAgentEntities(t, home, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	env := &u1Env{api: &restAPI{agentLoop: al, homePath: home}, home: home}
	for _, a := range cfg.Agents.List {
		env.agentIDs = append(env.agentIDs, a.ID)
	}
	return env
}

func (e *u1Env) store(t *testing.T) *session.UnifiedStore {
	t.Helper()
	s := e.api.agentLoop.GetSessionStore()
	require.NotNil(t, s, "shared session store must exist")
	return s
}

// u1SeedWorkspace writes a workspace record directly (the fixture seam existing
// tests use). team is the starting core_team.
func (e *u1Env) seedWorkspace(t *testing.T, id string, isDefault bool, team ...string) {
	t.Helper()
	ws := storedWorkspace{
		ID: id, Name: "WS " + id, Status: "active", IsDefault: isDefault,
		CoreTeam:  team,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}
	require.NoError(t, writeWorkspaceFile(e.home, ws))
}

// put sends PUT /api/v1/workspaces/{id} with the current revision merged into fields.
func (e *u1Env) put(t *testing.T, wsID string, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	state, err := workspace.ReadState(e.home, wsID)
	require.NoError(t, err)
	body := map[string]any{"revision": state.Revision}
	for k, v := range fields {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+wsID, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces/" + wsID
	e.api.HandleWorkspaces(w, r)
	return w
}

func (e *u1Env) putTeam(t *testing.T, wsID string, team ...string) *httptest.ResponseRecorder {
	t.Helper()
	return e.put(t, wsID, map[string]any{"core_team": team})
}

// getWorkspace returns GET /api/v1/workspaces/{id} decoded generically.
func (e *u1Env) getWorkspace(t *testing.T, wsID string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+wsID, nil)
	r.URL.Path = "/api/v1/workspaces/" + wsID
	e.api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET workspace: %s", w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

// memberConfig returns workspace.member_configs[agentID] (nil when absent).
func u1MemberConfig(ws map[string]any, agentID string) map[string]any {
	mcs, _ := ws["member_configs"].(map[string]any)
	mc, _ := mcs[agentID].(map[string]any)
	return mc
}

// getSession returns the "session" object of GET /api/v1/sessions/{id}
// (SessionDetail envelope); nil unless the status is 200.
func (e *u1Env) getSession(t *testing.T, id string) (map[string]any, int) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil)
	r.URL.Path = "/api/v1/sessions/" + id
	e.api.HandleSessions(w, r)
	var detail map[string]any
	var out map[string]any
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail), "body: %s", w.Body.String())
		var ok bool
		out, ok = detail["session"].(map[string]any)
		require.True(t, ok, "SessionDetail must carry a session object: %s", w.Body.String())
	}
	return out, w.Code
}

// listSessions returns the rows of GET /api/v1/sessions?<query>.
func (e *u1Env) listSessions(t *testing.T, query string) []map[string]any {
	t.Helper()
	target := "/api/v1/sessions"
	if query != "" {
		target += "?" + query
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.URL.Path = "/api/v1/sessions"
	e.api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "list sessions: %s", w.Body.String())
	var page sessionPageWire
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	return page.Sessions
}

func u1Row(rows []map[string]any, id string) map[string]any {
	for _, r := range rows {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func (e *u1Env) deleteSession(t *testing.T, id string) int {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+id, nil)
	r.URL.Path = "/api/v1/sessions/" + id
	e.api.HandleSessions(w, r)
	return w.Code
}

// u1Stored is one session directory as persisted. doc is nil when meta.json is
// missing or unparseable.
type u1Stored struct {
	dir string
	doc map[string]any
}

// u1StoreDirs returns every distinct session-store base directory this env
// exposes: the shared store first, then each per-agent store, in a
// deterministic order. Two stores can legitimately hold DIFFERENT sessions; the
// same computed main id must never appear in two of them (C1 CHECK survivor
// m26), so callers enumerate locations, never only merged names.
func (e *u1Env) u1StoreDirs(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var dirs []string
	add := func(s *session.UnifiedStore) {
		if s == nil || seen[s.BaseDir()] {
			return
		}
		seen[s.BaseDir()] = true
		dirs = append(dirs, s.BaseDir())
	}
	add(e.api.agentLoop.GetSessionStore())
	for range e.agentIDs {
		add(e.api.agentLoop.GetSessionStore())
	}
	return dirs
}

// u1LocationsOf returns the store base directories that physically contain a
// session directory named id. A computed main id must have EXACTLY ONE such
// location: a second physical copy in a per-agent store is hidden from a
// name-keyed merge (C1 CHECK survivor m26: the same "main-session-<ws>+<agent>"
// persisted in both the shared and a per-agent store survived a count that
// keyed by directory name).
func (e *u1Env) u1LocationsOf(t *testing.T, id string) []string {
	t.Helper()
	var out []string
	for _, d := range e.u1StoreDirs(t) {
		if info, err := os.Stat(filepath.Join(d, id)); err == nil && info.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// u1RawDirNames returns every session-store directory NAME across the shared
// and per-agent stores, taken from the RAW listing — independent of whether it
// carries parseable metadata. It excludes only the store's own ".context"
// backend. (C1 CHECK survivor m21: a stray directory with no meta.json is
// invisible to the meta-bearing helpers, so the absence of a forbidden id must
// be checked raw.)
func (e *u1Env) u1RawDirNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, d := range e.u1StoreDirs(t) {
		entries, err := os.ReadDir(d)
		require.NoError(t, err)
		for _, ent := range entries {
			if ent.IsDir() && ent.Name() != ".context" {
				out = append(out, ent.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// u1PersistedWorkspace reads the workspace record straight off disk
// ($OMNIPUS_HOME/workspaces/<id>.json) as raw JSON. This is the PERSISTED
// settings, distinct from the wire projection, which may conceal a field the
// record still carries (C1 CHECK survivor m19r: a retired heartbeat session
// address stayed in the persisted record while the wire hid it).
func (e *u1Env) u1PersistedWorkspace(t *testing.T, wsID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.home, "workspaces", wsID+".json"))
	require.NoError(t, err, "persisted workspace record %s.json must exist", wsID)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// storedSessions scans every distinct session store directory (shared + each
// per-agent store) and returns the persisted identities keyed by directory
// name. BDD-01.1: counts persisted identities, not calls.
//
// CAVEAT (C1 CHECK survivor m26): keying by directory NAME merges the SAME id
// persisted in two stores into one entry, hiding a duplicate physical copy. Use
// u1LocationsOf for any claim that a computed id exists in exactly one place.
func (e *u1Env) storedSessions(t *testing.T) map[string]u1Stored {
	t.Helper()
	out := map[string]u1Stored{}
	for _, d := range e.u1StoreDirs(t) {
		entries, err := os.ReadDir(d)
		require.NoError(t, err)
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			rec := u1Stored{dir: filepath.Join(d, ent.Name())}
			if raw, err := os.ReadFile(filepath.Join(rec.dir, "meta.json")); err == nil {
				var doc map[string]any
				if json.Unmarshal(raw, &doc) == nil {
					rec.doc = doc
				}
			}
			out[ent.Name()] = rec
		}
	}
	return out
}

// storedOfType returns persisted identities whose meta type equals typ.
func (e *u1Env) storedOfType(t *testing.T, typ string) map[string]u1Stored {
	t.Helper()
	out := map[string]u1Stored{}
	for name, rec := range e.storedSessions(t) {
		if rec.doc != nil && rec.doc["type"] == typ {
			out[name] = rec
		}
	}
	return out
}

// u1MetaBearingDirs returns the sorted names of every store directory that
// carries a parseable session record (meta.json), across the shared and
// per-agent stores. It excludes the store's own non-session directories (e.g.
// ".context", which has no meta.json) — so it is the exact set of PERSISTED
// SESSION IDENTITIES of ANY type. Used to prove a refusal stored nothing: an
// unexpected entry means a directory or replacement identity was minted.
func u1MetaBearingDirs(t *testing.T, env *u1Env) []string {
	t.Helper()
	var out []string
	for name, rec := range env.storedSessions(t) {
		if rec.doc != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// u1MainID derives the computed main-session id per the founder ruling on the
// id format (Q1=B, 2026-10-08): the literal prefix "main-session-", the
// workspace ID, a "+" separator, then the agent ID. The "+" join (not "-") is
// load-bearing: it keeps the ids of two distinct (workspace, agent) pairs
// distinct (see the collision guard in sessioncore_u1_main_identity_test.go).
func u1MainID(workspaceID, agentID string) string {
	return "main-session-" + workspaceID + "+" + agentID
}

func u1Prefixed(m map[string]u1Stored, prefix string) []string {
	var out []string
	for name := range m {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out
}
