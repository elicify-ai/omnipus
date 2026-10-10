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
// ORDINAL MODEL: the interfaces expose a zero-based `archive_line` ordinal
// (ProjectionKey.ArchiveLine, WindowState.Skip/Count). An ordinal is assigned per
// MODEL PLACEMENT, in placement order, and is the stable public recall selector:
// a payload whose view_membership is model/both takes the next ordinal when it
// is appended; a model_ref placement takes its own ordinal and resolves to its
// source payload; a chat-only saved input has none until it is placed. The
// ordinal -> record mapping lives in the content-free ordinal index
// (ordinal_index.go), so a window read costs the active window, not the
// lifetime (FR-005). Unpublished archive records are retained-but-excluded
// residue: they have no ordinal and no window can reach them.
//
// The dense lifetime methods (SnapshotWindow, AppendWindowMessage, ReadArchive,
// ScanArchive) remain only until their callers move to WindowView /
// ReadModelSlots (session-core U2 caller-migration steps 2-8); they read through
// the index, in ordinal order.
//
//   - Provenance: legacy SessionWriter appends mint EntrySource from the role
//     only; the explicit AppendModelMessage seam takes the producer's trusted
//     source and membership.
//   - Raw recall representation: ScanArchiveRange quotes the literal stored
//     model_message JSON (spec raw-range amendment), never the whole envelope.
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
	"strings"
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
	s, err := NewArchiveDayStore(b.baseDir, owningSessionID(key))
	if err != nil {
		return nil, err
	}
	b.stores[key] = s
	return s, nil
}

// owningSessionID maps an agent routing key to the immutable owning session id
// the model content belongs to (ARCHITECT-ANSWER-CUTOVER-SLICE4.md, "Identity";
// Decision D "no current-agent/active-agent guessing"). The agent routing key
// "agent:<agentID>:session:<sessionID>" embeds the store-backed owning session
// id, so the archive then lives at <baseDir>/<sessionID>/u2archive — beside the
// chat transcript, so a session delete removes both. Any other key (a plain
// session id, or a chat-scoped key whose owning session is not encoded) is left
// unchanged.
func owningSessionID(key string) string {
	rest, ok := strings.CutPrefix(key, "agent:")
	if !ok {
		return key
	}
	i := strings.Index(rest, ":session:")
	if i < 0 {
		return key
	}
	id := rest[i+len(":session:"):]
	if id == "" {
		return key
	}
	return id
}

// archiveBackendMeta is the persisted, content-free backend window state.
type archiveBackendMeta struct {
	Skip       int                    `json:"skip"`
	Count      int                    `json:"count"`
	AnchorLine *int                   `json:"anchor_archive_line,omitempty"`
	Retracted  []memory.ArchiveSpan   `json:"retracted,omitempty"`
	Hydrated   bool                   `json:"hydrated,omitempty"`
	Projection []archiveProjectionRow `json:"projection,omitempty"`
	// ConvSourceTorn records that the one-time CONV conversion tolerated a torn
	// final line (N2=B) and therefore RETAINED the legacy source bytes rather
	// than retiring them. A later boot must not delete them either.
	ConvSourceTorn bool `json:"conv_source_torn,omitempty"`
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
	view, err := b.WindowView(context.Background(), key)
	if err != nil {
		slog.Error("archive_backend: get history", "key", key, "error", err)
		return []providers.Message{}
	}
	msgs, _ := view.History()
	return msgs
}

// ReadArchive returns the full model archive from ordinal 0, ignoring Skip. It
// is an explicit full read (recall, CONV, tests) — never a per-step read.
func (b *archiveBackend) ReadArchive(_ context.Context, key string) ([]memory.ArchivedMessage, error) {
	lines, err := b.payloadLines(key)
	if err != nil {
		return nil, err
	}
	out := make([]memory.ArchivedMessage, len(lines))
	for i, l := range lines {
		if out[i], err = archivedMessage(l); err != nil {
			return nil, err
		}
	}
	return out, nil
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
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		slog.Error("archive_backend: set history: open", "key", key, "error", err)
		return
	}
	n, err := store.OrdinalCount()
	if err != nil {
		slog.Error("archive_backend: set history: count", "key", key, "error", err)
		return
	}
	if n > 0 {
		slog.Error("archive_backend: set history refused",
			"key", key, "error", fmt.Errorf("%w: %q has %d line(s)", memory.ErrArchiveNotEmpty, key, n))
		return
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		slog.Error("archive_backend: set history: read meta", "key", key, "error", err)
		return
	}
	// Skip is left as-is (FR-047); projection and retracted cannot survive a
	// first fill.
	meta.Projection = nil
	meta.Retracted = nil
	for _, m := range history {
		if _, err := b.appendLegacyLocked(key, m); err != nil {
			slog.Error("archive_backend: set history: append", "key", key, "error", err)
			return
		}
	}
	if meta.Count, err = store.OrdinalCount(); err != nil {
		slog.Error("archive_backend: set history: count", "key", key, "error", err)
		return
	}
	if err := b.saveMetaLocked(key, meta); err != nil {
		slog.Error("archive_backend: set history: write meta", "key", key, "error", err)
	}
}

