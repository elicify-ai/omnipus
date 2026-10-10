package memory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// WindowState is internal context-view persistence, never a gateway wire type.
// AnchorLine addresses the original user message when it lies before Skip.
type WindowState struct {
	Skip       int
	Count      int
	AnchorLine *int
	Projection ProjectionMeta
}

func (w WindowState) Clone() WindowState {
	out := w
	if w.AnchorLine != nil {
		v := *w.AnchorLine
		out.AnchorLine = &v
	}
	out.Projection.Entries = w.Projection.Entries.Clone()
	out.Projection.SourceRunes = make(map[ProjectionKey]int, len(w.Projection.SourceRunes))
	for k, v := range w.Projection.SourceRunes {
		out.Projection.SourceRunes[k] = v
	}
	out.Projection.TranscriptAddr = make(map[ProjectionKey]RecordAddress, len(w.Projection.TranscriptAddr))
	for k, v := range w.Projection.TranscriptAddr {
		out.Projection.TranscriptAddr[k] = v
	}
	return out
}

// ArchiveSpan is a half-open [Start, End) range of physical archive-line
// indices that is RETAINED on disk but EXCLUDED from the model view by an
// append-only rollback effect (session-core FR-006 / DEL-12). The bytes stay in
// the archive — ReadArchive/recall still return them — while every provider
// request built from WindowHistory omits them. Because the archive is strictly
// append-only and never renumbered, a span address stays exact for the
// session's whole lifetime, which is why spans are never re-derived or shifted.
type ArchiveSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// WindowSnapshot binds archive identities and metadata under one session lock.
// Retracted lists the retained-but-excluded spans (FR-006); an empty slice
// means the whole archive is in the model view.
type WindowSnapshot struct {
	State     WindowState
	Archive   []ArchivedMessage
	Retracted []ArchiveSpan
}

func windowState(meta sessionMeta) WindowState {
	return WindowState{Skip: meta.Skip, Count: meta.Count, AnchorLine: meta.AnchorLine,
		Projection: projectionMeta(meta)}.Clone()
}

func projectionMeta(meta sessionMeta) ProjectionMeta {
	pm := ProjectionMeta{Entries: projectionFromEntries(meta.Projection),
		SourceRunes:    make(map[ProjectionKey]int),
		TranscriptAddr: make(map[ProjectionKey]RecordAddress)}
	for _, e := range meta.Projection {
		k := ProjectionKey{ToolCallID: e.ToolCallID, ArchiveLine: e.ArchiveLine}
		if e.SourceRunes != nil && *e.SourceRunes >= 0 && validProjectionState(e.State) {
			pm.SourceRunes[k] = *e.SourceRunes
		}
		if e.TranscriptAddr != nil && validRecordAddress(*e.TranscriptAddr) && k.ToolCallID != "" && k.ArchiveLine >= 0 {
			pm.TranscriptAddr[k] = *e.TranscriptAddr
		}
	}
	return pm
}

func entriesWithLimits(pm ProjectionMeta) []projectionEntry {
	keys := pm.Entries.Clone()
	for k := range pm.TranscriptAddr {
		if _, exists := keys[k]; !exists {
			keys[k] = "" // A full result still needs its transcript identity.
		}
	}
	entries := projectionToEntries(keys)
	for i := range entries {
		k := ProjectionKey{ToolCallID: entries[i].ToolCallID, ArchiveLine: entries[i].ArchiveLine}
		if n, ok := pm.SourceRunes[k]; ok {
			v := n
			entries[i].SourceRunes = &v
		}
		if a, ok := pm.TranscriptAddr[k]; ok {
			v := a
			entries[i].TranscriptAddr = &v
		}
	}
	return entries
}

func applyWindow(meta *sessionMeta, state WindowState) {
	state = state.Clone()
	meta.Skip, meta.Count, meta.AnchorLine = state.Skip, state.Count, state.AnchorLine
	meta.Projection = entriesWithLimits(state.Projection)
	meta.UpdatedAt = time.Now()
}

// retractedAt reports whether physical archive line i lies inside a
// retained-but-excluded span (FR-006). Spans are few (at most one per aborted
// turn) and disjoint, so a linear scan is the right shape here.
func retractedAt(spans []ArchiveSpan, i int) bool {
	for _, s := range spans {
		if i >= s.Start && i < s.End {
			return true
		}
	}
	return false
}

