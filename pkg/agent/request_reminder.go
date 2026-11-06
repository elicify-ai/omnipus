// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// request_reminder.go: an agent that ends a turn with a request still
// unanswered gets ONE visible, non-human reminder to answer it with the
// answer_request tool, never silence. A plain conversational reply reaches
// nobody, so without this the sender simply waits.
//
// Pending requests are tracked in memory from admission (peer request or bound
// connector message) until an accepted answer or a discard. A restart forgets
// them; a request that no turn had yet been given stays held in the ledger and
// is tracked again when the receiver's next turn delivers it
// (deliverHeldRequests).
// The reminder reuses the owner-wake marker and so carries no human authority:
// it can start the next round of a finished chat but never continues a
// stopped one, and it is sent once per request so it cannot loop.

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// requestReminderMetadataKey marks the reminder inbound (alongside the
// owner-wake marker that makes it non-human).
const requestReminderMetadataKey = "request_reminder"

// markRequestDelivered records that a turn has been given the request (its own
// wake, or steered into a live turn), so a turn ending earlier cannot remind
// about a request the agent has not seen. For a tracked request the mark is also
// written to the ledger: a request that is not marked is still held and is
// delivered by the receiver's next turn.
func (al *AgentLoop) markRequestDelivered(sessionID, transcriptEntryID string) {
	if transcriptEntryID == "" {
		return
	}
	// Every inbound carries a transcript entry id (each human message too); only
	// a pending request is ever owed a reminder, and admission registers it
	// BEFORE its turn can run, so anything else is not recorded.
	if _, tracked := al.pendingRequests.Load(transcriptEntryID); !tracked {
		return
	}
	al.deliveredRequests.Store(transcriptEntryID, struct{}{})
	al.heldRepublished.Delete(transcriptEntryID)
	if sessionID == "" {
		return
	}
	if ledger := al.RequestLedger(); ledger != nil {
		// Read and mark in ONE ledger operation: only the FIRST delivery of a
		// request that was held moves its sender's row on, and a failed read
		// marks nothing, so the transition (and the tick) is retried by the
		// next delivery instead of being lost while the request reads delivered.
		prior, first, err := ledger.MarkDeliveredFirst(sessionID, transcriptEntryID)
		if err != nil {
			logger.ErrorCF("agent", "request delivery could not be recorded; it stays undelivered, is retried on the next turn, and its sender's row waits until then",
				map[string]any{"session_id": sessionID, "request_id": transcriptEntryID, "error": err.Error()})
			return
		}
		if first && prior.Held && prior.SenderMessageID != "" &&
			prior.Source.Kind == addressing.SourceConversation && prior.Source.SessionID != "" {
			al.notifyHeldRequestDelivered(prior.Source.SessionID, prior.SenderMessageID)
		}
	}
}

// notifyHeldRequestDelivered tells the installed receiver (the gateway) that a
// held request's sender row can move on.
func (al *AgentLoop) notifyHeldRequestDelivered(senderSessionID, senderMessageID string) {
	hook := al.requestDeliveredHook.Load()
	if hook == nil {
		return
	}
	(*hook)(senderSessionID, senderMessageID)
}

// takeUnansweredRequests returns, for sessionID, the delivered, still-pending
// request ids that have not yet had their one reminder, marking them reminded.
func (al *AgentLoop) takeUnansweredRequests(sessionID string) []string {
	var ids []string
	al.pendingRequests.Range(func(k, v any) bool {
		// pendingRequests maps request id -> session id (both strings, written
		// only by this package); anything else is skipped, never reminded.
		owner, ownerOK := v.(string)
		id, idOK := k.(string)
		if !ownerOK || !idOK || owner != sessionID {
			return true
		}
		if _, delivered := al.deliveredRequests.Load(id); !delivered {
			return true // its turn has not run yet: nothing to remind about
		}
		if _, already := al.remindedRequests.LoadOrStore(id, struct{}{}); !already {
			ids = append(ids, id)
		}
		return true
	})
	sort.Strings(ids)
	return ids
}