// TruncateHistory keeps only the last keepLast messages (mirrors the JSONL rule).
func (b *archiveBackend) TruncateHistory(key string, keepLast int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		slog.Error("archive_backend: truncate: open", "key", key, "error", err)
		return
	}
	n, err := store.OrdinalCount()
	if err != nil {
		slog.Error("archive_backend: truncate: count", "key", key, "error", err)
		return
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		slog.Error("archive_backend: truncate: read meta", "key", key, "error", err)
		return
	}
	meta.Count = n
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

// AppendWindowMessage is the dense-snapshot append the callers migrate off
// (step 2); it appends through the same checked seam as AppendModelMessage.
func (b *archiveBackend) AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (memory.WindowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return memory.WindowSnapshot{}, err
	}
	if err := b.appendMessage(key, msg); err != nil {
		return memory.WindowSnapshot{}, err
	}
	return b.SnapshotWindow(ctx, key)
}

// SnapshotWindow is the dense lifetime snapshot the callers migrate off (steps
// 3-5). It reads every model slot through the ordinal index.
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
	store, err := b.store(key)
	if err != nil {
		return err
	}
	n, err := store.OrdinalCount()
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
	if after.Count != before.Count || after.Count != n || after.Skip < 0 ||
		(!restore && after.Skip < before.Skip) || after.Skip > after.Count {
		return errors.New("archive_backend: invalid checkpoint cursor")
	}
	if err := validateWindowAnchor(store, after); err != nil {
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

// RollbackWindow restores the given turn-start state AND records the appended span
// it predates as a retained-but-excluded effect (FR-006). It never rewrites the
// archive: the aborted bytes stay on disk and stay recallable.
func (b *archiveBackend) RollbackWindow(ctx context.Context, key string, start memory.WindowState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		return err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return err
	}
	if start.Count < 0 || start.Count > n {
		return errors.New("archive_backend: rollback archive is shorter than snapshot")
	}
	meta.Retracted = archiveRetractSpan(meta.Retracted, start.Count, n)
	meta.Skip = start.Skip
	meta.Count = n
	meta.AnchorLine = cloneIntPtr(start.AnchorLine)
	meta.Projection = metaFromProjection(start.Projection)
	meta.Hydrated = start.Projection.Hydrated
	return b.saveMetaLocked(key, meta)
}

// --- bounded window surface (U2 caller-migration step 1) ---

