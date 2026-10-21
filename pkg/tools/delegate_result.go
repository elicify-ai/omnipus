// delegate_result.go: action "result" — read a finished child's final
// hand-back again, after its inbox message was already consumed.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// delegateInboxEntriesStore is the raw-entry read the production inbox
// (*session.MessageInboxStore) offers: messages AND ack lines, with no ack
// filtering, so an already-consumed final is still found.
type delegateInboxEntriesStore interface {
	Entries(ownerKey string) ([]session.InboxEntry, error)
}

// executeResult implements action:"result" (issue #1306, UAT R1): a parent
// asks for a finished child's final result explicitly. status deliberately
// does not repeat a final the parent already received (A5: poll OR wake,
// never both), and the inbox shows only unacknowledged messages, so before
// this a parent whose wake consumed the final had no way to read it again.
//
// The text returned is the final hand-back's result — for a goal-bearing
// helper, everything its claiming turn wrote, not its claim evidence
// (pkg/agent/goal_upward_answer.go). Reading it marks the final as received
// exactly as status does, so a wake still pending does not hand it over a
// second time.
func (t *DelegateTool) executeResult(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, err := t.lifecycle.Load(sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sessionID))
	}
	if ownErr := t.verifyCallerOwnsSession(ctx, rec); ownErr != nil {
		return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sessionID))
	}
	state := delegateStateName(rec)
	if !session.IsTerminalLifecycleState(rec.State) && rec.State != session.LifecycleStopped {
		return NewToolResult(fmt.Sprintf("%s, no final result yet — use status to follow it", state))
	}
	msg, found, err := t.finalHandback(rec)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: result: %v", err)).WithError(err)
	}
	if !found {
		return NewToolResult(fmt.Sprintf("%s, no final result was handed back by this session", state))
	}
	if err := t.checkFinalDispositions(rec, []generated.SessionMessage{*msg}); err != nil {
		return delegateDispositionResult(state, err)
	}
	_, note := t.recordFinalsRead(ctx, rec, []generated.SessionMessage{*msg})
	if err := t.checkFinalDispositions(rec, []generated.SessionMessage{*msg}); err != nil {
		return delegateDispositionResult(state, err)
	}
	text := "(the final hand-back carried no result text)"
	if handback, decodeErr := msg.AsSessionMessageHandback(); decodeErr == nil && strings.TrimSpace(handback.ResultSoFar) != "" {
		text = handback.ResultSoFar
	}
	return NewToolResult(fmt.Sprintf("%s — final result:\n%s", state, text) + note)
}

// finalHandback finds rec's final hand-back: the newest inbox message whose
// id is the session's "<child>:<generation>:final" identity, acknowledged or
// not. If the inbox no longer holds it (compaction), the record's own
// committed outbox payload is used while it is retained. found=false means
// no final was ever handed back (e.g. a stopped child).
func (t *DelegateTool) finalHandback(rec *session.LifecycleRecord) (*generated.SessionMessage, bool, error) {
	parentID := rec.SteeringSessionID()
	if t.inbox != nil && parentID != "" {
		var candidates []generated.SessionMessage
		if store, ok := t.inbox.(delegateInboxEntriesStore); ok {
			entries, entriesErr := store.Entries(parentID)
			if entriesErr != nil {
				return nil, false, fmt.Errorf("read the parent inbox: %w", entriesErr)
			}
			for _, e := range entries {
				if e.Kind == session.InboxEntryMessage && e.Message != nil {
					candidates = append(candidates, *e.Message)
				}
			}
		} else {
			msgs, _, _, err := t.inbox.Drain(parentID, rec.SessionID, "", 0)
			if err != nil {
				return nil, false, fmt.Errorf("read the parent inbox: %w", err)
			}
			candidates = msgs
		}
		for i := len(candidates) - 1; i >= 0; i-- {
			if isFinalHandbackOf(candidates[i], rec.SessionID) {
				match := candidates[i]
				return &match, true, nil
			}
		}
	}
	if fd := rec.FinalDelivery; fd != nil && len(fd.Payload) > 0 {
		var committed generated.SessionMessage
		if decodeErr := json.Unmarshal(fd.Payload, &committed); decodeErr != nil {
			return nil, false, fmt.Errorf("decode the committed final hand-back: %w", decodeErr)
		}
		if isFinalHandbackOf(committed, rec.SessionID) {
			return &committed, true, nil
		}
	}
	return nil, false, nil
}

// isFinalHandbackOf reports whether msg is childID's final hand-back
// ("<child>:<generation>:final").
func isFinalHandbackOf(msg generated.SessionMessage, childID string) bool {
	raw, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	var id struct {
		MessageID string `json:"message_id"`
		Kind      string `json:"kind"`
	}
	if json.Unmarshal(raw, &id) != nil {
		return false
	}
	return id.Kind == "handback" && strings.HasPrefix(id.MessageID, childID+":") && strings.HasSuffix(id.MessageID, ":final")
}
