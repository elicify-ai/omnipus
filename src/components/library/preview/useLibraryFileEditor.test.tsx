// useLibraryFileEditor.test.tsx — the shared save mechanics behind every
// editable Library preview (library-spec.md D-5). Covers dirty tracking, the
// saving/saved/error status machine, that a failed save is never silently
// swallowed, and the unsavedGuard integration (library-spec.md's
// "warn before navigating away from unsaved edits" wiring) that
// LibraryExplorer.test.tsx's navigation-guard test builds on.
//
// ADR-083 Step 0 (EMB-001/EMB-004/EMB-007) coverage lives in the sibling
// useLibraryFileEditor.conflict.test.tsx (spec tests 7 and 8) — this file
// keeps the pre-existing dirty/save/error coverage, updated for the version
// token this hook now requires on every save.

import { describe, it, expect, vi, beforeEach } from 'vitest'
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

import { putLibraryContent, fetchLibraryContentVersioned } from '@/lib/api'
import { useLibraryFileEditor } from './useLibraryFileEditor'
import { isLibraryEditorDirty, setLibraryEditorDirty } from './unsavedGuard'

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
  // ADR-083 EMB-007 — this hook reads its OWN version token on mount
  // (independent of whatever `initialContent` it was constructed with — see
  // the hook's module doc). Every test needs SOME resolution here or the
  // effect's `.then()` has nothing to chain onto; tests that care about a
  // specific token override this.
  mockedFetchVersioned.mockResolvedValue({
    data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
    version: 'v1:initial',
  })
})

describe('useLibraryFileEditor — dirty tracking', () => {
  it('starts clean, goes dirty on edit, and registers with the global unsavedGuard', () => {
    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    expect(result.current.isDirty).toBe(false)
    expect(isLibraryEditorDirty()).toBe(false)

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))

    expect(result.current.isDirty).toBe(true)
    expect(isLibraryEditorDirty()).toBe(true)
  })

  it('clears the global guard on unmount even if the edit was never saved', () => {
    const { result, unmount } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '' }),
      { wrapper },
    )
    act(() => result.current.setDraft('unsaved'))
    expect(isLibraryEditorDirty()).toBe(true)

    unmount()

    expect(isLibraryEditorDirty()).toBe(false)
  })
})

describe('useLibraryFileEditor — save', () => {
  it('calls putLibraryContent with the draft and the token it read, then clears dirty and the guard on success', async () => {
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: 'v1:abc123',
    })
    mockedPut.mockResolvedValue({ data: makeEntry({ size: 40 }), version: 'v1:new456' })
    const onSaved = vi.fn()

    const { result } = renderHook(
      () =>
        useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n', onSaved }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())

    await waitFor(() => expect(result.current.status).toBe('saved'))
    expect(mockedPut).toHaveBeenCalledWith('ws-1', {
      path: 'report.md',
      content: '# Report\n\nEdited.\n',
      expect_version: 'v1:abc123',
    })
    expect(result.current.isDirty).toBe(false)
    expect(isLibraryEditorDirty()).toBe(false)
    expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ size: 40 }))
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'success')).toBe(true)
  })

  it('surfaces a failed save as status "error" with a visible message, and keeps the edit dirty for retry', async () => {
    mockedPut.mockRejectedValue(new Error('network unavailable'))

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())

    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.error).toBe('network unavailable')
    // Never silently swallowed — a toast fired too.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(true)
    // Still dirty: the edit was NOT discarded by the failed attempt.
    expect(result.current.isDirty).toBe(true)
    expect(isLibraryEditorDirty()).toBe(true)
    // A generic failure is not a conflict.
    expect(result.current.conflict).toBeUndefined()
  })

  it('is a no-op when there is nothing dirty to save', () => {
    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.save())

    expect(mockedPut).not.toHaveBeenCalled()
  })

  it('refuses to save with a client-side error when the version read never resolved a token', async () => {
    mockedFetchVersioned.mockResolvedValue({
      data: { path: 'report.md', content: '# Report\n', size: 9, is_text: true, too_large: false },
      version: null,
    })

    const { result } = renderHook(
      () => useLibraryFileEditor({ workspaceId: 'ws-1', path: 'report.md', initialContent: '# Report\n' }),
      { wrapper },
    )

    act(() => result.current.setDraft('# Report\n\nEdited.\n'))
    act(() => result.current.save())

    await waitFor(() => expect(result.current.status).toBe('error'))
    // MUTATION THIS DIES ON: falling back to an empty string or inventing a
    // token instead of refusing — putLibraryContent must never be called
    // with no real token.
    expect(mockedPut).not.toHaveBeenCalled()
  })
})
