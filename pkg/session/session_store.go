package session

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// SessionReader defines the read-only operations used by the agent loop.
// Split out of SessionStore (interface segregation) so callers that only
// ever read a session's history/summary can depend on the narrower contract.
type SessionReader interface {
	// GetHistory returns the live window messages (post-Skip) for the session.
	GetHistory(key string) []providers.Message
	// ReadArchive returns the FULL archived log for a session from line 0,
	// ignoring meta.Skip. Evicted (skipped) turns are included. Each
	// ArchivedMessage carries the per-line TS written by addMsg (FR-016/FR-017).
	// Legacy lines pre-dating the TS stamp unmarshal with TS==0.
	ReadArchive(ctx context.Context, key string) ([]memory.ArchivedMessage, error)
	// GetSummary returns the conversation summary, or "" if none.
	GetSummary(key string) string
}

// SessionWriter defines the mutating persistence operations used by the
// agent loop, plus lifecycle management (Save, Close). Write methods (Add*,
// Set*, Truncate*) are fire-and-forget: they do not return errors.
// Implementations should log failures internally. This matches the original
// SessionManager contract that the agent loop relies on.
type SessionWriter interface {
	// AddMessage appends a simple role/content message to the session.
	AddMessage(sessionKey, role, content string)
	// AddFullMessage appends a complete message including tool calls.
	AddFullMessage(sessionKey string, msg providers.Message)
	// SetSummary replaces the conversation summary.
	SetSummary(key, summary string)
	// SetHistory replaces the full message history.
	SetHistory(key string, history []providers.Message)
	// TruncateHistory keeps only the last keepLast messages.
	TruncateHistory(key string, keepLast int)
	// RollbackAppended truncates the on-disk archive to targetArchiveLen physical
	// lines and restores meta.Skip = min(targetSkip, targetArchiveLen). The Skip
	// restore is required to fix the mid-turn eviction bug: if windowTrim advanced
	// Skip during a live turn and the turn then aborts, the old clamp-forward
	// (Skip = Count) would shrink the visible window below the pre-turn size.
	// Callers compute: targetSkip = initialArchiveLen - initialHistoryLength.
	// If targetArchiveLen >= current archive line count, the file is not rewritten
	// but Skip is still restored if it has drifted.
	RollbackAppended(key string, targetArchiveLen, targetSkip int)
	// Save persists any pending state to durable storage.
	// context-paging: Save MUST NOT compact the JSONL file (FR-005).
	Save(key string) error
	// Close releases resources held by the store.
	Close() error
}

// SessionStore defines the persistence operations used by the agent loop,
// composed of SessionReader (read-only) and SessionWriter (mutating +
// lifecycle). SessionManager (legacy JSON backend), JSONLBackend, and
// UnifiedStore all satisfy this interface, allowing the storage layer to be
// swapped without touching the agent loop code.
//
// Kept as a single composed name because AgentInstance.Sessions and
// turnState.session hold the full interface and the agent loop interleaves
// reads and writes across many call sites (loop.go, turn.go) — narrowing
// every one of those would be an invasive refactor for no behavioral gain.
//
// This is an intentional composition of the two already-segregated
// SessionReader/SessionWriter interfaces above (the split is the actual fix
// for golangci-lint's interfacebloat 10-method threshold on the two
// behavioral halves); embedding them back together here does not itself
// re-trip the linter, since interfacebloat counts an interface's own
// directly-declared method signatures, not methods promoted through
// embedding.
type SessionStore interface {
	SessionReader
	SessionWriter
}