// WindowView builds the bounded snapshot: the window state, the live slots, the
// anchor and the turn counters. Its cost is the active window plus one anchor
// read — it never walks the evicted prefix.
func (b *archiveBackend) WindowView(ctx context.Context, key string) (WindowView, error) {
	if err := ctx.Err(); err != nil {
		return WindowView{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.viewLocked(key)
}

// AppendModelMessage is the checked model append: the producer chooses the
// membership and supplies the trusted source and, for a tool result, the exact
// address of the assistant record that issued the call. It returns the new slot
// and the bounded view after the append — no lifetime snapshot, no guessed
// identity.
func (b *archiveBackend) AppendModelMessage(ctx context.Context, key string, in ModelAppend) (ModelSlot, WindowView, error) {
	if err := ctx.Err(); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	slot, err := b.appendModelLocked(key, in)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	view, err := b.viewLocked(key)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	return slot, view, nil
}

// PlaceSavedInput places a saved, chat-only input into model order exactly once
// by appending a body-free model_ref at the consumption fence (Decision B). The
// placement takes its own ordinal. A second placement of the same source returns
// ErrAlreadyConsumed. The caller applies the existing consumption/Stop fence.
func (b *archiveBackend) PlaceSavedInput(ctx context.Context, key string, source ArchiveAddress) (ModelSlot, WindowView, error) {
	if err := ctx.Err(); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	// The already-placed set is derived from the published window rows — no
	// separate counter or queue. A placement is always inside the window that
	// consumed it; an evicted source was consumed long before.
	rows, err := store.readOrdinalRows(clampSkip(meta.Skip, n), n)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	for _, row := range rows {
		if row.Source != nil && *row.Source == source {
			return ModelSlot{}, WindowView{}, ErrAlreadyConsumed
		}
	}
	src, err := store.ReadAt(source)
	if err != nil {
		return ModelSlot{}, WindowView{}, fmt.Errorf("model placement: resolve source: %w", err)
	}
	if src.ModelMessage == nil || src.Type == EntryTypeModelRef {
		return ModelSlot{}, WindowView{}, fmt.Errorf("model placement: source %s is not a payload record", source.EntryID)
	}
	if src.ViewMembership != ViewMembershipChat {
		return ModelSlot{}, WindowView{}, fmt.Errorf(
			"model placement: source %s is %q membership and already holds its own model slot", source.EntryID, src.ViewMembership)
	}
	rec, err := newModelRefRecord(store, source, src)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	addr, row, err := store.AppendIndexed(rec)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	meta.Count = row.Ordinal + 1
	if err := b.saveMetaLocked(key, meta); err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	msg, err := DecodeModelPayload(*src.ModelMessage)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	view, err := b.viewLocked(key)
	if err != nil {
		return ModelSlot{}, WindowView{}, err
	}
	return ModelSlot{Ordinal: row.Ordinal, Addr: addr, Message: msg, TS: row.TS, UserTurn: row.UserTurn}, view, nil
}

// ReadModelSlots streams the slots with ordinals in [from, to] through the
// ordinal index: the work is the width of the range, never the lifetime. fn
// receives the slot and the LITERAL stored model_message JSON. A slot whose
// record cannot be resolved is an error, never a silently short range.
func (b *archiveBackend) ReadModelSlots(ctx context.Context, key string, from, to int, fn func(ModelSlot, []byte) error) error {
	if from < 0 || to < from {
		return fmt.Errorf("archive_backend: archive_range must have 0 <= from <= to, got [%d,%d]", from, to)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		return err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return err
	}
	if to >= n {
		return fmt.Errorf("archive_backend: archive_range [%d,%d] outside available archive records (%d records)", from, to, n)
	}
	rows, err := store.readOrdinalRows(from, to+1)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		slot, raw, err := loadSlot(store, row)
		if err != nil {
			return err
		}
		if err := fn(slot, raw); err != nil {
			return err
		}
	}
	return nil
}

// --- extra store surface used by UnifiedStore ---

// scanChunk is how many index rows ScanArchive reads at a time.
const scanChunk = 128

// ScanArchive streams model slots to fn by ordinal, stopping on false. It is an
// explicit recall read: it walks the index in chunks and loads one slot at a
// time, never materializing the archive.
func (b *archiveBackend) ScanArchive(ctx context.Context, key string, fn func(idx int, msg memory.ArchivedMessage) bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		return err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return err
	}
	for from := 0; from < n; from += scanChunk {
		rows, err := store.readOrdinalRows(from, min(n, from+scanChunk))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			slot, _, err := loadSlot(store, row)
			if err != nil {
				return err
			}
			if !fn(slot.Ordinal, memory.ArchivedMessage{Message: slot.Message, TS: slot.TS}) {
				return nil
			}
		}
	}
	return nil
}

