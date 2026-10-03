package session

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// AppendTranscriptIndexed shares the ordinary strict appender and returns the
// actual zero-based nonempty record index. Only result recording needs an index;
// token-stream appends do not scan the transcript.
func (us *UnifiedStore) AppendTranscriptIndexed(sessionID string, entry TranscriptEntry) (int, error) {
	return us.appendTranscript(sessionID, entry, true, "append transcript strict")
}

func (us *UnifiedStore) appendTranscript(sessionID string, entry TranscriptEntry, indexed bool, what string) (int, error) {
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
	// Existence is resolved before any write, including the append's MkdirAll.
	meta, err := us.readMetaLocked(sessionID)
	if err != nil {
		return -1, fmt.Errorf("unified_store: %s: session %q does not exist: %w", what, sessionID, err)
	}
	path := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	line := -1
	if indexed {
		line, err = transcriptLineCount(path)
		if err != nil {
			return -1, fmt.Errorf("unified_store: %s: count transcript records: %w", what, err)
		}
	}
	if err := fileutil.AppendJSONL(path, entry); err != nil {
		return -1, fmt.Errorf("unified_store: append transcript: %w", err)
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
