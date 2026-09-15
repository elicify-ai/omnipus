// Only the app document that opened a popout owns its restoration. An actual
// WindowProxy survives reload; pagehide and origin-wide broadcasts cannot
// distinguish reload from close or identify the correct originating dock.
const CLOSE_POLL_MS = 250

export function watchPopoutClosed(popup: Pick<Window, 'closed'>, onClosed: () => void): () => void {
  const timer = setInterval(() => {
    if (!popup.closed) return
    clearInterval(timer)
    onClosed()
  }, CLOSE_POLL_MS)
  return () => clearInterval(timer)
}
