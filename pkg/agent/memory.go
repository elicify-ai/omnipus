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
// The 3 tools (remember/recall_memory/retrospective) are re-pointed here.
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

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/memrooms"
	memindex "github.com/dapicom-ai/omnipus/pkg/memrooms/index"
	"github.com/dapicom-ai/omnipus/pkg/memrooms/minhash"
	"github.com/dapicom-ai/omnipus/pkg/validation"
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

	// omnipusHome is the resolved $OMNIPUS_HOME used to build workspace room paths.
	omnipusHome string

	// sharedRoom is the active workspace shared room for the current turn.
	// Nil when no workspace_id is set. Set by SetWorkspaceID before each turn.
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
		privateRoom: private,
		omnipusHome: omnipusHome,
		indexCache:  make(map[string]*memindex.RoomIndex),
		sigCache:    make(map[string][]minhash.Signature),
		sigIDs:      make(map[string][]string),
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

// roomIndex returns the bleve RoomIndex for room, lazily opening it.
// MUST be called with ms.indexMu held.
func (ms *MemoryStore) roomIndexLocked(room memrooms.Room) *memindex.RoomIndex {
	ri, ok := ms.indexCache[room.Root]
	if ok {
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
	return ri
}

// ensureSigCache lazily loads the MinHash signature cache for room.
// Scans all .md files the first time; afterwards incremental via addSigLocked.
// MUST be called with ms.indexMu held.
func (ms *MemoryStore) ensureSigCacheLocked(room memrooms.Room) {
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
		text := mf.Frontmatter.Title + " " + mf.Body
		sig := minhash.Compute(text, minhash.DefaultNumPerm)
		sigs = append(sigs, sig)
		ids = append(ids, mf.Frontmatter.ID)
	}
	ms.sigCache[room.Root] = sigs
	ms.sigIDs[room.Root] = ids
}

// checkAndRegisterSig checks if content is a near-duplicate of any known memory
// in room. If it is, appends a NearDupRecord to minhash.jsonl and returns true.
// If not, registers the new signature and returns false.
// MUST be called with ms.indexMu held.
func (ms *MemoryStore) checkAndRegisterSigLocked(room memrooms.Room, id, content string) bool {
	ms.ensureSigCacheLocked(room)

	newSig := minhash.Compute(content, minhash.DefaultNumPerm)
	sigs := ms.sigCache[room.Root]
	ids := ms.sigIDs[room.Root]

	for i, existing := range sigs {
		if minhash.IsNearDup(newSig, existing, minhash.DefaultThreshold) {
			existingID := ids[i]
			j := minhash.Jaccard(newSig, existing)
			rec := minhash.NearDupRecord{
				TS:         time.Now().UTC(),
				NewID:      id,
				ExistingID: existingID,
				Jaccard:    j,
				RoomRoot:   room.Root,
			}
			mhPath := filepath.Join(room.Root, ".index", minhash.MinHashJSONLFile)
			if appendErr := minhash.AppendNearDupRecord(mhPath, rec); appendErr != nil {
				logger.WarnCF("agent.memory", "checkAndRegisterSig: failed to append minhash record",
					map[string]any{"room_root": room.Root, "new_id": id, "existing_id": existingID, "error": appendErr.Error()})
			}
			logger.WarnCF("agent.memory", "near-duplicate memory detected (non-destructive link written)",
				map[string]any{"new_id": id, "existing_id": existingID, "jaccard": j})
			// Still register the new sig so we track it for future dedup.
			ms.sigCache[room.Root] = append(sigs, newSig)
			ms.sigIDs[room.Root] = append(ids, id)
			return true
		}
	}

	// Not a near-dup — register the signature.
	ms.sigCache[room.Root] = append(sigs, newSig)
	ms.sigIDs[room.Root] = append(ids, id)
	return false
}

