// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package agent provides the agent memory store backed by the two-room topology.
//
// FR-7.1: Two rooms —
//   - Private per-agent room:   agents/<id>/.omnipus/
//   - Shared workspace room:    workspaces/<id>/.omnipus/
//
// Per-memory files follow FR-7.2 (full frontmatter, every field present).
// The 4 tools (remember/recall_memory/recall_conversation/run_retrospective) use this store.
//
// GREENFIELD: old MEMORY.md data is not migrated (FR-7.6 / operator D2 decision).

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memrooms"
	memindex "github.com/elicify-ai/omnipus/pkg/memrooms/index"
	"github.com/elicify-ai/omnipus/pkg/memrooms/minhash"
	"github.com/elicify-ai/omnipus/pkg/validation"
)

// MemoryCategory is the closed set of categories an agent may tag a long-term
// memory entry with. Retained for backward compat with the MemoryStoreAdapter
// interface surface and existing tests.
type MemoryCategory string

const (
	CategoryKeyDecision   MemoryCategory = "key_decision"
	CategoryReference     MemoryCategory = "reference"
	CategoryLessonLearned MemoryCategory = "lesson_learned"
	CategoryLegacy        MemoryCategory = "legacy"
	CategoryLastSession   MemoryCategory = "last_session"
	CategoryRetro         MemoryCategory = "retro"
)

// ParseMemoryCategory validates and returns a typed category from a string.
// Accepts the three AppendLongTerm-legal values + maps them to the Spec-5
// MemoryType enum for storage.
func ParseMemoryCategory(s string) (MemoryCategory, error) {
	switch MemoryCategory(s) {
	case CategoryKeyDecision, CategoryReference, CategoryLessonLearned:
		return MemoryCategory(s), nil
	}
	return "", fmt.Errorf("invalid category %q (expected one of: key_decision, reference, lesson_learned)", s)
}

// categoryToMemoryType maps the legacy 3-category input to the Spec-5 8-type enum.
func categoryToMemoryType(cat MemoryCategory) memrooms.MemoryType {
	switch cat {
	case CategoryKeyDecision:
		return memrooms.MemoryTypeDecision
	case CategoryReference:
		return memrooms.MemoryTypeReference
	case CategoryLessonLearned:
		return memrooms.MemoryTypeLesson
	default:
		return memrooms.MemoryTypeNote
	}
}

// RecapTrigger is the closed set of triggers recorded on a Retro.
type RecapTrigger string

const (
	TriggerExplicit  RecapTrigger = "explicit"
	TriggerLazy      RecapTrigger = "lazy"
	TriggerIdle      RecapTrigger = "idle"
	TriggerBootstrap RecapTrigger = "bootstrap"
	TriggerJoined    RecapTrigger = "joined"
)

// LongTermEntry is the common result type for memory reads (tools interface).
type LongTermEntry struct {
	Timestamp time.Time
	Category  MemoryCategory
	Content   string
	// ID is the recalled memory's stable identifier (ULID). Surfaced to the
	// tools layer so the recall result can include it (FR-7.5 cited_in).
	ID string
	// Title is the recalled memory's human-readable title (FR-7.5 cited_in).
	Title string
}

// Retro is a structured retrospective record.
type Retro struct {
	Timestamp        time.Time
	Trigger          RecapTrigger
	Fallback         bool
	FallbackReason   string
	Recap            string
	WentWell         []string
	NeedsImprovement []string
}

// MemoryStore manages persistent memory for the agent using the two-room topology.
//
// The private room is always available (agents/<id>/.omnipus/).
// The shared room is set at run-time per turn (workspaces/<id>/.omnipus/).
//
// Thread safety: AppendLongTerm / AppendRetro use per-file flocks via
// fileutil.WriteFileAtomic + pkg/fileutil.AppendJSONL (which is POSIX-safe).
// SearchEntries reads are safe to call concurrently.
//
// Bleve integration (FR-7.4): a per-room scorch index is lazily opened on first
// use and cached by room-root path. The index cache is protected by indexMu.
// MinHash dedup (FR-7.5 / M-5): signatures of known memories are kept in sigCache;
// a near-dup write appends a NearDupRecord to minhash.jsonl (non-destructive).
type MemoryStore struct {
	// privateRoom is the per-agent private room. Never nil after NewMemoryStore.
	privateRoom memrooms.Room

	// agentID is the wired agent ID used as the memory author. Set via
	// SetAgentID (called from ContextBuilder.WithAgentInfo). When empty,
	// resolveAuthor falls back to deriving it from the private room's path —
	// kept for tests that construct a MemoryStore directly without agent info.
	agentID string

	// omnipusHome is the resolved $OMNIPUS_HOME used to build workspace room paths.
	omnipusHome string

	// sharedMu guards sharedRoom. SetWorkspaceID (writer, called per turn) races
	// concurrent readers (rooms / resolveWriteRoom / SearchEntriesInScope /
	// SharedRoom); an unsynchronised write here could leak one workspace's shared
	// room into another's reads. All access to sharedRoom MUST hold sharedMu.
	sharedMu   sync.RWMutex
	sharedRoom *memrooms.Room

	// indexMu protects indexCache and sigCache maps.
	indexMu sync.Mutex
	// indexCache maps room.Root → open bleve RoomIndex. Lazily populated.
	indexCache map[string]*memindex.RoomIndex
	// sigCache maps room.Root → slice of known MinHash signatures for dedup.
	// In-memory only; rebuilt from .md files on next access after restart.
	sigCache map[string][]minhash.Signature
	// sigIDs maps room.Root → memory ID parallel to sigCache entries (same indices).
	sigIDs map[string][]string
	// indexedDirMtime maps room.MemoriesDir → the directory mtime observed the
	// last time the bleve index and sigCache for that room were known to be in
	// sync with the on-disk .md files. When the directory mtime advances (a new
	// or removed .md file — including memories written directly to disk, e.g. by
	// a peer agent process or a test, NOT via AppendLongTermToScope), the cached
	// bleve index is stale: a Search() would miss the new file and the substring
	// scan fallback never fires (it only runs when the cached index is nil). We
	// then rebuild the bleve index and drop this room's sigCache so the next
	// access rescans the .md sources. A missing key means "never synced" → force
	// a refresh on first access.
	indexedDirMtime map[string]time.Time

	// now returns the current time used to stamp dedup links (minhash.jsonl) and
	// access events (counters.jsonl). Defaults to time.Now().UTC(). It is an
	// injectable clock so tests can assert exact, deterministic timestamps on
	// persisted records instead of depending on wall-clock time at write (M6).
	// Always returns UTC. Set via SetClock; never nil after NewMemoryStore.
	now func() time.Time
}