// ScanArchiveRange yields the LITERAL stored JSON bytes of each selected
// ordinal's private model_message (spec raw-range amendment), never the whole
// envelope, so source/route/mark fields never leak into recall output.
func (b *archiveBackend) ScanArchiveRange(ctx context.Context, key string, from, to int, fn func(int, []byte, memory.ArchivedMessage) error) error {
	return b.ReadModelSlots(ctx, key, from, to, func(slot ModelSlot, raw []byte) error {
		return fn(slot.Ordinal, raw, memory.ArchivedMessage{Message: slot.Message, TS: slot.TS})
	})
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

// appendMessage appends one legacy SessionWriter message: the role-derived
// membership and source it has always had, and, for a tool result, the issuing
// assistant found among the window's rows. The explicit AppendModelMessage seam
// is the replacement; this stays until every producer has moved to it.
func (b *archiveBackend) appendMessage(key string, msg providers.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.appendLegacyLocked(key, msg)
	return err
}

func (b *archiveBackend) appendLegacyLocked(key string, msg providers.Message) (ModelSlot, error) {
	in := ModelAppend{
		Message:        msg,
		ViewMembership: viewMembershipForRole(msg.Role),
		Source:         EntrySource{Kind: sourceKindForRole(msg.Role)},
	}
	if msg.Role == "tool" {
		issuer, err := b.issuerAddrLocked(key, msg.ToolCallID)
		if err != nil {
			return ModelSlot{}, err
		}
		in.ToolResultFor = &issuer
	}
	return b.appendModelLocked(key, in)
}

// issuerAddrLocked finds the assistant record that issued callID among the
// window's rows, newest first (providers reuse ids such as call_0 every turn, so
// the most recent issuer is the producer of a result that follows its call).
func (b *archiveBackend) issuerAddrLocked(key, callID string) (ArchiveAddress, error) {
	store, err := b.store(key)
	if err != nil {
		return ArchiveAddress{}, err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return ArchiveAddress{}, err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return ArchiveAddress{}, err
	}
	rows, err := store.readOrdinalRows(clampSkip(meta.Skip, n), n)
	if err != nil {
		return ArchiveAddress{}, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Role != "assistant" {
			continue
		}
		for _, id := range rows[i].CallIDs {
			if id == callID {
				return rows[i].content(), nil
			}
		}
	}
	return ArchiveAddress{}, fmt.Errorf("archive_backend: tool result %q has no issuing assistant in the window", callID)
}

// appendModelLocked appends one checked model message and publishes its ordinal.
// b.mu must be held.
func (b *archiveBackend) appendModelLocked(key string, in ModelAppend) (ModelSlot, error) {
	if err := in.validate(); err != nil {
		return ModelSlot{}, err
	}
	store, err := b.store(key)
	if err != nil {
		return ModelSlot{}, err
	}
	rec, err := newPayloadRecord(store, in)
	if err != nil {
		return ModelSlot{}, err
	}
	addr, row, err := store.AppendIndexed(rec)
	if err != nil {
		return ModelSlot{}, err
	}
	if row == nil {
		return ModelSlot{}, fmt.Errorf("archive_backend: record %s took no model slot", rec.ID)
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return ModelSlot{}, err
	}
	meta.Count = row.Ordinal + 1
	if err := b.saveMetaLocked(key, meta); err != nil {
		return ModelSlot{}, err
	}
	return ModelSlot{Ordinal: row.Ordinal, Addr: addr, Message: in.Message, TS: row.TS, UserTurn: row.UserTurn}, nil
}

// viewLocked builds the bounded WindowView. b.mu must be held.
func (b *archiveBackend) viewLocked(key string) (WindowView, error) {
	store, err := b.store(key)
	if err != nil {
		return WindowView{}, err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return WindowView{}, err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return WindowView{}, err
	}
	if meta.Count != n {
		meta.Count = n
		if err := b.saveMetaLocked(key, meta); err != nil {
			return WindowView{}, err
		}
	}
	if meta.Skip < 0 || meta.Skip > n {
		return WindowView{}, errors.New("archive_backend: invalid context window cursor")
	}
	state := memory.WindowState{Skip: meta.Skip, Count: meta.Count, AnchorLine: cloneIntPtr(meta.AnchorLine),
		Projection: projectionFromMeta(meta)}
	if err := validateWindowAnchor(store, state); err != nil {
		return WindowView{}, err
	}
	// Rows from Skip-1 (for the prior user-turn count) to the end.
	first := meta.Skip
	if first > 0 {
		first--
	}
	rows, err := store.readOrdinalRows(first, n)
	if err != nil {
		return WindowView{}, err
	}
	view := WindowView{State: state, Excluded: append([]memory.ArchiveSpan(nil), meta.Retracted...)}
	if meta.Skip > 0 {
		view.PriorUserTurns = rows[0].UserTurn
		rows = rows[1:]
	}
	view.turnsAfter = make([]int, 0, len(rows))
	for _, row := range rows {
		view.turnsAfter = append(view.turnsAfter, row.UserTurn)
		if retractedOrdinal(meta.Retracted, row.Ordinal) {
			continue
		}
		slot, _, err := loadSlot(store, row)
		if err != nil {
			return WindowView{}, err
		}
		view.Live = append(view.Live, slot)
	}
	if meta.Skip > 0 {
		lead, err := b.leadLocked(store, meta.Skip)
		if err != nil {
			return WindowView{}, err
		}
		view.Lead = lead
	}
	if a := meta.AnchorLine; a != nil && !retractedOrdinal(meta.Retracted, *a) {
		arows, err := store.readOrdinalRows(*a, *a+1)
		if err != nil {
			return WindowView{}, err
		}
		slot, _, err := loadSlot(store, arows[0])
		if err != nil {
			return WindowView{}, err
		}
		view.Anchor = &slot
	}
	return view, nil
}

// leadLocked reads the evicted tail an open tool group still owns: the slots
// directly before skip, back to and including the nearest user or assistant slot
// (at most leadLimit). The walk uses index rows only; only the slots it keeps are
// loaded.
func (b *archiveBackend) leadLocked(store *ArchiveDayStore, skip int) ([]ModelSlot, error) {
	lo := max(0, skip-leadLimit)
	rows, err := store.readOrdinalRows(lo, skip)
	if err != nil {
		return nil, err
	}
	start := 0
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Role == "user" || rows[i].Role == "assistant" {
			start = i
			break
		}
	}
	rows = rows[start:]
	out := make([]ModelSlot, 0, len(rows))
	for _, row := range rows {
		slot, _, err := loadSlot(store, row)
		if err != nil {
			return nil, err
		}
		out = append(out, slot)
	}
	return out, nil
}

