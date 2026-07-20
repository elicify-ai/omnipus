// Pure visualViewport-metrics math for AppShell's iPad/iOS scroll-stability
// hook. Extracted so the logic can be unit-tested directly without mounting
// AppShell or mocking `window.visualViewport` — see AppShell.viewport.test.ts
// and docs/internal/architecture/ios-scroll-stability.md (part 2).

export interface ViewportLike {
  /** Distance from the layout viewport's top to the visual viewport's top. */
  offsetTop: number
  /** Visual viewport height (shrinks when the on-screen keyboard is up). */
  height: number
}

export interface AppMetrics {
  /** CSS length for `--app-top`, e.g. "200px". ALWAYS set — see below. */
  appTop: string
  /**
   * CSS length for `--app-vh`, or `null` when the keyboard is deterministically
   * closed — in which case the var must be REMOVED so the CSS `100dvh`
   * fallback rules (the caller is responsible for the removal side-effect;
   * this function only reports the value).
   */
  appVh: string | null
}

/**
 * Compute the `--app-top` / `--app-vh` shell-metrics from the current
 * `visualViewport` state. ALWAYS-ON: unlike the retired focus-gated
 * mechanism (see the doc), this has no dependency on `document.activeElement`
 * — it is pure w.r.t. its two inputs.
 *
 * - `appTop` always mirrors `vv.offsetTop`. This is the fix for the "tap any
 *   non-editable element and the header jumps off-screen" regression: iOS
 *   pans the visual viewport (offsetTop > 0) independent of what has focus,
 *   so the shell must track it unconditionally to stay aligned with what's
 *   actually visible.
 * - `appVh` is deterministically removed (`null`) whenever the keyboard is
 *   closed, defined as `|vv.height - innerHeight| < 2px`. This is the guard
 *   against the "stale short height latches after keyboard close" regression
 *   (IMG_0616) that always-on tracking caused before — WITHOUT reintroducing
 *   a focus gate, which itself caused the header-jump regression this fixes.
 */
export function computeAppMetrics(vv: ViewportLike, innerHeight: number): AppMetrics {
  const appTop = `${Math.round(vv.offsetTop)}px`
  const keyboardClosed = Math.abs(vv.height - innerHeight) < 2
  const appVh = keyboardClosed ? null : `${Math.round(vv.height)}px`
  return { appTop, appVh }
}
