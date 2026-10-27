package irc

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// gateIRCServer speaks just enough IRCv3 registration: "sasl-reject" offers
// SASL and answers the PLAIN exchange with 904; "pass-reject" answers
// registration with 464 then ERROR and hangs up; "ok" welcomes with 001 and
// ends registration with 422 (no MOTD).
func gateIRCServer(t *testing.T, mode string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveGateIRC(conn, mode)
		}
	}()
	return ln.Addr().String()
}

func serveGateIRC(conn net.Conn, mode string) {
	defer conn.Close()
	send := func(line string) { _, _ = fmt.Fprintf(conn, ":fake.server %s\r\n", line) }
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "CAP":
			switch {
			case len(fields) > 1 && fields[1] == "LS":
				if mode == "sasl-reject" {
					send("CAP * LS :sasl")
				} else {
					send("CAP * LS :")
				}
			case len(fields) > 2 && fields[1] == "REQ":
				capName := strings.TrimPrefix(fields[2], ":")
				if capName == "sasl" && mode == "sasl-reject" {
					send("CAP * ACK :" + capName)
				} else {
					send("CAP * NAK :" + capName)
				}
			case len(fields) > 1 && fields[1] == "END":
				switch mode {
				case "pass-reject":
					send("464 bot :Password incorrect")
					_, _ = fmt.Fprint(conn, "ERROR :Closing Link: bad password\r\n")
					return
				case "ok":
					send("001 bot :Welcome to the fake network")
					send("422 bot :MOTD File is missing") // ends registration
				}
			}
		case "AUTHENTICATE":
			if len(fields) > 1 && fields[1] == "PLAIN" {
				_, _ = fmt.Fprint(conn, "AUTHENTICATE +\r\n")
			} else {
				send("904 bot :SASL authentication failed")
			}
		case "PING":
			send("PONG fake.server " + strings.Join(fields[1:], " "))
		case "QUIT":
			return
		}
	}
}

func newGateIRCChannel(t *testing.T, server string, cfg config.IRCConfig, secrets credentials.SecretBundle) *IRCChannel {
	t.Helper()
	cfg.Server, cfg.Nick = server, "bot"
	ch, err := NewIRCChannel(cfg, secrets, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewIRCChannel: %v", err)
	}
	return ch
}

// TestIRCStart_RejectedCredentialsFailTheStart settles the Train 2 gate r1
// survey's inferred IRC verdict (Q1 = A): a SASL failure (904) or a server
// password rejection (464) must fail Start rather than leave a running
// channel, and an accepted registration starts it.
func TestIRCStart_RejectedCredentialsFailTheStart(t *testing.T) {
	cases := map[string]*IRCChannel{
		"sasl 904": newGateIRCChannel(t, gateIRCServer(t, "sasl-reject"),
			config.IRCConfig{SASLUser: "bot", SASLPasswordRef: "IRC_SASL"}, credentials.SecretBundle{"IRC_SASL": "wrong"}),
		"pass 464": newGateIRCChannel(t, gateIRCServer(t, "pass-reject"),
			config.IRCConfig{PasswordRef: "IRC_PASS"}, credentials.SecretBundle{"IRC_PASS": "wrong"}),
	}
	for name, ch := range cases {
		if err := ch.Start(context.Background()); err == nil {
			_ = ch.Stop(context.Background())
			t.Fatalf("%s: a rejected credential must fail Start", name)
		}
		if ch.IsRunning() {
			t.Fatalf("%s: a rejected channel must not be running", name)
		}
	}
	ok := newGateIRCChannel(t, gateIRCServer(t, "ok"), config.IRCConfig{}, credentials.SecretBundle{})
	if err := ok.Start(context.Background()); err != nil {
		t.Fatalf("an accepted registration must start, got %v", err)
	}
	if !ok.IsRunning() {
		t.Fatal("an accepted registration must leave the channel running")
	}
	_ = ok.Stop(context.Background())
}
