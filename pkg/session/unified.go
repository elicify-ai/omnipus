package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/memory"
)

// UnifiedSessionType classifies what created a session.
type UnifiedSessionType string

const (
	SessionTypeChat    UnifiedSessionType = "chat"
	SessionTypeTask    UnifiedSessionType = "task"
	SessionTypeChannel UnifiedSessionType = "channel"
	// SessionTypeScheduled classifies sessions created by a fired schedule
	// (issue #264, FR-005). isolated/continue scheduled runs use this type so
	// the SPA can badge them and group them separately from human chat/task/
	// channel sessions.
	SessionTypeScheduled UnifiedSessionType = "scheduled"
	// SessionTypeHeartbeat classifies the eager standing session created when
	// a workspace-scoped heartbeat is enabled (FR-010, A1/F-02, A2/F-11). The
	// session is stamped with workspace_id + agent + type="heartbeat" at create
	// time so the cron job can continue it (via JobSpec.SessionID) rather than
	// minting a fresh session each run. The SPA pins sessions of this type to
	// the top of the Session panel and disables their delete control while the
	// heartbeat is active (FR-021, FR-028).
	SessionTypeHeartbeat UnifiedSessionType = "heartbeat"
	// SessionTypeVerifier classifies a session created for a verifier-role
	// adjudication (ADR-052 FR-036/US-13 Acceptance 6): the Judge System
	// Agent, or a future custom verifier, runs its own real agent-loop turn
	// in a session of this type rather than the old blind Provider.Chat
	// shortcut. Persisted with normal 90-day retention but hidden by default
	// from GET /api/v1/sessions (see the include_verifier query param);
	// Sidebar and SearchModal always exclude it, UsageScreen INCLUDES it
	// (verifier LLM spend must stay visible, SC-014), and the ActivityPanel /
	// verdict drill-down surface it on demand. Client-created via POST
	// /api/v1/sessions is intentionally NOT supported for this type
	// (SessionCreateRequest.yaml's type enum is narrower than this one, by
	// design — mirrors the existing "scheduled" precedent) — only the engine
	// mints verifier sessions, via NewVerifierSession below.
	SessionTypeVerifier UnifiedSessionType = "verifier"
	// SessionTypeDelegate classifies a subordinate session minted for a
	// delegated child sub-turn (ADR-057 FR-005/FR-008/FR-009): every
	// delegated child now owns its OWN real, store-backed session (D1) with
	// ParentSessionID naming its direct parent, rather than sharing the
	// parent's transcript.jsonl. The literal MUST be exactly "delegate" —
	// U10 already generated the matching OpenAPI enum member
	// (SessionTypeDelegate SessionType = "delegate",
	// pkg/api/generated/openapi_types.gen.go) from contracts/, and any other
	// spelling here would silently drift the Go implementation from the
	// wire contract with no compiler error to catch it.
	SessionTypeDelegate UnifiedSessionType = "delegate"
)

// IsValidSessionType reports whether t is one of the known session types.
// New types must be added here and to the const block above so every
// validation/listing site accepts them.
func IsValidSessionType(t UnifiedSessionType) bool {
	switch t {
	case SessionTypeChat, SessionTypeTask, SessionTypeChannel, SessionTypeScheduled, SessionTypeHeartbeat, SessionTypeVerifier, SessionTypeDelegate:
		return true
	default:
		return false
	}
}

// MetaPatch is a partial update applied to a session's meta.json.
// Only non-nil fields are written.
type MetaPatch struct {
	Title  *string
	Status *SessionStatus
	TaskID *string
	// Owner stamps the authenticated user who created the session.
	// Only written when non-nil; empty string is a valid value (clears ownership).
	Owner *string
	// WorkspaceID tags the session with the active workspace (M4 workspace→turn
	// binding). Only written when non-nil; empty string clears the tag.
	WorkspaceID *string

	// InstanceID patches the channel instance key. Needed so a session created
	// before the field existed can be identified later rather than guessed at.
	InstanceID *string
	// ParentSessionID stamps this session's direct parent (ADR-057 FR-008).
	// Only written when non-nil; empty string clears it (making the session
	// a root again). The write path also wires the FR-097 in-memory parent
	// index (see u5WriteIdentityLocked in unified_meta_files.go) — setting
	// this via SetMeta is a fully-indexed operation, not just a field write.
	ParentSessionID *string

	// ADR-086 GOAL-FR-005 (wave S6, "the deletion half"): every Goal*
	// field that used to live here (GoalID, GoalCondition, GoalRoundsUsed,
	// GoalMaxRounds, GoalLatestReason, GoalStartedAt, GoalLastActivityAt,
	// GoalCriteriaJSON, GoalQuestionRoundsUsed, GoalZeroOutputPushes, and
	// the four GoalRoute* fields) is RETIRED — the goal is its own stored
	// entity now (pkg/goal.Store), not a patch you apply to a session.
	//
	// PendingAskJSON is the AskUserQuestion durable pending set (see
	// SessionMeta.PendingAskJSON). Pass an empty string to CLEAR it.
	// ADR-086 GOAL-FR-005 (wave S2): despite once sitting among the Goal*
	// fields above in this struct's declaration order, this field was never
	// goal state — it is session-scoped interaction state, and persists to
	// its own pending_ask.json group (pendingAskTouched/
	// u5WritePendingAskLocked, unified.go/pending_ask.go), unaffected by
	// this wave's deletion.
	PendingAskJSON *string

	// Loop fields (ADR-049 D6/D7, /loop). Only non-nil fields are written;
	// callers that want to CLEAR a loop must pass an empty-string LoopMode
	// explicitly.
	LoopMode           *string
	LoopPrompt         *string
	LoopRunCount       *int
	LoopMaxRuns        *int
	LoopIntervalMS     *int64
	LoopNextDelayMS    *int64
	LoopJobID          *string
	LoopStartedAt      *string
	LoopLastActivityAt *string
}

