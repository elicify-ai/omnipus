package session

import (
	"fmt"
	"time"
)

// controlVerbSteer is the ledger's verb literal for a delegate steer/respond
// instruction (the D4 verb enum's "steer").
const controlVerbSteer = "steer"

// Steer receipt states (D4): queued on acceptance; delivered once the exact
// text is durably in the child's transcript (final for a steer); superseded
// when a newer control (a Stop) replaced it before delivery.
const (
	SteerStateDelivered  = "delivered"
	SteerStateSuperseded = "superseded"
)

// AcceptSteerControl durably records an accepted steer for sessionID before
// it is queued (D4: ledger line first, then the effect). It returns the
// granted control identity the queued item carries. A missing record is an
// error: only a session with a lifecycle record has a control ledger.
//
// not-wire-format: internal storage only.
func (s *LifecycleStore) AcceptSteerControl(sessionID, text, actor, correlationID string) (ControlGrant, error) {
	var grant ControlGrant
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return grant, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	cur, found, err := s.tail(sessionID)
	if err != nil {
		return grant, err
	}
	if !found {
		return grant, fmt.Errorf("session: control ledger: steer for %q: %w", sessionID, ErrLifecycleNotFound)
	}
	highWater, err := s.controlSeqHighWaterLocked(sessionID)
	if err != nil {
		return grant, err
	}
	controlID, err := newControlID()
	if err != nil {
		return grant, err
	}
	grant = ControlGrant{Seq: highWater + 1, ControlID: controlID}
	line := controlLedgerLine{
		Seq: grant.Seq, ControlID: grant.ControlID, Verb: controlVerbSteer,
		State: controlStateQueued, AcceptedAt: time.Now().UTC(),
		Generation: cur.Generation, Actor: actor, Text: text, CorrelationID: correlationID,
	}
	if err := appendControlLineLocked(s, sessionID, line); err != nil {
		return ControlGrant{}, fmt.Errorf("session: control ledger: append steer acceptance for %q: %w", sessionID, err)
	}
	return grant, nil
}

// RecordSteerControlState moves an accepted steer's receipt to delivered or
// superseded by appending the refined line. A receipt already final is left
// unchanged; an unknown control id is a visible error.
func (s *LifecycleStore) RecordSteerControlState(sessionID, controlID, state, reason string) error {
	if state != SteerStateDelivered && state != SteerStateSuperseded {
		return fmt.Errorf("session: control ledger: invalid steer receipt state %q", state)
	}
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	lines, err := s.readControlLedgerLocked(sessionID)
	if err != nil {
		return err
	}
	var latest *controlLedgerLine
	for i := range lines {
		if lines[i].ControlID == controlID && lines[i].Verb == controlVerbSteer {
			latest = &lines[i]
		}
	}
	if latest == nil {
		return fmt.Errorf("session: control ledger: steer %q of %q has no accepted receipt", controlID, sessionID)
	}
	if latest.State != controlStateQueued {
		return nil
	}
	refined := *latest
	refined.State, refined.Reason = state, reason
	return appendControlLineLocked(s, sessionID, refined)
}

// SupersedeSteersByStop marks each listed still-queued steer of sessionID
// superseded by the Stop control stopControlID (D4S-01, D5): reason "stop"
// and superseded_by_seq that Stop's own sequence. A steer receipt already
// final is left unchanged; an unknown steer or Stop control is a visible
// error and nothing is written.
func (s *LifecycleStore) SupersedeSteersByStop(sessionID, stopControlID string, steerControlIDs []string) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	lines, err := s.readControlLedgerLocked(sessionID)
	if err != nil {
		return err
	}
	stopSeq := int64(0)
	latest := make(map[string]controlLedgerLine)
	for _, line := range lines {
		if line.ControlID == stopControlID && line.Verb == controlVerbStop {
			stopSeq = line.Seq
		}
		if line.Verb == controlVerbSteer {
			latest[line.ControlID] = line
		}
	}
	if stopSeq == 0 {
		return fmt.Errorf("session: control ledger: Stop %q of %q has no accepted control", stopControlID, sessionID)
	}
	var refined []controlLedgerLine
	for _, id := range steerControlIDs {
		line, ok := latest[id]
		if !ok {
			return fmt.Errorf("session: control ledger: steer %q of %q has no accepted receipt", id, sessionID)
		}
		if line.State != controlStateQueued {
			continue
		}
		bySeq := stopSeq
		line.State, line.Reason, line.SupersededBySeq = SteerStateSuperseded, "stop", &bySeq
		refined = append(refined, line)
	}
	for _, line := range refined {
		if err := appendControlLineLocked(s, sessionID, line); err != nil {
			return fmt.Errorf("session: control ledger: supersede steer %q of %q: %w", line.ControlID, sessionID, err)
		}
	}
	return nil
}
