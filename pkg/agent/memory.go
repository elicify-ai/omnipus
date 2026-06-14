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
	"time"

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/memrooms"
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
type MemoryStore struct {
	// privateRoom is the per-agent private room. Never nil after NewMemoryStore.
	privateRoom memrooms.Room

	// omnipusHome is the resolved $OMNIPUS_HOME used to build workspace room paths.
	omnipusHome string

	// sharedRoom is the active workspace shared room for the current turn.
	// Nil when no workspace_id is set. Set by SetWorkspaceID before each turn.
	sharedRoom *memrooms.Room
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
	}
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

	return memrooms.WriteMemoryFile(room.MemoriesDir, mf)
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

// SearchEntriesInScope searches the specified room scope for query.
func (ms *MemoryStore) SearchEntriesInScope(query string, limit int, scope memrooms.RoomScope) ([]LongTermEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	var dirs []string
	switch scope {
	case memrooms.RoomScopePrivate:
		dirs = []string{ms.privateRoom.MemoriesDir}
	case memrooms.RoomScopeShared:
		if ms.sharedRoom != nil {
			dirs = []string{ms.sharedRoom.MemoriesDir}
		} else {
			// Fallback to private.
			dirs = []string{ms.privateRoom.MemoriesDir}
		}
	case memrooms.RoomScopeBoth:
		dirs = []string{ms.privateRoom.MemoriesDir}
		if ms.sharedRoom != nil {
			dirs = append(dirs, ms.sharedRoom.MemoriesDir)
		}
	default:
		dirs = []string{ms.privateRoom.MemoriesDir}
	}

	var all []memrooms.MemoryFile
	seenIDs := make(map[string]bool)
	for _, dir := range dirs {
		found, err := memrooms.SearchMemories(dir, query)
		if err != nil {
			logger.WarnCF("agent.memory", "SearchEntriesInScope: scan failed",
				map[string]any{"dir": dir, "error": err.Error()})
			continue
		}
		for _, mf := range found {
			if !seenIDs[mf.Frontmatter.ID] {
				seenIDs[mf.Frontmatter.ID] = true
				all = append(all, mf)
			}
		}
	}

	// Sort newest-first. For now we use ID lexicographic order (UUIDs are not
	// time-ordered). When bleve is added (separate unit), ranking supersedes this.
	// We derive an approximate recency from the file mtime when available.
	sortMemoriesNewestFirst(dirs, all)

	var results []LongTermEntry
	for i, mf := range all {
		if i >= limit {
			break
		}
		results = append(results, memoryFileToEntry(mf))
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
