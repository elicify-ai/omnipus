// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-20260928 sub-agent control plane (asset cd20cf8b), D4/D6.
//
// The per-session control ledger is append-only JSONL beside the lifecycle
// journal. This file is the historical landed-stop reader. It does not
// allocate seq and it does not append: a bare ledger intent is not a landed
// stop, and the seq allocator waits for the stopseq RED pack.
//
// not-wire-format: internal storage only. The SPA receipt remains the
// generated ControlReceipt field. No new gateway byte is defined here.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StoppedTransition is one stop that landed. It stays readable after a
// same-generation RESUME clears the active StopNote. SessionID is the child.
// StopSeq is the control-ledger sequence of that landed stop, not the
// child's generation. An empty ParentSessionID is a root stop: it is history,
// and it is not a child-notice projection. The reader does not invent a parent.
type StoppedTransition struct {
	SessionID       string
	ParentSessionID string
	Generation      int
	StopSeq         uint64
	ControlID       string
	Cause           StopCause
	Actor           string
	At              time.Time
}

// landedStopRecord is the on-disk projection attached to a control line
// only once the stop has landed. Absent on a bare intent.
type landedStopRecord struct {
	ParentSessionID string    `json:"parent_session_id"`
	Generation      int       `json:"generation"`
	Cause           StopCause `json:"cause"`
	Actor           string    `json:"actor"`
	At              time.Time `json:"at"`
}

// controlLedgerLine is one append-only control-ledger record.
type controlLedgerLine struct {
	Seq        int64             `json:"seq"`
	ControlID  string            `json:"control_id"`
	Verb       string            `json:"verb"`
	State      string            `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	AcceptedAt time.Time         `json:"accepted_at"`
	LandedStop *landedStopRecord `json:"landed_stop,omitempty"`
}

func (s *LifecycleStore) controlLedgerPath(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".control.jsonl")
}

// ListStoppedTransitions returns landed stops for the child session, in
// ledger order. A missing ledger is an empty result. A queued or otherwise
// unlanded line is not a result. A newline-terminated line that does not
// parse is a visible error. Only an unterminated torn tail is skipped.
func (s *LifecycleStore) ListStoppedTransitions(sessionID string) ([]StoppedTransition, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return nil, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return s.listStoppedTransitionsLocked(sessionID)
}

func (s *LifecycleStore) listStoppedTransitionsLocked(sessionID string) ([]StoppedTransition, error) {
	raw, err := os.ReadFile(s.controlLedgerPath(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: control ledger: open %q: %w", sessionID, err)
	}
	return stoppedTransitionsFromLedger(sessionID, raw)
}

// stoppedTransitionsFromLedger keeps a final incomplete line only when the
// file does not end in a newline (a crash mid-append). A newline-terminated
// line that does not parse is returned as an error, including the last one.
func stoppedTransitionsFromLedger(sessionID string, raw []byte) ([]StoppedTransition, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	tornTail := raw[len(raw)-1] != '\n'
	parts := bytes.Split(raw, []byte("\n"))
	if !tornTail && len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	var out []StoppedTransition
	var lastSeq uint64
	for i, part := range parts {
		line := bytes.TrimSpace(part)
		if len(line) == 0 {
			continue
		}
		var rec controlLedgerLine
		if err := json.Unmarshal(line, &rec); err != nil {
			if tornTail && i == len(parts)-1 {
				continue
			}
			return nil, fmt.Errorf("session: control ledger: session %q line %d: %w", sessionID, i+1, err)
		}
		if rec.LandedStop == nil || rec.State != "applied" {
			continue
		}
		tr, err := landedTransition(sessionID, rec)
		if err != nil {
			return nil, fmt.Errorf("session: control ledger: session %q line %d: %w", sessionID, i+1, err)
		}
		if lastSeq != 0 && tr.StopSeq <= lastSeq {
			return nil, fmt.Errorf("session: control ledger: session %q stop seq %d does not advance %d", sessionID, tr.StopSeq, lastSeq)
		}
		lastSeq = tr.StopSeq
		out = append(out, tr)
	}
	return out, nil
}

func landedTransition(sessionID string, rec controlLedgerLine) (StoppedTransition, error) {
	landed := rec.LandedStop
	if rec.Seq <= 0 {
		return StoppedTransition{}, fmt.Errorf("landed stop seq %d is not a positive control sequence", rec.Seq)
	}
	if landed.Generation < 1 || landed.Actor == "" || landed.At.IsZero() {
		return StoppedTransition{}, fmt.Errorf("landed stop is missing generation, actor or time")
	}
	if !IsValidStopCause(landed.Cause) {
		return StoppedTransition{}, fmt.Errorf("landed stop cause %q is not valid", landed.Cause)
	}
	if rec.ControlID == "" {
		return StoppedTransition{}, fmt.Errorf("landed stop is missing control_id")
	}
	return StoppedTransition{
		SessionID:       sessionID,
		ParentSessionID: landed.ParentSessionID,
		Generation:      landed.Generation,
		StopSeq:         uint64(rec.Seq),
		ControlID:       rec.ControlID,
		Cause:           landed.Cause,
		Actor:           landed.Actor,
		At:              landed.At,
	}, nil
}
