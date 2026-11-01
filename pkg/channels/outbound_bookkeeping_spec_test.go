package channels

// T-15b / S-85 cases 1–2. The real manager's sendWithRetry and preSend own
// all state transitions. The fake is only the external editor/deleter/sender.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

type t15DeliveryEvent struct{ kind, chat, id, content string }

type t15BookkeepingAdapter struct {
	*BaseChannel
	events []t15DeliveryEvent
}

func (a *t15BookkeepingAdapter) Start(context.Context) error { return nil }
func (a *t15BookkeepingAdapter) Stop(context.Context) error  { return nil }
func (a *t15BookkeepingAdapter) Send(_ context.Context, msg bus.OutboundMessage) error {
	a.events = append(a.events, t15DeliveryEvent{kind: "send", chat: msg.ChatID, content: msg.Content})
	return nil
}
func (a *t15BookkeepingAdapter) EditMessage(_ context.Context, chat, id, content string) error {
	a.events = append(a.events, t15DeliveryEvent{kind: "edit", chat: chat, id: id, content: content})
	return nil
}
func (a *t15BookkeepingAdapter) DeleteMessage(_ context.Context, chat, id string) error {
	a.events = append(a.events, t15DeliveryEvent{kind: "delete", chat: chat, id: id})
	return nil
}

func t15BookkeepingFixture() (*Manager, *channelWorker, *t15BookkeepingAdapter) {
	cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"discord": {
		Type: "discord", Enabled: true, WorkspaceID: "ws-t15",
		Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"},
		Outbound: &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"discord:222"}},
	}}}
	m := newTestManager()
	m.config = cfg
	m.liveCfg.Store(cfg)
	m.SetPolicySource(func() *config.Config { return cfg })
	a := &t15BookkeepingAdapter{BaseChannel: NewBaseChannel("discord", nil, nil, nil)}
	a.SetRunning(true)
	w := &channelWorker{ch: a, limiter: rate.NewLimiter(rate.Inf, 1)}
	m.channels["discord"] = a
	m.workers["discord"] = w
	return m, w, a
}

func t15BookkeepingStart(t *testing.T) bus.OutboundMessage {
	t.Helper()
	msg := bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: "S1",
		AgentID: "mia", WorkspaceID: "ws-t15", OwnershipChecked: true}
	op := reflect.ValueOf(&msg).Elem().FieldByName("Operation")
	if !op.IsValid() || op.Kind() != reflect.String || !op.CanSet() {
		t.Fatal("BLOCKED: bus.OutboundMessage.Operation not implemented — required by T-15b/S-85/FR-040/FR-044")
	}
	op.SetString("start")
	return msg
}

func TestT15b_StartPreservesReplyPlaceholderAndFinalizedStreamMarker(t *testing.T) {
	for _, marker := range []bool{false, true} {
		name := "placeholder"
		if marker {
			name = "finalized_stream"
		}
		t.Run(name, func(t *testing.T) {
			start := t15BookkeepingStart(t)
			m, worker, adapter := t15BookkeepingFixture()
			// S-85 calls the conversation c1; this fixture uses the valid numeric
			// Discord id 222 so the independent T-3 start gate can succeed.
			m.RecordPlaceholder("discord", "222", "reply-placeholder")
			typingStops, reactionUndos := 0, 0
			m.RecordTypingStop("discord", "222", func() { typingStops++ })
			m.RecordReactionUndo("discord", "222", func() { reactionUndos++ })
			if marker {
				m.streamActive.Store("discord:222", time.Now())
			}
			m.sendWithRetry(context.Background(), "discord", worker, start, false)
			require.Equal(t, []t15DeliveryEvent{{kind: "send", chat: "222", content: "S1"}}, adapter.events,
				"S-85: start posted exactly once; no edit/delete of the reply's pending message")
			require.Equal(t, 0, typingStops, "FR-044: start cannot consume reply typing-stop")
			require.Equal(t, 0, reactionUndos, "FR-044: start cannot consume reply reaction-undo")
			m.sendWithRetry(context.Background(), "discord", worker, bus.OutboundMessage{
				Channel: "discord", ChatID: "222", Content: "R1",
			}, false)
			expected := []t15DeliveryEvent{{kind: "send", chat: "222", content: "S1"}}
			if marker {
				expected = append(expected, t15DeliveryEvent{kind: "delete", chat: "222", id: "reply-placeholder"})
			} else {
				expected = append(expected, t15DeliveryEvent{kind: "edit", chat: "222", id: "reply-placeholder", content: "R1"})
			}
			require.Equal(t, expected, adapter.events, "S-85: reply consumes its own state exactly once; no duplicate R1 Send")
			require.Equal(t, 1, typingStops)
			require.Equal(t, 1, reactionUndos)
			_, placeholderRemains := m.placeholders.Load("discord:222")
			require.False(t, placeholderRemains, "reply, not start, consumed placeholder")
			_, markerRemains := m.streamActive.Load("discord:222")
			require.False(t, markerRemains, "reply consumed its own finalized marker")
		})
	}
}

func TestT15b_OrdinaryReply_ControlKeepsExistingBookkeepingSemantics(t *testing.T) {
	for _, marker := range []bool{false, true} {
		t.Run(map[bool]string{false: "placeholder", true: "stream_marker"}[marker], func(t *testing.T) {
			m, worker, adapter := t15BookkeepingFixture()
			m.RecordPlaceholder("discord", "222", "reply-placeholder")
			if marker {
				m.streamActive.Store("discord:222", time.Now())
			}
			m.sendWithRetry(context.Background(), "discord", worker, bus.OutboundMessage{
				Channel: "discord", ChatID: "222", Content: "R1",
			}, false)
			expected := []t15DeliveryEvent{{kind: "edit", chat: "222", id: "reply-placeholder", content: "R1"}}
			if marker {
				expected = []t15DeliveryEvent{{kind: "delete", chat: "222", id: "reply-placeholder"}}
			}
			require.Equal(t, expected, adapter.events, "positive control: ordinary reply edit/stream-suppression remains unchanged")
		})
	}
}
