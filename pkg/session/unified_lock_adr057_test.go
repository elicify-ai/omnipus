// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U4 (Wave B) — coverage for W15 (UnifiedStore.mu striping), W15b
// (RetentionSweep shard-order conversion), FR-097 (parent index surface) and
// FR-101 (lock-order observation seam).
//
// Per binding Rule 1, every assertion here runs against a REAL *UnifiedStore
// rooted at a t.TempDir() and real on-disk sessions — never a spy/fake
// standing in for the store. Per Rule 3, the AC-20 concurrency assertions are
// on a SLOPE (doubling concurrency must not double wall-clock), never a
// machine-specific millisecond constant. Per Rule 4/FR-085, the static gate
// (#9) asserts its positive lower bound (>= 3 located cacheMu regions)
// BEFORE asserting the zero-violations exclusion.

package session

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------
// Test #8: TestStripedSessionLock_ShardIsolation
// ---------------------------------------------------------------------

// TestStripedSessionLock_ShardIsolation is test #8: u4ShardFor must be
// STABLE (the same key always maps to the same shard) and must NOT collapse
// every distinct key onto one shard (a degenerate constant function would
// still be "stable" but useless for isolation). FNV collisions on SOME pairs
// are expected and accepted (AC-20 dataset row 5 — "documented, not a
// failure"), so this asserts the statistical property across many ids, not
// that any two SPECIFIC ids differ.
func TestStripedSessionLock_ShardIsolation(t *testing.T) {
	const n = 200
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("sess-shard-isolation-%d", i)
	}

	// Stability: repeated calls for the SAME key return the SAME shard.
	for _, id := range ids[:20] {
		first := u4ShardFor(id)
		for r := 0; r < 5; r++ {
			require.Equal(t, first, u4ShardFor(id), "u4ShardFor(%q) must be stable across repeated calls", id)
		}
	}

	// Distinct ids must map to MORE THAN ONE shard overall — a constant
	// function (e.g. always shard 0) would pass every other test in this
	// file vacuously (every "different session" write would actually
	// serialize on the same shard) while looking correct in isolation.
	seen := make(map[uint32]int)
	for _, id := range ids {
		seen[u4ShardFor(id)]++
	}
	require.Greaterf(t, len(seen), 1,
		"200 distinct ids must spread across more than 1 of the 64 shards — got shard distribution %v", seen)

	// Also exercise the store-level Get path (not just the free function) to
	// confirm shard() agrees with u4ShardFor for the same key.
	var pool u4SessionStripedLock
	for _, id := range ids[:20] {
		idx, mu := pool.shard(id)
		require.Equal(t, u4ShardFor(id), idx)
		require.NotNil(t, mu)
	}
}

// ---------------------------------------------------------------------
// Test #9: TestCacheMu_NoFilesystemInCriticalSection
// ---------------------------------------------------------------------

// cacheMuRegion is one located critical section: the statements considered
// "inside" a cacheMu.Lock()/RLock() ... cacheMu.Unlock()/RUnlock() span.
type cacheMuRegion struct {
	file     string
	funcName string
	stmts    []ast.Stmt
}

