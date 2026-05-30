// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/validation"
)

// MemoryStore manages persistent memory for the agent.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
// - Last session: memory/sessions/LAST_SESSION.md
// - Retrospectives: memory/sessions/YYYY-MM-DD/<sessionID>_retro.md
type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string

	// mu protects the mtime-keyed entry cache.
	mu              sync.Mutex
	entryCacheMtime time.Time
	entryCache      []LongTermEntry
}

// MemoryCategory is the closed set of categories an agent may tag a long-term
// memory entry with. Keeping it typed (rather than a free-form string) makes
// the domain explicit at every call site and catches drift at compile time.
type MemoryCategory string

const (
	CategoryKeyDecision   MemoryCategory = "key_decision"
	CategoryReference     MemoryCategory = "reference"
	CategoryLessonLearned MemoryCategory = "lesson_learned"
	// CategoryLegacy is assigned to entries parsed from pre-structured
	// MEMORY.md files (no ts/cat header). Not valid on write.
	CategoryLegacy MemoryCategory = "legacy"
	// CategoryLastSession / CategoryRetro are synthetic categories applied
	// to entries surfaced through SearchEntries from non-MEMORY.md sources.
	CategoryLastSession MemoryCategory = "last_session"
	CategoryRetro       MemoryCategory = "retro"
)

// ParseMemoryCategory validates and returns a typed category from a string.
// Accepts only the three AppendLongTerm-legal values; anything else is an
// error so callers can't silently persist "garbage" as cat=garbage.
func ParseMemoryCategory(s string) (MemoryCategory, error) {
	switch MemoryCategory(s) {
	case CategoryKeyDecision, CategoryReference, CategoryLessonLearned:
		return MemoryCategory(s), nil
	}
	return "", fmt.Errorf("invalid category %q (expected one of: key_decision, reference, lesson_learned)", s)
}

// RecapTrigger is the closed set of triggers recorded on a Retro. Keeping this
// typed means a future refactor cannot quietly introduce a fourth source
// without the type system noticing.
type RecapTrigger string

const (
	TriggerExplicit  RecapTrigger = "explicit"
	TriggerLazy      RecapTrigger = "lazy"
	TriggerIdle      RecapTrigger = "idle"
	TriggerBootstrap RecapTrigger = "bootstrap"
	TriggerJoined    RecapTrigger = "joined"
)

// LongTermEntry is a single parsed entry from MEMORY.md.
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

// NewMemoryStore creates a new MemoryStore with the given workspace path.
// It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure memory directory exists
	if mkErr := os.MkdirAll(memoryDir, 0o755); mkErr != nil {
		// Non-fatal: the agent can still operate without the memory dir;
		// reads will return empty and writes will fail with a clear error.
		logger.WarnCF("agent", "Failed to create memory directory",
			map[string]any{"memory_dir": memoryDir, "error": mkErr.Error()})
	}

	return &MemoryStore{
		workspace:  workspace,
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
	}
}

// getTodayFile returns the path to today's daily note file (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) getTodayFile() string {
	today := time.Now().Format("20060102") // YYYYMMDD
	monthDir := today[:6]                  // YYYYMM
	filePath := filepath.Join(ms.memoryDir, monthDir, today+".md")
	return filePath
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadLongTerm() string {
	if data, err := os.ReadFile(ms.memoryFile); err == nil {
		return string(data)
	}
	return ""
}

// WriteLongTerm writes content to the long-term memory file (MEMORY.md).
func (ms *MemoryStore) WriteLongTerm(content string) error {
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	return fileutil.WriteFileAtomic(ms.memoryFile, []byte(content), 0o600)
}

