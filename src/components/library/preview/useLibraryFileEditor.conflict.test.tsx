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

// --- B1: the token must be paired with the bytes it was read with ---------
//
// THE DEFECT THIS GUARDS: fetchLibraryContentVersioned returns content AND a
// token from ONE response, but the hook used to discard the content and pair
// the token with `initialContent` — a DIFFERENT, STRICTLY EARLIER read
// (LibraryPreviewPane's own contentQuery). If a writer changed the file in
// between, a save built on the stale `initialContent` would still pass the
// server's compare-and-swap (the token really is current) and silently
// destroy the intervening write. Every fixture above deliberately sets
// `data.content` EQUAL to `initialContent`, so none of them could ever catch
// this — the tests below are the ones where they differ.

/** Resolves/controls a fetchLibraryContentVersioned response on demand, so a
 * test can make the version read settle at a chosen moment relative to other
 * actions (typing, calling save()) instead of always before the first
 * assertion. */
function deferredVersionRead() {
  let resolve!: (v: {
    data: { path: string; content: string; size: number; is_text: boolean; too_large: boolean }
    version: string | null
  }) => void
  const promise = new Promise<{
    data: { path: string; content: string; size: number; is_text: boolean; too_large: boolean }
    version: string | null
  }>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

describe('useLibraryFileEditor — B1: pairs the token with the bytes it was read with', () => {
  it('rebases the editing baseline onto the freshly-read content when nothing has been typed yet, so the saved diff is built on the SAME bytes as the token', async () => {
    // Simulates: LibraryPreviewPane's contentQuery read 'stale content' at
    // T0 (passed in as initialContent). Before THIS hook's own read
    // resolves at T2, a concurrent writer changed the file to
    // 'fresh content' at T1 — the token this hook reads belongs to
    // 'fresh content', not 'stale content'.
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: 'fresh content\n', size: 14, is_text: true, too_large: false },
      version: 'v1:fresh-token',
    })
    mockedPut.mockResolvedValue({ data: makeEntry(), version: 'v1:after-save' })

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: 'stale content\n' }),
      { wrapper },
    )

    // MUTATION THIS DIES ON: never rebasing — draft would stay 'stale
    // content\n' forever, which is exactly what today's code does.
    await waitFor(() => expect(result.current.draft).toBe('fresh content\n'))
    expect(result.current.isDirty).toBe(false)
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'warning')).toBe(true)

    act(() => result.current.setDraft('fresh content\nedited by user\n'))
    act(() => result.current.save())

    await waitFor(() => expect(mockedPut).toHaveBeenCalled())
    const [, body] = mockedPut.mock.calls[0] as [string, { content: string; expect_version: string }]

    // The load-bearing assertion: the diff is built on the bytes the token
    // was actually read with, never on the stale initialContent.
    expect(body.content).toBe('fresh content\nedited by user\n')
    expect(body.expect_version).toBe('v1:fresh-token')
  })

  it('does not discard an edit already typed against the older content, and refuses to save rather than pair a fresh token with a stale diff', async () => {
    const { promise, resolve } = deferredVersionRead()
    mockedFetchVersioned.mockReturnValue(promise)

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: 'stale content\n' }),
      { wrapper },
    )

    // The user starts typing BEFORE the version read resolves.
    act(() => result.current.setDraft('stale content\nmy in-progress edit\n'))
    expect(result.current.isDirty).toBe(true)

    // NOW the read resolves — with DIFFERENT content, proving the token
    // belongs to a version the user never saw.
    await act(async () => {
      resolve({
        data: { path: 'report.md', content: 'fresh content\n', size: 14, is_text: true, too_large: false },
        version: 'v1:fresh-token',
      })
      await promise
    })

    // MUTATION THIS DIES ON: silently discarding the user's typed edit by
    // rebasing the draft anyway.
    expect(result.current.draft).toBe('stale content\nmy in-progress edit\n')
    await waitFor(() => expect(result.current.status).toBe('conflict'))
    expect(result.current.conflict?.actualVersion).toBe('v1:fresh-token')

    act(() => result.current.save())

    // MUTATION THIS DIES ON: letting this save reach the network — that
    // would pair 'v1:fresh-token' with a diff built on 'stale content',
    // exactly the B1 lost update.
    expect(mockedPut).not.toHaveBeenCalled()
    await waitFor(() => expect(result.current.status).toBe('conflict'))
  })

  it('refuses the save even when save() is invoked WHILE the version read is still in flight and only later discovers the mismatch', async () => {
    const { promise, resolve } = deferredVersionRead()
    mockedFetchVersioned.mockReturnValue(promise)

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: 'stale content\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('stale content\nmy in-progress edit\n'))

    // save() is called BEFORE the read settles — mutationFn's mismatch
    // check must happen AFTER it awaits the token, not before, or it would
    // race the read's own detection and let this through.
    act(() => result.current.save())

    await act(async () => {
      resolve({
        data: { path: 'report.md', content: 'fresh content\n', size: 14, is_text: true, too_large: false },
        version: 'v1:fresh-token',
      })
      await promise
    })

    // MUTATION THIS DIES ON: checking contentMismatchRef before awaiting
    // ensureVersion() — that ordering lets a save triggered mid-flight
    // reach putLibraryContent before the mismatch is known.
    await waitFor(() => expect(result.current.status).toBe('conflict'))
    expect(mockedPut).not.toHaveBeenCalled()
  })
})

