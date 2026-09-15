package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// These are sender-provided DOM identities, not a server-side US-layout map.
// The real live-input path must preserve them at the Chrome command boundary.
// Shortcut frames deliberately carry no text; filtering shortcut text belongs
// to the sender, whereas key-up must discard even explicitly supplied text.
func TestLiveKeyboardLayoutPreservesCharactersAndShortcutIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, key, code, text string
		keyCode, modifiers    int
		kind                  input.KeyType
	}{
		{"German Mac Option L", "@", "KeyL", "@", 76, 1, input.KeyDown},
		{"Space", " ", "Space", " ", 32, 0, input.KeyDown},
		{"Shift Space", " ", "Space", " ", 32, 8, input.KeyDown},
		{"Control A shortcut", "a", "KeyA", "", 65, 2, input.KeyRawDown},
		{"Meta A shortcut", "a", "KeyA", "", 65, 4, input.KeyRawDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var commands []*input.DispatchKeyEventParams
			lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				for _, action := range actions {
					key, ok := action.(*input.DispatchKeyEventParams)
					require.True(t, ok, "keyboard dispatch unexpectedly emitted %T", action)
					commands = append(commands, key)
				}
				return nil
			})
			picture := installInputTestPicture(t, lv)
			frame := LiveInput{Kind: "key_down", Key: tc.key, Code: tc.code, KeyCode: tc.keyCode, Text: tc.text, Modifiers: tc.modifiers}
			require.NoError(t, lv.dispatchInput("viewer", inputWithTestPicture(picture, frame)))
			frame.Kind = "key_up"
			frame.Text = "must not insert on release"
			require.NoError(t, lv.dispatchInput("viewer", inputWithTestPicture(picture, frame)))
			require.Equal(t, []*input.DispatchKeyEventParams{
				{Type: tc.kind, Key: tc.key, Code: tc.code, Text: tc.text, WindowsVirtualKeyCode: int64(tc.keyCode), NativeVirtualKeyCode: int64(tc.keyCode), Modifiers: input.Modifier(tc.modifiers)},
				{Type: input.KeyUp, Key: tc.key, Code: tc.code, WindowsVirtualKeyCode: int64(tc.keyCode), NativeVirtualKeyCode: int64(tc.keyCode), Modifiers: input.Modifier(tc.modifiers)},
			}, commands)
		})
	}
}

func TestLiveKeyboardLayoutCancellationReleasesOnlyFinalPhysicalKeyOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var commands []*input.DispatchKeyEventParams
		lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			for _, action := range actions {
				key, ok := action.(*input.DispatchKeyEventParams)
				require.True(t, ok)
				commands = append(commands, key)
			}
			return nil
		})
		picture := installInputTestPicture(t, lv)
		first, cancelFirst := context.WithCancel(context.Background())
		defer cancelFirst()
		second, cancelSecond := context.WithCancel(context.Background())
		defer cancelSecond()
		for _, source := range []context.Context{first, second} {
			frame := LiveInput{SourceContext: source, Kind: "key_down", Key: "@", Code: "KeyL", KeyCode: 76, Text: "@", Modifiers: 1}
			require.NoError(t, lv.dispatchInput("viewer", inputWithTestPicture(picture, frame)))
		}
		cancelFirst()
		synctest.Wait()
		require.Len(t, commands, 2, "retiring one source released another source's physical key")
		cancelSecond()
		synctest.Wait()
		require.Equal(t, []*input.DispatchKeyEventParams{
			{Type: input.KeyDown, Key: "@", Code: "KeyL", Text: "@", WindowsVirtualKeyCode: 76, NativeVirtualKeyCode: 76, Modifiers: 1},
			{Type: input.KeyDown, Key: "@", Code: "KeyL", Text: "@", WindowsVirtualKeyCode: 76, NativeVirtualKeyCode: 76, Modifiers: 1},
			{Type: input.KeyUp, Key: "@", Code: "KeyL", WindowsVirtualKeyCode: 76, NativeVirtualKeyCode: 76},
		}, commands, "final release must retain physical identity without reinserting text or retaining Option")
	})
}
