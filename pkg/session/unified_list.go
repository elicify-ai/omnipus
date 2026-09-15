// unified_list.go: List, index and sweep sessions (retention, ClearAll)

package session

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ListSessions returns every session this store currently knows about,
// UpdatedAt-descending. See listSessionsFiltered (the shared implementation)
// for the full reconcile/consistency-model doc comment.
func (us *UnifiedStore) ListSessions() ([]*UnifiedMeta, error) {
	return us.listSessionsFiltered(nil)
}

// ListSessionsFiltered performs the EXACT SAME reconcile + FR-097a prune
// pass as ListSessions, but clones only the metas for which pred returns
// true (14-reviewer fix-wave finding #3, MEDIUM perf).
//
// THE PROBLEM THIS ADDRESSES. ListSessions' final snapshot step
// unconditionally Clone()s every cached session — the right default for a
// caller that genuinely wants everything, but expensive for a caller that
// only cares about a narrow subset. The ADR-053 goal-loop sweeps
// (pkg/agent/goal_loop.go's goalIdleExpirySweep, pkg/agent/
// goal_triggers.go's goalQuietWindowSettle) run on a 30s tick and both
// immediately discard every meta whose GoalCondition is empty — at 1000
// sessions with zero active goals, that is 1000 wasted clones every 30s
// (4-8MB/min churn) for a result the caller throws away unfiltered. This
// method lets such a caller pay only for the clones it keeps; ListSessions'
// own public zero-arg behavior (and its exact byte-for-byte semantics) is
// completely unchanged — it is now simply "ListSessionsFiltered(nil)".
//
// pred receives the SAME live, cacheMu-guarded *UnifiedMeta a caller would
// otherwise get a CLONE of from ListSessions — pred MUST treat it as
// read-only and MUST NOT retain the pointer or mutate any field on it
// (mutating the store's live cache entry from outside its own locked
// writers is exactly the aliasing hazard every Clone() call in this package
// exists to prevent). Only entries pred approves are cloned before being
// added to the returned slice, so the CALLER's own copy is always safe to
// keep and mutate as usual. nil pred means "everything" — used internally
// by ListSessions() itself so the two entry points can never silently
// diverge on the reconcile/prune logic.
//
// Consistency model, locking, and the FR-097a prune pass are identical to
// ListSessions — see below.
func (us *UnifiedStore) ListSessionsFiltered(pred func(*UnifiedMeta) bool) ([]*UnifiedMeta, error) {
	return us.listSessionsFiltered(pred)
}

