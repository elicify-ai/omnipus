package session

import (
	"context"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// JSONLBackend adapts a memory.Store into the SessionStore interface.
// Write errors are logged rather than returned, matching the fire-and-forget
// contract of SessionManager that the agent loop relies on.
type JSONLBackend struct {
	store memory.Store
}

// NewJSONLBackend wraps a memory.Store for use as a SessionStore.
func NewJSONLBackend(store memory.Store) *JSONLBackend {
	return &JSONLBackend{store: store}
}

// ReadArchive implements SessionStore — returns the full archived log including
// evicted (skipped) turns (FR-016). TS==0 on legacy lines is not an error.
func (b *JSONLBackend) ReadArchive(ctx context.Context, key string) ([]memory.ArchivedMessage, error) {
	return b.store.ReadArchive(ctx, key)
}

func (b *JSONLBackend) AddMessage(sessionKey, role, content string) {
	if err := b.store.AddMessage(context.Background(), sessionKey, role, content); err != nil {
		slog.Error("session: add message", "key", sessionKey, "error", err)
	}
}

func (b *JSONLBackend) AddFullMessage(sessionKey string, msg providers.Message) {
	if err := b.store.AddFullMessage(context.Background(), sessionKey, msg); err != nil {
		slog.Error("session: add full message", "key", sessionKey, "error", err)
	}
}

func (b *JSONLBackend) GetHistory(key string) []providers.Message {
	msgs, err := b.store.GetHistory(context.Background(), key)
	if err != nil {
		slog.Error("session: get history", "key", key, "error", err)
		return []providers.Message{}
	}
	return msgs
}

func (b *JSONLBackend) SetHistory(key string, history []providers.Message) {
	if err := b.store.SetHistory(context.Background(), key, history); err != nil {
		slog.Error("session: set history", "key", key, "error", err)
	}
}

func (b *JSONLBackend) TruncateHistory(key string, keepLast int) {
	if err := b.store.TruncateHistory(context.Background(), key, keepLast); err != nil {
		slog.Error("session: truncate history", "key", key, "error", err)
	}
}

// RollbackAppended implements SessionStore — truncates the on-disk archive to
// targetArchiveLen lines and restores meta.Skip = min(targetSkip, targetArchiveLen).
// This fixes the mid-turn eviction bug: if windowTrim advanced Skip during a live
// turn and the turn then aborts, restoring Skip to targetSkip ensures GetHistory
// returns the exact pre-turn live window (SC-001, SC-010).
func (b *JSONLBackend) RollbackAppended(key string, targetArchiveLen, targetSkip int) {
	if err := b.store.RollbackAppended(context.Background(), key, targetArchiveLen, targetSkip); err != nil {
		slog.Error("session: rollback appended", "key", key, "error", err)
	}
}

// Save persists session state. Since the JSONL store fsyncs every write
// immediately, the data is already durable.
//
// context-paging (FR-005): Save does NOT compact the JSONL file. Evicted
// (skipped) lines must remain on disk so recall_conversation can reach them.
// The retention sweep is the sole legitimate deleter of context.jsonl content.
func (b *JSONLBackend) Save(key string) error {
	return nil
}

// Close releases resources held by the underlying store.
func (b *JSONLBackend) Close() error {
	return b.store.Close()
}
