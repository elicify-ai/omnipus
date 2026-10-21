package agent

import (
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// HelperEventFrameFor is the one conversion used for live publication and
// gateway replay. Both wire payloads are generated from the approved contract.
func HelperEventFrameFor(sessionID, messageID string, event generated.HelperEvent) generated.HelperEventFrame {
	return generated.HelperEventFrame{Type: "helper_event", SessionId: sessionID, MessageId: messageID,
		HelperEvent: generated.HelperEventFrameEvent{
			HelperSessionId: event.HelperSessionId, HelperTitle: event.HelperTitle,
			Kind: string(event.Kind), Text: event.Text, GoalId: event.GoalId, CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano),
		}}
}

func helperEventMessageID(destination, sourceID string, kind generated.HelperEventKind) string {
	return fmt.Sprintf("helper-event-%s-%s-%s", kind, destination, sourceID)
}

func (al *AgentLoop) saveHelperEvent(destination, sourceID string, event generated.HelperEvent) error {
	if sourceID == "" {
		return fmt.Errorf("helper event: source id is required")
	}
	return al.saveHelperEventRow(destination, helperEventMessageID(destination, sourceID, event.Kind), event)
}

func (al *AgentLoop) saveHelperEventRow(destination, id string, event generated.HelperEvent) error {
	store := al.GetSessionStore()
	if store == nil {
		return fmt.Errorf("helper event: session store not configured")
	}
	meta, err := store.GetMeta(destination)
	if err != nil {
		return fmt.Errorf("helper event: recipient %q unavailable: %w", destination, err)
	}
	event.Text = al.filterWakeText(event.Text)
	if strings.TrimSpace(event.Text) == "" || event.HelperSessionId == "" || id == "" {
		return fmt.Errorf("helper event: source, helper and text are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	written, err := store.AppendTranscriptOnce(destination, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeSystem, Role: "system", Content: event.Text,
		AgentID: meta.AgentID, Timestamp: event.CreatedAt, SystemSubtype: "helper_event", HelperEvent: &event,
	})
	if err != nil {
		return fmt.Errorf("save helper event %q: %w", id, err)
	}
	if written {
		al.emitEvent(EventKindHelperEvent, EventMeta{Source: "helper_event"}, HelperEventFrameFor(destination, id, event))
	}
	return nil
}

func (al *AgentLoop) filterWakeText(text string) string {
	text = truncateAsyncNotifyContent(text)
	if cfg := al.GetConfig(); cfg != nil {
		text = cfg.FilterSensitiveData(text)
	}
	return text
}
