import { useLayoutEffect, useRef, useState } from 'react'

export type WorkspaceHeaderMode = 'full' | 'icons' | 'narrow'

/** Compare natural, off-layout strip widths with the space left after the
 * sidebar launcher and ChatControls. No viewport/rem breakpoint is involved. */
function widestFittingMode(available: number, full: number, icons: number): WorkspaceHeaderMode {
  if (full <= available) return 'full'
  if (icons <= available) return 'icons'
  return 'narrow'
}

export function useWorkspaceHeaderMode() {
  const availableRef = useRef<HTMLDivElement>(null)
  const fullRef = useRef<HTMLDivElement>(null)
  const iconsRef = useRef<HTMLDivElement>(null)
  const [mode, setMode] = useState<WorkspaceHeaderMode>('full')

  useLayoutEffect(() => {
    const available = availableRef.current
    const full = fullRef.current
    const icons = iconsRef.current
    if (!available || !full || !icons) return

    const measure = () => {
      const width = available.getBoundingClientRect().width
      const fullWidth = full.getBoundingClientRect().width
      const iconsWidth = icons.getBoundingClientRect().width
      // A hidden/unmounted row has no available size yet. Keep the previous
      // mode until all three real measurements exist, rather than guessing.
      if (width <= 0 || fullWidth <= 0 || iconsWidth <= 0) return
      setMode(widestFittingMode(width, fullWidth, iconsWidth))
    }
    // Set the initial mode before paint. Natural probes never change with the
    // selected mode, and the available flex slot never sizes to its content:
    // shrinking the strip cannot immediately make the full mode fit again.
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    observer?.observe(available)
    observer?.observe(full)
    observer?.observe(icons)
    // Observing the probes also catches font/root-size and workspace-name
    // changes, including font loading when the row itself does not resize.
    window.addEventListener('resize', measure)
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [])

  return { mode, availableRef, fullRef, iconsRef }
}
