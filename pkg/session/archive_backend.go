// archive_backend.go: the archive-backed implementation of the session store
// interfaces — SessionStore (reader + writer) and ContextWindowStore — over the
// addressed day archive (ArchiveDayStore) and its content-free window
// (AddressedWindow). Session-core C-ARCHIVE / U2 Decision D (spec FR-004/005/006,
// DEL-12).
//
// WHY this file exists: Decision D replaces the separate `.context` model JSONL
// backend with the ONE addressed archive. This backend is the drop-in the
// UnifiedStore constructor will hold in place of *memory.JSONLStore (slice 2),
// so `pkg/agent`'s turn loop keeps its existing store interface while the bytes
// on disk are the addressed archive. It deliberately does NOT wrap a
// JSONLStore: it re-homes the window/projection/rollback algorithms onto the
// archive (spec Decision D, "not a compatibility backend").
//
// ORDINAL MODEL (documented, transitional): the interfaces expose a zero-based
// `archive_line` ordinal (ProjectionKey.ArchiveLine, WindowState.Skip/Count).
// An ordinal here is a MODEL-ARCHIVE line: one PAYLOAD record — an ArchiveRecord
// carrying a ModelMessage — in append order. A body-free model_ref placement is
// NOT a new line (it resolves to the payload it references), which is what keeps
// "one placement per source, no duplicate" true. Consecutive unreadable/partial
// lines are skipped without consuming an ordinal, matching the existing reader.
//
// What is NOT done in this slice (deliberately, tracked for later slices):
//
//   - Bounded reads: SnapshotWindow/ReadArchive enumerate the whole archive
//     (an ordinal needs a full walk). This matches the CURRENT JSONL behaviour;
//     the FR-005 offset-bounded path is the caller-migration slice.
//   - Provenance: a payload's EntrySource is minted from the role only; the
//     admission-time trusted source (FR-008) is wired when the loop's write
//     callers adopt this backend.
//   - Raw recall representation: the spec's raw-range amendment (literal
//     model_message JSON, private fields excluded) is implemented here, but the
//     recall consumers still move in a later slice.
package session

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/oklog/ulid/v2"
)

// archiveBackendMetaFile is the content-free backend state persisted beside the
// archive partitions: the ordinal cursor, anchor, retained-but-excluded spans,
// projection state and hydrated flag. It carries marks and ordinals only — never
// provider content.
const archiveBackendMetaFile = "backend_meta.json"

// archiveBackend is the archive-backed session store. It is safe for concurrent
// use; one backend-level mutex serializes its read-modify-write operations, and
// each ArchiveDayStore serializes its own file writes.
type archiveBackend struct {
	mu      sync.Mutex
	baseDir string
	stores  map[string]*ArchiveDayStore
}

func newArchiveBackend(baseDir string) *archiveBackend {
	return &archiveBackend{baseDir: baseDir, stores: make(map[string]*ArchiveDayStore)}
}

// Compile-time guarantees that the backend is a drop-in for the store
// interfaces the UnifiedStore constructor will hold in place of the JSONL
// backend (slice 2).
var (
	_ SessionStore       = (*archiveBackend)(nil)
	_ ContextWindowStore = (*archiveBackend)(nil)
)

func (b *archiveBackend) store(key string) (*ArchiveDayStore, error) {
	if s, ok := b.stores[key]; ok {
		return s, nil
	}
	s, err := NewArchiveDayStore(b.baseDir, key)
	if err != nil {
		return nil, err
	}
	b.stores[key] = s
	return s, nil
}

// archiveBackendMeta is the persisted, content-free backend window state.
type archiveBackendMeta struct {
	Skip       int                    `json:"skip"`
	Count      int                    `json:"count"`
	AnchorLine *int                   `json:"anchor_archive_line,omitempty"`
	Retracted  []memory.ArchiveSpan   `json:"retracted,omitempty"`
	Hydrated   bool                   `json:"hydrated,omitempty"`
	Projection []archiveProjectionRow `json:"projection,omitempty"`
}

