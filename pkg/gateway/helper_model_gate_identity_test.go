package gateway

// #1537 gate N3: FR-001/002 and Wire identity length 1..255.
// Exercise real hubSyncTap -> publication; no network, timers or guard mocks.
import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/stretchr/testify/require"
)

// hmGateIdentityChild sets an in-process, intentionally untagged payload via
// the same opaque JSON fixture boundary as hmChildPayload (not a wire type).
func hmGateIdentityChild(t *testing.T, payload any, child string) {
	t.Helper()
	identity, err := json.Marshal(map[string]string{"ChildSessionID": child, "child_session_id": child})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(identity, payload))
}

func TestHelperProviderNoticeIdentityGuardKeepsOwnCopy(t *testing.T) {
	cases := []struct {
		name  string
		child string
		agent string
		named bool
	}{
		{"empty-agent", hmChild, "", false},
		{"child-256", strings.Repeat("c", 256), hmAgent, false},
		{"agent-256", hmChild, strings.Repeat("a", 256), false},
		{"child-255", strings.Repeat("c", 255), hmAgent, true},
		{"agent-255", hmChild, strings.Repeat("a", 255), true},
		{"both-255", strings.Repeat("c", 255), strings.Repeat("a", 255), true},
	}
	for _, frameType := range []string{"provider_retry", "provider_status"} {
		for _, tc := range cases {
			t.Run(frameType+"/"+tc.name, func(t *testing.T) {
				h, conns := hmHandler(t)
				own := conns[tc.child]
				if own == nil {
					own = newFakeHubConn(tc.child)
					_, ok := h.hubs.getOrCreate(tc.child).bindLive(own)
					require.True(t, ok, "SETUP: bind boundary helper chat")
				}
				evt := hmRetry(t, agent.EventKindProviderRetry, "rate_limit", "guard-turn", false)
				if frameType == "provider_status" {
					evt = hmStatus(t, "waiting", "guard-turn", false)
				}
				evt.Meta.AgentID = tc.agent
				switch payload := evt.Payload.(type) {
				case agent.LLMRetryPayload:
					hmGateIdentityChild(t, &payload, tc.child)
					evt.Payload = payload
				case agent.ProviderStatusPayload:
					hmGateIdentityChild(t, &payload, tc.child)
					evt.Payload = payload
				default:
					t.Fatalf("SETUP: unexpected notice payload %T", payload)
				}
				h.hubSyncTap(evt)
				ownFrames := hmFrames(t, own)
				require.Len(t, ownFrames, 1, "FR-001: identity rejection must not discard helper's own copy")
				require.Equal(t, frameType, ownFrames[0]["type"])
				require.Equal(t, tc.child, ownFrames[0]["session_id"])
				require.Equal(t, "guard-turn", ownFrames[0]["turn_id"])
				require.NotContains(t, ownFrames[0], "child_session_id")
				require.NotContains(t, ownFrames[0], "agent_id")
				top := hmFrames(t, conns[hmRoot])
				if !tc.named {
					require.Empty(t, top, "N3 Wire: empty or >255 identity cannot publish a named copy")
					return
				}
				require.Len(t, top, 1, "N3 positive control: 255 exactly is valid, not an off-by-one rejection")
				require.Equal(t, frameType, top[0]["type"])
				require.Equal(t, hmRoot, top[0]["session_id"])
				require.Equal(t, tc.child, top[0]["child_session_id"])
				require.Equal(t, tc.agent, top[0]["agent_id"])
			})
		}
	}
}
