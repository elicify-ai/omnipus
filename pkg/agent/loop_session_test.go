// loop_session_test.go: tests for channel session index and listing

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestRebuildChannelSessionIndex_RestoresSessionIDs verifies that after a restart,
// rebuildChannelSessionIndex re-populates channelSessionIdx from disk so that
// resolveOrCreateChannelSession returns the SAME session ID, not a new one.
//
// BDD: Given a shared session for (telegram, user-1) already exists on disk,
// When a new AgentLoop is created (simulating restart) and rebuildChannelSessionIndex is called,
// Then resolveOrCreateChannelSession("telegram", "user-1", ...) returns the existing session ID,
// AND ListSessions returns exactly 1 session (no duplicate created).
//
// Traces to: pkg/agent/loop.go rebuildChannelSessionIndex + resolveOrCreateChannelSession
//
//	(channel session routing feature)
func TestRebuildChannelSessionIndex_RestoresSessionIDs(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	// Pre-create a channel session in the shared store (simulates a previous run).
	meta, err := store.NewChannelSession("telegram", "telegram", "user-1", "agent-1", "Alice")
	require.NoError(t, err)
	sessionID1 := meta.ID

	// Simulate a restart: clear the in-memory index and rebuild from disk.
	al.channelSessionIdx = sync.Map{}
	al.rebuildChannelSessionIndex()

	// resolveOrCreateChannelSession must return the existing session ID (not create a new one).
	resolved := al.resolveOrCreateChannelSession("telegram", "telegram", "user-1", "agent-1", "Alice", "")
	assert.Equal(t, sessionID1, resolved,
		"after rebuildChannelSessionIndex, resolveOrCreateChannelSession must return the existing session ID")

	// ListSessions must still return exactly 1 session (no duplicate).
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "rebuildChannelSessionIndex must not create a duplicate session")
}

// TestResolveOrCreateChannelSession_DeduplicatesSamePeer verifies that two calls
// with the same channel/chatID return the same session ID (no duplicate sessions).
//
// BDD: Given an empty sharedSessionStore,
// When resolveOrCreateChannelSession("discord", "peer-9", ...) is called twice,
// Then both calls return the same session ID,
// AND ListSessions returns exactly 1 session.
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession (channel session routing feature)
func TestResolveOrCreateChannelSession_DeduplicatesSamePeer(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	id1 := al.resolveOrCreateChannelSession("discord", "discord", "peer-9", "agent-1", "Bob", "")
	require.NotEmpty(t, id1, "first call must return a non-empty session ID")

	id2 := al.resolveOrCreateChannelSession("discord", "discord", "peer-9", "agent-1", "Bob", "")
	assert.Equal(t, id1, id2,
		"second call with same channel/chatID must return the same session ID (dedup)")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "two calls for the same peer must result in exactly 1 session")
}

// TestResolveOrCreateChannelSession_DifferentPeersGetDifferentSessions verifies that
// different chatIDs on the same channel produce different session IDs.
//
// This is the differentiation test: different inputs → different outputs.
//
// BDD: Given an empty sharedSessionStore,
// When resolveOrCreateChannelSession is called with peer-A and then with peer-B,
// Then the two returned session IDs are different.
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession (channel session routing feature)
func TestResolveOrCreateChannelSession_DifferentPeersGetDifferentSessions(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	idA := al.resolveOrCreateChannelSession("telegram", "telegram", "peer-A", "agent-1", "Alice", "")
	idB := al.resolveOrCreateChannelSession("telegram", "telegram", "peer-B", "agent-1", "Bob", "")

	require.NotEmpty(t, idA, "peer-A must get a session ID")
	require.NotEmpty(t, idB, "peer-B must get a session ID")
	assert.NotEqual(t, idA, idB, "different peers must get different session IDs")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2, "two different peers must produce exactly 2 sessions")
}

