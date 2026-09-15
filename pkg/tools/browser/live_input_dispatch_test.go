// live_input_dispatch_test.go: tests for translate validated live inputs into CDP input commands.

package browser

import (
	"testing"

	"github.com/chromedp/cdproto/input"
	"github.com/stretchr/testify/require"
)

// --- moved from live.go tests 2026-09-15 ---

// --- buildInputAction / mouseButton: pure CDP-action mapping (no browser) ---

func TestBuildInputAction(t *testing.T) {
	tests := []struct {
		name    string
		in      LiveInput
		wantErr bool
	}{
		{"mouse_move", LiveInput{Kind: "mouse_move", X: 1, Y: 2, HasXY: true}, false},
		{"mouse_down", LiveInput{Kind: "mouse_down", X: 1, Y: 2, HasXY: true, Button: "left"}, false},
		{"mouse_up", LiveInput{Kind: "mouse_up", X: 1, Y: 2, HasXY: true, Button: "right"}, false},
		{"wheel", LiveInput{Kind: "wheel", X: 1, Y: 2, HasXY: true, DeltaX: 3, DeltaY: 4}, false},
		{"key_down", LiveInput{Kind: "key_down", Key: "a", Code: "KeyA"}, false},
		{"key_up", LiveInput{Kind: "key_up", Key: "a", Code: "KeyA"}, false},
		{"text", LiveInput{Kind: "text", Text: "hello"}, false},
		{"text requires non-empty", LiveInput{Kind: "text", Text: ""}, true},
		// ADR-039 D-A2: navigate mirrors the pre-existing "text" empty guard —
		// buildInputAction rejects an empty URL on its own (no BrowserManager
		// needed for this check); the SSRF/scheme gate is a separate step in
		// dispatchInput, covered by the Navigate_* tests below.
		{"navigate", LiveInput{Kind: "navigate", URL: "http://example.com/path"}, false},
		{"navigate requires non-empty url", LiveInput{Kind: "navigate", URL: ""}, true},
		// 7-reviewer LOW finding (type-safety defense-in-depth): a navigate
		// input must not also carry mouse coordinates — see LiveInput.URL's
		// doc comment.
		{
			"navigate must not carry coordinates",
			LiveInput{Kind: "navigate", URL: "http://example.com/", HasXY: true},
			true,
		},
		{"unknown kind", LiveInput{Kind: "bogus"}, true},
		// ADR-038 finding #5: per-kind validation added alongside the
		// pre-existing "text" guard — mouse/wheel kinds need real
		// coordinates (HasXY unset must be rejected, not silently dispatched
		// at (0,0)), key kinds need at least a key or a code.
		{"mouse_move without coordinates", LiveInput{Kind: "mouse_move"}, true},
		{"mouse_down without coordinates", LiveInput{Kind: "mouse_down"}, true},
		{"mouse_up without coordinates", LiveInput{Kind: "mouse_up"}, true},
		{"wheel without coordinates", LiveInput{Kind: "wheel"}, true},
		{"key_down without key or code", LiveInput{Kind: "key_down"}, true},
		{"key_up without key or code", LiveInput{Kind: "key_up"}, true},
		{"key_down with only code", LiveInput{Kind: "key_down", Code: "KeyA"}, false},
		{"key_up with only key", LiveInput{Kind: "key_up", Key: "a"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, err := buildInputAction(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, action)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, action)
		})
	}
}

// TestBuildInputAction_ClampsModifiers guards against a malformed
// out-of-range modifiers bitmask reaching cdproto directly (ADR-038 finding
// #5) — schema validation (finding #3) catches this on the wire when
// gateway.validate_inbound=true, but this is the defense-in-depth backstop
// for when it's off, or for any future caller that bypasses the WS frame
// entirely.
func TestBuildInputAction_ClampsModifiers(t *testing.T) {
	require.Equal(t, 0, clampModifiers(-5))
	require.Equal(t, 0, clampModifiers(0))
	require.Equal(t, 15, clampModifiers(15))
	require.Equal(t, 15, clampModifiers(999))

	// End-to-end through buildInputAction: an out-of-range value must not
	// error (it's clamped, not rejected) and must produce a valid action.
	action, err := buildInputAction(LiveInput{Kind: "text", Text: "x", Modifiers: 999})
	require.NoError(t, err)
	require.NotNil(t, action)
}

