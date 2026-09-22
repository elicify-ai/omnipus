//go:build race

package gateway

import "time"

// browserAttachmentArrivalBound is the wall-clock ceiling for asserting that
// an attachment reaches a controlled CDP endpoint, RAISED under the race
// detector. During a full package run, detector instrumentation and competing
// tests delayed this 300ms isolated operation beyond the five-second budget.
// Keep the bound finite so the test still proves attachment arrives promptly
// and a blocking offer cannot hang the test.
const browserAttachmentArrivalBound = 30 * time.Second