// UnifiedMeta extends SessionMeta with the session type field.
// It is JSON-compatible with SessionMeta (same file, additional fields).
type UnifiedMeta struct {
	SessionMeta
	Type UnifiedSessionType `json:"type"`
}

// Clone returns a deep, independent copy of m. Nil-safe (returns nil for a
// nil receiver). This is the sole mechanism by which UnifiedStore.metaCache
// entries are handed to or accepted from callers: scalar fields copy by
// value via the struct assignment, but slice/map fields alias their backing
// array/storage unless cloned explicitly, so every reference field on
// UnifiedMeta/SessionMeta/SessionStats is deep-copied here.
//
// This is load-bearing, not polish: NewChannelSession mutates
// meta.PeerID/meta.Title on the pointer returned by NewSession without
// holding any lock (see its doc comment). If the cache aliased that pointer,
// a concurrent ListSessions/GetMeta clone-read racing that mutation would be
// a real -race failure. Cloning on every cache insert and cache read keeps
// the cache and every caller's copy fully independent in both directions.
// unifiedMetaCloneCalls counts Clone() invocations process-wide — a
// test-only observability seam (14-reviewer finding #3) proving
// ListSessionsFiltered clones ONLY the entries its predicate approves,
// rather than cloning every cached session and filtering afterward. The
// atomic increment is unconditional and cheap (a single atomic add), so it
// is never gated behind a test-only code path — same convention as
// pkg/session/message_inbox.go's messageInboxPeekEnvelopeCalls seam. Clone
// is called pervasively throughout this file (every GetMeta, every writer's
// cache-miss fallback, etc.), so a test must measure the DELTA around one
// specific call, never a raw total.
var unifiedMetaCloneCalls atomic.Int64

func (m *UnifiedMeta) Clone() *UnifiedMeta {
	if m == nil {
		return nil
	}
	unifiedMetaCloneCalls.Add(1)
	c := *m
	c.Partitions = slices.Clone(m.Partitions)
	c.AgentIDs = slices.Clone(m.AgentIDs)
	c.CompactionSummaries = maps.Clone(m.CompactionSummaries)
	c.Stats.ByModel = maps.Clone(m.Stats.ByModel)
	return &c
}

