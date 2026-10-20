package systools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestConnectChannel_RestartsAChannelStoppedInPlace is Train 2 gate r1
// architect F04: after Stop ended a pairing, the channel is registered but
// stopped and its config is unchanged, so the reload alone never restarts it.
// An approved connect_channel must explicitly start it again.
func TestConnectChannel_RestartsAChannelStoppedInPlace(t *testing.T) {
	tool, _ := connectRevisionFixture(t, func(*config.Config) {})
	running := false
	ensured := []string{}
	tool.deps.ChannelLiveStatus = func(string) (bool, string, bool) { return running, "", true }
	tool.deps.EnsureChannelStarted = func(id string) (bool, error) {
		ensured = append(ensured, id)
		running = true
		return true, nil
	}
	res := tool.Execute(context.Background(), map[string]any{
		"id": "whatsapp", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
	require.NotNil(t, res)
	require.Equal(t, []string{"whatsapp"}, ensured, "connect_channel must start the stopped channel")
	require.Contains(t, strings.ToLower(res.ForLLM), "pairing not finished", "a restarted WhatsApp channel reports fresh pairing: %s", res.ForLLM)
}
