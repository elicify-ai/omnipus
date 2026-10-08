// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: main session identity over the REST surface.
//
// Spec: docs/internal/specs/session-core-spec.md FR-002; BDD-01.1; C-MAIN.
// Expected IDs are the founder-ruled literal `main-session-<workspaceid>+<agentid>`
// (Q1=B, 2026-10-08): "main-session-" prefix, "+" separator.
// Trigger seam: PUT/POST workspace membership (rest_workspaces.go::
// prepareWorkspacePutMembers is the named E-MAIN seam). Persisted identities are
// counted on disk, not inferred from call counts.

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func u1Sorted(m map[string]u1Stored) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BDD-01.1 / FR-002: Mia on two workspaces (one default, one not), heartbeat
// off, gets exactly one stored main per workspace; Jim likewise; an existing
// extra chat is untouched. Spec literal IDs.
func TestSessionCoreU1_EligibleMembersGetExactlyOneMainPerWorkspaceHeartbeatOff(t *testing.T) {
	env := u1NewEnv(t, false)
	const wsDefault, wsOther = "01JU1DEFAULTWS00000000001", "01JU1OTHERWS000000000002"
	env.seedWorkspace(t, wsDefault, true, "jim")
	env.seedWorkspace(t, wsOther, false, "jim")

	// Existing extra chat of Mia in the default workspace: must stay as it is.
	store := env.store(t)
	extra, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ws := wsDefault
	require.NoError(t, store.SetMeta(extra.ID, session.MetaPatch{WorkspaceID: &ws}))
	extraBefore, err := os.ReadFile(filepath.Join(store.BaseDir(), extra.ID, "meta.json"))
	require.NoError(t, err)

	for _, id := range []string{wsDefault, wsOther} {
		w := env.putTeam(t, id, "jim", "mia")
		require.Equal(t, http.StatusOK, w.Code, "PUT team (control): %s", w.Body.String())
	}

	want := []string{
		u1MainID(wsDefault, "jim"), u1MainID(wsDefault, "mia"),
		u1MainID(wsOther, "jim"), u1MainID(wsOther, "mia"),
	}
	sort.Strings(want)
	assert.Equal(t, want, u1Sorted(env.storedOfType(t, "main")),
		"exactly one persisted main per eligible (workspace, agent) pair, spec literal IDs")

	for _, pair := range [][2]string{{wsDefault, "mia"}, {wsOther, "mia"}, {wsDefault, "jim"}, {wsOther, "jim"}} {
		id := u1MainID(pair[0], pair[1])
		sess, code := env.getSession(t, id)
		require.Equal(t, http.StatusOK, code, "GET %s", id)
		assert.Equal(t, id, sess["id"])
		assert.Equal(t, "main", sess["type"], "C-MAIN: Session.type=main")
		assert.Equal(t, pair[1], sess["agent_id"], "immutable owner")
		assert.Equal(t, pair[0], sess["workspace_id"], "immutable workspace")
		assert.Equal(t, true, sess["protected"], "C-MAIN: protected independent of heartbeat enablement (heartbeat is off)")

		mc := u1MemberConfig(env.getWorkspace(t, pair[0]), pair[1])
		require.NotNil(t, mc, "member_configs[%s] must exist for an eligible member", pair[1])
		assert.Equal(t, id, mc["main_session_id"], "C-MAIN: WorkspaceMemberConfig.main_session_id")
		if hb, _ := mc["heartbeat"].(map[string]any); hb != nil {
			assert.NotEqual(t, true, hb["enabled"], "fixture: heartbeat stays off")
		}
	}

	// Existing extra chat unchanged (control: passes at baseline).
	extraAfter, err := os.ReadFile(filepath.Join(store.BaseDir(), extra.ID, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, string(extraBefore), string(extraAfter), "control: existing extra chat meta unchanged")
	got, code := env.getSession(t, extra.ID)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "chat", got["type"])
	assert.NotEqual(t, true, got["protected"], "an extra chat is never protected")

	// Idempotence: re-submitting the same team neither replaces nor duplicates.
	first, _ := env.getSession(t, u1MainID(wsDefault, "mia"))
	require.Equal(t, http.StatusOK, env.putTeam(t, wsDefault, "jim", "mia").Code)
	assert.Equal(t, want, u1Sorted(env.storedOfType(t, "main")), "re-PUT must not add or replace identities")
	again, code := env.getSession(t, u1MainID(wsDefault, "mia"))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, first["created_at"], again["created_at"], "same identity, not recreated")
}

