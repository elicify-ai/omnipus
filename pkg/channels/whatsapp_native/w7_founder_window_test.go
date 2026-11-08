//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// Founder decisions 2026-11-08 08:13Z (team-lead brief, round 7), about the
// time while OUR managed reconnect is failing — the window issue #1687's F1
// fix produces, where the channel reports not running:
//
//	Q1 B: a send during that window keeps the short temporary retry it had
//	before (about 3.5s, a temporary error the manager retries) instead of
//	failing at once with the permanent not-running error.
//	Q3 B: the channel's availability reason in that window is
//	WhatsApp-specific (the connection was lost and is being re-established),
//	not the shared "not started".
//
// Oracle surfaces (read, cited):
//   - pkg/channels/manager.go::sendWithRetry — ErrNotRunning/ErrSendFailed
//     are permanent (no retry); ErrTemporary/unknown retry with exponential
//     backoff (maxRetries=3, baseBackoff=500ms doubling — the ~3.5s window).
//   - pkg/channels/whatsapp_native/whatsapp_native.go::Send — the two guard
//     classes: the not-running guard, and the "connection not established"
//     temporary guard every non-running outage answered before F1.
//   - pkg/channels/manager_outbound.go::Availability — the availability API,
//     the only status surface that carries a reason (manager.go::GetStatus
//     reports enabled/running only); read by the send_message tool
//     (pkg/agent/message_start_policy.go::CheckStart), the Channels block,
//     GET and Configure.

// w7BreakDialling points the captured client's proxy at a closed local port,
// so every reconnect dial is refused while the live socket is untouched
// (vendored whatsmeow client.go::SetProxy documents that a swap takes effect
// only on the next connect). SetProxy(nil) later restores direct dialling.
func w7BreakDialling(t *testing.T, captured *whatsmeow.Client) {
	t.Helper()
	brokenListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	brokenAddr := brokenListener.Addr().String()
	require.NoError(t, brokenListener.Close())
	brokenProxy, err := url.Parse("http://" + brokenAddr)
	require.NoError(t, err)
	captured.SetProxy(http.ProxyURL(brokenProxy))
}

// w7EnterFailingReconnectWindow drives the real channel into the failing
// managed reconnect: the remote end drops the live socket, our
// reconnectWithBackoff dials through the broken proxy and fails, and the
// channel reports not running while the loop keeps retrying (F1's fix).
func w7EnterFailingReconnectWindow(t *testing.T, f *nativeFixture, captured *whatsmeow.Client, remote interface{ CloseNow() error }) {
	t.Helper()
	held, release := fix1HoldCapturedReconnect(t)
	require.NoError(t, remote.CloseNow(), "instrument: the remote drop reached the socket")
	w2WaitOurReconnectHeld(t, held)
	release()
	require.Eventually(t, func() bool { return !f.ch.IsRunning() },
		2*time.Second, 5*time.Millisecond,
		"instrument: the managed reconnect must be failing (the channel reports not running) before the window assertions run")
}

// w7RecoverFromFailingReconnect restores direct dialling while the loop's
// next iteration is parked at its own log line — the hold gives the
// transport swap its ordering (see the round-6 note on
// TestReconnect_FailureIsVisible...) — and requires the reported state to
// clear. Shared by both window tests' recovery legs.
func w7RecoverFromFailingReconnect(t *testing.T, f *nativeFixture, captured *whatsmeow.Client, server *fix1NoiseServer) {
	t.Helper()
	held2, release2 := fix1HoldCapturedReconnect(t)
	select {
	case <-held2:
	case <-time.After(12 * time.Second):
		t.Fatal("HARNESS: the managed reconnect loop never reached its next iteration for the recovery swap")
	}
	captured.SetProxy(nil)
	release2()
	select {
	case <-server.connected:
	case <-time.After(10 * time.Second):
		t.Fatal("the managed reconnect did not recover after the endpoint returned")
	}
	require.Eventually(t, func() bool { return f.ch.IsRunning() },
		5*time.Second, 10*time.Millisecond,
		"instrument: the reconnect recovered and the channel reports running again")
}

