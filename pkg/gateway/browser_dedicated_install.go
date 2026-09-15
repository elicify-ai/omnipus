package gateway

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// installPeer serializes publication with control admission. No native peer is
// constructed while a previously admitted control is still changing the page.
func (d *browserDedicatedInput) installPeer(ctx context.Context, epoch, offer int, create func(int) (*webrtc.DedicatedInputPeer, error)) (*webrtc.DedicatedInputPeer, error) {
	for {
		d.mu.Lock()
		if err := ctx.Err(); err != nil {
			d.mu.Unlock()
			return nil, err
		}
		if d.closed || d.epoch != epoch || d.offer != offer {
			d.mu.Unlock()
			return nil, context.Canceled
		}
		if d.applied != d.control {
			if d.controlChanged == nil {
				d.controlChanged = make(chan struct{})
			}
			changed := d.controlChanged
			d.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-changed:
				continue
			}
		}
		peer, err := create(d.control)
		if err == nil && peer == nil {
			err = errors.New("input peer constructor returned no peer")
		}
		if err == nil {
			d.peer = peer
		}
		d.mu.Unlock()
		return peer, err
	}
}

// Called under d.mu after a successful control or ownership retirement.
func (d *browserDedicatedInput) notifyControlChangedLocked() {
	if d.controlChanged != nil {
		close(d.controlChanged)
		d.controlChanged = nil
	}
}
