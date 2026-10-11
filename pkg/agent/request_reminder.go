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
// them, so at worst a request admitted before the restart is not reminded.
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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// requestReminderMetadataKey marks the reminder inbound (alongside the
// owner-wake marker that makes it non-human).
const requestReminderMetadataKey = "request_reminder"

// markRequestDelivered records that a turn has been given the request (its own
// wake, or steered into a live turn), so a turn ending earlier cannot remind
// about a request the agent has not seen.
func (al *AgentLoop) markRequestDelivered(transcriptEntryID string) {
	if transcriptEntryID == "" {
		return
	}
	// Every inbound carries a transcript entry id (each human message too); only
	// a pending request is ever owed a reminder, and admission registers it
	// BEFORE its turn can run, so anything else is not recorded.
	if _, pending := al.pendingRequests.Load(transcriptEntryID); !pending {
		return
	}
	al.deliveredRequests.Store(transcriptEntryID, struct{}{})
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
