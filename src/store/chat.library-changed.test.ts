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
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'

import { useChatStore } from './chat'
import { queryClient } from '@/lib/queryClient'
import { libraryQueryKeys } from '@/lib/api'
import type { LibraryChangedFrame } from '@/lib/api/generated/asyncapi-types'

describe('chat handleFrame → library_changed (D-107)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('invalidates every listing query for the named workspace, and the workspaces list', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const frame: LibraryChangedFrame = {
      type: 'library_changed',
      workspace_id: 'ws-stale',
      path: 'notes/gone.md',
      reason: 'delete',
    }

    useChatStore.getState().handleFrame(frame)

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

    useChatStore.getState().handleFrame(frame)

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-upload'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })
})
