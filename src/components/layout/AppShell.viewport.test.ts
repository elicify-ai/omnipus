// AppShell.viewport.test.ts — regression coverage for the iPad scroll-jump
// bug: "header row jumps out of the viewable area, and tapping any
// non-editable element makes it jump to the top again."
//
// Root cause (see docs/internal/architecture/ios-scroll-stability.md, part 2,
// and the detailed comment above the visualViewport effect in AppShell.tsx):
// a prior fix (`dec7713b`) gated `--app-top` / `--app-vh` publishing on an
// editable element being focused. The instant focus left the composer for
// any non-editable element, the vars were REMOVED and the fixed shell
// snapped to `top:0` while the visual viewport was still panned by iOS —
// the header jumped off-screen. The fix restores ALWAYS-ON tracking
// (`--app-top` never gated) with a DETERMINISTIC stale-height guard for
// `--app-vh` instead of a focus gate.
//
// `computeAppMetrics` (src/components/layout/appShellViewport.ts) is the
// pure math extracted from AppShell's visualViewport hook specifically so
// this invariant can be locked without mounting the shell or mocking
// `window.visualViewport` / `document.activeElement`.

import { describe, it, expect } from 'vitest'
import { computeAppMetrics } from './appShellViewport'

describe('computeAppMetrics — always-on --app-top (no focus gate)', () => {
  it('sets --app-top from vv.offsetTop even when nothing editable is focused', () => {
    // This is the exact scenario that reproduces the bug: the visual
    // viewport is panned (offsetTop=200, e.g. keyboard was open and iOS
    // hasn't fully settled, or a tap scrolled chrome) but there is no
    // focused editable element. A focus-gated implementation would remove
    // `--app-top` here, snapping the shell to `top:0` while the viewport is
    // still panned 200px down — the header jumps out of view. This
    // assertion FAILS under that old gate and PASSES under always-on
    // tracking.
    const result = computeAppMetrics({ offsetTop: 200, height: 600 }, 800)
    expect(result.appTop).toBe('200px')
  })

  it('sets --app-top to 0px when the viewport is not panned', () => {
    const result = computeAppMetrics({ offsetTop: 0, height: 800 }, 800)
    expect(result.appTop).toBe('0px')
  })
})

describe('computeAppMetrics — deterministic stale-height guard for --app-vh', () => {
  it('removes --app-vh (null) when the keyboard is closed (height ≈ innerHeight)', () => {
    // Keyboard closed: vv.height and innerHeight agree (within the 2px
    // tolerance for iOS rounding/toolbar jitter). This is the guard against
    // the IMG_0616 regression (a stale short height permanently shortening
    // the shell) — done by height math, NOT by whether something is focused.
    const result = computeAppMetrics({ offsetTop: 0, height: 800 }, 800)
    expect(result.appVh).toBeNull()
  })

  it('removes --app-vh within the 2px tolerance band', () => {
    const result = computeAppMetrics({ offsetTop: 0, height: 799 }, 800)
    expect(result.appVh).toBeNull()
  })

  it('sets --app-vh to the visual viewport height when the keyboard is open', () => {
    // Keyboard open: vv.height well below innerHeight.
    const result = computeAppMetrics({ offsetTop: 200, height: 600 }, 800)
    expect(result.appVh).toBe('600px')
  })

  it('sets --app-vh even with no focused editable — height math is independent of focus', () => {
    // Same panned/keyboard-open scenario as the --app-top test above,
    // reaffirming that neither output depends on document.activeElement.
    const result = computeAppMetrics({ offsetTop: 200, height: 600 }, 800)
    expect(result.appTop).toBe('200px')
    expect(result.appVh).toBe('600px')
  })
})