// ListSessions returns all session metas, sorted by UpdatedAt descending.
//
// Primarily served from metaCache — avoiding the old per-call O(N)
// os.ReadDir + per-session os.ReadFile+json.Unmarshal scan — but first
// reconciles the cache against disk with a directory-NAMES-only os.ReadDir
// (one syscall, no per-file reads for entries already cached). Any directory
// not yet in metaCache — e.g. a session written directly to disk out-of-band,
// bypassing this store entirely — is lazily loaded via readMetaLocked (the
// same self-heal path GetMeta/SetMeta/etc. use on a cache miss), which also
// populates the cache so the disk read is paid at most once per session, not
// on every ListSessions call. A session whose meta.json fails to read/parse
// is logged at Warn and skipped, not treated as fatal.
//
// A missing base directory (os.IsNotExist) is a normal empty state — a store
// that has never persisted a session yet — and is NOT an error: this
// reconciles nothing and falls through to whatever is already in metaCache
// (empty, in that case). Any OTHER read failure (e.g. permission denied) is a
// genuine, caller-visible error: it is both logged at Warn AND returned, so
// callers that aggregate across multiple stores — e.g.
// pkg/agent's AgentLoop.ListAllSessions, which surfaces one error per broken
// store in its partial-errors slice — can actually see and report it, rather
// than this store silently reporting "0 sessions" indistinguishably from a
// genuinely empty store. The already-cached metas (if any) are still
// returned alongside the error — a transient/permission failure to
// reconcile against disk should not erase sessions this store already knows
// about from a prior successful call.
//
// Consistency model (FR-086, post-striping): a best-effort POINT-IN-TIME
// snapshot. It MAY omit a session deleted DURING this call, MUST NOT panic
// or deadlock, and MUST NOT return a partially-composed meta. It MUST NOT
// return a session whose directory was already absent when the call began
// (FR-086's "must not resurrect a gone session" clause) — ADR-057 U6 (Wave
// D) closes this: the reconcile pass below now PRUNES a metaCache entry
// whose directory vanished out of band (RetentionSweep, an operator rm, a
// crashed deploy — FR-097a) in the SAME pass that adds newly-discovered
// out-of-band directories, reusing the single os.ReadDir result for both
// directions so the prune costs ZERO additional disk reads beyond what this
// method already performed before FR-097a existed. A cacheLoadFailures
// exclusion (Ambiguity item 8) is untouched by this: a session that failed
// to load at construction was never admitted into metaCache, so it is never
// a candidate for this prune.
//
// FR-051 locking (replacing the old store-global us.mu.Lock() for the whole
// call): reconciliation of each out-of-band directory happens under THAT
// session's own shard only (lockSession), never a store-global lock, so a
// concurrent create/append/etc. against a DIFFERENT session is never blocked
// by a ListSessions call in flight. The final snapshot is taken under
// cacheMu.RLock — a short, I/O-free critical section (FR-049) — so the
// steady-state (no out-of-band directories) cost is unchanged: one
// os.ReadDir plus map lookups, zero per-session disk reads.
//
// FR-102 seam: u6ReconcileSnapshotBarrierFn (unified_stats_flush.go) fires
// between the reconcile pass (add + prune) above and the final snapshot
// below, letting an in-package test interleave a DeleteSession
// deterministically at exactly that boundary (BDD-95/#92/SC-041) instead of
// hoping go test -race happens to hit the window.
//
// Defect-2 fix seam: listSessionsPruneRaceBarrierFn fires right after the
// os.ReadDir snapshot below is captured but BEFORE onDisk is built from it,
// letting an in-package test deterministically create (or dirty-mark) a
// session in the exact window a real concurrent goroutine would occupy
// between this call's ReadDir and its stale-eviction pass further down —
// see the stale-eviction loop's own doc comment for why that window used to
// be able to permanently lose a session's unflushed stats and parent-index
// edge. Default no-op; overridden only by a test in this package.
var listSessionsPruneRaceBarrierFn = func() {}

