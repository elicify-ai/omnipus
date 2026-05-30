// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package session provides day-partitioned JSONL session storage per
// Appendix E §E.5 and Wave 1 user story US-5.
package session

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// EntryType classifies a transcript entry.
type EntryType string

const (
	// EntryTypeMessage is the default entry type for a user or assistant chat turn.
	EntryTypeMessage EntryType = "message"
	// EntryTypeCompaction marks a context-compaction summary entry.
	EntryTypeCompaction EntryType = "compaction"
	// EntryTypeSystem marks a system-level event entry.
	EntryTypeSystem EntryType = "system"
	// EntryTypeToolCall marks a tool invocation entry.
	EntryTypeToolCall EntryType = "tool_call"
	// EntryTypeTurnCancelled marks the JSONL entry written when a turn is canceled
	// mid-stream. Written once per fired cancel to transcript.jsonl (FR-15).
	EntryTypeTurnCancelled EntryType = "turn_canceled"
)

// SessionStatus classifies the lifecycle state of a session.
type SessionStatus string

const (
	// StatusActive is a session that is currently in use.
	StatusActive SessionStatus = "active"
	// StatusArchived is a session that has been intentionally closed.
	StatusArchived SessionStatus = "archived"
	// StatusInterrupted is a session that was terminated unexpectedly or canceled.
	StatusInterrupted SessionStatus = "interrupted"
)

// PartitionStore manages day-partitioned JSONL session transcripts per Appendix E §E.5.
type PartitionStore struct {
	mu      sync.Mutex
	agentID string
	baseDir string // ~/.omnipus/agents/<agentID>/sessions/
}

// NewPartitionStore returns a PartitionStore for the given agent workspace.
func NewPartitionStore(agentWorkspaceDir, agentID string) *PartitionStore {
	return &PartitionStore{
		agentID: agentID,
		baseDir: filepath.Join(agentWorkspaceDir, "sessions"),
	}
}

// SessionMeta is the meta.json file per Appendix E §E.5.1.
type SessionMeta struct {
	ID         string        `json:"id"`
	AgentID    string        `json:"agent_id"`
	Title      string        `json:"title,omitempty"`
	Status     SessionStatus `json:"status"` // StatusActive | StatusArchived | StatusInterrupted
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Model      string        `json:"model,omitempty"`
	Provider   string        `json:"provider,omitempty"`
	Stats      SessionStats  `json:"stats"`
	ProjectID  string        `json:"project_id,omitempty"`
	TaskID     string        `json:"task_id,omitempty"`
	Channel    string        `json:"channel"`
	Partitions []string      `json:"partitions"`

	LastCompactionSummary string `json:"last_compaction_summary,omitempty"`

	// v2 multi-agent fields (joined session model)
	AgentIDs            []string          `json:"agent_ids,omitempty"`
	ActiveAgentID       string            `json:"active_agent_id,omitempty"`
	CompactionSummaries map[string]string `json:"compaction_summaries,omitempty"` // per-agent compaction
}

// PostLoad backfills v2 multi-agent fields from the legacy AgentID field.
// Call after every JSON unmarshal of SessionMeta.
func (m *SessionMeta) PostLoad() {
	if len(m.AgentIDs) == 0 && m.AgentID != "" {
		m.AgentIDs = []string{m.AgentID}
	}
	if m.ActiveAgentID == "" && m.AgentID != "" {
		m.ActiveAgentID = m.AgentID
	}
}

// SessionStats aggregates usage across all partitions.
type SessionStats struct {
	TokensIn     int     `json:"tokens_in"`
	TokensOut    int     `json:"tokens_out"`
	TokensTotal  int     `json:"tokens_total"`
	Cost         float64 `json:"cost"`
	ToolCalls    int     `json:"tool_calls"`
	MessageCount int     `json:"message_count"`
}

