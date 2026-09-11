// KbAudioEmbedMount.test.tsx — ADR-083 embedded-content spec, Step 6
// (EMB-105, US-12 AS-1): "Given a note embedding a sound file, When it is
// read, Then it plays in place using the same component the full-screen
// preview uses."
//
// Every assertion here is paired: a positive "the real renderer mounted"
// assertion alongside every negative one, so deleting the dispatch (mounting
// nothing, or mounting only the loading/error placeholder forever) makes the
// pair fail rather than trivially pass. See this task's own report for the
// mutation-test proof (comment out the render call in
// `KbAudioEmbedContent`, watch this file fail; restore it, watch it pass).

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KbAudioEmbedMount } from './KbAudioEmbedMount'
import type { LibraryEntry } from '@/lib/api'

const ENTRY: LibraryEntry = {
  name: 'song.mp3',
  path: 'audio/song.mp3',
  is_dir: false,
  is_hidden: false,
  size: 2048,
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
      <KbAudioEmbedMount workspaceId="ws-1" workspacePath="audio/song.mp3" />
    </QueryClientProvider>,
  )
}

describe('KbAudioEmbedMount', () => {
  it('mounts the real audio player once the directory listing resolves the entry', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    renderMount()

    // Positive: the shared renderer's own surface actually appears.
    const audio = await screen.findByTestId('library-audio-preview')
    expect(audio.querySelector('audio')).toHaveAttribute(
      'src',
      '/api/v1/library/ws-1/download?path=audio/song.mp3',
    )
    // Paired negative: the loading/error placeholders are gone once it has.
    expect(screen.queryByTestId('kb-embed-mount-loading')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
  })

  it('shows the loading placeholder before the directory listing resolves', () => {
    vi.mocked(fetchLibraryEntries).mockReturnValue(new Promise(() => {}))
    renderMount()

    expect(screen.getByTestId('kb-embed-mount-loading')).toBeInTheDocument()
    // Paired negative: the real player has not appeared yet.
    expect(screen.queryByTestId('library-audio-preview')).not.toBeInTheDocument()
  })

  it('shows a visible, named error and a working retry when the entry cannot be found', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([])
    renderMount()

    const error = await screen.findByTestId('kb-embed-mount-error')
    expect(error).toHaveTextContent(/could not read this file/i)
    // Paired negative: no player mounted for a target that failed to resolve.
    expect(screen.queryByTestId('library-audio-preview')).not.toBeInTheDocument()

    vi.mocked(fetchLibraryEntries).mockResolvedValueOnce([ENTRY])
    error.querySelector('button')?.click()
    await screen.findByTestId('library-audio-preview')
  })

  it('shows the SAME visible error when the directory listing request itself fails', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('network down'))
    renderMount()

    await waitFor(() => expect(screen.getByTestId('kb-embed-mount-error')).toBeInTheDocument())
    expect(screen.queryByTestId('library-audio-preview')).not.toBeInTheDocument()
  })
})
