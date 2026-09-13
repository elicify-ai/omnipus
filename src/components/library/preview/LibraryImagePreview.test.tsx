// LibraryImagePreview.test.tsx — the `pane`/`inline` variant contract
// (ADR-083 embedded-content spec, EMB-027/028/030 — "each embeddable kind
// mounts its own renderer", "a size given after a bar applies to pictures
// only", "a renderer's layout variant changes layout ONLY").
//
// The false-green register for this exact family of test (`*.variant.test.tsx`)
// names the trap directly: asserting the two variants render identical state
// identifiers and text is trivially true of a component that ignores
// `variant` altogether. The positive control below — the outermost
// container's class list actually DIFFERS between the two — is what proves
// the prop was read at all; without it this file would pass unchanged if
// every reference to `variant` were deleted from the component.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LibraryImagePreview } from './LibraryImagePreview'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

const ENTRY: LibraryEntry = {
  name: 'diagram.png',
  path: 'assets/diagram.png',
  is_dir: false,
  is_hidden: false,
  size: 2048,
  modified_at: '2026-08-22T10:15:00Z',
  is_text_editable: false,
}

describe('LibraryImagePreview — pane variant (default, unchanged)', () => {
  it('renders the download URL as the image source and the file name as alt text', () => {
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} />)
    const img = screen.getByRole('img')
    expect(img).toHaveAttribute('src', libraryDownloadUrl('ws-1', ENTRY.path))
    expect(img).toHaveAttribute('alt', ENTRY.name)
  })

  it('carries data-variant="pane" when no variant prop is given, matching the explicit default', () => {
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} />)
    expect(screen.getByTestId('library-image-preview')).toHaveAttribute('data-variant', 'pane')
  })
})

describe('LibraryImagePreview — inline variant (EMB-027: same renderer, EMB-028: layout only)', () => {
  it('renders the identical src and alt as the pane variant', () => {
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    const img = screen.getByRole('img')
    expect(img).toHaveAttribute('src', libraryDownloadUrl('ws-1', ENTRY.path))
    expect(img).toHaveAttribute('alt', ENTRY.name)
  })

  // The positive control (false-green register, step 1): without this, a
  // component that ignores `variant` entirely would still pass every other
  // assertion in this file.
  it('draws its outermost container with DIFFERENT layout classes than the pane variant — proving variant was read', () => {
    const pane = render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} variant="pane" />)
    const paneClass = pane.getByTestId('library-image-preview').className
    pane.unmount()

    const inline = render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    const inlineClass = inline.getByTestId('library-image-preview').className
    inline.unmount()

    // MUTATION THIS DIES ON: a component that computes the same className
    // string regardless of `variant`.
    expect(inlineClass).not.toBe(paneClass)
    // Pane-shaped classes (EMB-027/028's whole point: the pane needs an
    // ancestor to size against; an inline embed in a note's text flow does
    // not have one) must not leak into the inline variant.
    expect(inlineClass).not.toContain('flex-1')
    expect(inlineClass).not.toContain('min-h-0')
  })
})

describe('LibraryImagePreview — width modifier (EMB-030: a size after a bar applies to pictures only)', () => {
  it('applies a given width only on the inline variant', () => {
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} variant="inline" width={400} />)
    expect(screen.getByRole('img')).toHaveAttribute('width', '400')
  })

  it('ignores a width on the pane variant, where the pane itself decides size', () => {
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} variant="pane" width={400} />)
    expect(screen.getByRole('img')).not.toHaveAttribute('width')
  })

  it('never renders the width as a caption or as alternative text', () => {
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} variant="inline" width={400} />)
    const img = screen.getByRole('img')
    expect(img).toHaveAttribute('alt', ENTRY.name)
    expect(img.getAttribute('alt')).not.toContain('400')
    expect(screen.queryByText('400')).not.toBeInTheDocument()
  })
})

// UAT D-107 (2026-09-13): a deleted file used to preview as the browser's own
// broken-image glyph — no message, no placeholder.
describe('LibraryImagePreview — D-107 a picture that fails to load says so', () => {
  it('replaces the broken image with a notice naming the file and offers Try again', async () => {
    const { fireEvent, waitFor } = await import('@testing-library/react')
    render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} />)
    fireEvent.error(screen.getByRole('img'))
    const notice = await screen.findByTestId('library-image-unavailable')
    expect(notice).toHaveTextContent('diagram.png')
    expect(notice).toHaveTextContent(/deleted or moved/)
    expect(screen.queryByRole('img')).not.toBeInTheDocument()

    // Try again re-requests with a fresh URL, not the cached failure.
    fireEvent.click(screen.getByTestId('library-image-retry'))
    await waitFor(() => expect(screen.getByRole('img')).toBeInTheDocument())
    expect(screen.getByRole('img').getAttribute('src')).toMatch(/retry=1$/)
  })

  it('a new entry resets the failure state', async () => {
    const { fireEvent } = await import('@testing-library/react')
    const view = render(<LibraryImagePreview workspaceId="ws-1" entry={ENTRY} />)
    fireEvent.error(screen.getByRole('img'))
    await screen.findByTestId('library-image-unavailable')
    view.rerender(<LibraryImagePreview workspaceId="ws-1" entry={{ ...ENTRY, path: 'assets/other.png', name: 'other.png' }} />)
    expect(screen.getByRole('img')).toHaveAttribute('alt', 'other.png')
  })
})