// archiveProjectionRow mirrors memory's unexported projectionEntry so the state
// round-trips through this backend's own meta file.
type archiveProjectionRow struct {
	ToolCallID     string                 `json:"tool_call_id"`
	ArchiveLine    int                    `json:"archive_line"`
	State          memory.ProjectionState `json:"state,omitempty"`
	SourceRunes    *int                   `json:"retained_source_runes,omitempty"`
	TranscriptLine *int                   `json:"transcript_line,omitempty"`
}

// payloadLine is one model-archive line: its exact address, its decoded envelope
// and the literal JSON bytes of its private model_message (for the raw recall
// path — never the whole envelope, which would leak source/marks).
type payloadLine struct {
	addr     ArchiveAddress
	rec      ArchiveRecord
	rawModel []byte
}

// --- SessionReader ---

// GetHistory returns the live (post-Skip) window messages.
func (b *archiveBackend) GetHistory(key string) []providers.Message {
	snap, err := b.SnapshotWindow(context.Background(), key)
	if err != nil {
		slog.Error("archive_backend: get history", "key", key, "error", err)
		return []providers.Message{}
	}
	msgs, _ := memory.WindowHistory(snap)
	return msgs
}

// ReadArchive returns the full model archive from line 0, ignoring Skip.
func (b *archiveBackend) ReadArchive(_ context.Context, key string) ([]memory.ArchivedMessage, error) {
	lines, err := b.payloadLines(key)
	if err != nil {
		return nil, err
	}
	return archivedMessages(lines)
}

// Projection returns the persisted projection state.
func (b *archiveBackend) Projection(key string) memory.ProjectionMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		slog.Error("archive_backend: get projection", "key", key, "error", err)
		return memory.ProjectionMeta{Entries: memory.ProjectionSet{}}
	}
	return projectionFromMeta(meta)
}

// --- SessionWriter ---

func (b *archiveBackend) AddMessage(sessionKey, role, content string) {
	if err := b.appendMessage(sessionKey, providers.Message{Role: role, Content: content}); err != nil {
		slog.Error("archive_backend: add message", "key", sessionKey, "error", err)
	}
}

func (b *archiveBackend) AddFullMessage(sessionKey string, msg providers.Message) {
	if err := b.appendMessage(sessionKey, msg); err != nil {
		slog.Error("archive_backend: add full message", "key", sessionKey, "error", err)
	}
}

// SetHistory fills an EMPTY model archive; it refuses a non-empty one (FR-047).
func (b *archiveBackend) SetHistory(key string, history []providers.Message) {
	b.mu.Lock()
	lines, err := b.payloadLinesLocked(key)
	if err != nil {
		b.mu.Unlock()
		slog.Error("archive_backend: set history: count", "key", key, "error", err)
		return
	}
	if len(lines) > 0 {
		b.mu.Unlock()
		slog.Error("archive_backend: set history refused",
			"key", key, "error", fmt.Errorf("%w: %q has %d line(s)", memory.ErrArchiveNotEmpty, key, len(lines)))
		return
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		b.mu.Unlock()
		slog.Error("archive_backend: set history: read meta", "key", key, "error", err)
		return
	}
	// Skip is left as-is (FR-047); projection and retracted cannot survive a
	// first fill.
	meta.Count = len(history)
	meta.Projection = nil
	meta.Retracted = nil
	if err := b.saveMetaLocked(key, meta); err != nil {
		b.mu.Unlock()
		slog.Error("archive_backend: set history: write meta", "key", key, "error", err)
		return
	}
	for _, m := range history {
		if err := b.appendOneLocked(key, m); err != nil {
			b.mu.Unlock()
			slog.Error("archive_backend: set history: append", "key", key, "error", err)
			return
		}
	}
	b.mu.Unlock()
}

