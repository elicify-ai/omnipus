package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// These synthetic IDs distinguish the bot, an allowed sender, the instrument's
// initial rejected sender, and a sender offered only after Stop.
const (
	ghostBotID     = "700000000000000009"
	ghostAllowedID = "700000000000000001"
	ghostDeniedID  = "700000000000000002"
	ghostLateID    = "700000000000000003"
	ghostChatID    = "700000000000000007"
)

type ghostGatewayEvent struct {
	kind string
	at   time.Time
	op   int
	peer *ghostGatewayPeer
	err  error
}

type ghostGatewayPeer struct {
	conn *websocket.Conn
	mu   sync.Mutex // websocket permits one concurrent writer
	seq  int
}

func (p *ghostGatewayPeer) write(value any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn.WriteJSON(value)
}

func (p *ghostGatewayPeer) message(id, sender, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	return p.conn.WriteJSON(map[string]any{
		"op": 0, "t": "MESSAGE_CREATE", "s": p.seq,
		"d": map[string]any{
			"id": id, "channel_id": ghostChatID, "content": content,
			"author":      map[string]any{"id": sender, "username": "test-sender", "discriminator": "0"},
			"attachments": []any{}, "mentions": []any{},
		},
	})
}

type ghostGateway struct {
	server *httptest.Server
	ctx    context.Context
	cancel context.CancelFunc
	events chan ghostGatewayEvent
	wg     sync.WaitGroup
	mu     sync.Mutex
	down   bool
	fail   int
	auth   []int
	peers  []*ghostGatewayPeer
}

func newGhostGateway(ctx context.Context) *ghostGateway {
	ctx, cancel := context.WithCancel(ctx)
	g := &ghostGateway{ctx: ctx, cancel: cancel, events: make(chan ghostGatewayEvent, 32)}
	g.server = httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	return g
}

func (g *ghostGateway) emit(event ghostGatewayEvent) {
	event.at = time.Now()
	select {
	case g.events <- event:
	case <-g.ctx.Done():
	}
}

func (g *ghostGateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// discordgo.Gateway canonicalizes the returned URL with a trailing slash.
	if r.URL.Path == "/gateway" || r.URL.Path == "/gateway/" {
		g.serveGateway(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var response any
	switch r.URL.Path {
	case "/api/v9/users/@me":
		response = map[string]any{"id": ghostBotID, "username": "test-bot", "bot": true}
	case "/api/v9/gateway":
		response = map[string]any{"url": "ws" + strings.TrimPrefix(g.server.URL, "http") + "/gateway"}
	default:
		g.emit(ghostGatewayEvent{kind: "fault", err: fmt.Errorf("unexpected REST request: %s %s", r.Method, r.URL.Path)})
		http.Error(w, "unexpected fake Discord request", http.StatusNotFound)
		return
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		g.emit(ghostGatewayEvent{kind: "fault", err: err})
	}
}

func (g *ghostGateway) serveGateway(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	unavailable := g.down || g.fail > 0
	if g.fail > 0 {
		g.fail--
	}
	g.mu.Unlock()
	if unavailable {
		http.Error(w, "test gateway temporarily unavailable", http.StatusServiceUnavailable)
		g.emit(ghostGatewayEvent{kind: "retry_failed"})
		return
	}

	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		g.emit(ghostGatewayEvent{kind: "fault", err: err})
		return
	}
	peer := &ghostGatewayPeer{conn: conn, seq: 1}
	g.mu.Lock()
	g.peers = append(g.peers, peer)
	g.mu.Unlock()
	g.wg.Add(1)
	defer g.wg.Done()
	defer conn.Close()

	// A real HELLO/IDENTIFY-or-RESUME/READY exchange, not a mocked Session.
	// The one-minute heartbeat interval keeps heartbeat timing out of this test;
	// the reader still acknowledges real heartbeats, including the initial one.
	if err = peer.write(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 60000}}); err != nil {
		g.emit(ghostGatewayEvent{kind: "fault", err: err})
		return
	}
	var authentication struct {
		Op int `json:"op"`
	}
	if err = conn.ReadJSON(&authentication); err != nil {
		g.emit(ghostGatewayEvent{kind: "fault", err: err})
		return
	}
	if authentication.Op != 2 && authentication.Op != 6 {
		g.emit(ghostGatewayEvent{kind: "fault", err: fmt.Errorf("expected IDENTIFY (2) or RESUME (6), got %d", authentication.Op)})
		return
	}
	g.mu.Lock()
	g.auth = append(g.auth, authentication.Op)
	g.mu.Unlock()
	ready := map[string]any{
		"op": 0, "t": "READY", "s": 1,
		"d": map[string]any{
			"v": 9, "session_id": "test-ghost-session", "guilds": []any{}, "private_channels": []any{},
			"user": map[string]any{"id": ghostBotID, "username": "test-bot", "bot": true},
		},
	}
	if authentication.Op == 6 {
		ready["t"] = "RESUMED"
		ready["d"] = map[string]any{}
	}
	if err = peer.write(ready); err != nil {
		g.emit(ghostGatewayEvent{kind: "fault", err: err})
		return
	}
	g.emit(ghostGatewayEvent{kind: "authenticated", op: authentication.Op, peer: peer})
	for {
		var packet struct {
			Op int `json:"op"`
		}
		if err = conn.ReadJSON(&packet); err != nil {
			return // a client Stop or the test's deliberate socket drop
		}
		if packet.Op != 1 {
			g.emit(ghostGatewayEvent{kind: "fault", err: fmt.Errorf("unexpected gateway operation %d", packet.Op)})
			return
		}
		if err = peer.write(map[string]any{"op": 11, "d": nil}); err != nil {
			return // connection may have been deliberately dropped during this ACK
		}
	}
}

