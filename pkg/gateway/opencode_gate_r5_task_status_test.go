package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// C2 / team-lead follow-up: whichever intake stage refuses a task-chat
// message, exactly one failed tick reconciles that original sender row.
func TestOpenCodeR5_TaskChatRefusalMarksOriginalMessageFailedExactlyOnce(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "owner_resolved"
		if explicit {
			name = "explicit_worker"
		}
		t.Run(name, func(t *testing.T) {
			f := opencodeR4GatewayFixture(t, "opencode")
			opencodeR5Send(t, f, f.sid, f.task.WorkspaceID, explicit)
			var refusals []generated.ErrorFrame
			var statuses []generated.MessageStatusFrame
			for len(f.conn.sendCh) > 0 {
				raw := <-f.conn.sendCh
				var envelope wsTypeOnly
				require.NoError(t, json.Unmarshal(raw, &envelope))
				switch envelope.Type {
				case string(generated.WsFrameTypeError):
					var frame generated.ErrorFrame
					require.NoError(t, json.Unmarshal(raw, &frame))
					refusals = append(refusals, frame)
				case string(generated.WsFrameTypeMessageStatus):
					var frame generated.MessageStatusFrame
					require.NoError(t, json.Unmarshal(raw, &frame))
					statuses = append(statuses, frame)
				}
			}
			require.Len(t, refusals, 1, "instrument: the real intake actually refused the message")
			assert.Equal(t, opencodeR4TaskChatSentence("opencode"), refusals[0].Message)
			require.Len(t, statuses, 1, "C2: exactly one failed tick, never a direct plus deferred duplicate")
			assert.Equal(t, "failed", statuses[0].State)
			assert.Equal(t, f.sid, statuses[0].SessionId)
			assert.Equal(t, opencodeR5Client, statuses[0].ClientMessageId)
			f.assertNoAdmission(t)
		})
	}
}
