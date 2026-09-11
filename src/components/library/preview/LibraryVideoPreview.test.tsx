// LibraryVideoPreview.test.tsx — the `pane`/`inline` variant contract added
// for ADR-083 embedded-content spec Step 6 (EMB-027/028/105). No test file
// existed for this component before this change; the pane's own behaviour
// (a plain `<video controls src=...>`) is covered here alongside the new
// inline variant, following the same shape LibraryImagePreview.test.tsx and
// LibraryAudioPreview.test.tsx already use for their own variant contracts.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { LibraryVideoPreview } from './LibraryVideoPreview'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

const ENTRY: LibraryEntry = {
  name: 'clip.mp4',
  path: 'video/clip.mp4',
  is_dir: false,
  is_hidden: false,
  size: 1_048_576,
  modified_at: '2026-08-22T10:15:00Z',
  is_text_editable: false,
}

describe('LibraryVideoPreview — pane variant (default, unchanged)', () => {
  it('renders the download URL as the video source', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} />)
    const video = screen.getByTestId('library-video-preview').querySelector('video')
    expect(video).toHaveAttribute('controls')
    expect(video).toHaveAttribute('src', libraryDownloadUrl('ws-1', ENTRY.path))
  })

  it('carries data-variant="pane" when no variant prop is given', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} />)
    expect(screen.getByTestId('library-video-preview')).toHaveAttribute('data-variant', 'pane')
  })
})

describe('LibraryVideoPreview — inline variant (EMB-027: same renderer, EMB-028: layout only)', () => {
  it('renders the identical src as the pane variant', () => {
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    expect(screen.getByTestId('library-video-preview').querySelector('video')).toHaveAttribute(
      'src',
      libraryDownloadUrl('ws-1', ENTRY.path),
    )
  })

  it('draws its outermost container with DIFFERENT layout classes than the pane variant', () => {
    const pane = render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="pane" />)
    const paneClass = pane.getByTestId('library-video-preview').className
    pane.unmount()

    const inline = render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" />)
    const inlineClass = inline.getByTestId('library-video-preview').className
    inline.unmount()

    // MUTATION THIS DIES ON: a component that computes the same className
    // regardless of `variant`.
    expect(inlineClass).not.toBe(paneClass)
    expect(inlineClass).not.toContain('flex-1')
  })
})

describe('LibraryVideoPreview — no width modifier surface (EMB-030)', () => {
  it('accepts no width prop at all — a |400 segment has nothing to apply to', () => {
    // @ts-expect-error — width is not a prop of this component, by design (EMB-030).
    render(<LibraryVideoPreview workspaceId="ws-1" entry={ENTRY} variant="inline" width={400} />)
    expect(screen.queryByText('400')).not.toBeInTheDocument()
  })
})
