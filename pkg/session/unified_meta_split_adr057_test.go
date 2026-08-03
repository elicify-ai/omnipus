// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U5 (Wave C) — coverage for W23 (the meta.json four-file split,
// FR-053...FR-060, FR-084), the FR-103 read seam, FR-008's ParentSessionID
// round trip + FR-097 index wiring, the FR-097 "delegate" session-type
// literal, and the W11a IsDelegateChildEntry compile shim.
//
// Per binding Rule 1, every assertion below runs against a REAL
// *UnifiedStore rooted at a t.TempDir() and REAL on-disk files — AC-21
// requires assertions land on "the session directory's actual files and
// bytes", not a mock. Per Rule 4/FR-085, every negative/exclusion gate
// states and asserts its positive lower bound before its exclusion. Per the
// spec's "Corollary — distinct ids everywhere", every parent/child pair
// below is constructed as two distinct, non-equal values.
package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u5NewTestStore mirrors the package's existing newTestStore/u2NewTestStore
// helpers — a small, self-contained constructor so this file has no
// ordering dependency on any of them. Prefixed u5 per ownership Rule 6.
func u5NewTestStore(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	return store
}

// ---------------------------------------------------------------------
// Test #13: TestReadUnifiedMeta_ComposesFourFiles (BDD-58, AC-21(a))
// ---------------------------------------------------------------------

// TestReadUnifiedMeta_ComposesFourFiles exercises BDD-58: a session that has
// been created, then given one /goal set (via SetMeta), one /loop start
// (via SetMeta), and one transcript append, MUST have exactly four meta
// files on disk — meta.json, stats.json, goal.json, loop.json — and each
// MUST contain ONLY its own group's fields (AC-21(a)). This is asserted on
// the session directory's actual files and bytes, per AC-21's own text, not
// on the composed *UnifiedMeta struct (which would look identical whether
// the split is real or not).
func TestReadUnifiedMeta_ComposesFourFiles(t *testing.T) {
	store := u5NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// Immediately after create: meta.json ONLY (dataset row 2 — "a session
	// that never ran a goal"). This is the positive control proving the
	// OTHER three files are genuinely absent before the actions below, not
	// merely unchecked.
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	assertFileExists(t, filepath.Join(sessionDir, "meta.json"))
	assertFileAbsent(t, filepath.Join(sessionDir, "stats.json"))
	assertFileAbsent(t, filepath.Join(sessionDir, "goal.json"))
	assertFileAbsent(t, filepath.Join(sessionDir, "loop.json"))

	// One /goal set.
	goalCond := "ship the feature"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{GoalCondition: &goalCond}))

	// One /loop start.
	loopMode := "interval"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{LoopMode: &loopMode}))

	// One transcript append (also exercises the strict FR-002 existence
	// check succeeding for a real session).
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "hi"}))

	// ADR-057-U6-inverted: AppendTranscript's Stats bump is now throttled
	// (W24/FR-061) — it lands in the cache only, and stats.json is created
	// lazily by the first flush, not synchronously by the append itself.
	// Force that flush here so the assertion below still exercises AC-21(a)
	// ("a session directory that HAS run a transcript append holds a real
	// stats.json"), which is unaffected by whether that write is immediate
	// or deferred — only the append's own file-split correctness is under
	// test here, not W24's timing (see unified_stats_flush_adr057_test.go
	// for that).
	require.NoError(t, store.FlushSessionStats(sessionID))

	// Now all four MUST exist.
	assertFileExists(t, filepath.Join(sessionDir, "meta.json"))
	assertFileExists(t, filepath.Join(sessionDir, "stats.json"))
	assertFileExists(t, filepath.Join(sessionDir, "goal.json"))
	assertFileExists(t, filepath.Join(sessionDir, "loop.json"))

	// AC-21(a): each file contains ONLY its own group's fields. Assert by
	// decoding each file into a raw key-set and checking for absence of the
	// OTHER groups' distinguishing keys.
	metaKeys := readJSONKeys(t, filepath.Join(sessionDir, "meta.json"))
	assert.Contains(t, metaKeys, "id")
	assert.Contains(t, metaKeys, "status")
	assert.NotContains(t, metaKeys, "stats", "meta.json must not carry the stats group")
	assert.NotContains(t, metaKeys, "goal_condition", "meta.json must not carry goal fields")
	assert.NotContains(t, metaKeys, "loop_mode", "meta.json must not carry loop fields")

	statsKeys := readJSONKeys(t, filepath.Join(sessionDir, "stats.json"))
	assert.Contains(t, statsKeys, "tokens_in")
	assert.NotContains(t, statsKeys, "goal_condition")
	assert.NotContains(t, statsKeys, "loop_mode")
	assert.NotContains(t, statsKeys, "id", "stats.json must not carry identity fields")

	goalKeys := readJSONKeys(t, filepath.Join(sessionDir, "goal.json"))
	assert.Contains(t, goalKeys, "goal_condition")
	assert.NotContains(t, goalKeys, "loop_mode")
	assert.NotContains(t, goalKeys, "tokens_in")
	assert.NotContains(t, goalKeys, "id")

	loopKeys := readJSONKeys(t, filepath.Join(sessionDir, "loop.json"))
	assert.Contains(t, loopKeys, "loop_mode")
	assert.NotContains(t, loopKeys, "goal_condition")
	assert.NotContains(t, loopKeys, "tokens_in")
	assert.NotContains(t, loopKeys, "id")

	// The composed read must still see all of it.
	composed, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, goalCond, composed.GoalCondition)
	assert.Equal(t, loopMode, composed.LoopMode)
	assert.Equal(t, 1, composed.Stats.MessageCount)
}