// SetClock overrides the clock used to stamp persisted memory records
// (minhash.jsonl dedup links, counters.jsonl access events) and retention
// windows. Intended for deterministic tests. The supplied function's result is
// normalised to UTC. Passing nil restores the default time.Now clock.
func (ms *MemoryStore) SetClock(now func() time.Time) {
	if now == nil {
		ms.now = func() time.Time { return time.Now().UTC() }
		return
	}
	ms.now = func() time.Time { return now().UTC() }
}

// SetAgentID wires the agent ID used as the memory author. Called by
// ContextBuilder.WithAgentInfo once the agent ID is known. When not called,
// resolveAuthor falls back to deriving the ID from the private room's path.
func (ms *MemoryStore) SetAgentID(id string) {
	ms.agentID = id
}

// NewMemoryStore creates a MemoryStore with the given agent workspace directory.
// agentWorkspace is the agent's workspace dir (e.g., $OMNIPUS_HOME/agents/<id>/).
// omnipusHome is the $OMNIPUS_HOME data directory.
func NewMemoryStore(agentWorkspace, omnipusHome string) *MemoryStore {
	private := memrooms.MustEnsureRoom(
		memrooms.ResolveAgentPrivateRoom(agentWorkspace),
		"agent:"+filepath.Base(agentWorkspace),
	)
	return &MemoryStore{
		privateRoom:     private,
		omnipusHome:     omnipusHome,
		indexCache:      make(map[string]*memindex.RoomIndex),
		sigCache:        make(map[string][]minhash.Signature),
		sigIDs:          make(map[string][]string),
		indexedDirMtime: make(map[string]time.Time),
		now:             func() time.Time { return time.Now().UTC() },
	}
}

// Close releases all open bleve indexes held by this store.
// Safe to call more than once. After Close, the store must not be used.
func (ms *MemoryStore) Close() {
	ms.indexMu.Lock()
	defer ms.indexMu.Unlock()
	for root, ri := range ms.indexCache {
		if err := ri.Close(); err != nil {
			logger.WarnCF("agent.memory", "Close: failed to close bleve index",
				map[string]any{"room_root": root, "error": err.Error()})
		}
	}
	ms.indexCache = make(map[string]*memindex.RoomIndex)
}

// memoriesDirMtime returns the mtime of a room's memories directory, or the
// zero time when it cannot be stat'd (e.g. not yet created). The directory
// mtime advances when a .md file is added to or removed from it, which is how a
// memory written directly to disk — bypassing AppendLongTermToScope, and thus
// never handed to ri.Index() — becomes observable to a long-lived process whose
// bleve index and sigCache were built while the directory was in an older state.
func memoriesDirMtime(memoriesDir string) time.Time {
	info, err := os.Stat(memoriesDir)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// syncRoomToDiskLocked detects whether the room's memories directory has changed
// on disk since the bleve index / sigCache were last synced, and if so:
//   - rebuilds the bleve index from the .md sources (so Search() sees new files;
//     otherwise it returns zero hits and the substring-scan fallback never runs
//     because that fallback only fires when the cached index is nil), and
//   - drops this room's sigCache so the next dedup check rescans the .md sources.
//
// It then records the observed directory mtime as the new sync baseline.
//
// Crucially, when nothing changed (mtime equals the recorded baseline) it is a
// no-op and does NOT touch the sigCache — so the append path, which records the
// baseline while (re)building the sigCache *before* it reaches the bleve index
// call, never has its freshly built sigCache dropped. This preserves the
// round-2 single-append invariant (no double sig-append on first write) while
// still picking up externally written memory files.
//
// MUST be called with ms.indexMu held. ri may be nil (index unavailable); the
// sigCache drop still happens so dedup stays correct.
func (ms *MemoryStore) syncRoomToDiskLocked(room memrooms.Room, ri *memindex.RoomIndex) {
	current := memoriesDirMtime(room.MemoriesDir)
	baseline, seen := ms.indexedDirMtime[room.MemoriesDir]
	if seen && current.Equal(baseline) {
		return // in sync; nothing to do
	}

	// Directory changed (or first sync). Rebuild the bleve index from the .md
	// sources so externally written memories become searchable.
	if ri != nil {
		if err := ri.Rebuild(); err != nil {
			logger.WarnCF(
				"agent.memory",
				"syncRoomToDisk: bleve rebuild failed; recall may miss new files until next open",
				map[string]any{"room_root": room.Root, "error": err.Error()},
			)
		}
	}

	// Drop the sigCache so the next dedup check rescans the .md sources and picks
	// up any externally written memories. ensureSigCacheLocked then rebuilds it.
	delete(ms.sigCache, room.Root)
	delete(ms.sigIDs, room.Root)

	ms.indexedDirMtime[room.MemoriesDir] = current
}

// roomIndex returns the bleve RoomIndex for room, lazily opening it.
// MUST be called with ms.indexMu held.
//
// Before returning, it reconciles the index with the on-disk memories directory
// via syncRoomToDiskLocked: if a .md file was added or removed since the index
// was last synced (including memories written directly to disk by a peer process
// or a test), the index is rebuilt so a subsequent Search() observes the change.
func (ms *MemoryStore) roomIndexLocked(room memrooms.Room) *memindex.RoomIndex {
	ri, ok := ms.indexCache[room.Root]
	if ok {
		ms.syncRoomToDiskLocked(room, ri)
		return ri
	}
	var openErr error
	ri, openErr = memindex.OpenOrCreate(room)
	if openErr != nil {
		logger.WarnCF("agent.memory", "roomIndex: failed to open bleve index; BM25 disabled for room",
			map[string]any{"room_root": room.Root, "error": openErr.Error()})
		return nil
	}
	ms.indexCache[room.Root] = ri
	// OpenOrCreate already built the index from the current .md sources, so seed
	// the sync baseline to the current directory mtime to avoid an immediate
	// redundant rebuild on the very next access.
	ms.indexedDirMtime[room.MemoriesDir] = memoriesDirMtime(room.MemoriesDir)
	return ri
}

// ensureSigCache lazily loads the MinHash signature cache for room.
// Scans all .md files the first time; afterwards incremental via addSigLocked.
// A directory-mtime change (a new/removed .md file) drops the cache via
// syncRoomToDiskLocked so the scan below re-runs against the current sources.
// MUST be called with ms.indexMu held.
func (ms *MemoryStore) ensureSigCacheLocked(room memrooms.Room) {
	// Reconcile with disk first: if the memories dir changed since the last
	// sync, this drops a stale sigCache so we rescan below. Pass the cached
	// bleve index (if any) so it is rebuilt in the same pass. nil is fine.
	ms.syncRoomToDiskLocked(room, ms.indexCache[room.Root])
	if _, ok := ms.sigCache[room.Root]; ok {
		return
	}
	memories, err := memrooms.ScanMemories(room.MemoriesDir)
	if err != nil {
		logger.WarnCF("agent.memory", "ensureSigCache: scan failed",
			map[string]any{"room_root": room.Root, "error": err.Error()})
		ms.sigCache[room.Root] = nil
		ms.sigIDs[room.Root] = nil
		return
	}
	sigs := make([]minhash.Signature, 0, len(memories))
	ids := make([]string, 0, len(memories))
	for _, mf := range memories {
		sig := minhash.Compute(memorySigText(mf.Frontmatter.Title, mf.Body), minhash.DefaultNumPerm)
		sigs = append(sigs, sig)
		ids = append(ids, mf.Frontmatter.ID)
	}
	ms.sigCache[room.Root] = sigs
	ms.sigIDs[room.Root] = ids
}

// memorySigText composes the canonical text a memory's MinHash signature is
// computed over. The SAME composition MUST be used everywhere a signature is
// derived (cache build AND write-time check); otherwise the cached signature
// (title+body) and the freshly computed check signature would shingle over
// different inputs, so a re-derived signature could never match its own cache
// entry — dedup would silently never fire (the title/body asymmetry bug, M2).
func memorySigText(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		return body
	}
	if body == "" {
		return title
	}
	return title + " " + body
}