// --- stale-promise resurrection: a deleted file or a stripped ETag must ---
// --- not turn Save into an inescapable identical-retry loop ---------------
//
// THE DEFECT THIS GUARDS: ensureVersion() falls back to the mount-time
// read's settled promise whenever versionRef.current is falsy. That promise
// is never cleared once it settles, so a LATER null in versionRef.current —
// a 409 whose body has no actual_version (the file was deleted), or a
// successful save whose response had no ETag (a proxy stripped it) — was
// silently masked by resurrecting the ORIGINAL, long-stale mount-time token
// instead of being reported honestly. Save then resent that exact stale
// token and got the exact same 409 forever, with no way out short of a page
// reload.

describe('useLibraryFileEditor — stale-promise resurrection', () => {
  it('a 409 with actual_version ABSENT does not let the next save resend the stale mount-time token forever', async () => {
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:mount-token',
    })
    // The file was deleted since — LibraryConflictError.yaml: actual_version
    // is absent in exactly this case.
    mockedPut.mockRejectedValueOnce(
      new LibraryVersionConflictError(
        {
          error: 'report.md changed on disk since you opened it: it has been deleted',
          code: 'library_version_conflict',
          path: 'report.md',
          expected_version: 'v1:mount-token',
          actual_version: undefined,
        },
        '',
      ),
    )

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())
    await waitFor(() => expect(mockedPut).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(result.current.status).toBe('conflict'))
    expect(result.current.conflict?.actualVersion).toBeUndefined()

    // The user presses Save again — the module's own documented retry path.
    act(() => result.current.save())

    // MUTATION THIS DIES ON: ensureVersion() falling back to the settled
    // mount-time promise and resending 'v1:mount-token' — that would call
    // putLibraryContent a SECOND time with the identical stale token
    // (indistinguishable, from the outside, from an infinite retry loop).
    // The honest outcome is a CLIENT-SIDE refusal: no second network call.
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(mockedPut).toHaveBeenCalledTimes(1)
    expect(result.current.error).toMatch(/version/i)
  })

  it('a successful save whose response carries no ETag does not let the next save resurrect the stale mount-time token', async () => {
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:mount-token',
    })
    // The PUT succeeds, but a proxy stripped the ETag off the response.
    mockedPut.mockResolvedValueOnce({ data: makeEntry({ size: 20 }), version: null })

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nfirst edit\n'))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.status).toBe('saved'))
    expect(mockedPut).toHaveBeenCalledTimes(1)

    // A second, independent edit and save.
    act(() => result.current.setDraft('# Report\n\nsecond edit\n'))
    act(() => result.current.save())

    // MUTATION THIS DIES ON: ensureVersion() resurrecting 'v1:mount-token'
    // (the ORIGINAL mount-time read, now describing a version two saves
    // out of date) instead of honestly reporting no current token is known.
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(mockedPut).toHaveBeenCalledTimes(1)
    expect(result.current.error).toMatch(/version/i)
  })
})
