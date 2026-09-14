package webrtc

import (
	"context"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	pion "github.com/pion/webrtc/v4"
)

func TestDedicatedInputPeerChannelsAndRetirement(t *testing.T) {
	for _, invalid := range []string{"", "duplicate", "wrong-options", "wrong-label", "wrong-protocol", "json-message", "unknown-version", "truncated-message"} {
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
			reliable, err := client.CreateDataChannel("input-reliable", &pion.DataChannelInit{Protocol: func() *string { value := InputBinaryProtocol; return &value }()})
			if err != nil {
				t.Fatal(err)
			}
			reliableOpen := make(chan struct{})
			reliable.OnOpen(func() { close(reliableOpen) })
			ordered, retries := false, uint16(0)
			if invalid == "wrong-options" {
				ordered = true
			}
			protocol := InputBinaryProtocol
			if invalid == "wrong-protocol" {
				protocol = "omnipus.input.v2"
			}
			label := "input-hover"
			if invalid == "wrong-label" {
				label = "unknown"
			}
			if _, err = client.CreateDataChannel(label, &pion.DataChannelInit{Ordered: &ordered, MaxRetransmits: &retries, Protocol: &protocol}); err != nil {
				t.Fatal(err)
			}
			if invalid == "duplicate" {
				if _, err = client.CreateDataChannel("input-reliable", &pion.DataChannelInit{Protocol: &protocol}); err != nil {
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
			channelInvalid := invalid != "" && invalid != "json-message" && invalid != "unknown-version" && invalid != "truncated-message"
			if channelInvalid {
				want = "invalid input data channel"
			}
			for {
				select {
				case actual := <-states:
					if actual == want {
						if !channelInvalid {
							select {
							case <-reliableOpen:
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							}
							// Fixed v1 vector: text field (bit 10), epochs (15/16),
							// reliable sequence (17), barrier (19), all little-endian.
							payload, err := hex.DecodeString("4f4249010700840b00010061000000000000f03f0000000000000000000000000000f03f0000000000000000")
							if err != nil {
								t.Fatal(err)
							}
							if invalid != "" {
								wantFailure := "invalid input payload"
								if invalid == "json-message" {
									wantFailure = "invalid input message"
									err = reliable.SendText(`{"type":"browser_input","kind":"text","text":"a"}`)
								} else {
									if invalid == "unknown-version" {
										payload[3] = 2
									} else {
										payload = payload[:len(payload)-1]
									}
									err = reliable.Send(payload)
								}
								if err != nil {
									t.Fatal(err)
								}
								select {
								case failure := <-states:
									if failure != wantFailure {
										t.Fatalf("failure=%q want=%q", failure, wantFailure)
									}
								case <-ctx.Done():
									t.Fatal("malformed packet was not refused")
								}
								select {
								case frame := <-delivered:
									t.Fatalf("malformed packet dispatched: %#v", frame)
								default:
								}
								return
							}
							if err := reliable.Send(payload); err != nil {
								t.Fatal(err)
							}
							select {
							case frame := <-delivered:
								if frame.Kind != "text" || frame.Text == nil || *frame.Text != "a" {
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
