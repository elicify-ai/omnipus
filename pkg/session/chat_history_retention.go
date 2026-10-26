package session

import (
	"context"
	"path/filepath"
)

type chatHistoryIndexState struct {
	FormatVersion                        int
	IndexGeneration, RetentionGeneration string
	ArchiveEnds                          map[string]int64
	DispositionEnd, MutationEnd          int64
	RetainedPartitions                   []string
}

// Only the opaque position and private acceptance rank survive expiry.
type chatHistoryTombstone struct {
	Position HistoryPosition
	Rank     uint64
}

func (us *UnifiedStore) readChatHistoryIndexState(ctx context.Context, sessionID string) (chatHistoryIndexState, error) {
	if err := validateSessionID(sessionID); err != nil {
		return chatHistoryIndexState{}, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	cp, err := historyLoadCheckpoint(ctx, filepath.Join(us.baseDir, sessionID), sessionID)
	if err != nil {
		return chatHistoryIndexState{}, err
	}
	t, err := historyOpenTree(ctx, filepath.Join(us.baseDir, sessionID), cp, false)
	if err != nil {
		return chatHistoryIndexState{}, historyUnavailable("index_inconsistent", err)
	}
	if err = t.file.Close(); err != nil {
		return chatHistoryIndexState{}, err
	}
	return chatHistoryIndexState{FormatVersion: cp.FormatVersion, IndexGeneration: cp.Generation, RetentionGeneration: cp.RetentionGeneration, ArchiveEnds: cp.ArchiveEnds, DispositionEnd: cp.DispositionEnd, MutationEnd: cp.MutationEnd, RetainedPartitions: historyRetained(cp)}, nil
}
func (us *UnifiedStore) chatHistoryRetentionGeneration(ctx context.Context, sessionID string) (string, error) {
	state, err := us.readChatHistoryIndexState(ctx, sessionID)
	return state.RetentionGeneration, err
}
func (us *UnifiedStore) lookupChatHistoryTombstone(ctx context.Context, sessionID string, position HistoryPosition) (chatHistoryTombstone, bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return chatHistoryTombstone{}, false, err
	}
	if _, err := ParseHistoryPosition(string(position)); err != nil {
		return chatHistoryTombstone{}, false, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	dir := filepath.Join(us.baseDir, sessionID)
	cp, err := historyLoadCheckpoint(ctx, dir, sessionID)
	if err != nil {
		return chatHistoryTombstone{}, false, err
	}
	t, err := historyOpenTree(ctx, dir, cp, false)
	if err != nil {
		return chatHistoryTombstone{}, false, err
	}
	defer t.file.Close()
	var rank uint64
	ok, err := t.get("p/"+string(position), &rank)
	if err != nil || !ok {
		return chatHistoryTombstone{}, false, err
	}
	_, present, err := historyReadRecordMeta(t, rank)
	if err != nil || present {
		return chatHistoryTombstone{}, false, err
	}
	return chatHistoryTombstone{Position: position, Rank: rank}, true, nil
}