// isCacheMuLockCall reports whether expr is a call of the form
// `<x>.cacheMu.<method>()` for one of the given method names (Lock/RLock/
// Unlock/RUnlock).
func isCacheMuLockCall(expr ast.Expr, methodNames ...string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	matched := false
	for _, m := range methodNames {
		if sel.Sel.Name == m {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return inner.Sel.Name == "cacheMu"
}

// extractCacheMuRegions walks a statement list, recursing into every nested
// control-flow block (if/for/range/plain block) to find EVERY cacheMu
// Lock/RLock call, then determines its critical section as either:
//   - the statements between it and a matching Unlock/RUnlock call as the
//     NEXT sibling statement in the SAME list, or
//   - (if the very next statement is `defer cacheMu.Unlock()`/`RUnlock()`)
//     every remaining statement in the SAME list — since a deferred release
//     fires at function return, everything after it in this block runs
//     while the lock is held.
func extractCacheMuRegions(file, funcName string, stmts []ast.Stmt) []cacheMuRegion {
	var regions []cacheMuRegion
	for i := 0; i < len(stmts); i++ {
		switch s := stmts[i].(type) {
		case *ast.IfStmt:
			if s.Body != nil {
				regions = append(regions, extractCacheMuRegions(file, funcName, s.Body.List)...)
			}
			if blk, ok := s.Else.(*ast.BlockStmt); ok {
				regions = append(regions, extractCacheMuRegions(file, funcName, blk.List)...)
			}
		case *ast.ForStmt:
			if s.Body != nil {
				regions = append(regions, extractCacheMuRegions(file, funcName, s.Body.List)...)
			}
		case *ast.RangeStmt:
			if s.Body != nil {
				regions = append(regions, extractCacheMuRegions(file, funcName, s.Body.List)...)
			}
		case *ast.BlockStmt:
			regions = append(regions, extractCacheMuRegions(file, funcName, s.List)...)
		}

		exprStmt, ok := stmts[i].(*ast.ExprStmt)
		if !ok {
			continue
		}
		if !isCacheMuLockCall(exprStmt.X, "Lock", "RLock") {
			continue
		}

		if i+1 < len(stmts) {
			if deferStmt, ok := stmts[i+1].(*ast.DeferStmt); ok {
				if isCacheMuLockCall(deferStmt.Call, "Unlock", "RUnlock") {
					regions = append(regions, cacheMuRegion{file: file, funcName: funcName, stmts: stmts[i+2:]})
					continue
				}
			}
		}
		for j := i + 1; j < len(stmts); j++ {
			es, ok := stmts[j].(*ast.ExprStmt)
			if !ok {
				continue
			}
			if isCacheMuLockCall(es.X, "Unlock", "RUnlock") {
				regions = append(regions, cacheMuRegion{file: file, funcName: funcName, stmts: stmts[i+1 : j]})
				break
			}
		}
	}
	return regions
}

// findFilesystemCall reports the first os.*/fileutil.* call found anywhere
// within stmts (recursing into every nested node via ast.Inspect), or ok
// == false if none is found.
func findFilesystemCall(fset *token.FileSet, stmts []ast.Stmt) (desc string, line int, ok bool) {
	for _, s := range stmts {
		var stop bool
		ast.Inspect(s, func(n ast.Node) bool {
			if stop {
				return false
			}
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			ident, isIdent := sel.X.(*ast.Ident)
			if !isIdent {
				return true
			}
			if ident.Name == "os" || ident.Name == "fileutil" {
				desc = ident.Name + "." + sel.Sel.Name
				line = fset.Position(call.Pos()).Line
				ok = true
				stop = true
				return false
			}
			return true
		})
		if ok {
			break
		}
	}
	return desc, line, ok
}

// TestCacheMu_NoFilesystemInCriticalSection is test #9 (FR-049/BDD-57): an
// AST gate over the three files U4 owns. Per binding Rule 4, it MUST locate
// >= 3 cacheMu critical sections BEFORE asserting the exclusion — cacheMu
// does not exist before this unit's change (`grep -c cacheMu
// pkg/session/unified.go` was 0), so a gate that locates zero has found a
// bug in the gate, not proven anything safe.
func TestCacheMu_NoFilesystemInCriticalSection(t *testing.T) {
	files := []string{"unified.go", "retention_sweep.go", "unified_lock.go"}
	fset := token.NewFileSet()

	var regions []cacheMuRegion
	for _, fname := range files {
		src, err := os.ReadFile(fname)
		require.NoErrorf(t, err, "read %s", fname)
		astFile, err := parser.ParseFile(fset, fname, src, 0)
		require.NoErrorf(t, err, "parse %s", fname)

		for _, decl := range astFile.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			regions = append(regions, extractCacheMuRegions(fname, fn.Name.Name, fn.Body.List)...)
		}
	}

	// Positive lower bound (Rule 4/FR-085), asserted FIRST: the gate itself
	// must be proven live before its zero-assertion means anything.
	require.GreaterOrEqualf(t, len(regions), 3,
		"must locate >= 3 cacheMu critical sections across %v (FR-048 creates them) — found %d; "+
			"a gate finding fewer than the stated bound has found a bug in the SEARCH, not proven anything safe",
		files, len(regions))

	for _, r := range regions {
		if desc, line, found := findFilesystemCall(fset, r.stmts); found {
			t.Errorf("cacheMu critical section in %s:%s contains a filesystem call %s at line %d — "+
				"FR-049 forbids holding cacheMu across an os.*/fileutil.* call",
				r.file, r.funcName, desc, line)
		}
	}
}

// ---------------------------------------------------------------------
// Test #88 (U4 portion): FR-101 lock-order seam / BDD-92 / SC-038
// ---------------------------------------------------------------------

// lockEvent is one recorded acquire/release through the FR-101 seam. at is
// the wall-clock instant the underlying real mutex operation completed
// (captured immediately after the real Lock()/before Unlock(), i.e. it always
// reflects genuine mutual-exclusion state, not just call/bookkeeping order) —
// used by the AC-20 structural overlap proof below to detect whether two
// DIFFERENT session shards were ever held open at the same instant, without
// relying on any wall-clock duration/ratio threshold.
type lockEvent struct {
	acquire bool
	shard   uint32
	at      time.Time
}

// installLockRecorder overrides sessionLockAcquireFn/sessionLockReleaseFn to
// append to a shared, mutex-protected recorder while still calling through
// to the REAL mutex operation — so recorded order reflects genuine mutual
// exclusion, not just call order. Returns the recorder and a restore func
// (register with t.Cleanup by the caller).
func installLockRecorder(t *testing.T) (events *[]lockEvent, restore func()) {
	t.Helper()
	var mu sync.Mutex
	var recorded []lockEvent
	origAcquire := sessionLockAcquireFn
	origRelease := sessionLockReleaseFn
	sessionLockAcquireFn = func(shard uint32, m *sync.Mutex) {
		m.Lock()
		// Timestamp captured the instant the REAL lock is held, before the
		// bookkeeping mu (which only serializes recording, not the shard
		// mutex itself) — so `at` always reflects genuine mutual-exclusion
		// state.
		now := time.Now()
		mu.Lock()
		recorded = append(recorded, lockEvent{acquire: true, shard: shard, at: now})
		mu.Unlock()
	}
	sessionLockReleaseFn = func(shard uint32, m *sync.Mutex) {
		now := time.Now()
		mu.Lock()
		recorded = append(recorded, lockEvent{acquire: false, shard: shard, at: now})
		mu.Unlock()
		m.Unlock()
	}
	return &recorded, func() {
		sessionLockAcquireFn = origAcquire
		sessionLockReleaseFn = origRelease
	}
}

// TestSessionLock_SequentialParentThenChildNeverOverlaps is test #88's U4
// portion (BDD-92, SC-038): U2's CreateSessionWithID does not exist in this
// wave (it is unified_api.go, Wave C) so this simulates the exact FR-082
// protocol it must follow — read the parent's meta under
// lockSession(parentID), Unlock, THEN lockSession(childID) — using the
// production lockSession/Unlock entry points and the FR-101 seam to record
// real acquire/release order. Distinct, non-equal parent/child ids per the
// corollary rule.
func TestSessionLock_SequentialParentThenChildNeverOverlaps(t *testing.T) {
	store := newTestStoreForLockTests(t)
	events, restore := installLockRecorder(t)
	t.Cleanup(restore)

	const parentID = "parent-session-88"
	const childID = "child-session-88"
	require.NotEqual(t, parentID, childID, "FR-074 corollary: parent and child ids must be distinct")

	// FR-082's protocol: acquire parent's shard, read, release, THEN acquire
	// child's shard. lockSession/Unlock are the real production entry points.
	hParent := store.lockSession(parentID)
	_ = hParent // simulate "read the parent's meta" here
	hParent.Unlock()

	hChild := store.lockSession(childID)
	hChild.Unlock()

	recorded := *events
	require.Len(t, recorded, 4, "expected exactly 4 recorded events: acquire(parent) release(parent) acquire(child) release(child)")

	parentShard := u4ShardFor(parentID)
	childShard := u4ShardFor(childID)

	require.True(t, recorded[0].acquire && recorded[0].shard == parentShard, "event 0 must be acquire(shard(parent))")
	require.True(t, !recorded[1].acquire && recorded[1].shard == parentShard, "event 1 must be release(shard(parent))")
	require.True(t, recorded[2].acquire && recorded[2].shard == childShard, "event 2 must be acquire(shard(child))")
	require.True(t, !recorded[3].acquire && recorded[3].shard == childShard, "event 3 must be release(shard(child))")

	// The core BDD-92 assertion: at NO instant are two session shards held
	// simultaneously — walk the event log tracking currently-held shards.
	held := make(map[uint32]bool)
	for idx, ev := range recorded {
		if ev.acquire {
			held[ev.shard] = true
			require.LessOrEqualf(t, len(held), 1, "two session shards held simultaneously at event %d: %+v", idx, held)
		} else {
			delete(held, ev.shard)
		}
	}
}

// TestLockAllSessionShards_AcquiresInStrictAscendingIndexOrder covers
// BDD-92's second clause and SC-038's second clause: ClearAll/RetentionSweep
// (via lockAllSessionShards, the FR-050(a) exception) MUST acquire all 64
// shards in strictly ascending index order, recorded via the SAME FR-101
// seam used above.
func TestLockAllSessionShards_AcquiresInStrictAscendingIndexOrder(t *testing.T) {
	store := newTestStoreForLockTests(t)
	events, restore := installLockRecorder(t)
	t.Cleanup(restore)

	unlock := store.lockAllSessionShards()
	unlock()

	recorded := *events
	require.Len(t, recorded, 128, "64 acquires + 64 releases expected")

	acquires := recorded[:64]
	for i, ev := range acquires {
		require.Truef(t, ev.acquire, "event %d must be an acquire", i)
		require.Equalf(t, uint32(i), ev.shard, "acquire %d must be shard index %d (strictly ascending)", i, i)
	}
	releases := recorded[64:]
	for i, ev := range releases {
		require.Falsef(t, ev.acquire, "event %d must be a release", 64+i)
		require.Equalf(t, uint32(i), ev.shard, "release %d must be shard index %d (ascending)", i, i)
	}
}

// TestClearAll_AcquiresAllShardsInIndexOrder proves the REAL ClearAll call
// (not a hand-rolled simulation) goes through lockAllSessionShards, by
// asserting the recorded order matches the strict-ascending-index shape
// while ClearAll runs against a real store with real sessions.
func TestClearAll_AcquiresAllShardsInIndexOrder(t *testing.T) {
	store := newTestStoreForLockTests(t)
	for i := 0; i < 3; i++ {
		_, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
	}

	events, restore := installLockRecorder(t)
	t.Cleanup(restore)

	_, err := store.ClearAll()
	require.NoError(t, err)

	recorded := *events
	require.GreaterOrEqual(t, len(recorded), 128, "ClearAll must acquire+release all 64 shards")
	// The first 64 events (the lockAllSessionShards acquire loop, which runs
	// before ClearAll does any per-session work) must be a strictly
	// ascending 0..63 acquire sequence.
	for i := 0; i < 64; i++ {
		require.Truef(t, recorded[i].acquire, "event %d must be an acquire", i)
		require.Equalf(t, uint32(i), recorded[i].shard, "acquire %d must be shard %d", i, i)
	}
}

// TestRetentionSweep_AcquiresAllShardsInIndexOrder is W15b's direct coverage:
// RetentionSweep must go through the SAME lockAllSessionShards primitive as
// ClearAll (FR-050(a)), not the old narrow per-directory us.mu it used
// before this unit's change.
func TestRetentionSweep_AcquiresAllShardsInIndexOrder(t *testing.T) {
	store := newTestStoreForLockTests(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	events, restore := installLockRecorder(t)
	t.Cleanup(restore)

	_, err = store.RetentionSweep(365) // long window: nothing aged, but the shard-acquire loop always runs
	require.NoError(t, err)
	_ = meta

	recorded := *events
	require.GreaterOrEqual(t, len(recorded), 128, "RetentionSweep must acquire+release all 64 shards")
	for i := 0; i < 64; i++ {
		require.Truef(t, recorded[i].acquire, "event %d must be an acquire", i)
		require.Equalf(t, uint32(i), recorded[i].shard, "acquire %d must be shard %d", i, i)
	}
}

// newTestStoreForLockTests mirrors newTestStore/newUnifiedStoreForTest
// (unified_test.go / retention_sweep_test.go) — a small local helper so this
// file has no ordering dependency on either.
func newTestStoreForLockTests(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	// See u2NewTestStore's doc comment (unified_api_adr057_test.go) / issue
	// #634: without this, the store's background stats-flusher goroutine
	// outlives this test and can inject foreign events into a LATER test's
	// installLockRecorder trace via the shared package-level FR-101 seam —
	// exactly the class of pollution this file's own recorder exists to
	// detect, so this file must not itself be a source of it.
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ---------------------------------------------------------------------
// AC-20: concurrent writes to DIFFERENT sessions do not serialize
//
// DECISION (2026-08-04, CI-flake follow-up): this claim was previously ALSO
// covered by two wall-clock ratio tests —
// TestUnifiedStore_ConcurrentWritesToDifferentSessionsDoNotSerialize
// (AC-20(a), N-vs-2N slope) and TestAppendTranscript_NotDelayedBySessionBCreate
// (AC-20(c), baseline-vs-contention) — each already hardened with a 2.5x
// bound and up to 3 independent re-measurement attempts. CI still flaked on
// them post-hardening (a DIFFERENT one of the two failing each run — see
// this unit's CI report), because this project's shared/virtualized devpod
// disk has fsync-throughput bursts (observed: a healthy 7.35ms measurement
// immediately followed by a 111.87ms one, ratio 15.21, moments later in the
// SAME run) that no point-in-time load check or bounded retry count can
// fully rule out.
//
// Raising the ratio threshold further was rejected: it would erode the
// signal until the test stops proving anything, and this test guards
// ADR-057's D10/W15 lock striping that the project's concurrency claims rest
// on. Instead, the two ratio tests were DELETED, not loosened, because the
// underlying claim they existed to protect — independent session shards can
// be held open at the same time; one session's I/O never blocks behind
// another's — is proven twice below by DIRECT OBSERVATION of real lock
// acquire/release instants (the FR-101 sessionLockAcquireFn/
// sessionLockReleaseFn seam) instead of wall-clock inference, so neither
// depends on machine speed:
//
//   - TestSessionLock_DifferentSessionsNeverBlockEachOther (below) holds one
//     shard for a fixed, large window and asserts a DIFFERENT shard's
//     unrelated op resolves in a small fraction of that window — the exact
//     AC-20(c) scenario (A's work vs. B's), in fixed-op form.
//   - TestUnifiedStore_ConcurrentSessionWritesGenuinelyOverlapAcrossShards
//     runs the SAME real concurrent create+append workload the deleted
//     AC-20(a) ratio test used, and asserts two DIFFERENT shards' critical
//     sections were open at the same wall-clock instant — non-serialization
//     proven by construction rather than by timing.
//
// Both were verified (2026-08-04) to still FAIL if the striped lock is
// collapsed back to a single mutex (u4ShardFor forced to a constant shard);
// see this file's introducing commit message for the before/after proof.
// Host contention can only make a real overlap take LONGER, never turn it
// into "no overlap" — unlike a ratio, neither assertion can be defeated by a
// noisy shared box.
// ---------------------------------------------------------------------

// shardInterval is one fully-observed [acquire, release) window for a single
// session shard, derived from a lockEvent stream by pairing each acquire with
// the NEXT release recorded for the same shard index. Because a shard's
// underlying *sync.Mutex is a real mutex, a given shard index can never have
// two open intervals at once — pairing sequentially is safe.
type shardInterval struct {
	shard      uint32
	start, end time.Time
}

// buildShardIntervals walks a lockEvent stream (in recorded order) and
// returns one shardInterval per complete acquire/release pair, across all
// shards that appeared in events. Any shard left "open" (acquired but never
// released, e.g. a test bug) is dropped rather than fabricating an end time.
func buildShardIntervals(events []lockEvent) []shardInterval {
	open := make(map[uint32]time.Time)
	var intervals []shardInterval
	for _, ev := range events {
		if ev.acquire {
			open[ev.shard] = ev.at
			continue
		}
		if start, ok := open[ev.shard]; ok {
			intervals = append(intervals, shardInterval{shard: ev.shard, start: start, end: ev.at})
			delete(open, ev.shard)
		}
	}
	return intervals
}

// distinctShardsOverlapped reports whether any two intervals belonging to
// DIFFERENT shard indices overlap in wall-clock time (half-open interval
// overlap: a.start < b.end && b.start < a.end). Same-shard pairs are skipped
// — a real mutex makes same-shard overlap impossible by construction, so
// checking it proves nothing about independence.
func distinctShardsOverlapped(intervals []shardInterval) bool {
	for i := range intervals {
		for j := i + 1; j < len(intervals); j++ {
			if intervals[i].shard == intervals[j].shard {
				continue
			}
			if intervals[i].start.Before(intervals[j].end) && intervals[j].start.Before(intervals[i].end) {
				return true
			}
		}
	}
	return false
}

// Traces to: docs/internal/specs/adr-057-session-unification-spec.md:3045
// (AC-20(a)/(c) — "assert wall-clock completion is close to the single-
// session time... the assertion is on the SLOPE"; this test proves the same
// underlying D10-sharding claim structurally instead of via that slope).
//
// TestUnifiedStore_ConcurrentSessionWritesGenuinelyOverlapAcrossShards is the
// AC-20 STRUCTURAL, load-independent regression proof called for by this
// unit's CI-hardening pass. It replaces a wall-clock RATIO test that used to
// live in this file (see the "AC-20: concurrent writes to DIFFERENT
// sessions" section header above for the removal rationale) — this file's
// own doc comments already documented that ratio as noisy on a
// shared/contended box (1.74x-22x swings with no code change, see
// TestSessionLock_DifferentSessionsNeverBlockEachOther's doc comment).
// Host contention can only ever make an already-true overlap take LONGER —
// it can never turn a real overlap into "no overlap", so this assertion
// (unlike a ratio) survives arbitrary host/CI noise without a threshold.
//
// Method: run the SAME real concurrent create+append workload the deleted
// ratio test used, with the FR-101 seam's timestamped recorder installed,
// then directly check whether two DIFFERENT session shards were
// EVER held open (acquired-but-not-yet-released) at the same wall-clock
// instant. AppendTranscript/NewSession hold lockSession(id) across the real
// fsync-bound filesystem work (unified.go), so a genuinely striped lock lets
// one goroutine's fsync syscall block while ANOTHER goroutine's DIFFERENT
// shard is concurrently held — real overlap, observable regardless of host
// speed. A reverted single-global-mutex design (the pre-ADR-057/R-8
// regression this unit fixes) makes this overlap IMPOSSIBLE BY CONSTRUCTION:
// only one shard would ever be touched, so no two intervals could ever carry
// different shard indices — see this unit's report for the verified
// before/after (collapsing u4ShardFor to a constant made this test fail with
// "must touch more than 1 of the 64 shards", and made
// TestStripedSessionLock_ShardIsolation and
// TestSessionLock_DifferentSessionsNeverBlockEachOther fail too).
func TestUnifiedStore_ConcurrentSessionWritesGenuinelyOverlapAcrossShards(t *testing.T) {
	const n = 16
	const appendsPerSession = 8
	const maxAttempts = 5 // guards the OTHER direction: a machine fast enough that no
	// interval ever overlaps in wall-clock terms would otherwise make this test flap
	// on a genuinely-correct implementation. Retrying with a fresh store/recorder each
	// time costs nothing (each attempt is a handful of milliseconds, see AC-20(a)'s
	// baseline) and does not touch the pass/fail bar itself — only whether we ask again.

	var lastIntervals []shardInterval
	var lastShards map[uint32]bool
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		store := newTestStoreForLockTests(t)
		events, restore := installLockRecorder(t)

		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(idx int) {
				defer wg.Done()
				meta, err := store.NewSession(SessionTypeChat, "", "agent-overlap")
				if err != nil {
					t.Errorf("NewSession[%d] failed: %v", idx, err)
					return
				}
				for a := 0; a < appendsPerSession; a++ {
					if appendErr := store.AppendTranscript(meta.ID, TranscriptEntry{
						Role: "assistant", Content: fmt.Sprintf("line-%d-%d", idx, a),
					}); appendErr != nil {
						t.Errorf("AppendTranscript[%d/%d] failed: %v", idx, a, appendErr)
					}
				}
			}(i)
		}
		wg.Wait()
		restore()

		recorded := *events
		require.NotEmpty(t, recorded, "workload must have recorded at least one lock event")

		intervals := buildShardIntervals(recorded)
		require.NotEmpty(t, intervals, "must have at least one complete acquire/release interval")

		distinctShards := make(map[uint32]bool, len(intervals))
		for _, iv := range intervals {
			distinctShards[iv.shard] = true
		}
		// Positive lower bound BEFORE the overlap check (mirrors this file's Rule 4
		// AST-gate pattern): with n=16 distinct session ids hashed across 64 shards,
		// fewer than 2 distinct shards touched means the WORKLOAD/HASHING found a
		// bug, not that overlap is absent — a single-shard collapse (the exact
		// regression this test exists to catch) hits this branch, not the assert
		// below, and is reported just as loudly.
		require.Greaterf(t, len(distinctShards), 1,
			"workload of %d distinct sessions must touch more than 1 of the 64 shards — got %v "+
				"(a collapsed/reverted striped lock, e.g. u4ShardFor always returning the same index, "+
				"produces exactly this)", n, distinctShards)

		lastIntervals, lastShards = intervals, distinctShards
		if distinctShardsOverlapped(intervals) {
			t.Logf("AC-20 structural overlap: found on attempt %d/%d — %d intervals across %d distinct shards",
				attempt, maxAttempts, len(intervals), len(distinctShards))
			return
		}
		t.Logf("AC-20 structural overlap: attempt %d/%d found no overlap (%d intervals across %d shards) — retrying",
			attempt, maxAttempts, len(intervals), len(distinctShards))
	}

	t.Fatalf("expected at least two DIFFERENT session shards to be held open simultaneously during "+
		"%d concurrent session creates+appends, across %d attempts — got %d intervals across %d shards "+
		"with no overlap on the final attempt, which is exactly what a reverted single-global-mutex "+
		"design would produce (only one shard could ever be held at a time)",
		n, maxAttempts, len(lastIntervals), len(lastShards))
}

// TestUnifiedStore_ConcurrentAppendsToSameSessionStaySafe is AC-20's mutual-
// exclusion counterpart: striping must not weaken correctness for
// concurrent writers to the SAME session. Run under -race (see this unit's
// verification commands) to catch any data race, and assert zero transcript
// lines are lost.
func TestUnifiedStore_ConcurrentAppendsToSameSessionStaySafe(t *testing.T) {
	store := newTestStoreForLockTests(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	const writers = 20
	const perWriter = 10
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(idx int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if appendErr := store.AppendTranscript(meta.ID, TranscriptEntry{
					Role: "assistant", Content: fmt.Sprintf("w%d-%d", idx, i),
				}); appendErr != nil {
					t.Errorf("AppendTranscript failed: %v", appendErr)
				}
			}
		}(w)
	}
	wg.Wait()

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	assert.Len(t, entries, writers*perWriter, "no transcript line may be lost under concurrent same-session appends")
}