// snapshotLocked builds the DENSE lifetime snapshot (callers migrate off it).
func (b *archiveBackend) snapshotLocked(key string) (memory.WindowSnapshot, error) {
	lines, err := b.payloadLinesLocked(key)
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	meta, err := b.loadMetaLocked(key)
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	archive := make([]memory.ArchivedMessage, len(lines))
	for i, l := range lines {
		if archive[i], err = archivedMessage(l); err != nil {
			return memory.WindowSnapshot{}, err
		}
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
	store, err := b.store(key)
	if err != nil {
		return memory.WindowSnapshot{}, err
	}
	state := memory.WindowState{Skip: meta.Skip, Count: meta.Count, AnchorLine: cloneIntPtr(meta.AnchorLine),
		Projection: projectionFromMeta(meta)}
	if err := validateWindowAnchor(store, state); err != nil {
		return memory.WindowSnapshot{}, err
	}
	return memory.WindowSnapshot{State: state, Archive: archive,
		Retracted: append([]memory.ArchiveSpan(nil), meta.Retracted...)}, nil
}

// payloadLines enumerates this session's model slots as content lines, in
// ordinal order. It is an explicit full read (recall, tests, the dense snapshot).
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
	n, err := store.OrdinalCount()
	if err != nil {
		return nil, err
	}
	rows, err := store.readOrdinalRows(0, n)
	if err != nil {
		return nil, err
	}
	out := make([]payloadLine, 0, len(rows))
	for _, row := range rows {
		raw, rec, err := store.ReadAtRaw(row.content())
		if err != nil {
			return nil, err
		}
		out = append(out, payloadLine{addr: row.content(), rec: rec, rawModel: literalModelMessage(raw)})
	}
	return out, nil
}

// loadSlot reads one slot's record (following a model_ref to its source) and
// decodes its message. raw is the literal stored model_message JSON.
func loadSlot(store *ArchiveDayStore, row ordinalRow) (ModelSlot, []byte, error) {
	raw, rec, err := store.ReadAtRaw(row.content())
	if err != nil {
		return ModelSlot{}, nil, fmt.Errorf("window: read model slot %d: %w", row.Ordinal, err)
	}
	if rec.ModelMessage == nil {
		return ModelSlot{}, nil, fmt.Errorf("window: model slot %d resolves no payload", row.Ordinal)
	}
	msg, err := DecodeModelPayload(*rec.ModelMessage)
	if err != nil {
		return ModelSlot{}, nil, fmt.Errorf("window: decode model slot %d: %w", row.Ordinal, err)
	}
	return ModelSlot{Ordinal: row.Ordinal, Addr: row.Slot, Message: msg, TS: row.TS, UserTurn: row.UserTurn,
		Origin: rec.ModelOrigin}, literalModelMessage(raw), nil
}

