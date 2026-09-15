/**
 * chat.library-changed.test.ts — D-107, WS half (2026-09-14 fix round).
 *
 * Asserts that handleFrame({ type: 'library_changed', ... }) invalidates the
 * stale-listing queries for the named workspace: every cached entries query
 * (partial-key match on ['library', workspace_id]) AND the workspaces list
 * (its entry_count column changes on any write). Before this case existed
 * there was NO cross-tab reconciliation at all — a tab that did not perform
 * the write kept serving its stale listing until a manual reload (UAT: a
 * listing still showing files deleted 17 minutes earlier).
 *
 * Mirrors chat.task-run-status.test.ts's structure/style.
 *
 * F3 fix round (SILENT-FAILURES-rate-limits-dd25339bf.md): the invalidation
 * is now debounced (LIBRARY_CHANGED_INVALIDATE_DEBOUNCE_MS, chat.ts) rather
 * than synchronous, so a burst of frames coalesces into one flush instead of
 * one reload per frame — see chat.library-changed-burst.test.ts for the
 * burst-coalescing regression coverage itself. These two tests advance fake
 * timers past that debounce window before asserting on invalidateQueries,
 * mirroring chat.plan-status-frame.test.ts's already-proven shape.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from 'react'

import { useChatStore } from './chat'
import { queryClient } from '@/lib/queryClient'
import { libraryQueryKeys } from '@/lib/api'
import type { LibraryChangedFrame } from '@/lib/api/generated/asyncapi-types'

// Mirrors the private LIBRARY_CHANGED_INVALIDATE_DEBOUNCE_MS in chat.ts —
// kept as a local literal so this suite fails loudly if the window is ever
// widened without a matching test update.
const DEBOUNCE_MS = 1000

describe('chat handleFrame → library_changed (D-107)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    act(() => {
      vi.runOnlyPendingTimers()
    })
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('invalidates every listing query for the named workspace, and the workspaces list, once the debounce window elapses', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const frame: LibraryChangedFrame = {
      type: 'library_changed',
      workspace_id: 'ws-stale',
      path: 'notes/gone.md',
      reason: 'delete',
    }

    act(() => {
      useChatStore.getState().handleFrame(frame)
    })
    // Nothing fires synchronously — the debounce hasn't elapsed yet.
    expect(invalidateSpy).not.toHaveBeenCalled()

    act(() => {
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    // Partial-key: must cover entries AND content queries for the workspace
    // whatever folder they were listing (and both include_hidden variants).
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-stale'] })
    // The workspaces list carries entry_count — a write changes it.
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })

  it('a frame without path/reason (upload, vault create) still invalidates', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const frame: LibraryChangedFrame = {
      type: 'library_changed',
      workspace_id: 'ws-upload',
    }

    act(() => {
      useChatStore.getState().handleFrame(frame)
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-upload'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })
})