// checkAndRegisterSig checks if a memory (title+body) is a near-duplicate of any
// known memory in room. If it is, appends a NearDupRecord to minhash.jsonl and
// returns true. If not, registers the new signature and returns false.
// MUST be called with ms.indexMu held.
//
// The signature is computed over memorySigText(title, body) — the SAME
// composition used to build the cache (ensureSigCacheLocked) so the inputs are
// symmetric (M2: title/body asymmetry fix).
//
// A degenerate (all-zero) signature — produced for memories whose combined
// title+body has fewer than 3 words — is never compared against the cache nor
// registered for future comparison: every all-zero signature Jaccard-matches
// every other all-zero signature at 1.0, which would falsely link unrelated
// short memories (M2: all-zero signature fix).
func (ms *MemoryStore) checkAndRegisterSigLocked(room memrooms.Room, id, title, body string) bool {
	ms.ensureSigCacheLocked(room)

	newSig := minhash.Compute(memorySigText(title, body), minhash.DefaultNumPerm)
	sigs := ms.sigCache[room.Root]
	ids := ms.sigIDs[room.Root]

	// Degenerate signatures (too-short text) carry no discriminating information.
	// Skip dedup entirely and do not pollute the cache with an all-zero entry that
	// would later false-match other short memories.
	if newSig.IsZero() {
		return false
	}

	for i, existing := range sigs {
		// Never compare a memory against itself. The .md file is written to disk
		// (AppendLongTermToScope) BEFORE this dedup check, and ensureSigCacheLocked
		// builds the cache by scanning the memories dir — so on the first write of
		// a room the just-written memory is already in the cache and would match
		// itself at Jaccard 1.0, spuriously logging a self near-dup link (M2).
		if ids[i] == id {
			continue
		}
		if existing.IsZero() {
			// A previously-registered short memory; never a meaningful match.
			continue
		}
		if minhash.IsNearDup(newSig, existing, minhash.DefaultThreshold) {
			existingID := ids[i]
			j := minhash.Jaccard(newSig, existing)
			rec := minhash.NearDupRecord{
				TS:         ms.now(),
				NewID:      id,
				ExistingID: existingID,
				Jaccard:    j,
				RoomRoot:   room.Root,
			}
			mhPath := filepath.Join(room.Root, ".index", minhash.MinHashJSONLFile)
			if appendErr := minhash.AppendNearDupRecord(mhPath, rec); appendErr != nil {
				logger.WarnCF(
					"agent.memory",
					"checkAndRegisterSig: failed to append minhash record",
					map[string]any{
						"room_root":   room.Root,
						"new_id":      id,
						"existing_id": existingID,
						"error":       appendErr.Error(),
					},
				)
			}
			logger.WarnCF("agent.memory", "near-duplicate memory detected (non-destructive link written)",
				map[string]any{"new_id": id, "existing_id": existingID, "jaccard": j})
			// Still register the new sig so we track it for future dedup.
			ms.registerSigLocked(room, id, newSig)
			return true
		}
	}

	// Not a near-dup — register the signature.
	ms.registerSigLocked(room, id, newSig)
	return false
}

