package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/elicify-ai/omnipus/pkg/providers"
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

	// Goal loop fields (ADR-049 D6/D7, /goal). Only non-nil fields are
	// written; callers that want to CLEAR a goal must pass an empty-string
	// GoalCondition explicitly (matching the TaskID clear convention).
	// GoalID is the stable per-generation goal identifier (see SessionMeta.GoalID's
	// doc comment); pass an empty string to CLEAR it on /goal clear.
	GoalID             *string
	GoalCondition      *string
	GoalRoundsUsed     *int
	GoalMaxRounds      *int
	GoalLatestReason   *string
	GoalStartedAt      *string
	GoalLastActivityAt *string
	// GoalCriteriaJSON is the ADR-053 Phase-2 compiled criteria ladder
	// (FR-110/FR-113). Pass an empty string to CLEAR it (paired with clearing
	// GoalCondition on /goal clear, FR-114).
	GoalCriteriaJSON *string
	// GoalPendingJSON is the proposed CompiledGoal during a re-statement
	// amendment (N-6/D11). Pass an empty string to CLEAR it (on confirm/clear).
	GoalPendingJSON *string

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

// ErrAlreadyActive is returned by SwitchAgent when the session's ActiveAgentID
// already matches the requested newAgentID. Callers should treat this as success
// (idempotent operation).
var ErrAlreadyActive = errors.New("agent already active on this session")

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
	// construction (loadMetaCacheLocked); on every successful mutation via one
	// of the four ADR-057 W23 targeted field-group writers
	// (u5WriteIdentityLocked/u5WriteStatsLocked/u5WriteGoalLocked/
	// u5WriteLoopLocked, unified_meta_files.go) — NOT a single funnel anymore
	// (FR-059/FR-084): each writer updates ONLY its own field group on the
	// cached entry and never replaces it wholesale, so a loop tick no longer
	// touches this entry's Stats/Goal fields and a transcript append no
	// longer touches its Goal/Loop fields; via readMetaLocked self-healing
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

// removeAllFn is a package-level test seam for ClearAll's per-session-directory
// removal call. It defaults to os.RemoveAll; tests override it to force a
// deterministic removal failure without depending on OS permission enforcement
// (which root bypasses via CAP_DAC_OVERRIDE, making a chmod-based
// failure-injection test a no-op in CI, which runs as root). Scoped narrowly
// to ClearAll's one call site — not a general refactor of the package's other
// os.RemoveAll/os.ReadDir calls.
var removeAllFn = os.RemoveAll

// writeFileAtomicFn is a package-level test seam for the four ADR-057 W23
// targeted meta-group writers' (u5WriteIdentityLocked/u5WriteStatsLocked/
// u5WriteGoalLocked/u5WriteLoopLocked, unified_meta_files.go) disk write
// step. It defaults to fileutil.WriteFileAtomic; tests override it to force
// a deterministic write failure (the MB-1 cache/disk-divergence regression
// guard) without depending on OS permission enforcement — same rationale as
// removeAllFn above (root bypasses chmod-based failure injection via
// CAP_DAC_OVERRIDE in CI).
var writeFileAtomicFn = fileutil.WriteFileAtomic

// readFileFn is the ADR-057 FR-103 read-side mirror of writeFileAtomicFn: a
// package-level seam every meta-group file read (u5ReadIdentityFile/
// u5ReadStatsFile/u5ReadGoalFile/u5ReadLoopFile, unified_meta_files.go) goes
// through, defaulting to os.ReadFile. `[grill2 M2-6]` Before this file split,
// the store had an injectable WRITE seam but no injectable READ seam, so
// neither test #103's "a cache hit performs zero disk reads" assertion nor
// FR-092's bounded-cost clause (c) — which reuses the same counter — was
// constructible: there was nothing to instrument. A test installs a counting
// wrapper here to prove GetMeta/ListSessions never touch this function on a
// warm cacheMu hit.
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
	// ADR-057 W23 (FR-053/FR-054): a brand-new session has no goal/loop/stats
	// activity yet, so only the identity group (meta.json) is written here —
	// stats.json/goal.json/loop.json are created lazily by their own
	// targeted writers the first time something actually touches that group
	// (dataset row 2, "a session that never ran a goal": meta.json only).
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

