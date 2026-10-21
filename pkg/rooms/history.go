// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"sort"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// History limits (#1332, ROOM-HISTORY-PAGING-1332-20261018 D3).
const (
	HistoryDefaultLimit = 100
	HistoryMaxLimit     = 1000
)

// History answers GET /api/v1/rooms/{id}/messages (#1332, ruling D2/D4/D5):
// the newest min(limit, eligible) entries, oldest to newest, where an entry is
// eligible when beforeSeq is nil or entry.seq < *beforeSeq (exclusive cursor).
// has_older is true exactly when another eligible entry precedes the page;
// next_before_seq is then the first returned entry's seq and omitted
// otherwise.
//
// The read goes through the same open path as Get and Attach (existence,
// workspace, deletion, reconciliation) and takes its page from one view of
// the room under the room lock. It never appends, publishes or moves a
// cursor. A cursor greater than the newest ENTRY seq (0 for an empty room)
// is refused, never clamped; the room's stream position, which state and
// metadata changes also advance, is not the bound. limit must already be in
// [1, HistoryMaxLimit]; the REST layer validates it before the room is
// opened (ruling D3 order).
func (s *Service) History(roomID string, beforeSeq *int64, limit int) (generated.RoomHistoryResponse, error) {
	if limit < 1 || limit > HistoryMaxLimit {
		return generated.RoomHistoryResponse{}, badRequest("limit must be an integer from 1 to 1000")
	}
	r, _, err := s.openRoom(roomID)
	if err != nil {
		return generated.RoomHistoryResponse{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleted {
		return generated.RoomHistoryResponse{}, notFound("no such group chat")
	}
	return historyPage(r, beforeSeq, limit)
}

// historyPage selects one page from r.entries (seq order). The caller holds
// r.mu.
func historyPage(r *room, beforeSeq *int64, limit int) (generated.RoomHistoryResponse, error) {
	end := len(r.entries)
	if beforeSeq != nil {
		var newest int64
		if end > 0 {
			newest = r.entries[end-1].Entry.Seq
		}
		if *beforeSeq > newest {
			return generated.RoomHistoryResponse{}, badRequest("before_seq must not be greater than the newest entry seq")
		}
		end = sort.Search(len(r.entries), func(i int) bool { return r.entries[i].Entry.Seq >= *beforeSeq })
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	out := generated.RoomHistoryResponse{Entries: make([]generated.RoomEntry, 0, end-start)}
	for _, se := range r.entries[start:end] {
		out.Entries = append(out.Entries, entryWire(r, se))
	}
	if start > 0 {
		out.HasOlder = true
		next := r.entries[start].Entry.Seq
		out.NextBeforeSeq = &next
	}
	return out, nil
}
