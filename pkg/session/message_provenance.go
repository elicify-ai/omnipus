package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
//     REST or WebSocket surface reads; TranscriptEntry gains NO provenance
//     DATA (no principal, no ordinal — nothing client-meaningful), so
//     nothing client-visible changes shape. Its one addition is the
//     server-internal write-phase marker TranscriptEntry.ProvenancePending
//     (Correction C4's abrupt-exit rule): it says only that a paired save
//     was once in flight for that line, carries no record content, and is
//     cleared once the record is durably written.
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
//   - The provenance record is mandatory for acceptance (founder decision
//     2026-10-04): the message and its record are saved together, or neither
//     is kept. If the record cannot be saved after the transcript line
//     landed, the transcript file is rolled back to its exact pre-append
//     state — the appended line is truncated away, or the whole file is
//     removed when this append created it — and the error is returned. No
//     half-saved message remains; the caller must treat the message as NOT
//     accepted (no echo, no acknowledgment, no turn). A rollback that itself
//     fails is wrapped into the returned error, never swallowed.
//   - A record read that hits a torn line (power-loss corner) fails closed:
//     appends and lookups for that session error until the record file is
//     repaired.
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

// transcriptFilePreState reports the transcript file's size before an append
// and whether the file exists at all — the exact state
// rollbackTranscriptAppend restores when the paired provenance record cannot
// be saved.
func transcriptFilePreState(path string) (size int64, existed bool, err error) {
	info, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return 0, false, nil
	}
	if statErr != nil {
		return 0, false, statErr
	}
	return info.Size(), true, nil
}

// rollbackTranscriptAppend restores the transcript to the exact pre-append
// state transcriptFilePreState captured: the bytes the failed paired append
// added are truncated away and fsynced — as durable as the append it undoes —
// or the whole file is removed when the append created it. The caller holds
// the session shard (appendTranscript's hold), so no other writer can
// interleave between the append and its rollback. Failures are returned so
// the caller can surface them alongside the provenance error that triggered
// the rollback — never logged and dropped.
func rollbackTranscriptAppend(path string, preSize int64, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rollback append-created transcript file: %w", err)
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open transcript for rollback: %w", err)
	}
	if err := f.Truncate(preSize); err != nil {
		_ = f.Close()
		return fmt.Errorf("truncate transcript to %d bytes: %w", preSize, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync rolled-back transcript: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close rolled-back transcript: %w", err)
	}
	return nil
}

// clearProvenancePendingLocked removes the paired append's write-phase marker
// (TranscriptEntry.ProvenancePending) from the entry's transcript line once
// its MessageProvenance record is durably written — Correction C4's "clear it
// when the sender record lands". The caller holds sessionID's shard
// (appendTranscript's hold). The rewrite is the store's established
// whole-file atomic form (transcript_rewrite.go): read, mutate in memory,
// WriteFileAtomic — no torn intermediate state, and every untouched line,
// malformed ones included, round-trips verbatim. A missing transcript file is
// nothing to clear (the session was deleted mid-flight); a line that no
// longer carries the marker is equally a no-op.
func (us *UnifiedStore) clearProvenancePendingLocked(sessionID, messageID string) error {
	path := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("unified_store: clear provenance pending: read transcript: %w", err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	changed := false
	for i, raw := range lines {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		var entry TranscriptEntry
		if json.Unmarshal(trimmed, &entry) != nil {
			continue // Malformed line stays verbatim, as every rewrite treats it.
		}
		if entry.ID != messageID || !entry.ProvenancePending {
			continue
		}
		entry.ProvenancePending = false
		rewritten, mErr := json.Marshal(entry)
		if mErr != nil {
			return fmt.Errorf("unified_store: clear provenance pending: marshal entry %q: %w", messageID, mErr)
		}
		lines[i] = rewritten
		changed = true
		break // Entry IDs are unique; the marker line is the one just appended.
	}
	if !changed {
		return nil
	}
	var buf bytes.Buffer
	for _, raw := range lines {
		buf.Write(raw)
		buf.WriteByte('\n') // Next O_APPEND record must start on its own line.
	}
	if err := fileutil.WriteFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("unified_store: clear provenance pending: write transcript: %w", err)
	}
	return nil
}

// filterUnpairedProvenanceLines applies Correction C4's abrupt-exit rule to a
// parsed transcript read: a line that still carries the write-phase marker
// AND has no MessageProvenance record for its ID is crash residue from an
// incomplete paired save — it is not shown and not admitted, from every
// reader's perspective neither half of the pair is kept, while its bytes
// remain on disk as inert residue. Every other line passes untouched:
// entries without the marker (all agent and tool lines, and every historical
// user line that was never part of a paired save) are never checked against
// provenance at all, and a marker line whose record EXISTS is the accepted
// pair whose clearing rewrite did not land — it stays visible.
//
// The provenance read is the only extra I/O, and only when a marker-bearing
// line is actually present: the steady state after any paired append (and
// every session that never used one) does none. An unreadable provenance
// file leaves acceptance unestablishable, so marker lines fail safe to
// hidden (the next read after a transient concurrent-append tear shows them
// once the pair and its clearing rewrite completed) while every non-marker
// line is unaffected; the acceptor-side reads (append, lookup) keep failing
// loudly on the same conditions — this projection never understates what the
// acceptor will still reject.
func (us *UnifiedStore) filterUnpairedProvenanceLines(sessionID string, entries []TranscriptEntry) []TranscriptEntry {
	pending := -1
	for i := range entries {
		if entries[i].ProvenancePending {
			pending = i
			break
		}
	}
	if pending < 0 {
		return entries
	}
	records, err := readMessageProvenance(us.messageProvenancePath(sessionID))
	if err != nil {
		slog.Warn("unified_store: provenance history unreadable; withholding provenance-pending lines",
			"session_id", sessionID, "error", err)
		records = nil
	}
	paired := make(map[string]struct{}, len(records))
	for _, record := range records {
		paired[record.MessageID] = struct{}{}
	}
	kept := make([]TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.ProvenancePending {
			if _, ok := paired[entry.ID]; !ok {
				continue // Crash residue: the paired save never completed.
			}
		}
		kept = append(kept, entry)
	}
	return kept
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
