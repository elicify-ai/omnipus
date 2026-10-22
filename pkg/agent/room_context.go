package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (ts *turnState) roomContextSource(in session.ModelAppend, requestID string) (session.ModelAppend, error) {
	id := ts.transcriptSessionID
	if id == "" {
		id = ts.sessionKey
	}
	if requestID != "" {
		in.Source.RoomRequest = &session.RoomRequestRef{SessionID: id, RequestID: requestID}
		return in, nil
	}
	if in.Source.Kind != "agent" {
		return in, nil
	}
	meter := ts.opts.roomCost
	if meter == nil || meter.al == nil || ts.transcriptStore == nil {
		return in, nil
	}
	var refs []session.RoomRequestRef
	meter.mu.Lock()
	for requestID := range meter.joined {
		refs = append(refs, session.RoomRequestRef{SessionID: id, RequestID: requestID})
	}
	meter.mu.Unlock()
	lc := meter.al.GetSessionLifecycleStore()
	if lc == nil {
		if len(refs) > 0 {
			return in, fmt.Errorf("context_repair_required: room execution identity is unavailable")
		}
		return in, nil
	}
	rec, err := lc.Load(id)
	if err != nil {
		if len(refs) > 0 {
			return in, err
		}
		return in, nil
	}
	if rec.SteeredBy != nil && rec.RoomScope != nil {
		inherited, err := session.ReadRoomRequestScope(ts.transcriptStore.BaseDir(), rec.RoomScope)
		if err != nil {
			return in, err
		}
		refs = append(refs, inherited...)
	}
	if len(refs) == 0 {
		return in, nil
	}
	scope, err := session.SaveRoomRequestScope(ts.transcriptStore.BaseDir(), id, refs)
	if err != nil {
		return in, err
	}
	in.Source.RoomScope = scope
	if rec.RoomScope != nil && *rec.RoomScope == *scope {
		return in, nil
	}
	claim := meter.al.tsExecutionClaim(ts, id)
	if err := lc.Mutate(id, func(current *session.LifecycleRecord) error {
		if !claim.matches(current) || current.Terminal() {
			return fmt.Errorf("context_repair_required: room producer no longer owns execution")
		}
		current.RoomScope = scope
		return nil
	}); err != nil {
		return in, err
	}
	return in, nil
}

func (ts *turnState) bindRoomInput(requestID string, slot session.ModelSlot) error {
	if requestID == "" {
		return nil
	}
	meter := ts.opts.roomCost
	if meter == nil || meter.al == nil {
		return fmt.Errorf("context_repair_required: room input owner is missing")
	}
	claim := meter.al.tsExecutionClaim(ts, ts.transcriptSessionID)
	execution := session.ExecutionIdentity{RunID: claim.RunID, BootSeq: claim.BootSeq}
	if err := meter.al.RequestLedger().BindRoomInput(ts.transcriptSessionID, requestID, slot.Addr, execution); err != nil {
		return err
	}
	meter.al.markRequestDelivered(ts.transcriptSessionID, requestID)
	return nil
}

func (al *AgentLoop) retireCutRoomWork(rec *session.LifecycleRecord) error {
	if rec == nil || rec.SteeredBy != nil || rec.ExecutionID == nil || rec.ExecutionID.BootSeq >= al.bootEpochFor() {
		return nil
	}
	if rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued && rec.State != session.LifecycleNeedsInput &&
		!(rec.State == session.LifecycleStopped && rec.StopNote != nil && rec.StopNote.Cause == session.StopCauseRestart) {
		return nil
	}
	ledger := al.RequestLedger()
	if ledger == nil {
		return nil
	}
	captures, err := ledger.ListCaptures(rec.SessionID)
	if err != nil {
		return err
	}
	for _, c := range captures {
		if c.Source.Kind != addressing.SourceRoom {
			continue
		}
		if c.ModelExecution == nil || c.ModelInput == nil {
			if c.Delivered && !c.Discarded {
				return fmt.Errorf("context_repair_required: consumed room input %s has no exact execution correlation", c.RequestID)
			}
			continue
		}
		if *c.ModelExecution != *rec.ExecutionID {
			continue
		}
		if err := al.retireRoomCapture(c); err != nil {
			return err
		}
	}
	return nil
}

func (al *AgentLoop) retireRoomCapture(c addressing.Capture) error {
	if c.ModelInput == nil || c.ModelExecution == nil {
		return fmt.Errorf("context_repair_required: room input correlation is missing")
	}
	if err := session.RetireRoomRequest(al.GetSessionStore().BaseDir(), session.RoomRequestRetirement{
		Request: session.RoomRequestRef{SessionID: c.ReceiverSessionID, RequestID: c.RequestID}, Input: *c.ModelInput, Execution: *c.ModelExecution,
	}); err != nil {
		return err
	}
	al.settleRequest(c.RequestID)
	return nil
}

func (al *AgentLoop) retiredRoomScope(scope *session.RoomScopeRef) (bool, error) {
	if scope == nil {
		return false, nil
	}
	refs, err := session.ReadRoomRequestScope(al.GetSessionStore().BaseDir(), scope)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		retired, err := session.RoomRequestRetired(al.GetSessionStore().BaseDir(), ref)
		if err != nil || retired {
			return retired, err
		}
	}
	return false, nil
}

func (al *AgentLoop) refuseRetiredRoomWork(rec *session.LifecycleRecord) error {
	retired, err := al.retiredRoomScope(rec.RoomScope)
	if err != nil {
		return err
	}
	if retired {
		return fmt.Errorf("room request work is retired; a new request or delegation is required")
	}
	return nil
}

// inheritedRoomScope excludes independent task/plan work from room continuation.
func inheritedRoomScope(origin session.Origin, parent *session.LifecycleRecord) *session.RoomScopeRef {
	if origin.Kind != session.OriginKindDelegate || parent.RoomScope == nil {
		return nil
	}
	copy := *parent.RoomScope
	return &copy
}

// Keep the first placement explicit; independent steering does not inherit its request id.
func (ts *turnState) appendStartingRoomInput(msg providers.Message, producer windowProducer) (session.ModelSlot, error) {
	return ts.appendWindowMessage(msg, producer, ts.opts.RoomRequestID)
}