// UnifiedStore manages per-session directories under a base directory.
// Each session has: meta.json, context.jsonl (agent loop), transcript.jsonl (UI).
//
// It implements SessionStore so the agent loop works unchanged, and adds
// UI-oriented methods (NewSession, AppendTranscript, ReadTranscript, etc.).
//
// Locking (ADR-057 FR-048, replacing the single pre-ADR-057 us.mu
// sync.RWMutex): two independent primitives, never a store-global lock.
//
//   - sessionLocks (unified_lock.go) is a 64-shard per-session mutex pool.
//     Every method that mutates or reads a SINGLE session's on-disk state
//     acquires that session's shard via lockSession(id) for the duration of
//     its filesystem work — concurrent operations on DIFFERENT sessions no
//     longer contend with each other (this is the R-8 fix: a wide delegation
//     fan-out no longer stalls token streaming in every other session).
//   - cacheMu is a narrow sync.RWMutex guarding ONLY metaCache,
//     cacheLoadFailures, and the FR-097 parent index (parentIndex/
//     childToParent) below. It is NEVER held across an os.* or fileutil.*
//     call (FR-049) — every cacheMu critical section is a tight map
//     read/write with no I/O inside it.
//
// Lock order (FR-050): sessionLock(id) -> cacheMu, one-directional. Two
// session shards are never held simultaneously except by ClearAll/
// RetentionSweep (which take all 64 in ascending index order via
// lockAllSessionShards) and by U2's CreateSessionWithID parent-Owner copy
// (FR-082: read under lockSession(parent), Unlock, THEN lockSession(child)).
// See unified_lock.go's doc comment for the full design and the FR-101
// lock-order observation seam.
type UnifiedStore struct {
	// sessionLocks is the FR-048(a) 64-shard per-session mutex pool. See
	// unified_lock.go's lockSession/lockAllSessionShards.
	sessionLocks u4SessionStripedLock
	baseDir      string // {workspace}/sessions/
	homePath     string // ~/.omnipus/ — uploads cascade-delete root (home-rooted per rest.go:4352)
	backend      *memory.JSONLStore

	// cacheMu is the FR-048(b) narrow lock guarding metaCache,
	// cacheLoadFailures and the FR-097 parent index ONLY — see this struct's
	// doc comment above for the full lock-order contract.
	cacheMu sync.RWMutex

	// metaCache holds one independent clone per session, keyed by session ID.
	// It is the primary data source for ListSessions/GetOrCreateScheduledSession's
	// existence check and GetMeta's fast path — avoiding the O(N) os.ReadDir +
	// per-session disk read that ListSessions previously did under the single
	// store-wide lock on every call. Populated via four paths: once at
	// construction (loadMetaCacheLocked); on every successful mutation via
	// one of the ADR-057 W23 targeted field-group writers
	// (u5WriteIdentityLocked/u5WriteStatsLocked/u5WriteLoopLocked,
	// unified_meta_files.go; u5WritePendingAskLocked, pending_ask.go) — NOT
	// a single funnel anymore (FR-059/FR-084): each writer updates ONLY its
	// own field group on the cached entry and never replaces it wholesale,
	// so a loop tick no longer touches this entry's Stats field and a
	// transcript append no longer touches its Loop fields; via
	// readMetaLocked self-healing
	// the cache on a cache-miss disk read (SetMeta, SwitchAgent,
	// AppendTranscript, GetOrCreateScheduledSession, and GetMeta's cache-miss
	// path all reach the cache this way, composing across all four on-disk
	// group files — see readUnifiedMeta); and via ListSessions' own
	// reconciliation pass, which uses a directory-NAMES-only os.ReadDir to
	// find sessions written directly to disk out-of-band (bypassing this
	// store) and self-heals them into the cache the same way readMetaLocked
	// does — without any per-file disk read for entries already cached
	// (FR-058/FR-103, verified zero on a cache hit). Explicit eviction on
	// DeleteSession, ClearAll, and RetentionSweep's empty-dir removal.
	// Guarded by cacheMu (was: us.mu).
	metaCache map[string]*UnifiedMeta

	// cacheLoadFailures counts sessions whose meta.json failed to read/parse
	// at construction time (loadMetaCacheLocked), even after one retry
	// (MB-2). Such a session is excluded from metaCache — and therefore from
	// ListSessions — for this UnifiedStore's entire process lifetime; a
	// transient blip that would have cleared up moments later does NOT
	// self-correct without a restart. This counter makes that accepted
	// limitation assertable/observable instead of a silent gap. See
	// CacheLoadFailureCount. Guarded by cacheMu (was: us.mu).
	cacheLoadFailures int

	// parentIndex/childToParent are the ADR-057 FR-097 in-memory parent
	// index: parentIndex maps a parent session id to the set of its DIRECT
	// children, and childToParent is its reverse (child id -> parent id),
	// letting both ChildCount and "is this a tracked child" resolve in O(1)
	// without a reverse scan. Guarded by the SAME cacheMu that guards
	// metaCache — deliberately NOT its own lock (contrast
	// lifecycle_index.go's lifecycleParentIndex, which has its own mutex),
	// because a session's cache entry and its parent-index membership must
	// never be observable out of sync with each other, and sharing one lock
	// is the simplest way to guarantee that.
	//
	// FULLY WIRED as of ADR-057 U5 (Wave C): U4 (Wave B) built this surface
	// with only the DELETE/EVICT side wired (u4IndexEvict, into
	// DeleteSession/ClearAll/RetentionSweep below) because
	// SessionMeta.ParentSessionID did not exist yet. It now does (FR-008/W2,
	// daypartition.go), and the ADD side (u4IndexAddChild) is called from
	// u5WriteIdentityLocked (unified_meta_files.go) — the ADR-057 W23
	// targeted identity-group writer, itself called from every path that can
	// mint or re-key a session's identity group: createSessionLocked,
	// SetMeta (including a bare Owner/Title patch — u4IndexAddChild is a
	// no-op when ParentSessionID is unchanged/empty, so this is safe to call
	// unconditionally rather than only when the patch touches
	// ParentSessionID specifically), SwitchAgent, NewChannelSession, and
	// unified_api.go's CreateSessionWithID/AppendTranscriptStrict call sites
	// via the writeMetaLocked dispatcher. U6 consumes ChildCount for
	// roots-only listing (FR-097's stated ownership split: "U4 creates the
	// index surface ... U6 consumes it for listing").
	parentIndex   map[string]map[string]struct{}
	childToParent map[string]string

	// --- ADR-057 U6 (Wave D), W24 — the FR-061...FR-067 stats-flush
	// throttle. See unified_stats_flush.go for the mechanics; the fields
	// live here because UnifiedStore itself does. ---

	// dirtyStats tracks sessions with an unflushed in-memory-only Stats
	// delta (FR-061): AppendTranscript's per-token counter bump no longer
	// writes stats.json synchronously — it mutates the cached entry via
	// u6MarkStatsDirtyLocked and records the session id here instead. The
	// periodic flusher (runStatsFlusher) and every forced-flush point
	// (SetMeta w/ Status, DeleteSession, Close, FlushSessionStats) drain
	// this set. Guarded by the SAME cacheMu that guards metaCache — a
	// session's dirty membership must never be observable out of sync with
	// its cached Stats.
	dirtyStats map[string]struct{}

	// flushMu guards statsFlushInterval/flusherStarted/flushStopCh/
	// flushDoneCh below. Deliberately a SEPARATE lock from cacheMu/
	// sessionLocks — it protects the flusher's own control-plane state, not
	// session data, so it is never part of the sessionLock -> cacheMu order
	// and is never held across an os.*/fileutil.* call either.
	flushMu            sync.Mutex
	statsFlushInterval time.Duration
	flusherStarted     bool
	flushStopCh        chan struct{}
	flushDoneCh        chan struct{}
	// flushTimer is the flusher goroutine's LIVE timer. SetStatsFlushInterval
	// resets it directly so an override takes effect immediately rather than
	// only after the in-flight wait (started with whatever interval was
	// current when the timer was last armed) happens to elapse on its own.
	flushTimer *time.Timer
}

// CacheLoadFailureCount returns the number of sessions that failed to load
// into metaCache at construction time (after one retry) — see
// loadMetaCacheLocked's doc comment for the accepted limitation this
// signals. Safe to call concurrently with any other UnifiedStore method.
func (us *UnifiedStore) CacheLoadFailureCount() int {
	us.cacheMu.RLock()
	defer us.cacheMu.RUnlock()
	return us.cacheLoadFailures
}

// BaseDir returns the root directory of this store.
// Exported for tests that need to create fixture files directly in the store.
func (us *UnifiedStore) BaseDir() string {
	return us.baseDir
}