// TestResolveOrCreateChannelSession_EmptyChatIDReturnsEmpty verifies the guard:
// when chatID is empty, resolveOrCreateChannelSession returns "" and creates no session.
//
// BDD: Given an empty sharedSessionStore,
// When resolveOrCreateChannelSession("discord", "", ...) is called,
// Then "" is returned and no session is created.
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession early-return guard
//
//	(channel session routing feature)
func TestResolveOrCreateChannelSession_EmptyChatIDReturnsEmpty(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	result := al.resolveOrCreateChannelSession("discord", "discord", "", "agent-1", "NoName", "")
	assert.Equal(t, "", result, "empty chatID must cause resolveOrCreateChannelSession to return ''")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions, "empty chatID must not create any session")
}

// TestResolveOrCreateChannelSession_TitleFallbackToChatID verifies that when
// displayName is empty, the session title falls back to chatID.
//
// BDD: Given displayName is "",
// When resolveOrCreateChannelSession("slack", "room-99", "agent-1", "") is called,
// Then the session title stored in the shared store equals "room-99".
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession title fallback
//
//	(channel session routing feature)
func TestResolveOrCreateChannelSession_TitleFallbackToChatID(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	sessionID := al.resolveOrCreateChannelSession("slack", "slack", "room-99", "agent-1", "", "")
	require.NotEmpty(t, sessionID, "must return a session ID even when displayName is empty")

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, "room-99", meta.Title,
		"when displayName is empty, title must fall back to chatID ('room-99')")
}