// AppendLongTerm appends a new entry to MEMORY.md under advisory flock.
// FR-001: category must be one of key_decision | reference | lesson_learned.
// FR-002: content must be non-empty, ≤ 4096 runes, and must not contain "<!--".
// FR-003: NUL bytes are stripped silently.
func (ms *MemoryStore) AppendLongTerm(content, category string) error {
	// Validate category (FR-001) via the typed enum parser so callers can
	// never smuggle a freeform "cat=garbage" onto disk.
	if _, err := ParseMemoryCategory(category); err != nil {
		return err
	}

	// Strip NUL bytes silently (FR-003).
	content = strings.ReplaceAll(content, "\x00", "")

	// Validate content (FR-002).
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

	// Ensure memory directory exists before acquiring flock.
	if err := os.MkdirAll(ms.memoryDir, 0o700); err != nil {
		return fmt.Errorf("memory: create memory dir: %w", err)
	}

	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	return fileutil.WithFlock(ms.memoryFile, func() error {
		// Check whether file is non-empty so we can emit the separator.
		var needsSeparator bool
		if info, err := os.Stat(ms.memoryFile); err == nil && info.Size() > 0 {
			needsSeparator = true
		}

		f, err := os.OpenFile(ms.memoryFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("memory: open MEMORY.md for append: %w", err)
		}

		var writeErr error
		if needsSeparator {
			if _, err := fmt.Fprintf(f, "<!-- next -->\n\n"); err != nil {
				f.Close()
				return fmt.Errorf("memory: write separator: %w", err)
			}
		}

		_, writeErr = fmt.Fprintf(f, "<!-- ts=%s cat=%s -->\n%s\n", ts, category, trimmed)
		if writeErr != nil {
			f.Close()
			return fmt.Errorf("memory: write entry: %w", writeErr)
		}

		if err := f.Sync(); err != nil {
			f.Close()
			return fmt.Errorf("memory: sync MEMORY.md: %w", err)
		}
		return f.Close()
	})
}

// ReadLongTermEntries parses MEMORY.md into typed LongTermEntry values.
// Results are cached mtime-keyed; the cache is reused when the file has not changed.
// FR-004: returns newest-first.
// FR-006: legacy MEMORY.md (no separators) → single entry with cat=legacy, ts=<file mtime>.
func (ms *MemoryStore) ReadLongTermEntries() ([]LongTermEntry, error) {
	info, err := os.Stat(ms.memoryFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: stat MEMORY.md: %w", err)
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Cache hit: mtime has not advanced. Return a copy so callers cannot race
	// on the shared slice (H4 fix — shared mutable slice race).
	if !ms.entryCacheMtime.IsZero() && !info.ModTime().After(ms.entryCacheMtime) {
		out := make([]LongTermEntry, len(ms.entryCache))
		copy(out, ms.entryCache)
		return out, nil
	}

	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		return nil, fmt.Errorf("memory: read MEMORY.md: %w", err)
	}

	entries := parseLongTermEntries(string(data), info.ModTime())

	// Store newest-first.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	ms.entryCache = entries
	ms.entryCacheMtime = info.ModTime()
	// Return a copy so callers cannot race on the shared cache slice.
	out := make([]LongTermEntry, len(entries))
	copy(out, entries)
	return out, nil
}

// parseLongTermEntries splits raw MEMORY.md content into individual LongTermEntry values.
// Handles both the structured format (<!-- next --> + <!-- ts= cat= -->) and legacy
// free-form content (no separators → single entry with cat=legacy, ts=fileMtime).
func parseLongTermEntries(content string, fileMtime time.Time) []LongTermEntry {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	// Split by "<!-- next -->" separator.
	blocks := strings.Split(content, "<!-- next -->")
	var entries []LongTermEntry
	isLegacy := true

	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}

		// Attempt to parse the header line: <!-- ts=... cat=... -->
		entry, ok := parseEntryBlock(block)
		if ok {
			isLegacy = false
			entries = append(entries, entry)
		} else if isLegacy {
			// Legacy entry: no structured header anywhere in the file.
			entries = append(entries, LongTermEntry{
				Timestamp: fileMtime,
				Category:  CategoryLegacy,
				Content:   block,
			})
		} else {
			// A structured file should not carry unparseable blocks. A dropped
			// entry silently shrinks user memory, which is the kind of
			// regression that must show up in logs. DEBUG rather than WARN
			// because a corrupted block is rare and each one fires at read time.
			logger.DebugCF("agent.memory", "Skipping unparseable MEMORY.md block",
				map[string]any{"block_bytes": len(block)})
		}
	}

	return entries
}

