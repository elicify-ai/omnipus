// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
)

// parkedPuller holds the pull open until the context is cancelled, then
// returns a valid document anyway — modelling a GitHub release download that
// completes just as the gateway is stopping.
type parkedPuller struct {
	body  []byte
	ready chan struct{}
}

func (p *parkedPuller) Pull(ctx context.Context) ([]byte, error) {
	close(p.ready)
	<-ctx.Done()
	return p.body, nil
}

func (p *parkedPuller) LastPullDegraded() (bool, error) { return false, nil }
