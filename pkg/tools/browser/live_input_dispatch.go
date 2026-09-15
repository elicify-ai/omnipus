package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func (lv *LiveView) dispatchInputCommand(ctx, targetCtx context.Context, viewerID string, in LiveInput) error {
	// Root-cause doc Fault 3: x/y arrive in the CLIENT's capture-frame pixel
	// space, which is no longer guaranteed to equal the tab's CSS pixel
	// space now that SetViewport (Fault 1 fix, above) can resize the tab
	// independently of what the encoder's downscaling happens to produce.
	// Only pointer-position kinds carry a meaningful position to rescale —
	// wheel's DeltaX/DeltaY are scroll deltas, not positions, and key/text
	// carry no coordinates at all.
	switch in.Kind {
	case "mouse_move", "mouse_down", "mouse_up", "wheel":
		if in.HasXY && in.CaptureWidth > 0 && in.CaptureHeight > 0 {
			in.observeTiming("mapping_start")
			rx, ry, ok := lv.rescaleToCSSViewport(ctx, in.X, in.Y, in.CaptureWidth, in.CaptureHeight)
			in.observeTiming("mapping_done")
			if !ok {
				if err := ctx.Err(); err != nil {
					return realInputError("browser live: input canceled: %w", err)
				}
				// DROP rather than dispatch at an unmapped coordinate — see
				// rescaleToCSSViewport's doc comment: unscaled coordinates
				// land ~34% off (measured), i.e. on the wrong element, and a
				// mis-aimed click can navigate away, delete or submit.
				//
				// Classification matters as much as the drop. A one-off miss
				// is a transient the user retries past, so it stays benign and
				// silent. But a SUSTAINED streak means the CDP transport is
				// wedged or the tab is dead — and LiveInputErrorReal's own doc
				// comment names exactly that as the thing that must reach the
				// user ("a dead browser looked identical to a healthy, idle
				// one", ADR-038 finding #4). Without this escalation a crashed
				// tab would swallow every click forever with no error, since
				// pointer kinds bail out here and never reach the real-error
				// CDP dispatch below.
				lv.mu.Lock()
				failures := lv.viewportFetchFailures
				lv.mu.Unlock()
				if failures >= viewportFetchFailureEscalation {
					return realInputError(
						"browser live: cannot read the tab's CSS viewport after %d consecutive attempts — the browser tab may have crashed or the CDP transport is wedged",
						failures,
					)
				}
				return benignInputError(
					"browser live: viewport unknown, dropped %s to avoid a mis-aimed dispatch",
					in.Kind,
				)
			}
			in.X, in.Y = rx, ry
		}
	}

	return lv.dispatchTrackedInput(ctx, targetCtx, viewerID, in)
}

// --- moved from live.go 2026-09-15 ---

// clampModifiers clamps the CDP modifier bitmask to the valid [0,15] range
// (Alt=1, Ctrl=2, Meta=4, Shift=8; ADR-038 finding #5) — defense in depth
// alongside the wire schema's minimum/maximum constraint
// (BrowserInputFrame.yaml), since schema validation only runs when
// gateway.validate_inbound=true (finding #3). Without this, an
// out-of-range value would be passed straight to cdproto's input.Modifier,
// which has no validation of its own.
func clampModifiers(m int) int {
	if m < 0 {
		return 0
	}
	if m > 15 {
		return 15
	}
	return m
}

