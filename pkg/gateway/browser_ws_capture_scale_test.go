package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// browserViewportContractScaleRegexp matches the device_scale_factor
// property block in contracts/components/schemas/BrowserViewportFrame.yaml
// and captures its declared minimum/maximum. Anchored on the exact
// "type: number" line so it cannot accidentally match a different numeric
// property that happens to be named similarly.
var browserViewportContractScaleRegexp = regexp.MustCompile(
	`(?m)^  device_scale_factor:\n    type: number\n    minimum: (\d+(?:\.\d+)?)\n    maximum: (\d+(?:\.\d+)?)`,
)

// browserViewportContractScaleRange reads device_scale_factor's declared
// [minimum, maximum] straight out of the wire contract (Constraint #8's
// single source of truth) rather than from any Go constant, so tests using
// it cannot be made to pass by moving the Go-side constant they are meant to
// police. See this file's oracle-independence note on the F10 tests below:
// the original defect was exactly the opposite of this — the expected value
// WAS the constant under test.
func browserViewportContractScaleRange(t *testing.T) (rangeMin, rangeMax float64) {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "components", "schemas", "BrowserViewportFrame.yaml")
	raw, err := os.ReadFile(path) // gosec rationale (out of gosec scope; kept as documentation): fixed, repo-relative contract path
	require.NoError(t, err, "the contract must be readable — it is the authority on this range")

	m := browserViewportContractScaleRegexp.FindSubmatch(raw)
	require.NotNil(t, m,
		"BrowserViewportFrame.device_scale_factor must still declare minimum/maximum for this test to mean anything")

	minVal, err := strconv.ParseFloat(string(m[1]), 64)
	require.NoError(t, err)
	maxVal, err := strconv.ParseFloat(string(m[2]), 64)
	require.NoError(t, err)
	return minVal, maxVal
}

// f64ptr is a tiny helper so test literals can address a float64 constant
// inline, matching generated.BrowserViewportFrame.DeviceScaleFactor's
// *float64 field.
func f64ptr(v float64) *float64 { return &v }

// The oracle is the wire contract; observations come from the real handler,
// live-view admission and CDP metrics command, not a requested-scale cache.
func assertMeasuredViewportClamp(t *testing.T, input, expected float64) {
	t.Helper()
	var mu sync.Mutex
	var sent []float64
	f := newHandlerContextFixture(t, false, func(_ int, _ int, scale float64) { mu.Lock(); sent = append(sent, scale); mu.Unlock() })
	raw, err := json.Marshal(generated.BrowserViewportFrame{Type: "browser_viewport", Width: 900, Height: 700, DeviceScaleFactor: f64ptr(input)})
	require.NoError(t, err)
	f.handler.handleViewport(f.conn, f.state, "fixture-viewer", raw)
	mu.Lock()
	observed := append([]float64(nil), sent...)
	mu.Unlock()
	require.NotEmpty(t, observed, "attached viewport never dispatched a metrics command")
	require.Equal(t, expected, observed[len(observed)-1], "CDP scale must match the contract clamp")
	err = f.manager.Live().RefreshCaptureFrameContext(context.Background(), "panel", f.capture)
	require.NoError(t, err)
	frame := f.capture.FrameState()
	require.Equal(t, 900, frame.Width)
	require.Equal(t, 700, frame.Height)
	require.Equal(t, expected, frame.Scale, "capture must publish the measured scale")
}

func TestBrowserWS_HandleViewport_ClampsOutOfRangeScale_BeforeRecording(t *testing.T) {
	_, maximum := browserViewportContractScaleRange(t)
	assertMeasuredViewportClamp(t, 50, maximum)
}
func TestBrowserWS_HandleViewport_ClampsSubOneScale_BeforeRecording(t *testing.T) {
	minimum, _ := browserViewportContractScaleRange(t)
	assertMeasuredViewportClamp(t, 0.5, minimum)
}
func TestBrowserWS_HandleViewport_ScaleClampBoundaries(t *testing.T) {
	minimum, maximum := browserViewportContractScaleRange(t)
	for _, tc := range []struct {
		name            string
		input, expected float64
	}{
		{"at_max_boundary_unchanged", maximum, maximum},
		{"just_above_max_clamped", maximum + 0.5, maximum},
		{"at_min_boundary_unchanged", minimum, minimum},
		{"just_below_min_floored", minimum - 0.5, minimum},
		{"mid_range_valid_unchanged", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) { assertMeasuredViewportClamp(t, tc.input, tc.expected) })
	}
}
func TestMaxDeviceScaleFactor_MatchesContractMaximum(t *testing.T) {
	_, maximum := browserViewportContractScaleRange(t)
	require.Equal(t, maximum, maxDeviceScaleFactor)
}

func TestCaptureIngest_Recapture_AlwaysSendsCaptureScale(t *testing.T) {
	cs, _, url := ingestWireFixture(t)
	_, err := cs.BeginFrameTransition("page-a", 800, 600, 2)
	require.NoError(t, err)
	conn := ingestWireConnect(t, cs, url)
	initial := ingestWireRead(t, conn, "browser_capture_control")
	require.Equal(t, float64(2), initial["capture_scale"])
	frame, err := cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	require.True(t, cs.RecaptureFrameContext(context.Background(), frame))
	downgrade := ingestWireRead(t, conn, "browser_capture_control")
	require.Equal(t, float64(1), downgrade["capture_scale"], "a measured downgrade to 1 must remain explicit on the wire")
	require.Equal(t, float64(frame.Generation), downgrade["capture_generation"])
}