func WindowHistory(snap WindowSnapshot) ([]providers.Message, []int) {
	out := make([]providers.Message, 0, len(snap.Archive)-min(snap.State.Skip, len(snap.Archive))+1)
	lines := make([]int, 0, cap(out))
	if a := snap.State.AnchorLine; a != nil && *a >= 0 && *a < snap.State.Skip && *a < len(snap.Archive) && !retractedAt(snap.Retracted, *a) {
		out = append(out, snap.Archive[*a].Message)
		lines = append(lines, *a)
	}
	for i := max(0, snap.State.Skip); i < len(snap.Archive); i++ {
		// FR-006: a retracted span keeps its bytes in the archive but is
		// excluded from every provider request built from this window.
		if retractedAt(snap.Retracted, i) {
			continue
		}
		out = append(out, snap.Archive[i].Message)
		lines = append(lines, i)
	}
	return out, lines
}

func (s *JSONLStore) SnapshotWindow(ctx context.Context, key string) (WindowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return WindowSnapshot{}, err
	}
	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()
	return s.snapshotWindowLocked(ctx, key)
}

func (s *JSONLStore) snapshotWindowLocked(ctx context.Context, key string) (WindowSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return WindowSnapshot{}, err
	}
	meta, err := s.readMeta(key)
	if err != nil {
		return WindowSnapshot{}, err
	}
	archive, err := readMessages(s.jsonlPath(key), 0)
	if err != nil {
		return WindowSnapshot{}, err
	}
	// Archive addresses index decoded records, just like ReadArchive/ScanArchive.
	// Recover an append whose metadata update was interrupted, without deriving
	// Skip from an anchored view or counting malformed physical lines as records.
	countChanged := meta.Count != len(archive)
	meta.Count = len(archive)
	if meta.Skip < 0 || meta.Skip > meta.Count {
		return WindowSnapshot{}, errors.New("memory: invalid context window cursor")
	}
	if err := validateWindowAnchor(meta.AnchorLine, meta.Skip, archive); err != nil {
		return WindowSnapshot{}, err
	}
	if countChanged {
		meta.UpdatedAt = time.Now()
		if err := ctx.Err(); err != nil {
			return WindowSnapshot{}, err
		}
		if err := s.writeMeta(key, meta); err != nil {
			return WindowSnapshot{}, err
		}
	}
	return WindowSnapshot{State: windowState(meta), Archive: archive,
		Retracted: append([]ArchiveSpan(nil), meta.Retracted...)}, nil
}

// AppendWindowMessage returns the actual appended identity and metadata under
// the same session shard as the append. Errors never become a live message.
func (s *JSONLStore) AppendWindowMessage(ctx context.Context, key string, msg providers.Message) (WindowSnapshot, error) {
	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()
	if _, err := s.snapshotWindowLocked(ctx, key); err != nil {
		return WindowSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return WindowSnapshot{}, err
	}
	if err := s.addMsgLocked(key, msg); err != nil {
		return WindowSnapshot{}, err
	}
	return s.snapshotWindowLocked(ctx, key)
}

func validateWindowAnchor(anchor *int, skip int, archive []ArchivedMessage) error {
	if anchor != nil && (*anchor < 0 || *anchor >= skip || *anchor >= len(archive) || archive[*anchor].Role != "user") {
		return errors.New("memory: invalid checkpoint user anchor")
	}
	return nil
}

var ErrWindowChanged = errors.New("memory: context window changed while staging checkpoint")

// CommitWindow performs one atomic metadata write, before callers install a slice.
func (s *JSONLStore) CommitWindow(ctx context.Context, key string, before, after WindowState) error {
	return s.commitWindow(ctx, key, before, after, false)
}

// RestoreWindow undoes only an uninstalled metadata candidate. Unlike turn
// rollback, it never removes archive appends and refuses a concurrent change.
func (s *JSONLStore) RestoreWindow(ctx context.Context, key string, before, after WindowState) error {
	return s.commitWindow(ctx, key, before, after, true)
}

func (s *JSONLStore) commitWindow(ctx context.Context, key string, before, after WindowState, restore bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()
	meta, err := s.readMeta(key)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(windowState(meta), before.Clone()) {
		return ErrWindowChanged
	}
	archive, err := readMessages(s.jsonlPath(key), 0)
	if err != nil {
		return err
	}
	if after.Count != before.Count || after.Count != len(archive) || after.Skip < 0 || (!restore && after.Skip < before.Skip) || after.Skip > after.Count {
		return errors.New("memory: invalid checkpoint cursor")
	}
	if err := validateWindowMetadata(after, archive); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	applyWindow(&meta, after)
	return s.writeMeta(key, meta)
}

