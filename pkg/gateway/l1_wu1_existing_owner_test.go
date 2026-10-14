package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1WebSocketExistingSession_ConflictingAgentRefusesBeforeWrites(t *testing.T) {
	// F02 / §6.4 / chief Q1 A: explicit agent_id is not @ addressing.
	// An omitted/matching id proceeds, conflicts name BOTH ids and write nothing.
	for _, validation := range []bool{false, true} {
		for _, row := range []struct {
			name, owner, explicit string
			accepted              bool
		}{
			{"omitted_owner_control", "mia", "", true},
			{"matching_owner_control", "mia", "mia", true},
			{"case_equivalent_owner_control", "mia", "MIA", true},
			{"jim_owner_control", "jim", "", true},
			{"conflicting_registered_agent", "mia", "jim", false},
			{"conflicting_reverse_agents", "jim", "mia", false},
			{"deleted_owner_no_fallback", "deleted", "", false},
			{"deleted_owner_explicit_survivor", "deleted", "jim", false},
		} {
			mode := map[bool]string{false: "off", true: "on"}[validation]
			t.Run(mode+"/"+row.name, func(t *testing.T) {
				h, msgBus, al := l1WU1IdentitySocket(t, validation)
				store := al.GetSessionStore()
				meta, err := store.NewSession(session.SessionTypeChat, "webchat", row.owner)
				require.NoError(t, err)
				before := l1WU1SessionSnapshot(t, store)
				conn, finish := l1WU1OpenSocket(t, h)
				const content = "existing owner's input"
				body := map[string]string{"type": "message", "session_id": meta.ID, "content": content}
				if row.explicit != "" {
					body["agent_id"] = row.explicit
				}
				require.NoError(t, conn.WriteJSON(body))
				outcome := l1WU1WSMintOutcome(t, conn)
				finish()
				if !row.accepted {
					assert.Equal(t, "error", outcome["type"], "conflicting or deleted owner must be refused: %v", outcome)
					assert.Contains(t, outcome["message"], row.owner, "visible refusal names canonical owner")
					if row.explicit != "" {
						assert.Contains(t, outcome["message"], row.explicit, "visible conflict names requested agent too")
					}
					assert.Equal(t, before, l1WU1SessionSnapshot(t, store), "refusal changes no owner/workspace/transcript/session")
					l1WU1AssertNoInbound(t, msgBus)
					return
				}
				require.Equal(t, "user_message", outcome["type"], "matching/omitted owner must really accept: %v", outcome)
				assert.Equal(t, meta.ID, outcome["session_id"])
				assert.Equal(t, row.owner, outcome["agent_id"], "echo uses canonical spelling, never alias")
				assert.Equal(t, content, outcome["content"])
				msg := awaitInboundMessage(t, msgBus, "existing canonical-owner positive control")
				assert.Equal(t, meta.ID, msg.SessionID)
				assert.Equal(t, row.owner, msg.Metadata["agent_id"], "dispatch is pinned to stored owner")
				assert.Equal(t, content, msg.Content)
				entries, err := store.ReadTranscript(meta.ID)
				require.NoError(t, err)
				require.Len(t, entries, 1, "accepted input is durable exactly once")
				assert.Equal(t, "user", entries[0].Role)
				assert.Equal(t, row.owner, entries[0].AgentID)
				assert.Equal(t, content, entries[0].Content)
				assert.Equal(t, entries[0].ID, outcome["id"])
				l1WU1AssertNoInbound(t, msgBus)
			})
		}
	}
}
