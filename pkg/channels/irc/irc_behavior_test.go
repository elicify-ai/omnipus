package irc

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ergochat/irc-go/ircevent"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

func newTestIRCChannel(t *testing.T, cfg config.IRCConfig) (*IRCChannel, *bus.MessageBus) {
	t.Helper()
	if cfg.Server == "" {
		cfg.Server = "irc.example.com:6667"
	}
	if cfg.Nick == "" {
		cfg.Nick = "testbot"
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewIRCChannel(cfg, credentials.SecretBundle{}, msgBus)
	if err != nil {
		t.Fatalf("new channel: %v", err)
	}
	return ch, msgBus
}

func receiveInbound(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(time.Second):
		t.Fatal("expected inbound message")
		return bus.InboundMessage{}
	}
}

func expectNoInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// privmsg builds a PRIVMSG ircmsg.Message from a sender nick to a target.
func privmsg(senderNick, target, text string) ircmsg.Message {
	return ircmsg.MakeMessage(nil, senderNick+"!user@host", "PRIVMSG", target, text)
}

// newConn returns a bare, unconnected ircevent.Connection usable by the
// PRIVMSG handler. CurrentNick() reports "" on an unconnected conn, which is
// fine for DM/channel routing tests (they do not depend on mention detection).
func newConn(nick string) *ircevent.Connection {
	return &ircevent.Connection{Nick: nick}
}

func TestOnPrivmsg_DirectMessageRoutesToInbound(t *testing.T) {
	ch, msgBus := newTestIRCChannel(t, config.IRCConfig{Nick: "testbot"})
	ch.ctx = context.Background()

	// DM: target is the bot's nick (not a channel prefix).
	ch.onPrivmsg(newConn("testbot"), privmsg("alice", "testbot", "hello there"))

	inbound := receiveInbound(t, msgBus)
	if inbound.Channel != "irc" {
		t.Fatalf("channel=%q", inbound.Channel)
	}
	if inbound.Peer.Kind != bus.PeerDirect || inbound.Peer.ID != "alice" {
		t.Fatalf("peer=%+v want direct/alice", inbound.Peer)
	}
	if inbound.ChatID != "alice" {
		t.Fatalf("chat_id=%q want alice", inbound.ChatID)
	}
	if inbound.Content != "hello there" {
		t.Fatalf("content=%q", inbound.Content)
	}
}

func TestOnPrivmsg_ChannelMessageUsesChannelAsChat(t *testing.T) {
	ch, msgBus := newTestIRCChannel(t, config.IRCConfig{Nick: "testbot"})
	ch.ctx = context.Background()

	ch.onPrivmsg(newConn("testbot"), privmsg("bob", "#general", "hi all"))

	inbound := receiveInbound(t, msgBus)
	if inbound.Peer.Kind != bus.PeerGroup || inbound.Peer.ID != "#general" {
		t.Fatalf("peer=%+v want group/#general", inbound.Peer)
	}
	if inbound.ChatID != "#general" {
		t.Fatalf("chat_id=%q", inbound.ChatID)
	}
}

func TestOnPrivmsg_TooFewParamsIgnored(t *testing.T) {
	ch, msgBus := newTestIRCChannel(t, config.IRCConfig{Nick: "testbot"})
	ch.ctx = context.Background()

	// PRIVMSG with only a target and no text → ignored.
	ch.onPrivmsg(newConn("testbot"), ircmsg.MakeMessage(nil, "alice!u@h", "PRIVMSG", "testbot"))
	expectNoInbound(t, msgBus)
}

func TestOnPrivmsg_EmptyContentIgnored(t *testing.T) {
	ch, msgBus := newTestIRCChannel(t, config.IRCConfig{Nick: "testbot"})
	ch.ctx = context.Background()

	ch.onPrivmsg(newConn("testbot"), privmsg("alice", "testbot", "   "))
	expectNoInbound(t, msgBus)
}

func TestOnPrivmsg_AllowlistRejectsUnknownSender(t *testing.T) {
	ch, msgBus := newTestIRCChannel(t, config.IRCConfig{
		Nick:      "testbot",
		AllowFrom: config.FlexibleStringSlice{"irc:trusted"},
	})
	ch.ctx = context.Background()

	ch.onPrivmsg(newConn("testbot"), privmsg("intruder", "testbot", "let me in"))
	expectNoInbound(t, msgBus)
}

