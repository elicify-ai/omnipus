// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import "context"

type chatHistoryIndexState struct {
	FormatVersion       int
	IndexGeneration     string
	RetentionGeneration string
	ArchiveEnds         map[string]int64
	DispositionEnd      int64
	MutationEnd         int64
	RetainedPartitions  []string
}

// chatHistoryTombstone contains only the occurrence token and private rank.
type chatHistoryTombstone struct {
	Position HistoryPosition
	Rank     uint64
}

// These operational lookup stubs perform no I/O and cannot seed or reconcile
// an index. They explicitly refuse until the bounded lookup is implemented.
func (us *UnifiedStore) readChatHistoryIndexState(
	ctx context.Context, sessionID string,
) (chatHistoryIndexState, error) {
	return chatHistoryIndexState{}, ErrChatHistoryNotImplemented
}

func (us *UnifiedStore) chatHistoryRetentionGeneration(
	ctx context.Context, sessionID string,
) (string, error) {
	return "", ErrChatHistoryNotImplemented
}

func (us *UnifiedStore) lookupChatHistoryTombstone(
	ctx context.Context, sessionID string, position HistoryPosition,
) (chatHistoryTombstone, bool, error) {
	return chatHistoryTombstone{}, false, ErrChatHistoryNotImplemented
}