// TestSend_WhileReconnectFailsKeepsTheTemporaryRetryClass pins founder Q1 B:
// inside the failing-managed-reconnect window a send answers the temporary,
// manager-retried class it answered before F1 — never the permanent
// ErrNotRunning. manager.go::sendWithRetry retries ErrTemporary with
// exponential backoff (3 attempts over about 3.5s), so a message sent during
// the window is attempted again and succeeds if the connection comes back
// inside it. Recovery half: after the reconnect succeeds, a send reaches the
// real send path — neither guard answers.
func TestSend_WhileReconnectFailsKeepsTheTemporaryRetryClass(t *testing.T) {
	f, server, captured, remote := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	w7BreakDialling(t, captured)
	w7EnterFailingReconnectWindow(t, f, captured, remote)

	// The send attempt inside the window. The 2s bound exists only so the
	// probe cannot hang on the library's peer-device exchange, which the fake
	// endpoint cannot complete; the assertions below read the guard
	// classification, which is decided before any peer exchange.
	msg := bus.OutboundMessage{Channel: "wa7", ChatID: "15550001111", Content: "sent during the failing reconnect"}
	sendCtx, cancelSend := context.WithTimeout(context.Background(), 2*time.Second)
	err := f.ch.Send(sendCtx, msg)
	cancelSend()
	// RED on the current worktree (F1's fix present): Send answers bare
	// channels.ErrNotRunning at whatsapp_native.go::Send's not-running guard.
	require.NotErrorIs(t, err, channels.ErrNotRunning,
		"founder Q1 B: during the failing managed reconnect a send must keep the temporary retry class, not fail at once with the permanent not-running error")
	require.ErrorIs(t, err, channels.ErrTemporary,
		"founder Q1 B: the send in the window must stay a temporary error, the class manager.go::sendWithRetry retries for about 3.5s")

	w7RecoverFromFailingReconnect(t, f, captured, server)

	// A send now reaches the real send path: neither the not-running guard nor
	// the connection guard may answer it. The real send itself may still fail
	// (the fake endpoint cannot complete a WhatsApp send to a never-seen
	// peer); the pinned surface is the guard classification, which is what the
	// manager's retry decision reads.
	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 2*time.Second)
	err = f.ch.Send(retryCtx, msg)
	cancelRetry()
	require.NotErrorIs(t, err, channels.ErrNotRunning,
		"after recovery a send must not answer the permanent not-running class")
	require.False(t, err != nil && strings.Contains(err.Error(), "connection not established"),
		"after recovery a send must pass the connection guard (the temporary class the window's retries relied on) and reach the real send path")
}

// TestAvailability_WhileReconnectFailsIsTheWhatsAppReconnectReason pins
// founder Q3 B: in the failing-managed-reconnect window the availability API
// answers not_running with a WhatsApp-specific reason — the connection was
// lost and is being re-established — never the shared "not started" (nor any
// other shared reason value). The wording belongs to the developer under the
// omnipus-voice rules; the test pins distinctness, not text. A recovered
// channel answers running with an empty reason again.
func TestAvailability_WhileReconnectFailsIsTheWhatsAppReconnectReason(t *testing.T) {
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := channels.NewManager(&config.Config{Channels: map[string]config.ChannelInstanceConfig{}},
		credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	startCtx, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	require.NoError(t, m.StartAll(startCtx))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	f, server, captured, remote := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	const instance = "wa7-reconnect"
	m.RegisterChannel(instance, f.ch)

	w7BreakDialling(t, captured)
	w7EnterFailingReconnectWindow(t, f, captured, remote)

	avail := m.Availability(instance)
	require.Equal(t, channels.AvailabilityNotRunning, avail.State,
		"instrument: the window reports not running; the founder pins the reason, not the state")
	// Round-8 strengthening: the reason is pinned as a distinct VALUE —
	// exactly the channel's reconnecting reason constant — not merely "not
	// one of the shared strings".
	require.Equal(t, reconnectingReason, avail.Reason,
		"founder Q3 B: in the failing managed reconnect window the availability reason is exactly the WhatsApp reconnect reason (the channel's reconnectingReason constant)")
	require.Equal(t, reconnectingReason, f.ch.NotRunningReason(),
		"founder Q3 B: the channel's NotRunningReasoner accessor carries the same reason the manager reports through Availability")
	require.NotEqual(t, "not started", avail.Reason,
		"founder Q3 B: in the failing managed reconnect window the availability reason must be the WhatsApp reconnect reason, not the shared \"not started\"")
	require.NotEqual(t, "not running", avail.Reason,
		"founder Q3 B: the reason must be the distinct WhatsApp reconnect value, not startMayProceed's shared placeholder")
	require.NotEqual(t, "not loaded", avail.Reason,
		"founder Q3 B: the reason must be the distinct WhatsApp reconnect value, not the unregistered fallback")
	require.NotEmpty(t, avail.Reason,
		"founder Q3 B: the window's availability carries the WhatsApp reconnect reason")

	w7RecoverFromFailingReconnect(t, f, captured, server)

	availAfter := m.Availability(instance)
	require.Equal(t, channels.AvailabilityRunning, availAfter.State,
		"a recovered channel answers running through the same availability surface")
	require.Empty(t, availAfter.Reason,
		"the reason is cleared with the recovery (running carries no reason)")
	require.Empty(t, f.ch.NotRunningReason(),
		"the channel's reconnect reason clears with the recovery (the manager then uses its shared reasons again)")
}