// registerSigLocked appends (sig, id) to the room's MinHash signature cache,
// but only if id is not already cached. This prevents a double sig-append on
// the first write to a room: AppendLongTermToScope writes the .md file BEFORE
// the dedup check, and ensureSigCacheLocked's initial dir scan already picks up
// the just-written file — so an unconditional append would cache the same
// memory's signature twice (slow, unbounded cache bloat). On subsequent writes
// the cache already exists (no rescan), the just-written file is not yet
// cached, and the append proceeds normally.
//
// MUST be called with ms.indexMu held.
func (ms *MemoryStore) registerSigLocked(room memrooms.Room, id string, sig minhash.Signature) {
	for _, existingID := range ms.sigIDs[room.Root] {
		if existingID == id {
			return // already cached (scan picked up the just-written file)
		}
	}
	ms.sigCache[room.Root] = append(ms.sigCache[room.Root], sig)
	ms.sigIDs[room.Root] = append(ms.sigIDs[room.Root], id)
}

// SetWorkspaceID wires the shared workspace room for the active turn.
// Called by the adapter when workspace_id is available in tool context.
// workspaceID must be a valid entity ID (no path traversal).
// Passing "" clears the shared room (reverts to private-only).
func (ms *MemoryStore) SetWorkspaceID(workspaceID string) {
	if workspaceID == "" {
		ms.sharedMu.Lock()
		ms.sharedRoom = nil
		ms.sharedMu.Unlock()
		return
	}
	// Guard against path-traversal via crafted workspace IDs.
	if err := validation.EntityID(workspaceID); err != nil {
		logger.WarnCF("agent.memory", "SetWorkspaceID: invalid workspace ID; ignoring",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		ms.sharedMu.Lock()
		ms.sharedRoom = nil
		ms.sharedMu.Unlock()
		return
	}
	// MustEnsureRoom touches the filesystem; do it before taking the lock to keep
	// the critical section to the pointer swap only.
	room := memrooms.MustEnsureRoom(
		memrooms.ResolveWorkspaceSharedRoom(ms.omnipusHome, workspaceID),
		"workspace:"+workspaceID,
	)
	ms.sharedMu.Lock()
	ms.sharedRoom = &room
	ms.sharedMu.Unlock()
}

// currentSharedRoom returns a snapshot of the active shared room pointer under
// the read lock. The returned pointer is stable (SetWorkspaceID swaps the
// pointer rather than mutating the pointee), so callers may use it lock-free.
func (ms *MemoryStore) currentSharedRoom() *memrooms.Room {
	ms.sharedMu.RLock()
	defer ms.sharedMu.RUnlock()
	return ms.sharedRoom
}

// rooms returns the Rooms value for the current state.
func (ms *MemoryStore) rooms() memrooms.Rooms {
	return memrooms.Rooms{
		Private: ms.privateRoom,
		Shared:  ms.currentSharedRoom(),
	}
}

// resolveWriteRoom returns the target room for a write operation given a scope.
// When scope is "shared" but no shared room is set, falls back to private and logs WARN.
func (ms *MemoryStore) resolveWriteRoom(scope memrooms.RoomScope) memrooms.Room {
	switch scope {
	case memrooms.RoomScopeShared:
		if shared := ms.currentSharedRoom(); shared != nil {
			return *shared
		}
		logger.WarnCF("agent.memory", "write requested shared room but no workspace_id set; falling back to private",
			map[string]any{"private_root": ms.privateRoom.Root})
		return ms.privateRoom
	default:
		return ms.privateRoom
	}
}

// AppendLongTerm appends a new per-memory .md file to the target room.
// content must be non-empty, ≤ 4096 runes, no NUL bytes.
// category must be one of key_decision | reference | lesson_learned.
//
// The scope follows the session: if a shared room is active (workspace session),
// writes to shared; otherwise private. Callers can override via the room param
// in the tool (handled by the adapter layer).
func (ms *MemoryStore) AppendLongTerm(content, category string) error {
	return ms.AppendLongTermToScope(content, category, ms.rooms().DefaultRoomScope())
}

// AppendLongTermToScope writes to the specified room scope.
func (ms *MemoryStore) AppendLongTermToScope(content, category string, scope memrooms.RoomScope) error {
	cat, err := ParseMemoryCategory(category)
	if err != nil {
		return err
	}

	content = strings.ReplaceAll(content, "\x00", "")
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return fmt.Errorf("content must not be empty")
	}
	if len([]rune(trimmed)) > 4096 {
		return fmt.Errorf("content exceeds 4096 runes")
	}
	if strings.Contains(trimmed, "<!--") {
		return fmt.Errorf("content must not contain HTML comment markers")
	}

	room := ms.resolveWriteRoom(scope)

	// Generate a new memory ID. We use a UUID-based ID so no external dep is needed.
	// The brief does not mandate ULIDs; a UUID is fine for v0.1.0.
	id := uuid.New().String()

	mf := memrooms.MemoryFile{
		Frontmatter: memrooms.MemoryFrontmatter{
			ID:         id,
			Title:      truncateTitle(trimmed, 80),
			Type:       categoryToMemoryType(cat),
			Tags:       []string{},
			Confidence: 0,
			Status:     memrooms.MemoryStatusActive,
			Supersedes: "",
			Author:     ms.resolveAuthor(),
			BornIn:     ms.resolveBornIn(),
		},
		Body: trimmed,
	}

	// Write the .md file first (FR-7.2 / FR-7.3).
	if err := memrooms.WriteMemoryFile(room.MemoriesDir, mf); err != nil {
		return err
	}

	// MinHash dedup check (FR-7.5 / M-5): non-destructive — links written to
	// minhash.jsonl even if near-dup detected; the .md file is already written.
	// Acquire indexMu to serialize sig cache + bleve index updates AND to guard
	// the bleve index against a concurrent Close(): if we released indexMu before
	// ri.Index(), Close() could close ri underneath us (use-after-close). Holding
	// the lock across the index call keeps ri valid for its whole lifetime here.
	ms.indexMu.Lock()
	isDup := ms.checkAndRegisterSigLocked(room, id, mf.Frontmatter.Title, trimmed)
	// Wire into bleve index (FR-7.4): index unconditionally — even near-dups
	// are indexed (we keep all .md files).
	if ri := ms.roomIndexLocked(room); ri != nil {
		if idxErr := ri.Index(mf); idxErr != nil {
			// Non-fatal: .md file is already written; bleve index will be rebuilt on next open.
			logger.WarnCF("agent.memory", "AppendLongTermToScope: bleve index failed (will rebuild on next open)",
				map[string]any{"id": id, "room_root": room.Root, "error": idxErr.Error()})
		}
	}
	ms.indexMu.Unlock()

	if isDup {
		logger.WarnCF("agent.memory", "near-duplicate memory written (dedup link in minhash.jsonl)",
			map[string]any{"id": id, "room_root": room.Root})
	}

	return nil
}