// BDD-01.1 via workspace creation: POST /workspaces with an explicit core_team.
func TestSessionCoreU1_WorkspaceCreateWithTeamCreatesTheMain(t *testing.T) {
	env := u1NewEnv(t, false)
	body := bytes.NewReader([]byte(`{"name":"Created","core_team":["mia"]}`))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", body)
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/workspaces"
	env.api.HandleWorkspaces(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "POST workspace (control): %s", w.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	wsID, _ := created["id"].(string)
	require.NotEmpty(t, wsID)

	sess, code := env.getSession(t, u1MainID(wsID, "mia"))
	require.Equal(t, http.StatusOK, code, "member of a newly created workspace gets a main")
	assert.Equal(t, "main", sess["type"])
	assert.Equal(t, 1, len(env.storedOfType(t, "main")))
}

// FR-002: workers and other system agents have no main; the eligible member in
// the same call does (positive companion so the negative cannot pass vacuously).
func TestSessionCoreU1_WorkersAndSystemAgentsHaveNoMain(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JU1WORKERWS00000000003"
	env.seedWorkspace(t, ws, false, "jim")

	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia", "worker1").Code,
		"control: a worker may sit on the team")
	_, code := env.getSession(t, u1MainID(ws, "mia"))
	require.Equal(t, http.StatusOK, code, "positive companion: Mia (eligible) has a main")

	_, code = env.getSession(t, u1MainID(ws, "worker1"))
	assert.Equal(t, http.StatusNotFound, code, "worker has no main")
	assert.Empty(t, u1Prefixed(env.storedSessions(t), u1MainID(ws, "worker1")), "no worker identity persisted")
	if mc := u1MemberConfig(env.getWorkspace(t, ws), "worker1"); mc != nil {
		_, has := mc["main_session_id"]
		assert.False(t, has, "C-MAIN: main_session_id absent for workers")
	}

	// A hidden system agent cannot even be a member (existing 400), and has no main.
	w := env.putTeam(t, ws, "jim", "mia", "worker1", "judge")
	assert.Equal(t, http.StatusBadRequest, w.Code, "control: system agent refused as member")
	_, code = env.getSession(t, u1MainID(ws, "judge"))
	assert.Equal(t, http.StatusNotFound, code)
	assert.Empty(t, u1Prefixed(env.storedSessions(t), u1MainID(ws, "judge")))
	assert.Equal(t, []string{u1MainID(ws, "jim"), u1MainID(ws, "mia")}, u1Sorted(env.storedOfType(t, "main")),
		"only the two eligible members have a main")
}

// FR-002 / BDD-01.1 Admin variant: Admin has a main in the DEFAULT workspace
// only, shown through Session list/detail, with no fake team membership.
func TestSessionCoreU1_AdminHasDefaultWorkspaceMainOnly(t *testing.T) {
	env := u1NewEnv(t, false)
	const wsDefault, wsOther = "01JU1ADMINDEFAULTWS0000004", "01JU1ADMINOTHERWS00000005"
	env.seedWorkspace(t, wsDefault, true, "mia")
	env.seedWorkspace(t, wsOther, false, "mia")

	id := u1MainID(wsDefault, "admin")
	sess, code := env.getSession(t, id)
	require.Equal(t, http.StatusOK, code, "Admin default-workspace main is reachable through Session detail")
	assert.Equal(t, "main", sess["type"])
	assert.Equal(t, "admin", sess["agent_id"])
	assert.Equal(t, wsDefault, sess["workspace_id"])
	assert.Equal(t, true, sess["protected"])
	require.NotNil(t, u1Row(env.listSessions(t, ""), id), "Admin main is listed through Session list")

	// Not outside the default workspace.
	_, code = env.getSession(t, u1MainID(wsOther, "admin"))
	assert.Equal(t, http.StatusNotFound, code, "no Admin main in a non-default workspace")
	assert.Empty(t, u1Prefixed(env.storedSessions(t), u1MainID(wsOther, "admin")))
	assert.Nil(t, u1Row(env.listSessions(t, ""), u1MainID(wsOther, "admin")))

	// No fake membership (control: passes at baseline).
	for _, wsID := range []string{wsDefault, wsOther} {
		w := env.getWorkspace(t, wsID)
		team, _ := w["core_team"].([]any)
		assert.NotContains(t, team, "admin", "Admin never appears as a team member")
		assert.Nil(t, u1MemberConfig(w, "admin"), "no member_configs entry for Admin")
	}
}

