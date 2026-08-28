package session

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type Session struct {
	Key      string              `json:"key"`
	Messages []providers.Message `json:"messages"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}

type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	storage  string
	// projection holds the in-memory per-session projection state
	// (ADR-066 FR-019) for this legacy backend; it is not persisted.
	projection map[string]*memory.ProjectionMeta
}

func NewSessionManager(storage string) *SessionManager {
	sm := &SessionManager{
		sessions:   make(map[string]*Session),
		storage:    storage,
		projection: make(map[string]*memory.ProjectionMeta),
	}

	if storage != "" {
		if err := os.MkdirAll(storage, 0o700); err != nil {
			slog.Error(
				"session: could not create storage directory — persistence will fail",
				"path",
				storage,
				"error",
				err,
			)
		}
		if err := sm.loadSessions(); err != nil {
			slog.Error(
				"session: could not load sessions from disk — starting with empty state",
				"path",
				storage,
				"error",
				err,
			)
		}
	}

	return sm
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		return session
	}

	session = &Session{
		Key:      key,
		Messages: []providers.Message{},
		Created:  time.Now(),
		Updated:  time.Now(),
	}
	sm.sessions[key] = session

	return session
}

func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.AddFullMessage(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionKey]
	if !ok {
		session = &Session{
			Key:      sessionKey,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[sessionKey] = session
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return []providers.Message{}
	}

	history := make([]providers.Message, len(session.Messages))
	copy(history, session.Messages)
	return history
}

// ReadArchive implements SessionStore. SessionManager is a pure in-memory
// backend that never evicts turns to disk (no Skip-based windowing, no
// append-only JSONL archive). Consequently ReadArchive == GetHistory:
// it returns the complete current in-memory message slice wrapped as
// ArchivedMessage with TS=0 (no per-line timestamps exist in this
// backend). There is NO line-0 archive here — recall and breadcrumb
// callers that rely on evicted turns find nothing extra beyond what
// GetHistory already returns. Callers must treat TS==0 as
// "unknown/earlier" (FR-017 backward-compat rule).
func (sm *SessionManager) ReadArchive(_ context.Context, key string) ([]memory.ArchivedMessage, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return []memory.ArchivedMessage{}, nil
	}

	archived := make([]memory.ArchivedMessage, len(session.Messages))
	for i, m := range session.Messages {
		archived[i] = memory.ArchivedMessage{Message: m}
	}
	return archived, nil
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	if keepLast <= 0 {
		session.Messages = []providers.Message{}
		session.Updated = time.Now()
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	session.Messages = session.Messages[len(session.Messages)-keepLast:]
	session.Updated = time.Now()
}

// sanitizeFilename converts a session key into a cross-platform safe filename
// using hex encoding to prevent collisions. For example, "a:b" and "a_b" would
// both become "_" under the old character-replacement scheme but are distinct
// under hex encoding. The original key is preserved inside the JSON file, so
// loadSessions still maps back to the right in-memory key.
func sanitizeFilename(key string) string {
	return hex.EncodeToString([]byte(key))
}

func (sm *SessionManager) Save(key string) error {
	if sm.storage == "" {
		return nil
	}

	// Reject clearly invalid raw keys before encoding.
	if key == "" || key == "." || key == ".." {
		return os.ErrInvalid
	}

	filename := sanitizeFilename(key)

	// Sanity check: the hex-encoded filename must be non-empty and a local path.
	if filename == "" || !filepath.IsLocal(filename) {
		return os.ErrInvalid
	}

	// Snapshot under read lock, then perform slow file I/O after unlock.
	sm.mu.RLock()
	stored, ok := sm.sessions[key]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}

	snapshot := Session{
		Key:     stored.Key,
		Created: stored.Created,
		Updated: stored.Updated,
	}
	if len(stored.Messages) > 0 {
		snapshot.Messages = make([]providers.Message, len(stored.Messages))
		copy(snapshot.Messages, stored.Messages)
	} else {
		snapshot.Messages = []providers.Message{}
	}
	sm.mu.RUnlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal %q: %w", key, err)
	}

	sessionPath := filepath.Join(sm.storage, filename+".json")
	tmpFile, err := os.CreateTemp(sm.storage, "session-*.tmp")
	if err != nil {
		return fmt.Errorf("session: create temp file for %q: %w", key, err)
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("session: write temp file for %q: %w", key, err)
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("session: chmod temp file for %q: %w", key, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("session: sync temp file for %q: %w", key, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("session: close temp file for %q: %w", key, err)
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return fmt.Errorf("session: rename temp file for %q: %w", key, err)
	}
	cleanup = false
	return nil
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return fmt.Errorf("session: read storage dir %q: %w", sm.storage, err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		sessionPath := filepath.Join(sm.storage, file.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			slog.Warn("session: skipping unreadable session file", "path", sessionPath, "error", err)
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			slog.Warn("session: skipping corrupt session file", "path", sessionPath, "error", err)
			continue
		}

		sm.sessions[session.Key] = &session
	}

	return nil
}

// Close is a no-op for the in-memory SessionManager; it satisfies the
// SessionStore interface so callers can release resources uniformly.
func (sm *SessionManager) Close() error {
	return nil
}

// RollbackAppended implements SessionStore for the in-memory backend.
// SessionManager has no append-only JSONL archive and no Skip concept, so
// this truncates to the first targetArchiveLen messages and restores the
// in-memory projection state to emptiedSet (entries at index ≥
// targetArchiveLen dropped). targetSkip is accepted for interface
// compatibility but has no effect (no Skip windowing here).
// In practice this is never reached for agent-loop abort paths because those
// paths require an archive-backed store — but it must satisfy the interface.
func (sm *SessionManager) RollbackAppended(key string, targetArchiveLen, _ int, emptiedSet memory.ProjectionSet) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if targetArchiveLen < 0 {
		targetArchiveLen = 0
	}
	restored := make(memory.ProjectionSet, len(emptiedSet))
	for k, v := range emptiedSet {
		if k.ArchiveLine < targetArchiveLen {
			restored[k] = v
		}
	}
	pm := sm.projectionLocked(key)
	pm.Entries = restored

	session, ok := sm.sessions[key]
	if !ok {
		return
	}
	if targetArchiveLen >= len(session.Messages) {
		return
	}
	// Truncate to the first targetArchiveLen messages.
	msgs := make([]providers.Message, targetArchiveLen)
	copy(msgs, session.Messages[:targetArchiveLen])
	session.Messages = msgs
	session.Updated = time.Now()
}

// projectionLocked returns the (lazily created) projection record for key.
// Caller holds sm.mu.
func (sm *SessionManager) projectionLocked(key string) *memory.ProjectionMeta {
	pm, ok := sm.projection[key]
	if !ok {
		pm = &memory.ProjectionMeta{Entries: memory.ProjectionSet{}}
		sm.projection[key] = pm
	}
	return pm
}

// Projection implements SessionStore (in-memory, not persisted).
func (sm *SessionManager) Projection(key string) memory.ProjectionMeta {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	pm, ok := sm.projection[key]
	if !ok {
		return memory.ProjectionMeta{Entries: memory.ProjectionSet{}}
	}
	return memory.ProjectionMeta{Entries: pm.Entries.Clone(), Hydrated: pm.Hydrated}
}

// SetProjectionState implements SessionStore (in-memory, not persisted).
func (sm *SessionManager) SetProjectionState(key string, pk memory.ProjectionKey, state memory.ProjectionState) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.projectionLocked(key).Entries[pk] = state
}

// MarkHydrated implements SessionStore (in-memory, not persisted).
func (sm *SessionManager) MarkHydrated(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.projectionLocked(key).Hydrated = true
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		// Create a deep copy to strictly isolate internal state
		// from the caller's slice.
		msgs := make([]providers.Message, len(history))
		copy(msgs, history)
		session.Messages = msgs
		session.Updated = time.Now()
	}
}
