// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core FR-024 / C-INPUT: a per-session, append-only sidecar that
// records which user inputs Stop DISCARDED before they were delivered into the
// agent's model input. The archived message bytes are never touched - this
// file only labels them, and the REST and replay projections read it to show
// the read-only input_disposition on the original message.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const inputDispositionFileName = "input_dispositions.jsonl"

const (
	// InputStateDiscarded is the only disposition state: the input never
	// reached the agent.
	InputStateDiscarded = "discarded"
	// InputReasonStoppedBeforeDelivery is why: a Stop landed first.
	InputReasonStoppedBeforeDelivery = "stopped_before_delivery"
)

// InputDisposition is one discarded-input record (mirrors the wire
// input_disposition on Message / ReplayMessageFrame).
type InputDisposition struct {
	MessageID       string `json:"message_id"`
	ClientMessageID string `json:"client_message_id,omitempty"`
	State           string `json:"state"`
	Reason          string `json:"reason"`
}

func (us *UnifiedStore) inputDispositionPath(sessionID string) string {
	return filepath.Join(us.baseDir, sessionID, inputDispositionFileName)
}

// RecordInputDiscarded durably records that messageID was discarded by Stop
// before delivery. Idempotent: a message already recorded is not recorded
// again. messageID is the original transcript entry id.
func (us *UnifiedStore) RecordInputDiscarded(sessionID, messageID, clientMessageID string) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if messageID == "" {
		return errors.New("session: record discarded input: empty message id")
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	existing, err := readInputDispositions(us.inputDispositionPath(sessionID))
	if err != nil {
		return err
	}
	if _, ok := existing[messageID]; ok {
		return nil
	}
	rec := InputDisposition{
		MessageID: messageID, ClientMessageID: clientMessageID,
		State: InputStateDiscarded, Reason: InputReasonStoppedBeforeDelivery,
	}
	if err := fileutil.AppendJSONLSync(us.inputDispositionPath(sessionID), rec); err != nil {
		return fmt.Errorf("session: record discarded input %q: %w", messageID, err)
	}
	return nil
}

// InputDispositions returns the session's discarded-input records keyed by
// original message id. A missing file means nothing was discarded.
func (us *UnifiedStore) InputDispositions(sessionID string) (map[string]InputDisposition, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	h := us.lockSession(sessionID)
	defer h.Unlock()
	return readInputDispositions(us.inputDispositionPath(sessionID))
}

func readInputDispositions(path string) (map[string]InputDisposition, error) {
	out := map[string]InputDisposition{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: read input dispositions: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec InputDisposition
		if err := json.Unmarshal(line, &rec); err != nil || rec.MessageID == "" {
			continue // a torn tail line is skipped, as the other sidecars do
		}
		out[rec.MessageID] = rec
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("session: scan input dispositions: %w", err)
	}
	return out, nil
}

// ReadTranscriptWithDispositions is ReadTranscript plus the read-only
// input_disposition projection (FR-024) on every user entry Stop discarded
// before delivery. The archive bytes are unchanged; the label comes from the
// sidecar, and the entry's own client_message_id is filled into it.
func (us *UnifiedStore) ReadTranscriptWithDispositions(sessionID string) ([]TranscriptEntry, error) {
	entries, err := us.ReadTranscript(sessionID)
	if err != nil {
		return nil, err
	}
	dispositions, err := us.InputDispositions(sessionID)
	if err != nil {
		return nil, err
	}
	if len(dispositions) == 0 {
		return entries, nil
	}
	for i := range entries {
		rec, ok := dispositions[entries[i].ID]
		if !ok {
			continue
		}
		if rec.ClientMessageID == "" {
			rec.ClientMessageID = entries[i].ClientMessageID
		}
		entries[i].InputDisposition = &rec
	}
	return entries, nil
}