// BDD-01.1 concurrency: many concurrent writers racing the same workspace and
// several workspaces for the same agent end with exactly one stored main per
// pair. Race-safety is judged by CI's -race leg.
func TestSessionCoreU1_ConcurrentMembershipWritesStoreOneMainPerPair(t *testing.T) {
	env := u1NewEnv(t, false)
	const racing = "01JU1RACEWS00000000000006"
	env.seedWorkspace(t, racing, false, "jim")
	others := []string{"01JU1RACEWSA0000000000007", "01JU1RACEWSB0000000000008", "01JU1RACEWSC0000000000009"}
	for _, id := range others {
		env.seedWorkspace(t, id, false, "jim")
	}

	// Pre-read revisions on the test goroutine; PUTs race afterwards.
	var wg sync.WaitGroup
	start := make(chan struct{})
	bodies := map[string][]byte{}
	rev := func(id string) string {
		w := env.getWorkspace(t, id)
		r, _ := w["revision"].(string)
		require.NotEmpty(t, r)
		return r
	}
	for _, id := range append([]string{racing}, others...) {
		b, err := json.Marshal(map[string]any{"revision": rev(id), "core_team": []string{"jim", "mia"}})
		require.NoError(t, err)
		bodies[id] = b
	}
	do := func(id string) {
		defer wg.Done()
		<-start
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id, bytes.NewReader(bodies[id]))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/workspaces/" + id
		env.api.HandleWorkspaces(w, r)
	}
	for i := 0; i < 8; i++ { // eight writers, same workspace, same stale-or-fresh revision
		wg.Add(1)
		go do(racing)
	}
	for _, id := range others {
		wg.Add(1)
		go do(id)
	}
	close(start)
	wg.Wait()

	want := []string{
		u1MainID(racing, "jim"), u1MainID(racing, "mia"),
	}
	for _, id := range others {
		want = append(want, u1MainID(id, "jim"), u1MainID(id, "mia"))
	}
	sort.Strings(want)
	assert.Equal(t, want, u1Sorted(env.storedOfType(t, "main")),
		"exactly one stored main per pair after the race (no duplicates, none missing)")
}

// BDD-01.1 longest VALID pair under the 255-byte cap. The computed id is
//
//	13 ("main-session-") + len(workspace) + 1 ("+") + len(agent)
//
// so the byte cap forces len(workspace) + len(agent) = 241 for a 255-byte id.
// It is NOT two 128s (128 + 128 = 256 parts sum > 241 → a 270-byte id, over the
// cap). The longest valid pair keeps the workspace at its own 128-char bound
// (rest_workspaces.go::validWorkspaceID) and the agent at the rest, 113 chars
// (agentstore.ValidateAgentID) — each part legal under its own 128 cap, the
// computed id exactly at the 255-byte limit of the store's directory-per-session
// layout.
func TestSessionCoreU1_LongestValidPairViaMembershipGetsTheLiteralID(t *testing.T) {
	longWS := "w" + string(bytes.Repeat([]byte("b"), 127))    // 128 chars
	longAgent := "a" + string(bytes.Repeat([]byte("c"), 112)) // 113 chars
	require.True(t, validWorkspaceID(longWS), "precondition: 128-char workspace ID is valid today")
	require.Len(t, longWS, 128)
	require.NoError(t, agentstore.ValidateAgentID(longAgent), "precondition: 113-char agent ID is valid")
	require.Len(t, longAgent, 113)
	require.False(t, validWorkspaceID(longWS+"x"), "precondition: 129 chars is the first invalid length")
	require.Len(t, u1MainID(longWS, longAgent), 255,
		"derivation: 13 + 128 + 1 + 113 = 255, exactly at the byte cap")

	env := u1NewEnv(t, false, config.AgentConfig{ID: longAgent, Name: "Long", Type: config.AgentTypeCustom})
	env.seedWorkspace(t, longWS, false, "jim")
	w := env.putTeam(t, longWS, "jim", longAgent)
	require.Equal(t, http.StatusOK, w.Code, "PUT with longest valid IDs: %s", w.Body.String())

	id := u1MainID(longWS, longAgent)
	sess, code := env.getSession(t, id)
	require.Equal(t, http.StatusOK, code, "longest valid pair has a main")
	assert.Equal(t, id, sess["id"])
	assert.Equal(t, longAgent, sess["agent_id"])
	assert.Equal(t, longWS, sess["workspace_id"])
}

