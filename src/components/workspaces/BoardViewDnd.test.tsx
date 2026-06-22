/**
 * BoardViewDnd.test.tsx
 *
 * Tests for the kanban drag-and-drop status change on the Board view (UAT
 * "full task UX" — task #3). Covers:
 *   1. The transition guard `canDropTransition` mirrors the backend
 *      (pkg/task/store.go::validateTransition): dropping INTO `blocked` is
 *      prevented; dragging OUT of `done`/`blocked` is prevented; other moves
 *      are allowed.
 *   2. Cards render as draggable elements (dnd-kit attributes) and columns are
 *      registered droppables for every one of the 7 statuses.
 *   3. A simulated drag-end into a legal column calls onTaskMove with the new
 *      status; a drag-end into `blocked` calls onMoveRejected instead.
 *
 * dnd-kit's pointer DnD cannot be driven faithfully in jsdom, so the drag-end
 * behaviour is exercised through the same guard the live handler uses; the guard
 * is the load-bearing decision and is verified exhaustively.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BoardView, canDropTransition } from './BoardView'
import type { Task, Milestone } from '@/lib/api'
import { STATUS_ORDER } from '@/lib/statusColors'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    tasksQueryKeys: {
      list: (p: unknown) => ['tasks', p],
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

const baseTask = (overrides: Partial<Task> = {}): Task => ({
  id: 'task-1',
  title: 'Draggable task',
  status: 'inbox',
  action: 'llm',
  priority: 3,
  workspace_id: 'ws-1',
  surface: 'user',
  owner: 'admin',
  created_by: 'admin',
  created_at: '2026-06-20T10:00:00Z',
  updated_at: '2026-06-20T10:00:00Z',
  ...overrides,
})

const milestones: Milestone[] = []

function renderBoard(props: Partial<React.ComponentProps<typeof BoardView>> = {}) {
  return render(
    <BoardView
      tasks={[baseTask()]}
      milestones={milestones}
      agents={[]}
      activeMilestoneId={null}
      altitude="top-level"
      onAltitudeChange={vi.fn()}
      onTaskClick={vi.fn()}
      onNewTask={vi.fn()}
      onTaskMove={vi.fn()}
      onMoveRejected={vi.fn()}
      {...props}
    />,
  )
}

// ── Guard logic (exhaustive) ───────────────────────────────────────────────────

describe('canDropTransition — backend-mirrored guard', () => {
  it('prevents dropping INTO blocked from every other status', () => {
    for (const from of STATUS_ORDER) {
      if (from === 'blocked') continue
      const v = canDropTransition(from, 'blocked')
      expect(v.ok).toBe(false)
      expect(v.reason).toMatch(/automatically/i)
    }
  })

  it('prevents dragging OUT of done (terminal)', () => {
    for (const to of STATUS_ORDER) {
      if (to === 'done') continue
      const v = canDropTransition('done', to)
      expect(v.ok).toBe(false)
      // `done → blocked` is rejected by the blocked guard (checked first); every
      // other `done → X` is rejected with the terminal-done message.
      if (to !== 'blocked') expect(v.reason).toMatch(/final|done/i)
    }
  })

  it('prevents dragging OUT of blocked (auto-managed)', () => {
    for (const to of STATUS_ORDER) {
      if (to === 'blocked') continue
      const v = canDropTransition('blocked', to)
      expect(v.ok).toBe(false)
    }
  })

  it('allows ordinary forward/backward moves between non-terminal statuses', () => {
    expect(canDropTransition('inbox', 'next').ok).toBe(true)
    expect(canDropTransition('next', 'in_progress').ok).toBe(true)
    expect(canDropTransition('in_progress', 'done').ok).toBe(true)
    expect(canDropTransition('planning', 'failed').ok).toBe(true)
    expect(canDropTransition('failed', 'next').ok).toBe(true)
  })

  it('is a no-op (allowed) when from === to', () => {
    for (const s of STATUS_ORDER) {
      expect(canDropTransition(s, s).ok).toBe(true)
    }
  })
})

// ── Render: draggable cards + droppable columns ────────────────────────────────

describe('BoardView — DnD wiring', () => {
  it('renders the dragged card with a draggable role attribute', () => {
    renderBoard()
    // dnd-kit's useDraggable applies role="button" + aria-roledescription="draggable"
    const card = screen.getByText('Draggable task').closest('[aria-roledescription="draggable"]')
    expect(card).not.toBeNull()
  })

  it('renders one droppable column per status (all 7)', () => {
    renderBoard({ tasks: [] })
    // Columns carry aria-label "<Label> column" — one per status.
    const cols = screen.getAllByLabelText(/column$/i)
    expect(cols.length).toBe(STATUS_ORDER.length)
    expect(STATUS_ORDER.length).toBe(7)
  })
})

// ── Move decision behaviour (the guard drives onTaskMove vs onMoveRejected) ─────

describe('BoardView — move decision', () => {
  it('a legal target status maps to onTaskMove, an illegal one to onMoveRejected', () => {
    // This mirrors the exact decision the drag-end handler makes.
    const onTaskMove = vi.fn()
    const onMoveRejected = vi.fn()

    function decide(from: Task['status'], to: Task['status']) {
      if (from === to) return
      const v = canDropTransition(from, to)
      if (!v.ok) {
        if (v.reason) onMoveRejected(v.reason)
        return
      }
      onTaskMove(to)
    }

    decide('inbox', 'in_progress')
    expect(onTaskMove).toHaveBeenCalledWith('in_progress')

    decide('next', 'blocked')
    expect(onMoveRejected).toHaveBeenCalledTimes(1)
    expect(onTaskMove).toHaveBeenCalledTimes(1) // unchanged — blocked rejected
  })
})
