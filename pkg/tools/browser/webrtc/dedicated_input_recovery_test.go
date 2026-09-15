package webrtc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// Keep the native data channels and queue real. The controlled Chrome boundary
// blocks an admitted key while its release waits; this test does not simulate
// DOM-held-state cleanup, which remains the gateway's source-drain contract.
func TestDedicatedInputPeerExpiryRecoversOnSameNativeChannels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type deliveredInput struct {
		source context.Context
		frame  generated.BrowserInputFrame
	}
	delivered := make(chan deliveredInput, 4)
	states := make(chan string, 8)
	failures := make(chan int, 2)
	canceled, finish := make(chan struct{}), make(chan struct{})
	var finishOnce sync.Once
	release := func() { finishOnce.Do(func() { close(finish) }) }
	defer release()
	peer := NewDedicatedInputPeer(ctx, Config{}, 1, 0, func(source context.Context, frame generated.BrowserInputFrame) {
		delivered <- deliveredInput{source, frame}
		if *frame.ControlEpoch == 0 {
			<-source.Done()
			close(canceled)
			<-finish
		}
	}, nil, func(reason string) { states <- reason })
	peer.SetControlFailureHandler(func(control int, reason string) {
		if reason != "reliable input queue expired" {
			t.Errorf("control failure=%q", reason)
		}
		failures <- control
	})
	defer func() {
		release()
		peer.Close()
		select {
		case <-peer.Closed():
		case <-time.After(5 * time.Second):
			t.Error("peer cleanup did not join")
		}
	}()
	settings := pion.SettingEngine{}
	settings.SetIncludeLoopbackCandidate(true)
	client, err := pion.NewAPI(pion.WithSettingEngine(settings)).NewPeerConnection(pion.Configuration{})
	require.NoError(t, err)
	defer client.Close()
	protocol := InputBinaryProtocol
	reliable, err := client.CreateDataChannel("input-reliable", &pion.DataChannelInit{Protocol: &protocol})
	require.NoError(t, err)
	ordered, retries := false, uint16(0)
	hover, err := client.CreateDataChannel("input-hover", &pion.DataChannelInit{Protocol: &protocol, Ordered: &ordered, MaxRetransmits: &retries})
	require.NoError(t, err)
	opened := make(chan struct{})
	reliable.OnOpen(func() { close(opened) })
	offer, err := client.CreateOffer(nil)
	require.NoError(t, err)
	gathered := pion.GatheringCompletePromise(client)
	require.NoError(t, client.SetLocalDescription(offer))
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	answer, err := peer.Answer(ctx, client.LocalDescription().SDP)
	require.NoError(t, err)
	nativePeer := peer.pc
	require.NoError(t, client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}))
	select {
	case state := <-states:
		require.Equal(t, "ready", state)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	send := func(control, seq int, kind string) {
		t.Helper()
		frame := pressureWheel(seq, 0, 0)
		frame.Kind = kind
		frame.ControlEpoch = &control
		frame.GestureBarrier = &seq
		key, code := "a", "KeyA"
		frame.Key, frame.Code = &key, &code
		packet, err := EncodeInputPacket(frame)
		require.NoError(t, err)
		require.NoError(t, reliable.Send(packet))
	}
	receive := func() deliveredInput {
		t.Helper()
		select {
		case event := <-delivered:
			return event
		case <-ctx.Done():
			t.Fatal("input was not delivered")
			return deliveredInput{}
		}
	}
	send(0, 1, "key_down")
	first := receive()
	require.Equal(t, 1, *first.frame.ReliableSeq)
	send(0, 2, "key_up")
	select {
	case control := <-failures:
		require.Equal(t, 0, control)
	case <-ctx.Done():
		t.Fatal("queue did not explicitly report expiry")
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("old source was not canceled")
	}
	require.NoError(t, peer.ctx.Err(), "expiry canceled the healthy input peer")
	require.Equal(t, pion.PeerConnectionStateConnected, nativePeer.ConnectionState())
	require.Equal(t, pion.DataChannelStateOpen, reliable.ReadyState())
	require.Equal(t, pion.DataChannelStateOpen, hover.ReadyState())
	source, done, err := peer.PauseControl(1)
	require.NoError(t, err)
	require.Same(t, first.source, source)
	require.Error(t, peer.ResumeControl(1), "old dispatch must join before new control")
	// Unblock the original command's cleanup, without releasing a new source.
	release()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("old dispatch did not join")
	}
	require.NoError(t, peer.ResumeControl(1))
	send(0, 3, "key_down") // A late old packet must not consume new sequence 1.
	send(1, 1, "key_down")
	fresh := receive()
	require.Equal(t, 1, *fresh.frame.ControlEpoch)
	require.Equal(t, 1, *fresh.frame.ReliableSeq)
	require.NotSame(t, first.source, fresh.source)
	send(1, 2, "key_up")
	released := receive()
	require.Equal(t, 1, *released.frame.ControlEpoch)
	require.Equal(t, 2, *released.frame.ReliableSeq)
	require.Same(t, nativePeer, peer.pc)
	require.Equal(t, pion.PeerConnectionStateConnected, nativePeer.ConnectionState())
	require.Equal(t, pion.DataChannelStateOpen, reliable.ReadyState())
	select {
	case duplicate := <-delivered:
		t.Fatalf("old/duplicate input replayed: %+v", duplicate.frame)
	default:
	}
	select {
	case reason := <-states:
		t.Fatalf("healthy peer failed: %s", reason)
	default:
	}
}
