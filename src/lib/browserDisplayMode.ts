/** Installed/standalone windows have no tab strip. Native Chrome --app also
 * reports standalone; normal Chrome tabs report browser instead. */
export function isStandaloneWindow(): boolean {
  if (typeof window.matchMedia !== 'function') return false
  return ['standalone', 'minimal-ui', 'window-controls-overlay', 'fullscreen'].some(
    (mode) => window.matchMedia(`(display-mode: ${mode})`).matches,
  )
}