// SetMeta applies a partial update to a session's meta.json.
//
// ADR-057 W23/FR-084: unlike the pre-split version, this no longer funnels
// every patch through one whole-document rewrite. Each non-nil patch field
// is routed to its OWN field group (identity/stats/goal/loop, FR-053), and
// ONLY the group(s) actually touched by this call are written — a `/goal`
// or `/loop` patch no longer rewrites meta.json's UpdatedAt or touches
// stats.json at all (BDD-59: a goal round leaves loop.json AND meta.json
// byte-identical). meta.json's own UpdatedAt is bumped only when an
// identity-group field (including the new ParentSessionID) is touched;
// Goal*/Loop* groups carry no UpdatedAt of their own (only stats.json does,
// per FR-053/FR-066) so a goal/loop-only patch does not change the
// session's composed recency.
func (us *UnifiedStore) SetMeta(sessionID string, patch MetaPatch) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()

	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return err
	}

	var identityTouched, goalTouched, loopTouched bool

	if patch.Title != nil {
		meta.Title = *patch.Title
		identityTouched = true
	}
	if patch.Status != nil {
		meta.Status = *patch.Status
		identityTouched = true
	}
	if patch.TaskID != nil {
		meta.TaskID = *patch.TaskID
		identityTouched = true
	}
	if patch.InstanceID != nil {
		meta.InstanceID = *patch.InstanceID
	}
	if patch.Owner != nil {
		meta.Owner = *patch.Owner
		identityTouched = true
	}
	if patch.WorkspaceID != nil {
		meta.WorkspaceID = *patch.WorkspaceID
		identityTouched = true
	}
	if patch.ParentSessionID != nil {
		meta.ParentSessionID = *patch.ParentSessionID
		identityTouched = true
	}
	if patch.GoalID != nil {
		meta.GoalID = *patch.GoalID
		goalTouched = true
	}
	if patch.GoalCondition != nil {
		meta.GoalCondition = *patch.GoalCondition
		goalTouched = true
	}
	if patch.GoalRoundsUsed != nil {
		meta.GoalRoundsUsed = *patch.GoalRoundsUsed
		goalTouched = true
	}
	if patch.GoalMaxRounds != nil {
		meta.GoalMaxRounds = *patch.GoalMaxRounds
		goalTouched = true
	}
	if patch.GoalLatestReason != nil {
		meta.GoalLatestReason = *patch.GoalLatestReason
		goalTouched = true
	}
	if patch.GoalStartedAt != nil {
		meta.GoalStartedAt = *patch.GoalStartedAt
		goalTouched = true
	}
	if patch.GoalLastActivityAt != nil {
		meta.GoalLastActivityAt = *patch.GoalLastActivityAt
		goalTouched = true
	}
	if patch.GoalCriteriaJSON != nil {
		meta.GoalCriteriaJSON = *patch.GoalCriteriaJSON
		goalTouched = true
	}
	if patch.GoalPendingJSON != nil {
		meta.GoalPendingJSON = *patch.GoalPendingJSON
		goalTouched = true
	}
	if patch.LoopMode != nil {
		meta.LoopMode = *patch.LoopMode
		loopTouched = true
	}
	if patch.LoopPrompt != nil {
		meta.LoopPrompt = *patch.LoopPrompt
		loopTouched = true
	}
	if patch.LoopRunCount != nil {
		meta.LoopRunCount = *patch.LoopRunCount
		loopTouched = true
	}
	if patch.LoopMaxRuns != nil {
		meta.LoopMaxRuns = *patch.LoopMaxRuns
		loopTouched = true
	}
	if patch.LoopIntervalMS != nil {
		meta.LoopIntervalMS = *patch.LoopIntervalMS
		loopTouched = true
	}
	if patch.LoopNextDelayMS != nil {
		meta.LoopNextDelayMS = *patch.LoopNextDelayMS
		loopTouched = true
	}
	if patch.LoopJobID != nil {
		meta.LoopJobID = *patch.LoopJobID
		loopTouched = true
	}
	if patch.LoopStartedAt != nil {
		meta.LoopStartedAt = *patch.LoopStartedAt
		loopTouched = true
	}
	if patch.LoopLastActivityAt != nil {
		meta.LoopLastActivityAt = *patch.LoopLastActivityAt
		loopTouched = true
	}

	if identityTouched {
		meta.UpdatedAt = time.Now().UTC()
		if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if patch.Status != nil {
		// ADR-057 U6 W24 (FR-064): a Status transition is one of the four
		// forced-flush points — a session about to pause/complete/error
		// must not leave pending counter deltas stranded in the cache until
		// the next periodic tick. This call already holds sessionID's shard
		// (h, acquired above), so u6FlushDirtySessionLocked is safe to call
		// directly. A flush failure here is logged, not returned: the
		// Status write itself already succeeded durably above, and failing
		// the whole SetMeta call would make a successfully-persisted status
		// change look like a lost one (same rationale AppendTranscript uses
		// for its own post-write stats failure).
		if err := us.u6FlushDirtySessionLocked(sessionID); err != nil {
			slog.Warn("unified_store: forced stats flush on status change failed",
				"session_id", sessionID, "error", err)
		}
	}
	if goalTouched {
		if err := us.u5WriteGoalLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if loopTouched {
		if err := us.u5WriteLoopLocked(sessionID, meta); err != nil {
			return err
		}
	}
	return nil
}

// SwitchAgent atomically updates the ActiveAgentID on a session.
// The caller must NOT already hold sessionID's shard (see lockSession) — was:
// caller must NOT hold us.mu. Returns ErrAlreadyActive if the session
// is already on newAgentID (idempotent — callers should treat this as success).
// newAgentID is appended to AgentIDs if not already present.
func (us *UnifiedStore) SwitchAgent(sessionID, newAgentID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()

	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return err
	}
	if meta.ActiveAgentID == newAgentID {
		return ErrAlreadyActive
	}
	meta.ActiveAgentID = newAgentID

	found := false
	for _, id := range meta.AgentIDs {
		if id == newAgentID {
			found = true
			break
		}
	}
	if !found {
		meta.AgentIDs = append(meta.AgentIDs, newAgentID)
	}
	meta.UpdatedAt = time.Now().UTC()
	// ActiveAgentID/AgentIDs are identity-group fields (FR-053).
	return us.u5WriteIdentityLocked(sessionID, meta)
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
// (ADR-057 W23: meta.json plus stats.json/goal.json/loop.json, each
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

// writeMetaLocked is RETAINED, post-W23, as a backward-compatible DISPATCHER
// over the four FR-054 targeted field-group writers
// (u5WriteIdentityLocked/u5WriteStatsLocked/u5WriteGoalLocked/
// u5WriteLoopLocked, unified_meta_files.go) — it is no longer "the single
// invalidation/update point for every mutation path" (that whole-document
// funnel is exactly what FR-084/Alternative-F forbids; see the doc comments
// above metaCache and readMetaLocked). This file's OWN five mutation paths
// (createSessionLocked, SetMeta, SwitchAgent, AppendTranscript,
// NewChannelSession) call the four targeted writers DIRECTLY and never reach
// this function.
//
// It survives only for pkg/session/unified_api.go's two call sites
// (AppendTranscriptStrict's stats-only mutation, CreateSessionWithID's
// identity-only Owner stamp) — U2's file, landed commit acfd0e5a in this
// same wave BEFORE this rewrite reached unified.go, both passing one fully-
// mutated *UnifiedMeta rather than a field-group-scoped patch. Ownership
// Rule 1/2 forbids this unit from editing unified_api.go to convert those
// two call sites itself, so this function closes the gap from this side of
// the boundary: it diffs the supplied meta against the meta CURRENTLY
// cached/persisted for sessionID to determine exactly which of the four
// field groups actually changed (identity's own UpdatedAt is excluded from
// that comparison — a Stats-only touch, e.g. AppendTranscriptStrict, always
// changes the single in-memory UpdatedAt field, and that must not be
// misread as an identity change), and calls ONLY the targeted writer(s)
// for the group(s) that differ. This keeps FR-084's "update only its own
// field group, never a wholesale cache replace" true for these two call
// sites as well, and preserves W23's lazy-file-creation property (a
// delegated child that only ever calls AppendTranscriptStrict never gains
// an empty goal.json/loop.json it never touched).
//
// Caller must hold sessionID's shard (see lockSession) — was: caller must
// hold us.mu. (writeUnifiedMetaDirect, used only by migrateLegacy before the
// cache exists, is a SEPARATE, unmodified function — FR-060 forbids
// changing it or providing a reader for its pre-split fused output; nothing
// in this dispatcher touches it.)
func (us *UnifiedStore) writeMetaLocked(sessionID string, meta *UnifiedMeta) error {
	prev, prevErr := us.readMetaLocked(sessionID)

	writeIdentity, writeStats, writeGoal, writeLoop := true, true, true, true
	if prevErr == nil {
		prevIdentity, curIdentity := u5IdentityFromMeta(prev), u5IdentityFromMeta(meta)
		// UpdatedAt is the single in-memory field shared by BOTH the identity
		// and stats groups (FR-066 composes them on read) — excluded here so
		// a Stats-only caller (AppendTranscriptStrict) that bumps it does not
		// register as an identity change too.
		prevIdentity.UpdatedAt, curIdentity.UpdatedAt = time.Time{}, time.Time{}
		writeIdentity = !u5SameJSON(prevIdentity, curIdentity)
		writeStats = !u5SameJSON(u5StatsFromMeta(prev), u5StatsFromMeta(meta))
		writeGoal = !u5SameJSON(u5GoalFromMeta(prev), u5GoalFromMeta(meta))
		writeLoop = !u5SameJSON(u5LoopFromMeta(prev), u5LoopFromMeta(meta))
	}

	if writeIdentity {
		if err := us.u5WriteIdentityLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if writeStats {
		if err := us.u5WriteStatsLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if writeGoal {
		if err := us.u5WriteGoalLocked(sessionID, meta); err != nil {
			return err
		}
	}
	if writeLoop {
		if err := us.u5WriteLoopLocked(sessionID, meta); err != nil {
			return err
		}
	}
	return nil
}

// AppendTranscript appends an entry to {session-id}/transcript.jsonl.
//
// R-8 fix (ADR-057 FR-048): this used to take the store-global us.mu on
// EVERY streamed line, held across the append AND a full meta rewrite — a
// 24-way delegation fan-out serialised 24 fsync-bound creates/appends and
// stalled token streaming in every OTHER session in the store. Now it takes
// only sessionID's own shard, so a concurrent append/create against a
// DIFFERENT session never waits on this one.
//
// STRICT (ADR-057 FR-002/W3a, AC-1). Before this change, the existence
// check ran AFTER the transcript write: fileutil.AppendJSONL begins with
// os.MkdirAll, so an append against a session id with no meta.json silently
// minted an orphan directory, wrote the line into it, then failed the
// follow-up meta-stats read, logged a WARN, and returned nil — "success"
// for a write that both landed somewhere nobody asked for AND told its
// caller nothing went wrong. That lenient branch is DELETED, not converted
// into a strict SIBLING: AC-1's frozen text is a property of
// AppendTranscript itself, so a sibling (AppendTranscriptStrict, U2's
// unified_api.go) would only satisfy it for callers that switched to the
// sibling — every one of the 22 tree-wide AppendTranscript( matches would
// still be able to silently create (`[grill2 C2-3]`). The existence check
// now runs FIRST, under sessionID's own shard, before ANY filesystem write:
// an unknown session id returns a non-nil error and creates ZERO
// directories (SC-001), matching AppendTranscriptStrict's contract exactly
// — "a name, not a second behavior" per FR-002's own text.
func (us *UnifiedStore) AppendTranscript(sessionID string, entry TranscriptEntry) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	h := us.lockSession(sessionID)
	defer h.Unlock()

	// Existence check FIRST — see this method's doc comment above. No
	// filesystem write of any kind happens before this succeeds.
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return fmt.Errorf("unified_store: append transcript: session %q does not exist: %w", sessionID, err)
	}

	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	if err := fileutil.AppendJSONL(transcriptPath, entry); err != nil {
		return fmt.Errorf("unified_store: append transcript: %w", err)
	}

	// Update stats and UpdatedAt — targeted stats-group write only (FR-084);
	// a transcript append never touches meta.json/goal.json/loop.json. See
	// accumulateEntryStats (entry_stats.go) for the full token-accounting
	// convention shared with PartitionStore.AppendMessage and
	// UnifiedStore.AppendTranscriptStrict.
	accumulateEntryStats(&meta.Stats, entry)
	meta.UpdatedAt = entry.Timestamp
	// ADR-057 U6 W24 (FR-061/FR-062): the per-token counter write is
	// throttled — this mutates ONLY the cached entry (never touching
	// stats.json on disk) and marks the session dirty for the periodic
	// flusher (or the next forced-flush point) to persist. The transcript
	// line itself already landed durably above via fileutil.AppendJSONL
	// (FR-062: the append stays immediate and unthrottled); only the
	// counters are deferred. Before this throttle, this call site invoked
	// u5WriteStatsLocked directly — a full marshal + WithFlock + fsync +
	// rename + directory fsync on EVERY streamed line (US-13's governing
	// complaint). u6MarkStatsDirtyLocked can never fail (it is pure
	// in-memory bookkeeping), so there is no error to log here.
	us.u6MarkStatsDirtyLocked(sessionID, meta)
	return nil
}

// MarkLastEntryTruncated finds the last assistant transcript entry for the
// given session in transcript.jsonl that belongs to turnID and rewrites it
// with truncated=true.
//
// H2: The turnID parameter scopes the backward-walk to entries whose
// turn_id matches. This prevents a cancel on turn T2 from mutating the
// clean final assistant entry of a previously-completed turn T1 when both
// share the same sessionID.
//
// If turnID is empty, the function falls back to the pre-H2 behavior (match
// the last assistant entry regardless of turn_id) and logs a warning. This
// preserves backward compatibility with any call sites that cannot supply
// a turn ID.
//
// Acquires the same per-session shard as AppendTranscript (see lockSession),
// so it serializes with any concurrent operation against THIS session
// without contending with any other session's shard. Does NOT touch
// context.jsonl (LLM history) — per FR-14a, the partial content there remains
// untouched so the next turn's LLM context sees natural truncation.
//
// Returns nil if no matching assistant entry is found (e.g., cancel arrived
// before any assistant content was written). Returns an error only on I/O
// failure.
func (us *UnifiedStore) MarkLastEntryTruncated(sessionID, turnID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if turnID == "" {
		slog.Warn(
			"unified_store: MarkLastEntryTruncated called with empty turnID — falling back to last-assistant-entry behavior",
			"session_id",
			sessionID,
		)
	}

	h := us.lockSession(sessionID)
	defer h.Unlock()

	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No transcript at all — nothing to mark; treat as no-op.
			return nil
		}
		return fmt.Errorf("unified_store: mark truncated: read transcript: %w", err)
	}

	// Split into non-empty lines and parse.
	rawLines := bytes.Split(data, []byte{'\n'})
	entries := make([]json.RawMessage, 0, len(rawLines))
	for _, line := range rawLines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		entries = append(entries, json.RawMessage(line))
	}

	if len(entries) == 0 {
		return nil
	}

	// Walk backward to find the last assistant entry matching turnID.
	// When turnID is empty (backward-compat path) any assistant entry matches.
	targetIdx := -1
	for i := len(entries) - 1; i >= 0; i-- {
		var e TranscriptEntry
		if jsonErr := json.Unmarshal(entries[i], &e); jsonErr != nil {
			// Skip malformed lines.
			slog.Warn(
				"unified_store: mark truncated: skipping malformed line",
				"session_id",
				sessionID,
				"index",
				i,
				"error",
				jsonErr,
			)
			continue
		}
		if e.Role != "assistant" {
			continue
		}
		if turnID != "" && e.TurnID != turnID {
			continue
		}
		targetIdx = i
		break
	}

	if targetIdx == -1 {
		// No matching assistant entry found — no-op, not an error.
		return nil
	}

	// Unmarshal the target entry, set Truncated, re-marshal into the slot.
	var target TranscriptEntry
	if jsonErr := json.Unmarshal(entries[targetIdx], &target); jsonErr != nil {
		return fmt.Errorf("unified_store: mark truncated: unmarshal target entry: %w", jsonErr)
	}
	target.Truncated = true
	rewritten, jsonErr := json.Marshal(target)
	if jsonErr != nil {
		return fmt.Errorf("unified_store: mark truncated: marshal updated entry: %w", jsonErr)
	}
	entries[targetIdx] = json.RawMessage(rewritten)

	// Rebuild the file contents: one JSON object per line, WITH a trailing
	// newline after the LAST line too. This is load-bearing, not cosmetic:
	// a rewrite that omits the final newline would leave the next
	// AppendTranscript call's record concatenated directly onto this
	// rewrite's last line — e.g. "{lastEntry}{newRecord}\n" — which
	// ReadTranscript cannot parse as JSON and silently drops via its
	// "skipping malformed transcript line" continue, losing BOTH entries.
	// Confirmed via a byte-level repro (rewrite → append → inspect raw
	// bytes → ReadTranscript entry count) before this fix; see
	// TestMarkLastEntryTruncated_TrailingNewlineSurvivesSubsequentAppend
	// and TestUpdateToolCallStatus_TrailingNewlineSurvivesSubsequentAppend.
	// This is the PRIMARY fix; AppendJSONL (pkg/fileutil/file.go) now also
	// carries a SECOND, independent defensive layer — it detects a missing
	// trailing newline on the existing file and prepends one before its own
	// record — so even a future rewrite site that forgets this discipline
	// degrades to a defensively-recovered file, not silent data loss.
	var buf bytes.Buffer
	for _, line := range entries {
		buf.Write(line)
		buf.WriteByte('\n')
	}

	if writeErr := fileutil.WriteFileAtomic(transcriptPath, buf.Bytes(), 0o600); writeErr != nil {
		return fmt.Errorf("unified_store: mark truncated: write transcript: %w", writeErr)
	}
	return nil
}

