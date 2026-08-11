//go:build !race

package browser

import "time"

// runFirstAttachPromptBound is coldstart_bound_test.go's wall-clock ceiling
// for asserting runFirstAttach returns promptly. See the //go:build race
// variant for why the detector needs a wider number — and why asserting this
// strict one under -race risked turning the -race gate red.
const runFirstAttachPromptBound = 200 * time.Millisecond