// ---------------------------------------------------------------------
// Test #14: TestReadUnifiedMeta_MissingGroupFilesAreZeroValue (BDD-60)
// ---------------------------------------------------------------------

func TestReadUnifiedMeta_MissingGroupFilesAreZeroValue(t *testing.T) {
	store := u5NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	sessionDir := filepath.Join(store.BaseDir(), meta.ID)
	assertFileAbsent(t, filepath.Join(sessionDir, "stats.json"))
	assertFileAbsent(t, filepath.Join(sessionDir, "goal.json"))
	assertFileAbsent(t, filepath.Join(sessionDir, "loop.json"))

	composed, err := readUnifiedMeta(sessionDir)
	require.NoError(t, err, "a directory with only meta.json must load successfully")
	assert.Zero(t, composed.Stats.TokensIn)
	assert.Zero(t, composed.Stats.MessageCount)
	assert.Empty(t, composed.GoalCondition)
	assert.Empty(t, composed.LoopMode)
}

// ---------------------------------------------------------------------
// Test #15: TestReadUnifiedMeta_MissingMetaJSONIsError (BDD-61)
// ---------------------------------------------------------------------

func TestReadUnifiedMeta_MissingMetaJSONIsError(t *testing.T) {
	store := u5NewTestStore(t)
	sessionID := "no-meta-json-session"
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))

	// stats.json present, but meta.json absent — the D11 asymmetry.
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "stats.json"), []byte(`{"tokens_in":5}`), 0o600))
	assertFileAbsent(t, filepath.Join(sessionDir, "meta.json"))

	_, err := readUnifiedMeta(sessionDir)
	require.Error(t, err, "a directory with stats.json but no meta.json must be an error, not an empty session")

	// Asymmetry asserted in BOTH directions: meta.json-only IS fine (already
	// covered by TestReadUnifiedMeta_MissingGroupFilesAreZeroValue above).
}

// ---------------------------------------------------------------------
// Test #16: TestReadUnifiedMeta_CorruptGroupFileErrors (BDD-62)
// ---------------------------------------------------------------------

func TestReadUnifiedMeta_CorruptGroupFileErrors(t *testing.T) {
	store := u5NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionDir := filepath.Join(store.BaseDir(), meta.ID)

	// Present-but-truncated goal.json.
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "goal.json"), []byte(`{"goal_condition":`), 0o600))

	_, err = readUnifiedMeta(sessionDir)
	require.Error(t, err, "a present-but-corrupt goal.json must surface an error for that group")
	assert.Contains(t, err.Error(), "goal.json", "the error must name the group that failed")

	// Same rule applies to stats.json.
	require.NoError(t, os.Remove(filepath.Join(sessionDir, "goal.json")))
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "stats.json"), []byte(`{not valid`), 0o600))
	_, err = readUnifiedMeta(sessionDir)
	require.Error(t, err, "a present-but-corrupt stats.json must surface an error for that group")
	assert.Contains(t, err.Error(), "stats.json")
}