// parseEntryBlock attempts to extract a structured entry from a single block.
// Returns the entry and true on success.
func parseEntryBlock(block string) (LongTermEntry, bool) {
	// Header format: <!-- ts=<ISO8601> cat=<category> -->
	if !strings.HasPrefix(block, "<!--") {
		return LongTermEntry{}, false
	}
	headerEnd := strings.Index(block, "-->")
	if headerEnd < 0 {
		return LongTermEntry{}, false
	}
	header := block[4:headerEnd] // content between <!-- and -->
	header = strings.TrimSpace(header)

	var ts time.Time
	var category string

	for _, field := range strings.Fields(header) {
		if strings.HasPrefix(field, "ts=") {
			raw := strings.TrimPrefix(field, "ts=")
			parsed, err := time.Parse("2006-01-02T15:04:05.000Z", raw)
			if err == nil {
				ts = parsed
			}
		}
		if strings.HasPrefix(field, "cat=") {
			category = strings.TrimPrefix(field, "cat=")
		}
	}

	if ts.IsZero() && category == "" {
		return LongTermEntry{}, false
	}

	contentStart := headerEnd + 3 // skip past "-->"
	bodyContent := strings.TrimSpace(block[contentStart:])

	return LongTermEntry{
		Timestamp: ts,
		Category:  MemoryCategory(category),
		Content:   bodyContent,
	}, true
}

// SearchEntries performs a case-insensitive literal substring search across:
// - MEMORY.md entries
// - LAST_SESSION.md (as a single entry with cat=last_session)
// - retrospectives from the last 30 days
// Results are newest-first. limit defaults to 20 if ≤ 0, max 50.
// FR-005: no regex — literal substring match only.
func (ms *MemoryStore) SearchEntries(query string, limit int) ([]LongTermEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	lowerQuery := strings.ToLower(query)

	var candidates []LongTermEntry

	// 1. Long-term memory entries.
	memEntries, err := ms.ReadLongTermEntries()
	if err != nil {
		return nil, fmt.Errorf("memory: search: read long-term entries: %w", err)
	}
	candidates = append(candidates, memEntries...)

	// 2. LAST_SESSION.md as a single entry.
	lastSession, err := ms.ReadLastSession()
	if err == nil && strings.TrimSpace(lastSession) != "" {
		// Stat for timestamp.
		lsPath := filepath.Join(ms.memoryDir, "sessions", "LAST_SESSION.md")
		var lsTime time.Time
		if info, statErr := os.Stat(lsPath); statErr == nil {
			lsTime = info.ModTime()
		} else {
			lsTime = time.Now().UTC()
		}
		candidates = append(candidates, LongTermEntry{
			Timestamp: lsTime,
			Category:  CategoryLastSession,
			Content:   lastSession,
		})
	}

	// 3. Retrospectives from the last 30 days. A read failure silently
	// shrinks recall results — operators must see it in logs so a permission
	// regression on the retros directory doesn't hide every past session.
	retros, retrosErr := ms.ReadRetros(30)
	if retrosErr != nil {
		logger.WarnCF("agent.memory", "SearchEntries: ReadRetros failed; recall will be incomplete",
			map[string]any{"error": retrosErr.Error()})
	} else {
		for _, r := range retros {
			retroContent := formatRetroForSearch(r)
			candidates = append(candidates, LongTermEntry{
				Timestamp: r.Timestamp,
				Category:  CategoryRetro,
				Content:   retroContent,
			})
		}
	}

	// Sort candidates newest-first.
	sortEntriesNewestFirst(candidates)

	// Deduplicate by timestamp (same-millisecond duplicates can arise when
	// LAST_SESSION and a retro share an mtime).
	seen := make(map[time.Time]bool, len(candidates))
	var deduped []LongTermEntry
	for _, e := range candidates {
		if seen[e.Timestamp] {
			continue
		}
		seen[e.Timestamp] = true
		deduped = append(deduped, e)
	}

	// Filter by query substring match.
	var results []LongTermEntry
	for _, e := range deduped {
		if strings.Contains(strings.ToLower(e.Content), lowerQuery) ||
			strings.Contains(strings.ToLower(string(e.Category)), lowerQuery) {
			results = append(results, e)
			if len(results) >= limit {
				break
			}
		}
	}

	return results, nil
}