// TruncateHistory keeps only the last keepLast messages (mirrors the JSONL rule).
func (b *archiveBackend) TruncateHistory(key string, keepLast int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines, err := b.payloadLinesLocked(key)
	if err != nil {
		slog.Error("archive_backend: truncate: count", "key", key, "error", err)
		return
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		slog.Error("archive_backend: truncate: read meta", "key", key, "error", err)
		return
	}
	meta.Count = len(lines)
	if meta.Skip < 0 || meta.Skip > meta.Count {
		slog.Error("archive_backend: truncate: invalid cursor", "key", key, "skip", meta.Skip)
		return
	}
	if keepLast <= 0 {
		meta.Skip = meta.Count
	} else {
		effective := meta.Count - meta.Skip
		if keepLast < effective {
			meta.Skip = meta.Count - keepLast
		}
	}
	// Prune projection rows for evicted lines (FR-019 / US-6.AC9).
	kept := meta.Projection[:0]
	for _, row := range meta.Projection {
		if row.ArchiveLine >= meta.Skip {
			kept = append(kept, row)
		}
	}
	meta.Projection = kept
	if err := b.saveMetaLocked(key, meta); err != nil {
		slog.Error("archive_backend: truncate: write meta", "key", key, "error", err)
	}
}

func (b *archiveBackend) SetProjectionState(key string, pk memory.ProjectionKey, state memory.ProjectionState) {
	if err := validateArchiveProjection(pk, state); err != nil {
		slog.Error("archive_backend: set projection", "key", key, "error", err)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		slog.Error("archive_backend: set projection: read meta", "key", key, "error", err)
		return
	}
	pm := projectionFromMeta(meta)
	pm.Entries[pk] = state
	if state == memory.ProjectionEmptied {
		pm.SourceRunes[pk] = 0
	}
	meta.Projection = metaFromProjection(pm)
	if err := b.saveMetaLocked(key, meta); err != nil {
		slog.Error("archive_backend: set projection: write meta", "key", key, "error", err)
	}
}

func (b *archiveBackend) MarkHydrated(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		slog.Error("archive_backend: mark hydrated: read meta", "key", key, "error", err)
		return
	}
	meta.Hydrated = true
	if err := b.saveMetaLocked(key, meta); err != nil {
		slog.Error("archive_backend: mark hydrated: write meta", "key", key, "error", err)
	}
}

// Save is a no-op: every append already fsyncs, and evicted lines must stay.
func (b *archiveBackend) Save(_ string) error { return nil }

// Close releases nothing (per-session stores hold no long-lived handles).
func (b *archiveBackend) Close() error { return nil }

// --- ContextWindowStore ---

func (b *archiveBackend) AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return memory.WindowSnapshot{}, err
	}
	if err := b.appendMessage(key, msg); err != nil {
		return memory.WindowSnapshot{}, err
	}
	return b.SnapshotWindow(ctx, key)
}