// ---------------------------------------------------------------------
// Test #116 (U4 portion): FR-097 parent-index surface mechanics
// ---------------------------------------------------------------------

// TestSessionParentIndex_AddChildCountIsO1PerRow is the U4 half of test
// #116: the index mechanics (u4IndexAddChild/ChildCount) work correctly for
// a representative fan-out, using synthetic ids — since
// SessionMeta.ParentSessionID (FR-008/W2) does not exist in this wave, this
// exercises the index SURFACE directly rather than through a real
// create-with-parent call, per this unit's documented Wave-B scope (see
// UnifiedStore.parentIndex's doc comment in unified.go).
func TestSessionParentIndex_AddChildCountIsO1PerRow(t *testing.T) {
	store := newTestStoreForLockTests(t)

	const root = "root-session-116"
	// Dataset row 1: create a root — before any child is added, ChildCount
	// must be 0 (never negative, never erroring on an untracked id).
	require.Equal(t, 0, store.ChildCount(root))

	// Dataset row 3: 24 children of one root, resolved O(1).
	const n = 24
	children := make([]string, n)
	for i := 0; i < n; i++ {
		children[i] = fmt.Sprintf("child-116-%d", i)
		require.NotEqual(t, root, children[i], "FR-074: parent/child ids must be distinct")
		store.u4IndexAddChild(root, children[i])
	}
	assert.Equal(t, n, store.ChildCount(root), "child_count(root) must equal the number of registered children")

	// A no-op parentID must never register a child (root safety).
	store.u4IndexAddChild("", "orphan-child-116")
	assert.Equal(t, 0, store.ChildCount(""), "empty parentID must never accumulate children")

	// A malformed self-parent must never be indexed.
	store.u4IndexAddChild("self-parent-116", "self-parent-116")
	assert.Equal(t, 0, store.ChildCount("self-parent-116"), "a session must never be indexed as its own child")

	// Idempotency: re-adding an existing pair must not inflate the count.
	store.u4IndexAddChild(root, children[0])
	assert.Equal(t, n, store.ChildCount(root), "re-adding an existing (parent, child) pair must not change child_count")
}

