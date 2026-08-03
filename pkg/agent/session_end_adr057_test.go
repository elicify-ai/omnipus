// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U17b (Wave E) — W10e: CloseSession's teardown store-flush
// (FR-064) and metaCache-eviction (FR-033) coverage.
//
// Ownership Rule 6: every unexported package-level helper this file
// introduces is prefixed u17b, matching the convention U4/U5/U6 established
// in pkg/session, to rule out a silent redeclaration collision with any
// concurrently-landing unit in this package.
//
// Binding rules 1-2 (spec "Four rules bind every test in this spec"): every
// assertion below lands on real store-backed state — actual bytes read back
// from disk, or a real UnifiedStore's real in-memory cache — never a spy
// that records an invocation. Rule 4: each negative/zero assertion below is
// preceded by a positive lower bound proving the dirty/stale state it is
// about to test away is genuinely present.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// u17bNewTeardownLoop builds a minimal, real AgentLoop wired to a real
// UnifiedStore rooted at a fresh t.TempDir() (never a mock/spy). Mirrors the
// existing newSmokeLoop pattern (session_end_smoke_test.go) but also wires
// al.sharedSessionStore, which CloseSession's new store-flush/evict block
// needs and newSmokeLoop's callers never populate.
//
// AutoRecapEnabled is left false so CloseSession returns synchronously right
// after the unconditional cleanup block under test, without launching a
// recap goroutine that would need a real agent/model/memory store wired up
// too — none of which this unit's W10e scope touches.
func u17bNewTeardownLoop(t *testing.T) (*AgentLoop, *session.UnifiedStore) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = false
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// A long interval keeps the periodic flusher goroutine (started
	// unconditionally by NewUnifiedStore) from racing this test's own
	// forced-flush assertions — every flush this test observes must be the
	// one CloseSession forces, not a lucky periodic tick.
	store.SetStatsFlushInterval(time.Hour)
	al.sharedSessionStore = store

	return al, store
}

// u17bStatsJSONPath returns the on-disk path FR-053's targeted stats writer
// (u5WriteStatsLocked) uses for sessionID, so this test can read the real
// bytes a forced flush produces without going through any store accessor.
func u17bStatsJSONPath(store *session.UnifiedStore, sessionID string) string {
	return filepath.Join(store.BaseDir(), sessionID, "stats.json")
}

