// useFileSearch.test.ts — unified-search-and-grep-spec.md US-2/US-4, MV-11,
// FR-016.
//
// Three things this hook is the ONLY place that can be pinned down without a
// live server: (1) the human bar is ALWAYS literal/smart-case — regex:false,
// case:smart — no matter what the typed text looks like, so metacharacters
// can never produce a parse error from the bar (FR-016); (2) at most one
// search is ever in flight — a debounced query change aborts whatever was
// still running, which cancels the server-side walk too, because the walk
// semaphore is a scarce, gateway-wide 2-slot resource (MV-11); (3) a 429
// (busy) answer is invisible to a typist — previous results stay on screen,
// exactly one retry fires 500ms later, and only a SECOND failure becomes a
// visible error.

import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'
import {
  useFileSearch,
  FILE_SEARCH_DEBOUNCE_MS,
  FILE_SEARCH_DEADLINE_MS,
  FILE_SEARCH_RETRY_DELAY_MS,
  type FileSearchResponse,
} from './useFileSearch'

function response(over: Partial<FileSearchResponse> = {}): FileSearchResponse {
  return {
    hits: [],
    truncated: false,
    limits_applied: {
      files: 50000,
      bytes: 268435456,
      matches: 1000,
      matches_per_file: 50,
      depth: 32,
      deadline_ms: FILE_SEARCH_DEADLINE_MS,
      output_bytes: 1048576,
    },
    stats: {
      files_visited: 0,
      bytes_scanned: 0,
      files_skipped_problems: 0,
      files_pruned_ignored: 0,
      files_skipped_per_file_cap: 0,
      hits_capped_per_file: 0,
    },
    ...over,
  }
}

const settle = (ms: number) => act(async () => { await new Promise((r) => setTimeout(r, ms)) })

// ─────────────────────────────────────────────────────────────────────────────
// Gating — no request without a real reason to send one
// ─────────────────────────────────────────────────────────────────────────────

describe('useFileSearch — gating', () => {
  it('issues no request for a blank or whitespace-only query', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(() => useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: '   ', searchFn }))
    await settle(FILE_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })

  it('issues no request at the virtual root (workspaceId null)', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(() => useFileSearch({ workspaceId: null, folderPath: '', query: 'report', searchFn }))
    await settle(FILE_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })

  it('issues no request while disabled — the bar is in a different mode (e.g. a vault folder)', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(() =>
      useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: 'report', enabled: false, searchFn }),
    )
    await settle(FILE_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Debounce
// ─────────────────────────────────────────────────────────────────────────────

describe('useFileSearch — debounce', () => {
  it('collapses a burst of keystrokes into ONE request, for the final term', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    const { rerender } = renderHook(
      ({ q }: { q: string }) => useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: q, searchFn }),
      { initialProps: { q: '' } },
    )
    rerender({ q: 'r' })
    rerender({ q: 're' })
    rerender({ q: 'rep' })
    rerender({ q: 'report' })

    await waitFor(() => expect(searchFn).toHaveBeenCalled())
    await settle(FILE_SEARCH_DEBOUNCE_MS * 2)

    expect(searchFn.mock.calls.map((c) => (c[1] as { query: string }).query)).toEqual(['report'])
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// FR-016 / MV-11 — the human bar is always literal, smart-case, 3s deadline
// ─────────────────────────────────────────────────────────────────────────────

describe('useFileSearch — the request the human bar sends (FR-016, MV-11)', () => {
  it('sends regex:false, case:smart, include_hidden:false and a 3s deadline, unconditionally', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(() =>
      useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: 'f(x)', debounceMs: 5, searchFn }),
    )
    await waitFor(() => expect(searchFn).toHaveBeenCalled())

    const [workspaceId, body] = searchFn.mock.calls[0] as [string, Record<string, unknown>]
    expect(workspaceId).toBe('ws-1')
    // The literal text is sent VERBATIM — "f(x)" is not escaped, transformed,
    // or rejected; regex:false is what makes it match literally server-side.
    expect(body.query).toBe('f(x)')
    expect(body.regex).toBe(false)
    expect(body.case).toBe('smart')
    expect(body.include_hidden).toBe(false)
    expect(body.context_lines).toBe(0)
    expect(body.limits).toEqual({ deadline_ms: FILE_SEARCH_DEADLINE_MS })
  })

  it('scopes the request to the browsed folder via `path`, omitting it at the workspace root', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    const { rerender } = renderHook(
      ({ folderPath }: { folderPath: string }) =>
        useFileSearch({ workspaceId: 'ws-1', folderPath, query: 'report', debounceMs: 5, searchFn }),
      { initialProps: { folderPath: '01-Areas/Finance' } },
    )
    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(1))
    expect((searchFn.mock.calls[0]?.[1] as { path?: string }).path).toBe('01-Areas/Finance')

    rerender({ folderPath: '' })
    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(2))
    expect(searchFn.mock.calls[1]?.[1]).not.toHaveProperty('path')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// MV-11 — at most one search in flight