// TestSessionParentIndex_EvictHandlesBothParentAndChildRoles is dataset rows
// 5 and 6: DeleteSession-equivalent eviction of a CHILD decrements the
// parent's count and removes the child from the index; eviction of a PARENT
// turns every former child into an orphan (child_count of the deleted id no
// longer resolves, i.e. returns 0, and — critically — none of the former
// children is still reachable as a tracked child of the gone parent).
func TestSessionParentIndex_EvictHandlesBothParentAndChildRoles(t *testing.T) {
	store := newTestStoreForLockTests(t)

	const parent = "parent-116-evict"
	const childA = "child-116-evict-a"
	const childB = "child-116-evict-b"
	store.u4IndexAddChild(parent, childA)
	store.u4IndexAddChild(parent, childB)
	require.Equal(t, 2, store.ChildCount(parent))

	// Dataset row 5: evict one child — parent's count decrements, the OTHER
	// child is untouched.
	store.u4IndexEvict(childA)
	assert.Equal(t, 1, store.ChildCount(parent), "evicting one child must decrement child_count(parent) by exactly 1")

	// Dataset row 6: evict the parent itself — its child_count no longer
	// resolves (returns 0, the zero value for an untracked id), and the
	// remaining child must no longer be reachable as anyone's tracked child
	// (it becomes an orphan root at the index level).
	store.u4IndexEvict(parent)
	assert.Equal(t, 0, store.ChildCount(parent), "child_count of a deleted parent must no longer resolve (reads as 0)")

	// Re-adding childB under a DIFFERENT new parent must work cleanly —
	// proving evict() actually cleared its old childToParent entry rather
	// than leaving a stale pointer to the gone parent.
	const newParent = "new-parent-116"
	store.u4IndexAddChild(newParent, childB)
	assert.Equal(t, 1, store.ChildCount(newParent), "a former child must be re-parentable after its old parent is evicted")

	// Evicting an id that was never indexed at all must be a safe no-op.
	store.u4IndexEvict("never-indexed-116")
}

