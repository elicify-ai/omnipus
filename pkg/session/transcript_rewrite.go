package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

type transcriptMutation struct {
	id     ToolCallID
	line   *int
	match  func(TranscriptEntry, ToolCall) bool
	mutate func(*ToolCall, int)
}

// rewriteTranscriptToolCalls retains latest-id status-update semantics. Indexed
// projection mutations use the same core with an explicit record address.
func (us *UnifiedStore) rewriteTranscriptToolCalls(sessionID string, mutators map[ToolCallID]func(*ToolCall), what string) (int, error) {
	mutations := make([]transcriptMutation, 0, len(mutators))
	for id, mutate := range mutators {
		mutations = append(mutations, transcriptMutation{
			id: id, mutate: func(tc *ToolCall, _ int) { mutate(tc) },
		})
	}
	return us.rewriteTranscriptToolCallsAt(sessionID, mutations, what)
}

// All lookups and mutations are staged under one session shard. No write occurs
// until the entire batch resolves and marshals successfully. Nonempty malformed
// rows remain verbatim and occupy an index; blank rows do not occupy an index.
func (us *UnifiedStore) rewriteTranscriptToolCallsAt(sessionID string, mutations []transcriptMutation, what string) (int, error) {
	if err := validateSessionID(sessionID); err != nil {
		return 0, err
	}
	if len(mutations) == 0 {
		return 0, nil
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	path := filepath.Join(us.baseDir, sessionID, "transcript.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			for _, m := range mutations {
				if m.line != nil {
					return 0, fmt.Errorf("unified_store: %s: addressed transcript is missing", what)
				}
			}
			return 0, nil
		}
		return 0, fmt.Errorf("unified_store: %s: read transcript: %w", what, err)
	}
	entries := make([]json.RawMessage, 0)
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if raw = bytes.TrimSpace(raw); len(raw) > 0 {
			entries = append(entries, raw)
		}
	}
	applied := 0
	for _, m := range mutations {
		idx, call, target, err := findTranscriptMutation(entries, m)
		if err != nil {
			return 0, fmt.Errorf("unified_store: %s: %w", what, err)
		}
		if idx < 0 {
			continue // Legitimate latest-id caller with no recorded row yet.
		}
		m.mutate(&target.ToolCalls[call], idx)
		rewritten, err := json.Marshal(target)
		if err != nil {
			return 0, fmt.Errorf("unified_store: %s: marshal updated entry: %w", what, err)
		}
		entries[idx] = rewritten
		applied++
	}
	if applied == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	for _, raw := range entries {
		buf.Write(raw)
		buf.WriteByte('\n') // Next O_APPEND record must start on its own line.
	}
	if err := fileutil.WriteFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		return 0, fmt.Errorf("unified_store: %s: write transcript: %w", what, err)
	}
	return applied, nil
}

func findTranscriptMutation(entries []json.RawMessage, m transcriptMutation) (int, int, TranscriptEntry, error) {
	start, end := len(entries)-1, 0
	if m.line != nil {
		if *m.line < 0 || *m.line >= len(entries) {
			return -1, -1, TranscriptEntry{}, fmt.Errorf("transcript line %d is outside the transcript", *m.line)
		}
		start, end = *m.line, *m.line
	}
	for i := start; i >= end; i-- {
		var entry TranscriptEntry
		if err := json.Unmarshal(entries[i], &entry); err != nil {
			if m.line != nil {
				return -1, -1, entry, fmt.Errorf("addressed transcript line %d is malformed: %w", i, err)
			}
			continue
		}
		for j, tc := range entry.ToolCalls {
			if tc.ID == m.id && (m.match == nil || m.match(entry, tc)) {
				return i, j, entry, nil
			}
		}
	}
	if m.line != nil {
		return -1, -1, TranscriptEntry{}, fmt.Errorf("transcript line %d does not carry tool call %q", *m.line, m.id)
	}
	return -1, -1, TranscriptEntry{}, nil
}