func TestSend_FailsWhenNotRunning(t *testing.T) {
	ch, _ := newTestIRCChannel(t, config.IRCConfig{Nick: "testbot"})

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "#chan", Content: "hi"})
	if err != channels.ErrNotRunning {
		t.Fatalf("Send err=%v want ErrNotRunning", err)
	}
}

func TestSend_EmptyTargetFails(t *testing.T) {
	ch, _ := newTestIRCChannel(t, config.IRCConfig{Nick: "testbot"})
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "", Content: "hi"})
	if err == nil {
		t.Fatal("expected error for empty target")
	}
}

// --- Zombie regression: IsRunning() must become false once the connection
// loop exits, so the manager stops routing into a dead channel. ---

// fakeIRCServer accepts one client and completes the IRC registration handshake
// by replying with 001 (welcome) + 422 (no MOTD), which is what unblocks
// ircevent's Connect(). It then idles. Tests can drop the client connection to
// simulate a disconnect; closing the server tears everything down.
type fakeIRCServer struct {
	ln       net.Listener
	mu       sync.Mutex
	clients  []net.Conn
	closeOne sync.Once
}

func startFakeIRCServer(t *testing.T) *fakeIRCServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeIRCServer{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.clients = append(s.clients, conn)
			s.mu.Unlock()
			go s.handle(conn)
		}
	}()
	return s
}

func (s *fakeIRCServer) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	registered := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		// ircevent's Connect() blocks until it sees end-of-MOTD (376/422),
		// which is what closes its internal welcome channel — 001 alone is
		// not enough.
		if !registered && strings.HasPrefix(strings.ToUpper(line), "USER ") {
			registered = true
			_, _ = conn.Write([]byte(":server 001 testbot :Welcome\r\n"))
			_, _ = conn.Write([]byte(":server 422 testbot :MOTD File is missing\r\n"))
		}
		// On QUIT, close the client connection like a real ircd would, so the
		// client's read loop sees EOF promptly instead of waiting on KeepAlive.
		if strings.HasPrefix(strings.ToUpper(line), "QUIT") {
			_ = conn.Close()
			return
		}
	}
}

// dropClients force-closes every accepted client connection, simulating an
// abrupt server-side disconnect.
func (s *fakeIRCServer) dropClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		_ = c.Close()
	}
}

func (s *fakeIRCServer) addr() string { return s.ln.Addr().String() }

func (s *fakeIRCServer) Close() {
	s.closeOne.Do(func() {
		_ = s.ln.Close()
		s.dropClients()
	})
}

func TestStop_MarksNotRunning_NoZombie(t *testing.T) {
	srv := startFakeIRCServer(t)
	defer srv.Close()

	ch, _ := newTestIRCChannel(t, config.IRCConfig{
		Server: srv.addr(),
		Nick:   "testbot",
	})

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("expected IsRunning() true after Start")
	}

	// Stop() quits the connection; the Loop() goroutine then returns and the
	// wrapper flips IsRunning() to false. Poll briefly for the goroutine.
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for ch.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ch.IsRunning() {
		t.Fatal("zombie: IsRunning() still true after Stop() and loop exit")
	}
}

func TestConnectionLoopExit_MarksNotRunning(t *testing.T) {
	srv := startFakeIRCServer(t)

	ch, _ := newTestIRCChannel(t, config.IRCConfig{
		Server: srv.addr(),
		Nick:   "testbot",
	})

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("expected IsRunning() true after Start")
	}

	defer srv.Close()

	// Quit() sets the permanent-quit flag and sends QUIT; the fake server
	// responds by closing the socket, so the client's read loop sees EOF and
	// Loop() returns instead of reconnecting forever. The wrapper goroutine
	// must then mark the channel not running (no zombie).
	ch.conn.Quit()

	deadline := time.Now().Add(3 * time.Second)
	for ch.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ch.IsRunning() {
		t.Fatal("zombie: IsRunning() still true after connection loop exit")
	}
}
