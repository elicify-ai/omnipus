// useLibraryFileEditor.timeout.test.tsx — UAT D-98 (2026-09-13): a save cut
// off by a network outage must FAIL LOUDLY within a bounded time, keep the
// draft exactly as typed, and leave Save pressable again.
//
// Observed before the fix: Save during an outage sat in "Saving…" with the
// button disabled at 21 s, 66 s and 111 s — no timeout, no error — until a
// background refetch destroyed the editor and the text with it.
//
// The mocked PUT below honours the AbortSignal the hook passes and settles
// ONLY when aborted, so on the old code (no signal, no deadline) the
// mutation never settles and `status` never leaves 'saving' — the test
// fails on its waitFor, which is the defect verbatim.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, putLibraryContent: vi.fn(), fetchLibraryContentVersioned: vi.fn() }
})

import { ApiError, putLibraryContent, fetchLibraryContentVersioned } from '@/lib/api'
import { useLibraryFileEditor } from './useLibraryFileEditor'
import { setLibraryEditorDirty } from './unsavedGuard'

const mockedPut = vi.mocked(putLibraryContent)
const mockedFetchVersioned = vi.mocked(fetchLibraryContentVersioned)

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

const entry: LibraryEntry = {
  name: 'report.md',
  path: 'report.md',
  is_dir: false,
  is_hidden: false,
  size: 9,
  modified_at: '2026-07-28T10:15:00Z',
  is_text_editable: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  setLibraryEditorDirty(false)
  mockedFetchVersioned.mockResolvedValue({
    data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
    version: 'v1:initial',
  })
})

describe('useLibraryFileEditor — D-98 save deadline', () => {
  it('a PUT that never answers is abandoned after the deadline: error stated, draft kept, Save available again', async () => {
    // Settles only on abort — the shape of a request into a dead network.
    mockedPut.mockImplementationOnce(
      (_ws, _body, opts) =>
        new Promise((_resolve, reject) => {
          opts?.signal?.addEventListener('abort', () =>
            reject(new ApiError(0, 'Network unavailable. Check your connection.')),
          )
        }),
    )

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n', saveTimeoutMs: 40 }),
      { wrapper },
    )
    await waitFor(() => expect(mockedFetchVersioned).toHaveBeenCalledTimes(1))

    act(() => result.current.setDraft('# Report\n\nTyped while offline.\n'))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('saving'))

    // The deadline passes: the failure is REPORTED, not waited on forever.
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toMatch(/took too long/i)
    expect(result.current.error).toMatch(/press Save again/i)
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(true)

    // The text the reader typed is exactly where they left it, still dirty.
    expect(result.current.draft).toBe('# Report\n\nTyped while offline.\n')
    expect(result.current.isDirty).toBe(true)

    // And Save works again once the network is back — same button, same draft.
    mockedPut.mockResolvedValueOnce({ data: { ...entry, size: 30 }, version: 'v1:after' })
    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('saved'))
    expect(mockedPut).toHaveBeenCalledTimes(2)
    expect(mockedPut.mock.calls[1][1]).toMatchObject({ content: '# Report\n\nTyped while offline.\n', expect_version: 'v1:initial' })
    expect(result.current.isDirty).toBe(false)
  })

  it('a PUT that answers in time is unaffected by the deadline', async () => {
    mockedPut.mockResolvedValueOnce({ data: entry, version: 'v1:after' })
    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n', saveTimeoutMs: 40 }),
      { wrapper },
    )
    await waitFor(() => expect(mockedFetchVersioned).toHaveBeenCalledTimes(1))
    act(() => result.current.setDraft('# Report\n\nQuick.\n'))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('saved'))
    expect(result.current.error).toBeUndefined()
  })
})

// Claude review 2026-09-14, cut-list: the retry after a timed-out save used
// to be a BLIND PUT with the token this editor last saw. But the deadline is
// a ceiling on SILENCE, not on the write: the server can complete the timed-
// out PUT moments after the client abandons it, and then the blind retry
// sends the old token into a file that has moved on — the reader gets a
// misleading "changed on disk" 409 about a write that was THEIR OWN. The
// retry now re-reads the file first:
//   - unchanged since the edit started -> nothing landed; retry proceeds,
//     with the token from THAT read;
//   - changed -> surface a real conflict (the timed-out write may have
//     landed), never a blind PUT.
describe('useLibraryFileEditor — retry after a timed-out save re-checks the file first', () => {
  const DRAFT = '# Report\n\nTyped while offline.\n'

  async function renderAndTimeOutASave() {
    mockedPut.mockImplementationOnce(
      (_ws, _body, opts) =>
        new Promise((_resolve, reject) => {
          opts?.signal?.addEventListener('abort', () =>
            reject(new ApiError(0, 'Network unavailable. Check your connection.')),
          )
        }),
    )
    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n', saveTimeoutMs: 40 }),
      { wrapper },
    )
    await waitFor(() => expect(mockedFetchVersioned).toHaveBeenCalledTimes(1))
    act(() => result.current.setDraft(DRAFT))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toMatch(/took too long/i)
    return result
  }

  it('unchanged on disk -> the retry proceeds and PUTs with the token from the re-check read', async () => {
    const result = await renderAndTimeOutASave()

    // The re-check read: nothing landed, but time (or a proxy) moved the
    // token on. The retry must send THIS token, not the pre-timeout one.
    mockedFetchVersioned.mockResolvedValueOnce({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:fresh',
    })
    mockedPut.mockResolvedValueOnce({ data: { ...entry, size: 30 }, version: 'v1:after' })

    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('saved'))
    expect(mockedFetchVersioned).toHaveBeenCalledTimes(2)
    expect(mockedPut).toHaveBeenCalledTimes(2)
    expect(mockedPut.mock.calls[1][1]).toMatchObject({ content: DRAFT, expect_version: 'v1:fresh' })
    expect(result.current.isDirty).toBe(false)
  })

  it('changed since the edit started -> a real conflict is surfaced, no blind PUT', async () => {
    const result = await renderAndTimeOutASave()

    // The server completed the timed-out write after all: the file now holds
    // the draft under a new token. The old code PUT blindly and answered
    // "changed on disk" about the reader's OWN write; the retry must surface
    // a conflict that says what actually happened and keep the draft.
    mockedFetchVersioned.mockResolvedValueOnce({
      data: { path: 'report.md', content: DRAFT, size: 30, is_text: true, too_large: false },
      version: 'v1:landed',
    })

    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('conflict'))
    expect(mockedPut).toHaveBeenCalledTimes(1)
    expect(result.current.conflict).toMatchObject({ actualVersion: 'v1:landed' })
    expect(result.current.error).toMatch(/timed out.*may have reached the server/i)
    expect(result.current.draft).toBe(DRAFT)
    expect(result.current.isDirty).toBe(true)
  })
})