// listSessionsFiltered is the shared implementation backing both
// ListSessions and ListSessionsFiltered.
func (us *UnifiedStore) listSessionsFiltered(pred func(*UnifiedMeta) bool) ([]*UnifiedMeta, error) {
	var listErr error
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("unified_store: list sessions: read base dir", "dir", us.baseDir, "error", err)
			listErr = fmt.Errorf("unified_store: list sessions: read base dir %q: %w", us.baseDir, err)
		}
	} else {
		listSessionsPruneRaceBarrierFn()
		onDisk := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == ".context" {
				continue
			}
			name := entry.Name()
			if err := validateSessionID(name); err != nil {
				continue
			}
			onDisk[name] = struct{}{}

			us.cacheMu.RLock()
			_, cached := us.metaCache[name]
			us.cacheMu.RUnlock()
			if cached {
				// Already cached — no per-file disk read needed.
				continue
			}
			// Unknown on-disk directory: lazily load via the standard
			// self-heal path under ONLY this session's shard (never a
			// store-global lock), which also populates metaCache as a side
			// effect (see readMetaLocked's cache-miss branch).
			h := us.lockSession(name)
			_, readErr := us.readMetaLocked(name)
			h.Unlock()
			if readErr != nil {
				slog.Warn(
					"unified_store: list sessions: skipping unreadable out-of-band session dir",
					"dir", name, "error", readErr,
				)
				continue
			}
		}

		// FR-097a: evict any cached session absent from onDisk — its
		// directory vanished out of band since it was cached. Snapshot the
		// stale ids first (a short cacheMu.RLock, no I/O), then evict each
		// under ITS OWN shard (mirroring DeleteSession's own lock order:
		// sessionLock(id) -> cacheMu -> u4IndexEvict) so a concurrent
		// create/delete for the SAME id can never interleave mid-eviction.
		//
		// Defect-2 fix: onDisk above is a SNAPSHOT from the os.ReadDir call
		// at the top of this method — it is stale the instant a concurrent
		// goroutine creates a new session between that ReadDir and this loop
		// reaching its id. Such a session is NOT a deletion (its directory
		// exists right now); evicting it unconditionally would silently and
		// permanently drop any cache-only-dirty stats delta it had accrued
		// (u6MarkStatsDirtyLocked/D12 defers the stats.json write, so the
		// cache entry can be the ONLY copy of a token/cost update) and its
		// FR-097 parent-index edge, with no re-add path — the exact
		// "success-shaped" silent data loss this whole spec exists to close
		// out. So each candidate is RE-STAT'd, under its own shard, right
		// before eviction — mirroring ClearAll's own re-stat pattern
		// (unified.go, ClearAll's "remaining" sweep) — and only evicted when
		// the directory is confirmed genuinely gone (os.ErrNotExist). An
		// ambiguous stat error (e.g. a permission blip) also skips eviction,
		// erring on the side of not losing data; a genuine deletion missed
		// this way is simply re-observed as stale on a later ListSessions
		// call once its absence is stable across an entire ReadDir snapshot.
		us.cacheMu.RLock()
		staleIDs := make([]string, 0)
		for id := range us.metaCache {
			if _, present := onDisk[id]; !present {
				staleIDs = append(staleIDs, id)
			}
		}
		us.cacheMu.RUnlock()
		for _, id := range staleIDs {
			h := us.lockSession(id)
			if _, statErr := os.Stat(filepath.Join(us.baseDir, id)); !errors.Is(statErr, os.ErrNotExist) {
				// The directory exists now (created/recreated after the
				// ReadDir snapshot above) or the stat was inconclusive —
				// this is a live session, not a deletion. Do not evict.
				h.Unlock()
				continue
			}
			us.cacheMu.Lock()
			delete(us.metaCache, id)
			delete(us.dirtyStats, id)
			us.cacheMu.Unlock()
			us.u4IndexEvict(id) // FR-097
			h.Unlock()
		}
	}

	// FR-102 barrier — see this method's doc comment.
	u6ReconcileSnapshotBarrierFn()

	us.cacheMu.RLock()
	metas := make([]*UnifiedMeta, 0, len(us.metaCache))
	for _, meta := range us.metaCache {
		if pred != nil && !pred(meta) {
			continue
		}
		metas = append(metas, meta.Clone())
	}
	us.cacheMu.RUnlock()

	// FR-098-style stable ordering at the store layer: UpdatedAt descending
	// with session id as a tiebreak, so two sessions sharing one timestamp
	// (down to whatever resolution the clock/filesystem gives) cannot
	// silently swap places between two calls with no intervening write —
	// slices.SortFunc alone is not a stable sort, and comparing only
	// UpdatedAt leaves ties resolved by map iteration order, which Go
	// deliberately randomizes.
	slices.SortFunc(metas, func(a, b *UnifiedMeta) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return metas, listErr
}

// SessionListPage is the ADR-057 FR-092 store-layer pagination result
// (W16a): a bounded window of ListSessions' full recency-ordered sequence,
// plus enough information for a caller to fetch the next page. Additive —
// ListSessions() itself keeps its existing zero-arg, "return everything"
// signature so every caller outside this migration's own session-list
// pipeline (usage aggregation, boot sweep, admin tooling, and the many other
// consumers `ListSessions()` already has across the tree) keeps compiling
// unchanged. ListSessionsPage is the primitive AgentLoop.ListAllSessions
// (pkg/agent/loop.go, U9, FR-098) is meant to call once it adopts
// pagination end to end; U6 does not — cannot, per ownership Rule 2 — edit
// that file itself.
type SessionListPage struct {
	// Sessions is this page's window, in the same UpdatedAt-descending,
	// id-tiebroken order ListSessions() returns.
	Sessions []*UnifiedMeta
	// NextOffset is the offset to request for the next page, or -1 when
	// this page reached the end of the sequence.
	NextOffset int
	// Total is the full sequence length (post FR-097a prune) at the moment
	// this page was computed — informational only; callers MUST NOT assume
	// it is stable across calls on a live store.
	Total int
}