// writeFileAtomicFn is a package-level test seam for the ADR-057 W23 /
// ADR-086 GOAL-FR-005 targeted meta-group writers' (u5WriteIdentityLocked/
// u5WriteStatsLocked/u5WriteLoopLocked, unified_meta_files.go;
// u5WritePendingAskLocked, pending_ask.go) disk write
// step. It defaults to fileutil.WriteFileAtomic; tests override it to force
// a deterministic write failure (the MB-1 cache/disk-divergence regression
// guard) without depending on OS permission enforcement — same rationale as
// removeAllFn above (root bypasses chmod-based failure injection via
// CAP_DAC_OVERRIDE in CI).
var writeFileAtomicFn = fileutil.WriteFileAtomic

// readFileFn is the ADR-057 FR-103 read-side mirror of writeFileAtomicFn: a
// package-level seam every meta-group file read (u5ReadIdentityFile/
// u5ReadStatsFile/u5ReadLoopFile, unified_meta_files.go;
// u5ReadPendingAskFile, pending_ask.go) goes through, defaulting to
// os.ReadFile. `[grill2 M2-6]` Before this file split, the store had an
// injectable WRITE seam but no injectable READ seam, so neither test #103's
// "a cache hit performs zero disk reads" assertion nor FR-092's
// bounded-cost clause (c) — which reuses the same counter — was
// constructible: there was nothing to instrument. A test installs a
// counting wrapper here to prove GetMeta/ListSessions never touch this
// function on a warm cacheMu hit.
var readFileFn = os.ReadFile

// validateSessionID rejects IDs that could escape the base directory.
func validateSessionID(id string) error {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") ||
		strings.Contains(id, "..") || id == "." || id == ".context" {
		return fmt.Errorf("unified_store: invalid session ID %q", id)
	}
	return nil
}

// NewUnifiedStore creates a UnifiedStore rooted at baseDir.
// It migrates legacy flat JSONL files if any are found.
// The agentID is no longer baked into the store — callers pass it per-operation
// (e.g., NewSession receives creatingAgentID).
//
// The uploads cascade-delete path is derived as filepath.Dir(baseDir)/uploads,
// which is correct when baseDir is directly under the home directory (e.g.,
// <home>/sessions). For per-agent stores whose baseDir is deeper in the tree
// (e.g., <home>/agents/<id>/sessions), use NewUnifiedStoreWithHome so that
// upload files are found at the correct <home>/uploads/<sessionID> path.
func NewUnifiedStore(baseDir string) (*UnifiedStore, error) {
	return NewUnifiedStoreWithHome(baseDir, filepath.Dir(filepath.Clean(baseDir)))
}

// NewUnifiedStoreWithHome creates a UnifiedStore rooted at baseDir whose
// upload files live under homePath/uploads/<sessionID>.
//
// Use this constructor when baseDir is not a direct child of homePath (e.g.,
// per-agent stores at <home>/agents/<id>/sessions). The homePath ensures that
// cascade-deletes on DeleteSession, ClearAll, and RetentionSweep always remove
// files from the correct location regardless of the store's baseDir depth.
func NewUnifiedStoreWithHome(baseDir, homePath string) (*UnifiedStore, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("unified_store: create base dir %q: %w", baseDir, err)
	}

	// The JSONL backend for context.jsonl lives in a sub-directory so its
	// flat .jsonl files don't collide with session sub-directories.
	contextDir := filepath.Join(baseDir, ".context")
	store, err := memory.NewJSONLStore(contextDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: init context backend: %w", err)
	}

	us := &UnifiedStore{
		baseDir:       baseDir,
		homePath:      homePath,
		backend:       store,
		metaCache:     make(map[string]*UnifiedMeta),
		parentIndex:   make(map[string]map[string]struct{}),
		childToParent: make(map[string]string),
		dirtyStats:    make(map[string]struct{}),
	}

	us.migrateLegacy()
	us.loadMetaCacheLocked()
	// ADR-057 U6 W24 (FR-063): every store gets a running periodic flusher
	// from construction — a fresh install with no operator override still
	// converges stats.json every DefaultSessionStatsFlushInterval (5s, see
	// unified_stats_flush.go) with zero wiring required by the caller.
	// SetStatsFlushInterval overrides the period later, e.g. once a caller
	// has resolved config.SessionConfig.EffectiveStatsFlushInterval().
	us.startStatsFlusher()
	return us, nil
}