// TestListAllSessions_PartialErrors verifies that ListAllSessions returns the
// sessions it could read along with per-agent errors for agents whose store is
// broken, rather than swallowing errors silently or failing entirely.
//
// BDD: Given two agents — one with a valid session store, one whose base
//
//	directory cannot be listed (permissions revoked) —
//	When ListAllSessions is called,
//	Then the returned slice contains the valid agent's session,
//	And the error slice contains exactly one error for the broken agent.
func TestListAllSessions_PartialErrors(t *testing.T) {
	if os.Getuid() == 0 {
		// Root bypasses DAC (Discretionary Access Control), so chmod 0o000 on
		// a directory does not prevent os.ReadDir from root. The failure-injection
		// this test relies on only works for non-privileged users.
		t.Skip("permission-based failure injection is ineffective under root; run as non-root")
	}
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				// No "main" sentinel to fall back to anymore — this is now
				// an ordinary, explicitly-registered agent named "main"
				// purely to pair with this test's pre-existing "agent-good"
				// naming below (which — confusingly — gets the BROKEN
				// store; "main" gets the good one).
				{
					ID:   "main",
					Name: "Main Agent",
					Home: tmpDir,
				},
				{
					ID:   "agent-good",
					Name: "Good Agent",
					Home: tmpDir,
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// Wire a valid UnifiedStore for the "main" agent and create one session.
	goodStore, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore(main): %v", err)
	}
	if _, sessErr := goodStore.NewSession(session.SessionTypeChat, "webchat", "main"); sessErr != nil {
		t.Fatalf("NewSession: %v", sessErr)
	}
	mainAgent, ok := al.GetRegistry().GetAgent("main")
	if !ok {
		t.Fatal("main agent not found in registry")
	}
	mainAgent.Sessions = goodStore

	// Wire a broken UnifiedStore for "agent-good": create the store then remove
	// its base directory so ListSessions fails.
	brokenBaseDir := t.TempDir()
	brokenStore, err := session.NewUnifiedStore(brokenBaseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore(agent-good): %v", err)
	}
	// Remove all read permission on the base dir so os.ReadDir fails.
	if err := os.Chmod(brokenBaseDir, 0o000); err != nil {
		t.Fatalf("chmod brokenBaseDir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(brokenBaseDir, 0o700) }) // restore for temp-dir cleanup

	goodAgent, ok := al.GetRegistry().GetAgent("agent-good")
	if !ok {
		t.Fatal("agent-good not found in registry")
	}
	goodAgent.Sessions = brokenStore

	// Call ListAllSessions; expect one session (from main) and one error (from agent-good).
	//
	// ADR-057 FR-098/W16b (U9) gave this method a pagination + hierarchy
	// signature; this pre-existing (non-ADR-057-owned) test is updated here
	// to the new (limit, offset, parentSessionID, flat) shape rather than
	// left broken — see this unit's dispatch report for why: it is the
	// mechanical, unavoidable consequence of the exclusive-owned signature
	// change, not an edit to another unit's file. limit=0/offset=0/flat=true
	// reproduces this test's original "list everything, unfiltered by
	// hierarchy" intent byte-for-byte.
	page, errs := al.ListAllSessions(0, 0, "", true)

	if len(page.Sessions) != 1 {
		t.Errorf("expected 1 session from good store, got %d", len(page.Sessions))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 partial error from broken store, got %d", len(errs))
	}
	if len(errs) > 0 {
		errMsg := errs[0].Error()
		if errMsg == "" {
			t.Error("partial error message must not be empty")
		}
	}
}

// ---------------------------------------------------------------------
// W16b — ListAllSessions pagination, ordering, hierarchy
// (FR-091/FR-097/FR-098/FR-104)
// ---------------------------------------------------------------------

// TestListAllSessions_PagesStablyAcrossSharedStore is this unit's positive
// lower bound plus window/stability pin for FR-098(a)/(b), mirroring
// pkg/session's TestListSessionsPage_WindowAndStability (U6, store layer) at
// the layer that merges across stores.
func TestListAllSessions_PagesStablyAcrossSharedStore(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	store := u9IsolateSharedStore(t, al)

	const n = 5
	for i := 0; i < n; i++ {
		_, err := store.NewSession(session.SessionTypeChat, "", "u9-page-agent")
		require.NoError(t, err)
	}

	full, errs := al.ListAllSessions(0, 0, "", false)
	require.Empty(t, errs)
	require.Len(t, full.Sessions, n, "positive lower bound: all N sessions must be listed before paging them")
	assert.Equal(t, -1, full.NextOffset, "an unlimited page must report no further offset")
	assert.Equal(t, n, full.Total)

	page1, errs := al.ListAllSessions(2, 0, "", false)
	require.Empty(t, errs)
	require.Len(t, page1.Sessions, 2)
	assert.Equal(t, 2, page1.NextOffset)
	assert.Equal(t, n, page1.Total)

	page2, errs := al.ListAllSessions(2, page1.NextOffset, "", false)
	require.Empty(t, errs)
	require.Len(t, page2.Sessions, 2)
	assert.Equal(t, 4, page2.NextOffset)

	page3, errs := al.ListAllSessions(2, page2.NextOffset, "", false)
	require.Empty(t, errs)
	require.Len(t, page3.Sessions, 1)
	assert.Equal(t, -1, page3.NextOffset, "the final page must report no further offset")

	// Stability (FR-098(b)): concatenating the pages, in order, must
	// reproduce the SAME sequence of ids the unpaged call returned — no
	// duplicate, no skip, no reorder — across independent calls with no
	// intervening write.
	paged := make([]string, 0, len(page1.Sessions)+len(page2.Sessions)+len(page3.Sessions))
	for _, m := range page1.Sessions {
		paged = append(paged, m.ID)
	}
	for _, m := range page2.Sessions {
		paged = append(paged, m.ID)
	}
	for _, m := range page3.Sessions {
		paged = append(paged, m.ID)
	}
	unpaged := make([]string, 0, len(full.Sessions))
	for _, m := range full.Sessions {
		unpaged = append(unpaged, m.ID)
	}
	assert.Equal(t, unpaged, paged, "concatenated pages must reproduce the unpaged sequence exactly")

	// Out-of-range offset (FR-098(b)): empty, non-nil page, NextOffset==-1,
	// not an error.
	oor, errs := al.ListAllSessions(2, 100, "", false)
	require.Empty(t, errs)
	assert.NotNil(t, oor.Sessions, "an out-of-range offset must return a non-nil, empty page")
	assert.Empty(t, oor.Sessions)
	assert.Equal(t, -1, oor.NextOffset)
}

// TestU9SessionRecencyLess_TiesBreakOnID pins FR-098(a)'s exact comparator:
// UpdatedAt descending, id ascending as the tiebreak for two sessions
// sharing a timestamp — the case a real filesystem clock cannot reliably
// reproduce on demand, so the comparator is tested directly against
// hand-built (but real-typed, non-spy) session.UnifiedMeta values carrying
// an EXACT tie, exactly mirroring UnifiedStore.ListSessions' own already-
// tested comparator shape (pkg/session/unified.go) at the layer that merges
// across stores.
func TestU9SessionRecencyLess_TiesBreakOnID(t *testing.T) {
	now := time.Now()
	a := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "u9-tie-aaa", UpdatedAt: now}}
	b := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "u9-tie-bbb", UpdatedAt: now}}
	require.True(t, a.UpdatedAt.Equal(b.UpdatedAt), "fixture defect: timestamps must be an exact tie")
	require.NotEqual(t, a.ID, b.ID, "fixture defect: ids must be distinct")

	assert.True(t, u9SessionRecencyLess(a, b), "on a tie, the lexicographically smaller id must sort first")
	assert.False(t, u9SessionRecencyLess(b, a), "the comparator must be antisymmetric on a tie")

	newer := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "u9-tie-zzz", UpdatedAt: now.Add(time.Second)}}
	assert.True(t, u9SessionRecencyLess(newer, a),
		"a strictly newer UpdatedAt must sort first regardless of id ordering")
}