// formatRetroForSearch renders a Retro as a plain text block for substring matching.
func formatRetroForSearch(r Retro) string {
	var sb strings.Builder
	sb.WriteString(r.Recap)
	for _, w := range r.WentWell {
		sb.WriteString("\n+ ")
		sb.WriteString(w)
	}
	for _, n := range r.NeedsImprovement {
		sb.WriteString("\n- ")
		sb.WriteString(n)
	}
	return sb.String()
}

// sortEntriesNewestFirst sorts LongTermEntry slice in descending timestamp order.
func sortEntriesNewestFirst(entries []LongTermEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
}

// WriteLastSession atomically writes content to memory/sessions/LAST_SESSION.md.
// FR-007.
func (ms *MemoryStore) WriteLastSession(content string) error {
	sessionsDir := filepath.Join(ms.memoryDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return fmt.Errorf("memory: create sessions dir: %w", err)
	}
	lsPath := filepath.Join(sessionsDir, "LAST_SESSION.md")
	return fileutil.WriteFileAtomic(lsPath, []byte(content), 0o600)
}

// ReadLastSession returns the contents of LAST_SESSION.md, or empty string if absent.
// FR-008.
func (ms *MemoryStore) ReadLastSession() (string, error) {
	lsPath := filepath.Join(ms.memoryDir, "sessions", "LAST_SESSION.md")
	data, err := os.ReadFile(lsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("memory: read LAST_SESSION.md: %w", err)
	}
	return string(data), nil
}