// loadMetaCacheLocked scans baseDir once and populates metaCache with every
// session's metadata. Called from the constructor, after migrateLegacy, so
// this moves the O(N) directory scan + per-session disk read from every
// future ListSessions call to a single one-time cost at store construction.
//
// Despite the "Locked" naming convention shared with readMetaLocked/
// writeMetaLocked, no lock is held or required here: us has not yet escaped
// the constructor, so no other goroutine can reach it concurrently.
//
// A session whose meta.json fails to read/parse is retried ONCE (MB-2) —
// this alone absorbs a transient boot-time blip, e.g. a concurrent writer
// mid-rename — before being treated as genuinely unreadable. If it still
// fails after the retry, this is logged at Error, not Warn: unlike the
// pre-cache behavior, where ListSessions re-scanned disk on every call so a
// transient failure would self-correct on the very next call, a session
// excluded here is excluded from ListSessions for this UnifiedStore's ENTIRE
// PROCESS LIFETIME — only a restart re-runs this scan and gives it another
// chance. That permanent-until-restart exclusion is also counted in
// cacheLoadFailures (see CacheLoadFailureCount) so it is assertable/
// observable rather than a silent gap. This is an accepted, minimal
// mitigation, not a full fix — a periodic reconciler that keeps retrying in
// the background would close the gap completely but is out of scope for
// this fix round; store construction (and therefore gateway boot) is still
// never aborted over one bad session directory.
func (us *UnifiedStore) loadMetaCacheLocked() {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("unified_store: load meta cache: read base dir", "dir", us.baseDir, "error", err)
		}
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".context" {
			continue
		}
		sessionDir := filepath.Join(us.baseDir, entry.Name())
		meta, err := readUnifiedMeta(sessionDir)
		if err != nil {
			// One retry: absorbs a transient boot-time blip before we give up.
			meta, err = readUnifiedMeta(sessionDir)
		}
		if err != nil {
			us.cacheLoadFailures++
			slog.Error(
				"unified_store: load meta cache: session unreadable after retry — excluded until restart",
				"dir", entry.Name(), "error", err,
			)
			continue
		}
		us.metaCache[entry.Name()] = meta
		// FR-097 (Defect-1 fix): wire the parent index for every session
		// composed here. This is the boot/restart-time cache-population
		// path — until this fix it was one of TWO paths (alongside
		// readMetaLocked's cache-miss branch) that populated metaCache
		// without ever touching parentIndex/childToParent. A prior process's
		// Persist-time maintenance (u5WriteIdentityLocked -> u4IndexAddChild)
		// only ever updated THAT process's in-memory index, which dies with
		// it; a NEW UnifiedStore over the same baseDir must rebuild the
		// index from the meta.json this scan just composed, or every
		// parent->child edge stays correct on disk (ParentSessionID
		// round-trips fine) while ChildCount silently reports 0 for every
		// parent until the child is re-written — the "success-shaped"
		// silent failure this whole spec exists to close out (see
		// lifecycle_index.go's ensureWarm doc comment for the sibling index
		// that already got this right). Safe to call unconditionally here:
		// u4IndexAddChild no-ops for an empty ParentSessionID and is
		// idempotent, and taking cacheMu per-entry is harmless since no
		// other goroutine can reach us yet (see this method's own doc
		// comment above).
		us.u4IndexAddChild(meta.ParentSessionID, entry.Name())
	}
}

// uploadsRoot returns the root directory for upload files associated with
// sessions in this store. Uploads are always home-rooted at
// <homePath>/uploads/ (matching rest.go:4352) regardless of the store's
// baseDir depth in the directory tree.
func (us *UnifiedStore) uploadsRoot() string {
	if us.homePath != "" {
		return filepath.Join(us.homePath, "uploads")
	}
	// Fallback: derive from baseDir (correct only for stores directly under home).
	return filepath.Join(filepath.Dir(filepath.Clean(us.baseDir)), "uploads")
}

// migrateLegacy scans for old flat JSONL files and wraps each in a session directory.
func (us *UnifiedStore) migrateLegacy() {
	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".jsonl")
		sessionDir := filepath.Join(us.baseDir, name)
		if mkErr := os.MkdirAll(sessionDir, 0o700); mkErr != nil {
			slog.Warn("unified_store: migrate: could not create dir", "name", name, "error", mkErr)
			continue
		}
		src := filepath.Join(us.baseDir, e.Name())
		dst := filepath.Join(sessionDir, "context.jsonl")
		if _, statErr := os.Stat(dst); statErr == nil {
			// Already migrated.
			continue
		}
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			slog.Warn("unified_store: migrate: could not read file", "path", src, "error", readErr)
			continue
		}
		if writeErr := fileutil.WriteFileAtomic(dst, data, 0o600); writeErr != nil {
			slog.Warn("unified_store: migrate: could not write context.jsonl", "path", dst, "error", writeErr)
			continue
		}
		now := time.Now().UTC()
		meta := &UnifiedMeta{
			SessionMeta: SessionMeta{
				ID:        name,
				Status:    StatusActive,
				CreatedAt: now,
				UpdatedAt: now,
			},
			Type: SessionTypeChat,
		}
		if writeMetaErr := writeUnifiedMetaDirect(sessionDir, meta); writeMetaErr != nil {
			slog.Warn("unified_store: migrate: could not write meta.json", "name", name, "error", writeMetaErr)
			continue
		}
		if removeErr := os.Remove(src); removeErr != nil {
			slog.Warn("unified_store: migrate: could not remove legacy file", "path", src, "error", removeErr)
		}
		slog.Info("unified_store: migrated legacy session", "id", name)
	}
}

// NewSession creates a new session directory with meta.json and empty files.
// creatingAgentID is the agent that owns this session initially; it is stored
// as AgentID (legacy compat), AgentIDs[0], and ActiveAgentID.
func (us *UnifiedStore) NewSession(
	sessionType UnifiedSessionType,
	channel string,
	creatingAgentID string,
) (*UnifiedMeta, error) {
	sessionID, err := NewSessionID()
	if err != nil {
		return nil, err
	}

	h := us.lockSession(sessionID)
	defer h.Unlock()
	return us.createSessionLocked(sessionID, sessionType, channel, creatingAgentID)
}