// FR-002 / C-MAIN boundary (max+1): a pair whose computed id is 256 bytes
// (13 + len(workspace) + 1 + len(agent) = 256 → parts sum = 242, e.g. 128 + 114)
// must be REFUSED with a visible error. Each part is still legal under its own
// 128 cap, so the refusal can only be about the computed-id length: nothing may
// be stored — no directory, no replacement id, no main for the pair.
func TestSessionCoreU1_ComputedIDOverTheByteCapIsRefused(t *testing.T) {
	longWS := "w" + string(bytes.Repeat([]byte("b"), 127))    // 128 chars
	overAgent := "a" + string(bytes.Repeat([]byte("c"), 113)) // 114 chars
	require.True(t, validWorkspaceID(longWS), "precondition: 128-char workspace ID is valid today")
	require.NoError(t, agentstore.ValidateAgentID(overAgent), "precondition: 114-char agent ID is valid on its own")
	require.Len(t, longWS, 128)
	require.Len(t, overAgent, 114)
	require.Len(t, u1MainID(longWS, overAgent), 256,
		"derivation: 13 + 128 + 1 + 114 = 256, one byte over the cap")

	env := u1NewEnv(t, false, config.AgentConfig{ID: overAgent, Name: "TooLong", Type: config.AgentTypeCustom})
	env.seedWorkspace(t, longWS, false, "jim")

	// A healthy pair on the same workspace gives the refusal a positive control.
	require.Equal(t, http.StatusOK, env.putTeam(t, longWS, "jim").Code, "control: add Jim")
	_, code := env.getSession(t, u1MainID(longWS, "jim"))
	require.Equal(t, http.StatusOK, code, "positive companion: Jim's main exists")

	w := env.putTeam(t, longWS, "jim", overAgent)
	assert.GreaterOrEqual(t, w.Code, http.StatusBadRequest, "a 256-byte computed id must be a visible client error, got %d: %s", w.Code, w.Body.String())
	assert.Less(t, w.Code, http.StatusInternalServerError, "and not a server fault: %s", w.Body.String())

	overID := u1MainID(longWS, overAgent)
	assert.Empty(t, u1Prefixed(env.storedSessions(t), overID), "no directory is created for the over-cap pair")
	assert.Empty(t, u1Prefixed(env.storedSessions(t), "main-session-"+longWS+"+"),
		"no replacement identity is minted for the over-cap pair")
	assert.Nil(t, u1Row(env.listSessions(t, ""), overID), "the over-cap main is never listed")
	if mc := u1MemberConfig(env.getWorkspace(t, longWS), overAgent); mc != nil {
		assert.Empty(t, mc["main_session_id"], "no main address is exposed for the over-cap pair")
	}
	// The healthy main is untouched by the refusal.
	_, code = env.getSession(t, u1MainID(longWS, "jim"))
	assert.Equal(t, http.StatusOK, code, "the healthy main keeps working")
	assert.Equal(t, []string{u1MainID(longWS, "jim")}, u1Sorted(env.storedOfType(t, "main")),
		"only the healthy pair has a main; the over-cap pair stored nothing")
}

// Id-format ruling (Q1=B): the "+" join keeps two distinct (workspace, agent)
// pairs distinct. Without it, pair ("a-b", "c") and pair ("a", "b-c") would both
// stringify to "main-session-a-b-c" under a hyphen join; the "+" join yields
// "main-session-a-b+c" and "main-session-a+b-c", so both mains coexist.
func TestSessionCoreU1_DistinctPairsNeverCollideOnOneID(t *testing.T) {
	env := u1NewEnv(t, false,
		config.AgentConfig{ID: "c", Name: "C", Type: config.AgentTypeCustom},
		config.AgentConfig{ID: "b-c", Name: "BC", Type: config.AgentTypeCustom})
	env.seedWorkspace(t, "a-b", false, "jim")
	env.seedWorkspace(t, "a", false, "jim")

	require.Equal(t, http.StatusOK, env.putTeam(t, "a-b", "jim", "c").Code, "control: add C to workspace a-b")
	require.Equal(t, http.StatusOK, env.putTeam(t, "a", "jim", "b-c").Code, "control: add BC to workspace a")

	id1 := u1MainID("a-b", "c") // main-session-a-b+c
	id2 := u1MainID("a", "b-c") // main-session-a+b-c
	require.NotEqual(t, id1, id2, "distinct pairs must not share one id (a hyphen join would collide)")
	assert.Equal(t, "main-session-a-b+c", id1)
	assert.Equal(t, "main-session-a+b-c", id2)

	stored := u1Sorted(env.storedOfType(t, "main"))
	assert.Contains(t, stored, id1, "pair (a-b, c) has its own main")
	assert.Contains(t, stored, id2, "pair (a, b-c) has its own main")
	assert.Len(t, stored, 2, "exactly two distinct mains, one per pair")
	_, c1 := env.getSession(t, id1)
	assert.Equal(t, http.StatusOK, c1, "pair (a-b, c) main is reachable")
	_, c2 := env.getSession(t, id2)
	assert.Equal(t, http.StatusOK, c2, "pair (a, b-c) main is reachable")
}