// TranscriptEntry represents one line in a partition JSONL file.
type TranscriptEntry struct {
	ID          string       `json:"id"`
	Type        EntryType    `json:"type,omitempty"` // EntryTypeMessage | EntryTypeCompaction | EntryTypeSystem; empty = message
	Role        string       `json:"role,omitempty"` // "user" | "assistant" | "system"
	Content     string       `json:"content,omitempty"`
	Summary     string       `json:"summary,omitempty"` // for compaction entries
	Timestamp   time.Time    `json:"timestamp"`
	Tokens      int          `json:"tokens,omitempty"`
	Cost        float64      `json:"cost,omitempty"`
	Status      string       `json:"status,omitempty"` // "ok" | "error" | "interrupted"
	Attachments []Attachment `json:"attachments,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	AgentID     string       `json:"agent_id"` // which agent produced this entry (FR-002)

	// For compaction entries.
	MessagesCompacted int `json:"messages_compacted,omitempty"`

	// Truncated is set to true on the last assistant entry when a turn is
	// canceled mid-stream. Only serialized when true (FR-14). Only written to
	// transcript.jsonl; context.jsonl is never mutated (FR-14a).
	Truncated bool `json:"truncated,omitempty"`

	// Cancel-specific fields — only populated for EntryTypeTurnCancelled entries
	// (FR-15). All are omitempty so they are invisible on other entry types.
	TurnID               string   `json:"turn_id,omitempty"`
	CancelledByUser      string   `json:"canceled_by_user,omitempty"`
	CancelledByChannel   string   `json:"canceled_by_channel,omitempty"`
	CancelMethod         string   `json:"cancel_method,omitempty"` // "graceful" | "hard"
	DescendantsCancelled []string `json:"descendants_canceled,omitempty"`
}

// Attachment represents a file attached to a message.
type Attachment struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
}

// ToolCallID is the opaque identifier for a single tool invocation.
// Using a named type prevents accidental mixing with other string IDs
// (e.g., span IDs, session IDs) at Go call sites.
// JSON marshaling is identical to a plain string — the wire format is unchanged.
type ToolCallID string

// ToolCall represents one tool invocation within a message.
type ToolCall struct {
	ID         ToolCallID     `json:"id"`
	Tool       string         `json:"tool"`
	Status     string         `json:"status"` // "success" | "error" | "pending" | "denied"
	DurationMS int64          `json:"duration_ms,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	// ParentToolCallID is set on tool calls that execute inside a sub-turn.
	// It equals the parent spawn tool call's ID, which is the correlation anchor
	// for the subagent span (FR-H-001). Empty for top-level tool calls.
	// span_id = "span_" + ParentToolCallID (derivable, not stored separately).
	ParentToolCallID ToolCallID `json:"parent_tool_call_id,omitempty"`
}

// NewSessionID generates a ULID-based session ID prefixed with "session_".
// Returns an error instead of panicking if ULID generation fails.
func NewSessionID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), crand.Reader)
	if err != nil {
		return "", fmt.Errorf("session: generate ULID: %w", err)
	}
	return "session_" + id.String(), nil
}

// NewSession creates a new session directory, writes meta.json, and returns the metadata.
// Implements US-5 AC1.
func (ps *PartitionStore) NewSession(channel, model, provider string) (*SessionMeta, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now().UTC()
	sessionID, err := NewSessionID()
	if err != nil {
		return nil, err
	}
	meta := &SessionMeta{
		ID:        sessionID,
		AgentID:   ps.agentID,
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
		Provider:  provider,
		Channel:   channel,
	}

	sessionDir := filepath.Join(ps.baseDir, meta.ID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create dir %q: %w", sessionDir, err)
	}

	if err := writeMeta(sessionDir, meta); err != nil {
		return nil, err
	}

	slog.Debug("session: created", "id", meta.ID, "agent", ps.agentID, "channel", channel)
	return meta, nil
}

// AppendMessage appends a transcript entry to the correct day partition and
// updates meta.json stats. Creates a new partition file when the date changes.
// Implements US-5 AC2, AC3, AC4.
func (ps *PartitionStore) AppendMessage(sessionID string, entry TranscriptEntry) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}

	// Determine partition file for entry's timestamp (UTC day boundary).
	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
		entry.Timestamp = ts
	}
	partitionName := ts.UTC().Format("2006-01-02") + ".jsonl"
	partitionPath := filepath.Join(sessionDir, partitionName)

	// Register partition in meta if new.
	if !slices.Contains(meta.Partitions, partitionName) {
		meta.Partitions = append(meta.Partitions, partitionName)
	}

	// Append entry to JSONL partition (O_APPEND, atomic per POSIX).
	if err := fileutil.AppendJSONL(partitionPath, entry); err != nil {
		return fmt.Errorf("session: append to partition %q: %w", partitionPath, err)
	}

	// Update aggregated stats. Assistant messages contribute to TokensOut;
	// all other roles (user, system) contribute to TokensIn (FR-013).
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
	meta.UpdatedAt = ts

	return writeMeta(sessionDir, meta)
}

