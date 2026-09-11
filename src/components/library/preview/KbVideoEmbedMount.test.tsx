// KbVideoEmbedMount.test.tsx — ADR-083 embedded-content spec, Step 6
// (EMB-105, US-12 AS-2): "Given a note embedding a video file, When it is
// read, Then it plays in place using the same component the full-screen
// preview uses."
//
// Same pairing discipline as KbAudioEmbedMount.test.tsx: every negative
// assertion sits beside a positive one proving the real player mounted, so a
// deleted dispatch fails the pair rather than passing trivially. See this
// task's own report for the mutation-test proof on this exact file.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KbVideoEmbedMount, VIDEO_EMBED_RESERVED_HEIGHT_PX } from './KbVideoEmbedMount'
import { holdEmbedsOutOfView, scrollIntoView, scrollOutOfView } from '@/test/intersectionObserver'
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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryEntries: vi.fn(),
  }
})

import { fetchLibraryEntries } from '@/lib/api'

function renderMount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <KbVideoEmbedMount workspaceId="ws-1" workspacePath="video/clip.mp4" />
    </QueryClientProvider>,
  )
}

describe('KbVideoEmbedMount', () => {
  it('mounts the real video player once the directory listing resolves the entry', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    renderMount()

    const video = await screen.findByTestId('library-video-preview')
    expect(video.querySelector('video')).toHaveAttribute(
      'src',
      '/api/v1/library/ws-1/download?path=video%2Fclip.mp4',
    )
    expect(screen.queryByTestId('kb-embed-mount-loading')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
  })

  it('shows the loading placeholder before the directory listing resolves', () => {
    vi.mocked(fetchLibraryEntries).mockReturnValue(new Promise(() => {}))
    renderMount()

    expect(screen.getByTestId('kb-embed-mount-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
  })

  it('shows a visible, named error and a working retry when the listing request FAILS', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('listing failed'))
    renderMount()

    const error = await screen.findByTestId('kb-embed-mount-error')
    expect(error).toHaveTextContent(/could not read this file/i)
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()

    vi.mocked(fetchLibraryEntries).mockResolvedValueOnce([ENTRY])
    error.querySelector('button')?.click()
    await screen.findByTestId('library-video-preview')
  })

  // Silent-failure audit M1: a listing that SUCCEEDED without this file is a
  // rename/delete, not a failed request — its own words, and no Retry that
  // cannot work.
  it('shows the distinct "missing" state, with no Retry, when the listing succeeded without the file', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([{ ...ENTRY, name: 'other.mp4', path: 'video/other.mp4' }])
    renderMount()

    const missing = await screen.findByTestId('kb-embed-mount-missing')
    expect(missing.querySelector('button')).toBeNull()
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
  })

  it('shows the SAME visible error when the directory listing request itself fails', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('network down'))
    renderMount()

    await waitFor(() => expect(screen.getByTestId('kb-embed-mount-error')).toBeInTheDocument())
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
  })
})

// ── The lazy-mount budget, actually exercised (EMB-065/066) ─────────────────
//
// See KbAudioEmbedMount.test.tsx's equivalent block for why these are new:
// jsdom has no IntersectionObserver and `LazyEmbedMount` fails open without
// one, so until `src/test/intersectionObserver.ts` was installed, nothing in
// this file ran with the mount gate switched on.

describe('KbVideoEmbedMount — the lazy-mount budget (EMB-065/066)', () => {
  beforeEach(() => {
    vi.mocked(fetchLibraryEntries).mockReset()
  })

  it('mounts nothing and issues NO directory listing while the embed is out of view', () => {
    holdEmbedsOutOfView()
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    renderMount()

    const wrapper = screen.getByTestId('lazy-embed-mount')
    expect(wrapper.getAttribute('data-mounted')).toBe('false')
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-embed-mount-loading')).not.toBeInTheDocument()
    // A video is the heaviest of these kinds to start fetching by accident.
    expect(fetchLibraryEntries).not.toHaveBeenCalled()
  })

  it("reserves THIS kind's own height while unmounted (EMB-066)", () => {
    holdEmbedsOutOfView()
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    renderMount()

    expect(screen.getByTestId('lazy-embed-mount').style.minHeight).toBe(
      `${VIDEO_EMBED_RESERVED_HEIGHT_PX}px`,
    )
  })

  it('mounts the real player, and only then issues the listing, once it scrolls into view', async () => {
    holdEmbedsOutOfView()
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    renderMount()

    const wrapper = screen.getByTestId('lazy-embed-mount')
    expect(fetchLibraryEntries).not.toHaveBeenCalled()

    scrollIntoView(wrapper)

    expect(wrapper.getAttribute('data-mounted')).toBe('true')
    const video = await screen.findByTestId('library-video-preview')
    expect(video.querySelector('video')).toHaveAttribute(
      'src',
      '/api/v1/library/ws-1/download?path=video%2Fclip.mp4',
    )
    expect(fetchLibraryEntries).toHaveBeenCalledTimes(1)
    expect(wrapper.style.minHeight).toBe('')
  })

  it('gives up its place again once the reader scrolls well past it', async () => {
    // The far boundary — `LazyEmbedMount`'s second observer, and the half of
    // "begins when near, STOPS when well outside" that nothing else asserts
    // at this level. The reservation must come back, or the page collapses
    // under a reader who scrolls away.
    holdEmbedsOutOfView()
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    renderMount()

    const wrapper = screen.getByTestId('lazy-embed-mount')
    scrollIntoView(wrapper)
    await screen.findByTestId('library-video-preview')

    scrollOutOfView(wrapper)

    expect(wrapper.getAttribute('data-mounted')).toBe('false')
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
    expect(wrapper.style.minHeight).toBe(`${VIDEO_EMBED_RESERVED_HEIGHT_PX}px`)
  })
})
