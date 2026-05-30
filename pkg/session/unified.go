package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// UnifiedSessionType classifies what created a session.
type UnifiedSessionType string

const (
	SessionTypeChat    UnifiedSessionType = "chat"
	SessionTypeTask    UnifiedSessionType = "task"
	SessionTypeChannel UnifiedSessionType = "channel"
)

// MetaPatch is a partial update applied to a session's meta.json.
// Only non-nil fields are written.
type MetaPatch struct {
	Title  *string
	Status *SessionStatus
	TaskID *string
}

// UnifiedMeta extends SessionMeta with the session type field.
// It is JSON-compatible with SessionMeta (same file, additional fields).
type UnifiedMeta struct {
	SessionMeta
	Type UnifiedSessionType `json:"type"`
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
type UnifiedStore struct {
	mu      sync.Mutex
	baseDir string // {workspace}/sessions/
	backend *memory.JSONLStore
}

// BaseDir returns the root directory of this store.
// Exported for tests that need to create fixture files directly in the store.
func (us *UnifiedStore) BaseDir() string {
	return us.baseDir
}

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
func NewUnifiedStore(baseDir string) (*UnifiedStore, error) {
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
		baseDir: baseDir,
		backend: store,
	}

	us.migrateLegacy()
	return us, nil
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
	us.mu.Lock()
	defer us.mu.Unlock()

	sessionID, err := NewSessionID()
	if err != nil {
		return nil, err
	}

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
	if err := us.writeMetaLocked(sessionID, meta); err != nil {
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

// GetMeta returns the metadata for a session.
func (us *UnifiedStore) GetMeta(sessionID string) (*UnifiedMeta, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	return us.readMetaLocked(sessionID)
}

// SetMeta applies a partial update to a session's meta.json.
func (us *UnifiedStore) SetMeta(sessionID string, patch MetaPatch) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	us.mu.Lock()
	defer us.mu.Unlock()

	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return err
	}
	if patch.Title != nil {
		meta.Title = *patch.Title
	}
	if patch.Status != nil {
		meta.Status = *patch.Status
	}
	if patch.TaskID != nil {
		meta.TaskID = *patch.TaskID
	}
	meta.UpdatedAt = time.Now().UTC()
	return us.writeMetaLocked(sessionID, meta)
}

// SwitchAgent atomically updates the ActiveAgentID on a session.
// The caller must NOT hold us.mu. Returns ErrAlreadyActive if the session
// is already on newAgentID (idempotent — callers should treat this as success).
// newAgentID is appended to AgentIDs if not already present.
func (us *UnifiedStore) SwitchAgent(sessionID, newAgentID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	us.mu.Lock()
	defer us.mu.Unlock()

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
	return us.writeMetaLocked(sessionID, meta)
}

// readMetaLocked reads meta.json for sessionID without acquiring the mutex.
// Caller must hold us.mu.
func (us *UnifiedStore) readMetaLocked(sessionID string) (*UnifiedMeta, error) {
	return readUnifiedMeta(filepath.Join(us.baseDir, sessionID))
}

// writeMetaLocked atomically writes meta.json for sessionID, acquiring an OS
// flock for cross-process defense-in-depth. Caller must hold us.mu.
func (us *UnifiedStore) writeMetaLocked(sessionID string, meta *UnifiedMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal meta: %w", err)
	}
	metaPath := filepath.Join(us.baseDir, sessionID, "meta.json")
	return fileutil.WithFlock(metaPath, func() error {
		return fileutil.WriteFileAtomic(metaPath, data, 0o600)
	})
}

