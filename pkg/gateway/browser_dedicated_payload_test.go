package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// R1/R4 and BrowserInputFrame define an 8192-character text limit, modifiers
// 0..15, and server-side channel-kind validation. Keep actual data channels,
// decoding, schema validation and serial dispatch real; observe the peer's
// sink boundary. This does not claim browser execution or capture authority.
// Empty text is valid at this boundary; the common browser action builder
// separately refuses it. Mutation targets are schema validation and the
// channel-kind/coordinate checks, not a fabricated successful browser action.
func TestDedicatedPayloadUsesRealSchemaAndChannelAdmission(t *testing.T) {
	cases := []struct {
		name, kind, text, failure string
		hover                     bool
		modifiers                 *int
	}{
		{name: "empty text", kind: "text"},
		{name: "maximum text", kind: "text", text: strings.Repeat("a", 8192)},
		{name: "oversized text", kind: "text", text: strings.Repeat("a", 8193), failure: "invalid input payload"},
		{name: "negative modifiers", kind: "text", text: "a", modifiers: payloadInt(-1), failure: "invalid input payload"},
		{name: "overflow modifiers", kind: "text", text: "a", modifiers: payloadInt(16), failure: "invalid input payload"},
		{name: "button missing coordinates", kind: "mouse_down", failure: "button input requires coordinates"},
		{name: "text on hover", kind: "text", text: "a", hover: true, failure: "invalid hover payload"},
		{name: "key on hover", kind: "key_down", hover: true, failure: "invalid hover payload"},
		{name: "wheel on hover", kind: "wheel", hover: true, failure: "invalid hover payload"},
		{name: "button on hover", kind: "mouse_down", hover: true, failure: "invalid hover payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			states := make(chan string, 8)
			delivered := make(chan generated.BrowserInputFrame, 2)
			peer := webrtc.NewDedicatedInputPeer(ctx, webrtc.Config{}, 1, 0,
				func(_ context.Context, frame generated.BrowserInputFrame) { delivered <- frame },
				func(raw []byte) error {
					message, serverError := ValidateInboundFrameJSON("BrowserInputFrame", raw)
					if serverError {
						t.Errorf("schema unavailable: %s", message)
					}
					if message != "" {
						return errors.New(message)
					}
					return nil
				}, func(reason string) { states <- reason })
			defer func() {
				peer.Close()
				select {
				case <-peer.Closed():
				case <-time.After(time.Second):
					t.Error("dedicated peer cleanup did not join")
				}
			}()
			settings := pion.SettingEngine{}
			settings.SetIncludeLoopbackCandidate(true)
			client, err := pion.NewAPI(pion.WithSettingEngine(settings)).NewPeerConnection(pion.Configuration{})
			require.NoError(t, err)
			defer client.Close()
			reliable, err := client.CreateDataChannel("input-reliable", &pion.DataChannelInit{Protocol: func() *string { value := webrtc.InputBinaryProtocol; return &value }()})
			require.NoError(t, err)
			ordered, retries := false, uint16(0)
			hover, err := client.CreateDataChannel("input-hover", &pion.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retries, Protocol: func() *string { value := webrtc.InputBinaryProtocol; return &value }()})
			require.NoError(t, err)
			channel := reliable
			if tc.hover {
				channel = hover
			}
			opened := make(chan struct{})
			channel.OnOpen(func() { close(opened) })
			offer, err := client.CreateOffer(nil)
			require.NoError(t, err)
			gathered := pion.GatheringCompletePromise(client)
			require.NoError(t, client.SetLocalDescription(offer))
			select {
			case <-gathered:
			case <-ctx.Done():
				t.Fatal("client ICE gathering timed out")
			}
			answer, err := peer.Answer(ctx, client.LocalDescription().SDP)
			require.NoError(t, err)
			require.NoError(t, client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}))
			select {
			case state := <-states:
				require.Equal(t, "ready", state)
			case <-ctx.Done():
				t.Fatal("dedicated channels did not become ready")
			}
			select {
			case <-opened:
			case <-ctx.Done():
				t.Fatal("client channel did not open")
			}
			frame := generated.BrowserInputFrame{Type: "browser_input", Kind: tc.kind, Text: &tc.text, Modifiers: tc.modifiers,
				InputEpoch: payloadInt(1), ControlEpoch: payloadInt(0), ReliableSeq: payloadInt(1), GestureBarrier: payloadInt(0)}
			if tc.hover {
				frame.ReliableSeq, frame.HoverSeq = nil, payloadInt(1)
				// Keep button coordinates valid so the channel-kind guard is
				// the sole intended refusal for the button-on-hover case.
				x, y := 1.0, 2.0
				frame.X, frame.Y = &x, &y
			}
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			var wireFrame generated.BrowserInputFrame
			require.NoError(t, json.Unmarshal(raw, &wireFrame))
			packet, err := webrtc.EncodeInputPacket(wireFrame)
			require.NoError(t, err)
			require.NoError(t, channel.Send(packet))
			if tc.failure == "" {
				select {
				case got := <-delivered:
					require.Equal(t, frame, got, "accepted payload must survive actual channel decoding exactly")
				case state := <-states:
					t.Fatalf("valid payload failed: %s", state)
				case <-ctx.Done():
					t.Fatal("valid payload did not reach dedicated sink")
				}
				peer.Close()
			} else {
				select {
				case state := <-states:
					require.Equal(t, tc.failure, state)
				case got := <-delivered:
					t.Fatalf("refused payload reached sink: kind=%s text length=%d", got.Kind, len(tc.text))
				case <-ctx.Done():
					t.Fatal("invalid payload was neither refused nor dispatched")
				}
			}
			select {
			case <-peer.Closed():
			case <-ctx.Done():
				t.Fatal("payload completion did not join peer retirement")
			}
			select {
			case extra := <-delivered:
				t.Fatalf("unexpected extra dispatch after retirement: %s", extra.Kind)
			default:
			}
		})
	}
}

func payloadInt(value int) *int { return &value }
