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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
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
	al.deliveredRequests.Store(transcriptEntryID, struct{}{})
	if _, tracked := al.pendingRequests.Load(transcriptEntryID); !tracked || sessionID == "" {
		return
	}
	if ledger := al.RequestLedger(); ledger != nil {
		if err := ledger.MarkDelivered(sessionID, transcriptEntryID); err != nil {
			logger.WarnCF("agent", "request delivery could not be recorded; it may be delivered again on the next turn",
				map[string]any{"session_id": sessionID, "request_id": transcriptEntryID, "error": err.Error()})
		}
	}
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
		logger.WarnCF("agent", "could not remind the agent about unanswered requests",
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

// receiverStopped reports whether sessionID's lifecycle record is stopped (a
// landed stop or an in-flight fence): the state in which a request does not
// start a turn.
func (al *AgentLoop) receiverStopped(sessionID string) bool {
	lc := al.GetSessionLifecycleStore()
	if lc == nil || sessionID == "" {
		return false
	}
	rec, err := lc.Load(sessionID)
	if err != nil {
		return false
	}
	return rec.Stopped()
}

// deliverHeldRequests runs when a turn of sessionID has just been admitted - the
// receiver "runs again", including a Resume. Every request held for it in the
// ledger is handed to that turn: the request is already in the conversation the
// turn reads, so delivery marks it delivered and tracks it for the unanswered
// reminder. A request whose asker's conversation is gone is discarded and logged
// instead, never delivered into a void.
func (al *AgentLoop) deliverHeldRequests(sessionID string) {
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
		if reason := al.askerUnavailable(c); reason != "" {
			al.pendingRequests.Delete(c.RequestID)
			if dErr := ledger.Discard(sessionID, c.RequestID); dErr != nil {
				logger.ErrorCF("agent", "a held request with no asker could not be discarded",
					map[string]any{"session_id": sessionID, "request_id": c.RequestID, "error": dErr.Error()})
			}
			logger.WarnCF("agent", "held request dropped, not delivered: "+reason,
				map[string]any{"session_id": sessionID, "request_id": c.RequestID, "asker_session_id": c.Source.SessionID})
			continue
		}
		al.pendingRequests.Store(c.RequestID, sessionID)
		al.markRequestDelivered(sessionID, c.RequestID)
	}
}

// askerUnavailable returns why the answer to c could not reach its asker, or ""
// when it can. A connector asker lives on the platform and is not checked here.
func (al *AgentLoop) askerUnavailable(c addressing.Capture) string {
	if c.Source.Kind != addressing.SourceConversation {
		return ""
	}
	store := al.GetSessionStore()
	if store == nil {
		return "the session store is unavailable"
	}
	meta, err := store.GetMeta(c.Source.SessionID)
	if err != nil || meta == nil {
		return "the asker's conversation no longer exists"
	}
	if !c.Source.Owner.IsZero() &&
		(meta.AgentID != c.Source.Owner.AgentID || meta.WorkspaceID != c.Source.Owner.WorkspaceID) {
		return "the asker's conversation no longer belongs to the agent that asked"
	}
	return ""
}
