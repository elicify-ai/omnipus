// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: the ONE place that depends on a symbol the spec
// does not name. The spec requires an eager create-or-reuse of
// `main-session-<workspaceid>-<agentid>` (FR-002, E-MAIN says the existing
// NewHeartbeatSession / GetOrCreateScheduledSession path "matures") but names
// no Go method. This file assumes
//
//	func (us *UnifiedStore) GetOrCreateMainSession(workspaceID, agentID string) (*UnifiedMeta, error)
//
// and reaches it by reflection, so a different final name or signature breaks
// only this file's helper (one line) and never the package's compile. While the
// method is missing every test here fails loudly with a BLOCKED message
// (RED rule: t.Fatal, never t.Skip). NEW SYMBOL: report to team-lead; replace
// the reflective call with a direct call once the real name exists.

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const u1MainMethod = "GetOrCreateMainSession"

// u1GetOrCreateMain calls the assumed method reflectively.
func u1GetOrCreateMain(t testing.TB, store *UnifiedStore, workspaceID, agentID string) (*UnifiedMeta, error) {
	t.Helper()
	m := reflect.ValueOf(store).MethodByName(u1MainMethod)
	if !m.IsValid() {
		t.Fatalf("BLOCKED: UnifiedStore.%s(workspaceID, agentID) (*UnifiedMeta, error) not implemented — required by session-core-spec FR-002 / E-MAIN", u1MainMethod)
	}
	mt := m.Type()
	if mt.NumIn() != 2 || mt.NumOut() != 2 {
		t.Fatalf("BLOCKED: UnifiedStore.%s has signature %s; this RED pack assumes (workspaceID, agentID string) (*UnifiedMeta, error) — ask qa-lead to update", u1MainMethod, mt)
	}
	out := m.Call([]reflect.Value{reflect.ValueOf(workspaceID), reflect.ValueOf(agentID)})
	var meta *UnifiedMeta
	if !out[0].IsNil() {
		var ok bool
		meta, ok = out[0].Interface().(*UnifiedMeta)
		require.True(t, ok, "first result must be *UnifiedMeta")
	}
	var err error
	if !out[1].IsNil() {
		err, _ = out[1].Interface().(error)
	}
	return meta, err
}

