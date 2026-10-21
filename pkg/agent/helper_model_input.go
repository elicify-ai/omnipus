package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Admission belongs to this boot attempt, not to the immutable interruption
// row. Only the successful normal dispatcher supplies this context receipt.
type restartContinuationKey struct{}
type restartContinuation struct {
	sessionID  string
	generation int
	stopSeq    uint64
}

func (al *AgentLoop) restartNoticeInput(ctx context.Context, rec *session.LifecycleRecord, parentID, outcomeID string, tr session.StoppedTransition) (string, error) {
	if body, found, err := al.savedHelperInput(parentID, outcomeID); err != nil || found {
		return body, err
	}
	current, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		return "", fmt.Errorf("restart notice: read current helper state: %w", err)
	}
	prefix := "Helper " + rec.SessionID + ": Interrupted by a restart. "
	admitted, _ := ctx.Value(restartContinuationKey{}).(restartContinuation)
	switch {
	case current.Is3P:
		return prefix + "The external helper was not resumed.", nil
	case current.Origin != nil && current.Origin.Kind == session.OriginKindTask:
		return prefix + "This task run was not resumed automatically; check the task's status.", nil
	case current.State == session.LifecycleStopped && current.StopNote != nil && current.StopNote.Cause == session.StopCauseRestart && current.StopNote.Seq == tr.StopSeq:
		return prefix + "Automatic continuation did not start; the helper remains stopped.", nil
	case admitted.sessionID == rec.SessionID && admitted.generation == current.Generation && admitted.stopSeq == tr.StopSeq &&
		!lifecycleInFlightStopFence(current) && (current.State == session.LifecycleRunning || current.State == session.LifecycleQueued):
		return prefix + fmt.Sprintf("Automatic continuation has started (current state: %s). This is not a request to resume the helper.", current.State), nil
	default:
		return prefix + fmt.Sprintf("The helper's current state is %s. This notice records the earlier interruption, not a request to resume it.", current.State), nil
	}
}

// A replay must reuse the FIRST saved model input. Reports, goals and current
// lifecycle state can change later; rebuilding their snapshot under the same
// event id would conflict with SaveChatInput's immutable-content guarantee.
func (al *AgentLoop) savedHelperInput(recipient, outcomeID string) (string, bool, error) {
	store := al.GetSessionStore()
	if store == nil {
		return "", false, errors.New("helper input: session store is not configured")
	}
	addr, _, err := store.SavedChatInput(recipient, "saved-input-"+outcomeID)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil // WakeSession reports the unavailable recipient
	}
	if err != nil || addr.EntryID == "" {
		return "", false, err
	}
	entries, err := store.ReadTranscript(recipient)
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		if entry.ID == "saved-input-"+outcomeID {
			return entry.Content, true, nil
		}
	}
	return "", false, fmt.Errorf("saved helper input %s for %s has no readable chat record", outcomeID, recipient)
}

func (al *AgentLoop) upwardModelInput(child *session.LifecycleRecord, msg generated.SessionMessage, recipient, outcomeID string) (string, error) {
	if body, found, err := al.savedHelperInput(recipient, outcomeID); err != nil || found {
		return body, err
	}
	body, err := al.combinedEndingInput(child, msg)
	if err != nil {
		return "", err
	}
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return "", errors.New("helper input: inbox is not configured")
	}
	messages, _, hasMore, err := inbox.Drain(recipient, child.SessionID, "", session.DefaultInboxUnackedMax)
	if err != nil {
		return "", fmt.Errorf("helper input: read stored reports: %w", err)
	}
	var reports []string
	var reportBytes int
	for _, message := range messages {
		kind, err := message.Discriminator()
		if err != nil {
			return "", err
		}
		var text, id string
		var generation *int
		switch kind {
		case "progress":
			v, err := message.AsSessionMessageProgress()
			if err != nil {
				return "", err
			}
			text, id, generation = v.Text, v.MessageId, v.Generation
		case "checkpoint":
			v, err := message.AsSessionMessageCheckpoint()
			if err != nil {
				return "", err
			}
			text, id, generation = v.Summary, v.MessageId, v.Generation
		case "artifact":
			v, err := message.AsSessionMessageArtifact()
			if err != nil {
				return "", err
			}
			text, id, generation = strings.Join(v.Paths, ", "), v.MessageId, v.Generation
		default:
			continue
		}
		// A report without a recorded round is still inbox-readable, but it
		// cannot be used as evidence of the helper's current round.
		if generation == nil || *generation != child.Generation {
			continue
		}
		line := fmt.Sprintf("%s %s: %s", kind, id, text)
		// Reuse the shipped child-message byte budget for extra context; a
		// full inbox must not multiply the size/cost of the final wake by 200.
		if reportBytes+len(line)+1 > session.DefaultChildSendBodyBytes {
			reports = append(reports, "More stored reports are available through delegate inbox; omitted here to keep this input bounded.")
			break
		}
		reportBytes += len(line) + 1
		reports = append(reports, line)
	}
	if hasMore {
		reports = append(reports, "Additional inbox entries remain; use delegate inbox to read the complete retained report history.")
	}
	if len(reports) != 0 {
		body += "\n\nStored reports from helper " + child.SessionID + " (these reports did not wake the parent):\n" + strings.Join(reports, "\n")
	}
	return body, nil
}
