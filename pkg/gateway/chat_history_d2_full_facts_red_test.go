package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A2.1/A2.2/B77: full projection means AFTER cancellation/disposition, never
// a private archive envelope and never the display preview of an ordinary row.
func TestHistoryEntryRead_FullProjectionKeepsCancellationAndDisposition(t *testing.T) {
	for _, kind := range []string{"discarded-user", "cancelled-assistant"} {
		t.Run(kind, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			t.Setenv("OMNIPUS_BEARER_TOKEN", "d2-full-facts-token")
			sid := createTestSession(t, api)
			s := api.agentLoop.GetSessionStore()
			role := "assistant"
			if kind == "discarded-user" {
				role = "user"
			}
			body := strings.Repeat("x", 65537)
			addr, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "d2-facts-source", Role: role, AgentID: "mia", Content: body, TurnID: "d2-facts-turn", ClientMessageID: "d2-user-client-id", ViewMembership: session.ViewMembershipChat})
			require.NoError(t, err)
			if kind == "discarded-user" {
				require.NoError(t, s.RecordInputDiscarded(sid, "d2-facts-source", "d2-user-client-id"))
			} else {
				require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "d2-cancel-fact", Type: session.EntryTypeTurnCancelled, Role: "system", TurnID: "d2-facts-turn", Content: "cancelled", ViewMembership: session.ViewMembershipChat}))
			}
			require.NoError(t, s.Close())
			before := pagingProjectionTree(t, api.homePath)
			position := pagingPositionOracle(sid, addr)
			w := d2EntryRequest(api, sid, position, "Bearer d2-full-facts-token", nil)
			require.Equal(t, 200, w.Code)
			var m generated.Message
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
			require.Equal(t, "d2-facts-source", m.Id)
			require.NotNil(t, m.Content)
			require.Equal(t, body, *m.Content)
			require.Nil(t, m.ContentOriginalSizeBytes)
			if kind == "discarded-user" {
				require.NotNil(t, m.InputDisposition)
				require.Equal(t, "discarded", m.InputDisposition.State)
				require.Equal(t, "stopped_before_delivery", m.InputDisposition.Reason)
			} else {
				require.NotNil(t, m.Truncated)
				require.True(t, *m.Truncated)
				require.NotNil(t, m.TruncationReason)
				require.Equal(t, "cancelled", string(*m.TruncationReason))
			}
			require.Equal(t, before, pagingProjectionTree(t, api.homePath))
		})
	}
}
