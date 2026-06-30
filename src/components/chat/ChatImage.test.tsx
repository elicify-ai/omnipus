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
})
