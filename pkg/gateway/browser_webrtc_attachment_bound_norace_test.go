//go:build !race

package gateway

import "time"

// browserAttachmentArrivalBound is the wall-clock ceiling for asserting that
// an attachment reaches a controlled CDP endpoint. See the //go:build race
// variant for why the race gate uses a wider bound.
const browserAttachmentArrivalBound = 5 * time.Second
