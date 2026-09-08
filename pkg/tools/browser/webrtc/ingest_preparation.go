package webrtc

import (
	"context"
	"errors"
	"sync"
)

const ingestPreparationLimit = 4

var errIngestPreparationBusy = errors.New("webrtc: ingest candidate preparation busy")

// Native SDP preparation cannot be interrupted. Admission remains occupied
// until negotiation and candidate or retired connection cleanup finish. The
// currently installed connection has its own lifetime and consumes no slot.
type ingestPreparationPool struct {
	mu     sync.Mutex
	active int
}

// runWithCleanup publishes the answer before retiring the previous connection,
// while retaining this worker's capacity until its native cleanup completes.
func (p *ingestPreparationPool) runWithCleanup(ctx context.Context, work func() (string, func(), error)) (string, error) {
	p.mu.Lock()
	if err := context.Cause(ctx); err != nil {
		p.mu.Unlock()
		return "", err
	}
	if p.active >= ingestPreparationLimit {
		p.mu.Unlock()
		return "", errIngestPreparationBusy
	}
	p.active++
	p.mu.Unlock()
	type result struct {
		answer string
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		defer func() {
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
		if err := context.Cause(ctx); err != nil {
			completed <- result{err: err}
			return
		}
		answer, cleanup, err := work()
		completed <- result{answer, err}
		if cleanup != nil {
			cleanup()
		}
	}()
	select {
	case result := <-completed:
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
		return result.answer, result.err
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}