// NewChannelSession creates a new shared session for (channel, peerID).
// Unlike NewSession it writes PeerID and Title atomically so the caller does
// not need a follow-up SetMeta call.
// NewChannelSession creates a channel session.
//
// instanceID is the channel INSTANCE key (e.g. "whatsapp.eu"), not the bare
// type. It is separate from channel because an install can hold many instances
// of one platform, each bound to its own (workspace, agent) pair — without it,
// their sessions are indistinguishable and anything acting on "this channel's
// sessions" acts on all of them.
func (us *UnifiedStore) NewChannelSession(channel, instanceID, peerID, agentID, title string) (*UnifiedMeta, error) {
	meta, err := us.NewSession(SessionTypeChannel, channel, agentID)
	if err != nil {
		return nil, err
	}
	meta.InstanceID = instanceID
	meta.PeerID = peerID
	meta.Title = title
	h := us.lockSession(meta.ID)
	// PeerID/Title are both identity-group fields (FR-053) — the targeted
	// identity writer, not the retired whole-document funnel.
	err = us.u5WriteIdentityLocked(meta.ID, meta)
	h.Unlock()
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// createSessionLocked creates a session directory with the EXACT supplied id,
// meta.json, and an empty transcript. Caller must hold sessionID's shard
// (see lockSession) — was: caller must hold us.mu.
func (us *UnifiedStore) createSessionLocked(
	sessionID string,
	sessionType UnifiedSessionType,
	channel string,
	creatingAgentID string,
) (*UnifiedMeta, error) {
	now := time.Now().UTC()
	meta := &UnifiedMeta{
		SessionMeta: SessionMeta{
			ID:            sessionID,
			AgentID:       creatingAgentID,
			AgentIDs:      []string{creatingAgentID},
			ActiveAgentID: creatingAgentID,
			Status:        StatusActive,
			Channel:       channel,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		Type: sessionType,
	}

	sessionDir := filepath.Join(us.baseDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("unified_store: create session dir: %w", err)
	}
	// ADR-057 W23 (FR-053/FR-054): a brand-new session has no loop/stats
	// activity yet, so only the identity group (meta.json) is written here —
	// stats.json/loop.json are created lazily by their own targeted writers
	// the first time something actually touches that group (dataset row 2:
	// meta.json only).
	if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
		return nil, err
	}
	// Create empty transcript so readers don't error on first access.
	transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
	if _, statErr := os.Stat(transcriptPath); os.IsNotExist(statErr) {
		if wErr := fileutil.WriteFileAtomic(transcriptPath, []byte{}, 0o600); wErr != nil {
			slog.Warn("unified_store: could not create empty transcript", "path", transcriptPath, "error", wErr)
		}
	}

	slog.Debug("unified_store: created session", "id", sessionID, "type", sessionType, "agent", creatingAgentID)
	return meta, nil
}

// NewScheduledSession mints a fresh isolated scheduled session (SessionTypeScheduled)
// with a freshly generated id, owned by ownerAgentID. This is the `isolated`
// session_mode primitive for fired schedules (issue #264, FR-005). It is a thin
// wrapper over NewSession that pins the type so callers don't have to remember it.
func (us *UnifiedStore) NewScheduledSession(ownerAgentID string) (*UnifiedMeta, error) {
	return us.NewSession(SessionTypeScheduled, "scheduled", ownerAgentID)
}

// NewHeartbeatSession eagerly creates the standing session for a workspace-
// scoped heartbeat (FR-010, A1/F-02, A2). It stamps:
//   - Type = SessionTypeHeartbeat
//   - WorkspaceID = workspaceID
//   - AgentID = agentID (also AgentIDs and ActiveAgentID)
//
// The caller (gateway workspace handler) stores the returned session's ID at
// member_configs[agentID].heartbeat.session_id so the cron reconciler can
// inject it into the JobSpec.SessionID field and continue the same session
// across every heartbeat run (FR-007b).
//
// Unlike NewScheduledSession, this variant accepts an explicit workspaceID so
// the session carries the correct workspace tag for the delete-guard lookup
// (FR-014) and the SPA's Session panel grouping (FR-021).
func (us *UnifiedStore) NewHeartbeatSession(workspaceID, agentID string) (*UnifiedMeta, error) {
	meta, err := us.NewSession(SessionTypeHeartbeat, "heartbeat", agentID)
	if err != nil {
		return nil, fmt.Errorf("session: new heartbeat session (workspace=%s agent=%s): %w", workspaceID, agentID, err)
	}
	// Stamp the workspace_id onto the meta so the delete-guard can load the
	// right workspace without scanning all workspaces (A2/G-01).
	if err := us.SetMeta(meta.ID, MetaPatch{WorkspaceID: &workspaceID}); err != nil {
		// MEDIUM-C: SetMeta failed — best-effort delete the half-initialized session
		// so a transient failure does not leave an orphaned session directory.
		if delErr := us.DeleteSession(meta.ID); delErr != nil {
			slog.Warn("session: cleanup of partial heartbeat session failed",
				"session_id", meta.ID, "workspace_id", workspaceID, "agent_id", agentID, "error", delErr)
		}
		return nil, fmt.Errorf("session: stamp workspace_id on heartbeat session %s: %w", meta.ID, err)
	}
	meta.WorkspaceID = workspaceID
	return meta, nil
}

// NewVerifierSession mints a fresh, isolated session of SessionTypeVerifier
// for a verifier-role adjudication (ADR-052 FR-036/US-13 Acceptance 1+6):
// runVerifierAdjudication (pkg/agent) creates one FRESH session per
// adjudication call — never reused across calls — so this is a thin
// "New", not "GetOrCreate", wrapper (mirroring NewScheduledSession's
// shape, not NewHeartbeatSession's reuse-by-id shape).
//
// ownerAgentID is the verifier agent actually running the turn (e.g. the
// Judge System Agent's id, coreagent.IDJudge) — NOT the agent under
// review. The returned session's Type is stamped SessionTypeVerifier so
// it round-trips through the wire as "verifier" and is excluded by
// listSessions' default filtering (pkg/gateway/rest.go) unless
// include_verifier=true is passed, while remaining fully visible to the
// usage/cost aggregation path (session.AggregateUsage applies no
// type-based filter at all — SC-014).
//
// This is the ONLY sanctioned way to create a verifier-typed session on
// disk; POST /api/v1/sessions (createSessionHTTP) intentionally does not
// accept type="verifier" in its request body (SessionCreateRequest.yaml's
// type enum is narrower by design), so a REST client can never spoof a
// hidden verifier session.
func (us *UnifiedStore) NewVerifierSession(ownerAgentID string) (*UnifiedMeta, error) {
	meta, err := us.NewSession(SessionTypeVerifier, "verifier", ownerAgentID)
	if err != nil {
		return nil, fmt.Errorf("session: new verifier session (owner=%s): %w", ownerAgentID, err)
	}
	return meta, nil
}

// GetOrCreateScheduledSession returns the scheduled session with the EXACT id,
// creating it if it does not exist (issue #264, W-2). It is the get-or-create
// primitive backing the `continue` (stable per-schedule id) and `main`
// (reserved id `sched-main-<owner>`) session modes.
//
// On create, the session is SessionTypeScheduled with
// ActiveAgentID == AgentID == ownerAgentID. The id must pass validateSessionID
// (no path-escape, non-empty) — the reserved `sched-main-<owner>` id is safe as
// long as owner ids are pre-normalized (slash-free); validateSessionID rejects
// any that are not, so safety is by-rejection, not intrinsic.
//
// If a session with id already exists, it is returned as-is regardless of its
// current owner/type (the caller's owner pinning happens in the agent loop, not
// here) so a human-touched continue session is not clobbered.
func (us *UnifiedStore) GetOrCreateScheduledSession(id, ownerAgentID string) (*UnifiedMeta, error) {
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	h := us.lockSession(id)
	defer h.Unlock()

	if meta, err := us.readMetaLocked(id); err == nil {
		// Cache-first existence check. readMetaLocked already returns an
		// independent clone (both cache-hit and cache-miss paths — see its
		// doc comment), so this second Clone() call is redundant but
		// harmless; kept as a defensive belt-and-braces guarantee that a
		// future readMetaLocked change can never leak a live cache pointer to
		// this external-facing existence check.
		return meta.Clone(), nil
	}
	return us.createSessionLocked(id, SessionTypeScheduled, "scheduled", ownerAgentID)
}

// GetMeta returns the metadata for a session.
func (us *UnifiedStore) GetMeta(sessionID string) (*UnifiedMeta, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	// Fast path: cacheMu.RLock-only cache hit, cloned before returning so the
	// caller never receives a pointer aliasing the cache's live entry. No
	// session shard needed — this touches only the cache, never disk.
	us.cacheMu.RLock()
	if meta, ok := us.metaCache[sessionID]; ok {
		clone := meta.Clone()
		us.cacheMu.RUnlock()
		return clone, nil
	}
	us.cacheMu.RUnlock()

	// Cache miss: acquire ONLY this session's shard and self-heal from disk
	// (readMetaLocked populates the cache on a successful read). Preserves
	// the existing not-found error contract, without contending with any
	// OTHER session's concurrent operation (was: store-global us.mu.Lock()).
	h := us.lockSession(sessionID)
	defer h.Unlock()
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return nil, err
	}
	return meta.Clone(), nil
}

