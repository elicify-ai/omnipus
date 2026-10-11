// ws_message_scoped_error_tag_test.go: S2-04 (combined review). Contract C2:
// an ErrorFrame about ONE message of an existing chat carries that chat's
// session_id together with the message's client_message_id, so the SPA files
// it in that chat and does not end the chat's running turn. The architect's
// ADR-20261012-session-core-delta-amendments-c2-r1-2.md (Part 1) names the mid-turn append failure
// and the client_message_id retry error as covered. ORACLE: that note.

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendAppendFailureError_TagsTheChatAndTheMessage(t *testing.T) {
	handler, _, store, wc := newProvenanceHarness(t)
	minted, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sessionID := minted.ID
	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	require.NoError(t, os.Remove(transcriptPath))
	require.NoError(t, os.Mkdir(transcriptPath, 0o700)) // a directory cannot be appended to

	frames := provenanceSendAuthenticated(t, handler, wc, "c2-append-chat", sessionID, "steer while a turn runs", "c2-append-m1")

	require.NotEmpty(t, frames)
	require.Equal(t, "error", frames[0]["type"], "frames=%v", frames)
	assert.Equal(t, "c2-append-m1", frames[0]["client_message_id"])
	assert.Equal(t, sessionID, frames[0]["session_id"],
		"a failed save of a message in an existing chat is message-scoped: it must name the chat")
	assert.NotContains(t, frames[0], "first_message_error")
}

func TestClientMessageIDPrincipalConflict_TagsTheChat(t *testing.T) {
	handler, _, _, frame, _, _ := issue1090SecurityExistingSend(t)
	sessionID := *frame.SessionId

	foreign := frame
	foreign.Content = "Bob reuses the id"
	_, replies := issue1090SecurityDispatch(t, handler, "1090-b1-bob", "c2-conflict-bob", foreign)

	var errFrame map[string]any
	for _, r := range replies {
		if r["type"] == "error" {
			errFrame = r
		}
	}
	require.NotNil(t, errFrame, "instrument check: the refusal is an error frame; frames=%v", replies)
	assert.Equal(t, *frame.ClientMessageId, errFrame["client_message_id"])
	assert.Equal(t, sessionID, errFrame["session_id"], "message-scoped refusal in an existing chat must name the chat")
}
