package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// R3: the signaling release must retire current input without waiting for ICE
// disconnect detection. Drive a real data-channel key_down into the serial
// sink and hold its completion after cancellation. No release acknowledgment
// may precede that join. This tests queue/handler retirement, not CDP key-up;
// the live held-key fixture supplies the browser-visible release oracle.
func TestDedicatedReleaseCancelsLivePeerSourceBeforeAcknowledgment(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	f.state.setDedicatedInput(true)
	ctx, cancel := context.WithTimeout(f.original, 5*time.Second)
	entered := make(chan context.Context, 1)
	ended := make(chan struct{})
	finish := make(chan struct{})
	var once sync.Once
	finishDispatch := func() { once.Do(func() { close(finish) }) }
	states := make(chan string, 4)
	peer := webrtc.NewDedicatedInputPeer(ctx, webrtc.Config{}, 1, 0,
		func(source context.Context, frame generated.BrowserInputFrame) {
			if frame.Kind != "key_down" || frame.Code == nil || *frame.Code != "ArrowLeft" {
				t.Errorf("unexpected dispatched input: %+v", frame)
			}
			entered <- source
			<-source.Done()
			close(ended)
			<-finish
		}, func(raw []byte) error {
			message, _ := ValidateInboundFrameJSON("BrowserInputFrame", raw)
			if message != "" {
				return errors.New(message)
			}
			return nil
		}, func(reason string) { states <- reason })
	t.Cleanup(func() {
		finishDispatch()
		cancel()
		f.state.setDedicatedInput(false)
		peer.Close()
		<-peer.Closed()
	})
	d := f.state.dedicatedInput()
	d.mu.Lock()
	d.epoch, d.offer, d.peer, d.cancel = 1, 1, peer, cancel
	d.mu.Unlock()
	client, reliable := dedicatedReleaseClient(t, ctx, peer)
	t.Cleanup(func() { _ = client.Close() })
	select {
	case state := <-states:
		require.Equal(t, "ready", state)
	case <-ctx.Done():
		t.Fatal("server channels did not become ready")
	}
	sendDedicatedBinaryJSON(t, reliable, `{"type":"browser_input","kind":"key_down","key":"ArrowLeft","code":"ArrowLeft","input_epoch":1,"control_epoch":0,"reliable_seq":1,"gesture_barrier":1}`)
	var oldSource context.Context
	select {
	case oldSource = <-entered:
	case <-ctx.Done():
		t.Fatal("real key input did not enter server dispatch")
	}
	require.NoError(t, oldSource.Err())
	f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user",
		[]byte(`{"type":"browser_control","action":"release","input_epoch":1,"control_epoch":1}`), "browser_control", f.cfg)
	require.ErrorIs(t, oldSource.Err(), context.Canceled, "socket admission must immediately retire live input")
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("in-flight dispatch did not observe release cancellation")
	}
	select {
	case premature := <-f.conn.sendCh:
		t.Fatalf("release replied before dispatch joined: %s", premature.data)
	default:
	}
	finishDispatch()
	f.handler.Wait()
	require.NoError(t, ctx.Err(), "release must not cancel the attachment's peer owner")
	require.NoError(t, peer.RetiredSource().Err(), "completed control must resume a fresh source")
	require.NotSame(t, oldSource, peer.RetiredSource())
	select {
	case <-peer.Closed():
		t.Fatal("explicit release depended on closing the native peer")
	default:
	}
	var types []string
	var ack generated.BrowserInputControlAckFrame
	for {
		select {
		case envelope := <-f.conn.sendCh:
			require.True(t, f.conn.canSendFrame(envelope))
			var header struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(envelope.data, &header))
			types = append(types, header.Type)
			if header.Type == "browser_input_control_ack" {
				require.NoError(t, json.Unmarshal(envelope.data, &ack))
			}
		default:
			require.Equal(t, []string{"browser_status", "browser_input_control_ack"}, types)
			require.True(t, ack.Ok)
			require.Equal(t, "chat", ack.SessionId)
			require.Equal(t, 1, ack.InputEpoch)
			require.Equal(t, 1, ack.ControlEpoch)
			require.Nil(t, ack.Reason)
			return
		}
	}
}

func dedicatedReleaseClient(t *testing.T, ctx context.Context, peer *webrtc.DedicatedInputPeer) (*pion.PeerConnection, *pion.DataChannel) {
	t.Helper()
	settings := pion.SettingEngine{}
	settings.SetIncludeLoopbackCandidate(true)
	client, err := pion.NewAPI(pion.WithSettingEngine(settings)).NewPeerConnection(pion.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	reliable, err := client.CreateDataChannel("input-reliable", &pion.DataChannelInit{Protocol: func() *string { value := webrtc.InputBinaryProtocol; return &value }()})
	require.NoError(t, err)
	opened := make(chan struct{})
	reliable.OnOpen(func() { close(opened) })
	ordered, retries := false, uint16(0)
	_, err = client.CreateDataChannel("input-hover", &pion.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retries, Protocol: func() *string { value := webrtc.InputBinaryProtocol; return &value }()})
	require.NoError(t, err)
	offer, err := client.CreateOffer(nil)
	require.NoError(t, err)
	gathered := pion.GatheringCompletePromise(client)
	require.NoError(t, client.SetLocalDescription(offer))
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatal("client gathering timed out")
	}
	answer, err := peer.Answer(ctx, client.LocalDescription().SDP)
	require.NoError(t, err)
	require.NoError(t, client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}))
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("client reliable channel did not open")
	}
	return client, reliable
}

func sendDedicatedBinaryJSON(t *testing.T, channel *pion.DataChannel, raw string) {
	t.Helper()
	var frame generated.BrowserInputFrame
	require.NoError(t, json.Unmarshal([]byte(raw), &frame))
	packet, err := webrtc.EncodeInputPacket(frame)
	require.NoError(t, err)
	require.NoError(t, channel.Send(packet))
}
