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
	var existing ArchiveAddress
	var conflict bool
	if err = archive.ScanAll(func(addr ArchiveAddress, rec ArchiveRecord) bool {
		if rec.ID != id {
			return true
		}
		existing = addr
		conflict = rec.ViewMembership != ViewMembershipChat || rec.ModelMessage == nil || rec.Content != body
		return false
	}); err != nil {
		return ArchiveAddress{}, err
	}
	if existing.EntryID != "" {
		if conflict {
			return ArchiveAddress{}, fmt.Errorf("saved input %q already exists with different content or membership", id)
		}
		return existing, nil
	}
	payload, err := EncodeModelPayload(providers.Message{Role: "user", Content: body})
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
	return addr, nil
}

// SavedChatInput resolves the durable address and whether it has already been
// placed. The scan includes evicted references: eviction is not permission to
// place the same logical event again after a process restart.
func (us *UnifiedStore) SavedChatInput(sessionID, id string) (ArchiveAddress, bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, false, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return ArchiveAddress{}, false, err
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return ArchiveAddress{}, false, err
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

// PendingChatInputs returns only saved wake inputs that have neither a model
// reference nor a consumed marker (including a final consumed by polling).
// They are placed by the next legitimate turn, never by an unanswered card.
func (us *UnifiedStore) PendingChatInputs(ctx context.Context, sessionID string) ([]TranscriptEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	if _, err := us.readMetaLocked(sessionID); err != nil {
		return nil, err
	}
	archive, err := us.archiveStore(sessionID)
	if err != nil {
		return nil, err
	}
	var inputs []TranscriptEntry
	consumed := make(map[string]bool)
	err = archive.ScanAll(func(_ ArchiveAddress, rec ArchiveRecord) bool {
		if rec.ModelRef != nil {
			consumed[rec.ModelRef.EntryID] = true
		}
		if strings.HasPrefix(rec.ID, "consumed-") {
			consumed["saved-input-"+strings.TrimPrefix(rec.ID, "consumed-")] = true
		}
		if strings.HasPrefix(rec.ID, "saved-input-") && rec.ModelMessage != nil && rec.ViewMembership == ViewMembershipChat {
			inputs = append(inputs, rec.TranscriptEntry)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	pending := inputs[:0]
	for _, entry := range inputs {
		if !consumed[entry.ID] {
			pending = append(pending, entry)
		}
	}
	return pending, nil
}