// u1StoredIdentities returns the raw meta.json documents on disk keyed by
// directory name (BDD-01.1: count persisted identities, not calls).
func u1StoredIdentities(t *testing.T, store *UnifiedStore) map[string]map[string]any {
	t.Helper()
	entries, err := os.ReadDir(store.BaseDir())
	require.NoError(t, err)
	out := map[string]map[string]any{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(store.BaseDir(), e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var doc map[string]any
		if json.Unmarshal(raw, &doc) == nil {
			out[e.Name()] = doc
		}
	}
	return out
}

func u1MainCount(ids map[string]map[string]any) int {
	n := 0
	for _, d := range ids {
		if d["type"] == "main" {
			n++
		}
	}
	return n
}

// longID builds a string of exactly n chars that matches the existing
// workspace/agent ID pattern ^[A-Za-z0-9]+(?:[-_.][A-Za-z0-9]+)*$ with a <=128 cap
// (pkg/gateway/rest_workspaces.go::validWorkspaceID, pkg/agentstore/state.go::ValidateAgentID).
func u1LongID(prefix string, n int) string {
	return prefix + strings.Repeat("a", n-len(prefix))
}

// BDD-01.1: concurrent eligible-main lookups produce exactly one stored
// identity per (workspace, agent) pair, with the spec's literal ID, immutable
// owner/workspace. Run under -race in CI.
func TestSessionCoreU1_ConcurrentLookupsStoreExactlyOneMainPerPair(t *testing.T) {
	store := newTestStore(t)
	pairs := [][2]string{{"W1", "mia"}, {"W2", "mia"}, {"W1", "jim"}}

	// Pre-existing extra chat for mia in W1 must be left alone (BDD-01.1 "existing extra unchanged").
	extra, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	const callersPerPair = 24
	type result struct {
		pair [2]string
		meta *UnifiedMeta
		err  error
	}
	results := make(chan result, callersPerPair*len(pairs))
	// Resolve the method once on the test goroutine so BLOCKED is a clean t.Fatal.
	_, _ = u1GetOrCreateMain(t, store, "W-probe", "probe")

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, p := range pairs {
		for i := 0; i < callersPerPair; i++ {
			wg.Add(1)
			go func(p [2]string) {
				defer wg.Done()
				<-start
				m, e := u1GetOrCreateMain(t, store, p[0], p[1])
				results <- result{p, m, e}
			}(p)
		}
	}
	close(start)
	wg.Wait()
	close(results)

	for r := range results {
		require.NoError(t, r.err, "lookup for %v", r.pair)
		require.NotNil(t, r.meta)
		want := "main-session-" + r.pair[0] + "-" + r.pair[1]
		assert.Equal(t, want, r.meta.ID, "spec FR-002 literal ID")
		assert.Equal(t, UnifiedSessionType("main"), r.meta.Type)
		assert.Equal(t, r.pair[1], r.meta.AgentID)
		assert.Equal(t, r.pair[0], r.meta.WorkspaceID)
	}

	ids := u1StoredIdentities(t, store)
	// 3 pairs + the probe pair above + 1 extra chat; the probe is a 4th pair.
	assert.Equal(t, 4, u1MainCount(ids), "exactly one stored main per pair (3 pairs + probe); got %d dirs: %v", len(ids), keys(ids))
	for _, want := range []string{"main-session-W1-mia", "main-session-W2-mia", "main-session-W1-jim"} {
		_, ok := ids[want]
		assert.True(t, ok, "stored identity %s must exist", want)
	}
	assert.Equal(t, "chat", ids[extra.ID]["type"], "existing extra chat unchanged")
	assert.Equal(t, 1+4, len(ids), "no extra identities beyond 4 mains + 1 extra chat")
}

func keys(m map[string]map[string]any) []string {
	var k []string
	for name := range m {
		k = append(k, name)
	}
	return k
}

// BDD-01.1 longest valid pair: IDs derive from existing bounds (<=128 each),
// not UUID/128 assumptions. The computed ID is then 13+128+1+128 = 270 chars.
// FINDING for team-lead: 270 exceeds the 255-byte file-name limit of common
// filesystems, and the store uses the ID as the directory name; the spec gives
// no mapping for that case (see report question). Expected literal below is the
// spec's; if the design later maps long IDs differently, this test changes with
// the spec, not with the implementation.
func TestSessionCoreU1_LongestValidPairGetsTheLiteralComputedID(t *testing.T) {
	ws := u1LongID("w", 128)
	ag := u1LongID("a", 128)
	require.Len(t, ws, 128, "boundary: max valid workspace ID length")
	require.Len(t, ag, 128, "boundary: max valid agent ID length")

	store := newTestStore(t)
	meta, err := u1GetOrCreateMain(t, store, ws, ag)
	require.NoError(t, err, "longest valid pair must be accepted (spec: bounds derive from existing validators)")
	assert.Equal(t, "main-session-"+ws+"-"+ag, meta.ID)
}

// FR-002 / BDD-01.4: invalid pair components are refused with an error and
// leave zero stored identity (no guessed replacement). Includes the 129-char
// boundary (max+1) next to the valid 128 above.
func TestSessionCoreU1_InvalidPairComponentsAreRefusedWithoutStoring(t *testing.T) {
	cases := []struct{ name, ws, ag string }{
		{"empty workspace", "", "mia"},
		{"empty agent", "W1", ""},
		{"workspace slash", "W/1", "mia"},
		{"workspace dotdot", "..", "mia"},
		{"agent backslash", "W1", `mi\a`},
		{"agent 129 chars (max+1)", "W1", u1LongID("a", 129)},
		{"workspace 129 chars (max+1)", u1LongID("w", 129), "mia"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newTestStore(t)
			meta, err := u1GetOrCreateMain(t, store, c.ws, c.ag)
			require.Error(t, err, "invalid pair must be refused")
			assert.Nil(t, meta)
			assert.Equal(t, 0, len(u1StoredIdentities(t, store)), "nothing may be stored for an invalid pair")
		})
	}
}

