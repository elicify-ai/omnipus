// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// recordingSlackChannel is the fake Slack channel of spec test 38: it is the
// OUTBOUND end of the Slack intake, so the refusal reply can be observed
// where a Slack user would read it (channels.Manager → Channel.Send), not
// merely on the bus.
type recordingSlackChannel struct {
	fakeChannel
	mu   sync.Mutex
	sent []bus.OutboundMessage
	got  chan bus.OutboundMessage
}

func newRecordingSlackChannel() *recordingSlackChannel {
	return &recordingSlackChannel{fakeChannel: fakeChannel{id: "slack"}, got: make(chan bus.OutboundMessage, 8)}
}

func (c *recordingSlackChannel) Name() string { return "slack" }

func (c *recordingSlackChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	c.sent = append(c.sent, msg)
	c.mu.Unlock()
	c.got <- msg
	return nil
}

// boundTestLoop is one running AgentLoop per sub-case, driven through the
// real bus exactly as every intake drives it (bus.PublishInbound → Run's
// dispatcher → session worker → processMessage → publishResponseIfNeeded).
type boundTestLoop struct {
	al       *AgentLoop
	cfg      *config.Config
	msgBus   *bus.MessageBus
	provider *sequenceProvider
	events   EventSubscription
	home     string
}

func newBoundTestLoop(t *testing.T, capChars int, responses ...*providers.LLMResponse) *boundTestLoop {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home}},
		},
	}
	if capChars > 0 {
		cfg.Context.BuiltinSuccessCap = capChars
	}
	msgBus := bus.NewMessageBus()
	provider := newScriptedProvider(responses...)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	events := al.SubscribeEvents(256)
	t.Cleanup(func() { al.UnsubscribeEvents(events.ID) })

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(5 * time.Second):
			t.Error("Run() did not stop within 5 s")
		}
	})
	return &boundTestLoop{al: al, cfg: cfg, msgBus: msgBus, provider: provider, events: events, home: home}
}

func (b *boundTestLoop) publish(t *testing.T, msg bus.InboundMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, b.msgBus.PublishInbound(ctx, msg))
}

func (b *boundTestLoop) awaitOutbound(t *testing.T) bus.OutboundMessage {
	t.Helper()
	select {
	case out := <-b.msgBus.OutboundChan():
		return out
	case <-time.After(10 * time.Second):
		t.Fatal("no outbound reply within 10 s")
		return bus.OutboundMessage{}
	}
}

// drainEvents returns the kinds seen so far (non-blocking).
func (b *boundTestLoop) drainEvents() map[EventKind]int {
	seen := map[EventKind]int{}
	for {
		select {
		case evt := <-b.events.C:
			seen[evt.Kind]++
		default:
			return seen
		}
	}
}

func (b *boundTestLoop) sessionCount(t *testing.T) int {
	t.Helper()
	sessions, err := b.al.GetSessionStore().ListSessions()
	require.NoError(t, err)
	return len(sessions)
}

// assertRefusedBeforeTurn is the FR-015 / SC-005 triple: no transcript
// entry, no turn, no error frame — and no provider call at all.
func (b *boundTestLoop) assertRefusedBeforeTurn(t *testing.T, out bus.OutboundMessage, wantChannel, wantChat string, n, bound int) {
	t.Helper()
	assert.Equal(t, wantChannel, out.Channel, "the refusal must go back on the originating channel")
	assert.Equal(t, wantChat, out.ChatID)
	assert.Contains(t, out.Content, fmt.Sprintf("%d characters", n), "the reply states N")
	assert.Contains(t, out.Content, fmt.Sprintf("limit is %d", bound), "the reply quotes the live limit")
	assert.Equal(t, 0, b.provider.CallCount(), "no LLM call: the message never became a turn")

	seen := b.drainEvents()
	assert.Zero(t, seen[EventKindTurnStart], "no turn registered")
	assert.Zero(t, seen[EventKindError], "no error frame")
	assert.Zero(t, seen[EventKindTurnEnd])
	b.al.activeTurnStates.Range(func(k, _ any) bool {
		t.Errorf("active turn registered under %v", k)
		return true
	})
}

func (b *boundTestLoop) assertTurnRan(t *testing.T, out bus.OutboundMessage, wantContent string) {
	t.Helper()
	assert.Equal(t, wantContent, out.Content)
	assert.Equal(t, 1, b.provider.CallCount(), "the message became a turn and reached the provider once")
	seen := b.drainEvents()
	assert.Equal(t, 1, seen[EventKindTurnStart], "one turn registered")
	assert.Zero(t, seen[EventKindError])
}

