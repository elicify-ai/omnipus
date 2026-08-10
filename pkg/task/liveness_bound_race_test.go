//go:build race

package task

import "time"

// livenessBound is the wall-clock ceiling for SC-008's "rejects a
// never-matching rule promptly" assertion, RAISED under the race detector.
//
// The detector instruments every memory access, so a CPU-bound rrule scan
// costs several times its normal wall clock: measured on this repo, the Feb-31
// rejection takes ~0.2s normally and 1.0–1.9s under -race, against a 1s bound.
// pkg/task joined the -race gate on 2026-08-10, which turned that into a
// DETERMINISTIC red gate — reproduced 3/3 isolated runs.
//
// A false-RED gate gets ignored, and an ignored gate is worth less than no
// gate; that is the same reasoning the gate's own comment uses to exclude
// pkg/tools/browser. Raising the ceiling under -race keeps the property under
// test (it must return, not stall) without asserting a number the detector
// makes meaningless.
const livenessBound = 10 * time.Second
