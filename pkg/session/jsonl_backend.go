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

// ScanArchive streams the archive for key line by line, stopping when fn
// returns false (ADR-066 FR-024 / B-31b — recall by tool_call_id). When the
// wrapped memory.Store can stream (memory.JSONLStore), the scan is a true
// stream; otherwise it degrades to a ReadArchive iteration with identical
// indexing and stop semantics.
func (b *JSONLBackend) ScanArchive(
	ctx context.Context, key string, fn func(idx int, msg memory.ArchivedMessage) bool,
) error {
	if sc, ok := b.store.(interface {
		ScanArchive(ctx context.Context, sessionKey string, fn func(idx int, msg memory.ArchivedMessage) bool) error
	}); ok {
		return sc.ScanArchive(ctx, key, fn)
	}
	msgs, err := b.store.ReadArchive(ctx, key)
	if err != nil {
		return err
	}
	for i, m := range msgs {
		if !fn(i, m) {
			return nil
		}
	}
	return nil
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
// targetArchiveLen lines, restores meta.Skip = min(targetSkip, targetArchiveLen)
// and restores the projection state to the turn-start emptiedSet, in one meta
// write (ADR-066 FR-020). The Skip restore fixes the mid-turn eviction bug: if
// windowTrim advanced Skip during a live turn and the turn then aborts,
// restoring Skip to targetSkip ensures GetHistory returns the exact pre-turn
// live window (SC-001, SC-010); the projection restore does the same for
// mid-turn empties.
func (b *JSONLBackend) RollbackAppended(key string, targetArchiveLen, targetSkip int, emptiedSet memory.ProjectionSet) {
	if err := b.store.RollbackAppended(context.Background(), key, targetArchiveLen, targetSkip, emptiedSet); err != nil {
		slog.Error("session: rollback appended", "key", key, "error", err)
	}
}

// Projection implements SessionStore.
func (b *JSONLBackend) Projection(key string) memory.ProjectionMeta {
	pm, err := b.store.GetProjection(context.Background(), key)
	if err != nil {
		slog.Error("session: get projection", "key", key, "error", err)
		return memory.ProjectionMeta{Entries: memory.ProjectionSet{}}
	}
	return pm
}

// SetProjectionState implements SessionStore.
func (b *JSONLBackend) SetProjectionState(key string, pk memory.ProjectionKey, state memory.ProjectionState) {
	if err := b.store.SetProjectionState(context.Background(), key, pk, state); err != nil {
		slog.Error("session: set projection state", "key", key, "error", err)
	}
}

// MarkHydrated implements SessionStore.
func (b *JSONLBackend) MarkHydrated(key string) {
	if err := b.store.MarkHydrated(context.Background(), key); err != nil {
		slog.Error("session: mark hydrated", "key", key, "error", err)
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
