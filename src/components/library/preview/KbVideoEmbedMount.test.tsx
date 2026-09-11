// KbVideoEmbedMount.test.tsx — ADR-083 embedded-content spec, Step 6
// (EMB-105, US-12 AS-2): "Given a note embedding a video file, When it is
// read, Then it plays in place using the same component the full-screen
// preview uses."
//
// Same pairing discipline as KbAudioEmbedMount.test.tsx: every negative
// assertion sits beside a positive one proving the real player mounted, so a
// deleted dispatch fails the pair rather than passing trivially. See this
// task's own report for the mutation-test proof on this exact file.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KbVideoEmbedMount } from './KbVideoEmbedMount'
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
    libraryDownloadUrl: (wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`,
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
      '/api/v1/library/ws-1/download?path=video/clip.mp4',
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

  it('shows a visible, named error and a working retry when the entry cannot be found', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([])
    renderMount()

    const error = await screen.findByTestId('kb-embed-mount-error')
    expect(error).toHaveTextContent(/could not read this file/i)
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()

    vi.mocked(fetchLibraryEntries).mockResolvedValueOnce([ENTRY])
    error.querySelector('button')?.click()
    await screen.findByTestId('library-video-preview')
  })

  it('shows the SAME visible error when the directory listing request itself fails', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('network down'))
    renderMount()

    await waitFor(() => expect(screen.getByTestId('kb-embed-mount-error')).toBeInTheDocument())
    expect(screen.queryByTestId('library-video-preview')).not.toBeInTheDocument()
  })
})