// SearchEntries performs a case-insensitive literal substring search across
// the in-scope room(s). scope defaults to RoomScopeBoth when a shared room
// is available, private otherwise.
func (ms *MemoryStore) SearchEntries(query string, limit int) ([]LongTermEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	return ms.SearchEntriesInScope(query, limit, memrooms.RoomScopeBoth)
}

// SearchEntriesInScope searches the specified room scope for query using bleve BM25 (FR-7.4).
// Falls back to substring scan when the bleve index is unavailable.
// On each successful recall, appends a CounterRecord (op=access) to counters.jsonl (FR-7.5).
func (ms *MemoryStore) SearchEntriesInScope(
	query string,
	limit int,
	scope memrooms.RoomScope,
) ([]LongTermEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	// Resolve which rooms to search.
	type roomAndIndex struct {
		room memrooms.Room
		ri   *memindex.RoomIndex // nil if unavailable
	}
	var targets []roomAndIndex

	// Snapshot the shared room once under its read lock so the scope branches below
	// observe a consistent value (a concurrent SetWorkspaceID must not swap it mid-read).
	shared := ms.currentSharedRoom()

	ms.indexMu.Lock()
	switch scope {
	case memrooms.RoomScopePrivate:
		targets = []roomAndIndex{{room: ms.privateRoom, ri: ms.roomIndexLocked(ms.privateRoom)}}
	case memrooms.RoomScopeShared:
		if shared != nil {
			targets = []roomAndIndex{{room: *shared, ri: ms.roomIndexLocked(*shared)}}
		} else {
			targets = []roomAndIndex{{room: ms.privateRoom, ri: ms.roomIndexLocked(ms.privateRoom)}}
		}
	default: // RoomScopeBoth
		targets = []roomAndIndex{{room: ms.privateRoom, ri: ms.roomIndexLocked(ms.privateRoom)}}
		if shared != nil {
			targets = append(targets, roomAndIndex{room: *shared, ri: ms.roomIndexLocked(*shared)})
		}
	}
	ms.indexMu.Unlock()

	// Collect results: bleve hits when index available, else substring scan.
	type scoredMemory struct {
		mf    memrooms.MemoryFile
		score float64
	}
	var scored []scoredMemory
	seenIDs := make(map[string]bool)

	for _, t := range targets {
		if t.ri != nil {
			// BM25 path (FR-7.4). Hold indexMu across the Search() call to guard
			// the bleve index against a concurrent Close(): Close() closes every
			// RoomIndex under indexMu, so releasing the lock before t.ri.Search()
			// would risk a use-after-close. This mirrors the append path
			// (AppendLongTermToScope), which holds indexMu across ri.Index().
			ms.indexMu.Lock()
			hits, err := t.ri.Search(query, limit)
			ms.indexMu.Unlock()
			if err != nil {
				logger.WarnCF("agent.memory", "SearchEntriesInScope: bleve search failed; falling back to scan",
					map[string]any{"room_root": t.room.Root, "query": query, "error": err.Error()})
				// Fall through to scan.
			} else {
				agentID := ms.resolveAuthor()
				for _, hit := range hits {
					if seenIDs[hit.ID] {
						continue
					}
					mf, readErr := readMemoryByID(t.room.MemoriesDir, hit.ID)
					if readErr != nil {
						// File may have been deleted externally; skip.
						logger.WarnCF("agent.memory", "SearchEntriesInScope: read memory file failed",
							map[string]any{"id": hit.ID, "error": readErr.Error()})
						continue
					}
					seenIDs[hit.ID] = true
					scored = append(scored, scoredMemory{mf: mf, score: hit.Score})
					// Append counters.jsonl access event (FR-7.5) only for memories
					// actually surfaced — not for deduped or unreadable hits, which
					// would over-count access frequency.
					rec := memrooms.CounterRecord{
						TS:       ms.now(),
						MemoryID: hit.ID,
						Op:       memrooms.CounterOpAccess,
						By:       agentID,
					}
					if appendErr := memrooms.AppendCounterRecord(t.room.CountersPath, rec); appendErr != nil {
						logger.WarnCF("agent.memory", "SearchEntriesInScope: counter append failed",
							map[string]any{"memory_id": hit.ID, "error": appendErr.Error()})
					}
				}
				continue
			}
		}

		// Substring-scan fallback (when bleve index is nil or errored).
		found, scanErr := memrooms.SearchMemories(t.room.MemoriesDir, query)
		if scanErr != nil {
			logger.WarnCF("agent.memory", "SearchEntriesInScope: scan failed",
				map[string]any{"dir": t.room.MemoriesDir, "error": scanErr.Error()})
			continue
		}
		agentIDScan := ms.resolveAuthor()
		for _, mf := range found {
			if !seenIDs[mf.Frontmatter.ID] {
				seenIDs[mf.Frontmatter.ID] = true
				scored = append(scored, scoredMemory{mf: mf, score: 0})
				// Append access counter record for scan-fallback results (FR-7.5).
				rec := memrooms.CounterRecord{
					TS:       ms.now(),
					MemoryID: mf.Frontmatter.ID,
					Op:       memrooms.CounterOpAccess,
					By:       agentIDScan,
				}
				if appendErr := memrooms.AppendCounterRecord(t.room.CountersPath, rec); appendErr != nil {
					logger.WarnCF("agent.memory", "SearchEntriesInScope: counter append failed (scan fallback)",
						map[string]any{"memory_id": mf.Frontmatter.ID, "error": appendErr.Error()})
				}
			}
		}
	}

	// Sort by BM25 score descending (ties: newest-first by mtime).
	dirs := make([]string, 0, len(targets))
	for _, t := range targets {
		dirs = append(dirs, t.room.MemoriesDir)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		// Equal scores: return false to satisfy strict-weak-ordering. The stable
		// sort then preserves insertion order (mtime-sorted scan) for ties.
		return false
	})

	// When all scores are 0 (scan fallback), sort by mtime.
	allZero := true
	for _, s := range scored {
		if s.score != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		all := make([]memrooms.MemoryFile, len(scored))
		for i, s := range scored {
			all[i] = s.mf
		}
		sortMemoriesNewestFirst(dirs, all)
		for i, mf := range all {
			scored[i].mf = mf
		}
	}

	// Resolve each memory's real stored creation time (its file mtime) so the
	// recalled entry reports when the memory was actually written, NOT
	// wall-clock-at-recall. memoryFileToEntry falls back to ms.now() when no
	// mtime is available (e.g. the file was removed between search and build).
	mtimes := memoryFileMtimes(dirs)

	// Build the result list up to limit.
	var results []LongTermEntry
	for i, s := range scored {
		if i >= limit {
			break
		}
		ts, ok := mtimes[s.mf.Frontmatter.ID]
		if !ok {
			ts = ms.now()
		}
		results = append(results, memoryFileToEntry(s.mf, ts))
	}
	return results, nil
}

