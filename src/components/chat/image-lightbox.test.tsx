// image-lightbox.test.tsx — ImageLightbox on the shared ZoomableView media
// viewer face (D18, docs/internal/design/components/zoomable-view.md).
//
// The defect-1 regression ("An enlarged wide diagram currently collapses to
// about 300px wide at every viewport") is the load-bearing test here: the
// PREVIOUS implementation rendered `dangerouslySetInnerHTML={{ __html:
// sanitizedSvg }}` directly with no explicit width/height at all — DOMPurify
// strips a sanitized SVG's own width/height/style, and a replaced element
// (an <img> or <svg>) with no intrinsic size and no CSS sizing falls back to
// the browser's default box, 300x150, regardless of its viewBox. That old
// wrapper had NO inline `style` attribute whatsoever, so
// `getComputedStyle`/`.style.width` on it would read `''`, never `'3000px'`
// — the assertion below is the one that would have failed against that
// code. Verified by reading the removed code directly (git diff of this
// commit), not by re-running the old build.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ImageLightbox } from './image-lightbox'

beforeEach(() => cleanup())

const WIDE_DIAGRAM_SVG = '<svg viewBox="0 0 3000 200"><text x="10" y="20">wide diagram</text></svg>'

describe('ImageLightbox — defect 1 regression: a wide diagram opens at its real size, not collapsed', () => {
  it('resolves the SVG wrapper size from the viewBox (3000x200), never the 300x150 browser default', () => {
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={vi.fn()} />)
    const wrapper = screen.getByTestId('image-lightbox-svg')
    expect(wrapper.style.width).toBe('3000px')
    expect(wrapper.style.height).toBe('200px')
  })

  it('a tall diagram resolves its size the same way (no special-casing of "wide")', () => {
    render(<ImageLightbox svg={'<svg viewBox="0 0 300 4000"><text>tall</text></svg>'} onClose={vi.fn()} />)
    const wrapper = screen.getByTestId('image-lightbox-svg')
    expect(wrapper.style.width).toBe('300px')
    expect(wrapper.style.height).toBe('4000px')
  })

  it('opens fitted: the frame reports a scale once the SVG size is known (not stuck at the 25% floor)', () => {
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={vi.fn()} />)
    // jsdom has no ResizeObserver (documented in zoomable-view.tsx); the frame
    // still measures once synchronously on mount (clientWidth/Height = 0),
    // which — combined with the SVG's now-known intrinsic size — resolves
    // computeFittedScale's zero-frame guard to a defined 100%, not the
    // pre-resolution 25% floor `useZoomableMedia` seeds before either size is
    // known. The percent pill reflecting a real, computed value (not the
    // floor, not a hardcoded 100%) is the proof the size actually reached
    // the zoom hook.
    expect(screen.getByTestId('zoomable-view-percent')).toHaveTextContent('100%')
    expect(screen.getByTestId('image-lightbox-frame')).toHaveAttribute('data-fit', 'true')
  })
})

describe('ImageLightbox — ZoomableView media-viewer face (D18)', () => {
  it('renders the shared ZoomPill in the toolbar, not a hand-rolled percent indicator', () => {
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={vi.fn()} />)
    expect(screen.getByTestId('zoomable-view-zoom-out')).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-zoom-in')).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-percent')).toBeInTheDocument()
  })

  it('zoom in / zoom out via the pill changes the displayed percent (wired to the real controller)', () => {
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={vi.fn()} />)
    expect(screen.getByTestId('zoomable-view-percent')).toHaveTextContent('100%')

    fireEvent.click(screen.getByTestId('zoomable-view-zoom-in'))
    expect(screen.getByTestId('zoomable-view-percent')).toHaveTextContent('125%')

    fireEvent.click(screen.getByTestId('zoomable-view-zoom-out'))
    fireEvent.click(screen.getByTestId('zoomable-view-zoom-out'))
    expect(screen.getByTestId('image-lightbox-frame')).toHaveAttribute('data-fit', 'false')
  })

  it('the Fit menu action returns to the opening scale', async () => {
    const user = userEvent.setup()
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={vi.fn()} />)
    fireEvent.click(screen.getByTestId('zoomable-view-zoom-in'))
    expect(screen.getByTestId('zoomable-view-percent')).toHaveTextContent('125%')

    // Radix's DropdownMenu opens on a real pointer interaction, not a bare
    // `fireEvent.click` — matches the pattern zoomable-view.stories.tsx uses.
    await user.click(screen.getByTestId('zoomable-view-percent'))
    await user.click(within(document.body).getByRole('menuitem', { name: /Fit/ }))
    expect(screen.getByTestId('zoomable-view-percent')).toHaveTextContent('100%')
  })

  it('Escape closes the viewer', () => {
    const onClose = vi.fn()
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={onClose} />)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('backdrop click closes; a click on the media itself does not', () => {
    const onClose = vi.fn()
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={onClose} />)
    fireEvent.click(screen.getByTestId('image-lightbox-frame'))
    expect(onClose).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('dialog'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('the close button closes the viewer', () => {
    const onClose = vi.fn()
    render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: 'Close image preview' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('restores focus to whatever opened it when the viewer unmounts', () => {
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    opener.focus()
    expect(document.activeElement).toBe(opener)

    const { unmount } = render(<ImageLightbox svg={WIDE_DIAGRAM_SVG} onClose={vi.fn()} />)
    unmount()

    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it('renders an <img> for a plain image (no svg) and reflects its title/alt in the dialog label', () => {
    render(<ImageLightbox src="/u/photo.png" alt="a photo" onClose={vi.fn()} />)
    const img = screen.getByRole('img')
    expect(img).toHaveAttribute('src', '/u/photo.png')
    expect(screen.getByRole('dialog')).toHaveAttribute('aria-label', 'a photo')
  })

  it('renders the passed-in toolbar alongside the zoom pill', () => {
    render(
      <ImageLightbox
        svg={WIDE_DIAGRAM_SVG}
        onClose={vi.fn()}
        toolbar={<button type="button" aria-label="Download diagram as PNG">download</button>}
      />,
    )
    expect(screen.getByRole('button', { name: 'Download diagram as PNG' })).toBeInTheDocument()
    expect(screen.getByTestId('zoomable-view-percent')).toBeInTheDocument()
  })
})