// UpdateToolCallStatus finds the transcript entry carrying a ToolCall with the
// given ID and rewrites that ToolCall's Status and DurationMS fields in place.
//
// This exists for the ASYNC delegation path (DelegateTool.executeAsync,
// pkg/tools/delegate.go): the spawning "delegate" tool call itself completes
// — and its own ToolCall record is appended via appendToolCallTranscript —
// almost instantly with a placeholder ack (Status="success", DurationMS≈0,
// from tools.AsyncResult), well BEFORE the actual sub-turn goroutine finishes
// running. The sub-turn's real terminal status/wall-clock duration is only
// known later, at EventKindSubTurnEnd (spawnSubTurn's cleanup defer,
// pkg/agent/subturn.go) — this method lets that defer go back and correct the
// already-persisted placeholder record so a session reload replays the same
// status/duration the live WS stream showed (Wave 3 fix 5b).
//
// Mirrors MarkLastEntryTruncated's read-mutate-rewrite-one-line pattern and
// shares its mutex. Walks backward so a duplicate ID (should not normally
// occur — appendToolCallTranscript writes one entry per completed tool call)
// updates the LATEST occurrence, matching the "last occurrence wins" semantics
// replay.go already applies when reading (buildSpawnIDsWithChildren /
// latestByID in pkg/gateway/replay.go).
//
// Returns found=false (with a nil error) when no entry with a matching
// ToolCall.ID is found — NOT necessarily an error condition (see below), but
// the caller can now distinguish this from a real update. This is the
// expected outcome for SYNCHRONOUS delegation (DelegateTool.executeSync):
// spawnSubTurn blocks until the child turn finishes, so at the point
// EventKindSubTurnEnd fires the caller has not yet appended the spawning
// tool call's own record — it does so correctly itself moments later, once
// spawnSubTurn returns with the real result.
//
// found=false can ALSO legitimately occur for ASYNC delegation due to a real
// race: DelegateTool.executeAsync launches the child sub-turn in a goroutine
// and returns immediately, while the PARENT (the turn that called the
// "delegate" tool) writes this tool call's OWN placeholder ack record only
// after further processing (hooks, media, events) in its own call stack. If
// the child's spawnSubTurn dispatch fails fast (e.g. a depth-limit or
// target-resolution rejection), its cleanup defer can call
// UpdateToolCallStatus BEFORE the parent's placeholder record exists yet.
// Callers in that race window (currently only spawnSubTurn's cleanup defer,
// pkg/agent/subturn.go) MUST retry briefly rather than treat found=false as
// terminal — see updateToolCallStatusWithRetry.
//
// Returns a non-nil error only on I/O failure.
func (us *UnifiedStore) UpdateToolCallStatus(
	sessionID string,
	toolCallID ToolCallID,
	status string,
	durationMS int64,
) (found bool, err error) {
	return us.UpdateToolCallStatusAndResult(sessionID, toolCallID, status, durationMS, nil)
}