// ─────────────────────────────────────────────────────────────────────────────

describe('useFileSearch — at most one search in flight (MV-11)', () => {
  it('cancels a superseded request via AbortSignal when a new query fires', async () => {
    const searchFn = vi.fn().mockImplementation(() => new Promise(() => {})) // never resolves
    const { rerender } = renderHook(
      ({ q }: { q: string }) => useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: q, debounceMs: 5, searchFn }),
      { initialProps: { q: 'first' } },
    )
    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(1))
    const firstSignal = searchFn.mock.calls[0]?.[2] as AbortSignal
    expect(firstSignal.aborted).toBe(false)

    rerender({ q: 'second' })
    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(2))

    expect(firstSignal.aborted).toBe(true)
  })

  it('aborts the in-flight request when the query is cleared', async () => {
    const searchFn = vi.fn().mockImplementation(() => new Promise(() => {}))
    const { rerender } = renderHook(
      ({ q }: { q: string }) => useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: q, debounceMs: 5, searchFn }),
      { initialProps: { q: 'report' } },
    )
    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(1))
    const signal = searchFn.mock.calls[0]?.[2] as AbortSignal

    rerender({ q: '' })
    await waitFor(() => expect(signal.aborted).toBe(true))
  })

  it('a slow answer to a since-superseded query never overwrites a later result', async () => {
    const resolvers: Record<string, (r: FileSearchResponse) => void> = {}
    const searchFn = vi.fn((_ws: string, body: { query: string }) => new Promise<FileSearchResponse>((resolve) => {
      resolvers[body.query] = resolve
    }))
    const { result, rerender } = renderHook(
      ({ q }: { q: string }) => useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: q, debounceMs: 5, searchFn }),
      { initialProps: { q: 'rep' } },
    )
    await waitFor(() => expect(resolvers['rep']).toBeDefined())

    rerender({ q: 'report' })
    await waitFor(() => expect(resolvers['report']).toBeDefined())

    await act(async () => {
      resolvers['report']?.(response({ hits: [{ path: 'report.md', match_kind: 'name' }] }))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.response?.hits).toHaveLength(1))

    // The earlier, slower "rep" answer resolves second — it must change nothing.
    await act(async () => {
      resolvers['rep']?.(response({ hits: [{ path: 'reply.md', match_kind: 'name' }] }))
      await Promise.resolve()
    })
    await settle(30)

    expect(result.current.debouncedQuery).toBe('report')
    expect(result.current.response?.hits.map((h) => h.path)).toEqual(['report.md'])
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// MV-11 — a 429 never error-flashes a typist
// ─────────────────────────────────────────────────────────────────────────────