// readMemoryByID resolves a bleve hit ID to its MemoryFile. The fast path is the
// canonical <memoriesDir>/<id>.md layout. When that file is absent — e.g. a
// memory file whose name does NOT equal its frontmatter ID (an externally
// authored .md, where the bleve index keys on the frontmatter ID but the file on
// disk has a different name) — it falls back to scanning the directory for the
// memory whose frontmatter ID matches. Without this fallback such a hit would be
// silently dropped and its content would never surface in recall, even though
// the index correctly found it.
func readMemoryByID(memoriesDir, id string) (memrooms.MemoryFile, error) {
	mf, err := memrooms.ReadMemoryFile(memoriesDir, id)
	if err == nil {
		return mf, nil
	}
	// Filename ≠ frontmatter ID: locate by scanning. ScanMemories tolerates
	// individual unreadable files, so one bad .md does not abort the lookup.
	memories, scanErr := memrooms.ScanMemories(memoriesDir)
	if scanErr != nil {
		return memrooms.MemoryFile{}, err // surface the original read error
	}
	for _, candidate := range memories {
		if candidate.Frontmatter.ID == id {
			return candidate, nil
		}
	}
	return memrooms.MemoryFile{}, err
}

// memoryFileMtimes builds an ID→mtime map by scanning each memories directory.
// The mtime of a memory's .md file is its real stored creation/modification
// time. Per-directory and per-file errors are skipped (a missing mtime falls
// back to the caller's clock), so this never aborts a recall.
func memoryFileMtimes(dirs []string) map[string]time.Time {
	mtimes := make(map[string]time.Time)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, infoErr := e.Info()
			if infoErr != nil {
				continue
			}
			mtimes[strings.TrimSuffix(e.Name(), ".md")] = info.ModTime().UTC()
		}
	}
	return mtimes
}