// TestListAllSessions_HierarchyRootsOrphansAndParentFilter is this unit's
// pin for FR-091/FR-104's three hierarchy cases u9FilterSessionHierarchy
// implements.
func TestListAllSessions_HierarchyRootsOrphansAndParentFilter(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()
	store := u9IsolateSharedStore(t, al)

	root, err := store.NewSession(session.SessionTypeChat, "", "u9-hier-agent")
	require.NoError(t, err)
	child, err := store.CreateSessionWithID("u9-hier-child-distinct", root.ID, session.SessionTypeDelegate, "", "u9-hier-agent")
	require.NoError(t, err)
	require.NotEqual(t, root.ID, child.ID, "fixture defect: root and child ids must be distinct")
	// FINDING (verified 2026-08-03, not this unit's file to fix):
	// CreateSessionWithID (pkg/session/unified_api.go, U2) takes parentID as
	// a parameter but only uses it to inherit Owner (FR-006/FR-082) — it
	// never persists ParentSessionID on the child, despite FR-005/FR-008
	// requiring every delegated child to carry it. `rg ParentSessionID
	// pkg/session/unified_api.go` returns zero matches. Until U7's spawnSubTurn
	// (Wave F) call site — or U2 itself — closes this gap, a real caller must
	// make this follow-up SetMeta call explicitly, exactly as done here, for
	// FR-091's roots/children split to see the edge at all. Flagged in this
	// unit's dispatch report for U7/U2/main; pkg/session/unified_api.go is
	// U2's exclusive file, not touched here.
	parentID := root.ID
	require.NoError(t, store.SetMeta(child.ID, session.MetaPatch{ParentSessionID: &parentID}))

	// An orphan: ParentSessionID names a session absent from the whole merge
	// (FR-091's "no longer resolves" clause).
	orphan, err := store.NewSession(session.SessionTypeDelegate, "", "u9-hier-agent")
	require.NoError(t, err)
	missingParent := "u9-hier-vanished-parent-" + orphan.ID
	require.NoError(t, store.SetMeta(orphan.ID, session.MetaPatch{ParentSessionID: &missingParent}))

	// Positive lower bound: all three are genuinely present in the merge
	// before any hierarchy filtering is asserted.
	flatPage, errs := al.ListAllSessions(0, 0, "", true)
	require.Empty(t, errs)
	require.Len(t, flatPage.Sessions, 3, "flat=true must return every merged session regardless of hierarchy")

	rootsPage, errs := al.ListAllSessions(0, 0, "", false)
	require.Empty(t, errs)
	rootIDs := make([]string, 0, len(rootsPage.Sessions))
	for _, m := range rootsPage.Sessions {
		rootIDs = append(rootIDs, m.ID)
	}
	assert.Contains(t, rootIDs, root.ID, "a genuine root must be listed")
	assert.Contains(t, rootIDs, orphan.ID,
		"an orphan (unresolvable parent) must be returned AS A ROOT, not dropped (FR-091)")
	assert.NotContains(t, rootIDs, child.ID, "a real, resolvable child must NOT appear in the roots-only view")

	childrenPage, errs := al.ListAllSessions(0, 0, root.ID, false)
	require.Empty(t, errs)
	require.Len(t, childrenPage.Sessions, 1, "parent_session_id filter must return exactly the direct children")
	assert.Equal(t, child.ID, childrenPage.Sessions[0].ID)
}

