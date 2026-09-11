package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gorilla/websocket"
)

// captureIngestTransportError distinguishes an attempted socket operation from
// canceled admission. Gorilla retains write failures, so this socket must be
// retired even when the originating frame or request has since been canceled.
type captureIngestTransportError struct{ cause error }

func (e *captureIngestTransportError) Error() string { return e.cause.Error() }
func (e *captureIngestTransportError) Unwrap() error { return e.cause }

func isCaptureIngestTransportError(err error) bool {
	var transport *captureIngestTransportError
	return errors.As(err, &transport)
}

func (c *captureIngestConn) acquireWrite(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.writeOnce.Do(func() { c.writeGate = make(chan struct{}, 1) })
	select {
	case c.writeGate <- struct{}{}:
		return func() { <-c.writeGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *captureIngestConn) sendJSONContext(ctx context.Context, value any, current func() bool) error {
	if ctx == nil {
		return fmt.Errorf("capture-ingest: write requires an originating context")
	}
	writeCtx, cancel := context.WithTimeout(ctx, captureIngestWriteTimeout)
	defer cancel()
	admissible := func() error {
		if err := writeCtx.Err(); err != nil {
			return err
		}
		if current != nil && !current() {
			return context.Canceled
		}
		// A state validator may wait for its lock while the source retires.
		if err := ctx.Err(); err != nil {
			return err
		}
		return writeCtx.Err()
	}
	if err := admissible(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("capture-ingest: marshal frame: %w", err)
	}
	release, err := c.acquireWrite(writeCtx)
	if err != nil {
		return err
	}
	defer release()
	deadline, _ := writeCtx.Deadline()
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return &captureIngestTransportError{cause: fmt.Errorf("capture-ingest: set write deadline: %w", err)}
	}
	if err := admissible(); err != nil {
		return err
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return &captureIngestTransportError{cause: fmt.Errorf("capture-ingest: write frame: %w", err)}
	}
	return nil
}
