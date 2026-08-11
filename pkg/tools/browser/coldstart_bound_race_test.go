//go:build race

package browser

import "time"

// runFirstAttachPromptBound is the wall-clock ceiling coldstart_bound_test.go
// uses to assert runFirstAttach returns "promptly" (either on timeout or on a
// fast underlying error), RAISED under the race detector.
//
// The detector instruments every memory access and goroutine scheduling
// point, so the handful of goroutine-hop/channel-select operations
// runFirstAttach performs cost measurably more wall clock under -race than
// normal. pkg/tools joined the -race gate on 2026-08-10 as `./pkg/tools`
// (non-recursive) specifically because pkg/tools/browser itself was not yet
// race-clean/race-gate-safe (#615); widening the glob to include this package
// makes the tight 200ms assertion a real risk of a false-RED gate under
// instrumentation load — the same shape pkg/task hit and fixed for SC-008
// (see pkg/task/liveness_bound_race_test.go). A false-RED gate gets ignored,
// and an ignored gate is worth less than no gate.
//
// Raising the ceiling under -race keeps the property under test (the
// function must return promptly, not hang for the slow path's full
// duration) without asserting a number the detector can make meaningless.
const runFirstAttachPromptBound = 2 * time.Second