// UpdateToolCallStatusAndResult is UpdateToolCallStatus's result-bearing
// sibling (W4): it performs the exact same in-place Status/DurationMS
// correction, and additionally rewrites the matching ToolCall's Result field
// when result is non-nil. Passing a nil result leaves the ToolCall's existing
// Result untouched (UpdateToolCallStatus delegates here with result=nil,
// preserving its original status-only behavior exactly).
//
// This exists for spawnSubTurn's completion path (pkg/agent/subturn.go): the
// persisted "delegate" tool_call previously never received the sub-turn's own
// output — UpdateToolCallStatus corrected Status/DurationMS but had no way to
// carry the result text, so a session reload showed a delegate tool_call with
// a terminal status but an empty `result`, even though the live WS stream
// carried the sub-turn's actual text via SubTurnEndPayload. Mirrors
// recordExternalToolResultUpdateInPlace's (pkg/agent/external_dispatch.go)
// read-modify-rewrite-one-line approach for the equivalent external-cli tool
// call case.
//
// See UpdateToolCallStatus's doc comment above for the found=false semantics
// (same race window, same retry-via-updateToolCallStatusWithRetry contract).
// Returns a non-nil error only on I/O failure.
func (us *UnifiedStore) UpdateToolCallStatusAndResult(
	sessionID string,
	toolCallID ToolCallID,
	status string,
	durationMS int64,
	result map[string]any,
) (found bool, err error) {
	if validationErr := validateSessionID(sessionID); validationErr != nil {
		return false, validationErr
	}
	if toolCallID == "" {
		return false, nil
	}

	h := us.lockSession(sessionID)
	defer h.Unlock()

	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No transcript at all — nothing to update; treat as no-op.
			return false, nil
		}
		return false, fmt.Errorf("unified_store: update tool call status: read transcript: %w", err)
	}

	// Split into non-empty lines and parse.
	rawLines := bytes.Split(data, []byte{'\n'})
	entries := make([]json.RawMessage, 0, len(rawLines))
	for _, line := range rawLines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		entries = append(entries, json.RawMessage(line))
	}

	if len(entries) == 0 {
		return false, nil
	}

	// Walk backward to find the last entry carrying a ToolCall with this ID.
	targetIdx := -1
	for i := len(entries) - 1; i >= 0; i-- {
		var e TranscriptEntry
		if jsonErr := json.Unmarshal(entries[i], &e); jsonErr != nil {
			// Skip malformed lines.
			slog.Warn(
				"unified_store: update tool call status: skipping malformed line",
				"session_id",
				sessionID,
				"index",
				i,
				"error",
				jsonErr,
			)
			continue
		}
		matched := false
		for _, tc := range e.ToolCalls {
			if tc.ID == toolCallID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		targetIdx = i
		break
	}

	if targetIdx == -1 {
		// No matching tool-call entry found — no-op, not an error (see doc comment).
		return false, nil
	}

	// Unmarshal the target entry, update the matching ToolCall in place, re-marshal.
	var target TranscriptEntry
	if jsonErr := json.Unmarshal(entries[targetIdx], &target); jsonErr != nil {
		return false, fmt.Errorf("unified_store: update tool call status: unmarshal target entry: %w", jsonErr)
	}
	for ti := range target.ToolCalls {
		if target.ToolCalls[ti].ID == toolCallID {
			target.ToolCalls[ti].Status = status
			target.ToolCalls[ti].DurationMS = durationMS
			if result != nil {
				target.ToolCalls[ti].Result = result
			}
		}
	}
	rewritten, jsonErr := json.Marshal(target)
	if jsonErr != nil {
		return false, fmt.Errorf("unified_store: update tool call status: marshal updated entry: %w", jsonErr)
	}
	entries[targetIdx] = json.RawMessage(rewritten)

	// Rebuild the file contents: one JSON object per line, WITH a trailing
	// newline after the LAST line too — see MarkLastEntryTruncated's doc
	// comment above for why omitting it silently corrupts and drops BOTH
	// this rewrite's last entry AND whatever AppendTranscript writes next
	// (the confirmed root cause of the Wave 3 fix-5b/5d data-loss bug: this
	// function's own rewrite, immediately followed by AsyncNotifier's
	// delivery of the delegate's result via AppendTranscript, corrupted and
	// dropped both).
	var buf bytes.Buffer
	for _, line := range entries {
		buf.Write(line)
		buf.WriteByte('\n')
	}

	if writeErr := fileutil.WriteFileAtomic(transcriptPath, buf.Bytes(), 0o600); writeErr != nil {
		return false, fmt.Errorf("unified_store: update tool call status: write transcript: %w", writeErr)
	}
	return true, nil
}