// u17bReadStatsMessageCount reads stats.json's raw bytes directly off disk
// and decodes only the field this test cares about. Returns (0, false) when
// the file does not exist yet — the pre-flush state for a brand-new session,
// since createSessionLocked never writes stats.json (it is created lazily by
// the first write that actually touches the Stats group).
//
// Also returns (0, false) when the path exists but is a DIRECTORY rather
// than a regular file: TestCloseSession_FlushFailure_DoesNotStrandRetryByEvictingCache
// (session_end_fix_test.go) deliberately pre-creates stats.json's own path as
// an empty directory to force a deterministic, uid-independent write failure
// (os.OpenFile(O_CREATE) on a path that is already a directory fails with
// EISDIR regardless of privilege level — unlike a chmod'd read-only
// directory, which root's CAP_DAC_OVERRIDE silently bypasses). A stray
// directory at this path is never a valid stats.json in the sense every
// caller of this helper cares about ("is there a readable message_count on
// disk"), so folding it into the same not-found branch is a genuine
// generalization, not a test-specific carve-out — no other test in this file
// ever creates a directory here, so this branch is unreachable for them.
func u17bReadStatsMessageCount(t *testing.T, store *session.UnifiedStore, sessionID string) (int, bool) {
	t.Helper()
	path := u17bStatsJSONPath(store, sessionID)
	if fi, statErr := os.Stat(path); statErr == nil && fi.IsDir() {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false
		}
		t.Fatalf("read %s: %v", path, err)
	}
	var f struct {
		MessageCount int `json:"message_count"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal %s: %v (bytes: %s)", path, err, data)
	}
	return f.MessageCount, true
}

// TestCloseSession_ForcesStatsFlushOnTeardown is the FR-064 red/green pair.
//
// RED (asserted inline, before CloseSession runs): after one AppendTranscript
// call, the in-memory cached Stats.MessageCount is 1 (positive lower bound —
// proves the dirty delta is real, not an artifact of a broken fixture) while
// stats.json does not exist on disk AT ALL — proving the FR-061 throttle
// really did keep this write in memory only, exactly the state in which a
// teardown with no forced flush would lose the counter permanently (nothing
// would ever write stats.json for a session that never appends again).
//
// GREEN: after al.CloseSession, stats.json exists on disk with
// message_count == 1 — real bytes, read back independently of any store
// accessor.
//
// Manually verified red/green (not committed, per binding rules 1-2's ban on
// invocation-spy tests): commenting out the `store.FlushSessionStats` call
// this unit added to CloseSession (pkg/agent/session_end.go) turns the GREEN
// assertion below into a failure — stats.json stays absent because nothing
// else in this test ever triggers a flush (the periodic flusher is parked at
// 1 hour and CloseSession returns before any recap path could run one).
func TestCloseSession_ForcesStatsFlushOnTeardown(t *testing.T) {
	al, store := u17bNewTeardownLoop(t)

	meta, err := store.NewSession(session.SessionTypeChat, "test", "test-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID

	if err = store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role:    "user",
		Content: "hello",
		Tokens:  42,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	// --- RED baseline: prove the dirty delta is real, then prove it is
	// genuinely absent from disk before any forced flush. ---
	preClose, err := store.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("GetMeta (pre-close): %v", err)
	}
	if preClose.Stats.MessageCount != 1 {
		t.Fatalf("positive lower bound failed: want cached MessageCount == 1 after one AppendTranscript, got %d — fixture is broken, RED/GREEN below would be meaningless",
			preClose.Stats.MessageCount)
	}
	if count, existed := u17bReadStatsMessageCount(t, store, sessionID); existed {
		t.Fatalf("RED precondition failed: stats.json already exists (message_count=%d) before any forced flush — the FR-061 throttle fixture is not exercising the code path this test needs",
			count)
	}

	// --- Act: the teardown under test. ---
	al.CloseSession(sessionID, "test-trigger")

	// --- GREEN: the forced flush must have persisted the delta to disk. ---
	count, existed := u17bReadStatsMessageCount(t, store, sessionID)
	if !existed {
		t.Fatal("FR-064 violated: stats.json still does not exist after CloseSession — the teardown did not force a flush, so the counter accumulated since the last periodic tick is lost")
	}
	if count != 1 {
		t.Fatalf("FR-064 violated: stats.json message_count = %d, want 1 — the forced flush ran but did not persist the correct delta", count)
	}
}

// TestCloseSession_EvictsMetaCacheOnTeardown is the FR-033 red/green pair.
//
// RED (asserted inline, before CloseSession runs): after mutating meta.json
// directly on disk (simulating any out-of-band divergence between disk and
// the live cache), GetMeta still returns the ORIGINAL cached Title — proving
// the cache is genuinely serving a value that differs from disk, i.e. proving
// the exact mechanism by which a closed session could go on serving stale
// meta forever if nothing ever evicts it.
//
// GREEN: after al.CloseSession, GetMeta returns the on-disk Title — proving
// the cache entry was evicted and GetMeta's cache-miss path self-healed from
// disk (readMetaLocked), never a value that was merely re-set to match.
//
// Manually verified red/green: commenting out the `store.EvictSessionMeta`
// call this unit added to CloseSession turns the GREEN assertion below into
// a failure — GetMeta keeps returning the stale pre-close Title forever,
// since nothing else in this test ever touches this session's cache entry.
func TestCloseSession_EvictsMetaCacheOnTeardown(t *testing.T) {
	al, store := u17bNewTeardownLoop(t)

	meta, err := store.NewSession(session.SessionTypeChat, "test", "test-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID

	const originalTitle = ""
	const mutatedOnDiskTitle = "mutated-out-of-band"

	// Mutate meta.json directly on disk, bypassing the store entirely, so the
	// live cache entry (still holding originalTitle from createSessionLocked)
	// diverges from disk.
	metaPath := filepath.Join(store.BaseDir(), sessionID, "meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var identity map[string]any
	if err = json.Unmarshal(raw, &identity); err != nil {
		t.Fatalf("unmarshal meta.json: %v", err)
	}
	identity["title"] = mutatedOnDiskTitle
	mutated, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		t.Fatalf("marshal mutated meta.json: %v", err)
	}
	if err = os.WriteFile(metaPath, mutated, 0o600); err != nil {
		t.Fatalf("write mutated meta.json: %v", err)
	}

	// --- RED baseline: prove the cache genuinely diverges from disk before
	// any eviction. ---
	preClose, err := store.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("GetMeta (pre-close): %v", err)
	}
	if preClose.Title != originalTitle {
		t.Fatalf("fixture broken: expected the cache to still hold the pre-mutation Title %q, got %q — this test needs the cache to be genuinely stale to prove eviction matters",
			originalTitle, preClose.Title)
	}

	// --- Act: the teardown under test. ---
	al.CloseSession(sessionID, "test-trigger")

	// --- GREEN: the cache entry must have been evicted, so GetMeta now
	// self-heals from disk and returns the mutated value. ---
	postClose, err := store.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("GetMeta (post-close): %v", err)
	}
	if postClose.Title != mutatedOnDiskTitle {
		t.Fatalf("FR-033 violated: GetMeta after CloseSession returned Title %q, want %q (the on-disk value) — the metaCache entry was not evicted, so a closed session can go on serving stale meta",
			postClose.Title, mutatedOnDiskTitle)
	}

	// Dataset row 8 (ADR-057 spec, "Session parent index"): eviction != deletion.
	// The session must still exist on disk after CloseSession — only its cache
	// entry is gone.
	if _, err := os.Stat(filepath.Join(store.BaseDir(), sessionID)); err != nil {
		t.Fatalf("FR-033/dataset-row-8 violated: session directory must survive CloseSession (eviction != deletion), stat error: %v", err)
	}
}
