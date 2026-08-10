//go:build !race

package task

import "time"

// livenessBound is SC-008's wall-clock ceiling for rejecting a never-matching
// rule. See the //go:build race variant for why the detector needs a different
// number — and why asserting the strict one under -race made the gate red.
const livenessBound = 1 * time.Second