// readMetaLocked returns an INDEPENDENT CLONE of sessionID's metadata,
// never the live cache entry pointer, on both the cache-hit and cache-miss
// paths. Caller must hold sessionID's shard (see lockSession) — was: caller
// must hold us.mu (the full write lock). FROZEN SIGNATURE (cross-unit
// contract, U2/U5): U2's AppendTranscriptStrict existence predicate is
// "readMetaLocked returned a non-nil error"; U5's W23 file split may change
// only this method's internals, never its signature.
//
// Internally this method touches metaCache ONLY under the narrower cacheMu
// (FR-048(b)), and the disk read (readUnifiedMeta) happens OUTSIDE any
// cacheMu critical section (FR-049 — cacheMu is never held across an os.*/
// fileutil.* call). Correctness against a concurrent cache-miss reader for
// the SAME sessionID still holds because the caller's session shard already
// serializes that — cacheMu only needs to protect the map itself.
//
// MB-1 fix (cache/disk divergence on write failure): an earlier version of
// this method returned the LIVE cache entry pointer on a hit, reasoning that
// every read-modify-write caller (SetMeta, SwitchAgent, AppendTranscript,
// GetOrCreateScheduledSession, GetMeta's cache-miss path) holds the
// session's shard across its entire mutate-then-write, so no reader of a
// DIFFERENT session could observe this entry mid-mutation. That reasoning
// covered the SUCCESS path but not FAILURE: those callers mutate the
// returned value in place and then call writeMetaLocked; if the disk write
// failed, writeMetaLocked returned early WITHOUT re-storing a clone — but
// the in-place mutation had already corrupted the shared cached object, so
// GetMeta/ListSessions would go on reporting the unpersisted, attempted
// value while meta.json on disk still held the old one. Cloning on every
// read (not just every write) closes this: every RMW caller now mutates its
// own private clone, so a failed writeMetaLocked leaves the cache entry
// completely untouched — it still reflects whatever was last durably
// persisted (the prior cache value on a hit, or the freshly disk-read value
// on a miss).
//
// Callers that hand this value to code outside the session's shard (GetMeta's
// cache-miss path, GetOrCreateScheduledSession) may return it directly — it
// is already an independent clone — though both currently call .Clone() on
// it again before returning; that second clone is redundant but harmless
// (kept as defensive belt-and-braces so a future readMetaLocked change can
// never leak a live cache pointer to those external-facing call sites).
//
// On a cache miss, composes the session's meta from disk via readUnifiedMeta
// (ADR-057 W23: meta.json plus stats.json/loop.json, each
// optional except meta.json — see readUnifiedMeta's doc comment), populates
// the cache with the freshly-composed (necessarily unaliased) object, and
// returns a clone of it.
func (us *UnifiedStore) readMetaLocked(sessionID string) (*UnifiedMeta, error) {
	us.cacheMu.RLock()
	if meta, ok := us.metaCache[sessionID]; ok {
		us.cacheMu.RUnlock()
		return meta.Clone(), nil
	}
	us.cacheMu.RUnlock()

	meta, err := readUnifiedMeta(filepath.Join(us.baseDir, sessionID))
	if err != nil {
		return nil, err
	}
	us.cacheMu.Lock()
	us.metaCache[sessionID] = meta
	us.cacheMu.Unlock()

	// FR-097 (Defect-1 fix): wire the parent index on this cache-miss
	// self-heal too — OUTSIDE the cacheMu critical section above (it takes
	// cacheMu itself; sync.Mutex is not reentrant), exactly mirroring
	// u5WriteIdentityLocked's own ADD-side call in unified_meta_files.go.
	// Before this fix, this was the second of two metaCache-population
	// paths (alongside loadMetaCacheLocked) that populated the cache
	// without ever touching parentIndex/childToParent — every caller that
	// reaches this branch (GetMeta, SetMeta, SwitchAgent, AppendTranscript,
	// GetOrCreateScheduledSession, ListSessions' out-of-band reconcile) was
	// silently leaving ChildCount under-reporting for the session it just
	// composed from disk. A no-op when meta.ParentSessionID is empty;
	// idempotent otherwise.
	us.u4IndexAddChild(meta.ParentSessionID, sessionID)
	return meta.Clone(), nil
}

