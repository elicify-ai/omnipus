/**
 * TaskDetailPanel.planMember.test.tsx
 *
 * UAT defect A, detail-panel half. The panel's sections ran TITLE…ARTIFACTS
 * with no write-set field and no join control anywhere, so an operator who
 * hit `pkg/plan/lint.go`'s overlapping-write or join-less-convergence refusal
 * had no way to edit the two fields the refusal names — `is_join` was
 * reachable only by hand-crafting a PATCH.
 *
 * Both fields already exist on `TaskUpdateRequest.yaml` and are honoured by
 * `handleTaskPatch` (pkg/gateway/rest_tasks.go), so the fix is the control,
 * not the contract. These tests assert on the PATCH body handed to
 * `updateTask`, so a panel that renders the controls but never saves them
 * still fails.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task } from '@/lib/api'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchTaskEvidence: vi.fn().mockResolvedValue([]),
    fetchTaskVerdicts: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    stopTaskGoalLoop: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
    taskEvidenceQueryKeys: actual.taskEvidenceQueryKeys,
    taskVerdictsQueryKeys: actual.taskVerdictsQueryKeys,
    workspacesQueryKeys: actual.workspacesQueryKeys,
    plansQueryKeys: actual.plansQueryKeys,
  }
})

import { updateTask } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'W1 writes report',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderPanel(task: Task | null) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel task={task} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(updateTask).mockReset().mockResolvedValue({} as never)
  mockAddToast.mockReset()
})

describe('TaskDetailPanel — plan-member fields (write_set + is_join)', () => {
  it('hides both fields on a standalone task — neither is meaningful without a plan', async () => {
    renderPanel(makeTask({ plan_id: undefined }))

    await screen.findByText('Tags')
    expect(screen.queryByLabelText(/add a path this task writes/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('plan-member-join-checkbox')).not.toBeInTheDocument()
  })

  it('shows both fields on a plan member', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1' }))

    expect(await screen.findByLabelText(/add a path this task writes/i)).toBeInTheDocument()
    expect(screen.getByTestId('plan-member-join-checkbox')).toBeInTheDocument()
    expect(screen.getByText('Files this task writes')).toBeInTheDocument()
  })

  it('renders the task\'s existing write_set as removable chips', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md', 'src/schema.go'] }))

    const chips = await screen.findAllByTestId('write-set-chip')
    expect(chips.map((c) => c.textContent)).toEqual(['out/report.md', 'src/schema.go'])
  })

  it('PATCHes the full replacement write_set when a path is added', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md'] }))

    const input = await screen.findByLabelText(/add a path this task writes/i)
    fireEvent.change(input, { target: { value: 'src/schema.go' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({
      write_set: ['out/report.md', 'src/schema.go'],
    })
  })

  it('PATCHes the remaining write_set when a path is removed', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md', 'src/schema.go'] }))

    fireEvent.click(await screen.findByRole('button', { name: 'Remove path out/report.md' }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ write_set: ['src/schema.go'] })
  })

  it('PATCHes is_join: true when the merge-point box is ticked', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', title: 'J3 merge point' }))

    fireEvent.click(await screen.findByTestId('plan-member-join-checkbox'))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ is_join: true })
  })

  it('PATCHes is_join: false when an already-marked join member is un-ticked', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', is_join: true }))

    const box = await screen.findByTestId('plan-member-join-checkbox')
    expect(box).toHaveAttribute('data-state', 'checked')
    fireEvent.click(box)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ is_join: false })
  })

  it('reflects an existing is_join: true as a ticked box', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', is_join: true }))
    expect(await screen.findByTestId('plan-member-join-checkbox')).toHaveAttribute(
      'data-state',
      'checked',
    )
  })
})

/**
 * The save path itself, on a link that is not instant.
 *
 * `doUpdate` is not optimistic: it sets the save indicator in `onMutate` and
 * the new value only reaches the component through `invalidateQueries` in
 * `onSuccess`. Every test below drives the panel while a PATCH is STILL IN
 * FLIGHT — `updateTask` is replaced with a promise that never settles — which
 * is the state the previous tests never entered, because each of them added
 * exactly one path and then waited.
 */