// TestListAllSessions_PartialErrorDoesNotHaltPage is FR-098(c)'s dedicated
// pin: a legacy per-agent store that errors mid-merge contributes zero rows
// and is appended to errs, but the page itself — including its
// NextOffset/cursor — is unaffected. Uses the same real
// permission-revocation failure injection as the pre-existing
// TestListAllSessions_PartialErrors (list_all_sessions_test.go, updated by
// this unit for the new signature) rather than a fake store, so the error
// is genuine I/O, not a spy.
func TestListAllSessions_PartialErrorDoesNotHaltPage(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission-based failure injection is ineffective under root; run as non-root")
	}

	al, cleanup := newAL(t)
	defer cleanup()
	sharedStore := u9IsolateSharedStore(t, al)

	// One good session in the shared store.
	goodMeta, err := sharedStore.NewSession(session.SessionTypeChat, "", "u9-partial-good")
	require.NoError(t, err)

	// One broken legacy per-agent store, wired the same way
	// list_all_sessions_test.go's pre-existing coverage does.
	brokenBaseDir := t.TempDir()
	brokenStore, err := session.NewUnifiedStore(brokenBaseDir)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(brokenBaseDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(brokenBaseDir, 0o700) })

	brokenAgentID := "u9-partial-broken-agent"
	seedTestWorkspaceMembershipForIDs(t, []string{brokenAgentID})
	al.registry.mu.Lock()
	al.registry.agents[brokenAgentID] = NewAgentInstance(
		&config.AgentConfig{ID: brokenAgentID, Name: brokenAgentID},
		&al.cfg.Agents.Defaults, al.cfg, &mockProvider{},
	)
	al.registry.agents[brokenAgentID].Sessions = brokenStore
	al.registry.mu.Unlock()

	page, errs := al.ListAllSessions(0, 0, "", true)
	require.Len(t, errs, 1, "the broken legacy store must contribute exactly one partial error")
	assert.NotEmpty(t, errs[0].Error())

	ids := make([]string, 0, len(page.Sessions))
	for _, m := range page.Sessions {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, goodMeta.ID, "the good session must still be returned despite the partial error")
	assert.Equal(t, -1, page.NextOffset, "an unlimited page must still report a valid (terminal) cursor, not fail")
}

// ─── FIX 3: forgetSession eviction ─────────────────────────────────────────

// TestForgetSession_Evicts proves forgetSession removes the entry from loadedTools.
func TestForgetSession_Evicts(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-evict", []string{"create_agent", "list_agents"})
	require.True(t, al.sessionLoadedTools("sess-evict")["create_agent"],
		"create_agent must be loaded before eviction")

	al.forgetSession("sess-evict")

	after := al.sessionLoadedTools("sess-evict")
	assert.Empty(t, after, "loadedTools entry must be empty after forgetSession")
	assert.False(t, after["create_agent"], "create_agent must not be present after forgetSession")
}

// TestForgetSession_NoopOnEmpty proves forgetSession on an unknown key is a no-op.
func TestForgetSession_NoopOnEmpty(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	// Must not panic on unknown or empty key.
	al.forgetSession("nonexistent")
	al.forgetSession("")
}