func validateWindowMetadata(after WindowState, archive []ArchivedMessage) error {
	if after.Skip < 0 || after.Skip > after.Count || after.Count != len(archive) {
		return errors.New("memory: invalid checkpoint cursor")
	}
	if err := validateWindowAnchor(after.AnchorLine, after.Skip, archive); err != nil {
		return err
	}
	for k, v := range after.Projection.Entries {
		if err := validateProjectionWrite(k, v); err != nil {
			return err
		}
		if k.ArchiveLine < after.Skip || k.ArchiveLine >= after.Count {
			return fmt.Errorf("memory: projection outside retained window")
		}
		if source := archive[k.ArchiveLine]; source.Role != "tool" || source.ToolCallID != k.ToolCallID {
			return errors.New("memory: projection does not address its archived tool result")
		}
	}
	for k, n := range after.Projection.SourceRunes {
		state := after.Projection.Entries[k]
		if n < 0 || state == "" || k.ArchiveLine < after.Skip || k.ArchiveLine >= len(archive) || n > utf8.RuneCountInString(archive[k.ArchiveLine].Content) || (state == ProjectionEmptied && n != 0) {
			return errors.New("memory: invalid retained source limit")
		}
	}
	for k, addr := range after.Projection.TranscriptAddr {
		if !validRecordAddress(addr) || k.ToolCallID == "" || k.ArchiveLine < after.Skip || k.ArchiveLine >= len(archive) {
			return errors.New("memory: invalid transcript projection identity")
		}
		if source := archive[k.ArchiveLine]; source.Role != "tool" || source.ToolCallID != k.ToolCallID {
			return errors.New("memory: transcript identity does not address its archived tool result")
		}
	}
	return nil
}

// RollbackWindow restores the session's view/window metadata (cursor, anchor
// and source limits) to the given snapshot AND records the appended span the
// snapshot predates as a RETAINED-BUT-EXCLUDED effect (session-core FR-006 /
// DEL-12). It NEVER rewrites the archive: the aborted bytes stay on disk and
// remain reachable via ReadArchive/recall, they are merely excluded from every
// later provider request built by WindowHistory.
//
// session-core FR-006: a rollback MUST move view/window metadata and never
// rewrite retained bytes. This used to call rewriteJSONL to truncate the
// archive down to start.Count — a whole-file rewrite that destroyed the
// retained bytes the recall archive depends on. It no longer touches the JSONL
// file at all: the archive only ever grows (append-only).
//
// Because the archive is never renumbered, a later valid append lands
// physically AFTER the excluded span and is visible again — the aborted span
// never resurrects, and it never blocks new content.
//
// This is now the SOLE rollback primitive: the abort path (pkg/agent
// turn_exit.go::restoreSession) calls it alone. Nothing truncates the archive
// on a rollback any more.
func (s *JSONLStore) RollbackWindow(ctx context.Context, key string, start WindowState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()
	meta, err := s.readMeta(key)
	if err != nil {
		return err
	}
	archive, err := readMessages(s.jsonlPath(key), 0)
	if err != nil {
		return err
	}
	if start.Count < 0 || start.Count > len(archive) {
		return errors.New("memory: rollback archive is shorter than snapshot")
	}
	if err := validateWindowMetadata(start, archive[:start.Count]); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Exclude [start.Count, len(archive)) from the model view without deleting
	// it, then restore the exact turn-start cursor/anchor/projection. The
	// archive did not shrink, so meta.Count re-syncs to its physical length —
	// exactly what the reader contract requires (see snapshotWindowLocked).
	meta.Retracted = retractSpan(meta.Retracted, start.Count, len(archive))
	applyWindow(&meta, start)
	meta.Count = len(archive)
	return s.writeMeta(key, meta)
}

// retractSpan adds the half-open span [start,end) to the retained-but-excluded
// set, coalescing it with any span it abuts or overlaps. The result stays
// sorted by Start and disjoint. An empty span (end <= start) is a no-op.
func retractSpan(spans []ArchiveSpan, start, end int) []ArchiveSpan {
	if end <= start {
		return spans
	}
	if start < 0 {
		start = 0
	}
	out := append([]ArchiveSpan(nil), spans...)
	out = append(out, ArchiveSpan{Start: start, End: end})
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

// validRecordAddress reports whether a is a complete address (offset 0 is valid).
func validRecordAddress(a RecordAddress) bool {
	return a.PartitionKey != "" && a.EntryID != "" && a.ByteOffset >= 0
}