func (b *archiveBackend) SnapshotWindow(ctx context.Context, key string) (memory.WindowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return memory.WindowSnapshot{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked(key)
}

// CommitWindow performs one compare-and-set metadata write.
func (b *archiveBackend) CommitWindow(ctx context.Context, key string, before, after memory.WindowState) error {
	return b.commitWindow(ctx, key, before, after, false)
}

// RestoreWindow undoes an uninstalled candidate; it may move Skip backwards.
func (b *archiveBackend) RestoreWindow(ctx context.Context, key string, before, after memory.WindowState) error {
	return b.commitWindow(ctx, key, before, after, true)
}

func (b *archiveBackend) commitWindow(ctx context.Context, key string, before, after memory.WindowState, restore bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	lines, err := b.payloadLinesLocked(key)
	if err != nil {
		return err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return err
	}
	if !windowStateEqual(meta, before) {
		return memory.ErrWindowChanged
	}
	if after.Count != before.Count || after.Count != len(lines) || after.Skip < 0 ||
		(!restore && after.Skip < before.Skip) || after.Skip > after.Count {
		return errors.New("archive_backend: invalid checkpoint cursor")
	}
	if err := validateWindowAnchor(after, lines); err != nil {
		return err
	}
	meta.Skip = after.Skip
	meta.Count = after.Count
	meta.AnchorLine = cloneIntPtr(after.AnchorLine)
	meta.Projection = metaFromProjection(after.Projection)
	meta.Hydrated = after.Projection.Hydrated
	if err := b.saveMetaLocked(key, meta); err != nil {
		return err
	}
	return nil
}

// RollbackWindow restores the given turn-start state AND records the appended
// span it predates as a retained-but-excluded effect (FR-006). It never rewrites
// the archive: the aborted bytes stay on disk and stay recallable.
func (b *archiveBackend) RollbackWindow(ctx context.Context, key string, start memory.WindowState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	lines, err := b.payloadLinesLocked(key)
	if err != nil {
		return err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return err
	}
	if start.Count < 0 || start.Count > len(lines) {
		return errors.New("archive_backend: rollback archive is shorter than snapshot")
	}
	meta.Retracted = archiveRetractSpan(meta.Retracted, start.Count, len(lines))
	meta.Skip = start.Skip
	meta.Count = len(lines)
	meta.AnchorLine = cloneIntPtr(start.AnchorLine)
	meta.Projection = metaFromProjection(start.Projection)
	meta.Hydrated = start.Projection.Hydrated
	return b.saveMetaLocked(key, meta)
}

// --- extra store surface used by UnifiedStore ---

// ScanArchive streams model-archive lines to fn by ordinal, stopping on false.
func (b *archiveBackend) ScanArchive(ctx context.Context, key string, fn func(idx int, msg memory.ArchivedMessage) bool) error {
	lines, err := b.payloadLines(key)
	if err != nil {
		return err
	}
	for i, l := range lines {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := archivedMessage(l)
		if err != nil {
			return err
		}
		if !fn(i, msg) {
			return nil
		}
	}
	return nil
}

// ScanArchiveRange yields the LITERAL stored JSON bytes of each selected
// ordinal's private model_message (spec raw-range amendment), never the whole
// envelope, so source/route/mark fields never leak into recall output.
func (b *archiveBackend) ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, memory.ArchivedMessage) error) error {
	if from < 0 || to < from {
		return fmt.Errorf("archive_backend: archive_range must have 0 <= from <= to, got [%d,%d]", from, to)
	}
	lines, err := b.payloadLines(key)
	if err != nil {
		return err
	}
	if to >= len(lines) {
		return fmt.Errorf("archive_backend: archive_range [%d,%d] outside available archive records (%d records)", from, to, len(lines))
	}
	for i := from; i <= to; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := archivedMessage(lines[i])
		if err != nil {
			return err
		}
		if err := fn(i, lines[i].rawModel, msg); err != nil {
			return err
		}
	}
	return nil
}

// ScanEvictedArchive streams the evicted prefix (ordinals below Skip).
func (b *archiveBackend) ScanEvictedArchive(ctx context.Context, key string, fn func(int, []byte, memory.ArchivedMessage) error) (int, error) {
	b.mu.Lock()
	meta, err := b.loadMetaLocked(key)
	b.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if meta.Skip == 0 {
		return 0, nil
	}
	if err := b.ScanArchiveRange(ctx, key, 0, meta.Skip-1, fn); err != nil {
		return meta.Skip, err
	}
	return meta.Skip, nil
}

// appendMessage appends one payload for a model message.
func (b *archiveBackend) appendMessage(key string, msg providers.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return err
	}
	if err := b.appendOneLocked(key, msg); err != nil {
		return err
	}
	meta.Count++
	return b.saveMetaLocked(key, meta)
}

// appendOneLocked appends one payload, resolving the producing assistant call
// for a role "tool" message from the archive itself (the SessionStore write
// interface does not carry that identity): a tool result carries tool_result_for
// naming the assistant entry that issued its call. b.mu must be held.
func (b *archiveBackend) appendOneLocked(key string, msg providers.Message) error {
	store, err := b.store(key)
	if err != nil {
		return err
	}
	var callIdx map[string]string
	if msg.Role == "tool" {
		lines, err := b.payloadLinesLocked(key)
		if err != nil {
			return err
		}
		callIdx = assistantCallIndex(lines)
	}
	return appendPayload(store, msg, callIdx)
}

// snapshotLocked builds the JSONL-shaped window snapshot from the archive.
func (b *archiveBackend) snapshotLocked(key string) (memory.WindowSnapshot, error) {
	lines, err := b.payloadLinesLocked(key)
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	archive, err := archivedMessages(lines)
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	if meta.Count != len(archive) {
		meta.Count = len(archive)
		if err := b.saveMetaLocked(key, meta); err != nil {
			return memory.WindowSnapshot{}, err
		}
	}
	if meta.Skip < 0 || meta.Skip > meta.Count {
		return memory.WindowSnapshot{}, errors.New("archive_backend: invalid context window cursor")
	}
	if err := validateWindowAnchor(memory.WindowState{Skip: meta.Skip, Count: meta.Count, AnchorLine: meta.AnchorLine}, lines); err != nil {
		return memory.WindowSnapshot{}, err
	}
	state := memory.WindowState{Skip: meta.Skip, Count: meta.Count, AnchorLine: cloneIntPtr(meta.AnchorLine),
		Projection: projectionFromMeta(meta)}
	return memory.WindowSnapshot{State: state, Archive: archive,
		Retracted: append([]memory.ArchiveSpan(nil), meta.Retracted...)}, nil
}

