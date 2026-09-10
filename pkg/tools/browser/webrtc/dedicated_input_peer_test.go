package webrtc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	pion "github.com/pion/webrtc/v4"
)

func TestDedicatedInputPeerChannelsAndRetirement(t *testing.T) {
	for _, invalid := range []string{"", "duplicate", "wrong-options", "wrong-label"} {
		t.Run(invalid, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			states := make(chan string, 8)
			delivered := make(chan generated.BrowserInputFrame, 1)
			canceled := make(chan struct{})
			finish := make(chan struct{})
			var finishOnce sync.Once
			release := func() { finishOnce.Do(func() { close(finish) }) }
			defer release()
			peer := NewDedicatedInputPeer(ctx, Config{}, 1, 0, func(source context.Context, frame generated.BrowserInputFrame) {
				delivered <- frame
				<-source.Done()
				close(canceled)
				<-finish
			}, func([]byte) error { return nil }, func(s string) { states <- s })
			defer func() {
				release()
				peer.Close()
				select {
				case <-peer.Closed():
				case <-time.After(5 * time.Second):
					t.Error("peer cleanup did not finish")
				}
			}()
			se := pion.SettingEngine{}
			se.SetIncludeLoopbackCandidate(true)
			client, err := pion.NewAPI(pion.WithSettingEngine(se)).NewPeerConnection(pion.Configuration{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			reliable, err := client.CreateDataChannel("input-reliable", nil)
			if err != nil {
				t.Fatal(err)
			}
			reliableOpen := make(chan struct{})
			reliable.OnOpen(func() { close(reliableOpen) })
			ordered, retries := false, uint16(0)
			if invalid == "wrong-options" {
				ordered = true
			}
			label := "input-hover"
			if invalid == "wrong-label" {
				label = "unknown"
			}
			if _, err = client.CreateDataChannel(label, &pion.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retries}); err != nil {
				t.Fatal(err)
			}
			if invalid == "duplicate" {
				if _, err = client.CreateDataChannel("input-reliable", nil); err != nil {
					t.Fatal(err)
				}
			}
			offer, err := client.CreateOffer(nil)
			if err != nil {
				t.Fatal(err)
			}
			gather := pion.GatheringCompletePromise(client)
			if err = client.SetLocalDescription(offer); err != nil {
				t.Fatal(err)
			}
			select {
			case <-gather:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			answer, err := peer.Answer(ctx, client.LocalDescription().SDP)
			if err != nil {
				t.Fatal(err)
			}
			if err = client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer}); err != nil {
				t.Fatal(err)
			}
			want := "ready"
			if invalid != "" {
				want = "invalid input data channel"
			}
			for {
				select {
				case actual := <-states:
					if actual == want {
						if invalid == "" {
							select {
							case <-reliableOpen:
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							}
							const payload = `{"type":"browser_input","kind":"text","text":"Zażółć 世界 👋","input_epoch":1,"control_epoch":0,"reliable_seq":1,"gesture_barrier":0}`
							if err := reliable.SendText(payload); err != nil {
								t.Fatal(err)
							}
							select {
							case frame := <-delivered:
								if frame.Kind != "text" || frame.Text == nil || *frame.Text != "Zażółć 世界 👋" {
									t.Fatalf("wrong delivered payload: %#v", frame)
								}
							case <-ctx.Done():
								t.Fatal("real data channel did not deliver input")
							}
							peer.Close()
							select {
							case <-canceled:
							case <-ctx.Done():
								t.Fatal("Close did not cancel dispatch")
							}
							if peer.RetiredSource().Err() == nil {
								t.Fatal("retired source remains live")
							}
							select {
							case <-peer.Closed():
								t.Fatal("Closed published while dispatch was still running")
							case <-time.After(100 * time.Millisecond):
							}
							release()
							select {
							case <-peer.Closed():
							case <-ctx.Done():
								t.Fatal("Close did not join dispatch")
							}
						}
						return
					}
					if actual != "ready" {
						t.Fatalf("state=%q want=%q", actual, want)
					}
				case <-ctx.Done():
					t.Fatalf("missing %q: %v", want, ctx.Err())
				}
			}
		})
	}
}