func clampSkip(skip, n int) int {
	if skip < 0 {
		return 0
	}
	if skip > n {
		return n
	}
	return skip
}

func retractedOrdinal(spans []memory.ArchiveSpan, ordinal int) bool {
	for _, s := range spans {
		if ordinal >= s.Start && ordinal < s.End {
			return true
		}
	}
	return false
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

// newPayloadRecord builds one model message as a validated payload envelope from
// a checked append. For a role "tool" message it resolves the producing assistant
// record from the producer-supplied address (one bounded read) and refuses an
// issuer that does not declare the call — an unprovable result is never stored.
func newPayloadRecord(store *ArchiveDayStore, in ModelAppend) (ArchiveRecord, error) {
	mp, err := EncodeModelPayload(in.Message)
	if err != nil {
		return ArchiveRecord{}, err
	}
	id, err := newArchivePayloadID()
	if err != nil {
		return ArchiveRecord{}, err
	}
	src := in.Source
	rec := ArchiveRecord{
		TranscriptEntry: TranscriptEntry{
			ID: id, Role: in.Message.Role, Content: in.Message.Content,
			ViewMembership: in.ViewMembership,
			Timestamp:      time.Now().UTC(),
			AgentID:        in.AgentID,
		},
		ModelMessage: &mp,
		Source:       &src,
	}
	if in.Message.Role == "tool" {
		issuer, err := store.ReadAt(*in.ToolResultFor)
		if err != nil {
			return ArchiveRecord{}, fmt.Errorf("archive_backend: tool result %q: resolve issuing assistant: %w", in.Message.ToolCallID, err)
		}
		if issuer.ModelMessage == nil || issuer.ModelMessage.Role != "assistant" || !payloadDeclaresCall(*issuer.ModelMessage, in.Message.ToolCallID) {
			return ArchiveRecord{}, fmt.Errorf("archive_backend: record %s does not declare tool call %q", in.ToolResultFor.EntryID, in.Message.ToolCallID)
		}
		rec.ToolResultFor = &ToolResultFor{AssistantEntryID: issuer.ID, ToolCallID: in.Message.ToolCallID}
	}
	return rec, nil
}

func payloadDeclaresCall(mp ModelPayload, callID string) bool {
	for _, tc := range mp.ToolCalls {
		if tc.ID == callID {
			return true
		}
	}
	return false
}

// newModelRefRecord builds the body-free model_ref placement of a saved source.
func newModelRefRecord(store *ArchiveDayStore, source ArchiveAddress, src ArchiveRecord) (ArchiveRecord, error) {
	effectID, err := newModelRefID()
	if err != nil {
		return ArchiveRecord{}, err
	}
	return ArchiveRecord{
		TranscriptEntry: TranscriptEntry{
			ID:             effectID,
			Type:           EntryTypeModelRef,
			ViewMembership: ViewMembershipModel,
			Timestamp:      store.now().UTC(),
			AgentID:        src.AgentID,
		},
		ModelRef: &ModelRef{EntryID: source.EntryID, PartitionKey: source.PartitionKey, ByteOffset: source.ByteOffset},
	}, nil
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

// literalModelMessage extracts the LITERAL stored JSON bytes of a payload's
// private model_message from the record's own raw line, so raw-range recall
// quotes it exactly as saved (spec Decision C raw-range amendment: "extracted
// from the retained record without rewriting it"). It never re-marshals the
// record, and it returns the WHOLE envelope's private sibling fields nowhere —
// only the model_message value is returned.
func literalModelMessage(rawLine []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawLine, &fields); err != nil {
		return nil
	}
	raw, ok := fields["model_message"]
	if !ok {
		return nil
	}
	return raw
}

// archivedMessage decodes one content line into the interface's ArchivedMessage.
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

// validateWindowAnchor mirrors the JSONL anchor rule over the ordinal index: the
// anchor must lie before Skip and name a user message.
func validateWindowAnchor(store *ArchiveDayStore, state memory.WindowState) error {
	if state.AnchorLine == nil {
		return nil
	}
	a := *state.AnchorLine
	if a < 0 || a >= state.Skip || a >= state.Count {
		return errors.New("archive_backend: invalid checkpoint user anchor")
	}
	rows, err := store.readOrdinalRows(a, a+1)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].Role != "user" {
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