// UpdateStats atomically updates the session stats (e.g., after a streaming
// response completes with final token counts).
func (ps *PartitionStore) UpdateStats(sessionID string, delta SessionStats) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}

	meta.Stats.TokensIn += delta.TokensIn
	meta.Stats.TokensOut += delta.TokensOut
	meta.Stats.TokensTotal += delta.TokensIn + delta.TokensOut
	meta.Stats.Cost += delta.Cost
	meta.Stats.ToolCalls += delta.ToolCalls
	meta.Stats.MessageCount += delta.MessageCount
	meta.UpdatedAt = time.Now().UTC()

	return writeMeta(sessionDir, meta)
}

// SetStatus updates the session status.
func (ps *PartitionStore) SetStatus(sessionID string, status SessionStatus) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}
	meta.Status = status
	meta.UpdatedAt = time.Now().UTC()
	return writeMeta(sessionDir, meta)
}

// SetTitle updates the title of an existing session.
func (ps *PartitionStore) SetTitle(sessionID string, title string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}
	meta.Title = title
	meta.UpdatedAt = time.Now().UTC()
	return writeMeta(sessionDir, meta)
}

// SetAgentID updates the agent_id on an existing session.
func (ps *PartitionStore) SetAgentID(sessionID string, agentID string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}
	meta.AgentID = agentID
	meta.UpdatedAt = time.Now().UTC()
	return writeMeta(sessionDir, meta)
}

// GetMeta returns the metadata for sessionID.
func (ps *PartitionStore) GetMeta(sessionID string) (*SessionMeta, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return readMeta(filepath.Join(ps.baseDir, sessionID))
}

// readMeta reads meta.json from sessionDir.
func readMeta(sessionDir string) (*SessionMeta, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("session: read meta.json in %q: %w", sessionDir, err)
	}
	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("session: parse meta.json in %q: %w", sessionDir, err)
	}
	meta.PostLoad()
	return &meta, nil
}

// writeMeta writes meta atomically to sessionDir/meta.json.
func writeMeta(sessionDir string, meta *SessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal meta: %w", err)
	}
	path := filepath.Join(sessionDir, "meta.json")
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("session: write meta.json: %w", err)
	}
	return nil
}

// ListSessions returns all session metas in this store, sorted by UpdatedAt descending.
// Missing or unreadable meta files are skipped with a warning.
func (ps *PartitionStore) ListSessions() ([]*SessionMeta, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	entries, err := os.ReadDir(ps.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: list sessions: %w", err)
	}

	var metas []*SessionMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := readMeta(filepath.Join(ps.baseDir, entry.Name()))
		if err != nil {
			slog.Warn("session: skipping unreadable session", "dir", entry.Name(), "error", err)
			continue
		}
		metas = append(metas, meta)
	}

	slices.SortFunc(metas, func(a, b *SessionMeta) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})

	return metas, nil
}

// ClearAll removes every session directory from the store.
// Returns the number of sessions removed.
func (ps *PartitionStore) ClearAll() (int, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	entries, err := os.ReadDir(ps.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("session: clear all: read dir: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(ps.baseDir, entry.Name())
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("session: clear all: remove session dir", "dir", dir, "error", err)
			continue
		}
		removed++
	}
	return removed, nil
}

// ReadMessages returns all transcript entries for sessionID, merged across all
// day partitions in chronological order. Missing partitions are skipped with a warning.
func (ps *PartitionStore) ReadMessages(sessionID string) ([]TranscriptEntry, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return nil, err
	}

	var all []TranscriptEntry
	for _, partition := range meta.Partitions {
		entries, err := readPartition(filepath.Join(sessionDir, partition))
		if err != nil {
			slog.Warn(
				"session: skipping unreadable partition",
				"session_id",
				sessionID,
				"partition",
				partition,
				"error",
				err,
			)
			continue
		}
		all = append(all, entries...)
	}

	return all, nil
}

// readPartition reads all TranscriptEntry values from a JSONL file.
// Blank lines and malformed lines are silently skipped.
func readPartition(path string) ([]TranscriptEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session: read partition %q: %w", path, err)
	}

	var entries []TranscriptEntry
	skipped := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry TranscriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			slog.Warn("session: skipping malformed partition line", "path", path, "error", err)
			skipped++
			continue
		}
		entries = append(entries, entry)
	}
	if skipped > 0 {
		slog.Warn("session: partition had skipped malformed lines", "path", path, "skipped", skipped)
	}

	return entries, nil
}