// TestBuildInputAction_KeyEventCarriesVirtualKeyCode guards the UAT keyboard
// fix: an editing/navigation key (Backspace, Delete, Enter, arrows) and
// modifier shortcuts (Ctrl+A) only PERFORM their action in the page when the
// CDP Input.dispatchKeyEvent carries the Windows/native virtual key code —
// key/code alone deliver an event that does nothing. Assert the built action's
// params thread KeyCode through to both fields for key_down and key_up.
func TestBuildInputAction_KeyEventCarriesVirtualKeyCode(t *testing.T) {
	for _, kind := range []string{"key_down", "key_up"} {
		action, err := buildInputAction(LiveInput{Kind: kind, Key: "Backspace", Code: "Backspace", KeyCode: 8})
		require.NoError(t, err)
		p, ok := action.(*input.DispatchKeyEventParams)
		require.True(t, ok, "%s must build a *input.DispatchKeyEventParams", kind)
		require.Equal(t, int64(8), p.WindowsVirtualKeyCode, "%s WindowsVirtualKeyCode", kind)
		require.Equal(t, int64(8), p.NativeVirtualKeyCode, "%s NativeVirtualKeyCode", kind)
	}
}

// TestBuildInputAction_KeyDown_EnterPerformsDefaultAction — live UAT
// 2026-07-31: typing into a remote search box then pressing Enter did
// nothing, because key_down always dispatched CDP "rawKeyDown" with empty
// text. rawKeyDown delivers the DOM event only; "keyDown" with text is what
// runs text processing and default actions (form submit). Mirrors
// Puppeteer's convention: Enter synthesizes text "\r" and upgrades to
// keyDown; textless keys stay rawKeyDown; key_up is unaffected.
func TestBuildInputAction_KeyDown_EnterPerformsDefaultAction(t *testing.T) {
	// Enter with no client-supplied text: synthesized "\r", type keyDown.
	action, err := buildInputAction(LiveInput{Kind: "key_down", Key: "Enter", Code: "Enter", KeyCode: 13})
	require.NoError(t, err)
	p, ok := action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "Enter key_down must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyDown, p.Type, "Enter must dispatch as keyDown, not rawKeyDown")
	require.Equal(t, "\r", p.Text, "Enter must carry the CR text that triggers default actions")

	// Client-supplied text also upgrades to keyDown, verbatim.
	action, err = buildInputAction(LiveInput{Kind: "key_down", Key: "a", Code: "KeyA", KeyCode: 65, Text: "a"})
	require.NoError(t, err)
	p, ok = action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "key_down with client-supplied text must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyDown, p.Type)
	require.Equal(t, "a", p.Text)

	// A textless non-Enter key stays rawKeyDown (no text processing to run).
	action, err = buildInputAction(LiveInput{Kind: "key_down", Key: "ArrowDown", Code: "ArrowDown", KeyCode: 40})
	require.NoError(t, err)
	p, ok = action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "textless key_down must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyRawDown, p.Type)
	require.Empty(t, p.Text)

	// key_up never synthesizes text, even for Enter.
	action, err = buildInputAction(LiveInput{Kind: "key_up", Key: "Enter", Code: "Enter", KeyCode: 13})
	require.NoError(t, err)
	p, ok = action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "Enter key_up must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyUp, p.Type)
	require.Empty(t, p.Text)
}

func TestMouseButton(t *testing.T) {
	require.Equal(t, input.Left, mouseButton("left"))
	require.Equal(t, input.Middle, mouseButton("middle"))
	require.Equal(t, input.Right, mouseButton("right"))
	require.Equal(t, input.Back, mouseButton("back"))
	require.Equal(t, input.Forward, mouseButton("forward"))
	require.Equal(t, input.None, mouseButton(""))
	require.Equal(t, input.None, mouseButton("not-a-button"))
}
