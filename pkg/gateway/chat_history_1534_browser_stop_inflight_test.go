package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stop1534WireGate delays one client TCP write, not any gateway/engine method.
// A browser freezes its answer before WebSocket.send queues the cancel; bytes
// can still arrive in the other direction before the server receives that
// request. Channels force this network ordering without a scheduler/time race.
// All actual WebSocket framing, reads, Stop dispatch and persistence stay real.
type stop1534WireGate struct {
	net.Conn
	armed   atomic.Bool
	held    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *stop1534WireGate) Write(p []byte) (int, error) {
	if g.armed.CompareAndSwap(true, false) {
		close(g.held)
		<-g.release
	}
	return g.Conn.Write(p)
}

func (g *stop1534WireGate) open() { g.once.Do(func() { close(g.release) }) }

func (g *stop1534WireGate) Close() error {
	g.open()
	return g.Conn.Close()
}

func stop1534Connect(t *testing.T, h *historyD16Harness) (*websocket.Conn, *stop1534WireGate) {
	t.Helper()
	var gate *stop1534WireGate
	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second, // failure bound, never an elapsed-time oracle
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			gate = &stop1534WireGate{Conn: conn, held: make(chan struct{}), release: make(chan struct{})}
			return gate, nil
		},
	}
	peer, response, err := dialer.Dial("ws"+strings.TrimPrefix(h.server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	require.NoError(t, err)
	h.peers = append(h.peers, peer)
	t.Cleanup(gate.open) // release a failed assertion's writer before the harness joins
	require.NoError(t, peer.WriteJSON(generated.AuthFrame{Type: "auth", Token: "d16-disposable-dev-token"}))
	h.readUntil(peer, func(raw []byte) bool { return historyD16Type(t, raw) == "session_state" })
	return peer, gate
}

// Only the external provider is scripted. The second chunk is released while
// the client Stop is on the wire, BEFORE the server can possibly claim it.
// The older #1534 providers emit their unwanted suffix only AFTER ctx.Done().
type stop1534TransitProvider struct {
	stop   bool
	prefix string
	next   chan struct{}
	finish chan struct{}
	calls  atomic.Int32
}

const (
	stop1534Prefix     = "though we name"
	stop1534InFlight   = " its parts for convenience.\n\nA further paragraph."
	stop1534AfterClaim = " AFTER-SERVER-STOP"
)

func (*stop1534TransitProvider) GetDefaultModel() string { return "d16-frozen-model" }

func (*stop1534TransitProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	return nil, errors.New("SETUP: #1534 requires the real web streaming path, not Chat")
}

func (p *stop1534TransitProvider) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	prefix := p.prefix
	if prefix == "" {
		prefix = stop1534Prefix
	}
	onChunk(prefix)
	select {
	case <-p.next:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	onChunk(prefix + stop1534InFlight)
	if p.stop {
		<-ctx.Done()
		onChunk(prefix + stop1534InFlight + stop1534AfterClaim)
		return nil, ctx.Err()
	}
	select {
	case <-p.finish:
		return &providers.LLMResponse{Content: prefix + stop1534InFlight, FinishReason: "stop"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var _ providers.StreamingProvider = (*stop1534TransitProvider)(nil)

func stop1534ReadToken(t *testing.T, h *historyD16Harness, peer *websocket.Conn) [][]byte {
	t.Helper()
	return h.readUntil(peer, func(raw []byte) bool {
		return historyD16Type(t, raw) == string(generated.WsFrameTypeToken)
	})
}

// Founder 31 Oct Q3=A: the saved answer stops where the SCREEN stopped, not
// where the server eventually receives Stop. Q1=A: no subsequent model round.
// The oracle is the actual socket token snapshot taken before the cancel send,
// never the provider response, accumulator, transcript or previous test output.
// This Go ingress test models the SPA's freeze/discard rule; it does not mount
// React. See outbound-lifecycle.ts::cancelStream and frames.ts::resolveTokenBubbleByMessageId.
func Test1534BrowserStop_InFlightTextMustNotGrowOnRESTOrReplay(t *testing.T) {
	for _, stopped := range []bool{true, false} {
		name := "stop_while_cancel_is_in_flight"
		if !stopped {
			name = "control_without_stop_keeps_all_received_text"
		}
		t.Run(name, func(t *testing.T) {
			home, cfg := historyD16Config(t)
			provider := &stop1534TransitProvider{stop: stopped, next: make(chan struct{}), finish: make(chan struct{})}
			h := newHistoryD16Harness(t, home, cfg, provider, true)
			sid := h.session()
			peer, gate := stop1534Connect(t, h)
			h.attach(peer, sid)
			h.send(peer, sid, "1534-browser-in-flight", "continue a long answer")

			before := stop1534ReadToken(t, h, peer)
			shown := stopBodyParityParseLive(t, before)
			want := strings.Join(shown.tokenContents, "")
			require.Equal(t, stop1534Prefix, want, "fixture must deliver a real nonempty prefix before Stop")
			require.NotEmpty(t, shown.liveMessageID)

			// The shared parser reads turn_id only from done. The checkpoint must
			// instead name the turn in tokens RECEIVED before the Stop write.
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(before[len(before)-1], &token))
			require.NotNil(t, token.TurnId)
			require.NotEmpty(t, *token.TurnId)
			cancelJSON := stop1534CancelFromTokens(t, sid, before)
			var sent chan error
			if stopped {
				// Freeze want FIRST, then send the architect's real shown payload.
				// Raw JSON retains the not-yet-generated field on today's code;
				// only this outbound TCP write is held, never a server method.
				gate.armed.Store(true)
				sent = make(chan error, 1)
				go func() { sent <- peer.WriteMessage(websocket.TextMessage, cancelJSON) }()
				select {
				case <-gate.held:
				case <-time.After(30 * time.Second):
					t.Fatal("SETUP: client cancel did not reach the held network write")
				}
			}

			close(provider.next)
			transit := stop1534ReadToken(t, h, peer)
			inFlight := stopBodyParityParseLive(t, transit)
			require.Equal(t, []string{stop1534InFlight}, inFlight.tokenContents,
				"fixture must receive the additional text while the server cannot yet see Stop")
			require.Equal(t, shown.liveMessageID, inFlight.liveMessageID, "this is growth of the SAME answer")
			if stopped {
				gate.open()
				select {
				case err := <-sent:
					require.NoError(t, err, "the held cancel must reach the real gateway")
				case <-time.After(30 * time.Second):
					t.Fatal("SETUP: released cancel write did not finish")
				}
			} else {
				// No Stop: the second token remains on screen, so include it in the
				// expected socket-derived body. Never truncate ordinary answers.
				want += strings.Join(inFlight.tokenContents, "")
				close(provider.finish)
			}

			terminal := stopBodyParityReadToDone(t, h, peer)
			live := stopBodyParityParseLive(t, terminal)
			require.NotEmpty(t, live.turnID, "a genuine terminal frame must precede reload")
			require.Equal(t, *token.TurnId, live.turnID, "the checkpoint named this turn before Stop, not after done")
			if stopped {
				claimed := h.event(func(event agent.Event) bool {
					payload, ok := event.Payload.(agent.InterruptReceivedPayload)
					return event.Kind == agent.EventKindInterruptReceived && ok &&
						payload.Kind == agent.InterruptKindGraceful && event.Meta.TurnID == live.turnID
				})
				require.Equal(t, live.turnID, claimed.Meta.TurnID, "Stop claimed this exact turn through the web dispatcher")
				historyD16AwaitCancelRecord(t, h, sid, live.turnID)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.True(t, h.loop.WaitForActiveRequestsContext(ctx), "join all owned provider work before reading persisted data")
			assert.Equal(t, int32(1), provider.calls.Load(), "Q1=A: no following model round")
			require.NoError(t, peer.Close(), "disconnect the viewing socket before fresh-socket reload")
			stop1534AssertReadback(t, h, sid, live.turnID, shown.liveMessageID, want, stopped)
		})
	}
}

func stop1534AssertReadback(t *testing.T, h *historyD16Harness, sid, turnID, messageID, want string, stopped bool) {
	t.Helper()
	page := h.page(sid, "") // real GET /api/v1/sessions/{id}/messages handler + generated validation
	assert.False(t, page.HasEarlier, "the entire tiny history must be examined")
	assert.False(t, page.HasLater, "no unseen later group may hide growth")
	var restBodies []string
	for _, entry := range page.Entries {
		if entry.Role != nil && *entry.Role == "assistant" && derefString(entry.Content) != "" {
			assert.Equal(t, messageID, entry.Id, "REST must keep the same nonempty message ID")
			assert.Equal(t, turnID, derefString(entry.TurnId))
			restBodies = append(restBodies, *entry.Content)
		}
	}
	assert.Equal(t, []string{want}, restBodies, "#1534: REST saved body must end at the client's received-token snapshot, not server Stop receipt")

	// The viewing socket was disconnected; now attach a fresh one for reload.
	// There is no helper-side seeding of the transcript or replay.
	reloaded := h.connect()
	replay := stopWholeTurnReplayAssistants(t, h.attach(reloaded, sid))
	var replayBodies []string
	for _, frame := range replay {
		if frame.Content != "" {
			assert.Equal(t, messageID, derefString(frame.Id), "replay must replace the SAME stopped bubble")
			assert.Equal(t, turnID, derefString(frame.TurnId))
			replayBodies = append(replayBodies, frame.Content)
		}
	}
	assert.Equal(t, []string{want}, replayBodies, "#1534: reload replay must equal the client's received-token snapshot at Stop")

	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	var savedBodies []string
	cancelRecords := 0
	for _, entry := range entries {
		assert.NotContains(t, entry.Content, stop1534AfterClaim, "the existing post-server-claim fence must still hold")
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == turnID {
			cancelRecords++
		}
		if entry.Role == "assistant" && entry.Content != "" {
			assert.Equal(t, messageID, entry.ID)
			assert.Equal(t, turnID, entry.TurnID)
			savedBodies = append(savedBodies, entry.Content)
		}
	}
	wantCancels := 0
	if stopped {
		wantCancels = 1
	}
	assert.Equal(t, wantCancels, cancelRecords, "Stop retains exactly one interrupted-label source; no Stop creates none")
	assert.Equal(t, []string{want}, savedBodies, "#1534: canonical saved body must not grow beyond the client Stop snapshot")
	t.Logf("client_snapshot=%q REST=%q replay=%q canonical=%q stopped=%t", want, restBodies, replayBodies, savedBodies, stopped)
}
