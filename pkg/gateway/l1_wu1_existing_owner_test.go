package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1WebSocketExistingSession_ConflictingAgentRefusesBeforeWrites(t *testing.T) {
	// F02 / §6.4 / chief Q1 A: explicit agent_id is not @ addressing.
	// #1330 corrects the old case_equivalent_owner_control acceptance oracle:
	// MIA is an unknown id, NOT an alias for mia, even in an existing chat.
	// Only the WU-1 system-agent exception folds case, pinned to the stored
	// owner spelling. Omitted/exact ids proceed; refusals name BOTH ids and
	// write nothing. Drive raw frames through the real WebSocket ingress so
	// an owner rewrite cannot hide a wrong-case id from the exact AgentLoop.
	for _, validation := range []bool{false, true} {
		for _, row := range []struct {
			name, owner, explicit string
			accepted, unknownID   bool
		}{
			{"omitted_owner_control", "mia", "", true, false},
			{"matching_owner_control", "mia", "mia", true, false},
			{"wrong_case_owner_is_unknown", "mia", "MIA", false, true},
			{"jim_owner_control", "jim", "", true, false},
			{"system_exact_owner_control", "omnipus", "omnipus", true, false},
			{"system_uppercase_exception", "omnipus", "OMNIPUS", true, false},
			{"system_mixed_case_exception", "omnipus", "OmNiPuS", true, false},
			{"conflicting_registered_agent", "mia", "jim", false, false},
			{"conflicting_reverse_agents", "jim", "mia", false, false},
			{"deleted_owner_no_fallback", "deleted", "", false, false},
			{"deleted_owner_explicit_survivor", "deleted", "jim", false, false},
		} {
			mode := map[bool]string{false: "off", true: "on"}[validation]
			t.Run(mode+"/"+row.name, func(t *testing.T) {
				h, msgBus, al := l1WU1IdentitySocket(t, validation)
				if row.unknownID {
					candidate, found := al.GetRegistry().GetAgent(row.explicit)
					require.False(t, found, "#1330 precondition: %q must be an unknown exact id", row.explicit)
					require.Nil(t, candidate, "wrong-case id must not select the stored owner through a registry alias")
				}
				store := al.GetSessionStore()
				var meta *session.UnifiedMeta
				var err error
				if row.owner == "omnipus" {
					// The exception is exercised in Omnipus's actual main chat,
					// not by minting a forbidden second system-agent conversation.
					meta, err = store.GetOrCreateMainSession("default", row.owner)
				} else {
					meta, err = store.NewSession(session.SessionTypeChat, "webchat", row.owner)
				}
				require.NoError(t, err)
				before := l1WU1SessionSnapshot(t, store)
				conn, finish := l1WU1OpenSocket(t, h)
				const content = "existing owner's input"
				body := generated.MessageFrame{
					Type: string(generated.WsFrameTypeMessage), SessionId: &meta.ID, Content: content,
				}
				if row.explicit != "" {
					body.AgentId = &row.explicit
				}
				require.NoError(t, conn.WriteJSON(body))
				outcome := l1WU1WSMintOutcome(t, conn)
				// The echo precedes PublishInbound. An ordered application pong
				// proves the entire preceding intake finished before socket close
				// can cancel it, so zero dispatch is not a teardown false green.
				require.NoError(t, conn.WriteJSON(generated.PingFrame{Type: string(generated.WsFrameTypePing)}))
				for {
					var frame map[string]any
					require.NoError(t, conn.ReadJSON(&frame), "ordered pong must follow completion of message intake")
					if frame["type"] == string(generated.WsFrameTypePong) {
						break
					}
				}
				finish()
				if !row.accepted {
					assert.Equal(t, "error", outcome["type"], "#1330 / WU-1: unknown, conflicting or deleted owner must be refused: %v", outcome)
					assert.Equal(t, meta.ID, outcome["session_id"], "refusal identifies the existing chat")
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
				afterMeta, err := store.GetMeta(meta.ID)
				require.NoError(t, err)
				assert.Equal(t, row.owner, afterMeta.AgentID, "accepted input never rewrites the stored owner spelling")
				entries, err := store.ReadTranscript(meta.ID)
				require.NoError(t, err)
				require.Len(t, entries, 1, "accepted input is durable exactly once")
				assert.Equal(t, "user", entries[0].Role)
				assert.Equal(t, row.owner, entries[0].AgentID)
				assert.Equal(t, content, entries[0].Content)
				assert.Equal(t, entries[0].ID, outcome["id"])
				assert.Equal(t, entries[0].ID, msg.TranscriptEntryID, "dispatch identifies the same durable user entry as the echo")
				l1WU1AssertNoInbound(t, msgBus)
			})
		}
	}
}