describe('TaskDetailPanel — plan-member saves with a PATCH still in flight', () => {
  /** An updateTask that never resolves: the PATCH is away and has not landed. */
  function patchNeverLands() {
    vi.mocked(updateTask).mockImplementation(() => new Promise<never>(() => {}))
  }

  it('keeps the first path when a second is added before the first PATCH lands', async () => {
    // The normal authoring motion: a write set is a multi-entry list, so two
    // paths in a row is what an operator actually does. `write_set` is a FULL
    // REPLACEMENT on the wire (TaskUpdateRequest.yaml), so a second PATCH
    // computed from the pre-PATCH array deletes the first path — under a
    // green "Saved" indicator.
    patchNeverLands()
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md'] }))

    const input = await screen.findByLabelText(/add a path this task writes/i)
    const addButton = screen.getByRole('button', { name: /add path/i })

    fireEvent.change(input, { target: { value: 'pkg/a.go' } })
    fireEvent.click(addButton)
    fireEvent.change(input, { target: { value: 'pkg/b.go' } })
    fireEvent.click(addButton)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(2))
    expect(vi.mocked(updateTask).mock.calls[1][1]).toEqual({
      write_set: ['out/report.md', 'pkg/a.go', 'pkg/b.go'],
    })
    expect(screen.getAllByTestId('write-set-chip').map((c) => c.textContent)).toEqual([
      'out/report.md',
      'pkg/a.go',
      'pkg/b.go',
    ])
  })

  it('keeps a removal when a second path is removed before the first PATCH lands', async () => {
    // The same replacement hazard in the other direction: the second removal
    // must be computed against the already-shortened list, or it silently
    // resurrects the entry just deleted.
    patchNeverLands()
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['a.go', 'b.go', 'c.go'] }))

    fireEvent.click(await screen.findByRole('button', { name: 'Remove path a.go' }))
    fireEvent.click(screen.getByRole('button', { name: 'Remove path b.go' }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(2))
    expect(vi.mocked(updateTask).mock.calls[1][1]).toEqual({ write_set: ['c.go'] })
    expect(screen.getAllByTestId('write-set-chip').map((c) => c.textContent)).toEqual(['c.go'])
  })

  it('moves the merge-point box on the first click, and PATCHes exactly once', async () => {
    // Radix keeps no internal state for a checkbox whose `checked` is
    // supplied, so a box driven straight off the server value does not move
    // until the PATCH round-trips. An operator who sees nothing happen clicks
    // again — and the second PATCH flips the value back.
    patchNeverLands()
    renderPanel(makeTask({ plan_id: 'plan-1' }))

    const box = await screen.findByTestId('plan-member-join-checkbox')
    expect(box).toHaveAttribute('data-state', 'unchecked')

    fireEvent.click(box)

    // Moved on the click itself — before the PATCH is even dispatched, let
    // alone answered. This assertion is synchronous on purpose.
    expect(screen.getByTestId('plan-member-join-checkbox')).toHaveAttribute('data-state', 'checked')

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ is_join: true })
  })

  it('rolls the merge-point box back, with a stated reason, when the server refuses', async () => {
    // A refusal must READ as a refusal: the control returns to the value the
    // server actually holds AND the reason is surfaced — not a silent snap-back.
    vi.mocked(updateTask).mockRejectedValue(new Error('plan is already approved'))
    renderPanel(makeTask({ plan_id: 'plan-1' }))

    fireEvent.click(await screen.findByTestId('plan-member-join-checkbox'))

    await waitFor(() =>
      expect(screen.getByTestId('plan-member-join-checkbox')).toHaveAttribute(
        'data-state',
        'unchecked',
      ),
    )
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'plan is already approved', variant: 'error' }),
    )
  })

  it('still adopts a later server value once two saves in a row have settled', async () => {
    // The panel ignores a refetch while its OWN save is in flight — otherwise
    // a refetch that has not seen the PATCH yet would clobber the draft with
    // the pre-PATCH value. That "in flight" count therefore has to reach zero
    // again, and `mutate()` detaches the observer from the mutation it
    // supersedes (MutationObserver.mutate → removeObserver), so the FIRST of
    // two rapid saves never runs its PER-CALL callbacks. Counting there would
    // strand the count above zero and this panel would ignore every later
    // server value for the rest of the task's life — silently, since the
    // control still looks live.
    const settle: Array<() => void> = []
    vi.mocked(updateTask).mockImplementation(
      () => new Promise<never>((resolve) => { settle.push(() => resolve(undefined as never)) }),
    )

    const client = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <TaskDetailPanel task={makeTask({ plan_id: 'plan-1', write_set: ['a.go'] })} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    const input = await screen.findByLabelText(/add a path this task writes/i)
    const addButton = screen.getByRole('button', { name: /add path/i })
    fireEvent.change(input, { target: { value: 'b.go' } })
    fireEvent.click(addButton)
    fireEvent.change(input, { target: { value: 'c.go' } })
    fireEvent.click(addButton)

    await waitFor(() => expect(settle).toHaveLength(2))
    await act(async () => { settle.forEach((resolve) => resolve()) })

    // A change made elsewhere (another operator, or the planning agent) now
    // arrives through the query cache.
    rerender(
      <QueryClientProvider client={client}>
        <TaskDetailPanel
          task={makeTask({ plan_id: 'plan-1', write_set: ['server/x.go'] })}
          onClose={vi.fn()}
        />
      </QueryClientProvider>,
    )

    await waitFor(() =>
      expect(screen.getAllByTestId('write-set-chip').map((c) => c.textContent)).toEqual([
        'server/x.go',
      ]),
    )
  })

  it('restores the previous write set when the server refuses the change', async () => {
    vi.mocked(updateTask).mockRejectedValue(new Error('plan is already approved'))
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md'] }))

    fireEvent.click(await screen.findByRole('button', { name: 'Remove path out/report.md' }))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalled())
    await waitFor(() =>
      expect(screen.getAllByTestId('write-set-chip').map((c) => c.textContent)).toEqual([
        'out/report.md',
      ]),
    )
  })
})