// ReadTranscript returns all entries from {session-id}/transcript.jsonl.
func (us *UnifiedStore) ReadTranscript(sessionID string) ([]TranscriptEntry, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []TranscriptEntry{}, nil
		}
		return nil, fmt.Errorf("unified_store: read transcript: %w", err)
	}
	var entries []TranscriptEntry
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry TranscriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			slog.Warn("unified_store: skipping malformed transcript line", "session_id", sessionID, "error", err)
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
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

// AddMessage implements SessionStore — appends a simple role/content message to context.jsonl.
func (us *UnifiedStore) AddMessage(sessionKey, role, content string) {
	if err := us.backend.AddMessage(context.Background(), sessionKey, role, content); err != nil {
		slog.Error("unified_store: add message", "key", sessionKey, "error", err)
	}
}

// AddFullMessage implements SessionStore — appends a complete message to context.jsonl.
func (us *UnifiedStore) AddFullMessage(sessionKey string, msg providers.Message) {
	if err := us.backend.AddFullMessage(context.Background(), sessionKey, msg); err != nil {
		slog.Error("unified_store: add full message", "key", sessionKey, "error", err)
	}
}

// GetHistory implements SessionStore — returns message history from context.jsonl.
func (us *UnifiedStore) GetHistory(sessionKey string) []providers.Message {
	msgs, err := us.backend.GetHistory(context.Background(), sessionKey)
	if err != nil {
		slog.Error("unified_store: get history", "key", sessionKey, "error", err)
		return []providers.Message{}
	}
	return msgs
}