// BDD-01.4: a stored ID with the wrong owner is refused, not adopted, and not
// repaired; no replacement identity appears.
func TestSessionCoreU1_StoredIDWithWrongOwnerIsRefusedNotAdopted(t *testing.T) {
	store := newTestStore(t)
	const id = "main-session-W1-mia"
	seeded, err := store.GetOrCreateScheduledSession(id, "jim") // existing seam: exact-ID create, owner jim
	require.NoError(t, err)
	require.Equal(t, "jim", seeded.AgentID, "fixture: wrong-owner record")

	meta, err := u1GetOrCreateMain(t, store, "W1", "mia")
	require.Error(t, err, "mismatched stored pair must be refused")
	assert.Nil(t, meta)

	ids := u1StoredIdentities(t, store)
	assert.Equal(t, 1, len(ids), "no replacement identity; got %v", keys(ids))
	assert.Equal(t, "jim", ids[id]["agent_id"], "wrong-owner record must not be rewritten")
	assert.NotEqual(t, "main", ids[id]["type"], "record must not be silently promoted to main")
}

// BDD-01.4: unreadable/corrupt metadata is refused and left byte-for-byte.
func TestSessionCoreU1_CorruptStoredMetadataIsRefusedNotRepaired(t *testing.T) {
	store := newTestStore(t)
	dir := filepath.Join(store.BaseDir(), "main-session-W1-mia")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	corrupt := []byte("{not json")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), corrupt, 0o600))

	meta, err := u1GetOrCreateMain(t, store, "W1", "mia")
	require.Error(t, err, "corrupt metadata must be refused")
	assert.Nil(t, meta)

	after, rerr := os.ReadFile(filepath.Join(dir, "meta.json"))
	require.NoError(t, rerr)
	assert.Equal(t, corrupt, after, "corrupt record must not be overwritten (no guessing)")
	entries, _ := os.ReadDir(store.BaseDir())
	assert.Equal(t, 1, len(entries), "no replacement directory may appear")
}

// C-MAIN: Session.agent_id / workspace_id are immutable. Existing public seam:
// SetMeta with a WorkspaceID patch must be refused for a main.
func TestSessionCoreU1_MainWorkspaceAndOwnerAreImmutable(t *testing.T) {
	store := newTestStore(t)
	meta, err := u1GetOrCreateMain(t, store, "W1", "mia")
	require.NoError(t, err)

	other := "W2"
	err = store.SetMeta(meta.ID, MetaPatch{WorkspaceID: &other})
	require.Error(t, err, "C-MAIN: workspace_id is immutable on a main")

	// agent_id has no patch field at all (MetaPatch.Owner is the authenticated USER stamp).
	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, "W1", got.WorkspaceID)
	assert.Equal(t, "mia", got.AgentID)
}

// FR-003: a retained identity survives a restart and is reused, not replaced.
func TestSessionCoreU1_ReopenedStoreReusesTheSameMain(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewUnifiedStore(dir)
	require.NoError(t, err)
	first, err := u1GetOrCreateMain(t, s1, "W1", "mia")
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := NewUnifiedStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })
	second, err := u1GetOrCreateMain(t, s2, "W1", "mia")
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.True(t, first.CreatedAt.Equal(second.CreatedAt), "retained identity keeps created_at")
	assert.Equal(t, 1, u1MainCount(u1StoredIdentities(t, s2)), "still exactly one stored main")
}
