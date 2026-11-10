package gateway

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1WebSocketNewChat_ExactAdmissionBeforeWrites(t *testing.T) {
	// #1330 / docs/agents.md "What it is" / round-3 admission ruling:
	// unknown ordinary IDs refuse BEFORE mint, transcript and publication.
	// Chief round-3 Q1 B: requested Omnipus spellings all reach WU-1's
	// singleton REFUSAL naming the canonical main, not a second conversation.
	// FR-122 still refuses an omitted ID, never choosing a default.
	for _, validation := range []bool{false, true} {
		for _, row := range []struct {
			name, requested, owner, refusal string
		}{
			{"wrong_case_is_unknown", "MIA", "", "unknown"},
			{"unregistered_exact_id", "missing-agent", "", "unknown"},
			{"omitted_agent_control", "", "", "missing"},
			{"exact_mia_control", "mia", "mia", ""},
			{"system_exact_control", "omnipus", "", "singleton"},
			{"system_uppercase_exception", "OMNIPUS", "", "singleton"},
			{"system_mixed_case_exception", "OmNiPuS", "", "singleton"},
		} {
			mode := map[bool]string{false: "off", true: "on"}[validation]
			t.Run(mode+"/"+row.name, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				h, msgBus, al := l1WU1IdentitySocket(t, validation)
				if row.refusal == "unknown" {
					candidate, found := al.GetRegistry().GetAgent(row.requested)
					require.False(t, found, "#1330 precondition: %q is not a registered exact ID", row.requested)
					require.Nil(t, candidate, "an unknown exact ID must not be a registry alias")
				}
				store := al.GetSessionStore()
				metas, err := store.ListSessions()
				require.NoError(t, err)
				require.Empty(t, metas, "new-chat control must start without an existing conversation, including Omnipus's main")
				before := l1WU1SessionSnapshot(t, store)
				beforeDisk := l1WU1AdmissionDiskSnapshot(t, store.BaseDir())
				conn, finish := l1WU1OpenSocket(t, h)
				const content = "new chat exact admission"
				body := generated.MessageFrame{Type: string(generated.WsFrameTypeMessage), Content: content}
				if row.requested != "" {
					body.AgentId = &row.requested
				}
				require.NoError(t, conn.WriteJSON(body))
				outcomes, started := l1WU1AdmissionThroughPong(t, conn)
				finish() // ordered pong precedes shutdown: cancellation cannot hide a late write
				require.Len(t, outcomes, 1, "one visible refusal or accepted user-message is required")
				outcome := outcomes[0]
				if row.refusal != "" {
					assert.Equal(t, "error", outcome["type"], "#1330 / FR-122 / WU-1: new-chat admission must refuse: %v", outcome)
					assert.Empty(t, outcome["session_id"], "refusal cannot claim a newly minted session")
					switch row.refusal {
					case "missing":
						assert.Equal(t, "an agent must be named", outcome["message"])
					case "singleton":
						assert.Contains(t, outcome["message"], "Omnipus has one conversation", "Q1 B: every requested spelling reaches the same singleton rule, not unknown-ID refusal")
						assert.Contains(t, outcome["message"], "main-session-default+omnipus", "Q1 B: refusal points to canonical main, never an alias-owned chat")
					case "unknown":
						assert.Contains(t, outcome["message"], row.requested, "visible unknown-ID refusal names the requested ID; no new wire error code is prescribed")
					}
					assert.Empty(t, started, "refusal must not publish a successful session_started acknowledgement")
					assert.Equal(t, before, l1WU1SessionSnapshot(t, store), "refusal mints no session and writes no transcript")
					assert.Equal(t, beforeDisk, l1WU1AdmissionDiskSnapshot(t, store.BaseDir()), "refusal leaves no unlisted session directory or transcript bytes")
					l1WU1AssertNoInbound(t, msgBus)
					return
				}
				require.Equal(t, "user_message", outcome["type"], "registered exact ordinary ID must really accept: %v", outcome)
				require.Len(t, started, 1, "accepted new chat acknowledges exactly one minted session")
				assert.Equal(t, outcome["session_id"], started[0]["session_id"])
				assert.Equal(t, row.owner, outcome["agent_id"], "echo uses canonical ID, never the requested alias")
				assert.Equal(t, content, outcome["content"])
				metas, err = store.ListSessions()
				require.NoError(t, err)
				require.Len(t, metas, 1, "positive control really minted exactly one session")
				assert.Equal(t, metas[0].ID, outcome["session_id"])
				assert.Equal(t, row.owner, metas[0].AgentID, "new session stores only the registered canonical owner")
				msg := awaitInboundMessage(t, msgBus, "new-chat exact-agent positive control")
				assert.Equal(t, metas[0].ID, msg.SessionID)
				assert.Equal(t, row.owner, msg.Metadata["agent_id"], "dispatch targets that same canonical owner")
				assert.Equal(t, content, msg.Content)
				entries, err := store.ReadTranscript(metas[0].ID)
				require.NoError(t, err)
				require.Len(t, entries, 1, "accepted input is durable exactly once")
				assert.Equal(t, "user", entries[0].Role)
				assert.Equal(t, row.owner, entries[0].AgentID)
				assert.Equal(t, content, entries[0].Content)
				assert.Equal(t, entries[0].ID, outcome["id"])
				assert.Equal(t, entries[0].ID, msg.TranscriptEntryID, "echo and dispatch name the same durable user entry")
				l1WU1AssertNoInbound(t, msgBus)
			})
		}
	}
}

// The real read loop handles message intake synchronously before this ping.
// Collect through its application pong, including any early success ack, so
// neither ignored frames nor early teardown can manufacture a zero-write pass.
func l1WU1AdmissionThroughPong(t *testing.T, conn *websocket.Conn) (outcomes, started []map[string]any) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	require.NoError(t, conn.WriteJSON(generated.PingFrame{Type: string(generated.WsFrameTypePing)}))
	for {
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame), "ordered pong must follow completed new-chat intake")
		switch frame["type"] {
		case string(generated.WsFrameTypeError), string(generated.WsFrameTypeUserMessage):
			outcomes = append(outcomes, frame)
		case string(generated.WsFrameTypeSessionStarted):
			started = append(started, frame)
		case string(generated.WsFrameTypePong):
			return outcomes, started
		}
	}
}

// Include directory entries and raw bytes, not only the store's metadata
// cache: even a transcript in an orphaned/unlisted session is a forbidden write.
func l1WU1AdmissionDiskSnapshot(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[rel+"/"] = nil
			return nil
		}
		out[rel], err = os.ReadFile(path)
		return err
	})
	require.NoError(t, err)
	return out
}