// ListSessionsPage returns a stably-ordered window of ListSessions' result
// (FR-092/FR-098's "window correctness + stability", #100(a)): two identical
// calls with no intervening write return a byte-identical window, because
// the underlying ListSessions() ordering is itself now a total order (see
// its doc comment). It performs the EXACT SAME reconcile + FR-097a prune
// pass as ListSessions() — this is a bounded VIEW over that same call, not a
// second listing mechanism, so it inherits ListSessions' consistency model
// (FR-086) and its zero-per-session-disk-read warm-cache cost (FR-058/
// FR-103) unchanged.
//
// limit <= 0 means "no limit" — the remainder of the sequence from offset is
// returned in one page. offset < 0 is treated as 0. An offset at or beyond
// the end of the sequence returns an empty, non-nil Sessions slice with
// NextOffset == -1 — not an error.
func (us *UnifiedStore) ListSessionsPage(offset, limit int) (SessionListPage, error) {
	all, err := us.ListSessions()
	if offset < 0 {
		offset = 0
	}
	total := len(all)
	if offset >= total {
		return SessionListPage{Sessions: []*UnifiedMeta{}, NextOffset: -1, Total: total}, err
	}
	end := total
	if limit > 0 && offset+limit < total {
		end = offset + limit
	}
	nextOffset := -1
	if end < total {
		nextOffset = end
	}
	page := make([]*UnifiedMeta, end-offset)
	copy(page, all[offset:end])
	return SessionListPage{Sessions: page, NextOffset: nextOffset, Total: total}, err
}

// IsOrphan reports whether meta's ParentSessionID names a session that no
// longer exists in this store's metaCache (BDD-106: "an orphaned child is
// listed as a root, not dropped" — the U18/REST listing layer's roots-only
// view needs to know this to avoid silently hiding such a child forever).
// O(1), no disk read — a direct FR-097-adjacent metaCache membership check.
// Returns false for a root (empty ParentSessionID) and for a nil meta.
func (us *UnifiedStore) IsOrphan(meta *UnifiedMeta) bool {
	if meta == nil || meta.ParentSessionID == "" {
		return false
	}
	us.cacheMu.RLock()
	defer us.cacheMu.RUnlock()
	_, parentExists := us.metaCache[meta.ParentSessionID]
	return !parentExists
}

// DeleteSession removes a single session directory from the store.
// It also cascade-deletes the corresponding uploads directory
// (~/.omnipus/uploads/{sessionID}/) when it exists; failure to remove uploads
// is logged but does not fail the operation — the session data is gone either way.
// Returns an error if the session does not exist or cannot be removed.
func (us *UnifiedStore) DeleteSession(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()

	dir := filepath.Join(us.baseDir, sessionID)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("unified_store: session %q not found", sessionID)
		}
		return fmt.Errorf("unified_store: stat session %q: %w", sessionID, err)
	}
	// ADR-057 U6 W24 (FR-064) — DeleteSession's stated ordered sequence,
	// step 2 of 6 ("flush the session's dirty stats under that shard"),
	// BEFORE the directory removal in step 3. This still holds sessionID's
	// shard (h, acquired above), so a concurrent periodic-flusher tick that
	// already snapshotted this id as dirty either loses the shard race and
	// finds the cache entry gone once it gets the shard (skips, no write —
	// see u6FlushDirtySessionLocked), or wins it and flushes before this
	// call even starts. A flush failure here is logged, not returned: the
	// session is being deleted either way, so a stale/never-written
	// stats.json is moot the instant os.RemoveAll below succeeds.
	if err := us.u6FlushDirtySessionLocked(sessionID); err != nil {
		slog.Warn("unified_store: forced stats flush before delete failed",
			"session_id", sessionID, "error", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("unified_store: delete session %q: %w", sessionID, err)
	}
	us.cacheMu.Lock()
	delete(us.metaCache, sessionID)
	delete(us.dirtyStats, sessionID) // step 5: drop the dirty-set entry too
	us.cacheMu.Unlock()
	us.u4IndexEvict(sessionID) // FR-097: drop sessionID from the parent index either as a parent or as a child
	contextFile := filepath.Join(us.baseDir, ".context", sessionID+".jsonl")
	os.Remove(contextFile) // best-effort, ignore error if file does not exist

	// Cascade-delete uploads that were associated with this session.
	// Uploads are always home-rooted at <homePath>/uploads/<sessionID> regardless
	// of the store's baseDir depth (ADR-017 D5, N-B fix).
	uploadsDir := filepath.Join(us.uploadsRoot(), sessionID)
	if rmErr := os.RemoveAll(uploadsDir); rmErr != nil && !os.IsNotExist(rmErr) {
		slog.Warn("unified_store: delete session: cascade-delete uploads failed",
			"session_id", sessionID, "uploads_dir", uploadsDir, "error", rmErr)
	}
	return nil
}