// SetWorkspaceID wires the shared workspace room for the active turn.
// Called by the adapter when workspace_id is available in tool context.
// workspaceID must be a valid entity ID (no path traversal).
// Passing "" clears the shared room (reverts to private-only).
func (ms *MemoryStore) SetWorkspaceID(workspaceID string) {
	if workspaceID == "" {
		ms.sharedRoom = nil
		return
	}
	// Guard against path-traversal via crafted workspace IDs.
	if err := validation.EntityID(workspaceID); err != nil {
		logger.WarnCF("agent.memory", "SetWorkspaceID: invalid workspace ID; ignoring",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		ms.sharedRoom = nil
		return
	}
	room := memrooms.MustEnsureRoom(
		memrooms.ResolveWorkspaceSharedRoom(ms.omnipusHome, workspaceID),
		"workspace:"+workspaceID,
	)
	ms.sharedRoom = &room
}

// rooms returns the Rooms value for the current state.
func (ms *MemoryStore) rooms() memrooms.Rooms {
	return memrooms.Rooms{
		Private: ms.privateRoom,
		Shared:  ms.sharedRoom,
	}
}

// resolveWriteRoom returns the target room for a write operation given a scope.
// When scope is "shared" but no shared room is set, falls back to private and logs WARN.
func (ms *MemoryStore) resolveWriteRoom(scope memrooms.RoomScope) memrooms.Room {
	switch scope {
	case memrooms.RoomScopeShared:
		if ms.sharedRoom != nil {
			return *ms.sharedRoom
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
	// Acquire indexMu to serialise sig cache + bleve index updates.
	ms.indexMu.Lock()
	isDup := ms.checkAndRegisterSigLocked(room, id, trimmed)
	// Wire into bleve index (FR-7.4): index unconditionally — even near-dups
	// are indexed (we keep all .md files).
	ri := ms.roomIndexLocked(room)
	ms.indexMu.Unlock()

	if isDup {
		logger.WarnCF("agent.memory", "near-duplicate memory written (dedup link in minhash.jsonl)",
			map[string]any{"id": id, "room_root": room.Root})
	}

	// Index in bleve after releasing indexMu. ri.Index() is internally serialised.
	if ri != nil {
		if idxErr := ri.Index(mf); idxErr != nil {
			// Non-fatal: .md file is already written; bleve index will be rebuilt on next open.
			logger.WarnCF("agent.memory", "AppendLongTermToScope: bleve index failed (will rebuild on next open)",
				map[string]any{"id": id, "room_root": room.Root, "error": idxErr.Error()})
		}
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
func (ms *MemoryStore) SearchEntriesInScope(query string, limit int, scope memrooms.RoomScope) ([]LongTermEntry, error) {
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

	ms.indexMu.Lock()
	switch scope {
	case memrooms.RoomScopePrivate:
		targets = []roomAndIndex{{room: ms.privateRoom, ri: ms.roomIndexLocked(ms.privateRoom)}}
	case memrooms.RoomScopeShared:
		if ms.sharedRoom != nil {
			targets = []roomAndIndex{{room: *ms.sharedRoom, ri: ms.roomIndexLocked(*ms.sharedRoom)}}
		} else {
			targets = []roomAndIndex{{room: ms.privateRoom, ri: ms.roomIndexLocked(ms.privateRoom)}}
		}
	default: // RoomScopeBoth
		targets = []roomAndIndex{{room: ms.privateRoom, ri: ms.roomIndexLocked(ms.privateRoom)}}
		if ms.sharedRoom != nil {
			targets = append(targets, roomAndIndex{room: *ms.sharedRoom, ri: ms.roomIndexLocked(*ms.sharedRoom)})
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
			// BM25 path (FR-7.4).
			hits, err := t.ri.Search(query, limit)
			if err != nil {
				logger.WarnCF("agent.memory", "SearchEntriesInScope: bleve search failed; falling back to scan",
					map[string]any{"room_root": t.room.Root, "query": query, "error": err.Error()})
				// Fall through to scan.
			} else {
				for _, hit := range hits {
					if seenIDs[hit.ID] {
						continue
					}
					mf, readErr := memrooms.ReadMemoryFile(t.room.MemoriesDir, hit.ID)
					if readErr != nil {
						// File may have been deleted externally; skip.
						logger.WarnCF("agent.memory", "SearchEntriesInScope: read memory file failed",
							map[string]any{"id": hit.ID, "error": readErr.Error()})
						continue
					}
					seenIDs[hit.ID] = true
					scored = append(scored, scoredMemory{mf: mf, score: hit.Score})
				}
				// Append counters.jsonl access events (FR-7.5).
				agentID := ms.resolveAuthor()
				for _, hit := range hits {
					rec := memrooms.CounterRecord{
						TS:       time.Now().UTC(),
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
					TS:       time.Now().UTC(),
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
		return true // stable sort preserves insertion order (mtime-sorted scan)
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

	// Build the result list up to limit.
	var results []LongTermEntry
	for i, s := range scored {
		if i >= limit {
			break
		}
		results = append(results, memoryFileToEntry(s.mf))
	}
	return results, nil
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

	content := sb.String()

	return fileutil.WithFlock(retroPath, func() error {
		f, err := os.OpenFile(retroPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("memory: open retro file: %w", err)
		}
		if _, err := f.WriteString(content); err != nil {
			f.Close()
			return fmt.Errorf("memory: write retro: %w", err)
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return fmt.Errorf("memory: sync retro: %w", err)
		}
		return f.Close()
	})
}

// WriteLastSession atomically writes content to the private room's last-session.md.
func (ms *MemoryStore) WriteLastSession(content string) error {
	return fileutil.WriteFileAtomic(ms.privateRoom.LastSessionPath, []byte(content), 0o600)
}

// ReadLastSession returns the contents of last-session.md, or "" if absent.
func (ms *MemoryStore) ReadLastSession() (string, error) {
	data, err := os.ReadFile(ms.privateRoom.LastSessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("memory: read last-session.md: %w", err)
	}
	return string(data), nil
}

// GetMemoryContext returns a formatted memory context string for the system prompt.
// Reads last-session.md + the most recent N memories from the default scope.
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
		for i, e := range entries {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			ts := e.Timestamp.UTC().Format("2006-01-02T15:04:05Z")
			fmt.Fprintf(&sb, "[%s | %s]\n%s", ts, e.Category, e.Content)
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
	return ms.sharedRoom
}

// --- helpers ---------------------------------------------------------------

// resolveAuthor returns the agent ID to record as the memory author.
// Since MemoryStore doesn't carry the agent ID directly, we read the directory name.
func (ms *MemoryStore) resolveAuthor() string {
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

// memoryFileToEntry converts a MemoryFile to a LongTermEntry for the tools interface.
func memoryFileToEntry(mf memrooms.MemoryFile) LongTermEntry {
	return LongTermEntry{
		Timestamp: time.Now().UTC(), // placeholder; bleve will provide real timestamps
		Category:  memoryTypeToCategory(mf.Frontmatter.Type),
		Content:   mf.Body,
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
	// Build a dir→entries map for mtime lookups.
	type dirEntry struct {
		dir   string
		mtime time.Time
	}
	mtimes := make(map[string]time.Time, len(memories))
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".md")
			mtimes[id] = info.ModTime()
		}
	}

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
	cutoff := time.Now().UTC().AddDate(0, 0, -daysBack)
	var retros []Retro

	for i := range daysBack {
		date := time.Now().UTC().AddDate(0, 0, -i)
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
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

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
