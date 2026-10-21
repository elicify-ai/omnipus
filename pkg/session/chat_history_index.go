// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import "context"

const chatHistoryRecoveryMaxItems = 64

const ChatHistoryIndexDirectory = ".chat-history"
const ChatHistoryIndexFormatVersion = 1

type chatHistoryReconcileResult struct {
	ObservedItems       int
	AppliedItems        int
	ArchiveRecords      int
	DispositionRecords  int
	ExpiredPartitions   int
	Published           bool
	IndexGeneration     string
	RetentionGeneration string
}

// reconcileChatHistoryIndex is a declaration-only recovery seam. It performs
// no reads, writes or publication and never reports a successful no-op.
func (us *UnifiedStore) reconcileChatHistoryIndex(
	ctx context.Context,
	sessionID string,
) (chatHistoryReconcileResult, error) {
	return chatHistoryReconcileResult{}, ErrChatHistoryNotImplemented
}

func (us *UnifiedStore) initEmptyChatHistoryIndexLocked(sessionID string) error {
	return ErrChatHistoryNotImplemented
}

type ChatHistoryIndexBuildOptions struct {
	ScratchRoot string
}

type ChatHistoryIndexBuildSummary struct {
	SessionID        string
	Action           string
	SourceGroupCount int64
	PartitionCount   int
}

// ChatHistoryIndexBuild reserves the opaque offline-build handle. No prepared
// build is returned until the common index engine is implemented.
type ChatHistoryIndexBuild struct {
	summary ChatHistoryIndexBuildSummary
}

func PrepareChatHistoryIndex(
	ctx context.Context,
	sessionDir string,
	opts ChatHistoryIndexBuildOptions,
) (*ChatHistoryIndexBuild, error) {
	return nil, ErrChatHistoryNotImplemented
}

func (b *ChatHistoryIndexBuild) Summary() ChatHistoryIndexBuildSummary {
	return b.summary
}

func (b *ChatHistoryIndexBuild) Publish(ctx context.Context) error {
	return ErrChatHistoryNotImplemented
}

func (b *ChatHistoryIndexBuild) Close() error {
	return ErrChatHistoryNotImplemented
}

func VerifyChatHistoryIndex(ctx context.Context, sessionDir string) error {
	return ErrChatHistoryNotImplemented
}