// buildInputAction maps a wire-level LiveInput to the corresponding CDP
// Input.dispatch* action (spike-proven mechanics, ADR-038 context section).
//
// ADR-038 finding #5: every kind is validated before building its action —
// previously only "text" was guarded, so a malformed mouse/wheel frame with
// no coordinates silently dispatched a (0,0)-origin event (a click in the
// page's top-left corner) instead of being rejected, and a malformed key
// event with neither key nor code silently dispatched a no-op keystroke.
func buildInputAction(in LiveInput) (chromedp.Action, error) {
	mods := input.Modifier(clampModifiers(in.Modifiers))

	switch in.Kind {
	case "mouse_move":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: mouse_move input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MouseMoved, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithModifiers(mods), nil
	case "mouse_down":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: mouse_down input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MousePressed, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithClickCount(1).
			WithModifiers(mods), nil
	case "mouse_up":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: mouse_up input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MouseReleased, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithClickCount(1).
			WithModifiers(mods), nil
	case "wheel":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: wheel input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MouseWheel, in.X, in.Y).
			WithDeltaX(in.DeltaX).
			WithDeltaY(in.DeltaY).
			WithModifiers(mods), nil
	case "key_down":
		if in.Key == "" && in.Code == "" {
			return nil, fmt.Errorf("browser live: key_down input requires a key or code")
		}
		// CDP dispatches two keydown variants and the split is what decides
		// whether the browser PERFORMS the key: "keyDown" runs text processing
		// and default actions (Enter submits the focused form, inserts a
		// newline in a textarea); "rawKeyDown" delivers the DOM event only.
		// Live UAT 2026-07-31: typing into a remote page's search box then
		// pressing Enter did nothing, because every key_down went out as
		// rawKeyDown with empty text — the virtual key code alone never
		// triggers form submission. Mirror Puppeteer's convention exactly:
		// synthesize text "\r" for Enter when the client sent none, and use
		// keyDown whenever text is present, rawKeyDown otherwise.
		text := in.Text
		if text == "" && in.Key == "Enter" {
			text = "\r"
		}
		keyType := input.KeyRawDown
		if text != "" {
			keyType = input.KeyDown
		}
		return input.DispatchKeyEvent(keyType).
			WithKey(in.Key).
			WithCode(in.Code).
			WithText(text).
			WithWindowsVirtualKeyCode(int64(in.KeyCode)).
			WithNativeVirtualKeyCode(int64(in.KeyCode)).
			WithModifiers(mods), nil
	case "key_up":
		if in.Key == "" && in.Code == "" {
			return nil, fmt.Errorf("browser live: key_up input requires a key or code")
		}
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey(in.Key).
			WithCode(in.Code).
			WithWindowsVirtualKeyCode(int64(in.KeyCode)).
			WithNativeVirtualKeyCode(int64(in.KeyCode)).
			WithModifiers(mods), nil
	case "text":
		if in.Text == "" {
			return nil, fmt.Errorf("browser live: text input requires a non-empty text field")
		}
		return input.InsertText(in.Text), nil
	case "navigate":
		// Defense-in-depth (7-reviewer LOW finding, see LiveInput.URL's doc
		// comment): a "navigate" input carrying HasXY would be a malformed/
		// confused frame — reject it rather than silently ignoring X/Y, so a
		// future refactor that starts reading X/Y for this kind fails loudly
		// instead of quietly bypassing the SSRF gate for what looked like a
		// mouse event.
		if in.HasXY {
			return nil, fmt.Errorf("browser live: navigate input must not carry x/y coordinates")
		}
		if in.URL == "" {
			return nil, fmt.Errorf("browser live: navigate input requires a non-empty url field")
		}
		return navigationInputAction{url: in.URL}, nil
	case "navigate_back":
		// History back — no URL (goes to a previously-navigated page, already
		// SSRF-cleared on its original navigate). Discrete, like navigate.
		if in.HasXY || in.URL != "" {
			return nil, fmt.Errorf("browser live: navigate_back input must not carry x/y or url")
		}
		return historyBackInputAction{}, nil
	case "stop_loading":
		if in.HasXY || in.URL != "" {
			return nil, fmt.Errorf("browser live: stop_loading input must not carry x/y or url")
		}
		return page.StopLoading(), nil
	case "reload":
		// Reload the current URL (already SSRF-cleared). Discrete, like navigate.
		if in.HasXY || in.URL != "" {
			return nil, fmt.Errorf("browser live: reload input must not carry x/y or url")
		}
		return page.Reload(), nil
	default:
		return nil, fmt.Errorf("browser live: unknown input kind %q", in.Kind)
	}
}

// mouseButton maps the wire button string to its cdproto MouseButton value.
// Unknown/empty maps to None, matching CDP's own default.
func mouseButton(b string) input.MouseButton {
	switch b {
	case "left":
		return input.Left
	case "middle":
		return input.Middle
	case "right":
		return input.Right
	case "back":
		return input.Back
	case "forward":
		return input.Forward
	default:
		return input.None
	}
}
