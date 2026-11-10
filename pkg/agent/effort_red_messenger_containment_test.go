package agent

// C2-04 / S-02: drive the outbound behavior with its own saved-config and
// environment Given. This is deliberately independent of the zero-trace name
// sweep and of S-04's reasoning-only/empty-answer scenario.
import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_C2_S02_LegacyChannelAndEnvNeverDeliverReasoningToAnyDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_CHANNELS_TELEGRAM_REASONING_CHANNEL_ID", "s02-legacy-env-destination")
	path := filepath.Join(home, "config.json")
	raw := []byte(`{"version":1,"channels":{"telegram":{"type":"telegram","enabled":false,"reasoning_channel_id":"s02-legacy-json-destination"}}}`)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err, "S-02 loads the actual saved config and old environment binding")
	require.Equal(t, "telegram", cfg.Channels["telegram"].Type, "instrument: ordinary messenger config loads")
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel.Model = "s02-scripted-model"
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Agents.List = []config.AgentConfig{{ID: testDefaultAgentID, Home: home}}
	msgBus := bus.NewMessageBus()
	prov := &effortScriptedProvider{
		content: "S02-VISIBLE-ANSWER-7731", reasoning: redMarker + " private S02 reasoning", finish: "stop",
	}
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(al.Close)
	manager, err := channels.NewManager(cfg, credentials.SecretBundle{}, msgBus, nil)
	require.NoError(t, err)
	// Only the external connector is a stub. Giving it the old cached
	// destination makes a surviving publication path observable, even after
	// the config decoder correctly stops retaining the obsolete key.
	manager.RegisterChannel("telegram", &fakeChannel{id: "s02-legacy-env-destination"})
	al.SetChannelManager(manager)
	// Exercise the ordinary session-worker delivery owner, not just the
	// processMessage helper (which returns text without publishing the reply).
	// Admission/routing selection is outside this test's outbound boundary.
	worker := newSessionWorker("agent:"+testDefaultAgentID+":telegram:s02-person", al, func() {})
	t.Cleanup(worker.cancel)
	worker.processTurn(context.Background(), bus.InboundMessage{
		Channel: "telegram", ChatID: "s02-person", Sender: bus.SenderInfo{CanonicalID: "s02-user"}, Content: "answer normally",
		Metadata: map[string]string{"agent_id": testDefaultAgentID},
	})
	require.Equal(t, 1, prov.callCount(), "instrument: the model actually returned answer plus reasoning")

	// Watch the common outbound bus, with NO channel/chat-id filter: a leak
	// under a differently named route must still fail. The turn's current
	// detached reasoning publisher is not joined at return. Observe for the
	// package's existing 10s outbound-test budget, not a sleep followed by an
	// instantaneous empty read. This is a bounded delivery observation, not
	// a proof about work scheduled beyond that window; CHECK retains that gap.
	observation, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type delivered struct{ Channel, ChatID, Content string }
	var all []delivered
	for {
		select {
		case outbound := <-msgBus.OutboundChan():
			all = append(all, delivered{outbound.Channel, outbound.ChatID, outbound.Content})
			assert.NotContains(t, outbound.Content, redMarker, "S-02: no reasoning reaches ANY destination, including renamed publish routes")
		case <-observation.Done():
			assert.Equal(t, []delivered{{"telegram", "s02-person", "S02-VISIBLE-ANSWER-7731"}}, all,
				"S-02: exactly the ordinary answer, with no separate reasoning message on any channel")
			return
		}
	}
}