// remindUnansweredRequests is called when a turn of sessionID ends.
func (al *AgentLoop) remindUnansweredRequests(sessionID string) {
	if al == nil || al.bus == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	ids := al.takeUnansweredRequests(sessionID)
	if len(ids) == 0 {
		return
	}
	agentID := ""
	if store := al.GetSessionStore(); store != nil {
		if meta, err := store.GetMeta(sessionID); err == nil && meta != nil {
			agentID = meta.AgentID
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := al.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "request-reminder:" + sessionID,
		Sender:    bus.SenderInfo{CanonicalID: "request-reminder:" + sessionID},
		Content:   requestReminderText(ids),
		SessionID: sessionID,
		// Non-human, exactly like the reply-refusal note: never UserInitiated or
		// OperatorPrompt, admitted as the owner-wake principal.
		Metadata: map[string]string{"agent_id": agentID, requestReminderMetadataKey: "1", ownerWakeMetadataKey: "1"},
	}); err != nil {
		// The reminder never went out: give the one reminder back so a later turn
		// end retries, instead of leaving the sender waiting for ever.
		for _, id := range ids {
			al.remindedRequests.Delete(id)
		}
		logger.ErrorCF("agent", "could not remind the agent about unanswered requests - the reminder stays owed",
			map[string]any{"session_id": sessionID, "requests": ids, "error": err.Error()})
	}
}

func requestReminderText(ids []string) string {
	if len(ids) == 1 {
		return fmt.Sprintf("Request %s is still unanswered. Answer it with the answer_request tool "+
			"(request_id=%q and your answer as text); a plain reply does not reach the sender.", ids[0], ids[0])
	}
	return fmt.Sprintf("Requests %s are still unanswered. Answer each with the answer_request tool "+
		"(request_id and your answer as text); a plain reply does not reach the sender.", strings.Join(ids, ", "))
}

// settleRequest ends a request's life in every set that tracks it: answered or
// discarded, it can owe no reminder and must not be remembered. Without this the
// delivered and reminded sets grew for the life of the process.
func (al *AgentLoop) settleRequest(requestID string) {
	al.pendingRequests.Delete(requestID)
	al.deliveredRequests.Delete(requestID)
	al.remindedRequests.Delete(requestID)
}