// ---------------------------------------------------------------------
// Test #17: TestMetaWriters_WriterIsolationByteLevel (BDD-59)
// ---------------------------------------------------------------------

// TestMetaWriters_WriterIsolationByteLevel is Rule 4's positive-lower-bound
// gate: it MUST first assert all 4 files exist with non-zero length and a
// DISTINCT content hash before any "unchanged" assertion — a gate that
// skipped this would pass vacuously if the split were entirely broken (e.g.
// all four files empty or identical).
func TestMetaWriters_WriterIsolationByteLevel(t *testing.T) {
	store := u5NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID
	sessionDir := filepath.Join(store.BaseDir(), sessionID)

	goalCond := "initial-goal"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{GoalCondition: &goalCond}))
	loopMode := "interval"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{LoopMode: &loopMode}))
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "seed"}))
	// ADR-057-U6-inverted: see the identical note in
	// TestReadUnifiedMeta_ComposesFourFiles above — force the W24 throttle's
	// deferred stats.json write so the positive-lower-bound check below
	// (Rule 4: all 4 files must exist before any byte-comparison) still
	// holds under the new deferred-write timing.
	require.NoError(t, store.FlushSessionStats(sessionID))

	metaPath := filepath.Join(sessionDir, "meta.json")
	statsPath := filepath.Join(sessionDir, "stats.json")
	goalPath := filepath.Join(sessionDir, "goal.json")
	loopPath := filepath.Join(sessionDir, "loop.json")

	// Positive lower bound (Rule 4): all 4 exist, non-zero length, distinct
	// content hashes.
	contents := map[string][]byte{}
	for _, p := range []string{metaPath, statsPath, goalPath, loopPath} {
		b, rErr := os.ReadFile(p)
		require.NoError(t, rErr, "file %s must exist", p)
		require.NotEmpty(t, b, "file %s must be non-empty", p)
		contents[p] = b
	}
	require.Len(t, contents, 4)
	seen := map[string]string{}
	for p, b := range contents {
		h := string(b)
		for otherPath, otherHash := range seen {
			assert.NotEqual(t, otherHash, h, "%s and %s must have distinct content", p, otherPath)
		}
		seen[p] = h
	}

	// Snapshot bytes before each targeted operation.
	snap := func() map[string][]byte {
		out := map[string][]byte{}
		for _, p := range []string{metaPath, statsPath, goalPath, loopPath} {
			b, rErr := os.ReadFile(p)
			require.NoError(t, rErr)
			out[p] = b
		}
		return out
	}

	// Row: transcript append -> goal.json and loop.json unchanged.
	before := snap()
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "assistant", Content: "reply", Tokens: 3}))
	// ADR-057-U6-inverted: force the W24 throttle's deferred stats.json
	// write so this row's "stats.json must actually change" check observes
	// the append's effect on disk rather than the still-pending in-memory
	// delta — see the note on the earlier FlushSessionStats call above.
	require.NoError(t, store.FlushSessionStats(sessionID))
	after := snap()
	assert.True(t, bytes.Equal(before[goalPath], after[goalPath]), "transcript append must leave goal.json byte-identical")
	assert.True(t, bytes.Equal(before[loopPath], after[loopPath]), "transcript append must leave loop.json byte-identical")
	assert.False(t, bytes.Equal(before[statsPath], after[statsPath]), "transcript append must actually change stats.json")

	// Row: /goal round -> loop.json and meta.json unchanged.
	before = snap()
	newGoalCond := "advanced-goal"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{GoalCondition: &newGoalCond}))
	after = snap()
	assert.True(t, bytes.Equal(before[loopPath], after[loopPath]), "a /goal round must leave loop.json byte-identical")
	assert.True(t, bytes.Equal(before[metaPath], after[metaPath]), "a /goal round must leave meta.json byte-identical")
	assert.False(t, bytes.Equal(before[goalPath], after[goalPath]), "a /goal round must actually change goal.json")

	// Row: /loop tick -> goal.json and meta.json unchanged.
	before = snap()
	newLoopMode := "self_paced"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{LoopMode: &newLoopMode}))
	after = snap()
	assert.True(t, bytes.Equal(before[goalPath], after[goalPath]), "a /loop tick must leave goal.json byte-identical")
	assert.True(t, bytes.Equal(before[metaPath], after[metaPath]), "a /loop tick must leave meta.json byte-identical")
	assert.False(t, bytes.Equal(before[loopPath], after[loopPath]), "a /loop tick must actually change loop.json")

	// Row: status transition -> goal.json, loop.json, stats.json unchanged.
	before = snap()
	newStatus := StatusArchived
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{Status: &newStatus}))
	after = snap()
	assert.True(t, bytes.Equal(before[goalPath], after[goalPath]), "a status transition must leave goal.json byte-identical")
	assert.True(t, bytes.Equal(before[loopPath], after[loopPath]), "a status transition must leave loop.json byte-identical")
	assert.True(t, bytes.Equal(before[statsPath], after[statsPath]), "a status transition must leave stats.json byte-identical")
	assert.False(t, bytes.Equal(before[metaPath], after[metaPath]), "a status transition must actually change meta.json")
}