func (g *ghostGateway) outage(down bool, failures int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.down, g.fail = down, failures
}

func (g *ghostGateway) authentications() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int{}, g.auth...)
}

func (g *ghostGateway) close() {
	g.cancel()
	g.server.Close()
	g.mu.Lock()
	peers := append([]*ghostGatewayPeer{}, g.peers...)
	g.mu.Unlock()
	for _, peer := range peers {
		_ = peer.conn.Close() // already-closed sockets are expected during teardown
	}
	g.wg.Wait()
}

type ghostRESTTransport struct {
	base      *url.URL
	transport *http.Transport
}

func (rt ghostRESTTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	local := request.Clone(request.Context())
	local.URL.Scheme, local.URL.Host = rt.base.Scheme, rt.base.Host
	return rt.transport.RoundTrip(local)
}

type ghostDiscordFixture struct {
	adapter *DiscordChannel
	gateway *ghostGateway
	bus     *bus.MessageBus
	seen    chan string
}

func newGhostDiscordFixture(t *testing.T, ctx context.Context) *ghostDiscordFixture {
	t.Helper()
	g := newGhostGateway(ctx)
	messageBus := bus.NewMessageBus()
	adapter, err := NewDiscordChannel(config.DiscordConfig{
		TokenRef: "ghost-test-token", AllowFrom: config.FlexibleStringSlice{ghostAllowedID},
	}, credentials.SecretBundle{"ghost-test-token": "synthetic-discord-token"}, messageBus)
	require.NoError(t, err, "instrument: construct the real Discord adapter")
	base, err := url.Parse(g.server.URL)
	require.NoError(t, err)
	// Both transports refuse any address except this fixture's loopback listener.
	dialLocal := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != base.Host {
			return nil, fmt.Errorf("instrument: refusing non-fixture network address %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	transport := &http.Transport{DialContext: dialLocal}
	adapter.session.Client = &http.Client{Transport: ghostRESTTransport{base, transport}, Timeout: 5 * time.Second}
	adapter.session.Dialer = &websocket.Dialer{NetDialContext: dialLocal, HandshakeTimeout: 5 * time.Second}
	adapter.session.SyncEvents = true
	f := &ghostDiscordFixture{adapter: adapter, gateway: g, bus: messageBus, seen: make(chan string, 16)}
	t.Cleanup(func() {
		g.outage(false, 0)
		assert.NoError(t, adapter.Stop(context.Background()), "cleanup: close the real connected adapter")
		transport.CloseIdleConnections()
		g.close()
		messageBus.Close()
	})
	return f
}

func ghostReceive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatalf("instrument: timed out waiting for an observable gateway/handler/bus event: %v", ctx.Err())
		var zero T
		return zero
	}
}

func ghostEvent(t *testing.T, ctx context.Context, g *ghostGateway) ghostGatewayEvent {
	t.Helper()
	event := ghostReceive(t, ctx, g.events)
	require.NoError(t, event.err, "instrument: fake gateway protocol fault")
	return event
}

func (f *ghostDiscordFixture) start(t *testing.T, ctx context.Context) *ghostGatewayPeer {
	t.Helper()
	require.NoError(t, f.adapter.Start(ctx), "instrument: Start must complete a real gateway handshake")
	event := ghostEvent(t, ctx, f.gateway)
	require.Equal(t, "authenticated", event.kind)
	require.Equal(t, []int{2}, f.gateway.authentications(), "instrument: fake observes initial IDENTIFY")
	// Start has already registered the REAL handleMessage. SyncEvents guarantees
	// this later observer runs after it, without replacing or wrapping it.
	f.adapter.session.AddHandler(func(_ *discordgo.Session, message *discordgo.MessageCreate) {
		select {
		case f.seen <- message.ID:
		case <-ctx.Done():
		}
	})
	return event.peer
}

func (f *ghostDiscordFixture) dispatch(t *testing.T, ctx context.Context, peer *ghostGatewayPeer, id, sender, content string) {
	t.Helper()
	require.NoError(t, peer.message(id, sender, content), "instrument: deliver an actual MESSAGE_CREATE frame")
	require.Equal(t, id, ghostReceive(t, ctx, f.seen), "instrument: synchronous dispatch completed after the real handler")
}

func (f *ghostDiscordFixture) forwarded(t *testing.T, ctx context.Context, peer *ghostGatewayPeer, id, content string) {
	t.Helper()
	f.dispatch(t, ctx, peer, id, ghostAllowedID, content)
	message := ghostReceive(t, ctx, f.bus.InboundChan())
	assert.Equal(t, []string{"discord", id, ghostChatID, ghostAllowedID, content},
		[]string{message.Channel, message.MessageID, message.ChatID, message.Sender.PlatformID, message.Content},
		"running adapter must forward the exact message, not merely connect")
	assert.Equal(t, bus.Peer{Kind: bus.PeerDirect, ID: ghostAllowedID}, message.Peer)
}
