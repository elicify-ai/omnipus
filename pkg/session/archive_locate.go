// archive_locate.go: tool-result lookup through the ordinal index — the
// tool_call_id recall path of session-core U2 (caller-migration step 7).
//
// It reads index rows only (ids, roles, turn counters — no provider content), so
// finding "the most recent result for this id" or "the result at this archive
// line" costs rows, not archive records, and the single located slot is then
// read through ReadModelSlots.
package session

import (
	"context"
	"fmt"
)

// ToolResultLocation is where a tool result sits in model-slot order.
type ToolResultLocation struct {
	Found bool
	// Ordinal is the stable archive_line of the result.
	Ordinal int
	// Issuer is the ordinal of the assistant slot that declared the call, or -1
	// when the index holds none before the result.
	Issuer int
	// TurnNum is 1 + the user messages strictly before the result (FR-018).
	TurnNum int
}

// LocateToolResult finds a tool result by call id: the one at archive line
// wantLine when wantLine >= 0 (found only if that slot is a result for id), else
// the most recent result carrying id (FR-025).
func (us *UnifiedStore) LocateToolResult(ctx context.Context, key, id string, wantLine int) (ToolResultLocation, error) {
	return us.backend.locateToolResult(ctx, key, id, wantLine)
}

func (b *archiveBackend) locateToolResult(ctx context.Context, key, id string, wantLine int) (ToolResultLocation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	store, err := b.store(key)
	if err != nil {
		return ToolResultLocation{}, err
	}
	n, err := store.OrdinalCount()
	if err != nil {
		return ToolResultLocation{}, err
	}
	notFound := ToolResultLocation{Ordinal: -1, Issuer: -1}
	if wantLine >= 0 {
		if wantLine >= n {
			return notFound, nil
		}
		rows, err := store.readOrdinalRows(wantLine, wantLine+1)
		if err != nil {
			return ToolResultLocation{}, err
		}
		if rows[0].Role != "tool" || rows[0].ToolCallID != id {
			return notFound, nil
		}
		return b.withIssuerLocked(ctx, store, rows[0], id)
	}
	for hi := n; hi > 0; hi -= scanChunk {
		if err := ctx.Err(); err != nil {
			return ToolResultLocation{}, err
		}
		lo := max(0, hi-scanChunk)
		rows, err := store.readOrdinalRows(lo, hi)
		if err != nil {
			return ToolResultLocation{}, err
		}
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].Role == "tool" && rows[i].ToolCallID == id {
				return b.withIssuerLocked(ctx, store, rows[i], id)
			}
		}
	}
	return notFound, nil
}

// withIssuerLocked completes a located result with its issuing assistant: the
// closest preceding assistant slot that declared id.
func (b *archiveBackend) withIssuerLocked(ctx context.Context, store *ArchiveDayStore, result ordinalRow, id string) (ToolResultLocation, error) {
	loc := ToolResultLocation{Found: true, Ordinal: result.Ordinal, Issuer: -1, TurnNum: result.UserTurn + 1}
	for hi := result.Ordinal; hi > 0; hi -= scanChunk {
		if err := ctx.Err(); err != nil {
			return ToolResultLocation{}, err
		}
		lo := max(0, hi-scanChunk)
		rows, err := store.readOrdinalRows(lo, hi)
		if err != nil {
			return ToolResultLocation{}, fmt.Errorf("locate issuer: %w", err)
		}
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].Role != "assistant" {
				continue
			}
			for _, call := range rows[i].CallIDs {
				if call == id {
					loc.Issuer = rows[i].Ordinal
					return loc, nil
				}
			}
		}
	}
	return loc, nil
}
