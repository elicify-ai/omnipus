// browser_ws_input_test.go: tests for translate socket input events (keys, pointer, scroll) into browser commands.

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/require"
)

// --- moved from browser_ws.go tests 2026-09-15 ---

// ---------------------------------------------------------------------------
// Input mapping parity (wave-plan W2-A item 4)
// ---------------------------------------------------------------------------

func TestBrowserInputFrameToLiveInput_TableParity(t *testing.T) {
	f64 := func(v float64) *float64 { return &v }
	str := func(v string) *string { return &v }
	i := func(v int) *int { return &v }

	cases := []struct {
		name  string
		frame generated.BrowserInputFrame
		want  browser.LiveInput
	}{
		{
			name:  "mouse_move with coords",
			frame: generated.BrowserInputFrame{Kind: "mouse_move", X: f64(12.5), Y: f64(30)},
			want:  browser.LiveInput{Kind: "mouse_move", X: 12.5, Y: 30, HasXY: true},
		},
		{
			name:  "mouse_down with button",
			frame: generated.BrowserInputFrame{Kind: "mouse_down", X: f64(1), Y: f64(2), Button: str("left")},
			want:  browser.LiveInput{Kind: "mouse_down", X: 1, Y: 2, HasXY: true, Button: "left"},
		},
		{
			name:  "wheel with deltas, no coords",
			frame: generated.BrowserInputFrame{Kind: "wheel", DeltaX: f64(-5), DeltaY: f64(10)},
			want:  browser.LiveInput{Kind: "wheel", DeltaX: -5, DeltaY: 10, HasXY: false},
		},
		{
			name: "key_down with key/code/keycode/modifiers",
			frame: generated.BrowserInputFrame{
				Kind: "key_down", Key: str("Enter"), Code: str("Enter"),
				KeyCode: i(13), Modifiers: i(2),
			},
			want: browser.LiveInput{Kind: "key_down", Key: "Enter", Code: "Enter", KeyCode: 13, Modifiers: 2},
		},
		{
			name:  "text",
			frame: generated.BrowserInputFrame{Kind: "text", Text: str("hello")},
			want:  browser.LiveInput{Kind: "text", Text: "hello"},
		},
		{
			name:  "navigate with url, no coords",
			frame: generated.BrowserInputFrame{Kind: "navigate", Url: str("https://example.com")},
			want:  browser.LiveInput{Kind: "navigate", URL: "https://example.com"},
		},
		{
			name:  "coordinate omitted entirely (HasXY must stay false, not silently 0,0)",
			frame: generated.BrowserInputFrame{Kind: "mouse_up"},
			want:  browser.LiveInput{Kind: "mouse_up", HasXY: false},
		},
		{
			name:  "explicit 0,0 coordinates ARE HasXY=true (distinguishable from omitted)",
			frame: generated.BrowserInputFrame{Kind: "mouse_move", X: f64(0), Y: f64(0)},
			want:  browser.LiveInput{Kind: "mouse_move", X: 0, Y: 0, HasXY: true},
		},
		{
			// Root-cause doc Fault 3
			// (docs/internal/browser-viewport-input-rootcause-2026-07-31.md):
			// capture_width/capture_height carry the intrinsic pixel size of
			// the capture frame the client mapped x/y into, so
			// dispatchInput can rescale into the tab's real CSS viewport.
			name: "mouse_move with capture_width/capture_height set",
			frame: generated.BrowserInputFrame{
				Kind: "mouse_move", X: f64(100), Y: f64(79),
				CaptureWidth: f64(319), CaptureHeight: f64(158),
			},
			want: browser.LiveInput{
				Kind: "mouse_move", X: 100, Y: 79, HasXY: true,
				CaptureWidth: 319, CaptureHeight: 158,
			},
		},
		{
			// Omitted capture_width/capture_height (older client, or a
			// server that never populated them) must convert to the zero
			// value, not some sentinel — dispatchInput's rescale gate
			// (CaptureWidth > 0 && CaptureHeight > 0) relies on exactly
			// this to mean "dispatch unscaled".
			name:  "mouse_down with capture_width/capture_height omitted",
			frame: generated.BrowserInputFrame{Kind: "mouse_down", X: f64(5), Y: f64(6), Button: str("left")},
			want: browser.LiveInput{
				Kind: "mouse_down", X: 5, Y: 6, HasXY: true, Button: "left",
				CaptureWidth: 0, CaptureHeight: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserInputFrameToLiveInput(tc.frame)
			require.Equal(t, tc.want, got)
		})
	}
}