// AppendRetro writes a retrospective to the private room's retro directory.
// (Retrospectives are always private — they capture agent reflection, not shared facts.)
func (ms *MemoryStore) AppendRetro(sessionID string, r Retro) error {
	if err := validation.EntityID(sessionID); err != nil {
		return fmt.Errorf("memory: invalid session ID: %w", err)
	}

	dateStr := r.Timestamp.UTC().Format("2006-01-02")
	retroDir := filepath.Join(ms.privateRoom.Root, "retros", dateStr)
	if err := os.MkdirAll(retroDir, 0o700); err != nil {
		return fmt.Errorf("memory: create retro dir: %w", err)
	}

	retroPath := filepath.Join(retroDir, sessionID+"_retro.md")
	ts := r.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
	fallbackStr := "false"
	if r.Fallback {
		fallbackStr = "true"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<!-- ts=%s trigger=%s fallback=%s -->\n", ts, r.Trigger, fallbackStr)
	fmt.Fprintf(&sb, "## Session recap\n%s\n", r.Recap)
	fmt.Fprintf(&sb, "### Went well\n")
	for _, item := range r.WentWell {
		fmt.Fprintf(&sb, "- %s\n", item)
	}
	fmt.Fprintf(&sb, "### Needs improvement\n")
	for _, item := range r.NeedsImprovement {
		fmt.Fprintf(&sb, "- %s\n", item)
	}
	fmt.Fprintf(&sb, "<!-- next -->\n")

	appended := sb.String()

	// Lock a SIDECAR path, never retroPath itself. fileutil.WithFlock opens
	// whatever path it is given with O_CREATE to hold the advisory lock, so
	// locking on retroPath created a zero-byte retro file the instant the lock
	// was taken — before a single content byte was written. Any reader that
	// discovers the retro via a directory listing (the recap idempotency check
	// agentSessionHasRetro does exactly this) could observe that empty file and
	// wrongly conclude the retro had already landed. Measured: 12 torn reads in
	// 200 rounds. retroPath is now only ever touched via WriteFileAtomic's
	// temp-file+rename, so a listing sees it fully absent or fully written —
	// never partial. The read-modify-write stays inside the lock, so concurrent
	// appends (the session-end recap and the agent-invocable retrospective tool
	// can both target one sessionID) still cannot clobber each other.
	lockPath := retroPath + ".lock"

	return fileutil.WithFlock(lockPath, func() error {
		existing, err := os.ReadFile(retroPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("memory: read existing retro: %w", err)
		}

		content := append(existing, []byte(appended)...)
		if err := fileutil.WriteFileAtomic(retroPath, content, 0o600); err != nil {
			return fmt.Errorf("memory: write retro: %w", err)
		}
		return nil
	})
}

// WriteLastSession atomically writes content to the private room's last-session.md.
// last-session.md is a private-room-only artifact (D19); the shared workspace
// room has no last-session.md.
func (ms *MemoryStore) WriteLastSession(content string) error {
	path := filepath.Join(ms.privateRoom.Root, memrooms.LastSessionFile)
	return fileutil.WriteFileAtomic(path, []byte(content), 0o600)
}

// ReadLastSession returns the contents of last-session.md, or "" if absent.
func (ms *MemoryStore) ReadLastSession() (string, error) {
	path := filepath.Join(ms.privateRoom.Root, memrooms.LastSessionFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("memory: read last-session.md: %w", err)
	}
	return string(data), nil
}

// maxMemoryContextChars caps the total size of the "## Long-term memory"
// entries injected by GetMemoryContext. Unlike every other block injected
// into the system prompt (envcontext's
// preamble at 2000 runes, the skills summary's maxSkillsInSummary, the
// manifest block's per-line cap), this section previously had NO size limit
// — up to 20 entries, each independently allowed up to 4096 runes
// (MemoryStore.AppendLongTerm's own cap), could inject 80K+ characters every
// single turn. Entries are re-sorted newest-first by their own Timestamp
// (below) before packing greedily and stopping once the budget is spent —
// this evicts the OLDEST entries first, so the model always sees its most
// recent notes, with a footer naming how many older ones were cut.
const maxMemoryContextChars = 8000

// GetMemoryContext returns a formatted memory context string for the system prompt.
// Reads last-session.md + the most recent N memories from the default scope,
// capped at maxMemoryContextChars with oldest-first eviction.
func (ms *MemoryStore) GetMemoryContext() string {
	var sb strings.Builder

	lastSession, err := ms.ReadLastSession()
	if err == nil && strings.TrimSpace(lastSession) != "" {
		sb.WriteString("## Last Session\n")
		sb.WriteString(lastSession)
	}

	entries, err := ms.SearchEntries("", 20)
	if err == nil && len(entries) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("## Long-term memory\n")
		// Framing: these are past notes, not live ground truth —
		// they may be stale and some may have been written by a teammate
		// agent sharing this workspace, not the acting agent itself.
		sb.WriteString(
			"Notes you and your workspace teammates saved in earlier sessions. They may be " +
				"outdated — prefer what the user tells you now, and use recall_memory to search " +
				"beyond these.\n",
		)

		// Re-sort newest-first by the entries' own Timestamp. SearchEntries'
		// empty-query path (bleve MatchAllQuery) assigns every hit the same
		// constant score, so its own "score descending" sort is a no-op tie
		// and the result order falls back to whatever the index happened to
		// return — NOT reliably recency. This budget's whole "oldest-first
		// eviction" guarantee depends on a real chronological order, so it is
		// established explicitly here rather than trusted from upstream.
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].Timestamp.After(entries[j].Timestamp)
		})

		budget := maxMemoryContextChars
		shown := 0
		for _, e := range entries {
			ts := e.Timestamp.UTC().Format("2006-01-02T15:04:05Z")
			entryText := fmt.Sprintf("[%s | %s]\n%s", ts, e.Category, e.Content)
			sep := ""
			if shown > 0 {
				sep = "\n\n"
			}
			// Rune-safe cost: maxMemoryContextChars is documented (and named)
			// in terms of characters/runes, not bytes. A raw len() here
			// undercounts the budget by up to 4x for non-ASCII content (e.g.
			// non-English memory text), silently collapsing the effective
			// cap to ~2000 runes — the same bug class the manifest-preview
			// truncation fix (commit f4f9b3f8) closed for maxManifestLineLen.
			cost := utf8.RuneCountInString(sep) + utf8.RuneCountInString(entryText)
			// Always show at least the first (newest) entry, even if it alone
			// exceeds the budget — an empty section would be worse than one
			// over-budget entry. Every subsequent entry must fit.
			if shown > 0 && cost > budget {
				break
			}
			sb.WriteString(sep)
			sb.WriteString(entryText)
			budget -= cost
			shown++
		}

		if remaining := len(entries) - shown; remaining > 0 {
			noun := "memories"
			if remaining == 1 {
				noun = "memory"
			}
			fmt.Fprintf(&sb, "\n\n_%d older %s not shown — use recall_memory to search them._", remaining, noun)
		}
	}

	return sb.String()
}

// PrivateRoom returns the private room (for adapter/tool access).
func (ms *MemoryStore) PrivateRoom() memrooms.Room {
	return ms.privateRoom
}

// SharedRoom returns the shared room, or nil if no workspace_id is set.
func (ms *MemoryStore) SharedRoom() *memrooms.Room {
	return ms.currentSharedRoom()
}

// --- helpers ---------------------------------------------------------------

// resolveAuthor returns the agent ID to record as the memory author.
// Prefers the wired agent ID (SetAgentID); falls back to the directory name
// of the agent workspace for callers (e.g. tests) that don't wire it.
func (ms *MemoryStore) resolveAuthor() string {
	if ms.agentID != "" {
		return ms.agentID
	}
	return filepath.Base(filepath.Dir(ms.privateRoom.Root))
}

// resolveBornIn returns the session ID for the born_in frontmatter field.
// In v0.1.0 this is empty — the adapter layer could set it but it's not
// plumbed yet. The field is present in the frontmatter as NFR-7 requires.
func (ms *MemoryStore) resolveBornIn() string {
	return ""
}

// truncateTitle extracts a short title from content (first line, max maxLen runes).
func truncateTitle(content string, maxLen int) string {
	line := strings.SplitN(content, "\n", 2)[0]
	runes := []rune(line)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return line
}

// memoryFileToEntry converts a MemoryFile to a LongTermEntry for the tools
// interface. ts is the memory's real stored creation time (its file mtime);
// callers pass their injectable clock as the fallback when no mtime is
// available, so the recalled timestamp is deterministic and reflects when the
// memory was actually written rather than wall-clock-at-recall.
func memoryFileToEntry(mf memrooms.MemoryFile, ts time.Time) LongTermEntry {
	return LongTermEntry{
		Timestamp: ts.UTC(),
		Category:  memoryTypeToCategory(mf.Frontmatter.Type),
		Content:   mf.Body,
		ID:        mf.Frontmatter.ID,
		Title:     mf.Frontmatter.Title,
	}
}

