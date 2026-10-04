package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
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
//     MessageID/SessionID/Content/Ordinal are stamped there from the entry
//     the store actually persisted — never from caller-supplied values. The
//     caller supplies nothing but the authenticated principal.
//   - The generic transcript writers (AppendTranscript /
//     AppendTranscriptStrict / AppendTranscriptIndexed — every pkg/agent,
//     pkg/tools and channel caller of them) structurally cannot mint,
//     extend or alter provenance: they never touch the provenance file.
//
// Wire boundary — who can read one:
//
//   - Records live in the session's own provenance.jsonl, a file no SPA,
//     REST or WebSocket surface reads; TranscriptEntry deliberately gains
//     NO provenance fields, so nothing client-visible changes shape.
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

// provenanceFileName is the per-session provenance record file, written with
// the same fsyncing single-record append the transcript uses.
const provenanceFileName = "provenance.jsonl"

// AppendTranscriptWithProvenance appends entry to the session transcript and,
// in the SAME per-session shard hold, persists the MessageProvenance record
// for it (ADR-20260928 D1.4). principal is the authenticated connection
// principal (FR-073) — the only field the caller supplies; MessageID,
// SessionID, Content and Ordinal are stamped by the store from what it
// actually wrote, so a caller cannot mint provenance for text or an id the
// store never accepted.
//
// Ordering and failure contract:
//
//   - The transcript append runs first; if it fails, NO provenance is
//     written and the session's provenance history is untouched.
//   - The provenance record is mandatory for acceptance: if the provenance
//     write fails after the transcript line landed, the error is returned
//     and the caller must treat the message as NOT accepted — no echo, no
//     acknowledgment, no turn. The already-written transcript line then
//     stays on disk unprovenanced and unacknowledged; that torn-write
//     window is inherent to a two-record file write with no transaction and
//     is documented here rather than hidden. A record read that hits a torn
//     line (power-loss corner) fails closed the same way: appends and
//     lookups for that session error until the record file is repaired.
func (us *UnifiedStore) AppendTranscriptWithProvenance(sessionID string, entry TranscriptEntry, principal string) error {
	_, err := us.appendTranscript(sessionID, entry, false, "append transcript with provenance", &MessageProvenance{Principal: principal})
	return err
}

// LookupMessageProvenance returns the provenance record persisted for the
// server message id (a transcript entry ID) in the session, and whether one
// exists. It takes the session's shard, so a lookup never observes a
// half-written append. A missing record file means no provenance has been
// minted in this session — not an error.
func (us *UnifiedStore) LookupMessageProvenance(sessionID, messageID string) (MessageProvenance, bool, error) {
	if err := validateSessionID(sessionID); err != nil {
		return MessageProvenance{}, false, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	records, err := readMessageProvenance(us.messageProvenancePath(sessionID))
	if err != nil {
		return MessageProvenance{}, false, err
	}
	for _, record := range records {
		if record.MessageID == messageID {
			return record, true, nil
		}
	}
	return MessageProvenance{}, false, nil
}

// messageProvenancePath is the session's provenance record file path.
func (us *UnifiedStore) messageProvenancePath(sessionID string) string {
	return filepath.Join(us.baseDir, sessionID, provenanceFileName)
}

// appendMessageProvenanceLocked stamps and appends the provenance record for
// a just-persisted transcript entry. The caller holds sessionID's shard
// (appendTranscript's hold), which is what makes the ordinal monotonic: the
// next ordinal is derived from the records on disk and no other writer can
// interleave.
func (us *UnifiedStore) appendMessageProvenanceLocked(sessionID string, entry TranscriptEntry, record *MessageProvenance) error {
	records, err := readMessageProvenance(us.messageProvenancePath(sessionID))
	if err != nil {
		return err
	}
	var last int64
	for _, existing := range records {
		if existing.Ordinal > last {
			last = existing.Ordinal
		}
	}
	record.MessageID = entry.ID
	record.SessionID = sessionID
	record.Content = entry.Content
	record.Ordinal = last + 1
	return fileutil.AppendJSONL(us.messageProvenancePath(sessionID), *record)
}

// readMessageProvenance loads every provenance record in the session's
// provenance.jsonl, in append order. A missing file is an empty history.
// Malformed records fail loudly (never skipped): a provenance history that
// cannot be read in full must not silently understate what was accepted.
func readMessageProvenance(path string) ([]MessageProvenance, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("unified_store: read provenance: %w", err)
	}
	records := []MessageProvenance{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var record MessageProvenance
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				return records, nil
			}
			return nil, fmt.Errorf("unified_store: parse provenance record: %w", err)
		}
		records = append(records, record)
	}
}
