// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// chatHistoryPublicationEvent declares private fault-injection metadata, never
// a log or wire payload. The operational engine does not invoke this hook yet.
type chatHistoryPublicationEvent struct {
	SessionID       string
	Operation       string
	Point           string
	Item            int
	IndexGeneration string
	Address         *ArchiveAddress
	PartitionKey    string
}

// chatHistoryPublicationHook is nil in production. The agreed Point vocabulary
// is empty_checkpoint_ready, canonical_synced, partition_unlinked,
// mutation_written, mutation_synced, lookups_staged, generation_verified,
// before_checkpoint_publish, checkpoint_published, root_installed,
// before_identity_publish, identity_published, before_torn_tail_trim and
// torn_tail_trimmed. Publication wiring belongs to the later implementation.
var chatHistoryPublicationHook func(chatHistoryPublicationEvent) error

// chatHistoryWriteCheckpoint declares the common checked durability dependency;
// no checkpoint writer calls it in this declaration-only step.
var chatHistoryWriteCheckpoint = fileutil.WriteFileAtomicSyncDir

// chatHistoryReadEvent declares actual-I/O instrumentation for the future
// reader. No synthetic reads or hook events are emitted by the current stubs.
type chatHistoryReadEvent struct {
	SessionID string
	Scope     string
	Purpose   string
	Phase     string
	Cut       ChatHistoryReadCut
	Stream    string
	Address   *ArchiveAddress
	Offset    int64
	Bytes     int64
}

// chatHistoryReadHook is nil in production. The agreed Phase vocabulary is
// cut_captured, record_opened, record_read and record_closed.
var chatHistoryReadHook func(context.Context, chatHistoryReadEvent) error
