package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// runAgentLoop covers every ordinary entry, including continuations. Its
// owner is installed before any admission/provider/window gate can return.
func (al *AgentLoop) runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (resp string, runErr error) {
	if opts.roomCost == nil {
		opts.roomCost = al.newRoomMeterForOptions(opts)
	}
	defer func() { runErr = errors.Join(runErr, al.finishRoomTurn(opts.roomCost, runErr)) }()
	return al.runAgentLoopBody(ctx, agent, opts)
}

// The reconstructed system/steered path does not go through runAgentLoop.
// Give it the identical ownership/cost/settlement boundary, including resolver
// refusal before the native turn body starts.
func (al *AgentLoop) runSteeredTurnBody(ctx context.Context, rec *session.LifecycleRecord, ts *turnState) (result turnResult, runErr error) {
	if ts.opts.roomCost == nil {
		ts.opts.roomCost = al.newRoomMeterForOptions(ts.opts)
	}
	defer func() {
		settleErr := runErr
		if ts.hardAbortRequested() || result.status == TurnEndStatusAborted {
			settleErr = context.Canceled
		}
		runErr = errors.Join(runErr, al.finishRoomTurn(ts.opts.roomCost, settleErr))
	}()
	return al.runSteeredTurnBodyInner(ctx, rec, ts)
}

func (al *AgentLoop) newRoomMeterForOptions(opts processOptions) *roomCostMeter {
	sessionID := opts.TranscriptSessionID
	if sessionID == "" {
		sessionID = sessionIDFromScopeKey(opts.SessionKey)
	}
	m := al.newTurnRoomMeter(bus.InboundMessage{SessionID: sessionID})
	m.scope = opts.SessionKey
	if opts.UserMessage == "" && len(opts.InitialSteeringMessages) > 0 {
		m.inputs = 0
	}
	m.addInputs(opts.InitialSteeringCorrelationIDs, len(opts.InitialSteeringMessages))
	return m
}

// The admitting turn already owns its execution. Use that turn's exact scope:
// reconstructed system wakes key by the concrete session id, while inbound
// turns key by agent/session. Resolving the room wake's route again would put
// its text in a different queue from the system turn that owns it.
func (al *AgentLoop) enqueueHeldRoomInput(msg bus.InboundMessage, meters []*roomCostMeter) error {
	for _, m := range meters {
		if m != nil && m.scope != "" {
			// Spec §6.18: the held group request carries its group origin
			// into the turn that consumes it.
			_, _, err := al.enqueueSteeringMessageEntryWithOrigin(m.scope, msg.Metadata["agent_id"],
				providers.Message{Role: "user", Content: msg.Content}, roomSteerCorrelation(msg.TranscriptEntryID), msg.TranscriptEntryID,
				al.turnApprovalOrigin(msg, msg.SessionID))
			return err
		}
	}
	return al.enqueueSteeringFromMessage(msg)
}

// ownInput records settlement responsibility at the synchronous handover,
// before Delivered is saved. Pricing still counts only inputs actually polled
// by the turn; ownership must not depend on reaching that later poll.
func (m *roomCostMeter) ownInput(requestID, roomID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.joined == nil {
		m.joined = map[string]string{}
	}
	m.joined[requestID] = roomID
}

func (al *AgentLoop) finishRoomTurn(m *roomCostMeter, turnErr error) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.finished {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	al.reportRoomCost(m, m.sessionID)
	if err := al.settleRoomInputs(m, turnErr); err != nil {
		return err
	}
	m.mu.Lock()
	m.finished = true
	m.mu.Unlock()
	return nil
}

// A handed-over input may still be queued when an early gate fails. Drop
// exactly those room copies, not unrelated human steering, before a drain can
// start another turn with work that has already settled.
func (al *AgentLoop) dropRoomSteering(owned map[string]string) {
	q := al.steering
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	keep := func(items []steeringQueueItem) []steeringQueueItem {
		out := items[:0]
		for _, item := range items {
			id, room := strings.CutPrefix(item.correlationID, roomSteerCorrelationPrefix)
			if _, drop := owned[id]; room && drop {
				continue
			}
			out = append(out, item)
		}
		return out
	}
	for scope, items := range q.queues {
		q.queues[scope] = keep(items)
	}
	for _, transition := range q.terminalizing {
		transition.finishingItems = keep(transition.finishingItems)
	}
}

// A checked completion can retain an answered request for mirror repair without
// keeping it scheduled as model input or owing another unanswered reminder.
func (al *AgentLoop) completeRoomInput(rt RoomRuntime, roomID, requestID, sessionID string, turnErr error) error {
	discard, err := rt.RoomConsumeEnded(roomID, requestID, turnErr)
	if err != nil {
		// The room's checked repair journal owns bookkeeping from here. Keep
		// the ledger capability, but never rerun a failed task via a reminder.
		al.settleRequest(requestID)
		return fmt.Errorf("complete group request %s: %w", requestID, err)
	}
	if discard && sessionID != "" {
		if err := al.DiscardRequest(sessionID, requestID); err != nil {
			return fmt.Errorf("retire completed group request %s: %w", requestID, err)
		}
	} else {
		al.settleRequest(requestID)
	}
	return nil
}