// removeAllFn is a package-level test seam for ClearAll's per-session-directory
// removal call. It defaults to os.RemoveAll; tests override it to force a
// deterministic removal failure without depending on OS permission enforcement
// (which root bypasses via CAP_DAC_OVERRIDE, making a chmod-based
// failure-injection test a no-op in CI, which runs as root). Scoped narrowly
// to ClearAll's one call site — not a general refactor of the package's other
// os.RemoveAll/os.ReadDir calls.
var removeAllFn = os.RemoveAll

// ClearAll removes every session directory from the store.
// Returns the number of sessions removed and, if one or more session
// directories could not be removed, a non-nil aggregate error (via
// errors.Join) describing every such failure. A per-entry removal failure
// does not abort the operation — it is logged via slog.Warn and the loop
// continues to the next entry — but the failure is still surfaced to the
// caller so a "clear all sessions" request cannot silently under-deliver on
// this privacy-sensitive, destructive action.
// Locking (FR-050(a) exception): ClearAll takes ALL 64 session shards, in
// strictly ascending index order, via lockAllSessionShards — never
// lockSession, which would deadlock (sync.Mutex is not reentrant and every
// shard is already held by this goroutine). metaCache/cacheLoadFailures/the
// parent index are still touched only under cacheMu, exactly as everywhere
// else — holding every session shard does not exempt this method from that
// rule, it only means no OTHER goroutine can be mid-mutation on any session
// while this runs.
func (us *UnifiedStore) ClearAll() (int, error) {
	unlock := us.lockAllSessionShards()
	defer unlock()

	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("unified_store: clear all: read dir: %w", err)
	}

	uploadsRoot := us.uploadsRoot()
	removed := 0
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".context" {
			continue
		}
		dir := filepath.Join(us.baseDir, entry.Name())
		if err := removeAllFn(dir); err != nil {
			slog.Warn("unified_store: clear all: remove session dir", "dir", dir, "error", err)
			errs = append(errs, fmt.Errorf("unified_store: clear all: remove session dir %q: %w", entry.Name(), err))
			continue
		}
		us.cacheMu.Lock()
		delete(us.metaCache, entry.Name())
		delete(us.dirtyStats, entry.Name())
		us.cacheMu.Unlock()
		us.u4IndexEvict(entry.Name()) // FR-097
		contextFile := filepath.Join(us.baseDir, ".context", entry.Name()+".jsonl")
		os.Remove(contextFile) // best-effort, ignore error if file does not exist
		// Cascade-delete uploads for this session.
		uploadsDir := filepath.Join(uploadsRoot, entry.Name())
		if rmErr := os.RemoveAll(uploadsDir); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("unified_store: clear all: cascade-delete uploads failed",
				"session_id", entry.Name(), "error", rmErr)
		}
		removed++
	}

	// Evict any remaining cache entry whose on-disk directory is already gone.
	// The per-dir loop above only deletes cache entries for directories THIS
	// call physically removed; when several stores share one baseDir (e.g.
	// multiple agents configured with the same workspace), whichever store's
	// ClearAll runs first removes the shared directories, and the others then
	// find nothing to remove and never evict their now-stale cache entries —
	// leaving ListSessions to resurrect sessions that are gone from disk.
	// Entries whose directory survived (a failed removal above) are kept, since
	// those sessions genuinely still exist.
	us.cacheMu.RLock()
	remaining := make([]string, 0, len(us.metaCache))
	for id := range us.metaCache {
		remaining = append(remaining, id)
	}
	us.cacheMu.RUnlock()
	for _, id := range remaining {
		if _, statErr := os.Stat(filepath.Join(us.baseDir, id)); errors.Is(statErr, os.ErrNotExist) {
			us.cacheMu.Lock()
			delete(us.metaCache, id)
			delete(us.dirtyStats, id)
			us.cacheMu.Unlock()
			us.u4IndexEvict(id) // FR-097
		}
	}

	return removed, errors.Join(errs...)
}

