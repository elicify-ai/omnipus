//go:build race

package agent

// chokePointRaceDetector is true when this package is compiled with -race.
// TestChokePoint_EncodedLineBound's archive round-trip admits an 8 MB tool
// result and then regex-scans it; under the race detector that single
// subtest blows pkg/agent's 900s package budget (observed 2026-09-12 on
// ci-omnipus: FAIL github.com/elicify-ai/omnipus/pkg/agent 900.506s, twice).
// The non-race go-test gate still runs the full assertion.
const chokePointRaceDetector = true
