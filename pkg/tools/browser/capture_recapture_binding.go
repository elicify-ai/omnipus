package browser

import (
	"context"
	"fmt"
)

// CaptureRecaptureSender admits an immutable measured frame at the transport.
// The transport must recheck current after waiting for its writer.
type CaptureRecaptureSender func(context.Context, CaptureFrameState, func() bool) error

// BindIngestRecaptureContext adds frame-qualified recapture to an ingest binding.
func (cs *CaptureSession) BindIngestRecaptureContext(ctx context.Context, send func(string, *string, int, int, int) error, recapture CaptureRecaptureSender, closeConn func()) (func(), uint64, error) {
	if recapture == nil {
		return nil, 0, fmt.Errorf("capture session: frame-qualified binding requires a recapture sender")
	}
	return cs.bindIngestContext(ctx, send, recapture, closeConn)
}
