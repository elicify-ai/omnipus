package agent

import "fmt"

type roomRecallProjectionSource interface{ RoomProjectionRevision() uint64 }

func (rct *recallConversationToolExecute) beginRoomProjectionRead() {
	if source, ok := rct.t.archive.(roomRecallProjectionSource); ok {
		rct.roomProjectionRevision, rct.roomProjectionBound = source.RoomProjectionRevision(), true
	}
}

func (rct *recallConversationToolExecute) bindRoomProjectionRead(span *RecallSpan) error {
	if !rct.roomProjectionBound {
		return nil
	}
	source := rct.t.archive.(roomRecallProjectionSource)
	if source.RoomProjectionRevision() != rct.roomProjectionRevision {
		return fmt.Errorf("recall context changed during room retirement; request a fresh recall")
	}
	span.roomProjectionRevision, span.roomProjectionBound = rct.roomProjectionRevision, true
	return nil
}

// roomSafeRecallSpan discards stale derived cache text, never archive data or
// independently addressed live inputs. A real new recall reads the projected
// archive again and binds its current revision; raw/unbound cache cannot bypass it.
func (al *AgentLoop) roomSafeRecallSpan(ts *turnState) *RecallSpan {
	span := al.activeRecallSpan(ts.sessionKey)
	if span == nil || ts.agent == nil {
		return span
	}
	source, known := ts.agent.Sessions.(roomRecallProjectionSource)
	if !known {
		return span
	}
	revision := source.RoomProjectionRevision()
	if (span.roomProjectionBound && span.roomProjectionRevision == revision) || (!span.roomProjectionBound && revision == 0) {
		return span
	}
	al.dropRecallSpan(ts.sessionKey, "room_retired")
	return nil
}

func bindRecallFactProjection(span *RecallSpan, archive ConversationArchiveReader) {
	if source, ok := archive.(roomRecallProjectionSource); ok {
		span.roomProjectionRevision, span.roomProjectionBound = source.RoomProjectionRevision(), true
	}
}
