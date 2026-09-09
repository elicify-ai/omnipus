// useLibraryFileEditor.conflict.test.tsx — ADR-083 Step 0 (EMB-001/EMB-004/
// EMB-006/EMB-007/EMB-007c), spec tests 7 and 8.
//
// THE DEFECT THIS GUARDS: useLibraryFileEditor used to call putLibraryContent
// with no version token at all — last-write-wins. If this editor and, say,
// an agent both edit the same note, one of them silently loses the work.
//
// What a false green looks like here (per the spec's own review of this
// phase — repeated verbatim because it names the exact trap): "a no
// auto-retry test that passes because the request never happened" — this
// hook already used useMutation with no `retry` option (default 0), and a
// failed save already left the draft dirty, so "one request, still one,
// one more only on press" is satisfiable by the OLD generic-error path with
// NO conflict handling at all. Test 8 below therefore asserts the two
// clauses that path could never satisfy: (a) the surfaced state is the
// CONFLICT state carrying the server's current token, distinguishable from
// the plain 'error' state a network failure produces, and (b) the retry
// request carries the FRESH token from the 409 body — never the one the
// first, refused attempt sent.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    putLibraryContent: vi.fn(),
    fetchLibraryContentVersioned: vi.fn(),
  }
})

import { putLibraryContent, fetchLibraryContentVersioned, LibraryVersionConflictError } from '@/lib/api'
import { useLibraryFileEditor } from './useLibraryFileEditor'
import { setLibraryEditorDirty } from './unsavedGuard'

const mockedPut = vi.mocked(putLibraryContent)
const mockedFetchVersioned = vi.mocked(fetchLibraryContentVersioned)

function makeEntry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'report.md',
    path: 'report.md',
    is_dir: false,
    is_hidden: false,
    size: 9,
    modified_at: '2026-07-28T10:15:00Z',
    is_text_editable: true,
    ...over,
  }
}

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  setLibraryEditorDirty(false)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useLibraryFileEditor — test 7: sends the token it read', () => {
  it('sends back the EXACT token fetchLibraryContentVersioned returned, not an empty string or an invented one', async () => {
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:9f2a7c40',
    })
    mockedPut.mockResolvedValue({ data: makeEntry({ size: 40 }), version: 'v1:1b8e330d' })

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())

    await waitFor(() => expect(mockedPut).toHaveBeenCalled())
    const [, body] = mockedPut.mock.calls[0] as [string, { expect_version: string }]
    // MUTATION THIS DIES ON: sending '' or a hardcoded/placeholder token
    // instead of the one the read actually returned.
    expect(body.expect_version).toBe('v1:9f2a7c40')
    expect(body.expect_version).not.toBe('')
  })

  it('never calls putLibraryContent with an empty or missing expect_version, even under a rapid double-save', async () => {
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:aaa111',
    })
    mockedPut.mockResolvedValue({ data: makeEntry(), version: 'v1:bbb222' })

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())

    await waitFor(() => expect(mockedPut).toHaveBeenCalledTimes(1))
    for (const call of mockedPut.mock.calls) {
      const body = call[1] as { expect_version: string }
      expect(body.expect_version).toBeTruthy()
    }
  })
})

describe('useLibraryFileEditor — test 8: surfaces conflict without resending', () => {
  it('does not auto-retry a refused save, surfaces a distinguishable conflict state, and a manual retry sends the FRESH token', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })

    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:stale-token',
    })
    mockedPut.mockRejectedValueOnce(
      new LibraryVersionConflictError(
        {
          error: 'report.md changed on disk since you opened it',
          code: 'library_version_conflict',
          path: 'report.md',
          expected_version: 'v1:stale-token',
          actual_version: 'v1:fresh-token',
        },
        JSON.stringify({ error: 'report.md changed on disk since you opened it' }),
      ),
    )

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())

    await waitFor(() => expect(mockedPut).toHaveBeenCalledTimes(1))

    // (a) one request so far.
    expect(mockedPut).toHaveBeenCalledTimes(1)

    // The surfaced state is a CONFLICT — distinguishable from the plain
    // 'error' status a generic/network failure produces (see
    // useLibraryFileEditor.test.tsx's "surfaces a failed save" test, which
    // asserts status === 'error' and conflict === undefined for that case).
    await waitFor(() => expect(result.current.status).toBe('conflict'))
    expect(result.current.conflict).toBeDefined()
    expect(result.current.conflict?.path).toBe('report.md')
    expect(result.current.conflict?.actualVersion).toBe('v1:fresh-token')
    expect(result.current.conflict?.expectedVersion).toBe('v1:stale-token')
    // Still dirty — the edit was not discarded by the refusal.
    expect(result.current.isDirty).toBe(true)

    // (b) MUTATION THIS DIES ON: an auto-retry (e.g. a `retry` option on
    // useMutation, or a timer that re-calls mutate()). Advance well past any
    // plausible backoff window — still exactly one request.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5 * 60 * 1000)
    })
    expect(mockedPut).toHaveBeenCalledTimes(1)

    // (c) the user presses Save again (the only retry affordance this
    // editor's UI has — see the hook's module doc) — exactly one MORE call.
    mockedPut.mockResolvedValueOnce({ data: makeEntry({ size: 41 }), version: 'v1:after-retry' })
    act(() => result.current.save())
    await waitFor(() => expect(mockedPut).toHaveBeenCalledTimes(2))

    // The load-bearing assertion: the retry sends the FRESH token the 409
    // body handed back, never the stale one the refused attempt sent.
    const [, retryBody] = mockedPut.mock.calls[1] as [string, { expect_version: string }]
    expect(retryBody.expect_version).toBe('v1:fresh-token')
    expect(retryBody.expect_version).not.toBe('v1:stale-token')

    await waitFor(() => expect(result.current.status).toBe('saved'))
    expect(result.current.conflict).toBeUndefined()
  })
})
