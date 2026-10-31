package session

import (
	"bytes"
	"encoding/json"
	"errors"
)

// historyReuseSaved validates only the already captured, bounded canonical
// delta. The reducer writes to a value overlay over the old immutable root,
// never to staged journal nodes: validation must not overwrite a complete
// transaction at the old published end, even when its size exceeds auto-flush.
// Missing mutations remain charged to the same 64-item attempt and are applied
// after the saved root, not replayed over it.
func historyReuseSaved(t *chatHistoryIndexTree, cp, saved historyCheckpoint, items []historyMutation) ([]historyMutation, error) {
	if saved.FormatVersion != cp.FormatVersion || saved.RetentionGeneration != cp.RetentionGeneration || saved.Generation == "" || saved.Root < 0 || saved.Root >= saved.MutationEnd {
		return nil, errors.New("invalid saved history generation")
	}
	validation := &chatHistoryIndexTree{
		ctx: t.ctx, file: t.file, end: cp.MutationEnd, root: cp.Root, sid: t.sid,
		overlay: make(map[string]json.RawMessage),
	}
	expected := historyCloneCheckpoint(cp)
	remaining := make([]historyMutation, 0, len(items))
	for _, item := range items {
		covered := false
		switch {
		case item.record != nil:
			covered = item.address.ByteOffset+int64(len(item.bytes)) <= saved.ArchiveEnds[item.address.PartitionKey]
		case item.disposition != nil:
			covered = item.dispositionRef.Offset+item.dispositionRef.Length <= saved.DispositionEnd
		case item.empty != "":
			// A known-empty earlier partition is covered by the saved
			// transaction only when that transaction already records its end.
			_, covered = saved.ArchiveEnds[item.empty]
		}
		if !covered {
			remaining = append(remaining, item)
			continue
		}
		if err := historyApplyMutations(validation, &expected, []historyMutation{item}); err != nil {
			return nil, err
		}
	}
	if expected.Rank != saved.Rank || !historySameCoverage(expected, saved) {
		return nil, errors.New("saved history transaction does not match canonical suffix")
	}
	savedTree := &chatHistoryIndexTree{ctx: t.ctx, file: t.file, end: saved.MutationEnd, root: saved.Root, sid: t.sid}
	for key, want := range validation.overlay {
		var got json.RawMessage
		ok, err := savedTree.get(key, &got)
		if err != nil {
			return nil, err
		}
		if !ok || !bytes.Equal(want, got) {
			return nil, errors.New("saved history lookup disagrees with canonical mutation")
		}
	}
	t.root = saved.Root
	t.end = saved.MutationEnd
	t.readCache = savedTree.readCache
	return remaining, nil
}