// receiverStopped reports whether sessionID's lifecycle record is stopped (a
// landed stop or an in-flight fence): the state in which a request does not
// start a turn.
func (al *AgentLoop) receiverStopped(sessionID string) bool {
	lc := al.GetSessionLifecycleStore()
	if lc == nil || sessionID == "" {
		return false
	}
	rec, err := lc.Load(sessionID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return false // an ordinary chat that never ran a steered turn
	}
	if err != nil {
		// The record cannot be read, so "running" is unknown: say held. The
		// request is durably kept either way and its note ("delivered when it
		// runs again") is true whatever the state; admitting silently would
		// tell the asker it was seen.
		logger.WarnCF("agent", "receiver lifecycle record unreadable; the request is reported as held",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return true
	}
	return rec.Stopped()
}

// deliverHeldRequests runs when a turn of sessionID has just been admitted - the
// receiver "runs again", including a Resume. Every request still held for it in
// the ledger is put in front of the model: its text is published as the same
// owner-wake inbound the admission would have started, which the session
// processes like any wake (steered into the live resumed turn, or a turn of its
// own), and THAT processing marks it delivered (markRequestDelivered). The
// request's transcript entry is chat-only, so flipping a flag here would leave
// the model blind to it.
//
// A request whose asker's conversation is definitively gone is discarded and
// logged instead, never delivered into a void. A request whose asker could not
// be read (a transient error) stays held and is retried by the next turn.
func (al *AgentLoop) deliverHeldRequests(sessionID string, skipRequestID ...string) {
	al.deliverHeldRequestsWithMeter(sessionID, nil, skipRequestID...)
}

func (al *AgentLoop) deliverHeldRequestsWithMeter(sessionID string, meter *roomCostMeter, skipRequestID ...string) {
	ledger := al.RequestLedger()
	if ledger == nil || sessionID == "" {
		return
	}
	held, err := ledger.Undelivered(sessionID)
	if err != nil {
		logger.ErrorCF("agent", "held requests could not be read; they stay held",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return
	}
	for _, c := range held {
		if slices.Contains(skipRequestID, c.RequestID) {
			// The turn being admitted IS this request's own wake: it is about to be
			// given to the model, and republishing it (the in-flight marker may be
			// older than its TTL after a long system turn) would show it twice.
			continue
		}
		gone, reason, readErr := al.askerStatus(c)
		if readErr != nil {
			logger.WarnCF("agent", "held request kept: the asker's conversation could not be read; it is retried on the next turn",
				map[string]any{"session_id": sessionID, "request_id": c.RequestID, "asker_session_id": c.Source.SessionID, "error": readErr.Error()})
			continue
		}
		if gone {
			al.pendingRequests.Delete(c.RequestID)
			al.heldRepublished.Delete(c.RequestID)
			if dErr := ledger.Discard(sessionID, c.RequestID); dErr != nil {
				logger.ErrorCF("agent", "a held request with no asker could not be discarded",
					map[string]any{"session_id": sessionID, "request_id": c.RequestID, "error": dErr.Error()})
			}
			logger.WarnCF("agent", "held request dropped, not delivered: "+reason,
				map[string]any{"session_id": sessionID, "request_id": c.RequestID, "asker_session_id": c.Source.SessionID})
			continue
		}
		al.pendingRequests.Store(c.RequestID, sessionID)
		if c.Source.Kind == addressing.SourceRoom {
			al.deliverHeldRoomRequest(sessionID, c, meter)
			continue
		}
		if c.Text == "" {
			// No stored text (a connector capture, whose message already
			// arrived as an inbound turn): nothing to republish.
			al.markRequestDelivered(sessionID, c.RequestID)
			continue
		}
		if !al.claimWakeSlot(c.RequestID) {
			continue // its wake is already on its way
		}
		if pubErr := al.publishHeldRequestWake(c); pubErr != nil {
			al.heldRepublished.Delete(c.RequestID)
			logger.WarnCF("agent", "held request could not be re-published; it stays held and is retried on the next turn",
				map[string]any{"session_id": sessionID, "request_id": c.RequestID, "error": pubErr.Error()})
		}
	}
}

// heldWakeInFlightTTL is how long a published request wake counts as "on its
// way". A wake can be lost without any signal (full worker inbox, a panic, the
// worker exiting before it drains); after this long the request is published
// again instead of staying skipped until the gateway restarts.
const heldWakeInFlightTTL = time.Minute

// claimWakeSlot reports whether the caller may publish requestID's wake now:
// true (and the slot is stamped) when no wake was published within
// heldWakeInFlightTTL, false while one is presumed in flight.
func (al *AgentLoop) claimWakeSlot(requestID string) bool {
	now := time.Now()
	for {
		prev, loaded := al.heldRepublished.Load(requestID)
		if !loaded {
			if _, raced := al.heldRepublished.LoadOrStore(requestID, now); !raced {
				return true
			}
			continue
		}
		at, _ := prev.(time.Time)
		if now.Sub(at) < heldWakeInFlightTTL {
			return false
		}
		if al.heldRepublished.CompareAndSwap(requestID, prev, now) {
			return true
		}
	}
}

// publishHeldRequestWake re-publishes the owner wake for a held request through
// the same publishRequestWake the admission uses.
func (al *AgentLoop) publishHeldRequestWake(c addressing.Capture) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return al.publishCaptureWake(ctx, c)
}

// askerStatus reports whether the answer to c can no longer reach its asker.
// gone is a definitive finding (reason says why); readErr is an unreadable
// asker - not evidence that it is gone. A connector asker lives on the platform
// and is not checked here.
func (al *AgentLoop) askerStatus(c addressing.Capture) (gone bool, reason string, readErr error) {
	if c.Source.Kind != addressing.SourceConversation {
		return false, "", nil
	}
	store := al.GetSessionStore()
	if store == nil {
		return false, "", errors.New("the session store is unavailable")
	}
	meta, err := store.GetMeta(c.Source.SessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "the asker's conversation no longer exists", nil
		}
		return false, "", err
	}
	if meta == nil {
		return true, "the asker's conversation no longer exists", nil
	}
	if !c.Source.Owner.IsZero() &&
		(meta.AgentID != c.Source.Owner.AgentID || meta.WorkspaceID != c.Source.Owner.WorkspaceID) {
		return true, "the asker's conversation no longer belongs to the agent that asked", nil
	}
	return false, "", nil
}