// AppendRetro writes a structured retrospective to
// memory/sessions/<YYYY-MM-DD>/<sessionID>_retro.md.
// FR-009: uses advisory flock. sessionID is validated via validation.EntityID.
func (ms *MemoryStore) AppendRetro(sessionID string, r Retro) error {
	// Validate sessionID via pkg/validation (FR-009, spec v7 FR-062).
	if err := validation.EntityID(sessionID); err != nil {
		return fmt.Errorf("memory: invalid session ID: %w", err)
	}

	dateStr := r.Timestamp.UTC().Format("2006-01-02")
	retroDir := filepath.Join(ms.memoryDir, "sessions", dateStr)
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

// ReadRetros returns structured Retro records from the last daysBack days.
// Clamps daysBack to 1..365. Files that don't parse are silently skipped.
// FR-010.
func (ms *MemoryStore) ReadRetros(daysBack int) ([]Retro, error) {
	if daysBack < 1 {
		daysBack = 1
	}
	if daysBack > 365 {
		daysBack = 365
	}

	sessionsDir := filepath.Join(ms.memoryDir, "sessions")
	cutoff := time.Now().UTC().AddDate(0, 0, -daysBack)

	var retros []Retro

	for i := range daysBack {
		date := time.Now().UTC().AddDate(0, 0, -i)
		dateStr := date.Format("2006-01-02")
		dayDir := filepath.Join(sessionsDir, dateStr)

		entries, err := os.ReadDir(dayDir)
		if err != nil {
			// Missing day dirs are normal (no retros that day). Other errors
			// (permission, I/O) indicate a real problem that would otherwise
			// silently shrink recall results.
			if !os.IsNotExist(err) {
				logger.WarnCF("agent.memory", "ReadRetros: cannot read day dir",
					map[string]any{"dir": dayDir, "error": err.Error()})
			}
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, "_retro.md") {
				continue
			}
			retroFilePath := filepath.Join(dayDir, name)
			data, err := os.ReadFile(retroFilePath)
			if err != nil {
				logger.DebugCF("agent.memory", "ReadRetros: skipping unreadable retro",
					map[string]any{"path": retroFilePath, "error": err.Error()})
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

// parseRetroFile parses one or more retro blocks from a retro file.
// Blocks are separated by "<!-- next -->" lines. Invalid blocks are skipped.
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

// parseRetroBlock parses a single retro block. Returns the Retro and true on success.
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

// SweepRetros deletes retro files whose enclosing date directory is older
// than retentionDays days. Returns the count of deleted files.
// FR-031.
func (ms *MemoryStore) SweepRetros(retentionDays int) (int, error) {
	if retentionDays < 0 {
		retentionDays = 0
	}
	sessionsDir := filepath.Join(ms.memoryDir, "sessions")
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("memory: sweep retros: read sessions dir: %w", err)
	}

	deleted := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Parse the directory name as YYYY-MM-DD.
		dirDate, parseErr := time.Parse("2006-01-02", entry.Name())
		if parseErr != nil {
			// Not a date directory; skip.
			continue
		}
		if !dirDate.Before(cutoff) {
			// Within retention window; skip.
			continue
		}

		dayDir := filepath.Join(sessionsDir, entry.Name())
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

// ReadToday reads today's daily note.
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadToday() string {
	todayFile := ms.getTodayFile()
	if data, err := os.ReadFile(todayFile); err == nil {
		return string(data)
	}
	return ""
}

// AppendToday appends content to today's daily note.
// If the file doesn't exist, it creates a new file with a date header.
func (ms *MemoryStore) AppendToday(content string) error {
	todayFile := ms.getTodayFile()

	// Ensure month directory exists
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return err
	}

	var existingContent string
	if data, err := os.ReadFile(todayFile); err == nil {
		existingContent = string(data)
	}

	var newContent string
	if existingContent == "" {
		// Add header for new day
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		newContent = header + content
	} else {
		// Append to existing content
		newContent = existingContent + "\n" + content
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(todayFile, []byte(newContent), 0o600)
}

// GetRecentDailyNotes returns daily notes from the last N days.
// Contents are joined with "---" separator.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var sb strings.Builder
	first := true

	for i := range days {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102") // YYYYMMDD
		monthDir := dateStr[:6]            // YYYYMM
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")

		if data, err := os.ReadFile(filePath); err == nil {
			if !first {
				sb.WriteString("\n\n---\n\n")
			}
			sb.Write(data)
			first = false
		}
	}

	return sb.String()
}

// GetMemoryContext returns formatted memory context for the agent system prompt.
// FR-019: includes LAST_SESSION.md before long-term memory.
// FR-020: budgets MEMORY.md content at 12000 runes; falls back to newest N entries.
func (ms *MemoryStore) GetMemoryContext() string {
	var sb strings.Builder

	// Section 1: Last session.
	lastSession, err := ms.ReadLastSession()
	if err == nil && strings.TrimSpace(lastSession) != "" {
		sb.WriteString("## Last Session\n")
		sb.WriteString(lastSession)
	}

	// Section 2: Long-term memory from MEMORY.md.
	entries, entryErr := ms.ReadLongTermEntries()
	if entryErr == nil && len(entries) > 0 {
		// Build the full memory text from all entries (already newest-first).
		fullMemory := buildFullMemoryText(entries)

		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("## Long-term memory\n")

		const runesBudget = 12000
		if len([]rune(fullMemory)) <= runesBudget {
			// Fits in budget: emit entire memory.
			sb.WriteString(fullMemory)
		} else {
			// Budget exceeded: emit newest N entries totalling ≤ 12000 runes (min N=10).
			sb.WriteString(buildBudgetedMemoryText(entries, runesBudget))
			sb.WriteString("\n\nolder entries available via recall_memory")
		}
	}

	return sb.String()
}

// buildFullMemoryText serializes all entries to a human-readable block (newest-first).
// Entries are separated by a blank line.
func buildFullMemoryText(entries []LongTermEntry) string {
	var sb strings.Builder
	for i, e := range entries {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		ts := e.Timestamp.UTC().Format("2006-01-02T15:04:05Z")
		fmt.Fprintf(&sb, "[%s | %s]\n%s", ts, e.Category, e.Content)
	}
	return sb.String()
}

// buildBudgetedMemoryText emits the N newest entries whose total rune count ≤ budget.
// Always emits at least min(10, len(entries)) entries regardless of budget.
func buildBudgetedMemoryText(entries []LongTermEntry, budget int) string {
	const minEntries = 10

	var included []LongTermEntry
	runeCount := 0

	for i, e := range entries {
		text := fmt.Sprintf("[%s | %s]\n%s",
			e.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
			e.Category,
			e.Content,
		)
		entryRunes := len([]rune(text))

		if i < minEntries {
			// Always include the first minEntries entries.
			included = append(included, e)
			runeCount += entryRunes
		} else if runeCount+entryRunes <= budget {
			included = append(included, e)
			runeCount += entryRunes
		} else {
			break
		}
	}

	return buildFullMemoryText(included)
}