// SetHistory implements SessionStore.
func (us *UnifiedStore) SetHistory(sessionKey string, history []providers.Message) {
	if err := us.backend.SetHistory(context.Background(), sessionKey, history); err != nil {
		slog.Error("unified_store: set history", "key", sessionKey, "error", err)
	}
}

// TruncateHistory implements SessionStore.
func (us *UnifiedStore) TruncateHistory(sessionKey string, keepLast int) {
	if err := us.backend.TruncateHistory(context.Background(), sessionKey, keepLast); err != nil {
		slog.Error("unified_store: truncate history", "key", sessionKey, "error", err)
	}
}

// RollbackAppended implements SessionStore — truncates the on-disk archive to
// targetArchiveLen physical lines, restores meta.Skip = min(targetSkip,
// targetArchiveLen) and restores the projection state to the turn-start
// emptiedSet in one meta write (ADR-066 FR-020). This is the fix for the
// mid-turn eviction bug: if windowTrim advanced Skip during a live turn and
// the turn then aborts, restoring Skip to its turn-start value ensures
// GetHistory returns exactly the pre-turn live window (SC-001, SC-010).
// Callers compute: targetSkip = initialArchiveLen - initialHistoryLength.
func (us *UnifiedStore) RollbackAppended(sessionKey string, targetArchiveLen, targetSkip int, emptiedSet memory.ProjectionSet) {
	if err := us.backend.RollbackAppended(context.Background(), sessionKey, targetArchiveLen, targetSkip, emptiedSet); err != nil {
		slog.Error("unified_store: rollback appended", "key", sessionKey, "error", err)
	}
}