// ---------------------------------------------------------------------
// Test #18: TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit (BDD-63, AC-21(e))
// ---------------------------------------------------------------------

// TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit proves FR-057: the
// UnifiedMeta struct's marshalled JSON is byte-identical for the same
// logical state whether or not the on-disk representation is split across
// four files — the split is a persistence-layer change only.
func TestUnifiedMetaMarshal_ByteIdenticalAcrossSplit(t *testing.T) {
	store := u5NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	goalCond := "byte-identical-goal"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{GoalCondition: &goalCond}))
	loopMode := "interval"
	require.NoError(t, store.SetMeta(sessionID, MetaPatch{LoopMode: &loopMode}))
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "x", Tokens: 2}))
	// ADR-057-U6-inverted: AppendTranscript's Stats bump is now throttled
	// (W24/FR-061) — the cache reflects it immediately, but stats.json on
	// disk does not until a flush. This test's whole point (FR-057/AC-21(e))
	// is that the CACHE and DISK compositions marshal byte-identically for
	// the SAME logical state — which requires disk to actually hold that
	// state. Force the flush so "fromDisk"/"fromReopen" below compose from a
	// stats.json that agrees with the cache, rather than trivially failing
	// on W24's now-real deferred-write window (a property this test was
	// never meant to cover; unified_stats_flush_adr057_test.go covers that).
	require.NoError(t, store.FlushSessionStats(sessionID))

	// Read the composed value TWICE: once served from the warm cache, once
	// forced through a fresh disk compose (simulating a new process).
	fromCache, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	fromDisk, err := readUnifiedMeta(sessionDir)
	require.NoError(t, err)

	cacheBytes, err := json.MarshalIndent(fromCache, "", "  ")
	require.NoError(t, err)
	diskBytes, err := json.MarshalIndent(fromDisk, "", "  ")
	require.NoError(t, err)

	assert.Equal(t, string(cacheBytes), string(diskBytes),
		"UnifiedMeta's marshalled JSON must be byte-identical whether served from the cache or freshly composed from the 4-file split")

	// Round-trip a THIRD time through a brand-new store instance over the
	// same baseDir (a fresh process re-opening the store), proving the split
	// survives a full reload, not just an in-process re-read.
	reopened, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	fromReopen, err := reopened.GetMeta(sessionID)
	require.NoError(t, err)
	reopenBytes, err := json.MarshalIndent(fromReopen, "", "  ")
	require.NoError(t, err)
	assert.Equal(t, string(cacheBytes), string(reopenBytes),
		"UnifiedMeta's marshalled JSON must survive a full store reload byte-identically")
}

// ---------------------------------------------------------------------
// Test #19: TestMetaDocComments_NoSingleFunnelClaim (BDD-64, FR-059)
// ---------------------------------------------------------------------