// --- ADR-057 FR-097: in-memory session parent index (surface) ---
//
// See UnifiedStore's parentIndex/childToParent field doc comment above for
// the full design. Every method below assumes it is called OUTSIDE any
// cacheMu critical section (it takes cacheMu itself) and OUTSIDE
// lockAllSessionShards' held-shards window is NOT required —
// DeleteSession/ClearAll/RetentionSweep call these after releasing cacheMu
// but while still holding the relevant session shard(s), which is fine: the
// lock order is sessionLock -> cacheMu, and these methods only ever take
// cacheMu.

// u4IndexAddChild registers childID as a direct child of parentID in the
// FR-097 parent index. A no-op when parentID is empty (a root has no parent
// to register under) or when parentID == childID (a malformed self-parent
// must never be indexed — it would make ChildCount and the eventual
// roots-only listing walk into themselves). Idempotent: registering the same
// pair twice is harmless.
//
// WIRED (ADR-057 U5, Wave C): called from u5WriteIdentityLocked
// (unified_meta_files.go) every time a session's identity group is written
// — see the parentIndex field's doc comment above for the full call-site
// list. This is the ADD half of the FR-097 surface; u4IndexEvict (below)
// remains the DELETE/EVICT half, wired by U4 into
// DeleteSession/ClearAll/RetentionSweep.
func (us *UnifiedStore) u4IndexAddChild(parentID, childID string) {
	if parentID == "" || parentID == childID {
		return
	}
	us.cacheMu.Lock()
	defer us.cacheMu.Unlock()
	if us.parentIndex[parentID] == nil {
		us.parentIndex[parentID] = make(map[string]struct{})
	}
	us.parentIndex[parentID][childID] = struct{}{}
	us.childToParent[childID] = parentID
}

// u4IndexEvict removes id from the FR-097 parent index entirely, handling
// both roles id may hold:
//
//   - If id is itself a tracked CHILD of some parent, that edge is dropped
//     (the parent's child_count decrements — dataset row 5, "DeleteSession on
//     a child").
//   - If id is itself a tracked PARENT, every one of its former children has
//     its own childToParent entry cleared, making each an orphan ROOT at the
//     index level (dataset row 6, "DeleteSession on the parent"). Their own
//     on-disk ParentSessionID is untouched here — patching that (once FR-008
//     lands) belongs to whichever unit wires the write path, not this
//     read/evict-only surface.
//
// Called whenever id's metaCache entry is deleted for a reason that means
// the session itself is gone (DeleteSession, ClearAll, RetentionSweep's
// empty-dir removal) — i.e. FR-097's "delete" and "eviction" mutation
// points that this unit owns. Idempotent and safe to call for an id that was
// never indexed at all.
func (us *UnifiedStore) u4IndexEvict(id string) {
	us.cacheMu.Lock()
	defer us.cacheMu.Unlock()
	if parentID, ok := us.childToParent[id]; ok {
		if kids := us.parentIndex[parentID]; kids != nil {
			delete(kids, id)
			if len(kids) == 0 {
				delete(us.parentIndex, parentID)
			}
		}
		delete(us.childToParent, id)
	}
	if kids, ok := us.parentIndex[id]; ok {
		for childID := range kids {
			delete(us.childToParent, childID)
		}
		delete(us.parentIndex, id)
	}
}

// ChildCount returns the number of DIRECT children sessionID has in the
// FR-097 parent index, resolved in O(1) — the mechanism FR-091's per-root
// child_count and FR-106-style orphan detection need. Returns 0 for a
// session with no tracked children (including one never indexed at all —
// the zero value of a missing map key). This is the "U6 consumes it for
// listing" half of FR-097's stated ownership split; U6's Wave D listing
// layer is the intended caller once it exists.
func (us *UnifiedStore) ChildCount(sessionID string) int {
	us.cacheMu.RLock()
	defer us.cacheMu.RUnlock()
	return len(us.parentIndex[sessionID])
}
