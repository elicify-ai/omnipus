package memory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	out.Projection.TranscriptLine = make(map[ProjectionKey]int, len(w.Projection.TranscriptLine))
	for k, v := range w.Projection.TranscriptLine {
		out.Projection.TranscriptLine[k] = v
	}
	return out
}

// WindowSnapshot binds archive identities and metadata under one session lock.
type WindowSnapshot struct {
	State   WindowState
	Archive []ArchivedMessage
}

func windowState(meta sessionMeta) WindowState {
	return WindowState{Skip: meta.Skip, Count: meta.Count, AnchorLine: meta.AnchorLine,
		Projection: projectionMeta(meta)}.Clone()
}

func projectionMeta(meta sessionMeta) ProjectionMeta {
	pm := ProjectionMeta{Entries: projectionFromEntries(meta.Projection),
		Hydrated: meta.Hydrated, SourceRunes: make(map[ProjectionKey]int),
		TranscriptLine: make(map[ProjectionKey]int)}
	for _, e := range meta.Projection {
		k := ProjectionKey{ToolCallID: e.ToolCallID, ArchiveLine: e.ArchiveLine}
		if e.SourceRunes != nil && *e.SourceRunes >= 0 && validProjectionState(e.State) {
			pm.SourceRunes[k] = *e.SourceRunes
		}
		if e.TranscriptLine != nil && *e.TranscriptLine >= 0 && k.ToolCallID != "" && k.ArchiveLine >= 0 {
			pm.TranscriptLine[k] = *e.TranscriptLine
		}
	}
	return pm
}

func entriesWithLimits(pm ProjectionMeta) []projectionEntry {
	keys := pm.Entries.Clone()
	for k := range pm.TranscriptLine {
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
		if n, ok := pm.TranscriptLine[k]; ok {
			v := n
			entries[i].TranscriptLine = &v
		}
	}
	return entries
}

func applyWindow(meta *sessionMeta, state WindowState) {
	state = state.Clone()
	meta.Skip, meta.Count, meta.AnchorLine = state.Skip, state.Count, state.AnchorLine
	meta.Projection = entriesWithLimits(state.Projection)
	meta.Hydrated = state.Projection.Hydrated
	meta.UpdatedAt = time.Now()
}

func WindowHistory(snap WindowSnapshot) ([]providers.Message, []int) {
	out := make([]providers.Message, 0, len(snap.Archive)-min(snap.State.Skip, len(snap.Archive))+1)
	lines := make([]int, 0, cap(out))
	if a := snap.State.AnchorLine; a != nil && *a >= 0 && *a < snap.State.Skip && *a < len(snap.Archive) {
		out = append(out, snap.Archive[*a].Message)
		lines = append(lines, *a)
	}
	for i := max(0, snap.State.Skip); i < len(snap.Archive); i++ {
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
	return WindowSnapshot{State: windowState(meta), Archive: archive}, nil
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
	for k, line := range after.Projection.TranscriptLine {
		if line < 0 || k.ToolCallID == "" || k.ArchiveLine < after.Skip || k.ArchiveLine >= len(archive) {
			return errors.New("memory: invalid transcript projection identity")
		}
		if source := archive[k.ArchiveLine]; source.Role != "tool" || source.ToolCallID != k.ToolCallID {
			return errors.New("memory: transcript identity does not address its archived tool result")
		}
	}
	return nil
}

// RollbackWindow is RollbackAppended's exact-snapshot path. Even without an
// append it restores the actual cursor, anchor and source limits in one write.
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
	applyWindow(&meta, start)
	if err := s.writeMeta(key, meta); err != nil {
		return err
	}
	if len(archive) == start.Count {
		return nil
	}
	return s.rewriteJSONL(key, archive[:start.Count])
}
