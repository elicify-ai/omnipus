package agent

import (
	"testing"

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

	if err := al.EnqueueSteeringWake(
		"agent:agent-1:session:"+meta.ID,
		"agent-1",
		meta.ID,
		"wake-message-1",
		providers.Message{Role: "user", Content: "child completed"},
	); err != nil {
		t.Fatalf("EnqueueSteeringWake: %v", err)
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
