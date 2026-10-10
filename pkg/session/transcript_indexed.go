package session

import (
	"fmt"
	"time"
)

// AppendTranscriptAddressed shares the ordinary strict appender and returns the
// exact ArchiveAddress of the record it wrote: the partition it landed in, its
// byte offset and its id. Callers that must correct or project the record later
// (a tool call's status, a capped result) keep this address and pass it to
// SettleToolCall / ProjectToolCalls — there is no index to guess and no scan.
func (us *UnifiedStore) AppendTranscriptAddressed(sessionID string, entry TranscriptEntry) (ArchiveAddress, error) {
	return us.appendTranscript(sessionID, entry, "append transcript strict", nil)
}

// appendTranscript is the single locked transcript-append body shared by
// AppendTranscript, AppendTranscriptStrict and AppendTranscriptAddressed.
// provenance is nil for every ordinary caller; when non-nil
// (AppendTranscriptWithProvenance only) the authenticated principal is written
// INTO the same line as the message (Decision A: the trusted source is a
// private member of the content record), so the message and its source are
// saved together or neither is — there is no second file, no write-phase
// marker and no rewrite. See MessageProvenance for the trust contract.
func (us *UnifiedStore) appendTranscript(sessionID string, entry TranscriptEntry, what string, provenance *MessageProvenance) (ArchiveAddress, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ArchiveAddress{}, err
	}
	if err := validateContextWindowNotice(sessionID, entry); err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: %s: %w", what, err)
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	return us.appendTranscriptLocked(sessionID, entry, what, provenance)
}

// AppendTranscriptOnce appends entry unless the transcript already holds an
// entry with the same non-empty ID. The check and the append run under the
// same session-shard hold, so concurrent callers with one ID append exactly
// once. The bool reports whether THIS call wrote the entry.
func (us *UnifiedStore) AppendTranscriptOnce(sessionID string, entry TranscriptEntry) (bool, error) {
	if entry.ID == "" {
		return false, fmt.Errorf("unified_store: append transcript once: entry ID is required")
	}
	if err := validateSessionID(sessionID); err != nil {
		return false, err
	}
	if err := validateContextWindowNotice(sessionID, entry); err != nil {
		return false, fmt.Errorf("unified_store: append transcript once: %w", err)
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	existing, err := us.ReadTranscript(sessionID)
	if err != nil {
		return false, fmt.Errorf("unified_store: append transcript once: %w", err)
	}
	for _, e := range existing {
		if e.ID == entry.ID {
			return false, nil
		}
	}
	if _, err := us.appendTranscriptLocked(sessionID, entry, "append transcript once", nil); err != nil {
		return false, err
	}
	return true, nil
}

// appendTranscriptLocked is appendTranscript's body; the caller holds the
// session shard.
func (us *UnifiedStore) appendTranscriptLocked(sessionID string, entry TranscriptEntry, what string, provenance *MessageProvenance) (ArchiveAddress, error) {
	// Existence is resolved before any write, including the append's MkdirAll.
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: %s: session %q does not exist: %w", what, sessionID, err)
	}
	// C-ARCHIVE (FR-004, effects design D3): a transcript append is a CHAT
	// record, fixed by the writer API. A caller cannot widen it to the model
	// view; model content has its own checked append.
	if entry.ViewMembership != "" && entry.ViewMembership != ViewMembershipChat {
		return ArchiveAddress{}, fmt.Errorf("unified_store: %s: a transcript append is a chat record, not %q", what, entry.ViewMembership)
	}
	entry.ViewMembership = ViewMembershipChat
	// Every archive record has an id: it is half of the record's address. An
	// entry written without one is given a server-minted id here.
	if entry.ID == "" {
		minted, idErr := newArchivePayloadID()
		if idErr != nil {
			return ArchiveAddress{}, fmt.Errorf("unified_store: %s: %w", what, idErr)
		}
		entry.ID = minted
	}
	rec := ArchiveRecord{TranscriptEntry: entry}
	if provenance != nil {
		kind := "user"
		if provenance.Principal == "" {
			kind = "anonymous" // a genuinely unauthenticated connection, never an invented human
		}
		rec.Source = &EntrySource{Kind: kind, Principal: provenance.Principal}
	}
	addr, err := us.appendArchiveRecordLocked(sessionID, rec)
	if err != nil {
		return ArchiveAddress{}, fmt.Errorf("unified_store: %s: %w", what, err)
	}
	accumulateEntryStats(&meta.Stats, entry)
	meta.UpdatedAt = entry.Timestamp
	// Session shard -> cacheMu; no cacheMu is held during file I/O.
	us.u6MarkStatsDirtyLocked(sessionID, meta)
	return addr, nil
}
