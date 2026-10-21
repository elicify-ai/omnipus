package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// SetFinalDispositionHook installs a non-consuming read gate. Ownership is
// checked by each action before this hook sees the addressed parent's identity.
func (t *DelegateTool) SetFinalDispositionHook(check func(parentSessionID, messageID string) error) {
	t.finalDisposition = check
}

func delegateMessageID(message generated.SessionMessage) (string, error) {
	data, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return "", err
	}
	var id string
	if err := json.Unmarshal(fields["message_id"], &id); err != nil {
		return "", fmt.Errorf("delegate: invalid message identity: %w", err)
	}
	if id == "" {
		return "", fmt.Errorf("delegate: empty message identity")
	}
	return id, nil
}

func (t *DelegateTool) checkFinalDispositions(rec *session.LifecycleRecord, messages []generated.SessionMessage) error {
	if t.finalDisposition == nil || rec.SteeringSessionID() == "" {
		return nil
	}
	for _, message := range messages {
		id, err := delegateMessageID(message)
		if err != nil {
			return err
		}
		if err := t.finalDisposition(rec.SteeringSessionID(), id); err != nil {
			return err
		}
	}
	return nil
}

// readableDelegateMessages closes the expiry race after a final-read claim.
// Valid siblings that were claimed are still returned; only a now-expired
// original is removed, with an explicit operational note, never its old body.
func (t *DelegateTool) readableDelegateMessages(rec *session.LifecycleRecord, messages []generated.SessionMessage) ([]generated.SessionMessage, string, error) {
	if t.finalDisposition == nil || rec.SteeringSessionID() == "" {
		return messages, "", nil
	}
	out := make([]generated.SessionMessage, 0, len(messages))
	var expired []string
	for _, message := range messages {
		id, err := delegateMessageID(message)
		if err != nil {
			return nil, "", err
		}
		if err := t.finalDisposition(rec.SteeringSessionID(), id); err != nil {
			if errors.Is(err, session.ErrSavedInputExpired) {
				expired = append(expired, delegateExpiryText(err))
				continue
			}
			return nil, "", err
		}
		out = append(out, message)
	}
	return out, strings.Join(expired, " "), nil
}

func delegateExpiryText(err error) string {
	var expired *session.SavedInputExpiry
	if errors.As(err, &expired) {
		return fmt.Sprintf("Helper report %s expired undelivered by retention in chat %s at %s. The original report will not arrive. This does not change the helper's work state.",
			expired.InputID, expired.SessionID, expired.ExpiredAt.UTC().Format(time.RFC3339))
	}
	return "A helper report expired undelivered by retention. The original report will not arrive. This does not change the helper's work state."
}

func delegateDispositionResult(state string, err error) *ToolResult {
	if errors.Is(err, session.ErrSavedInputExpired) {
		return NewToolResult(strings.TrimSpace(state + " — " + delegateExpiryText(err)))
	}
	return ErrorResult(fmt.Sprintf("delegate: report could not be read: %v", err)).WithError(err)
}
