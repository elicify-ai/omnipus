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
)

// IsValidSessionType reports whether t is one of the known session types.
// New types must be added here and to the const block above so every
// validation/listing site accepts them.
func IsValidSessionType(t UnifiedSessionType) bool {
	switch t {
	case SessionTypeChat, SessionTypeTask, SessionTypeChannel, SessionTypeScheduled, SessionTypeHeartbeat:
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
	mu       sync.Mutex
	baseDir  string // {workspace}/sessions/
	homePath string // ~/.omnipus/ — uploads cascade-delete root (home-rooted per rest.go:4352)
	backend  *memory.JSONLStore
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
		baseDir:  baseDir,
		homePath: homePath,
		backend:  store,
	}

	us.migrateLegacy()
	return us, nil
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

	us.mu.Lock()
	defer us.mu.Unlock()
	return us.createSessionLocked(sessionID, sessionType, channel, creatingAgentID)
}

// NewChannelSession creates a new shared session for (channel, peerID).
// Unlike NewSession it writes PeerID and Title atomically so the caller does
// not need a follow-up SetMeta call.
func (us *UnifiedStore) NewChannelSession(channel, peerID, agentID, title string) (*UnifiedMeta, error) {
	meta, err := us.NewSession(SessionTypeChannel, channel, agentID)
	if err != nil {
		return nil, err
	}
	meta.PeerID = peerID
	meta.Title = title
	us.mu.Lock()
	err = us.writeMetaLocked(meta.ID, meta)
	us.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return meta, nil
}

// createSessionLocked creates a session directory with the EXACT supplied id,
// meta.json, and an empty transcript. Caller must hold us.mu.
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
	us.mu.Lock()
	defer us.mu.Unlock()

	if meta, err := us.readMetaLocked(id); err == nil {
		return meta, nil
	}
	return us.createSessionLocked(id, SessionTypeScheduled, "scheduled", ownerAgentID)
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
	if patch.Owner != nil {
		meta.Owner = *patch.Owner
	}
	if patch.WorkspaceID != nil {
		meta.WorkspaceID = *patch.WorkspaceID
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
		meta.Stats.TokensCacheRead += entry.CacheReadTokens
		meta.Stats.TokensCacheWrite += entry.CacheWriteTokens
		if entry.Model != "" {
			if meta.Stats.ByModel == nil {
				meta.Stats.ByModel = make(map[string]ModelTokens)
			}
			mt := meta.Stats.ByModel[entry.Model]
			mt.CacheRead += entry.CacheReadTokens
			mt.CacheWrite += entry.CacheWriteTokens
			mt.Total += entry.Tokens
			meta.Stats.ByModel[entry.Model] = mt
		}
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
func (us *UnifiedStore) UpdateToolCallStatus(sessionID string, toolCallID ToolCallID, status string, durationMS int64) (found bool, err error) {
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	if toolCallID == "" {
		return false, nil
	}

	us.mu.Lock()
	defer us.mu.Unlock()

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

// RollbackAppended implements SessionStore — truncates the on-disk archive to
// targetArchiveLen physical lines and restores meta.Skip = min(targetSkip,
// targetArchiveLen). This is the fix for the mid-turn eviction bug: if
// windowTrim advanced Skip during a live turn and the turn then aborts,
// restoring Skip to its turn-start value ensures GetHistory returns exactly
// the pre-turn live window (SC-001, SC-010).
// Callers compute: targetSkip = initialArchiveLen - initialHistoryLength.
func (us *UnifiedStore) RollbackAppended(sessionKey string, targetArchiveLen, targetSkip int) {
	if err := us.backend.RollbackAppended(context.Background(), sessionKey, targetArchiveLen, targetSkip); err != nil {
		slog.Error("unified_store: rollback appended", "key", sessionKey, "error", err)
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
func (us *UnifiedStore) Close() error {
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
	return removed, errors.Join(errs...)
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
