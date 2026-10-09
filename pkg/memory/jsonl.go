package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// ArchivedMessage is a single line in context.jsonl.
//
// It embeds providers.Message so that JSON serialization is flat:
//
//	{"role":"user","content":"hello","ts":1751234567}
//
// The ts field carries the Unix write timestamp stamped by addMsg (FR-017).
// Legacy lines written before this change unmarshal with TS==0 — callers
// treat TS==0 as "unknown/earlier" and must NOT error on it.
// This type is an internal persistence format — it is NOT a gateway/SPA wire
// type and must not be added to contracts/openapi.yaml.
type ArchivedMessage struct {
	providers.Message
	TS int64 `json:"ts,omitempty"`
}

const (
	// maxLineSize is the maximum size of a single JSON line in a .jsonl
	// file. Tool results (read_file, web search, etc.) can be large, so
	// we set a generous limit. The scanner starts at 64 KB and grows
	// only as needed up to this cap.
	maxLineSize = 10 * 1024 * 1024 // 10 MB

	// EncodedLineBound is the largest JSON-encoded ArchivedMessage line the
	// ADR-066 D4 choke point lets reach the archive: 0.8 × maxLineSize
	// (FR-012, US-3.AC6). A line over maxLineSize breaks the scanner and
	// with it every later line of the session; the 20 % margin absorbs the
	// per-line framing (timestamp, ids, escapes) the cap cannot see.
	EncodedLineBound = maxLineSize * 4 / 5
)