// Projection implements SessionStore.
func (us *UnifiedStore) Projection(sessionKey string) memory.ProjectionMeta {
	pm, err := us.backend.GetProjection(context.Background(), sessionKey)
	if err != nil {
		slog.Error("unified_store: get projection", "key", sessionKey, "error", err)
		return memory.ProjectionMeta{Entries: memory.ProjectionSet{}}
	}
	return pm
}

// SetProjectionState implements SessionStore.
func (us *UnifiedStore) SetProjectionState(sessionKey string, pk memory.ProjectionKey, state memory.ProjectionState) {
	if err := us.backend.SetProjectionState(context.Background(), sessionKey, pk, state); err != nil {
		slog.Error("unified_store: set projection state", "key", sessionKey, "error", err)
	}
}

// MarkHydrated implements SessionStore.
func (us *UnifiedStore) MarkHydrated(sessionKey string) {
	if err := us.backend.MarkHydrated(context.Background(), sessionKey); err != nil {
		slog.Error("unified_store: mark hydrated", "key", sessionKey, "error", err)
	}
}

// ReadArchive implements SessionStore — returns the full archived log for
// sessionKey from line 0, ignoring meta.Skip. Evicted (skipped) turns are
// included. Each ArchivedMessage carries the per-line TS written by addMsg
// (FR-016/FR-017). Legacy lines pre-dating the TS stamp unmarshal with TS==0.
func (us *UnifiedStore) ReadArchive(ctx context.Context, sessionKey string) ([]memory.ArchivedMessage, error) {
	msgs, err := us.backend.ReadArchive(ctx, sessionKey)
	if err != nil {
		slog.Error("unified_store: read archive", "key", sessionKey, "error", err)
		return nil, err
	}
	return msgs, nil
}

