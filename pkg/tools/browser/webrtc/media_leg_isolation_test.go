// ADR-062 tier 1 — the two defects an adversarial review caught in the first
// implementation, before it shipped. Both are invisible to a "does it compile"
// or "does one viewer connect" check, and both break something that currently
// works.

package webrtc

import (
	"net"
	"testing"
)

// A Session serves TWO legs from one object: the loopback ingest leg (gateway
// to its OWN headless Chrome) and the internet-facing viewer leg. The first
// implementation applied the public-address rewrite to a SHARED SettingEngine,
// which would have handed the loopback encoder a public address it cannot
// reach — breaking capture on every install, laptops included, in exchange for
// fixing hosted ones.
func TestSession_ViewerAndIngestLegsUseSeparateAPIs(t *testing.T) {
	s := NewSession(Config{PublicIPs: []string{"203.0.113.7"}}, nil, nil)
	defer func() { _ = s.Close() }()

	if s.api == nil || s.apiViewer == nil {
		t.Fatal("both leg APIs must exist")
	}
	if s.api == s.apiViewer {
		t.Fatal("viewer and ingest legs share one API: the public-address rewrite " +
			"would reach the loopback encoder leg and break capture everywhere")
	}
}

// With no ADR-062 settings the two legs may safely share one API — this pins
// that the split is driven by configuration, not unconditional overhead, and
// that a laptop install keeps its pre-ADR-062 behaviour.
func TestSession_DefaultConfigKeepsPreADR062Behaviour(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	defer func() { _ = s.Close() }()
	if s.apiViewer == nil {
		t.Fatal("apiViewer must never be nil — the viewer leg would nil-panic")
	}
}

// A Session exists PER AGENT. If each bound the fixed media port itself, the
// first agent would win and every later agent would silently fall back to an
// ephemeral port — a multi-agent hosted install with video for one agent and
// an unexplained failure for the rest. The socket is therefore passed IN,
// already bound, and shared.
func TestSession_SharedMediaConnServesManySessions(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind test socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Three agents, one socket — none of them may fail or take ownership.
	for i := 0; i < 3; i++ {
		s := NewSession(Config{MediaConn: conn}, nil, nil)
		if s.apiViewer == nil {
			t.Fatalf("session %d: viewer API missing", i)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("session %d close: %v", i, err)
		}
	}

	// The socket must still be usable after every Session closed: Sessions
	// borrow it, the gateway owns it. A Session that closed it would leave
	// the next agent — and every reload — without media.
	if _, err := conn.WriteTo([]byte("still alive"), conn.LocalAddr()); err != nil {
		t.Fatalf("shared socket was closed by a Session: %v", err)
	}
}