// memoryTypeToCategory maps the Spec-5 MemoryType back to the legacy MemoryCategory
// used by the tools interface.
func memoryTypeToCategory(t memrooms.MemoryType) MemoryCategory {
	switch t {
	case memrooms.MemoryTypeDecision:
		return CategoryKeyDecision
	case memrooms.MemoryTypeReference:
		return CategoryReference
	case memrooms.MemoryTypeLesson:
		return CategoryLessonLearned
	default:
		return CategoryLegacy
	}
}

// sortMemoriesNewestFirst sorts MemoryFile slice by file mtime descending.
// Falls back to ID sort when mtime is unavailable.
func sortMemoriesNewestFirst(dirs []string, memories []memrooms.MemoryFile) {
	mtimes := memoryFileMtimes(dirs)

	sort.SliceStable(memories, func(i, j int) bool {
		ti := mtimes[memories[i].Frontmatter.ID]
		tj := mtimes[memories[j].Frontmatter.ID]
		if ti.IsZero() && tj.IsZero() {
			return memories[i].Frontmatter.ID > memories[j].Frontmatter.ID
		}
		if ti.IsZero() {
			return false
		}
		if tj.IsZero() {
			return true
		}
		return ti.After(tj)
	})
}

// ReadRetros returns structured Retro records from the last daysBack days (private room).
func (ms *MemoryStore) ReadRetros(daysBack int) ([]Retro, error) {
	if daysBack < 1 {
		daysBack = 1
	}
	if daysBack > 365 {
		daysBack = 365
	}
	retrosBase := filepath.Join(ms.privateRoom.Root, "retros")
	now := ms.now()
	cutoff := now.AddDate(0, 0, -daysBack)
	var retros []Retro

	for i := range daysBack {
		date := now.AddDate(0, 0, -i)
		dateStr := date.Format("2006-01-02")
		dayDir := filepath.Join(retrosBase, dateStr)
		entries, err := os.ReadDir(dayDir)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.WarnCF("agent.memory", "ReadRetros: cannot read day dir",
					map[string]any{"dir": dayDir, "error": err.Error()})
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_retro.md") {
				continue
			}
			retroPath := filepath.Join(dayDir, entry.Name())
			data, err := os.ReadFile(retroPath)
			if err != nil {
				logger.WarnCF("agent.memory", "ReadRetros: skipping unreadable retro file",
					map[string]any{"path": retroPath, "error": err.Error()})
				continue
			}
			fileRetros := parseRetroFile(string(data))
			for _, r := range fileRetros {
				if !r.Timestamp.IsZero() && r.Timestamp.After(cutoff) {
					retros = append(retros, r)
				}
			}
		}
	}

	sort.SliceStable(retros, func(i, j int) bool {
		return retros[i].Timestamp.After(retros[j].Timestamp)
	})
	return retros, nil
}

// SweepRetros deletes retro files older than retentionDays.
func (ms *MemoryStore) SweepRetros(retentionDays int) (int, error) {
	if retentionDays < 0 {
		retentionDays = 0
	}
	retrosBase := filepath.Join(ms.privateRoom.Root, "retros")
	cutoff := ms.now().AddDate(0, 0, -retentionDays)

	entries, err := os.ReadDir(retrosBase)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("memory: sweep retros: read retros dir: %w", err)
	}

	deleted := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirDate, parseErr := time.Parse("2006-01-02", entry.Name())
		if parseErr != nil {
			continue
		}
		if !dirDate.Before(cutoff) {
			continue
		}
		dayDir := filepath.Join(retrosBase, entry.Name())
		retroEntries, readErr := os.ReadDir(dayDir)
		if readErr != nil {
			continue
		}
		for _, retroEntry := range retroEntries {
			if retroEntry.IsDir() || !strings.HasSuffix(retroEntry.Name(), "_retro.md") {
				continue
			}
			retroPath := filepath.Join(dayDir, retroEntry.Name())
			if rmErr := os.Remove(retroPath); rmErr == nil {
				deleted++
			} else {
				logger.WarnCF("agent", "SweepRetros: failed to delete retro file",
					map[string]any{"path": retroPath, "error": rmErr.Error()})
			}
		}
	}
	return deleted, nil
}

// --- retro parsing (unchanged from original) --------------------------------

func parseRetroFile(content string) []Retro {
	blocks := strings.Split(content, "<!-- next -->")
	var retros []Retro
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		r, ok := parseRetroBlock(block)
		if ok {
			retros = append(retros, r)
		}
	}
	return retros
}

func parseRetroBlock(block string) (Retro, bool) {
	if !strings.HasPrefix(block, "<!--") {
		return Retro{}, false
	}
	headerEnd := strings.Index(block, "-->")
	if headerEnd < 0 {
		return Retro{}, false
	}
	header := strings.TrimSpace(block[4:headerEnd])

	var r Retro
	for _, field := range strings.Fields(header) {
		if strings.HasPrefix(field, "ts=") {
			raw := strings.TrimPrefix(field, "ts=")
			parsed, err := time.Parse("2006-01-02T15:04:05.000Z", raw)
			if err == nil {
				r.Timestamp = parsed
			}
		}
		if strings.HasPrefix(field, "trigger=") {
			r.Trigger = RecapTrigger(strings.TrimPrefix(field, "trigger="))
		}
		if strings.HasPrefix(field, "fallback=") {
			r.Fallback = strings.TrimPrefix(field, "fallback=") == "true"
		}
	}

	if r.Timestamp.IsZero() {
		return Retro{}, false
	}

	body := block[headerEnd+3:]
	lines := strings.Split(body, "\n")
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "## Session recap":
			section = "recap"
		case trimmed == "### Went well":
			section = "went_well"
		case trimmed == "### Needs improvement":
			section = "needs_improvement"
		case strings.HasPrefix(trimmed, "- ") && section == "went_well":
			r.WentWell = append(r.WentWell, strings.TrimPrefix(trimmed, "- "))
		case strings.HasPrefix(trimmed, "- ") && section == "needs_improvement":
			r.NeedsImprovement = append(r.NeedsImprovement, strings.TrimPrefix(trimmed, "- "))
		case section == "recap" && trimmed != "":
			if r.Recap == "" {
				r.Recap = trimmed
			} else {
				r.Recap += "\n" + trimmed
			}
		}
	}
	return r, true
}