// Save implements SessionStore — ensures all writes are durable.
// Since the JSONL backend fsyncs every write immediately, the data is
// already durable at this point.
//
// context-paging (FR-005): Save does NOT compact the JSONL file. Evicted
// (skipped) lines must remain on disk so recall_conversation can reach them.
// The retention sweep is the sole legitimate deleter of context.jsonl content.
func (us *UnifiedStore) Save(sessionKey string) error {
	return nil
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

// readUnifiedMeta reads sessionDir's meta and composes the ADR-057 W23
// four-file split (FR-053) back into one *UnifiedMeta: meta.json (identity),
// stats.json, goal.json, loop.json.
//
// FR-055: meta.json is REQUIRED — its absence or parse failure is always an
// error (unchanged from the pre-split contract: this is the "does this
// session exist at all" predicate every strict caller, including
// AppendTranscript/AppendTranscriptStrict, keys on). stats.json/goal.json/
// loop.json are each OPTIONAL: absent composes as that group's ZERO value
// (a session that never ran a goal, never started a loop, or never had a
// stats-touching write yet is not an error — dataset rows 2/3/8). FR-056: a
// file that IS present but fails to parse surfaces an error for THAT group
// specifically, never silently substituted with a zero value — "corrupt"
// and "absent" are deliberately different outcomes (BDD-62).
//
// FR-060: this is the ONLY reader for the four-file split. It does NOT (and
// must not) also accept a pre-split fused meta.json carrying embedded
// Stats/Goal*/Loop* fields — greenfield permits this (ADR-057 v4 operator
// decision 1: no migration, no back-compat) and migrateLegacy's own freshly
// migrated sessions carry zero-valued Stats/Goal/Loop anyway, so composing
// them from four files (three of which don't exist yet) yields the
// identical zero result a fused reader would have.
func readUnifiedMeta(sessionDir string) (*UnifiedMeta, error) {
	identity, err := u5ReadIdentityFile(sessionDir)
	if err != nil {
		return nil, err
	}
	stats, err := u5ReadStatsFile(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: read stats.json in %q: %w", sessionDir, err)
	}
	goal, err := u5ReadGoalFile(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: read goal.json in %q: %w", sessionDir, err)
	}
	loop, err := u5ReadLoopFile(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("unified_store: read loop.json in %q: %w", sessionDir, err)
	}

	meta := u5ComposeUnifiedMeta(identity, stats, goal, loop)
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
	return fileutil.WithFlock(metaPath, func() error {
		return fileutil.WriteFileAtomic(metaPath, data, 0o600)
	})
}