// sessionMeta holds per-session metadata stored in a .meta.json file.
//
// Projection (ADR-066 FR-019) is the per-result projection state keyed
// (tool_call_id, archive_line) → capped | emptied, persisted beside Skip so
// the live window and a reload project the same bytes. Hydrated (FR-048)
// marks an archive that was rebuilt from the UI transcript. Both are
// internal persistence state, not wire types.
type sessionMeta struct {
	Key        string            `json:"key"`
	Skip       int               `json:"skip"`
	Count      int               `json:"count"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Projection []projectionEntry `json:"projection,omitempty"`
	Hydrated   bool              `json:"hydrated,omitempty"`
	AnchorLine *int              `json:"anchor_archive_line,omitempty"`
	// Retracted lists physical archive-line spans that are RETAINED on disk
	// but EXCLUDED from the model view by an append-only rollback effect
	// (FR-006 / DEL-12). Never re-derived: the archive is append-only and its
	// line indices never shift, so a span address stays exact. Cleared only
	// when the archive is emptied (SetHistory) or physically compacted, both of
	// which renumber every line.
	Retracted []ArchiveSpan `json:"retracted,omitempty"`
}

// JSONLStore implements Store using append-only JSONL files.
//
// Each session is stored as two files:
//
//	{sanitized_key}.jsonl      — one JSON-encoded message per line, append-only
//	{sanitized_key}.meta.json  — session metadata (summary, logical truncation offset)
//
// Messages are never physically deleted from the JSONL file. Instead,
// TruncateHistory records a "skip" offset in the metadata file and
// GetHistory ignores lines before that offset. This keeps all writes
// append-only, which is both fast and crash-safe.
type JSONLStore struct {
	dir   string
	locks task.StripedLock
}

// NewJSONLStore creates a new JSONL-backed store rooted at dir.
func NewJSONLStore(dir string) (*JSONLStore, error) {
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("memory: create directory: %w", err)
	}
	return &JSONLStore{dir: dir}, nil
}

// sessionLock returns a mutex for the given session key.
// Keys are mapped to a fixed pool of 64 shards via FNV-32a hash, so
// memory usage is O(1) regardless of total session count.
// Delegates to task.StripedLock for the canonical sharded-mutex implementation.
func (s *JSONLStore) sessionLock(key string) *sync.Mutex {
	return s.locks.Get(key)
}

func (s *JSONLStore) jsonlPath(key string) string {
	return filepath.Join(s.dir, sanitizeKey(key)+".jsonl")
}

func (s *JSONLStore) metaPath(key string) string {
	return filepath.Join(s.dir, sanitizeKey(key)+".meta.json")
}

// sanitizeKey converts a session key to a safe filename component.
// Mirrors pkg/session.sanitizeFilename so that migration paths match.
// Replaces ':' with '_' (session key separator) and '/' and '\' with '_'
// so composite IDs (e.g. Telegram forum "chatID/threadID", Slack "channel/thread_ts")
// do not create subdirectories or break on Windows.
func sanitizeKey(key string) string {
	s := strings.ReplaceAll(key, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

// readMeta loads the metadata file for a session.
// Returns a zero-value sessionMeta if the file does not exist.
func (s *JSONLStore) readMeta(key string) (sessionMeta, error) {
	data, err := os.ReadFile(s.metaPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return sessionMeta{Key: key}, nil
	}
	if err != nil {
		return sessionMeta{}, fmt.Errorf("memory: read meta: %w", err)
	}
	var meta sessionMeta
	err = json.Unmarshal(data, &meta)
	if err != nil {
		return sessionMeta{}, fmt.Errorf("memory: decode meta: %w", err)
	}
	return meta, nil
}

// writeMeta atomically writes the metadata file using the project's
// standard WriteFileAtomic (temp + fsync + rename).
func (s *JSONLStore) writeMeta(key string, meta sessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: encode meta: %w", err)
	}
	return fileutil.WriteFileAtomic(s.metaPath(key), data, 0o644)
}

// readMessages reads valid JSON lines from a .jsonl file, skipping
// the first `skip` lines without unmarshaling them. This avoids the
// cost of json.Unmarshal on logically truncated messages.
// Malformed trailing lines (e.g. from a crash) are silently skipped.
//
// Each line is unmarshalled into ArchivedMessage (FR-017). Legacy lines
// that pre-date the TS stamp unmarshal with TS==0 — the embedded
// providers.Message fields still populate correctly because ArchivedMessage
// embeds providers.Message, so the JSON decoder fills Role/Content/etc.
// from the same flat keys regardless of whether "ts" is present.
func readMessages(path string, skip int) ([]ArchivedMessage, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []ArchivedMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: open jsonl: %w", err)
	}
	defer f.Close()

	var msgs []ArchivedMessage
	scanner := bufio.NewScanner(f)
	// Allow large lines for tool results (read_file, web search, etc.).
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	lineNum := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineNum++
		if lineNum <= skip {
			continue
		}
		var msg ArchivedMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Corrupt line — likely a partial write from a crash.
			// Log so operators know data was skipped, but don't
			// fail the entire read; this is the standard JSONL
			// recovery pattern.
			logger.WarnCF("memory", "skipping corrupt JSONL line", map[string]any{
				"line":  lineNum,
				"file":  filepath.Base(path),
				"error": err.Error(),
			})
			continue
		}
		msgs = append(msgs, msg)
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("memory: scan jsonl: %w", scanner.Err())
	}

	if msgs == nil {
		msgs = []ArchivedMessage{}
	}
	return msgs, nil
}

// countLines counts the total number of non-empty lines in a .jsonl file.
// Used by TruncateHistory to reconcile a stale meta.Count without
// the overhead of unmarshaling every message.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("memory: open jsonl: %w", err)
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			n++
		}
	}
	return n, scanner.Err()
}

func (s *JSONLStore) AddMessage(
	_ context.Context, sessionKey, role, content string,
) error {
	return s.addMsg(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

func (s *JSONLStore) AddFullMessage(
	_ context.Context, sessionKey string, msg providers.Message,
) error {
	return s.addMsg(sessionKey, msg)
}

// addMsg is the shared implementation for AddMessage and AddFullMessage.
// Each message is persisted as an ArchivedMessage (FR-017): the providers.Message
// fields are flattened into the JSON object alongside a "ts" field containing
// the Unix write timestamp in seconds. This is backward-compatible: readers
// that encounter a legacy line without "ts" will unmarshal TS==0.
func (s *JSONLStore) addMsg(sessionKey string, msg providers.Message) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	return s.addMsgLocked(sessionKey, msg)
}

// addMsgLocked appends and updates metadata while the caller holds the existing
// session shard. Checked context admission uses the same append path and lock.
func (s *JSONLStore) addMsgLocked(sessionKey string, msg providers.Message) error {
	// Wrap the message with a write timestamp before marshaling (FR-017).
	archived := ArchivedMessage{
		Message: msg,
		TS:      time.Now().Unix(),
	}

	// Append the message as a single JSON line.
	line, err := json.Marshal(archived)
	if err != nil {
		return fmt.Errorf("memory: marshal message: %w", err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(
		s.jsonlPath(sessionKey),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		return fmt.Errorf("memory: open jsonl for append: %w", err)
	}
	_, writeErr := f.Write(line)
	if writeErr != nil {
		f.Close()
		return fmt.Errorf("memory: append message: %w", writeErr)
	}
	// Flush to physical storage before closing. This matches the
	// durability guarantee of writeMeta and rewriteJSONL (which use
	// WriteFileAtomic with fsync). Without Sync, a power loss could
	// leave the append in the kernel page cache only — lost on reboot.
	if syncErr := f.Sync(); syncErr != nil {
		f.Close()
		return fmt.Errorf("memory: sync jsonl: %w", syncErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("memory: close jsonl: %w", closeErr)
	}

	// Update metadata.
	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	now := time.Now()
	if meta.Count == 0 && meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.Count++
	meta.UpdatedAt = now

	return s.writeMeta(sessionKey, meta)
}

func (s *JSONLStore) GetHistory(
	_ context.Context, sessionKey string,
) ([]providers.Message, error) {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return nil, err
	}

	// Decode from line zero so the anchor and suffix retain decoded archive
	// identities, including after a recovered malformed line.
	archived, err := readMessages(s.jsonlPath(sessionKey), 0)
	if err != nil {
		return nil, err
	}

	msgs, _ := WindowHistory(WindowSnapshot{State: windowState(meta), Archive: archived, Retracted: meta.Retracted})
	return msgs, nil
}

// ReadArchive returns the FULL archived log for sessionKey from line 0,
// ignoring meta.Skip entirely (FR-016). Each ArchivedMessage carries the
// write timestamp (TS) stamped by addMsg. Legacy lines pre-dating FR-017
// unmarshal with TS==0; callers must treat TS==0 as "unknown/earlier" and
// must not error on it.
//
// ReadArchive is the only correct path for recall_conversation and the
// breadcrumb builder — using GetHistory would miss evicted (skipped) turns.
func (s *JSONLStore) ReadArchive(
	_ context.Context, sessionKey string,
) ([]ArchivedMessage, error) {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	// skip=0 reads every line regardless of meta.Skip (FR-016).
	return readMessages(s.jsonlPath(sessionKey), 0)
}

// ScanArchive streams the FULL archived log for sessionKey line by line,
// ignoring meta.Skip, calling fn with the zero-based archive index and the
// decoded message. The index is identical to the position the same message
// takes in a ReadArchive result: empty lines are skipped and corrupt lines
// are skipped (logged) without consuming an index, exactly as readMessages
// does, so marks and projection keys address the same line either way.
//
// fn returning false stops the scan immediately — no later line is decoded
// (ADR-066 FR-024 / B-31b: recall by tool_call_id must not load the whole
// archive to serve one line). A missing session file scans zero lines and
// returns nil.
func (s *JSONLStore) ScanArchive(
	_ context.Context, sessionKey string, fn func(idx int, msg ArchivedMessage) bool,
) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	path := s.jsonlPath(sessionKey)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("memory: open jsonl: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow large lines for tool results (read_file, web search, etc.).
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	idx := 0
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineNum++
		var msg ArchivedMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Same recovery pattern as readMessages: log and continue
			// WITHOUT consuming an index, so ScanArchive and ReadArchive
			// agree on every message's archive index.
			logger.WarnCF("memory", "skipping corrupt JSONL line", map[string]any{
				"line":  lineNum,
				"file":  filepath.Base(path),
				"error": err.Error(),
			})
			continue
		}
		if !fn(idx, msg) {
			return nil
		}
		idx++
	}
	if scanner.Err() != nil {
		return fmt.Errorf("memory: scan jsonl: %w", scanner.Err())
	}
	return nil
}

func (s *JSONLStore) TruncateHistory(
	ctx context.Context, sessionKey string, keepLast int,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}

	// Cursor and projection addresses count decoded archive records, exactly
	// like ReadArchive. Malformed physical lines do not shift these identities.
	archive, err := readMessages(s.jsonlPath(sessionKey), 0)
	if err != nil {
		return err
	}
	meta.Count = len(archive)
	if meta.Skip < 0 || meta.Skip > meta.Count {
		return errors.New("memory: invalid context window cursor")
	}

	if keepLast <= 0 {
		meta.Skip = meta.Count
	} else {
		effective := meta.Count - meta.Skip
		if keepLast < effective {
			meta.Skip = meta.Count - keepLast
		}
	}
	// FR-019 / US-6.AC9: entries for evicted lines have no window view left
	// to project — prune everything below the new Skip.
	kept := make([]projectionEntry, 0, len(meta.Projection))
	for _, entry := range meta.Projection {
		if entry.ArchiveLine >= meta.Skip {
			kept = append(kept, entry)
		}
	}
	meta.Projection = kept // Preserve exact source limits and transcript mappings.
	meta.UpdatedAt = time.Now()
	if err := validateWindowMetadata(windowState(meta), archive); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.writeMeta(sessionKey, meta)
}

// SetHistory fills an EMPTY session archive with history (ADR-066 D5.5,
// FR-047). It refuses with ErrArchiveNotEmpty when the archive already
// holds at least one line, and it never touches meta.Skip: the only
// legitimate caller is transcript hydration of a brand-new archive, and a
// whole-file rewrite of an existing one was the verified mechanism that
// reset Skip and destroyed every tool result on reopen (US-15). Rolling a
// turn back is RollbackWindow's job, never this method's.
func (s *JSONLStore) SetHistory(
	_ context.Context,
	sessionKey string,
	history []providers.Message,
) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	// Count the file, not meta.Count — a crash between the append and the
	// meta write leaves meta stale, and "non-empty" must be judged on bytes.
	n, err := countLines(s.jsonlPath(sessionKey))
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %q has %d line(s)", ErrArchiveNotEmpty, sessionKey, n)
	}

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	now := time.Now()
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	// Skip is deliberately left as-is (FR-047). Any projection entry on an
	// empty archive addresses a line that does not exist — clear it. An
	// excluded (retracted) span likewise cannot survive an archive rewrite.
	meta.Count = len(history)
	meta.Projection = nil
	meta.Retracted = nil
	meta.UpdatedAt = now

	// Write meta BEFORE writing the JSONL file. If we crash between the two
	// writes, meta.Count overstates an empty file — GetHistory simply reads
	// nothing, and the next append reconciles Count.
	err = s.writeMeta(sessionKey, meta)
	if err != nil {
		return err
	}

	// SetHistory receives plain providers.Message slices; wrap each with TS=0
	// (no write timestamp — these are externally-supplied replacements, not
	// new appends from addMsg). Callers of GetHistory see only providers.Message
	// after the TS is stripped, so round-trip fidelity is preserved.
	archived := make([]ArchivedMessage, len(history))
	for i, m := range history {
		archived[i] = ArchivedMessage{Message: m}
	}
	return s.rewriteJSONL(sessionKey, archived)
}

// GetProjection returns the session's persisted projection state and
// hydrated flag (FR-019, FR-048). A session with no meta file yields an
// empty, non-nil set.
func (s *JSONLStore) GetProjection(_ context.Context, sessionKey string) (ProjectionMeta, error) {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return ProjectionMeta{}, err
	}
	return projectionMeta(meta), nil
}

// SetProjectionState records state for one (tool_call_id, archive_line)
// (FR-019). Re-marking an existing key overwrites it. Invalid keys or
// states are refused, never stored.
func (s *JSONLStore) SetProjectionState(
	_ context.Context, sessionKey string, pk ProjectionKey, state ProjectionState,
) error {
	if err := validateProjectionWrite(pk, state); err != nil {
		return err
	}
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	pm := projectionMeta(meta)
	pm.Entries[pk] = state
	if state == ProjectionEmptied {
		pm.SourceRunes[pk] = 0
	}
	meta.Projection = entriesWithLimits(pm)
	meta.UpdatedAt = time.Now()
	return s.writeMeta(sessionKey, meta)
}

// MarkHydrated sets the one-way hydrated flag (FR-048): the archive was
// rebuilt from the UI transcript, so recall by tool_call_id cannot promise
// the original result bytes.
func (s *JSONLStore) MarkHydrated(_ context.Context, sessionKey string) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	if meta.Hydrated {
		return nil
	}
	meta.Hydrated = true
	meta.UpdatedAt = time.Now()
	return s.writeMeta(sessionKey, meta)
}

// Compact physically rewrites the JSONL file, dropping all logically
// skipped lines. This reclaims disk space that accumulates after
// repeated TruncateHistory calls.
//
// context-paging: MUST NOT be called from any Save path — it destroys the
// recall archive (FR-005). This function is retained for direct unit tests
// only. The retention sweep is the sole legitimate deleter of context.jsonl.
//
// It is safe to call at any time; if there is nothing to compact
// (skip == 0) the method returns immediately.
func (s *JSONLStore) Compact(
	_ context.Context, sessionKey string,
) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	if meta.Skip == 0 {
		return nil
	}

	// Read only the active messages (post-Skip), skipping truncated lines
	// without unmarshaling them.
	active, err := readMessages(s.jsonlPath(sessionKey), meta.Skip)
	if err != nil {
		return err
	}

	// Write meta BEFORE rewriting the JSONL file. If the process
	// crashes between the two writes, meta has Skip=0 and the old
	// (uncompacted) file is still intact, so GetHistory reads from
	// line 1 — returning previously-truncated messages rather than
	// losing data. The next Compact or TruncateHistory corrects this.
	meta.Skip = 0
	meta.Count = len(active)
	// A physical rewrite renumbers every line, so retained-but-excluded spans
	// no longer address anything — clear them with the bytes they referred to.
	meta.Retracted = nil
	meta.UpdatedAt = time.Now()

	err = s.writeMeta(sessionKey, meta)
	if err != nil {
		return err
	}

	return s.rewriteJSONL(sessionKey, active)
}

// rewriteJSONL atomically replaces the JSONL file with the given ArchivedMessages
// using the project's standard WriteFileAtomic (temp + fsync + rename).
// Each ArchivedMessage serializes as a flat JSON object containing all
// providers.Message fields plus the "ts" timestamp (omitted when zero).
func (s *JSONLStore) rewriteJSONL(
	sessionKey string, msgs []ArchivedMessage,
) error {
	var buf bytes.Buffer
	for i, msg := range msgs {
		line, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("memory: marshal message %d: %w", i, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return fileutil.WriteFileAtomic(s.jsonlPath(sessionKey), buf.Bytes(), 0o644)
}

func (s *JSONLStore) Close() error {
	return nil
}
