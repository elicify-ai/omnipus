package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// R3: installing a new input owner must join every admitted control transition.
// The constructor creates a real queue/peer; no native connection is negotiated.
// A context notification makes waiting observable without scheduling sleeps.
type dedicatedInstallContext struct {
	context.Context
	waiting chan struct{}
}

func (c dedicatedInstallContext) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestDedicatedInstallWaitsForLatestAppliedControl(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		name := "pending control"
		if supersede {
			name = "newer control admitted before wake"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			observed := dedicatedInstallContext{Context: ctx, waiting: make(chan struct{}, 1)}
			d := &browserDedicatedInput{epoch: 1, offer: 1, control: 1, controlChanged: make(chan struct{})}
			defer d.close()
			constructed := make(chan int, 1)
			type result struct {
				peer *webrtc.DedicatedInputPeer
				err  error
			}
			finished := make(chan result, 1)
			go func() {
				peer, err := d.installPeer(observed, 1, 1, func(control int) (*webrtc.DedicatedInputPeer, error) {
					constructed <- control
					return webrtc.NewDedicatedInputPeer(ctx, webrtc.Config{}, 1, control,
						func(context.Context, generated.BrowserInputFrame) {}, func([]byte) error { return nil }, func(string) {}), nil
				})
				finished <- result{peer, err}
			}()
			select {
			case <-observed.waiting:
			case got := <-finished:
				t.Fatalf("install returned before control applied: %v", got.err)
			case <-ctx.Done():
				t.Fatal("installer did not wait for control")
			}
			select {
			case control := <-constructed:
				t.Fatalf("constructed peer before control applied: control=%d", control)
			default:
			}
			want := 1
			d.mu.Lock()
			d.applied = 1
			if supersede {
				d.control = 2
				want = 2
			}
			close(d.controlChanged)
			d.controlChanged = make(chan struct{})
			d.mu.Unlock()
			if supersede {
				select {
				case <-observed.waiting:
				case got := <-finished:
					t.Fatalf("install returned before newer control applied: %v", got.err)
				case <-ctx.Done():
					t.Fatal("installer did not wait for newer control")
				}
				select {
				case control := <-constructed:
					t.Fatalf("constructed peer while newer control pending: control=%d", control)
				default:
				}
				d.mu.Lock()
				d.applied = 2
				close(d.controlChanged)
				d.controlChanged = make(chan struct{})
				d.mu.Unlock()
			}
			select {
			case got := <-finished:
				if got.err != nil || got.peer == nil {
					t.Fatalf("applied control did not permit install: peer=%p err=%v", got.peer, got.err)
				}
				defer func() {
					got.peer.Close()
					select {
					case <-got.peer.Closed():
					case <-time.After(time.Second):
						t.Error("installed peer cleanup did not join")
					}
				}()
				d.mu.Lock()
				published := d.peer
				d.mu.Unlock()
				if published != got.peer {
					t.Fatal("returned peer was not published as owner")
				}
				if control := <-constructed; control != want {
					t.Fatalf("installed control=%d want=%d", control, want)
				}
			case <-ctx.Done():
				t.Fatal("applied control did not wake installation")
			}
		})
	}
}

func TestDedicatedInstallRejectsRetiredOwnershipBeforeConstruction(t *testing.T) {
	for _, reason := range []string{"canceled", "wrong epoch", "wrong offer", "closed"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			d := &browserDedicatedInput{epoch: 1, offer: 1}
			epoch, offer := 1, 1
			switch reason {
			case "canceled":
				cancel()
			case "wrong epoch":
				epoch = 2
			case "wrong offer":
				offer = 2
			case "closed":
				d.close()
			}
			calls := 0
			peer, err := d.installPeer(ctx, epoch, offer, func(int) (*webrtc.DedicatedInputPeer, error) {
				calls++
				return nil, errors.New("constructor must not execute")
			})
			if calls != 0 || peer != nil || err == nil {
				t.Fatalf("retired owner constructed peer: calls=%d peer=%p err=%v", calls, peer, err)
			}
			if reason == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error=%v want context.Canceled", err)
			}
		})
	}
}

func TestDedicatedInstallPendingControlWakesOnRetirement(t *testing.T) {
	for _, closeOwner := range []bool{false, true} {
		name := "context cancellation"
		if closeOwner {
			name = "owner close"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := dedicatedInstallContext{Context: ctx, waiting: make(chan struct{}, 1)}
			d := &browserDedicatedInput{epoch: 1, offer: 1, control: 1, controlChanged: make(chan struct{})}
			defer d.close()
			finished := make(chan error, 1)
			go func() {
				_, err := d.installPeer(observed, 1, 1, func(int) (*webrtc.DedicatedInputPeer, error) {
					t.Error("retirement constructed a peer during pending control")
					return nil, errors.New("unexpected constructor")
				})
				finished <- err
			}()
			select {
			case <-observed.waiting:
			case err := <-finished:
				t.Fatalf("installer returned before retirement: %v", err)
			case <-time.After(time.Second):
				t.Fatal("installer did not enter pending wait")
			}
			if closeOwner {
				d.close()
			} else {
				cancel()
			}
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("retirement returned success")
				}
				if !closeOwner && !errors.Is(err, context.Canceled) {
					t.Fatalf("pending cancellation error=%v want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("retirement did not wake pending installer")
			}
		})
	}
}