// TestMetaDocComments_NoSingleFunnelClaim is a doc-truth gate (as AC-13):
// per binding Rule 4, it MUST locate BOTH comment blocks by anchor text
// BEFORE asserting their content — a search that can't find the comments at
// all would otherwise vacuously "pass" a check for absent language.
func TestMetaDocComments_NoSingleFunnelClaim(t *testing.T) {
	src, err := os.ReadFile("unified.go")
	require.NoError(t, err)
	text := string(src)

	// Positive lower bound: locate both anchor comments before asserting
	// their content.
	require.Contains(t, text, "writeMetaLocked is RETAINED",
		"must locate writeMetaLocked's doc comment by anchor text before asserting its content")
	require.Contains(t, text, "metaCache holds one independent clone per session",
		"must locate metaCache's doc comment by anchor text before asserting its content")

	// Neither comment may still claim a single whole-document write funnel.
	assert.NotContains(t, text, "single invalidation/update point for every mutation path",
		"writeMetaLocked's doc comment must no longer claim to be the single mutation funnel")
	assert.NotContains(t, text, "every mutation path funnels through it",
		"metaCache's doc comment must no longer claim every mutation funnels through one writer")
}

// ---------------------------------------------------------------------
// Test #103: TestMetaCache_HitCostsZeroDiskReads (BDD-58, FR-058/FR-103)
// ---------------------------------------------------------------------

// TestMetaCache_HitCostsZeroDiskReads installs a counting wrapper on the
// FR-103 readFileFn seam and proves a GetMeta/ListSessions cache hit
// performs ZERO disk reads after the W23 split — the split adds three more
// files to read on a cache MISS, so this is the guarantee that a cache HIT
// still costs nothing extra (FR-058).
func TestMetaCache_HitCostsZeroDiskReads(t *testing.T) {
	store := u5NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	// Warm the cache once (this call itself may or may not touch disk,
	// depending on whether NewSession's own writers already populated the
	// cache — assert only AFTER this point, isolating exactly "a cache hit").
	_, err = store.GetMeta(sessionID)
	require.NoError(t, err)

	origReadFileFn := readFileFn
	var reads int
	readFileFn = func(path string) ([]byte, error) {
		reads++
		return origReadFileFn(path)
	}
	t.Cleanup(func() { readFileFn = origReadFileFn })

	// Positive control: force a real disk read via a DIFFERENT, uncached
	// session id, proving the counting wrapper actually intercepts reads.
	other := "zero-disk-reads-control-session"
	otherDir := filepath.Join(store.BaseDir(), other)
	require.NoError(t, os.MkdirAll(otherDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "meta.json"),
		[]byte(`{"id":"zero-disk-reads-control-session","status":"active"}`), 0o600))
	_, err = store.GetMeta(other)
	require.NoError(t, err)
	require.Greater(t, reads, 0, "the counting wrapper must have observed at least one disk read for an uncached session")

	reads = 0
	for i := 0; i < 5; i++ {
		_, err := store.GetMeta(sessionID)
		require.NoError(t, err)
	}
	_, err = store.ListSessions()
	require.NoError(t, err)

	assert.Equal(t, 0, reads, "a GetMeta/ListSessions cache hit must perform ZERO disk reads through the FR-103 seam")
}

// ---------------------------------------------------------------------
// FR-008/FR-097: ParentSessionID round-trips and wires the parent index
// ---------------------------------------------------------------------

