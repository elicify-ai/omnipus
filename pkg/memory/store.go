package memory

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// StoreReader defines the read-only persistence operations for session
// storage. Split out of Store (interface segregation) so callers that only
// ever read a session's history can depend on the narrower contract.
type StoreReader interface {
	// GetHistory returns the live window messages for a session in insertion order,
	// honoring meta.Skip (evicted lines are excluded). Returns an empty slice
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
}

// StoreWriter defines the mutating persistence operations for session
// storage, plus lifecycle management (Compact, Close). Each method is an
// atomic operation — there is no separate Save() call.
type StoreWriter interface {
	// AddMessage appends a simple text message to a session.
	AddMessage(ctx context.Context, sessionKey, role, content string) error

	// AddFullMessage appends a complete message (with tool calls, etc.) to a session.
	AddFullMessage(ctx context.Context, sessionKey string, msg providers.Message) error

	// TruncateHistory removes all but the last keepLast messages from a session.
	// If keepLast <= 0, all messages are removed.
	TruncateHistory(ctx context.Context, sessionKey string, keepLast int) error

	// SetHistory replaces all messages in a session with the provided history.
	SetHistory(ctx context.Context, sessionKey string, history []providers.Message) error

	// RollbackAppended truncates the JSONL file to targetLines physical lines,
	// setting meta.Count = targetLines and restoring meta.Skip = min(targetSkip,
	// targetLines). The Skip restore is the fix for the mid-turn eviction bug: if
	// windowTrim advanced Skip during a turn and the turn then aborts,
	// RollbackAppended MUST restore Skip to its turn-start value so that
	// GetHistory returns the exact pre-turn live window (SC-001, SC-010).
	//
	// Callers compute: targetSkip = initialArchiveLen - initialHistoryLength
	// (the Skip value at turn start, before any mid-turn evictions).
	//
	// If targetLines >= current Count, the method is a no-op. targetSkip is
	// always clamped: meta.Skip = min(targetSkip, targetLines) so Skip never
	// exceeds the new Count.
	RollbackAppended(ctx context.Context, sessionKey string, targetLines, targetSkip int) error

	// Compact reclaims storage by physically removing logically truncated
	// data. Backends that do not accumulate dead data may return nil.
	//
	// context-paging: MUST NOT be called from any Save path — it destroys
	// the recall archive (FR-005). Test-only; no production caller.
	Compact(ctx context.Context, sessionKey string) error

	// Close releases any resources held by the store.
	Close() error
}

// Store defines an interface for persistent session storage, composed of
// StoreReader (read-only operations) and StoreWriter (mutating operations
// plus lifecycle). Each method is an atomic operation — there is no separate
// Save() call.
//
// Kept as a single composed name (rather than requiring every caller to
// depend on both halves separately) because the sole production consumer
// (JSONLBackend, pkg/session/jsonl_backend.go) interleaves reads and writes
// across nearly every method — narrowing that call site would not reduce its
// actual coupling to the backend, just add ceremony.
//
// This is an intentional composition of the two already-segregated
// StoreReader/StoreWriter interfaces above (the split is the actual fix for
// golangci-lint's interfacebloat 10-method threshold on the two behavioral
// halves); embedding them back together here does not itself re-trip the
// linter, since interfacebloat counts an interface's own directly-declared
// method signatures, not methods promoted through embedding.
type Store interface {
	StoreReader
	StoreWriter
}