// TestForgetSession_OtherSessionsUnaffected proves forgetSession only evicts
// the targeted session and leaves other sessions intact.
func TestForgetSession_OtherSessionsUnaffected(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-keep", []string{"list_agents"})
	al.markToolsLoaded("sess-drop", []string{"create_agent"})

	al.forgetSession("sess-drop")

	kept := al.sessionLoadedTools("sess-keep")
	assert.True(t, kept["list_agents"], "sess-keep must be unaffected by forgetSession(sess-drop)")
	dropped := al.sessionLoadedTools("sess-drop")
	assert.Empty(t, dropped, "sess-drop must be empty after forgetSession")
}

// TestResolveSessionStore_CorruptMeta_ReturnsStoreNotNil is the fix's core
// regression test. Before the fix, ResolveSessionStore accepted a probed
// store only on GetMeta returning err == nil, so a corrupt-but-present
// session took the IDENTICAL path as "this session lives in a different
// store (or nowhere)" — and after exhausting every probe the function
// returned nil, indistinguishable from a session that never existed. The fix
// returns the owning store immediately (after logging its own WARN) whenever
// the error is anything other than os.ErrNotExist. Uses a completely fresh
// UnifiedStore (via a freshly constructed AgentLoop) with a hand-corrupted
// meta.json written directly to disk, mirroring
// TestUnifiedStoreGetMeta_CorruptMetaJSON_ReturnsNonNotExistError above.
func TestResolveSessionStore_CorruptMeta_ReturnsStoreNotNil(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "resolve-corrupt.log")

	prevLevel := logger.GetLevel()
	t.Cleanup(logger.DisableConsole())
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("expected a shared session store on a freshly constructed AgentLoop")
	}

	const sessionID = "corrupt-meta-resolve-test"
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	if mkErr := os.MkdirAll(sessionDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	// Same hand-corrupted file shape as the GetMeta-level test above —
	// never went through writeMetaLocked's atomic-write path.
	metaPath := filepath.Join(sessionDir, "meta.json")
	if writeErr := os.WriteFile(metaPath, []byte("{not valid json"), 0o600); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}

	got := al.ResolveSessionStore(sessionID)
	if got == nil {
		t.Fatal("ResolveSessionStore must return the owning store for a corrupt-meta session, not nil — " +
			"corruption is not the same thing as 'session does not exist'")
	}
	if got != store {
		t.Fatalf("expected the shared store to be returned, got a different store (BaseDir=%q)", got.BaseDir())
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	if !strings.Contains(logged, "ResolveSessionStore") {
		t.Errorf("log file missing the ResolveSessionStore WARN marker; got:\n%s", logged)
	}
	if !strings.Contains(logged, sessionID) {
		t.Errorf("log file missing the session id %q; got:\n%s", sessionID, logged)
	}
	if !strings.Contains(logged, store.BaseDir()) {
		t.Errorf("log file missing the owning store's BaseDir %q; got:\n%s", store.BaseDir(), logged)
	}
	if !strings.Contains(logged, `"level":"warn"`) {
		t.Errorf("log file missing the warn level; got:\n%s", logged)
	}
}

// TestResolveSessionStore_MissingSession_StaysSilent is the control test:
// a session that genuinely does not exist anywhere (the frequent, legitimate
// case — e.g. a brand-new channel conversation with no session yet) must
// keep resolving to nil with NO warning logged. Over-correcting into a WARN
// on every not-yet-created session would be worse than the bug this fix
// closes.
func TestResolveSessionStore_MissingSession_StaysSilent(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "resolve-missing.log")

	prevLevel := logger.GetLevel()
	t.Cleanup(logger.DisableConsole())
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	got := al.ResolveSessionStore("session-that-truly-does-not-exist-anywhere")
	if got != nil {
		t.Fatalf("expected nil for a genuinely missing session, got a store (BaseDir=%q)", got.BaseDir())
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	if strings.Contains(logged, "ResolveSessionStore") {
		t.Errorf("expected NO WARN for a legitimately missing session (unchanged behavior), but got:\n%s", logged)
	}
}