// TestParentSessionID_RoundTripsAndWiresParentIndex proves the U4 obligation
// this unit inherits: "U5 MUST wire the FR-097 index ADD side... once
// SessionMeta.ParentSessionID lands, U5 must call
// us.u4IndexAddChild(meta.ParentSessionID, meta.ID) from its create/
// meta-write writers." Without this, ChildCount is permanently 0 for real
// children and every hierarchy feature silently shows no children — exactly
// this ADR's governing defect shape.
func TestParentSessionID_RoundTripsAndWiresParentIndex(t *testing.T) {
	store := u5NewTestStore(t)
	parent, err := store.NewSession(SessionTypeChat, "", "agent-parent")
	require.NoError(t, err)
	child, err := store.NewSession(SessionTypeDelegate, "", "agent-child")
	require.NoError(t, err)
	require.NotEqual(t, parent.ID, child.ID, "FR-074 corollary: parent and child ids must be distinct")

	// Before stamping: the index must NOT already claim this relationship.
	assert.Equal(t, 0, store.ChildCount(parent.ID), "precondition: parent must have zero children before the patch")

	require.NoError(t, store.SetMeta(child.ID, MetaPatch{ParentSessionID: &parent.ID}))

	// 1. Field round-trips via GetMeta (cache hit).
	got, err := store.GetMeta(child.ID)
	require.NoError(t, err)
	assert.Equal(t, parent.ID, got.ParentSessionID)

	// 2. Field survives a cache-miss disk reload (forces readUnifiedMeta to
	// actually compose it from meta.json, not just serve a cached Go value).
	reopened, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	gotReloaded, err := reopened.GetMeta(child.ID)
	require.NoError(t, err)
	assert.Equal(t, parent.ID, gotReloaded.ParentSessionID, "ParentSessionID must survive a full store reload from disk")

	// 3. The FR-097 index (on the ORIGINAL store instance, which is the one
	// that actually processed the SetMeta call) reflects the relationship —
	// this is obligation item 1's enforcement: if u4IndexAddChild was never
	// called, ChildCount stays 0 here despite the field being correctly
	// persisted, which is exactly the silent failure shape being guarded
	// against.
	assert.Equal(t, 1, store.ChildCount(parent.ID),
		"u4IndexAddChild must have been called from the identity-group writer when ParentSessionID was set via SetMeta")

	// 4. A second, distinct child registers additively, not by replacement.
	child2, err := store.NewSession(SessionTypeDelegate, "", "agent-child-2")
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(child2.ID, MetaPatch{ParentSessionID: &parent.ID}))
	assert.Equal(t, 2, store.ChildCount(parent.ID))

	// 5. DeleteSession on a child correctly decrements via U4's already-wired
	// evict side (cross-check that U5's add side and U4's evict side agree).
	require.NoError(t, store.DeleteSession(child.ID))
	assert.Equal(t, 1, store.ChildCount(parent.ID), "deleting one child must decrement the parent's child count by exactly one")
}

// TestSessionTypeDelegate_IsValidAndLiteral pins the exact literal U4's
// obligations doc requires: "U5 MUST use the exact literal `delegate`" —
// U10 already generated the matching OpenAPI enum member from contracts/
// (SessionTypeDelegate SessionType = "delegate",
// pkg/api/generated/openapi_types.gen.go), so any other spelling here would
// silently drift the Go implementation from the wire contract.
func TestSessionTypeDelegate_IsValidAndLiteral(t *testing.T) {
	assert.Equal(t, UnifiedSessionType("delegate"), SessionTypeDelegate,
		"the delegate session type literal must be exactly \"delegate\" to match U10's generated OpenAPI enum")
	assert.True(t, IsValidSessionType(SessionTypeDelegate))
}

// ---------------------------------------------------------------------
// W11a/W11b: the IsDelegateChildEntry compile shim — REMOVED
// ---------------------------------------------------------------------
//
// TestIsDelegateChildEntry_ShimAlwaysReturnsFalse (this unit, U5) pinned the
// W11a compile shim's own mechanical behavior for the cross-wave window
// hard ordering 6 required: U5 (Wave C) landed IsDelegateChildEntry as a
// shim that always returns false, keeping the tree compiling until U18
// (Wave F) could remove both the shim and its four non-test call sites in
// pkg/gateway/pkg/agent/pkg/tools in one commit. U18 has now landed that
// removal (FR-034: "MUST be deleted and MUST have zero references outside
// tests") — the method this test called no longer exists, so the test
// pinning its mechanical behavior is removed together with it, in the same
// commit that removes the shim, rather than left as a dangling compile
// break in a package U18 does not otherwise own. Test #58
// (TestIsDelegateChildEntry_ZeroNonTestReferences) is the surviving,
// broader gate for this requirement — see its own file for the
// now-satisfied zero-reference assertion.

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoErrorf(t, err, "expected file to exist: %s", path)
	assert.Falsef(t, info.IsDir(), "expected a file, not a directory: %s", path)
}

func assertFileAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	assert.Truef(t, os.IsNotExist(err), "expected file to be ABSENT: %s (stat err=%v)", path, err)
}

// readJSONKeys reads path and returns its top-level JSON object's key set,
// for asserting AC-21(a)'s "each file contains only its own group's fields".
func readJSONKeys(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