// payloadLines enumerates this session's model-archive lines.
func (b *archiveBackend) payloadLines(key string) ([]payloadLine, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.payloadLinesLocked(key)
}

func (b *archiveBackend) payloadLinesLocked(key string) ([]payloadLine, error) {
	store, err := b.store(key)
	if err != nil {
		return nil, err
	}
	var out []payloadLine
	err = store.ScanAll(func(addr ArchiveAddress, rec ArchiveRecord) bool {
		if rec.ModelMessage == nil {
			return true // a body-free model_ref placement is not a new line
		}
		out = append(out, payloadLine{addr: addr, rec: rec, rawModel: rawModelMessage(rec)})
		return true
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// loadMetaLocked reads the backend meta, defaulting to a fresh zero state.
func (b *archiveBackend) loadMetaLocked(key string) (archiveBackendMeta, error) {
	store, err := b.store(key)
	if err != nil {
		return archiveBackendMeta{}, err
	}
	data, err := os.ReadFile(filepath.Join(store.dir(), archiveBackendMetaFile))
	if errors.Is(err, fs.ErrNotExist) {
		return archiveBackendMeta{}, nil
	}
	if err != nil {
		return archiveBackendMeta{}, fmt.Errorf("archive_backend: read meta: %w", err)
	}
	var m archiveBackendMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return archiveBackendMeta{}, fmt.Errorf("archive_backend: decode meta: %w", err)
	}
	return m, nil
}

func (b *archiveBackend) saveMetaLocked(key string, m archiveBackendMeta) error {
	store, err := b.store(key)
	if err != nil {
		return err
	}
	dir := store.dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("archive_backend: create dir: %w", err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("archive_backend: encode meta: %w", err)
	}
	tmp := filepath.Join(dir, archiveBackendMetaFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("archive_backend: write meta: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, archiveBackendMetaFile)); err != nil {
		return fmt.Errorf("archive_backend: rename meta: %w", err)
	}
	return nil
}

// appendPayload writes one model message as a validated payload envelope. For a
// role "tool" message it fills tool_result_for from callIdx (the producing
// assistant entry id keyed by call id); an unmatched result is refused visibly
// rather than stored as an orphan the archive forbids.
func appendPayload(store *ArchiveDayStore, msg providers.Message, callIdx map[string]string) error {
	mp, err := EncodeModelPayload(msg)
	if err != nil {
		return err
	}
	id, err := newArchivePayloadID()
	if err != nil {
		return err
	}
	rec := ArchiveRecord{
		TranscriptEntry: TranscriptEntry{
			ID: id, Role: msg.Role, Content: msg.Content,
			ViewMembership: viewMembershipForRole(msg.Role),
			Timestamp:      time.Now().UTC(),
		},
		ModelMessage: &mp,
		Source:       &EntrySource{Kind: sourceKindForRole(msg.Role)},
	}
	if msg.Role == "tool" {
		assistantID := callIdx[msg.ToolCallID]
		if msg.ToolCallID == "" || assistantID == "" {
			return fmt.Errorf("archive_backend: tool result %q has no matching assistant tool call", msg.ToolCallID)
		}
		rec.ToolResultFor = &ToolResultFor{AssistantEntryID: assistantID, ToolCallID: msg.ToolCallID}
	}
	_, err = store.Append(rec)
	return err
}

// assistantCallIndex maps a tool call id to the entry id of the assistant
// payload that issued it, from the archive itself. The most recent assistant
// wins, which is the correct producer for a tool result that immediately
// follows its call (providers reuse ids such as call_0 every turn).
func assistantCallIndex(lines []payloadLine) map[string]string {
	idx := make(map[string]string)
	for _, l := range lines {
		mp := l.rec.ModelMessage
		if mp == nil || len(mp.ToolCalls) == 0 {
			continue
		}
		for _, tc := range mp.ToolCalls {
			if tc.ID != "" {
				idx[tc.ID] = l.rec.ID
			}
		}
	}
	return idx
}

func newArchivePayloadID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("archive_backend: generate payload id: %w", err)
	}
	return "msg_" + id.String(), nil
}

// viewMembershipForRole picks the persisted membership for a model message:
// user/assistant text appears in chat as well as model order; system/tool
// messages are model-only. (Revisited when the generated chat projection lands.)
func viewMembershipForRole(role string) string {
	switch role {
	case "user", "assistant":
		return ViewMembershipBoth
	default:
		return ViewMembershipModel
	}
}

// sourceKindForRole mints the transitional provenance kind from the role only;
// the admission-time trusted source is wired by the caller-migration slice.
func sourceKindForRole(role string) string {
	switch role {
	case "user":
		return "user"
	case "assistant":
		return "agent"
	case "system":
		return "system"
	case "tool":
		return "tool"
	default:
		return "anonymous"
	}
}

// rawModelMessage extracts the LITERAL stored JSON bytes of a payload's private
// model_message, preserving its exact encoding (never a re-marshal).
func rawModelMessage(rec ArchiveRecord) []byte {
	whole, err := json.Marshal(rec)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(whole, &fields); err != nil {
		return nil
	}
	raw, ok := fields["model_message"]
	if !ok {
		return nil
	}
	return raw
}

// archivedMessages decodes payload lines into the interface's ArchivedMessage.
func archivedMessages(lines []payloadLine) ([]memory.ArchivedMessage, error) {
	out := make([]memory.ArchivedMessage, len(lines))
	for i, l := range lines {
		msg, err := archivedMessage(l)
		if err != nil {
			return nil, err
		}
		out[i] = msg
	}
	return out, nil
}

func archivedMessage(l payloadLine) (memory.ArchivedMessage, error) {
	if l.rec.ModelMessage == nil {
		return memory.ArchivedMessage{}, fmt.Errorf("archive_backend: line %s carries no model payload", l.addr.EntryID)
	}
	msg, err := DecodeModelPayload(*l.rec.ModelMessage)
	if err != nil {
		return memory.ArchivedMessage{}, err
	}
	var ts int64
	if !l.rec.Timestamp.IsZero() {
		ts = l.rec.Timestamp.Unix()
	}
	return memory.ArchivedMessage{Message: msg, TS: ts}, nil
}

// validateWindowAnchor mirrors the JSONL anchor rule over payload lines.
func validateWindowAnchor(state memory.WindowState, lines []payloadLine) error {
	if state.AnchorLine == nil {
		return nil
	}
	a := *state.AnchorLine
	if a < 0 || a >= state.Skip || a >= len(lines) {
		return errors.New("archive_backend: invalid checkpoint user anchor")
	}
	if lines[a].rec.ModelMessage == nil || lines[a].rec.ModelMessage.Role != "user" {
		return errors.New("archive_backend: invalid checkpoint user anchor")
	}
	return nil
}

// windowStateEqual compares a persisted meta to a WindowState (cursor + anchor +
// projection), the compare half of the commit CAS.
func windowStateEqual(meta archiveBackendMeta, state memory.WindowState) bool {
	if meta.Skip != state.Skip || meta.Count != state.Count {
		return false
	}
	if !intPtrEqual(meta.AnchorLine, state.AnchorLine) {
		return false
	}
	return projectionEqual(projectionFromMeta(meta), state.Projection)
}

// projectionFromMeta rebuilds the in-memory projection view.
func projectionFromMeta(meta archiveBackendMeta) memory.ProjectionMeta {
	pm := memory.ProjectionMeta{
		Entries:        make(memory.ProjectionSet),
		Hydrated:       meta.Hydrated,
		SourceRunes:    make(map[memory.ProjectionKey]int),
		TranscriptLine: make(map[memory.ProjectionKey]int),
	}
	for _, row := range meta.Projection {
		pk := memory.ProjectionKey{ToolCallID: row.ToolCallID, ArchiveLine: row.ArchiveLine}
		if row.State != "" {
			pm.Entries[pk] = row.State
		}
		if row.SourceRunes != nil && *row.SourceRunes >= 0 {
			pm.SourceRunes[pk] = *row.SourceRunes
		}
		if row.TranscriptLine != nil && *row.TranscriptLine >= 0 {
			pm.TranscriptLine[pk] = *row.TranscriptLine
		}
	}
	return pm
}

// metaFromProjection flattens the in-memory projection view for persistence.
func metaFromProjection(pm memory.ProjectionMeta) []archiveProjectionRow {
	srcByKey := make(map[memory.ProjectionKey]int, len(pm.SourceRunes))
	for k, v := range pm.SourceRunes {
		srcByKey[k] = v
	}
	tlByKey := make(map[memory.ProjectionKey]int, len(pm.TranscriptLine))
	for k, v := range pm.TranscriptLine {
		tlByKey[k] = v
	}
	keys := make(map[memory.ProjectionKey]struct{}, len(pm.Entries))
	for k := range pm.Entries {
		keys[k] = struct{}{}
	}
	for k := range srcByKey {
		keys[k] = struct{}{}
	}
	for k := range tlByKey {
		keys[k] = struct{}{}
	}
	rows := make([]archiveProjectionRow, 0, len(keys))
	for k := range keys {
		row := archiveProjectionRow{ToolCallID: k.ToolCallID, ArchiveLine: k.ArchiveLine, State: pm.Entries[k]}
		if v, ok := srcByKey[k]; ok {
			vv := v
			row.SourceRunes = &vv
		}
		if v, ok := tlByKey[k]; ok {
			vv := v
			row.TranscriptLine = &vv
		}
		rows = append(rows, row)
	}
	sortRows(rows)
	return rows
}

func projectionEqual(a, b memory.ProjectionMeta) bool {
	if a.Hydrated != b.Hydrated || len(a.Entries) != len(b.Entries) ||
		len(a.SourceRunes) != len(b.SourceRunes) || len(a.TranscriptLine) != len(b.TranscriptLine) {
		return false
	}
	for k, v := range a.Entries {
		if b.Entries[k] != v {
			return false
		}
	}
	for k, v := range a.SourceRunes {
		if b.SourceRunes[k] != v {
			return false
		}
	}
	for k, v := range a.TranscriptLine {
		if b.TranscriptLine[k] != v {
			return false
		}
	}
	return true
}

func validateArchiveProjection(pk memory.ProjectionKey, state memory.ProjectionState) error {
	if pk.ToolCallID == "" {
		return errors.New("archive_backend: projection: empty tool_call_id")
	}
	if pk.ArchiveLine < 0 {
		return fmt.Errorf("archive_backend: projection: negative archive_line %d", pk.ArchiveLine)
	}
	switch state {
	case memory.ProjectionCapped, memory.ProjectionCappedFailure, memory.ProjectionEmptied:
		return nil
	default:
		return fmt.Errorf("archive_backend: projection: unknown state %q", state)
	}
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// archiveRetractSpan adds the half-open span [start,end) to the retained-but-
// excluded set, coalescing abutting/overlapping spans. It mirrors
// pkg/memory/window.go::retractSpan (which is unexported) so the same FR-006
// rollback effect is expressed in this package's own words.
func archiveRetractSpan(spans []memory.ArchiveSpan, start, end int) []memory.ArchiveSpan {
	if end <= start {
		return spans
	}
	if start < 0 {
		start = 0
	}
	out := append([]memory.ArchiveSpan(nil), spans...)
	out = append(out, memory.ArchiveSpan{Start: start, End: end})
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	merged := out[:0]
	for _, s := range out {
		if len(merged) > 0 && s.Start <= merged[len(merged)-1].End {
			if s.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = s.End
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// sortRows orders projection rows deterministically by (archive_line, tool_call_id).
func sortRows(rows []archiveProjectionRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ArchiveLine != rows[j].ArchiveLine {
			return rows[i].ArchiveLine < rows[j].ArchiveLine
		}
		return rows[i].ToolCallID < rows[j].ToolCallID
	})
}