describe('TaskDetailPanel — a write set that already contains a duplicate', () => {
  // Nothing dedupes on the way in: `pkg/task/store.go` assigns
  // `t.WriteSet = newWriteSet` verbatim and `pkg/gateway/rest_tasks.go` passes
  // the array straight through, so `create_plan` — or any non-SPA client — can
  // persist ['pkg/a.go', 'pkg/a.go']. The editor has to survive that.
  it('renders both copies without colliding React keys', async () => {
    const consoleErrors: string[] = []
    const spy = vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
      consoleErrors.push(args.map(String).join(' '))
    })
    try {
      renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['pkg/a.go', 'pkg/a.go', 'pkg/b.go'] }))
      const chips = await screen.findAllByTestId('write-set-chip')
      expect(chips.map((c) => c.textContent)).toEqual(['pkg/a.go', 'pkg/a.go', 'pkg/b.go'])
      expect(consoleErrors.join('\n')).not.toMatch(/same key/i)
    } finally {
      spy.mockRestore()
    }
  })

  it('removes only the copy whose own X was clicked', async () => {
    // Filtering by VALUE deletes every copy at once — a write set the
    // operator never asked for, PATCHed as a full replacement.
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['pkg/a.go', 'pkg/a.go', 'pkg/b.go'] }))

    const removeButtons = await screen.findAllByRole('button', { name: 'Remove path pkg/a.go' })
    expect(removeButtons).toHaveLength(2)
    fireEvent.click(removeButtons[0])

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ write_set: ['pkg/a.go', 'pkg/b.go'] })
    expect(screen.getAllByTestId('write-set-chip').map((c) => c.textContent)).toEqual([
      'pkg/a.go',
      'pkg/b.go',
    ])
  })
})
