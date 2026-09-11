// useResolvedEmbedEntry.test.tsx — ADR-083 review I11.
//
// THE HOOK'S HEADER MAKES A PERFORMANCE CLAIM AND NOTHING CHECKED IT:
// "Same query key, so TanStack Query dedupes a directory already open in the
// Library pane or requested by a sibling embed in the same note — no extra
// request per embed."
//
// That claim rests entirely on WHICH ARGUMENTS the hook passes, and `grep`
// for `toHaveBeenCalled` across KbAudioEmbedMount.test.tsx,
// KbVideoEmbedMount.test.tsx, KbPdfPageEmbedMount.test.tsx,
// LibraryAudioPreview.test.tsx and LibraryVideoPreview.test.tsx returned
// ZERO hits. Calling `fetchLibraryEntries(workspaceId, workspacePath, true)`
// — the FILE's own path, recursive — instead of `(workspaceId, parentDir,
// false)` passes every one of those tests, as long as the mock resolves an
// entry. In production it would miss the shared key entirely and issue one
// recursive listing PER EMBED: a note with eight audio embeds becomes eight
// recursive directory walks where the design says zero extra requests.
//
// So this file asserts the call itself, and the dedupe property directly:
// two hooks on two files in the SAME directory must produce ONE request.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchLibraryEntries: vi.fn() }
})

import { fetchLibraryEntries, type LibraryEntry } from '@/lib/api'
import { useResolvedEmbedEntry } from './useResolvedEmbedEntry'

const mockedFetch = vi.mocked(fetchLibraryEntries)

function entry(path: string): LibraryEntry {
  return {
    name: path.slice(path.lastIndexOf('/') + 1),
    path,
    is_dir: false,
    size: 1234,
    modified_at: '2026-09-01T00:00:00Z',
  } as LibraryEntry
}

/** A fresh client per test: retry off so an `error` case resolves at once,
 *  and no cache carried between tests (which would make the dedupe test pass
 *  for the wrong reason). */
function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useResolvedEmbedEntry — the query contract its dedupe claim rests on', () => {
  it('lists the PARENT DIRECTORY, non-recursively — not the file path, not a recursive walk', async () => {
    mockedFetch.mockResolvedValue([entry('media/song.mp3'), entry('media/other.mp3')])

    const { result } = renderHook(() => useResolvedEmbedEntry('ws-1', 'media/song.mp3'), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.status).toBe('ready'))

    expect(mockedFetch).toHaveBeenCalledTimes(1)
    // The exact argument tuple IS the contract — this is the assertion that
    // was missing everywhere.
    expect(mockedFetch).toHaveBeenCalledWith('ws-1', 'media', false)

    const [, path, recursive] = mockedFetch.mock.calls[0]
    expect(path).not.toBe('media/song.mp3')
    expect(recursive).toBe(false)
  })

  it('issues ONE request for two embeds of different files in the SAME directory', async () => {
    // This is the header's "no extra request per embed", asserted as a
    // discrete count rather than inferred from the key. A hook keyed by the
    // file path would make this 2.
    mockedFetch.mockResolvedValue([entry('media/song.mp3'), entry('media/clip.mp4')])
    const w = wrapper()

    const a = renderHook(() => useResolvedEmbedEntry('ws-1', 'media/song.mp3'), { wrapper: w })
    const b = renderHook(() => useResolvedEmbedEntry('ws-1', 'media/clip.mp4'), { wrapper: w })

    await waitFor(() => expect(a.result.current.status).toBe('ready'))
    await waitFor(() => expect(b.result.current.status).toBe('ready'))

    expect(mockedFetch).toHaveBeenCalledTimes(1)
  })

  it('POSITIVE CONTROL — two DIFFERENT directories really do issue two requests', async () => {
    // Without this, the test above passes on a hook that fetches once and
    // never again regardless of input — which would break every embed
    // outside the first directory.
    mockedFetch.mockResolvedValue([entry('media/song.mp3'), entry('audio/clip.mp4')])
    const w = wrapper()

    const a = renderHook(() => useResolvedEmbedEntry('ws-1', 'media/song.mp3'), { wrapper: w })
    const b = renderHook(() => useResolvedEmbedEntry('ws-1', 'audio/clip.mp4'), { wrapper: w })

    await waitFor(() => expect(a.result.current.status).toBe('ready'))
    await waitFor(() => expect(b.result.current.status).toBe('ready'))

    expect(mockedFetch).toHaveBeenCalledTimes(2)
    expect(mockedFetch).toHaveBeenCalledWith('ws-1', 'media', false)
    expect(mockedFetch).toHaveBeenCalledWith('ws-1', 'audio', false)
  })
})

describe('useResolvedEmbedEntry — dirnameOf at the root (review I11, the `i <= 0` branch)', () => {
  // Every existing fixture uses a nested path, so neither root case was
  // reachable. Both must resolve to the vault root, `''`.

  it('a ROOT-LEVEL embed (no slash at all) lists the root, not a directory named after the file', async () => {
    // `![[song.mp3]]` — lastIndexOf('/') is -1.
    mockedFetch.mockResolvedValue([entry('song.mp3')])

    const { result } = renderHook(() => useResolvedEmbedEntry('ws-1', 'song.mp3'), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(mockedFetch).toHaveBeenCalledWith('ws-1', '', false)
  })

  it('a LEADING-SLASH path also lists the root rather than an empty-named directory', async () => {
    // `/song.mp3` — lastIndexOf('/') is 0. Slicing to 0 gives '' only
    // because the branch tests `i <= 0` rather than `i < 0`; with `i < 0`
    // this would request a directory whose name is the empty string.
    mockedFetch.mockResolvedValue([entry('/song.mp3')])

    const { result } = renderHook(() => useResolvedEmbedEntry('ws-1', '/song.mp3'), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(mockedFetch).toHaveBeenCalledWith('ws-1', '', false)
  })
})

describe('useResolvedEmbedEntry — `missing` is not a flavour of `error` (review I15 regression guard)', () => {
  // The hook's own header argues these are different facts with different
  // remedies. Asserting they are different STATES is what stops a future
  // simplification from collapsing them back into one.

  it('a listing that succeeds WITHOUT this file is `missing`, and carries the directory it looked in', async () => {
    mockedFetch.mockResolvedValue([entry('media/something-else.mp3')])

    const { result } = renderHook(() => useResolvedEmbedEntry('ws-1', 'media/song.mp3'), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.status).toBe('missing'))
    expect(result.current.status).not.toBe('error')
    if (result.current.status === 'missing') {
      expect(result.current.parentDir).toBe('media')
    }
  })

  it('a listing that FAILS is `error` — the same input shape, the other outcome', async () => {
    mockedFetch.mockRejectedValue(new Error('network down'))

    const { result } = renderHook(() => useResolvedEmbedEntry('ws-1', 'media/song.mp3'), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.status).not.toBe('missing')
  })
})
