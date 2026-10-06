package session

import (
	"errors"
	"fmt"
	"strings"
)

// UnfinishedStopIntent is an accepted stop control that never landed: its
// ledger line is still queued and it has no landed history. Either its fence
// never reached the record (D4's crash state, "a stamp or persist refusal
// after a durable intent") or the fence is on the record but no owner landed
// it (a crash before the owning execution settled, D6 QA2). The agent's
// finisher completes it under its original identity or supersedes it.
//
// not-wire-format: internal runtime targeting only.
type UnfinishedStopIntent struct {
	Selection StopSelection
	Cause     StopCause
	Actor     string
}

// UnfinishedStopIntents returns sessionID's unfinished stop intents in ledger
// order. A missing ledger is empty; an unreadable ledger is a visible error.
func (s *LifecycleStore) UnfinishedStopIntents(sessionID string) ([]UnfinishedStopIntent, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return nil, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	lines, err := s.readControlLedgerLocked(sessionID)
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	type state struct {
		first  controlLedgerLine
		last   string
		landed bool
	}
	order := make([]string, 0, len(lines))
	byID := make(map[string]*state)
	for _, line := range lines {
		if line.Verb != controlVerbStop || line.StopEffect == nil || line.ControlID == "" {
			continue
		}
		st, ok := byID[line.ControlID]
		if !ok {
			st = &state{first: line}
			byID[line.ControlID] = st
			order = append(order, line.ControlID)
		}
		st.last = line.State
		if line.LandedStop != nil {
			st.landed = true
		}
	}
	var out []UnfinishedStopIntent
	for _, id := range order {
		st := byID[id]
		if st.landed || st.last != controlStateQueued {
			continue
		}
		out = append(out, UnfinishedStopIntent{
			Selection: StopSelection{SessionID: sessionID, Effect: *st.first.StopEffect},
			Cause:     st.first.Cause,
			Actor:     st.first.Actor,
		})
	}
	return out, nil
}

// ResumeAcceptedStop continues an ALREADY-ACCEPTED stop control (D4's
// "complete unfinished stop fences"; ADR-20260928 D6 class ii): it re-applies
// that control's own fence, note and effect — same control_id, same seq, its
// original cause, actor and selected execution — when the record still
// belongs to that selection, or confirms the fence when it is already on the
// record. It never allocates a new control.
//
// The outcome is StopAcceptGranted (fence on the record, selection
// returned), StopAcceptAlreadyLanded / StopAcceptTerminal (nothing left to
// do), or StopAcceptMissing; a record that moved past the selection (another
// generation or execution, or another control's fence) returns
// ErrStopIntentSuperseded so the caller marks the intent superseded.
func (s *LifecycleStore) ResumeAcceptedStop(intent UnfinishedStopIntent) (StopAcceptance, error) {
	var out StopAcceptance
	sel := intent.Selection
	if err := validateLifecycleSessionID(sel.SessionID); err != nil {
		return out, err
	}
	mu := s.Lock(sel.SessionID)
	mu.Lock()
	defer mu.Unlock()
	lines, err := s.readControlLedgerLocked(sel.SessionID)
	if err != nil {
		return out, err
	}
	var accepted *controlLedgerLine
	for i := range lines {
		if lines[i].ControlID == sel.Effect.ControlID && lines[i].Verb == controlVerbStop && lines[i].StopEffect != nil {
			if accepted == nil {
				accepted = &lines[i]
			}
		}
	}
	if accepted == nil || *accepted.StopEffect != sel.Effect {
		return out, fmt.Errorf("session: control ledger: stop %q of %q has no matching accepted intent", sel.Effect.ControlID, sel.SessionID)
	}
	cur, found, err := s.tail(sel.SessionID)
	if err != nil {
		return out, err
	}
	if !found {
		out.Outcome = StopAcceptMissing
		return out, nil
	}
	out.Generation = cur.Generation
	out.Grant = ControlGrant{Seq: accepted.Seq, ControlID: accepted.ControlID}
	switch {
	case cur.Terminal():
		out.Outcome = StopAcceptTerminal
		return out, nil
	case cur.State == LifecycleStopped:
		out.Outcome = StopAcceptAlreadyLanded
		return out, nil
	}
	if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
		if cur.StopEffect != nil && *cur.StopEffect == sel.Effect {
			out.Outcome = StopAcceptGranted
			out.Selection = &StopSelection{SessionID: sel.SessionID, Effect: sel.Effect}
			return out, nil
		}
		return out, ErrStopIntentSuperseded
	}
	target := sel.Effect.Target
	if cur.Generation != target.Generation {
		return out, ErrStopIntentSuperseded
	}
	if target.Selected() {
		if cur.ExecutionID == nil || cur.ExecutionID.RunID != target.RunID || cur.ExecutionID.BootSeq != target.BootSeq {
			return out, ErrStopIntentSuperseded
		}
	} else if cur.ExecutionID != nil {
		return out, ErrStopIntentSuperseded
	}
	next := *cur
	next.Stop = &Stop{At: accepted.AcceptedAt, Generation: cur.Generation, By: principalFromActor(accepted.Actor)}
	next.StopNote = &StopNote{At: accepted.AcceptedAt, By: accepted.Actor, Seq: uint64(accepted.Seq), Cause: accepted.Cause}
	effect := sel.Effect
	next.StopEffect = &effect
	if err := s.persistLocked(&next); err != nil {
		return out, fmt.Errorf("session: control ledger: resume accepted stop %q for %q: %w", sel.Effect.ControlID, sel.SessionID, err)
	}
	out.Outcome = StopAcceptGranted
	out.Selection = &StopSelection{SessionID: sel.SessionID, Effect: effect}
	return out, nil
}

// ErrStopIntentSuperseded reports that the session moved past an accepted
// stop's selection before its fence took hold.
var ErrStopIntentSuperseded = errors.New("session: control ledger: the accepted stop no longer owns the session")

// principalFromActor inverts StopActorFromPrincipal for an accepted intent's
// recorded actor ("kind:id").
func principalFromActor(actor string) Principal {
	kind, id, found := strings.Cut(actor, ":")
	if !found {
		return Principal{Kind: PrincipalKind(actor)}
	}
	return Principal{Kind: PrincipalKind(kind), ID: id}
}
