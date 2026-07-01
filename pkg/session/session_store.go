package session

import (
	"context"

	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// SessionStore defines the persistence operations used by the agent loop.
// Both SessionManager (legacy JSON backend) and JSONLBackend satisfy this
// interface, allowing the storage layer to be swapped without touching the
// agent loop code.
//
// Write methods (Add*, Set*, Truncate*) are fire-and-forget: they do not
// return errors. Implementations should log failures internally. This
// matches the original SessionManager contract that the agent loop relies on.
type SessionStore interface {
	// AddMessage appends a simple role/content message to the session.
	AddMessage(sessionKey, role, content string)
	// AddFullMessage appends a complete message including tool calls.
	AddFullMessage(sessionKey string, msg providers.Message)
	// GetHistory returns the live window messages (post-Skip) for the session.
	GetHistory(key string) []providers.Message
	// ReadArchive returns the FULL archived log for a session from line 0,
	// ignoring meta.Skip. Evicted (skipped) turns are included. Each
	// ArchivedMessage carries the per-line TS written by addMsg (FR-016/FR-017).
	// Legacy lines pre-dating the TS stamp unmarshal with TS==0.
	ReadArchive(ctx context.Context, key string) ([]memory.ArchivedMessage, error)
	// GetSummary returns the conversation summary, or "" if none.
	GetSummary(key string) string
	// SetSummary replaces the conversation summary.
	SetSummary(key, summary string)
	// SetHistory replaces the full message history.
	SetHistory(key string, history []providers.Message)
	// TruncateHistory keeps only the last keepLast messages.
	TruncateHistory(key string, keepLast int)
	// RollbackAppended truncates the on-disk archive to targetArchiveLen physical
	// lines, leaving meta.Skip untouched (clamped if needed). This is the
	// Skip-preserving primitive for undoing messages appended during an aborted
	// turn without destroying the eviction archive. If targetArchiveLen >=
	// current archive line count, it is a no-op.
	RollbackAppended(key string, targetArchiveLen int)
	// Save persists any pending state to durable storage.
	// context-paging: Save MUST NOT compact the JSONL file (FR-005).
	Save(key string) error
	// Close releases resources held by the store.
	Close() error
}