// TestSessionParentIndex_DeleteSessionWiresEviction proves u4IndexEvict is
// ACTUALLY called from the real DeleteSession path this unit owns (not just
// exercised standalone) — the FR-097 mutation point this unit is responsible
// for wiring.
func TestSessionParentIndex_DeleteSessionWiresEviction(t *testing.T) {
	store := newTestStoreForLockTests(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	const syntheticParent = "synthetic-parent-116-wired"
	store.u4IndexAddChild(syntheticParent, meta.ID)
	require.Equal(t, 1, store.ChildCount(syntheticParent))

	require.NoError(t, store.DeleteSession(meta.ID))

	assert.Equal(t, 0, store.ChildCount(syntheticParent),
		"DeleteSession must evict the deleted session from the FR-097 parent index, not just metaCache")
}

// TestSessionParentIndex_ClearAllWiresEviction is the ClearAll analogue of
// the above — the OTHER mutation point (FR-097's "delete"/"eviction") this
// unit owns.
func TestSessionParentIndex_ClearAllWiresEviction(t *testing.T) {
	store := newTestStoreForLockTests(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	const syntheticParent = "synthetic-parent-116-clearall"
	store.u4IndexAddChild(syntheticParent, meta.ID)
	require.Equal(t, 1, store.ChildCount(syntheticParent))

	_, err = store.ClearAll()
	require.NoError(t, err)

	assert.Equal(t, 0, store.ChildCount(syntheticParent),
		"ClearAll must evict every removed session from the FR-097 parent index")
}

// ---------------------------------------------------------------------
// AC-20, load-independent primary proof.
//
// This devpod is a documented shared, variable-spec environment (CLAUDE.md)
// that can run several concurrent agent sessions, browsers and dev servers
// at once. Verified in practice while developing this unit: host load
// average 8.26 on an 8-core box, from unrelated concurrent Claude Code /
// opencode sessions and headless-browser processes — entirely outside this
// code's control. A wall-clock N-vs-2N slope measurement (below) is
// therefore an inherently noisy signal on THIS box at any given moment: the
// same fsync-bound workload measured minutes apart swung from a 1.74x to a
// 22x ratio purely from host contention, with no code change in between.
//
// TestSessionLock_DifferentSessionsNeverBlockEachOther is the SLOPE-
// independent version of the same AC-20 claim: it holds session A's shard
// for an artificially long, fixed window (300ms — many multiples of any
// real lock hold in this store) and asserts session B's completely
// unrelated lock/unlock resolves in a small fraction of that window. Because
// B's own work is trivial (no I/O), the ~microsecond time it needs is robust
// to host scheduling noise by a wide margin: the test only fails if B is
// ACTUALLY blocked waiting on A's shard, which host load cannot fake in
// either direction. This is the authoritative, always-reliable proof that
// different sessions use independent shards — see the "AC-20: concurrent
// writes to DIFFERENT sessions" section header above for why the
// wall-clock ratio tests that used to corroborate it were removed instead
// of further loosened.
// ---------------------------------------------------------------------

func TestSessionLock_DifferentSessionsNeverBlockEachOther(t *testing.T) {
	store := newTestStoreForLockTests(t)
	const idA = "block-test-a-20"

	var idB string
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("block-test-b-20-%d", i)
		if u4ShardFor(candidate) != u4ShardFor(idA) {
			idB = candidate
			break
		}
		if i > 1000 {
			t.Fatal("could not find a distinct-shard id within 1000 attempts")
		}
	}

	hA := store.lockSession(idA)
	const holdFor = 300 * time.Millisecond
	fired := make(chan struct{})
	releaseTimer := time.AfterFunc(holdFor, func() {
		hA.Unlock()
		close(fired)
	})
	// Issue #634: this test's own `select` below returns as soon as B
	// completes (microseconds), almost always well BEFORE holdFor elapses —
	// so without this Cleanup, the test used to return with shard idA still
	// LOCKED and a goroutine still pending to unlock it up to holdFor later,
	// via the SAME package-level FR-101 sessionLockAcquireFn/ReleaseFn seam
	// every other test in this package shares. A later test's
	// installLockRecorder could easily still be installed when that stray
	// release finally fired, injecting a foreign event into its trace.
	// time.Timer.Stop's return value distinguishes the two cases correctly:
	// true means the AfterFunc body never ran at all (so hA.Unlock was never
	// called — release it here instead); false means it already fired or is
	// firing right now (so join `fired` to guarantee that goroutine has
	// fully exited before this test returns).
	t.Cleanup(func() {
		if releaseTimer.Stop() {
			hA.Unlock()
		} else {
			<-fired
		}
	})

	bDone := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		hB := store.lockSession(idB)
		hB.Unlock()
		bDone <- time.Since(start)
	}()

	select {
	case elapsed := <-bDone:
		// B must complete in a SMALL FRACTION of A's hold window, proving it
		// never waited on A's shard at all — not merely "finished before A's
		// timer fired by luck". Half the hold window is a comfortable margin
		// even under heavy host contention (Rule 3: this is an
		// independence/slope proof, not a tight absolute-timing assertion).
		assert.Lessf(t, elapsed, holdFor/2,
			"session B's lock/unlock took %v while session A's UNRELATED shard was deliberately held for %v — "+
				"this indicates the two sessions' locks are NOT independent",
			elapsed, holdFor)
	case <-time.After(holdFor + 5*time.Second):
		t.Fatal("session B never acquired its lock — deadlock, or idA/idB unexpectedly share a shard")
	}
}
