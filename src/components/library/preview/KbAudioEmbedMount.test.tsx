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

// URL ORACLE. `libraryDownloadUrl` is deliberately NOT mocked: the real one
// builds its query string with `URLSearchParams`, which percent-encodes the
// path separator, and a hand-written mock here re-implemented it WITHOUT
// that — so this file asserted `path=audio/song.mp3`, a URL production never
// emits. The expected string below is the real builder's actual output,
// written out as a literal, so a change to how download URLs are built fails
// this test instead of silently agreeing with a second copy of the old rule.
const EXPECTED_SRC = '/api/v1/library/ws-1/download?path=audio%2Fsong.mp3'

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
    expect(audio.querySelector('audio')).toHaveAttribute('src', EXPECTED_SRC)
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

  // A SUCCESSFUL listing that does not contain the file is its own state —
  // see the "missing is not error" block at the bottom of this file. It used
  // to render this same generic error, which is the defect that block covers.
  it('shows a visible, named error and a working retry when the listing request FAILS', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('listing failed'))
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

// ── "Missing" is not "error" (silent-failure audit M1) ──────────────────────
//
// A directory listing that came back FINE without this file (a rename, a
// move, a delete) used to render the SAME sentence and the SAME Retry button
// as a listing that never arrived. Different cause, different remedy, and
// only one of them can be fixed by pressing Retry — so a reader whose
// colleague renamed `song.mp3` was told the server was flaky and never
// learned the link needed updating.

describe('KbAudioEmbedMount — a renamed/deleted target is distinguishable from a failed request', () => {
  it('names the file and its folder, and offers NO Retry, when the listing succeeded without it', async () => {
    // The listing RESOLVED — it just does not contain this path.
    vi.mocked(fetchLibraryEntries).mockResolvedValue([
      { ...ENTRY, name: 'intro.mp3', path: 'audio/intro.mp3' },
    ])
    renderMount()

    const missing = await screen.findByTestId('kb-embed-mount-missing')
    expect(missing).toHaveTextContent('song.mp3')
    expect(missing).toHaveTextContent('audio')
    expect(missing).toHaveTextContent(/needs updating/i)
    // No Retry: the listing already succeeded, so retrying returns the same
    // answer and teaches the reader to blame the server.
    expect(missing.querySelector('button')).toBeNull()

    // Paired negatives: this is NOT the generic error box, and no player
    // mounted for a target that is not there.
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-audio-preview')).not.toBeInTheDocument()
  })

  it('a FAILED listing still renders the generic error WITH a working Retry — the two states are not the same render', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('network down'))
    renderMount()

    const error = await screen.findByTestId('kb-embed-mount-error')
    expect(error).toHaveTextContent(/could not read this file/i)
    // This one DOES offer Retry, and it works.
    const retry = error.querySelector('button')
    expect(retry).not.toBeNull()
    expect(screen.queryByTestId('kb-embed-mount-missing')).not.toBeInTheDocument()

    vi.mocked(fetchLibraryEntries).mockResolvedValueOnce([ENTRY])
    retry?.click()
    await screen.findByTestId('library-audio-preview')
  })
})
