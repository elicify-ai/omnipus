package session

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// AppendTranscriptIndexed shares the ordinary strict appender and returns the
// actual zero-based nonempty record index. Only result recording needs an index;
// token-stream appends do not scan the transcript.
func (us *UnifiedStore) AppendTranscriptIndexed(sessionID string, entry TranscriptEntry) (int, error) {
	return us.appendTranscript(sessionID, entry, true, "append transcript strict", nil)
}

// appendTranscript is the single locked transcript-append body shared by
// AppendTranscript, AppendTranscriptStrict and AppendTranscriptIndexed.
// provenance is nil for every ordinary caller; when non-nil
// (AppendTranscriptWithProvenance only) the record is stamped and persisted
// inside this same session-shard hold, immediately after the transcript line
// lands — and a failed record write rolls the transcript line back, so the
// message and its record are saved together or neither is kept. The paired
// line additionally carries the server-internal write-phase marker
// TranscriptEntry.ProvenancePending from the moment it lands: an abrupt exit
// between the two writes leaves marker-without-record on disk, which every
// reader skips (Correction C4's reopen-hide rule), and the marker is cleared
// by an atomic rewrite once the record is durably written. See
// MessageProvenance for the trust and failure contract.
func (us *UnifiedStore) appendTranscript(sessionID string, entry TranscriptEntry, indexed bool, what string, provenance *MessageProvenance) (int, error) {
	if err := validateSessionID(sessionID); err != nil {
		return -1, err
	}
	if err := validateContextWindowNotice(sessionID, entry); err != nil {
		return -1, fmt.Errorf("unified_store: %s: %w", what, err)
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	return us.appendTranscriptLocked(sessionID, entry, indexed, what, provenance)
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
	if _, err := us.appendTranscriptLocked(sessionID, entry, false, "append transcript once", nil); err != nil {
		return false, err
	}
	return true, nil
}

// appendTranscriptLocked is appendTranscript's body; the caller holds the
// session shard.
func (us *UnifiedStore) appendTranscriptLocked(sessionID string, entry TranscriptEntry, indexed bool, what string, provenance *MessageProvenance) (int, error) {
	// Existence is resolved before any write, including the append's MkdirAll.
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return -1, fmt.Errorf("unified_store: %s: session %q does not exist: %w", what, sessionID, err)
	}
	// C-ARCHIVE (FR-004): every canonical archive entry carries a view
	// membership. The writer defaults an unset one to "both" (an ordinary turn
	// lives in the chat render and the model window alike); a caller may set a
	// more specific view.
	if entry.ViewMembership == "" {
		entry.ViewMembership = ViewMembershipBoth
	}
	// FR-005: the archive is UTC day-partitioned. This resolves (and, when the
	// entry's day is newer than the current one, completes) the day rollover,
	// returning the file to append to. See transcript_partition.go.
	path, err := us.transcriptPartitionPathLocked(sessionID, entry.Timestamp.UTC().Format(transcriptDayLayout))
	if err != nil {
		return -1, fmt.Errorf("unified_store: %s: %w", what, err)
	}
	line := -1
	if indexed {
		line, err = us.transcriptLineCountPartitions(sessionID)
		if err != nil {
			return -1, fmt.Errorf("unified_store: %s: count transcript records: %w", what, err)
		}
	}
	// The provenance-paired append may have to roll this write back (founder
	// decision 2026-10-04: the message and its record are saved together, or
	// neither is kept), so capture the transcript's exact pre-append state
	// first. Ordinary appends (provenance == nil) skip this entirely.
	var preSize int64
	preExisted := false
	if provenance != nil {
		var statErr error
		preSize, preExisted, statErr = transcriptFilePreState(path)
		if statErr != nil {
			return -1, fmt.Errorf("unified_store: %s: stat transcript before paired append: %w", what, statErr)
		}
		// Correction C4 abrupt-exit rule: the paired line lands marked, so a
		// process death between this write and the provenance record leaves
		// marker-without-record — the residue every reader skips on reopen.
		// The marker is cleared below, once the record is durably written.
		entry.ProvenancePending = true
	}
	if err := fileutil.AppendJSONL(path, entry); err != nil {
		return -1, fmt.Errorf("unified_store: append transcript: %w", err)
	}
	if provenance != nil {
		if err := us.appendMessageProvenanceLocked(sessionID, entry, provenance); err != nil {
			// The record could not be saved, so the transcript line must not
			// survive it: restore the pre-append state and fail the append.
			// A rollback that itself fails is wrapped into the returned error,
			// never dropped with only a log line.
			if rbErr := rollbackTranscriptAppend(path, preSize, preExisted); rbErr != nil {
				return -1, fmt.Errorf("unified_store: %s: record provenance: %w; transcript rollback also failed: %w", what, err, rbErr)
			}
			return -1, fmt.Errorf("unified_store: %s: record provenance: %w", what, err)
		}
		// The pair is complete and durable — the message is accepted. Clear
		// the write-phase marker so the line stops reading as in-flight. A
		// failed clear does NOT fail the append: both halves are already on
		// disk, so returning an error would make the gateway reject an
		// accepted message and a retry would duplicate it. The leftover
		// marker is inert by design — readers skip a marker line only when
		// no provenance record exists for its ID, and here the record
		// exists, so the line stays visible — but the failure is surfaced
		// loudly, never silently dropped.
		if err := us.clearProvenancePendingLocked(sessionID, entry.ID); err != nil {
			slog.Error("unified_store: paired append accepted but its pending marker clear failed",
				"session_id", sessionID, "message_id", entry.ID, "error", err)
		}
	}
	accumulateEntryStats(&meta.Stats, entry)
	meta.UpdatedAt = entry.Timestamp
	// Session shard -> cacheMu; no cacheMu is held during file I/O.
	us.u6MarkStatsDirtyLocked(sessionID, meta)
	return line, nil
}

// Indexes include malformed nonempty records, exactly as transcript rewrites do.
// Empty physical lines do not count and therefore cannot shift an addressed row
// when another store rewrite normalizes the file.
func transcriptLineCount(path string) (int, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	n := 0
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