// Close implements SessionStore.
//
// ADR-057 U6 W24 (FR-064): UnifiedStore.Close had no flush hook at all
// before this throttle existed — moot while every stats write was
// synchronous, but a real gap now that AppendTranscript defers its counter
// bump. Close is the third of FR-064's four forced-flush points: it stops
// the periodic flusher goroutine FIRST (so no tick can race the final flush
// below or fire after Close returns) and then flushes every still-dirty
// session's stats.json synchronously, before closing the context backend.
func (us *UnifiedStore) Close() error {
	us.stopStatsFlusher()
	us.flushAllDirtyStats()
	return us.backend.Close()
}

// readUnifiedMeta reads sessionDir's meta and composes the ADR-057 W23
// file split (FR-053), plus ADR-086 GOAL-FR-005's pending-ask file (wave
// S2), back into one *UnifiedMeta: meta.json (identity), stats.json,
// loop.json, pending_ask.json. There is no goal.json in this composition —
// wave S6 retired the whole group; a goal.json left on disk by a
// pre-deletion session is never opened by this function (D-F: no
// migration, no legacy read).
//
// FR-055: meta.json is REQUIRED — its absence or parse failure is always an
// error (unchanged from the pre-split contract: this is the "does this
// session exist at all" predicate every strict caller, including
// AppendTranscript/AppendTranscriptStrict, keys on). stats.json/loop.json/
// pending_ask.json are each OPTIONAL: absent composes as that group's ZERO
// value (a session that never started a loop, never had a stats-touching
// write, or never parked a question yet is not an error — dataset rows
// 2/3/8). FR-056: a file that IS present but fails to parse surfaces an
// error for THAT group specifically, never silently substituted with a
// zero value — "corrupt" and "absent" are deliberately different outcomes
// (BDD-62).
//
// FR-060: this is the ONLY reader for the file split. It does NOT (and
// must not) also accept a pre-split fused meta.json carrying embedded
// Stats/Goal*/Loop* fields — greenfield permits this (ADR-057 v4 operator
// decision 1: no migration, no back-compat; ADR-086 D-F restates it for
// this addition) and migrateLegacy's own freshly migrated sessions carry
// zero-valued Stats/Loop/PendingAsk anyway, so composing them from the
// files that exist yields the identical zero result a fused reader would
// have.
func readUnifiedMeta(sessionDir string) (*UnifiedMeta, error) {
	identity, err := u5ReadIdentityFile(sessionDir)
	if err != nil {
		return nil, err
	}
	stats, err := u5ReadStatsFile(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: read stats.json in %q: %w", sessionDir, err)
	}
	loop, err := u5ReadLoopFile(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: read loop.json in %q: %w", sessionDir, err)
	}
	pendingAsk, err := u5ReadPendingAskFile(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: read pending_ask.json in %q: %w", sessionDir, err)
	}

	meta := u5ComposeUnifiedMeta(identity, stats, loop, pendingAsk)
	// If Type is not set (legacy PartitionStore session), default to chat.
	if meta.Type == "" {
		meta.Type = SessionTypeChat
	}
	meta.PostLoad()
	return meta, nil
}

// writeUnifiedMetaDirect atomically writes meta.json to sessionDir with an OS
// flock for cross-process defense-in-depth. This is a package-level helper used
// during migration (called before the store is fully constructed). Normal writes
// go through UnifiedStore.writeMetaLocked which also holds the in-process mutex.
func writeUnifiedMetaDirect(sessionDir string, meta *UnifiedMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal meta: %w", err)
	}
	metaPath := filepath.Join(sessionDir, "meta.json")
	return fileutil.WithFlock(sessionFileLockPath(metaPath), func() error {
		return fileutil.WriteFileAtomic(metaPath, data, 0o600)
	})
}
