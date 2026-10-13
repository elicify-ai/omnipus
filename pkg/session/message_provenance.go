package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// MessageProvenance is the SERVER-INTERNAL provenance record ADR-20260928
// D1.4 persists beside an accepted authenticated web-gateway user message.
// It answers, durably and without trusting anything the client sent: which
// server-generated message was accepted, in which session, with which exact
// text, by which authenticated connection principal, at which per-session
// append ordinal.
//
// Trust boundary — who can mint one:
//
//   - The ONLY writer is AppendTranscriptWithProvenance, and the record's
//     MessageID/SessionID/Content/Ordinal are read back from the line the
//     store actually persisted — never from caller-supplied values. The
//     caller supplies nothing but the authenticated principal.
//   - The generic transcript writers (AppendTranscript /
//     AppendTranscriptStrict / AppendTranscriptIndexed — every pkg/agent,
//     pkg/tools and channel caller of them) structurally cannot mint,
//     extend or alter provenance: they take a TranscriptEntry, which has no
//     source member.
//
// Wire boundary — who can read one:
//
//   - The source is a private member of the message's own transcript line
//     (ArchiveRecord.Source). TranscriptEntry gains NO provenance data (no
//     principal, no ordinal — nothing client-meaningful), and ReadTranscript
//     decodes into TranscriptEntry, so nothing client-visible changes shape.
//   - The programmatic read path is LookupMessageProvenance (by server
//     message id), for the D1.4 answer acceptor this writer precedes.
type MessageProvenance struct {
	// MessageID is the server-generated transcript entry ID ("server message
	// id") this record describes — the lookup key.
	MessageID string `json:"message_id"`
	// SessionID is the session the entry was appended to, stamped from the
	// store's own sessionID argument.
	SessionID string `json:"session_id"`
	// Content is the exact accepted text persisted on the transcript entry,
	// stamped from the entry the store wrote.
	Content string `json:"content"`
	// Principal is the authenticated connection principal (wc.userID at
	// message intake, FR-073). Empty only where the connection genuinely has
	// none (dev-mode bypass / legacy env-token auth); the gateway passes it
	// through verbatim and the store does not invent one.
	Principal string `json:"principal,omitempty"`
	// Ordinal is the monotonic per-session append ordinal: 1 on the first
	// provenance-carrying append in the session, last+1 on every later one.
	// Derived under the session shard lock, so two appends in one session
	// can never mint the same ordinal.
	Ordinal int64 `json:"ordinal"`
}

// AppendTranscriptWithProvenance appends entry to the session transcript with
// the authenticated principal written into the SAME line as its trusted source
// (ADR-20260928 D1.4; session-core Decision A). principal is the authenticated
// connection principal (FR-073) — the only field the caller supplies; the
// message id, session, content and ordinal a later lookup reports are read from
// the line the store actually wrote, so a caller cannot mint provenance for text
// or an id the store never accepted. The generic transcript writers cannot set a
// source at all: they take a TranscriptEntry, which has none.
//
// Failure contract: the line is one append, so the message and its source are
// saved together or neither is kept. A write that fails part-way leaves at most
// a torn final line, which no reader decodes and the next append starts past.
func (us *UnifiedStore) AppendTranscriptWithProvenance(sessionID string, entry TranscriptEntry, principal string) error {
	_, err := us.appendTranscript(sessionID, entry, "append transcript with provenance", &MessageProvenance{Principal: principal})
	return err
}

// LookupMessageProvenance returns the provenance persisted for the server
// message id (a transcript entry ID) in the session, and whether one exists. It
// takes the session's shard, so a lookup never observes a half-written append.
// Ordinal is the 1-based position of the message among the session's
// source-bearing lines: 1 on the first provenance-carrying append, last+1 on
// every later one — derived from the lines themselves, so two appends in one
// session can never share one and no counter file exists.
func (us *UnifiedStore) LookupMessageProvenance(sessionID, messageID string) (MessageProvenance, bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return MessageProvenance{}, false, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	paths, err := us.transcriptPartitionPaths(sessionID)
	if err != nil {
		return MessageProvenance{}, false, fmt.Errorf("unified_store: lookup provenance: %w", err)
	}
	var ordinal int64
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return MessageProvenance{}, false, fmt.Errorf("unified_store: lookup provenance: read transcript: %w", readErr)
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var rec ArchiveRecord
			if json.Unmarshal(line, &rec) != nil || rec.Source == nil || rec.ViewMembership != ViewMembershipChat {
				// Torn residue, a line that carries no trusted source, or a model
				// record (its source names the model's producer, not a person).
				continue
			}
			ordinal++
			if rec.ID == messageID {
				return MessageProvenance{
					MessageID: rec.ID, SessionID: sessionID, Content: rec.Content,
					Principal: rec.Source.Principal, Ordinal: ordinal,
				}, true, nil
			}
		}
	}
	return MessageProvenance{}, false, nil
}
