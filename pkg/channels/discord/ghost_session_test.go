package discord

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ghostTestContext(t *testing.T) context.Context {
	t.Helper()
	// The minute is a failure bound only. No assertion waits for it to expire;
	// progress comes from actual failed handshakes, authentication and dispatch.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	previousLogger := discordgo.Logger
	t.Cleanup(cancel)
	t.Cleanup(func() { discordgo.Logger = previousLogger })
	return ctx
}

// Oracle: channels-spec-r8 FR-050 + US-6 AS7, and team-lead's explicit ruling
// that no stopped adapter handler may run again. No production code is mocked.
func TestGhostDiscordStopWhileDisconnectedNeverReconnectsOrHandlesMessages(t *testing.T) {
	ctx := ghostTestContext(t)
	// Construct both before starting either: the real constructor sets the
	// discordgo package logger. The witness owns a separate socket and bus.
	subject := newGhostDiscordFixture(t, ctx)
	witness := newGhostDiscordFixture(t, ctx)
	initial := subject.start(t, ctx)
	witnessInitial := witness.start(t, ctx)
	subject.forwarded(t, ctx, initial, "before-stop", "message before Stop")
	subject.dispatch(t, ctx, initial, "denied-before-stop", ghostDeniedID, "handler instrument control")
	count, recent := subject.adapter.RejectedSenders()
	require.Equal(t, 1, count, "instrument: real pre-Stop handler must record the denied sender")
	require.Equal(t, []string{"discord:" + ghostDeniedID}, recent)

	// The fake stays unavailable until Stop returns. A failed upgrade proves
	// reconnection has begun. Open/Close's Session lock serializes any in-flight
	// failed Open with Stop, and no new live socket can appear during Stop.
	subject.gateway.outage(true, 0)
	require.NoError(t, initial.conn.Close(), "instrument: drop the established gateway socket")
	failed := ghostEvent(t, ctx, subject.gateway)
	require.Equal(t, "retry_failed", failed.kind, "instrument: Stop must occur after a failed reconnect, not on a live socket")
	require.NoError(t, subject.adapter.Stop(ctx))
	require.False(t, subject.adapter.IsRunning(), "Stop must finish before restoring the network")
	stoppedAt := time.Now()
	subject.gateway.outage(false, 0)

	// A real running witness supplies the observation window. Three rejected
	// upgrades then recovery span THREE back-off steps (1+2+4s in v0.29.0),
	// longer than the requested two. No guessed sleep or negative timeout.
	witness.gateway.outage(false, 3)
	require.NoError(t, witnessInitial.conn.Close())
	var witnessSteps []string
	var recovered *ghostGatewayPeer
	offered := 0
	offerStoppedMessage := func(event ghostGatewayEvent) {
		require.NoError(t, event.err, "instrument: subject gateway protocol fault")
		if event.kind == "authenticated" {
			offered++
			subject.dispatch(t, ctx, event.peer, fmt.Sprintf("after-stop-%d", offered), ghostLateID, "must not reach stopped handler")
			t.Logf("subject authenticated after Stop: op=%d; MESSAGE_CREATE dispatch completed", event.op)
		}
	}
	for recovered == nil {
		select {
		case event := <-subject.gateway.events:
			offerStoppedMessage(event)
		case event := <-witness.gateway.events:
			require.NoError(t, event.err, "instrument: witness gateway protocol fault")
			witnessSteps = append(witnessSteps, event.kind)
			t.Logf("witness step %d: %s at %s after Stop", len(witnessSteps), event.kind, event.at.Sub(stoppedAt).Round(time.Millisecond))
			if event.kind == "authenticated" {
				recovered = event.peer
			}
		case <-ctx.Done():
			t.Fatalf("instrument: witness did not complete its observable back-off window: %v", ctx.Err())
		}
	}
	require.Equal(t, []string{"retry_failed", "retry_failed", "retry_failed", "authenticated"}, witnessSteps)
	witness.forwarded(t, ctx, recovered, "witness-after-recovery", "running witness still handles messages")
	// Drain already-observed subject events before checking exact state.
	for pending := true; pending; {
		select {
		case event := <-subject.gateway.events:
			offerStoppedMessage(event)
		default:
			pending = false
		}
	}

	auth := subject.gateway.authentications()
	assert.Equal(t, []int{}, append([]int{}, auth[1:]...),
		"ghost: stopped adapter must send no IDENTIFY or RESUME after Stop")
	count, recent = subject.adapter.RejectedSenders()
	assert.Equal(t, 1, count,
		"ghost: stopped production handler ran again; rejected count must stay at its pre-Stop value")
	assert.Equal(t, []string{"discord:" + ghostDeniedID}, recent,
		"ghost: stopped production handler must not record the post-Stop MESSAGE_CREATE sender")
	assert.False(t, subject.adapter.IsRunning(), "ghost: stopped adapter must remain stopped")
	t.Logf("window complete: %d post-Stop handshakes/messages offered; rejected count=%d; elapsed=%s", len(auth)-1, count, time.Since(stoppedAt).Round(time.Millisecond))
}

// Positive control: fixing Stop must not silently disable recovery for an
// adapter that was never stopped (US-6 AS7 and the explicit dispatch).
func TestGhostDiscordRunningAdapterReconnectsAndHandlesMessages(t *testing.T) {
	ctx := ghostTestContext(t)
	fixture := newGhostDiscordFixture(t, ctx)
	initial := fixture.start(t, ctx)
	fixture.forwarded(t, ctx, initial, "running-before-drop", "message before drop")
	fixture.gateway.outage(false, 3)
	require.NoError(t, initial.conn.Close())
	var steps []string
	var recovered *ghostGatewayPeer
	for recovered == nil {
		event := ghostEvent(t, ctx, fixture.gateway)
		steps = append(steps, event.kind)
		t.Logf("running control step %d: %s", len(steps), event.kind)
		if event.kind == "authenticated" {
			recovered = event.peer
		}
	}
	require.Equal(t, []string{"retry_failed", "retry_failed", "retry_failed", "authenticated"}, steps,
		"instrument: running control must traverse actual retries, not a manually reopened session")
	fixture.forwarded(t, ctx, recovered, "running-after-recovery", "exact message after recovery")
	fixture.dispatch(t, ctx, recovered, "running-denied-after-recovery", ghostLateID, "handler instrument control")
	count, recent := fixture.adapter.RejectedSenders()
	assert.Equal(t, 1, count, "instrument: real reconnected handler must see denied messages too")
	assert.Equal(t, []string{"discord:" + ghostLateID}, recent)
	assert.Len(t, fixture.gateway.authentications(), 2, "running adapter must authenticate exactly one recovered connection")
	assert.True(t, fixture.adapter.IsRunning(), "running control was never stopped")
}
