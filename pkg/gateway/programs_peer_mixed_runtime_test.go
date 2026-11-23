package gateway

// Spec rev8 S80 / I-2 / FR010 / FR041: consume two actual inputs before
// the registered peer tool sends. The test never constructs their joined carrier.
import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only the model edge is held; the bus, enqueue, consumption and turn stay real.
type programsPeerHeldProvider struct {
	*programsPeerProvider
	entered, release chan struct{}
	once             sync.Once
}

func (p *programsPeerHeldProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.programsPeerProvider.Chat(ctx, messages, definitions, model, options)
}

func TestProgramProvenance_ActualMixedPeerRequest(t *testing.T) {
	t.Run("S80_mixed_provenance_through_a_peer_request", func(t *testing.T) {
		f := newProgramsPeerFixture(t)
		cfg := f.al.GetConfig()
		telegram := cfg.Channels["telegram"]
		telegram.ProgramsAllowed = true
		cfg.Channels["telegram"] = telegram
		cfg.Channels["discord"] = config.ChannelInstanceConfig{Type: "discord", WorkspaceID: "sales", ProgramsAllowed: false}
		controlPath := f.scriptCount(f.ops, "mixed-peer-positive")
		require.NoError(t, f.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: f.ops, ChatID: f.ops, Content: "positive connected Ops child", Provenance: bus.NewProvenance(bus.OriginWeb)}))
		f.waitEnds(t, f.ops)
		control, result := programsPeerCount(t, f, controlPath, "mixed-peer-positive")
		require.Equal(t, "16", control, "S80 instrument: the actual Ops web child receives the connected token")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		f.provider.mu.Lock()
		f.provider.origins[f.ops] = nil
		f.provider.mu.Unlock()

		held := &programsPeerHeldProvider{programsPeerProvider: f.provider, entered: make(chan struct{}), release: make(chan struct{})}
		dev, ok := f.al.GetRegistry().GetAgent("dev")
		require.True(t, ok)
		dev.Provider = held
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(held.release) }) }
		t.Cleanup(release)
		path := f.scriptCount(f.ops, "mixed-peer-received")
		f.provider.setScript(f.dev, []providers.LLMResponse{
			{ToolCalls: []providers.ToolCall{{ID: "mixed-first-boundary", Type: "function", Name: "bash", Arguments: map[string]any{"command": "printf S80_FIRST_CHILD_RAN"}}}},
			{ToolCalls: []providers.ToolCall{{ID: "mixed-peer-send", Type: "function", Name: "send_message", Arguments: map[string]any{"workspace_id": "sales", "agent_id": "ops", "content": "run printenv GH_TOKEN | wc -c"}}}},
			wu5StopGW("mixed peer request sent"),
		})
		const first = "S80 Telegram input: prepare the Ops request"
		const second = "S80 Discord input: now send the Ops request"
		input := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram", SessionID: f.dev, ChatID: f.dev, Content: first, Sender: bus.SenderInfo{CanonicalID: "telegram:s1", DisplayName: "S1 person"}, Provenance: bus.NewProvenance(bus.ConnectorOrigin("telegram"))}
		require.NoError(t, f.bus.PublishInbound(context.Background(), input))
		select {
		case <-held.entered:
		case <-time.After(15 * time.Second):
			t.Fatal("actual Dev turn never reached the held model boundary")
		}
		input.Channel, input.InstanceID, input.Content = "discord", "discord", second
		input.Sender.CanonicalID = "discord:s1"
		input.Provenance = bus.NewProvenance(bus.ConnectorOrigin("discord"))
		require.NoError(t, f.bus.PublishInbound(context.Background(), input))
		var queuedTurn string
		deadline := time.After(15 * time.Second)
		for queuedTurn == "" {
			select {
			case event := <-f.events.C:
				if event.Kind != agent.EventKindInterruptReceived || event.Meta.AgentID != "dev" {
					continue
				}
				payload, ok := event.Payload.(agent.InterruptReceivedPayload)
				require.True(t, ok)
				require.Equal(t, agent.InterruptKindSteering, payload.Kind)
				require.Equal(t, len(second), payload.ContentLen)
				require.Equal(t, 1, payload.QueueDepth, "instrument: the actual second input is admitted before the model is released")
				require.NotEmpty(t, event.Meta.TurnID)
				queuedTurn = event.Meta.TurnID
			case <-deadline:
				t.Fatal("actual Discord input never reached the live Dev steering queue")
			}
		}
		release()
		var endedDevTurn string
		pending := map[string]bool{f.dev: true, f.ops: true}
		deadline = time.After(15 * time.Second)
		for len(pending) > 0 {
			select {
			case event := <-f.events.C:
				if event.Kind != agent.EventKindTurnEnd {
					continue
				}
				payload, ok := event.Payload.(agent.TurnEndPayload)
				require.True(t, ok)
				if pending[payload.SessionID] {
					require.Equal(t, agent.TurnEndStatusCompleted, payload.Status)
					if payload.SessionID == f.dev {
						endedDevTurn = event.Meta.TurnID
					}
					delete(pending, payload.SessionID)
				}
			case <-deadline:
				t.Fatalf("actual mixed-input peer turns did not complete: %v", pending)
			}
		}
		require.Equal(t, queuedTurn, endedDevTurn, "instrument: one actual Dev turn consumed the queued input and sent the peer request")
		f.provider.mu.Lock()
		devPrompts := append([][]providers.Message(nil), f.provider.prompts[f.dev]...)
		f.provider.mu.Unlock()
		// The real reply router can start a later reminder turn. Inspect the
		// three scripted calls of the identified sending turn, not the total
		// lifetime call count (which is outside S80's promised behavior).
		require.GreaterOrEqual(t, len(devPrompts), 3, "instrument: initial boundary, actual peer-tool request, actual tool-result consumption")
		var consumed string
		for _, message := range devPrompts[1] {
			if message.Role == "user" {
				consumed += message.Content + "\n"
			}
		}
		require.Contains(t, consumed, first, "instrument: sending model request consumed the actual Telegram input")
		require.Contains(t, consumed, second, "instrument: sending model request consumed the actual Discord input")
		require.Contains(t, f.provider.result(f.dev, "mixed-first-boundary"), "S80_FIRST_CHILD_RAN")
		expected := bus.NewProvenance(bus.ConnectorOrigin("telegram"), bus.ConnectorOrigin("discord"))
		devOrigins := f.provider.observedOrigins(f.dev)
		require.GreaterOrEqual(t, len(devOrigins), 3)
		assert.Equal(t, bus.NewProvenance(bus.ConnectorOrigin("telegram")), devOrigins[0], "instrument: the original sending turn started from the actual Telegram input")
		assert.Equal(t, expected, devOrigins[1], "I-2: the actual sending turn joins both consumed origins")
		assert.Equal(t, expected, devOrigins[2], "I-2: the same sending turn preserves both origins while consuming the actual peer-tool result")
		origins := f.provider.observedOrigins(f.ops)
		require.GreaterOrEqual(t, len(origins), 2)
		assert.Equal(t, expected, origins[0], "S80: actual receiving command request retains both origins")
		assert.Equal(t, expected, origins[1], "S80: actual receiving result consumption retains both origins")
		count, result := programsPeerCount(t, f, path, "mixed-peer-received")
		assert.Equal(t, "0", count, "S80: Discord off withholds despite Telegram allowed")
		assert.Contains(t, result, "Connected programs were not used for this command: programs are off for work that came from discord. A person can switch them on for this channel on the Connectors page.", "S80/FR012 exact notice names the disallowed origin, never Telegram or unresolved")
		entries, err := f.al.GetSessionStore().ReadTranscript(f.ops)
		require.NoError(t, err)
		var captures int
		for _, entry := range entries {
			capture, found, err := f.al.RequestLedger().Get(f.ops, entry.ID)
			require.NoError(t, err)
			if !found {
				continue
			}
			captures++
			assert.ElementsMatch(t, []string{"connector:telegram", "connector:discord"}, capture.Provenance, "I-2: actual peer ledger preserves the full origin set")
			assert.Contains(t, capture.Text, "run printenv GH_TOKEN | wc -c", "instrument: the real captured request includes the exact original command body")
		}
		require.Equal(t, 1, captures, "instrument: inspect one actual admitted peer request, not an empty ledger")
		t.Logf("actual mixed peer captures=%d child_ran=true count=%s sender_origins=%v receiver_origins=%v same_dev_turn=true", captures, count, devOrigins[1], origins[:2])
	})
}
