package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// SaveChatInput saves an addressed, chat-only payload, without assigning a
// model ordinal. PlaceSavedInput is the separate consumption-fence operation.
// Retrying an id returns its original address; changing its bytes is refused.
// The saved input is also entered in the session's content-free pending
// directory (saved_input_index.go), so ordinary turns can find it without
// reading the archive.
func (us *UnifiedStore) SaveChatInput(sessionID, id, agentID, body string) (ArchiveAddress, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, err
	}
	if id == "" || strings.TrimSpace(body) == "" {
		return ArchiveAddress{}, fmt.Errorf("saved input requires an id and body")
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("saved input: recipient metadata: %w", err)
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return ArchiveAddress{}, err
	}
	idx, err := us.loadSavedInputIndexLocked(sessionID)
	if err != nil {
		return ArchiveAddress{}, err
	}
	var existing ArchiveAddress
	var conflict, indexed, consumed bool
	if entry, ok := idx[id]; ok {
		rec, readErr := archive.ReadAt(entry.Address)
		if readErr != nil {
			return ArchiveAddress{}, fmt.Errorf("saved input %q: read indexed record: %w", id, readErr)
		}
		existing, indexed = entry.Address, true
		conflict = rec.ID != id || rec.ViewMembership != ViewMembershipChat || rec.ModelMessage == nil || rec.Content != body
	} else if err = archive.ScanAll(func(addr ArchiveAddress, rec ArchiveRecord) bool {
		// Wake path only (once per outcome): a retried save of an input that
		// was already placed and left the pending directory must not append a
		// second copy. The scan also learns whether it was consumed — placed
		// into model history, or received by a poll's consumed marker.
		switch {
		case rec.ID == id:
			existing = addr
			conflict = rec.ViewMembership != ViewMembershipChat || rec.ModelMessage == nil || rec.Content != body
		case rec.ModelRef != nil && rec.ModelRef.EntryID == id:
			consumed = true
		case rec.ID == "consumed-"+strings.TrimPrefix(id, "saved-input-"):
			consumed = true
		}
		return true
	}); err != nil {
		return ArchiveAddress{}, err
	}
	if existing.EntryID != "" {
		if conflict {
			return ArchiveAddress{}, fmt.Errorf("saved input %q already exists with different content or membership", id)
		}
		if !indexed && !consumed {
			// R2-1: the archive append landed but the pending entry did not
			// (a failed index write, or a stop between the two writes). The
			// input is still unconsumed, so restore its pending entry before
			// reporting the save as complete; otherwise no ordinary fence
			// would ever find it again.
			fromOrdinal, countErr := archive.OrdinalCount()
			if countErr != nil {
				return ArchiveAddress{}, countErr
			}
			idx[id] = savedInputIndexEntry{Address: existing, FromOrdinal: fromOrdinal, Seq: time.Now().UnixNano()}
			if saveErr := us.saveSavedInputIndexLocked(sessionID, idx); saveErr != nil {
				return ArchiveAddress{}, saveErr
			}
		}
		return existing, nil
	}
	payload, err := EncodeModelPayload(providers.Message{Role: "user", Content: body})
	if err != nil {
		return ArchiveAddress{}, err
	}
	fromOrdinal, err := archive.OrdinalCount()
	if err != nil {
		return ArchiveAddress{}, err
	}
	entry := TranscriptEntry{ID: id, Role: "user", Content: body, AgentID: agentID,
		Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipChat}
	addr, err := archive.Append(ArchiveRecord{TranscriptEntry: entry, ModelMessage: &payload,
		Source: &EntrySource{Kind: "agent"}})
	if err != nil {
		return ArchiveAddress{}, err
	}
	accumulateEntryStats(&meta.Stats, entry)
	meta.UpdatedAt = entry.Timestamp
	us.u6MarkStatsDirtyLocked(sessionID, meta)
	idx[id] = savedInputIndexEntry{Address: addr, FromOrdinal: fromOrdinal, Seq: time.Now().UnixNano()}
	if err := us.saveSavedInputIndexLocked(sessionID, idx); err != nil {
		return ArchiveAddress{}, err
	}
	return addr, nil
}

// SavedChatInput resolves the durable address and whether it has already been
// placed. A pending input is answered from the content-free directory. An id
// that is not pending falls back to the archive (wake paths only, once per
// outcome): eviction is not permission to place the same logical event again
// after a process restart.
func (us *UnifiedStore) SavedChatInput(sessionID, id string) (ArchiveAddress, bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, false, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return ArchiveAddress{}, false, err
	}
	idx, err := us.loadSavedInputIndexLocked(sessionID)
	if err != nil {
		return ArchiveAddress{}, false, err
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return ArchiveAddress{}, false, err
	}
	if entry, ok := idx[id]; ok {
		// The pending entry is a hint, the archive is the authority: a placement
		// whose index retirement failed (crash or disk error after the model
		// reference landed) is placed, never offered again. The check reads only
		// the rows written since the save.
		placed, placedErr := savedSourceAlreadyPlaced(archive, entry.Address, entry.FromOrdinal)
		if placedErr != nil || !placed {
			return entry.Address, false, placedErr
		}
		if retireErr := us.retireSavedInputLocked(sessionID, id); retireErr != nil {
			return ArchiveAddress{}, false, fmt.Errorf("saved input %q: retire placed input: %w", id, retireErr)
		}
		return entry.Address, true, nil
	}
	var addr ArchiveAddress
	placed := false
	err = archive.ScanAll(func(a ArchiveAddress, rec ArchiveRecord) bool {
		if rec.ID == id && rec.ViewMembership == ViewMembershipChat && rec.ModelMessage != nil {
			addr = a
		}
		if rec.ModelRef != nil && rec.ModelRef.EntryID == id {
			placed = true
		}
		return true
	})
	return addr, placed, err
}

// PendingChatInputs returns the saved wake inputs still in the pending
// directory, in arrival order. A session with none reads no archive record;
// otherwise only each pending record's own address is read.
func (us *UnifiedStore) PendingChatInputs(ctx context.Context, sessionID string) ([]TranscriptEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	idx, err := us.loadSavedInputIndexLocked(sessionID)
	if err != nil || len(idx) == 0 {
		return nil, err
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return nil, err
	}
	pending := make([]TranscriptEntry, 0, len(idx))
	for _, id := range sortedSavedInputIDs(idx) {
		rec, readErr := archive.ReadAt(idx[id].Address)
		if readErr != nil {
			return nil, fmt.Errorf("saved input %q: read pending record: %w", id, readErr)
		}
		pending = append(pending, rec.TranscriptEntry)
	}
	return pending, nil
}

// savedInputFromOrdinal returns the bounded once-only search start for an
// indexed saved input, or ok=false for a source the directory does not track.
func (us *UnifiedStore) savedInputFromOrdinalLocked(sessionID string, source ArchiveAddress) (int, bool, error) {
	idx, err := us.loadSavedInputIndexLocked(sessionID)
	if err != nil {
		return 0, false, err
	}
	for _, entry := range idx {
		if entry.Address == source {
			return entry.FromOrdinal, true, nil
		}
	}
	return 0, false, nil
}
