/**
 * chat.task-run-status.test.ts — WS-handler coverage gap (pr-test #2).
 *
 * Asserts that handleFrame({ type: 'task_run_status', ... }) invalidates
 * BOTH the occurrence-overlay query (every workspace/range/tz variant, via
 * partial-key match on ['tasks', 'occurrences']) AND this task's own
 * run-history list (tasksQueryKeys.runs(task_id)) — see chat.ts's
 * `case 'task_run_status'` (ADR-050 / task-run-history-spec.md §3.8: the
 * frame fires at run OPEN and CLOSE, not just terminal, so the calendar chip
 * and the Runs list both update live). Mirrors
 * chat.notification-frame.test.ts's structure/style.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'

import { useChatStore } from './chat'
import { queryClient } from '@/lib/queryClient'
import { tasksQueryKeys } from '@/lib/api'
import type { TaskRunStatusFrame } from '@/lib/api/generated/asyncapi-types'

describe('chat handleFrame → task_run_status (ADR-050 §3.8)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('invalidates the occurrence overlay AND the task run-history list', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const frame: TaskRunStatusFrame = {
      type: 'task_run_status',
      task_id: 'task-42',
      run_id: 'run-7',
      occurrence_ms: 1_800_000_000_000,
      status: 'in_progress',
    }

    useChatStore.getState().handleFrame(frame)

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['tasks', 'occurrences'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: tasksQueryKeys.runs('task-42') })
  })

  it('invalidates on run OPEN (in_progress) as well as CLOSE (done/failed) — not terminal-only', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    for (const status of ['in_progress', 'done', 'failed'] as const) {
      invalidateSpy.mockClear()
      const frame: TaskRunStatusFrame = {
        type: 'task_run_status',
        task_id: 'task-status-cycle',
        run_id: 'run-cycle',
        status,
      }

      useChatStore.getState().handleFrame(frame)

      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['tasks', 'occurrences'] })
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: tasksQueryKeys.runs('task-status-cycle') })
    }
  })

  it('works without occurrence_ms (a manual/normal-task run, ADR-050 RD1 — nullable for a non-recurring run)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const frame: TaskRunStatusFrame = {
      type: 'task_run_status',
      task_id: 'task-manual',
      run_id: 'run-manual-1',
      status: 'done',
    }

    useChatStore.getState().handleFrame(frame)

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: tasksQueryKeys.runs('task-manual') })
  })
})