describe('useFileSearch — 429 keeps prior results and retries exactly once (MV-11)', () => {
  it('retries once after 500ms and shows the retried answer, without ever setting an error', async () => {
    const searchFn = vi
      .fn()
      .mockRejectedValueOnce(new ApiError(429, 'Too many requests', { retryAfterMs: 1000 }))
      .mockResolvedValueOnce(response({ hits: [{ path: 'a.md', match_kind: 'name' }] }))

    const { result } = renderHook(() =>
      useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: 'report', debounceMs: 5, searchFn }),
    )

    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(1))
    // Busy the whole time; never an error, even while the first attempt has
    // already failed and the retry has not landed yet.
    expect(result.current.error).toBeNull()

    await settle(FILE_SEARCH_RETRY_DELAY_MS + 50)

    expect(searchFn).toHaveBeenCalledTimes(2)
    expect(result.current.error).toBeNull()
    await waitFor(() => expect(result.current.response?.hits).toHaveLength(1))
  })

  it('keeps the PREVIOUS response on screen while the retry is pending', async () => {
    const searchFn = vi
      .fn()
      .mockResolvedValueOnce(response({ hits: [{ path: 'old.md', match_kind: 'name' }] }))
    const { result, rerender } = renderHook(
      ({ q }: { q: string }) => useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: q, debounceMs: 5, searchFn }),
      { initialProps: { q: 'old' } },
    )
    await waitFor(() => expect(result.current.response?.hits).toHaveLength(1))

    searchFn.mockRejectedValueOnce(new ApiError(429, 'Too many requests'))
    searchFn.mockImplementationOnce(() => new Promise(() => {})) // retry never resolves in this test
    rerender({ q: 'new' })

    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(2))
    // The 429 landed, but the stale "old.md" result is still what the caller
    // sees — never blanked, never error-flashed.
    expect(result.current.response?.hits.map((h) => h.path)).toEqual(['old.md'])
    expect(result.current.error).toBeNull()
  })

  it('surfaces an error when the retry ALSO fails — the grace period is exactly one attempt', async () => {
    const searchFn = vi
      .fn()
      .mockRejectedValueOnce(new ApiError(429, 'Too many requests'))
      .mockRejectedValueOnce(new ApiError(429, 'Too many requests'))

    const { result } = renderHook(() =>
      useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: 'report', debounceMs: 5, searchFn }),
    )
    await waitFor(() => expect(searchFn).toHaveBeenCalledTimes(1))
    await settle(FILE_SEARCH_RETRY_DELAY_MS + 50)

    expect(searchFn).toHaveBeenCalledTimes(2)
    await waitFor(() => expect(result.current.error).not.toBeNull())
  })

  it('surfaces a non-429 error immediately, without retrying', async () => {
    const searchFn = vi.fn().mockRejectedValue(new ApiError(500, 'Internal error'))
    const { result } = renderHook(() =>
      useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: 'report', debounceMs: 5, searchFn }),
    )
    await waitFor(() => expect(result.current.error).not.toBeNull())
    await settle(FILE_SEARCH_RETRY_DELAY_MS + 50)
    expect(searchFn).toHaveBeenCalledTimes(1)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// MV-11 — a pending 429 retry must not outlive the hook that scheduled it
// ─────────────────────────────────────────────────────────────────────────────

describe('useFileSearch — a superseded/unmounted hook never fires its pending 429 retry', () => {
  it('issues no further request when the hook unmounts before the retry delay elapses', async () => {
    vi.useFakeTimers()
    try {
      const searchFn = vi
        .fn()
        .mockRejectedValueOnce(new ApiError(429, 'Too many requests'))
        .mockResolvedValueOnce(response())

      const { unmount } = renderHook(() =>
        useFileSearch({ workspaceId: 'ws-1', folderPath: '', query: 'report', searchFn }),
      )

      // Let attempt 0 fire and its 429 rejection schedule the retry timer.
      await act(async () => {
        await Promise.resolve()
        await Promise.resolve()
      })
      expect(searchFn).toHaveBeenCalledTimes(1)

      unmount()

      // Advance past the retry delay — an unmounted hook must not occupy
      // the 2-slot walk semaphore with a request nothing can ever abort.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(FILE_SEARCH_RETRY_DELAY_MS + 50)
      })

      expect(searchFn).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  // NOT an oracle for the cleanup's clearTimeout — verified by mutation: with
  // that clearTimeout removed, this test still passes and only the unmount
  // test above fails. A folderPath change re-runs the effect, which bumps
  // requestTokenRef, so the pending retry's own isCurrent() check already
  // suppresses the request before the timer matters. This case guards THAT
  // token guard, which is a real path that could regress independently; the
  // unmount case is the one that pins the timer being cleared (unmount runs
  // no new effect, so the token is never bumped and the timer would fire).
  it('issues no further request for the abandoned folder when folderPath changes before the retry delay elapses (guards the request-token check, not the timer clear)', async () => {
    vi.useFakeTimers()
    try {
      const searchFn = vi
        .fn()
        .mockRejectedValueOnce(new ApiError(429, 'Too many requests')) // folder A, attempt 0
        .mockResolvedValue(response()) // folder B, and any leaked folder-A retry

      const { rerender } = renderHook(
        ({ folderPath }: { folderPath: string }) =>
          useFileSearch({ workspaceId: 'ws-1', folderPath, query: 'report', searchFn }),
        { initialProps: { folderPath: 'A' } },
      )

      await act(async () => {
        await Promise.resolve()
        await Promise.resolve()
      })
      expect(searchFn).toHaveBeenCalledTimes(1)

      rerender({ folderPath: 'B' })

      await act(async () => {
        await Promise.resolve()
      })
      // Folder B's own attempt 0 fires immediately.
      expect(searchFn).toHaveBeenCalledTimes(2)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(FILE_SEARCH_RETRY_DELAY_MS + 50)
      })

      // No third call — folder A's superseded retry must never fire.
      expect(searchFn).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })
})
