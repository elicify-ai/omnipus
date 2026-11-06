package discord

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief's 2026-11-02 08:09Z ruling / CHECK F1: an already-queued handler
// invocation must do nothing after Stop, including no rejection accounting.
func TestRound2_DiscordStoppedHandlerDropsQueuedMessages(t *testing.T) {
	ctx := ghostTestContext(t)
	fixture := newGhostDiscordFixture(t, ctx)
	peer := fixture.start(t, ctx)
	fixture.forwarded(t, ctx, peer, "round2-before-stop", "running bus control")
	fixture.dispatch(t, ctx, peer, "round2-denied-before-stop", ghostDeniedID, "running rejection control")
	beforeCount, beforeRecent := fixture.adapter.RejectedSenders()
	require.Equal(t, 1, beforeCount, "instrument: the real running handler records denied senders")
	require.Equal(t, []string{"discord:" + ghostDeniedID}, beforeRecent)

	require.NoError(t, fixture.adapter.Stop(ctx))
	require.False(t, fixture.adapter.IsRunning(), "instrument: Stop has returned before the queued invocations")
	for _, sender := range []string{ghostAllowedID, ghostLateID} {
		// Directly invoke the real registered handler: closing the socket alone
		// cannot prove the in-flight callback guard (the surviving CHECK mutant).
		fixture.adapter.handleMessage(fixture.adapter.session, &discordgo.MessageCreate{
			Message: &discordgo.Message{
				ID: "round2-after-stop-" + sender, ChannelID: ghostChatID,
				Author:  &discordgo.User{ID: sender, Username: "queued-sender"},
				Content: "valid queued direct message after Stop",
			},
		})
	}
	// Calls above are synchronous and complete before this observation; no
	// guessed quiet period or scheduler-dependent sleep proves the absence.
	select {
	case message := <-fixture.bus.InboundChan():
		assert.Fail(t, "stopped handler must publish nothing", "received message %q from %q", message.MessageID, message.Sender.PlatformID)
	default:
	}
	afterCount, afterRecent := fixture.adapter.RejectedSenders()
	assert.Equal(t, beforeCount, afterCount, "stopped handler must not grow the rejected count")
	assert.Equal(t, beforeRecent, afterRecent, "stopped handler must not record the queued denied sender")
}

// Chief's 2026-11-02 08:09Z ruling / CHECK F3. gateway.go allocates 30 s
// to service shutdown and 30 s to provider reload; rest_auth.go::reloadWaitTimeout
// adds 30 s slack (90 s total). Allocate ONE tenth of the service-stop slice
// to this adapter's cancellation: 3 s leaves 27 s for other service shutdowns.
// It also exceeds discordgo v0.29.0 Close's explicit 1 s grace by 2 s. The
// network stall is released by this test, not by waiting for a 45 s timeout.
func TestRound2_DiscordStopBoundsBlockedReconnectOpen(t *testing.T) {
	const stopBudget = 3 * time.Second
	// Bound setup/teardown faults too; a healthy RED finishes in about 5 s.
	ctx, cancel := context.WithTimeout(ghostTestContext(t), 20*time.Second)
	t.Cleanup(cancel)
	fixture := newGhostDiscordFixture(t, ctx)
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	releaseStall := func() { releaseOnce.Do(func() { close(release) }) }
	stalledGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway" && r.URL.Path != "/gateway/" {
			http.Error(w, "unexpected stalled gateway path", http.StatusNotFound)
			return
		}
		// Receiving the actual upgrade request proves that Open has entered
		// its handshake, not merely that a reconnect goroutine was scheduled.
		enteredOnce.Do(func() { close(entered) })
		<-release
		http.Error(w, "controlled handshake stall released", http.StatusServiceUnavailable)
	}))
	t.Cleanup(func() {
		releaseStall()
		stalledGateway.Close()
	})
	stalledURL, err := url.Parse(stalledGateway.URL)
	require.NoError(t, err)
	var stall atomic.Bool
	originalDial := fixture.adapter.session.Dialer.NetDialContext
	fixture.adapter.session.Dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if stall.Load() {
			return (&net.Dialer{}).DialContext(ctx, network, stalledURL.Host)
		}
		return originalDial(ctx, network, address)
	}
	// Longer than stopBudget, but NEVER awaited: the test releases the server
	// after observing Stop or its 3 s failure. Both endpoints are loopback-only.
	fixture.adapter.session.Dialer.HandshakeTimeout = 30 * time.Second
	peer := fixture.start(t, ctx)
	fixture.forwarded(t, ctx, peer, "round2-before-stall", "live connection control")
	stall.Store(true)
	require.NoError(t, peer.conn.Close(), "instrument: actual disconnect starts the production reconnect loop")
	ghostReceive(t, ctx, entered)
	t.Logf("instrument: reconnect Open handshake entered; Stop budget=%s from 30 s service-stop / 90 s reload budget", stopBudget)

	stopped := make(chan error, 1)
	started := time.Now()
	go func() { stopped <- fixture.adapter.Stop(ctx) }()
	// Always release and join before the original fixture's cleanup calls
	// Stop again, even when the latency assertion is RED.
	t.Cleanup(releaseStall)
	timer := time.NewTimer(stopBudget)
	defer timer.Stop()
	select {
	case err := <-stopped:
		assert.NoError(t, err, "Stop must complete without a gateway handshake reply")
		t.Logf("Stop returned after %s while the handshake server was held", time.Since(started))
	case <-timer.C:
		assert.Fail(t, "Stop exceeded the reload-derived adapter budget", "Stop did not return within %s while reconnect Open's handshake remained blocked", stopBudget)
		releaseStall()
		assert.NoError(t, ghostReceive(t, ctx, stopped), "cleanup: releasing the stall must let Stop finish")
	}
	assert.False(t, fixture.adapter.IsRunning(), "after Stop the stalled adapter must remain stopped")
}