// TestProcessMessage_UserMessageBound_AllIntakes is spec test 38 (ADR-066
// D4, FR-015, B-17, B-18, DS-2 #1–#4, SC-005's first clause). The bound is
// enforced in processMessage — the one point every intake funnels through
// — so the same cases run over the three intake shapes: the WebSocket
// handler (webchat + a real session id), the SSE handler (webchat, no
// session id) and a channel (a fake Slack registered with the channel
// manager, whose Send is where the user reads the reply).
func TestProcessMessage_UserMessageBound_AllIntakes(t *testing.T) {
	const bound = config.DefaultBuiltinSuccessCap // 64,000 — tracks the builtin-success cap

	type intake struct {
		name    string
		channel string
		// withSession mints a real session first (the WS handler shape);
		// the SSE handler publishes without one.
		withSession bool
		slack       bool
	}
	intakes := []intake{
		{name: "ws", channel: "webchat", withSession: true},
		{name: "sse", channel: "webchat"},
		{name: "slack", channel: "slack", slack: true},
	}

	for _, in := range intakes {
		t.Run(in.name, func(t *testing.T) {
			// DS-2 #2 / B-17: 64,001 chars → refused on the intake, nothing persisted.
			t.Run("64001 refused", func(t *testing.T) {
				b := newBoundTestLoop(t, 0)
				var slack *recordingSlackChannel
				if in.slack {
					slack = newRecordingSlackChannel()
					b.al.SetChannelManager(newStartedTestChannelManager(t, b.msgBus, media.NewFileMediaStore(), "slack", slack))
				}
				msg := bus.InboundMessage{
					Channel: in.channel, ChatID: "chat-" + in.name,
					Sender:  bus.SenderInfo{CanonicalID: in.name + "-user", DisplayName: "Alice"},
					Content: strings.Repeat("x", bound+1),
				}
				sessionsBefore := b.sessionCount(t)
				var sessionID string
				if in.withSession {
					meta, err := b.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
					require.NoError(t, err)
					sessionID = meta.ID
					msg.SessionID = sessionID
					sessionsBefore = b.sessionCount(t)
				}
				b.publish(t, msg)

				var out bus.OutboundMessage
				if slack != nil {
					select {
					case out = <-slack.got:
					case <-time.After(10 * time.Second):
						t.Fatal("the fake Slack channel never received the refusal")
					}
				} else {
					out = b.awaitOutbound(t)
				}
				b.assertRefusedBeforeTurn(t, out, in.channel, "chat-"+in.name, bound+1, bound)

				// Nothing persisted: no transcript entry on the WS session, no
				// channel session minted for the Slack/SSE message.
				assert.Equal(t, sessionsBefore, b.sessionCount(t), "no session created by the refused message")
				if sessionID != "" {
					entries, err := b.al.GetSessionStore().ReadTranscript(sessionID)
					require.NoError(t, err)
					assert.Empty(t, entries, "no transcript entry for the refused message")
				}
			})

			// DS-2 #1 / B-18: 63,999 and exactly 64,000 → the turn starts.
			for _, n := range []int{bound - 1, bound} {
				t.Run(fmt.Sprintf("%d accepted", n), func(t *testing.T) {
					b := newBoundTestLoop(t, 0, &providers.LLMResponse{Content: "turn ran"})
					msg := bus.InboundMessage{
						Channel: in.channel, ChatID: "chat-" + in.name,
						Sender:  bus.SenderInfo{CanonicalID: in.name + "-user"},
						Content: strings.Repeat("y", n),
					}
					if in.withSession {
						meta, err := b.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
						require.NoError(t, err)
						msg.SessionID = meta.ID
					}
					b.publish(t, msg)
					b.assertTurnRan(t, b.awaitOutbound(t), "turn ran")
				})
			}

			// DS-2 #3 / B-18: 1,000 chars + 3 media refs → the refs are not
			// counted; the turn starts.
			t.Run("media refs not counted", func(t *testing.T) {
				b := newBoundTestLoop(t, 1_000, &providers.LLMResponse{Content: "turn ran"})
				msg := bus.InboundMessage{
					Channel: in.channel, ChatID: "chat-" + in.name,
					Sender:  bus.SenderInfo{CanonicalID: in.name + "-user"},
					Content: strings.Repeat("z", 1_000),
					Media:   []string{"media-ref-one", "media-ref-two", "media-ref-three"},
				}
				if in.withSession {
					meta, err := b.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
					require.NoError(t, err)
					msg.SessionID = meta.ID
				}
				b.publish(t, msg)
				b.assertTurnRan(t, b.awaitOutbound(t), "turn ran")
			})

			// DS-2 #4 / B-18 / US-4.AC3: the bound TRACKS the builtin-success
			// cap — cap 50,000 and a 50,001-char message → refused quoting 50,000.
			t.Run("cap 50000 quoted", func(t *testing.T) {
				b := newBoundTestLoop(t, 50_000)
				msg := bus.InboundMessage{
					Channel: in.channel, ChatID: "chat-" + in.name,
					Sender:  bus.SenderInfo{CanonicalID: in.name + "-user"},
					Content: strings.Repeat("w", 50_001),
				}
				if in.withSession {
					meta, err := b.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
					require.NoError(t, err)
					msg.SessionID = meta.ID
				}
				b.publish(t, msg)
				out := b.awaitOutbound(t)
				b.assertRefusedBeforeTurn(t, out, in.channel, "chat-"+in.name, 50_001, 50_000)
				assert.NotContains(t, out.Content, "64000", "the reply must quote the LIVE cap, not the default")
			})
		})
	}
}

// TestUserMessageBound_Helpers pins the measure and the fallback the
// enforcement site and the early-persisting intakes share.
func TestUserMessageBound_Helpers(t *testing.T) {
	assert.Equal(t, config.DefaultBuiltinSuccessCap, userMessageBound(nil), "nil config → shipped default")
	assert.Equal(t, config.DefaultBuiltinSuccessCap, userMessageBound(&config.Config{}), "unset cap → shipped default, never 0")
	assert.Equal(t, 50_000, userMessageBound(&config.Config{Context: config.ContextSettings{BuiltinSuccessCap: 50_000}}))
	assert.Equal(t, 3, UserMessageChars("héé"), "characters, not bytes")
	var nilLoop *AgentLoop
	assert.Equal(t, config.DefaultBuiltinSuccessCap, nilLoop.UserMessageBound())
}
