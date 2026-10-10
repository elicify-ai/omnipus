package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Session-core F15: participant / reply_to_participant are display records.
// Oracles: live and replay carry the same values for the same entry; the
// projection exposes only kind, display_name, source and the agent pair — zero
// route, principal, platform or canonical ids (BDD-12.3 privacy control).

func human(name, source string) *generated.ChatParticipant {
	return &generated.ChatParticipant{Kind: "human", DisplayName: name, Source: &source}
}

func TestParticipant_ReplayCarriesTheEntrysLabels(t *testing.T) {
	entry := session.TranscriptEntry{
		ID: "e1", Role: "assistant", AgentID: "ava", Content: "hi", ReplyToMessageID: "q1",
		Participant: human("Alice", "telegram"), ReplyToParticipant: human("Bob", "slack"),
	}
	sr := &streamReplayState{sessionID: "s1"}
	sr.buildEntryMessage(entry)
	require.NotNil(t, sr.msgFrame.Participant)
	require.NotNil(t, sr.msgFrame.ReplyToParticipant)
	assert.Equal(t, "Alice", sr.msgFrame.Participant.DisplayName)
	assert.Equal(t, "slack", *sr.msgFrame.ReplyToParticipant.Source)
}

func TestParticipant_ProjectionHasNoRouteOrIdentityKeys(t *testing.T) {
	entry := session.TranscriptEntry{
		ID: "e1", Role: "user", AgentID: "ava", Content: "hi",
		Participant: human("Alice", "telegram"), ReplyToParticipant: human("Bob", "slack"),
	}
	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	for _, key := range []string{"participant", "reply_to_participant"} {
		p, ok := doc[key].(map[string]any)
		require.True(t, ok, "%s must be projected", key)
		for k := range p {
			assert.Contains(t, []string{"kind", "display_name", "source", "agent"}, k, "%s.%s is not a display key", key, k)
		}
	}
	for _, banned := range []string{"instance", "chat_id", "platform_id", "canonical", "principal", "ordinal", "message_provenance", "model_message"} {
		assert.False(t, strings.Contains(string(raw), banned), "projection leaks %q: %s", banned, raw)
	}
}

func TestParticipant_LiveUserEntryAndGuestReplyCarryLabels(t *testing.T) {
	h, _, _ := newRecipientHandler(t)
	wc := makeTestConn()
	bindTestConnToSession(h, "chat-x", "sess-live", wc)
	deps := gatewayAddressDeps{h: h}

	deps.PublishUserEntry("sess-live", session.TranscriptEntry{
		ID: "req-1", Role: "user", AgentID: "ray", Content: "request", Timestamp: time.Now(), Participant: human("Alice", "telegram"),
	})
	deps.PublishGuestReply("sess-live", session.TranscriptEntry{
		ID: "rep-1", Role: "assistant", AgentID: "ray", Content: "answer", ReplyToMessageID: "req-1", ReplyToParticipant: human("Alice", "telegram"),
	})

	var sawUser, sawToken bool
	deadline := time.After(2 * time.Second)
	for !(sawUser && sawToken) {
		select {
		case b := <-wc.sendCh:
			var f map[string]any
			require.NoError(t, json.Unmarshal(b, &f))
			switch f["type"] {
			case "user_message":
				p, _ := f["participant"].(map[string]any)
				assert.Equal(t, "Alice", p["display_name"])
				assert.Equal(t, "telegram", p["source"])
				sawUser = true
			case "token":
				p, _ := f["reply_to_participant"].(map[string]any)
				assert.Equal(t, "Alice", p["display_name"])
				assert.Equal(t, "req-1", f["reply_to_message_id"])
				sawToken = true
			}
		case <-deadline:
			t.Fatalf("frames not delivered (user=%v token=%v)", sawUser, sawToken)
		}
	}
}
