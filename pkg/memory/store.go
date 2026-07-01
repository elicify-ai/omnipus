package memory

import (
	"context"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// Store defines an interface for persistent session storage.
// Each method is an atomic operation — there is no separate Save() call.
type Store interface {
	// AddMessage appends a simple text message to a session.
	AddMessage(ctx context.Context, sessionKey, role, content string) error

	// AddFullMessage appends a complete message (with tool calls, etc.) to a session.
	AddFullMessage(ctx context.Context, sessionKey string, msg providers.Message) error

	// GetHistory returns the live window messages for a session in insertion order,
	// honouring meta.Skip (evicted lines are excluded). Returns an empty slice
	// (not nil) if the session does not exist.
	GetHistory(ctx context.Context, sessionKey string) ([]providers.Message, error)

	// ReadArchive returns the FULL archived log for a session from line 0,
	// ignoring meta.Skip. Each ArchivedMessage carries the per-line write
	// timestamp stamped by addMsg (FR-017). Legacy lines pre-dating the
	// timestamp stamp unmarshal with TS==0 — callers must treat TS==0 as
	// "unknown/earlier" and must not error on it.
	//
	// Use ReadArchive (not GetHistory) whenever evicted turns must be
	// reachable — e.g. recall_conversation and the breadcrumb builder (FR-016).
	ReadArchive(ctx context.Context, sessionKey string) ([]ArchivedMessage, error)

	// GetSummary returns the conversation summary for a session.
	// Returns an empty string if no summary exists.
	GetSummary(ctx context.Context, sessionKey string) (string, error)

	// SetSummary updates the conversation summary for a session.
	SetSummary(ctx context.Context, sessionKey, summary string) error

	// TruncateHistory removes all but the last keepLast messages from a session.
	// If keepLast <= 0, all messages are removed.
	TruncateHistory(ctx context.Context, sessionKey string, keepLast int) error

	// SetHistory replaces all messages in a session with the provided history.
	SetHistory(ctx context.Context, sessionKey string, history []providers.Message) error

	// Compact reclaims storage by physically removing logically truncated
	// data. Backends that do not accumulate dead data may return nil.
	//
	// context-paging: MUST NOT be called from any Save path — it destroys
	// the recall archive (FR-005). Test-only; no production caller.
	Compact(ctx context.Context, sessionKey string) error

	// Close releases any resources held by the store.
	Close() error
}
