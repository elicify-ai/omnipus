/**
 * MediaLightbox.test.tsx — the single, app-root-mounted enlarged-media overlay
 * driven by the @/store/ui `mediaLightbox` slice.
 *
 * Covers:
 *   - closed (mediaLightbox === null) → renders nothing
 *   - kind 'image' → ImageLightbox gets src/alt; toolbar = Copy/Share/Download (gated)
 *   - kind 'svg'   → ImageLightbox gets the svg; toolbar = Copy/Download as PNG
 *   - closing clears the store slice
 *
 * ImageLightbox + MediaActionToolbar are passthroughs (no portal / no styling), and
 * media-actions is mocked for feature detection. The store is the REAL one.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { ReactNode } from 'react'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'

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
  svgToPngBlob: vi.fn(),
}))

vi.mock('./image-lightbox', () => ({
  ImageLightbox: ({
    src,
    svg,
    alt,
    title,
    onClose,
    toolbar,
  }: {
    src?: string
    svg?: string
    alt?: string
    title?: string
    onClose: () => void
    toolbar?: ReactNode
  }) => (
    <div data-testid="lightbox" data-src={src ?? ''} data-svg={svg ?? ''} data-alt={alt ?? ''} data-title={title ?? ''}>
      <button type="button" aria-label="Close" onClick={onClose}>
        close
      </button>
      {toolbar}
    </div>
  ),
}))

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

import { MediaLightbox } from './MediaLightbox'
import { useUiStore } from '@/store/ui'

beforeEach(() => {
  cleanup()
  canCopyImageMock.mockReset().mockReturnValue(true)
  canShareFilesMock.mockReset().mockReturnValue(true)
  useUiStore.getState().closeMediaLightbox()
})

describe('MediaLightbox', () => {
  it('renders nothing when closed', () => {
    render(<MediaLightbox />)
    expect(screen.queryByTestId('lightbox')).toBeNull()
  })

  it('renders an image lightbox with a Copy/Share/Download toolbar', () => {
    render(<MediaLightbox />)
    act(() => useUiStore.getState().openMediaLightbox({ kind: 'image', src: '/u/x', alt: 'shot', filename: 'x.png' }))

    const lb = screen.getByTestId('lightbox')
    expect(lb.getAttribute('data-src')).toBe('/u/x')
    expect(lb.getAttribute('data-alt')).toBe('shot')
    expect(lb.getAttribute('data-svg')).toBe('')
    expect(screen.getByRole('button', { name: 'Copy image' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Share' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download' })).toBeInTheDocument()
  })

  it('hides Copy/Share in the toolbar when unsupported', () => {
    canCopyImageMock.mockReturnValue(false)
    canShareFilesMock.mockReturnValue(false)
    render(<MediaLightbox />)
    act(() => useUiStore.getState().openMediaLightbox({ kind: 'image', src: '/u/x' }))

    expect(screen.queryByRole('button', { name: 'Copy image' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Share' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Download' })).toBeInTheDocument()
  })

  it('renders an svg lightbox with Copy/Download as PNG (no Share)', () => {
    render(<MediaLightbox />)
    act(() => useUiStore.getState().openMediaLightbox({ kind: 'svg', svg: '<svg id="d"/>', title: 'Diagram' }))

    const lb = screen.getByTestId('lightbox')
    expect(lb.getAttribute('data-svg')).toBe('<svg id="d"/>')
    expect(lb.getAttribute('data-title')).toBe('Diagram')
    expect(lb.getAttribute('data-src')).toBe('')
    expect(screen.getByRole('button', { name: 'Copy diagram as PNG' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Download diagram as PNG' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Share' })).toBeNull()
  })

  it('closing the lightbox clears the store slice', () => {
    render(<MediaLightbox />)
    act(() => useUiStore.getState().openMediaLightbox({ kind: 'image', src: '/u/x' }))
    expect(screen.getByTestId('lightbox')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    expect(useUiStore.getState().mediaLightbox).toBeNull()
    expect(screen.queryByTestId('lightbox')).toBeNull()
  })
})