// AppendTranscript appends an entry to {session-id}/transcript.jsonl.
func (us *UnifiedStore) AppendTranscript(sessionID string, entry TranscriptEntry) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	us.mu.Lock()
	defer us.mu.Unlock()

	transcriptPath := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	if err := fileutil.AppendJSONL(transcriptPath, entry); err != nil {
		return fmt.Errorf("unified_store: append transcript: %w", err)
	}

	// Update stats and UpdatedAt in meta (best-effort).
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		slog.Warn("unified_store: could not update meta stats", "session_id", sessionID, "error", err)
		return nil
	}
	if entry.Role == "assistant" {
		meta.Stats.TokensOut += entry.Tokens
	} else {
		meta.Stats.TokensIn += entry.Tokens
	}
	meta.Stats.TokensTotal += entry.Tokens
	meta.Stats.Cost += entry.Cost
	meta.Stats.ToolCalls += len(entry.ToolCalls)
	if entry.Type == "" || entry.Type == EntryTypeMessage {
		meta.Stats.MessageCount++
	}
	meta.UpdatedAt = entry.Timestamp
	if writeErr := us.writeMetaLocked(sessionID, meta); writeErr != nil {
		slog.Warn(
			"unified_store: could not write meta after transcript append",
			"session_id",
			sessionID,
			"error",
			writeErr,
		)
	}
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
// Acquires the same in-process mutex as AppendTranscript. Does NOT touch
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

	us.mu.Lock()
	defer us.mu.Unlock()

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

	// Rebuild the file contents (one JSON object per line, no trailing newline on last).
	var buf bytes.Buffer
	for i, line := range entries {
		buf.Write(line)
		if i < len(entries)-1 {
			buf.WriteByte('\n')
		}
	}

	if writeErr := fileutil.WriteFileAtomic(transcriptPath, buf.Bytes(), 0o600); writeErr != nil {
		return fmt.Errorf("unified_store: mark truncated: write transcript: %w", writeErr)
	}
	return nil
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
func (us *UnifiedStore) ListSessions() ([]*UnifiedMeta, error) {
	us.mu.Lock()
	defer us.mu.Unlock()

	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("unified_store: list sessions: %w", err)
	}

	var metas []*UnifiedMeta
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".context" {
			continue
		}
		meta, err := readUnifiedMeta(filepath.Join(us.baseDir, entry.Name()))
		if err != nil {
			slog.Warn("unified_store: skipping unreadable session", "dir", entry.Name(), "error", err)
			continue
		}
		metas = append(metas, meta)
	}

	slices.SortFunc(metas, func(a, b *UnifiedMeta) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return metas, nil
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

// GetSummary implements SessionStore.
func (us *UnifiedStore) GetSummary(sessionKey string) string {
	summary, err := us.backend.GetSummary(context.Background(), sessionKey)
	if err != nil {
		slog.Error("unified_store: get summary", "key", sessionKey, "error", err)
		return ""
	}
	return summary
}

// SetSummary implements SessionStore.
func (us *UnifiedStore) SetSummary(sessionKey, summary string) {
	if err := us.backend.SetSummary(context.Background(), sessionKey, summary); err != nil {
		slog.Error("unified_store: set summary", "key", sessionKey, "error", err)
	}
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

// Save implements SessionStore — compacts the context backend.
func (us *UnifiedStore) Save(sessionKey string) error {
	return us.backend.Compact(context.Background(), sessionKey)
}

// Close implements SessionStore.
func (us *UnifiedStore) Close() error {
	return us.backend.Close()
}

// DeleteSession removes a single session directory from the store.
// Returns an error if the session does not exist or cannot be removed.
func (us *UnifiedStore) DeleteSession(sessionID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	us.mu.Lock()
	defer us.mu.Unlock()

	dir := filepath.Join(us.baseDir, sessionID)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("unified_store: session %q not found", sessionID)
		}
		return fmt.Errorf("unified_store: stat session %q: %w", sessionID, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("unified_store: delete session %q: %w", sessionID, err)
	}
	contextFile := filepath.Join(us.baseDir, ".context", sessionID+".jsonl")
	os.Remove(contextFile) // best-effort, ignore error if file does not exist
	return nil
}

// ClearAll removes every session directory from the store.
// Returns the number of sessions removed.
func (us *UnifiedStore) ClearAll() (int, error) {
	us.mu.Lock()
	defer us.mu.Unlock()

	entries, err := os.ReadDir(us.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("unified_store: clear all: read dir: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".context" {
			continue
		}
		dir := filepath.Join(us.baseDir, entry.Name())
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("unified_store: clear all: remove session dir", "dir", dir, "error", err)
			continue
		}
		contextFile := filepath.Join(us.baseDir, ".context", entry.Name()+".jsonl")
		os.Remove(contextFile) // best-effort, ignore error if file does not exist
		removed++
	}
	return removed, nil
}

// readUnifiedMeta reads meta.json from sessionDir, handling both legacy SessionMeta
// (without Type) and UnifiedMeta (with Type).
func readUnifiedMeta(sessionDir string) (*UnifiedMeta, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("unified_store: read meta.json in %q: %w", sessionDir, err)
	}
	var meta UnifiedMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unified_store: parse meta.json in %q: %w", sessionDir, err)
	}
	// If Type is not set (legacy PartitionStore session), default to chat.
	if meta.Type == "" {
		meta.Type = SessionTypeChat
	}
	meta.PostLoad()
	return &meta, nil
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
