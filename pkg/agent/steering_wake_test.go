package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestSteeringDrain_WritesConsumedMarkerForWake(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	defer store.Close()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "agent-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	al := &AgentLoop{
		steering:           newSteeringQueue(SteeringOneAtATime),
		sharedSessionStore: store,
	}

	if enqErr := al.EnqueueSteeringWake(
		"agent:agent-1:session:"+meta.ID,
		"agent-1",
		meta.ID,
		"wake-message-1",
		providers.Message{Role: "user", Content: "child completed"},
	); enqErr != nil {
		t.Fatalf("EnqueueSteeringWake: %v", enqErr)
	}

	msgs := al.dequeueSteeringMessagesForScopeWithFallback("agent:agent-1:session:" + meta.ID)
	if len(msgs) != 1 || msgs[0].Content != "child completed" {
		t.Fatalf("drained messages = %+v", msgs)
	}
	entries, err := store.ReadTranscript(meta.ID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	markers := 0
	for _, entry := range entries {
		if entry.Content == "consumed wake-message-1" {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("consumed markers = %d, want exactly 1; transcript = %+v", markers, entries)
	}

	if again := al.dequeueSteeringMessagesForScopeWithFallback("agent:agent-1:session:" + meta.ID); again != nil {
		t.Fatalf("second drain = %+v, want nil", again)
	}
	entries, err = store.ReadTranscript(meta.ID)
	if err != nil {
		t.Fatalf("ReadTranscript after second drain: %v", err)
	}
	markers = 0
	for _, entry := range entries {
		if entry.Content == "consumed wake-message-1" {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("second drain duplicated consumed marker: %d", markers)
	}
}

func TestProcessSystemMessage_ConsumedSteeredWakeDoesNotRunTurnAgain(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	defer store.Close()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "agent-1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	lifecycle := session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle"))
	if persistErr := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: meta.ID, Generation: 2, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, AgentID: "agent-1",
		Origin: &session.Origin{Kind: session.OriginKindChat},
	}); persistErr != nil {
		t.Fatalf("Persist: %v", persistErr)
	}
	if appendErr := store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID: "consumed-wake-1", Type: session.EntryTypeSystem, Role: "system", Content: "consumed wake-1",
	}); appendErr != nil {
		t.Fatalf("AppendTranscriptStrict: %v", appendErr)
	}
	al := &AgentLoop{sharedSessionStore: store}
	al.SetSessionMessagingStores(nil, lifecycle)
	response, err := al.processSystemMessage(context.Background(), bus.InboundMessage{
		Channel: "system", AsyncTranscriptSessionID: meta.ID,
		Metadata: map[string]string{"steer_message_id": "wake-1", "steer_generation": "2"},
	})
	if err != nil || response != "" {
		t.Fatalf("consumed wake = (%q, %v), want empty success", response, err)
	}
}
