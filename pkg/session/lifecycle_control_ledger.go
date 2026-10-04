// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-20260928 sub-agent control plane (asset cd20cf8b), D4/D6.
//
// The per-session control ledger is append-only JSONL under the lifecycle
// dir's dedicated "controls" subdirectory (a flat sidecar would collide
// with the flat *.jsonl session scanners — see the writer file's header).
// This file is the historical landed-stop reader and the shared on-disk
// line shape. It does not allocate seq and it does not append: the write
// half is lifecycle_control_ledger_writer.go.
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
// only once the stop has landed. Absent on a bare intent. Its presence —
// NOT the line's receipt state — is what makes a line a committed
// landed-transition event: the final "applied" receipt cannot precede the
// D6 direct-parent notice's durability, so a landed stop stays state
// "queued" until a later unit writes the applied receipt.
type landedStopRecord struct {
	ParentSessionID string    `json:"parent_session_id"`
	Generation      int       `json:"generation"`
	Cause           StopCause `json:"cause"`
	Actor           string    `json:"actor"`
	At              time.Time `json:"at"`
}

// controlLedgerLine is one append-only control-ledger record. The
// Generation/Cause/Actor/StopEffect fields are the stop verb's acceptance
// content (the write half fills them); Reason stays the D4 receipt reason.
type controlLedgerLine struct {
	Seq        int64             `json:"seq"`
	ControlID  string            `json:"control_id"`
	Verb       string            `json:"verb"`
	State      string            `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	AcceptedAt time.Time         `json:"accepted_at"`
	Generation int               `json:"generation,omitempty"`
	Cause      StopCause         `json:"cause,omitempty"`
	Actor      string            `json:"actor,omitempty"`
	StopEffect *StopEffect       `json:"stop_effect,omitempty"`
	LandedStop *landedStopRecord `json:"landed_stop,omitempty"`
}

func (s *LifecycleStore) controlLedgerPath(sessionID string) string {
	// Dedicated subdirectory, NOT a flat sidecar: the lifecycle dir's flat
	// *.jsonl scanners (scanSessionIDs, SteerBootRecovery.sessionIDs) skip
	// directories, so "controls/" never phantom-registers as a session id —
	// while a flat "<session>.control.jsonl" would. No scanner is patched to
	// hide the sidecar; the directory IS the non-collision.
	return filepath.Join(s.dir, "controls", sessionID+".jsonl")
}

// ListStoppedTransitions returns the child's committed landed-transition
// events, in ledger order. A line is a landed transition when its
// landed_stop projection is present — REGARDLESS of the line's receipt
// state: the final "applied" receipt waits for the D6 direct-parent notice's
// durability, so requiring it here would hide every landed stop whose
// notice has not been published yet, and a bare queued intent (no
// projection) is still not a landed stop. A control's landed history
// survives a same-generation RESUME (the ledger is append-only; the resume
// clears the record's active note, never history), and a control landed
// before its parent notice keeps returning until the notice unit finalizes
// it — the reader reports history, never the final receipt.
//
// One transition is returned per control sequence: a later line refining
// the same control's receipt state (a future applied receipt) never
// duplicates it — the first landed occurrence carries the history.
//
// A missing ledger is an empty result. A newline-terminated line that does
// not parse is a visible error. Only an unterminated torn tail is skipped.
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

// parseControlLedger parses the whole ledger, shared by the reader and the
// write half's scans. It keeps a final incomplete line only when the file
// does not end in a newline (a crash mid-append). A newline-terminated line
// that does not parse is returned as an error, including the last one.
func parseControlLedger(sessionID string, raw []byte) ([]controlLedgerLine, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	tornTail := raw[len(raw)-1] != '\n'
	parts := bytes.Split(raw, []byte("\n"))
	if !tornTail && len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	out := make([]controlLedgerLine, 0, len(parts))
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
		out = append(out, rec)
	}
	return out, nil
}

// stoppedTransitionsFromLedger projects the parsed ledger onto landed
// transitions: one per control sequence, strictly advancing, in ledger
// order.
func stoppedTransitionsFromLedger(sessionID string, raw []byte) ([]StoppedTransition, error) {
	lines, err := parseControlLedger(sessionID, raw)
	if err != nil {
		return nil, err
	}
	var out []StoppedTransition
	var lastSeq uint64
	returnedSeq := make(map[uint64]struct{})
	for i, rec := range lines {
		if rec.LandedStop == nil {
			continue
		}
		tr, err := landedTransition(sessionID, rec)
		if err != nil {
			return nil, fmt.Errorf("session: control ledger: session %q line %d: %w", sessionID, i+1, err)
		}
		if _, seen := returnedSeq[tr.StopSeq]; seen {
			continue
		}
		returnedSeq[tr.StopSeq] = struct{}{}
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
