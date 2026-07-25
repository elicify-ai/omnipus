/**
 * ChatImage.test.tsx — the shared chat <img> with a hover action toolbar and a
 * click-to-enlarge handoff to the global MediaLightbox.
 *
 * Covers:
 *   - renders an <img> for a same-origin RELATIVE upload URL (the common case)
 *   - rejects an unsafe src (javascript:) → placeholder, no <img>
 *   - feature-gates the toolbar: Copy hides without clipboard-image support, Share
 *     hides without the Web Share API; Download + Enlarge are always present
 *   - Enlarge AND clicking the image open the single global lightbox via the store
 *     (with the right {kind,src,alt,filename}) — NOT a per-row lightbox
 *
 * media-actions is mocked (feature detection + no real clipboard/canvas), and the
 * MediaActionToolbar is a passthrough that renders one button per action so the
 * gated action list is directly assertable. The @/store/ui store is the REAL one.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

const canCopyImageMock = vi.fn().mockReturnValue(true)
const canShareFilesMock = vi.fn().mockReturnValue(true)
vi.mock('./media-actions', () => ({
  canCopyImage: () => canCopyImageMock(),
  canShareFiles: () => canShareFilesMock(),
  copyImageBlob: vi.fn(),
  shareBlob: vi.fn(),
  downloadBlob: vi.fn(),
  fetchImageBlob: vi.fn(),
  fetchImagePng: vi.fn(),
}))

// Passthrough toolbar: one button per action so feature-gating + onClick are testable.
vi.mock('./MediaActionToolbar', () => ({
  MediaActionToolbar: ({ actions }: { actions: Array<{ label: string; onClick: () => void }> }) => (
    <div role="toolbar">
      {actions.map((a) => (
        <button key={a.label} type="button" aria-label={a.label} onClick={a.onClick}>
          {a.label}
        </button>
      ))}
    </div>
  ),
}))

import { ChatImage } from './ChatImage'
import { useUiStore } from '@/store/ui'

beforeEach(() => {
  cleanup()
  canCopyImageMock.mockReset().mockReturnValue(true)
  canShareFilesMock.mockReset().mockReturnValue(true)
  useUiStore.getState().closeMediaLightbox()
  useUiStore.setState({ toasts: [] })
})

describe('ChatImage', () => {
  it('renders an <img> for a same-origin relative upload URL', () => {
    const { container } = render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" filename="x.png" />)
    const img = container.querySelector('img')
    expect(img).toBeTruthy()
    expect(img?.getAttribute('src')).toBe('/api/v1/uploads/s/x')
  })

  it('rejects an unsafe javascript: src — placeholder, no <img>', () => {
    const { container } = render(<ChatImage src="javascript:alert(1)" alt="evil" />)
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('[image: evil]')).toBeInTheDocument()
  })

  it('feature-gates Copy and Share; always offers Download and Enlarge', () => {
    canCopyImageMock.mockReturnValue(false)
    canShareFilesMock.mockReturnValue(false)
    render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" />)

    expect(screen.queryByRole('button', { name: 'Copy image' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Share' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Download' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Enlarge' })).toBeInTheDocument()
  })

  it('shows Copy and Share when the browser supports them', () => {
    render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" />)
    expect(screen.getByRole('button', { name: 'Copy image' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Share' })).toBeInTheDocument()
  })

  it('Enlarge opens the global media lightbox with the image payload', () => {
    render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" filename="x.png" />)
    expect(useUiStore.getState().mediaLightbox).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Enlarge' }))

    expect(useUiStore.getState().mediaLightbox).toEqual({
      kind: 'image',
      src: '/api/v1/uploads/s/x',
      alt: 'shot',
      filename: 'x.png',
    })
  })

  it('clicking the image also opens the lightbox', () => {
    const { container } = render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" />)
    fireEvent.click(container.querySelector('img')!)
    expect(useUiStore.getState().mediaLightbox).toMatchObject({ kind: 'image', src: '/api/v1/uploads/s/x' })
  })

  it('pressing Space on the focused image also opens the lightbox', () => {
    const { container } = render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" filename="x.png" />)
    expect(useUiStore.getState().mediaLightbox).toBeNull()

    fireEvent.keyDown(container.querySelector('img')!, { key: ' ' })

    expect(useUiStore.getState().mediaLightbox).toEqual({
      kind: 'image',
      src: '/api/v1/uploads/s/x',
      alt: 'shot',
      filename: 'x.png',
    })
  })

  it('pressing Enter on the focused image also opens the lightbox', () => {
    const { container } = render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" filename="x.png" />)
    expect(useUiStore.getState().mediaLightbox).toBeNull()

    fireEvent.keyDown(container.querySelector('img')!, { key: 'Enter' })

    expect(useUiStore.getState().mediaLightbox).toEqual({
      kind: 'image',
      src: '/api/v1/uploads/s/x',
      alt: 'shot',
      filename: 'x.png',
    })
  })

  it('falls back to a visible placeholder card when the <img> fails to load (no silent broken glyph)', () => {
    const { container } = render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="shot" filename="diagram.png" />)
    expect(container.querySelector('img')).toBeTruthy()

    fireEvent.error(container.querySelector('img')!)

    // The broken <img> is gone — replaced by a labeled placeholder, not a
    // blank/broken glyph.
    expect(container.querySelector('img')).toBeNull()
    expect(screen.getByText('diagram.png')).toBeInTheDocument()
    expect(screen.getByText('Image unavailable')).toBeInTheDocument()
  })

  it('falls back using alt text when no filename is provided', () => {
    render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="a shot" />)
    fireEvent.error(screen.getByRole('button', { name: 'View: a shot' }))
    expect(screen.getByText('a shot')).toBeInTheDocument()
  })

  it('keeps the recovery actions available on error — Copy/Share/Download/Enlarge are independent fetches, not a replay of the failed <img>, plus an explicit Retry', () => {
    const { container } = render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="shot" filename="diagram.png" />)
    fireEvent.error(container.querySelector('img')!)

    const toolbar = screen.getByRole('toolbar')
    expect(toolbar).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Copy image' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Share' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Enlarge' })).toBeInTheDocument()
  })

  it('surfaces the load failure via a toast (matches the sibling upload-failure convention)', () => {
    const { container } = render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="shot" filename="diagram.png" />)
    expect(useUiStore.getState().toasts).toHaveLength(0)

    fireEvent.error(container.querySelector('img')!)

    expect(useUiStore.getState().toasts).toEqual([
      expect.objectContaining({ variant: 'error', message: expect.stringContaining('diagram.png') }),
    ])
  })

  it('Enlarge still works from the error card — opens the lightbox, an independent load attempt', () => {
    const { container } = render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="shot" filename="diagram.png" />)
    fireEvent.error(container.querySelector('img')!)
    expect(useUiStore.getState().mediaLightbox).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Enlarge' }))

    expect(useUiStore.getState().mediaLightbox).toEqual({
      kind: 'image',
      src: '/api/v1/media/workspace/ws-1/m-1',
      alt: 'shot',
      filename: 'diagram.png',
    })
  })

  it('Retry clears the error and re-mounts the <img> for a fresh load attempt', () => {
    const { container } = render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="shot" filename="diagram.png" />)
    fireEvent.error(container.querySelector('img')!)
    expect(screen.getByText('Image unavailable')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(screen.queryByText('Image unavailable')).toBeNull()
    expect(container.querySelector('img')).toBeTruthy()
    expect(container.querySelector('img')?.getAttribute('src')).toBe('/api/v1/media/workspace/ws-1/m-1')
  })

  it('a NEW src after a prior error is not stuck showing the stale error card (unkeyed re-render, e.g. via markdown-shared.tsx)', () => {
    const { container, rerender } = render(<ChatImage src="/api/v1/media/workspace/ws-1/m-1" alt="shot" filename="diagram.png" />)
    fireEvent.error(container.querySelector('img')!)
    expect(screen.getByText('Image unavailable')).toBeInTheDocument()

    // Same component instance (no key change), new src — mirrors an unkeyed
    // markdown re-render swapping which image is at this tree position.
    rerender(<ChatImage src="/api/v1/media/workspace/ws-1/m-2" alt="other shot" filename="other.png" />)

    expect(screen.queryByText('Image unavailable')).toBeNull()
    expect(container.querySelector('img')).toBeTruthy()
    expect(container.querySelector('img')?.getAttribute('src')).toBe('/api/v1/media/workspace/ws-1/m-2')
  })

  it('pressing Space calls preventDefault (blocks the page-scroll default), unlike Enter which has no default to block', () => {
    const { container } = render(<ChatImage src="/api/v1/uploads/s/x" alt="shot" filename="x.png" />)
    const img = container.querySelector('img')!

    // fireEvent's return value is the DOM-standard `dispatchEvent` result:
    // false when the event was canceled (preventDefault called) on a
    // cancelable event. This proves ChatImage.tsx's Space branch actually
    // calls e.preventDefault() (see ChatImage.tsx:91-99), not just that
    // enlarge() ran.
    const spaceResult = fireEvent.keyDown(img, { key: ' ' })
    expect(spaceResult).toBe(false)

    // Enter's branch has no e.preventDefault() call — nothing to cancel.
    const enterResult = fireEvent.keyDown(img, { key: 'Enter' })
    expect(enterResult).toBe(true)
  })
})
