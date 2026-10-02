package gateway

// RED — w5-integration claim 2: Mail panel presence is bound to the
// AUTHENTICATED CONNECTION, never to a caller-supplied identity, and the
// gateway's single teardown choke point revokes a dead connection's
// observers.
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-2.1: the server binds the observer "to *this connection*
//     (not to any caller-supplied session string)".
//   - w5 spec US-2.2: a close frame drops only that observer; "another
//     tab's observer on a different connection is unaffected".
//   - w5 spec US-2.3/B-6/MC-4: abrupt socket death → "the observer is
//     revoked by the teardown hook"; §2.3 teardown row: "Bind this teardown
//     to W1's published PanelPresence.UnbindAll(connID)… Keep revocation
//     before wc.close()".
//   - w5 spec §2.3: "no caller-supplied connection identity".
//
// Mutations this pack must kill (check-integration-report.md §2):
//   - M4: presence binds/unbinds by the CALLER-SUPPLIED observer id as the
//     connection key (one tab can then revoke another tab's observer).
//   - M5: ServeHTTP's deferred teardown block no longer revokes observers.

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// newPresenceTestHandler builds a WSHandler whose home points at a temp
// data root with the workspace seeded, so an open frame passes the
// workspace authorization gate, and binds W1's one PanelPresence registry.
func newPresenceTestHandler(t *testing.T) (*WSHandler, string) {
	t.Helper()
	h, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	home := t.TempDir()
	h.home = home
	const ws = "ws-presence"
	seedWorkspaceFile(t, home, ws)
	h.SetMailPresence(email.NewPanelPresence())
	return h, ws
}

func TestMailPanelPresence_CloseFromAnotherConnectionCannotRevoke(t *testing.T) {
	h, ws := newPresenceTestHandler(t)
	p := h.mailPresenceOf()

	connA, connB := makeTestConn(), makeTestConn()

	h.handleMailPanelObserverFrame(connA, "conn-A", generated.MailPanelObserverFrame{
		Action: MailPanelObserverActionOpen, ObserverId: "obs-1", WorkspaceId: ws,
	})
	require.Equal(t, 1, p.Count(ws), "US-2.1: an acknowledged open must bind the observer to its connection")

	// A DIFFERENT connection presents the same observer id and asks to
	// close it. The id is not authority: only the owning connection can
	// unbind (US-2.1/US-2.2 — the binding key is the connection, and no
	// caller-supplied identity can revoke another connection's observer).
	h.handleMailPanelObserverFrame(connB, "conn-B", generated.MailPanelObserverFrame{
		Action: MailPanelObserverActionClose, ObserverId: "obs-1", WorkspaceId: ws,
	})

	if got := p.Count(ws); got != 1 {
		t.Fatalf("US-2.1/MC-4: a close frame from ANOTHER connection removed the observer (count=%d) — presence is keyed by a caller-supplied identity instead of the authenticated connection", got)
	}

	// The owning connection's own close DOES remove it (US-2.2, the control).
	h.handleMailPanelObserverFrame(connA, "conn-A", generated.MailPanelObserverFrame{
		Action: MailPanelObserverActionClose, ObserverId: "obs-1", WorkspaceId: ws,
	})
	require.Equal(t, 0, p.Count(ws), "US-2.2: the owning connection's close must drop its observer")
}

func TestMailPanelPresence_CloseDropsOnlyThatObserver(t *testing.T) {
	h, ws := newPresenceTestHandler(t)
	p := h.mailPresenceOf()

	connA, connB := makeTestConn(), makeTestConn()
	open := func(conn *wsConn, connID, obs string) {
		h.handleMailPanelObserverFrame(conn, connID, generated.MailPanelObserverFrame{
			Action: MailPanelObserverActionOpen, ObserverId: obs, WorkspaceId: ws,
		})
	}
	open(connA, "conn-A", "obs-1")
	open(connA, "conn-A", "obs-2")
	open(connB, "conn-B", "obs-3")
	require.Equal(t, 3, p.Count(ws))

	h.handleMailPanelObserverFrame(connA, "conn-A", generated.MailPanelObserverFrame{
		Action: MailPanelObserverActionClose, ObserverId: "obs-1", WorkspaceId: ws,
	})

	if got := p.Count(ws); got != 2 {
		t.Fatalf("US-2.2: closing obs-1 must drop only that observer (obs-2 and the other tab's obs-3 survive); count=%d", got)
	}
}

// TestMailPanelPresence_SocketDeathRevokesViaTeardown drives the REAL
// ServeHTTP accept → auth → frame-dispatch → abrupt TCP death → deferred
// teardown path (US-2.3/B-6). Each connection opens one observer; then a
// connection dies without any close frame. The dead connection's observer
// must be revoked by the teardown hook while the live connection keeps its
// own (control). Kills M5.
func TestMailPanelPresence_SocketDeathRevokesViaTeardown(t *testing.T) {
	h, ws := newPresenceTestHandler(t)
	p := h.mailPresenceOf()

	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	wsURL := "ws" + server.URL[len("http"):]

	dialAndOpen := func(observerID string) *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		// Dev-bypass harness (newTestWSHandlerForModelName): the legacy
		// auth-frame handshake authenticates programmatic clients.
		require.NoError(t, conn.WriteJSON(map[string]string{"type": "auth", "token": "test"}))
		require.NoError(t, conn.WriteJSON(map[string]string{
			"type": "mail_panel_observer", "action": "open",
			"observer_id": observerID, "workspace_id": ws,
		}))
		return conn
	}

	conn1 := dialAndOpen("obs-dead")
	defer conn1.Close()
	conn2 := dialAndOpen("obs-live")
	defer conn2.Close()

	require.Eventually(t, func() bool { return p.Count(ws) == 2 },
		5*time.Second, 20*time.Millisecond,
		"both observers must bind through the real dispatch path")

	// Abrupt death of connection 1 — no close frame, the socket just dies
	// (US-2.3's "client crashes without sending close").
	if err := conn1.Close(); err != nil {
		t.Fatalf("closing connection 1: %v", err)
	}

	// Teardown is asynchronous (read error → deferred block); poll.
	require.Eventually(t, func() bool { return p.Count(ws) == 1 },
		5*time.Second, 10*time.Millisecond,
		"US-2.3/MC-4: after connection 1 died its observer must be revoked by the teardown hook; the live connection's observer must survive")

	require.NoError(t, conn2.Close())
	require.Eventually(t, func() bool { return p.Count(ws) == 0 },
		5*time.Second, 10*time.Millisecond,
		"US-2.3: the second connection's teardown must also revoke its observer")
}
