package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real socket outcome, not a validator call and not a timeout as oracle:
// pre-change code returns user_message, which is an observable wrong result.
func l1WU1WSMintOutcome(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "WS must return a visible accepted user-message or refusal outcome")
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "error" || frame["type"] == "user_message" {
			return frame
		}
	}
}

func TestL1WU1WebSocketNewChat_RequiresNamedAgentValidationOnOff(t *testing.T) {
	// FR-122 / BDD-13.3 / §7.2: no first-agent/default fallback, either mode.
	// Chief oracle ruling 2026-10-13: schema-on empty/null values name
	// MessageFrame; omitted/whitespace and schema-off missing-agent cases
	// use the exact semantic wording. Every refusal is zero-mint/zero-dispatch.
	for _, validation := range []bool{false, true} {
		mode := map[bool]string{false: "validation_off", true: "validation_on"}[validation]
		t.Run(mode, func(t *testing.T) {
			for _, row := range []struct{ name, body string }{
				{"omitted", `{"type":"message","content":"unaddressed"}`},
				{"empty", `{"type":"message","content":"unaddressed","agent_id":""}`},
				{"whitespace", `{"type":"message","content":"unaddressed","agent_id":"  "}`},
				{"null", `{"type":"message","content":"unaddressed","agent_id":null}`},
			} {
				t.Run(row.name, func(t *testing.T) {
					handler, msgBus, al := newTestWSHandler(t)
					al.GetConfig().Gateway.ValidateInbound = validation
					srv := httptest.NewServer(handler)
					conn := dialTestWS(t, srv)
					defer func() { require.NoError(t, conn.Close()); srv.Close(); handler.Wait() }()
					sendWSAuthFrameDevMode(t, conn)
					// Auth is the first ordered socket frame; this legacy path sends no auth acknowledgement.
					before, err := al.GetSessionStore().ListSessions()
					require.NoError(t, err)
					require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(row.body)))
					outcome := l1WU1WSMintOutcome(t, conn)
					assert.Equal(t, "error", outcome["type"], "FR-122: a missing agent must refuse, not mint a default-owned chat: %v", outcome)
					if validation && (row.name == "empty" || row.name == "null") {
						assert.Contains(t, outcome["message"], "MessageFrame", "schema-invalid values remain a visible schema refusal")
					} else {
						assert.Equal(t, "an agent must be named", outcome["message"])
					}
					after, err := al.GetSessionStore().ListSessions()
					require.NoError(t, err)
					assert.Equal(t, before, after, "WS refusal must not mint any session")
					// Once the response has arrived, bus publication is directly
					// inspectable; do not infer zero dispatch from a timed wait.
					if outcome["type"] == "error" {
						select {
						case msg := <-msgBus.InboundChan():
							t.Errorf("unaddressed message reached agent dispatch: %+v", msg)
						default:
						}
					}
				})
			}
			t.Run("explicit_agent_positive_control", func(t *testing.T) {
				handler, msgBus, al := newTestWSHandler(t)
				al.GetConfig().Gateway.ValidateInbound = validation
				srv := httptest.NewServer(handler)
				conn := dialTestWS(t, srv)
				defer func() { require.NoError(t, conn.Close()); srv.Close(); handler.Wait() }()
				sendWSAuthFrameDevMode(t, conn)
				// Auth is the first ordered socket frame; this legacy path sends no auth acknowledgement.
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"message","content":"explicit owner","agent_id":"mia"}`)))
				outcome := l1WU1WSMintOutcome(t, conn)
				require.Equal(t, "user_message", outcome["type"], "positive control must reach minting: %v", outcome)
				msg := awaitInboundMessage(t, msgBus, "explicitly named Mia WS message")
				assert.Equal(t, "explicit owner", msg.Content)
				require.NotEmpty(t, msg.SessionID)
				meta, err := al.GetSessionStore().GetMeta(msg.SessionID)
				require.NoError(t, err)
				assert.Equal(t, "mia", meta.AgentID)
			})
		})
	}
}

func TestL1WU1WebSocketOmnipus_NoSecondConversationAndRefusalNamesMain(t *testing.T) {
	// FR-005 / BDD-1.7 / F04 / M27: an explicitly supplied default
	// workspace cannot bypass the one-conversation guard, in either mode.
	for _, validation := range []bool{false, true} {
		for _, workspace := range []string{"", "default"} {
			mode := map[bool]string{false: "off", true: "on"}[validation]
			t.Run(mode+"/workspace_"+workspace, func(t *testing.T) {
				handler, msgBus, al := l1WU1IdentitySocket(t, validation)
				main, err := al.GetSessionStore().GetOrCreateMainSession("default", "omnipus")
				require.NoError(t, err)
				conn, finish := l1WU1OpenSocket(t, handler)
				body := map[string]any{"type": "message", "content": "second chat", "agent_id": "omnipus"}
				if workspace != "" {
					l1WU1AssertWorkspaceReceived(t, conn, al, msgBus, workspace)
					body["metadata"] = map[string]string{"workspace_id": workspace}
				}
				before := l1WU1SessionSnapshot(t, al.GetSessionStore())
				require.NoError(t, conn.WriteJSON(body))
				outcome := l1WU1WSMintOutcome(t, conn)
				finish()
				assert.Equal(t, "error", outcome["type"], "FR-005: %v", outcome)
				assert.Contains(t, outcome["message"], "Omnipus has one conversation")
				assert.Contains(t, outcome["message"], main.ID, "refusal must point to Omnipus's existing main")
				assert.Equal(t, before, l1WU1SessionSnapshot(t, al.GetSessionStore()), "zero session/transcript writes")
				l1WU1AssertNoInbound(t, msgBus)
			})
		}
	}
}
