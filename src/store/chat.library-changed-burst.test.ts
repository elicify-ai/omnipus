/**
 * chat.library-changed-burst.test.ts — F3 (SILENT-FAILURES-rate-limits-
 * dd25339bf.md): "Any Library write... sends a live library_changed update,
 * and the app then reloads EVERY mounted knowledge query in the workspace."
 * A burst of these frames (e.g. a bulk trash operation deleting 54 files, or
 * several tabs each performing writes) previously fired one FULL invalidation
 * pass per frame — N reloads for N frames, each of which can itself compete
 * for the same 60-per-minute-per-workspace knowledge rate limiter budget.
 *
 * Mirrors chat.plan-status-frame.test.ts's already-proven debounce/coalesce
 * shape (14-reviewer sign-off Finding #1) — same DEBOUNCE_MS constant value,
 * same fake-timer method, same "still correct, still fires for a genuine
 * change" contract: a burst collapses into ONE flush, not zero.
 *
 * Traces to: `library_changed` (src/store/chat.ts, around the case block
 * this test's own coordinating brief cited at "around lines 4766-4768" —
 * that block coalesces via `scheduleLibraryChangedInvalidate` below).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { queryClient } from '@/lib/queryClient'
import { libraryQueryKeys } from '@/lib/api'
import type { LibraryChangedFrame } from '@/lib/api/generated/asyncapi-types'

// Mirrors the private debounce window in chat.ts — kept as a local literal
// (not imported) so this suite fails loudly if the window is ever widened
// without a matching test update, the same discipline
// chat.plan-status-frame.test.ts already uses for PLAN_STATUS_INVALIDATE_DEBOUNCE_MS.
const DEBOUNCE_MS = 1000

function makeFrame(overrides: Partial<LibraryChangedFrame> = {}): LibraryChangedFrame {
  return {
    type: 'library_changed',
    workspace_id: 'ws-burst',
    ...overrides,
  }
}

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

describe('chat handleFrame — library_changed burst coalescing (F3)', () => {
  it('THE DEFECT reproduction: 20 rapid-fire frames for the SAME workspace must not fire 20 separate invalidation passes', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      for (let i = 0; i < 20; i++) {
        useChatStore.getState().handleFrame(makeFrame({ path: `notes/file-${i}.md`, reason: 'delete' }))
      }
    })
    // Nothing fires synchronously — the debounce hasn't elapsed yet.
    expect(invalidateSpy).not.toHaveBeenCalled()

    act(() => {
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    // Exactly ONE flush's worth of calls for one workspace: the listing
    // prefix + the workspaces list — NOT 40 (20 frames x 2 calls/frame, the
    // pre-fix behaviour).
    expect(invalidateSpy).toHaveBeenCalledTimes(2)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-burst'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })

  it('coalesces a burst across MULTIPLE distinct workspaces into one flush, with one scoped invalidation per workspace', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      for (let i = 0; i < 10; i++) {
        useChatStore.getState().handleFrame(makeFrame({ workspace_id: 'ws-a' }))
        useChatStore.getState().handleFrame(makeFrame({ workspace_id: 'ws-b' }))
      }
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    // 2 scoped calls (ws-a, ws-b) + 1 shared workspaces-list call = 3 —
    // bounded regardless of the 20 total frames that arrived.
    expect(invalidateSpy).toHaveBeenCalledTimes(3)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-a'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-b'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })

  it('a second burst AFTER the first flush schedules and fires its own new pass (correctness: a real change is never dropped)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    expect(invalidateSpy).toHaveBeenCalledTimes(2)

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      useChatStore.getState().handleFrame(makeFrame())
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    expect(invalidateSpy).toHaveBeenCalledTimes(4)
  })
})
