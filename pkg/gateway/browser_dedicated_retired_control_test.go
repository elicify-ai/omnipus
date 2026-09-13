package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

// R3: a control arriving while a replacement joins its canceled predecessor
// must complete before installation, without retiring the new offer. Keep the
// real peer queue, control dispatcher, serial worker, and browser release path.
// Removing the canceled-peer join fallback must yield a failed state instead
// of the successful acknowledgment; removing the wake leaves changed open.
func TestDedicatedControlJoinsRetiredPeerWithoutCancelingReplacement(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	f.state.setDedicatedInput(true)
	t.Cleanup(func() { f.state.setDedicatedInput(false) })
	d := f.state.dedicatedInput()
	old := webrtc.NewDedicatedInputPeer(f.original, webrtc.Config{}, 1, 0,
		func(context.Context, generated.BrowserInputFrame) {}, func([]byte) error { return nil }, func(string) {})
	old.Close()
	t.Cleanup(func() { old.Close(); <-old.Closed() })
	replacement, cancel := context.WithCancel(f.original)
	defer cancel()
	changed := make(chan struct{})
	d.mu.Lock()
	d.epoch, d.offer, d.peer, d.cancel, d.controlChanged = 2, 2, old, cancel, changed
	d.mu.Unlock()
	f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user",
		[]byte(`{"type":"browser_control","action":"release","input_epoch":2,"control_epoch":1}`), "browser_control", f.cfg)
	f.handler.Wait()
	require.NoError(t, replacement.Err(), "the retired queue must not cancel its replacement offer")
	select {
	case <-old.Closed():
	default:
		t.Fatal("control acknowledgment preceded old peer retirement")
	}
	select {
	case <-changed:
	default:
		t.Fatal("successful control did not wake the pending installer")
	}
	d.mu.Lock()
	peer, applied, control := d.peer, d.applied, d.control
	d.mu.Unlock()
	require.Nil(t, peer, "joined predecessor must no longer be published")
	require.Equal(t, 1, applied)
	require.Equal(t, 1, control)
	var replies []string
	var ack generated.BrowserInputControlAckFrame
	for {
		select {
		case envelope := <-f.conn.sendCh:
			require.True(t, f.conn.canSendFrame(envelope))
			var header struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal(envelope.data, &header))
			replies = append(replies, header.Type)
			if header.Type == "browser_input_control_ack" {
				require.NoError(t, json.Unmarshal(envelope.data, &ack))
			}
		default:
			require.Equal(t, []string{"browser_status", "browser_input_control_ack"}, replies)
			require.True(t, ack.Ok)
			require.Equal(t, "chat", ack.SessionId)
			require.Equal(t, 2, ack.InputEpoch)
			require.Equal(t, 1, ack.ControlEpoch)
			require.Nil(t, ack.Reason)
			return
		}
	}
}
