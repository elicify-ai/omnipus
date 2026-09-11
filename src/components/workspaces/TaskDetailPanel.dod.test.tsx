/**
 * TaskDetailPanel.dod.test.tsx
 *
 * GOAL-FR-054: the Definition-of-Done group. Test 54 of the goal-entity-spec
 * TDD plan ("panel passes dod to CriteriaVerdictList and the DoD group
 * renders"), extended to also cover the EDIT half U5 ships alongside it
 * (C-81): a `<Field label="Definition of Done">` wrapping
 * `DefinitionOfDoneEditor`, autosaving via `doUpdate({ dod })`.
 *
 * Zero new rendering code lives inside CriteriaVerdictList.tsx for this —
 * it already accepted a `dod` prop and already rendered a distinctly
 * labelled "Definition of Done" verdict group (C-42); the panel simply
 * never passed it before this wave.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task, AcceptanceCriterion, JudgeVerdict } from '@/lib/api'

beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

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

function makeCriterion(overrides: Partial<AcceptanceCriterion> = {}): AcceptanceCriterion {
  return {
    id: 'crit-1',
    kind: 'prose',
    judgment: 'boolean',
    text: 'Default criterion',
    author: { kind: 'user', id: 'alice' },
    status: 'pending',
    ...overrides,
  }
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Ship the release notes',
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

beforeEach(async () => {
  const api = await import('@/lib/api')
  vi.mocked(api.updateTask).mockReset().mockResolvedValue({} as never)
  mockAddToast.mockReset()
})

describe('TaskDetailPanel — Definition of Done group (GOAL-FR-054, C-81)', () => {
  it('renders a "Definition of Done" Field wrapping the DoD editor', async () => {
    renderPanel(makeTask({ dod: [makeCriterion({ id: 'dod-1', text: 'Nothing ships with a placeholder' })] }))
    // The seeded DoD item legitimately renders TWICE — once editable
    // (DefinitionOfDoneEditor) and once in CriteriaVerdictList's own DoD
    // verdict row (the `dod` prop reaches both, by design).
    await waitFor(() => {
      expect(screen.getAllByText('Nothing ships with a placeholder').length).toBeGreaterThanOrEqual(2)
    })
    // "Definition of Done" renders TWICE by design (C-81): once as the
    // Field's own small-caps section eyebrow, once as
    // DefinitionOfDoneEditor's own internal Label (U1's thin wrapper, not
    // this wave's to alter) — both must be present, not merged into one.
    expect(screen.getAllByText(/^definition of done/i).length).toBeGreaterThanOrEqual(2)
    // The editor's own standing-gates helper line proves it's the real
    // DefinitionOfDoneEditor, not a hand-rolled substitute.
    expect(screen.getByText(/standing gates, judged on every attempt/i)).toBeInTheDocument()
  })

  it('the dod prop reaches CriteriaVerdictList: a met DoD verdict renders in its own labelled group', async () => {
    const task = makeTask({
      criteria: [makeCriterion({ id: 'crit-1', text: 'Outcome criterion', status: 'met' })],
      dod: [makeCriterion({ id: 'dod-1', text: 'No secrets leak', status: 'met' })],
    })
    const verdicts: JudgeVerdict[] = [
      {
        id: 'verdict-1',
        scope: 'task',
        task_id: 'task-1',
        round: 1,
        met: true,
        per_criterion: [
          { criterion_id: 'crit-1', met: true, reason: 'The outcome is achieved.' },
          { criterion_id: 'dod-1', met: true, reason: 'grep found zero matches.' },
        ],
        model: 'z-ai/glm-5-turbo',
        judged_at: '2026-09-11T00:00:00Z',
        judge_agent_id: 'judge',
      },
    ]
    const { fetchTaskVerdicts } = await import('@/lib/api')
    vi.mocked(fetchTaskVerdicts).mockResolvedValueOnce(verdicts)

    renderPanel(task)

    // The verdict-rendering "Definition of Done" subheading, distinct from
    // the editor Field above (data-testid identifies it unambiguously).
    // Wait for the ASYNC verdicts query to resolve and its reason text to
    // land — the testid itself renders immediately (pending state), before
    // the query settles, so asserting on it too early would pass vacuously.
    await waitFor(() => {
      expect(screen.getByTestId('criteria-verdict-dod')).toHaveTextContent('grep found zero matches.')
    })
  })

  it('editing the DoD list autosaves via doUpdate({ dod }) — not folded into `criteria`', async () => {
    const { updateTask } = await import('@/lib/api')
    const task = makeTask({
      criteria: [makeCriterion({ id: 'crit-1', text: 'Existing criterion' })],
      dod: [makeCriterion({ id: 'dod-1', text: 'Existing DoD item' })],
    })
    renderPanel(task)

    // Wait for the editor's own draft input to be ready — unambiguous,
    // unlike the seeded item's text (which legitimately renders twice: once
    // editable, once in CriteriaVerdictList's verdict row).
    const input = await screen.findByLabelText(/definition of done item/i)
    fireEvent.change(input, { target: { value: 'A second DoD item' } })
    const draftPanel = input.parentElement as HTMLElement
    fireEvent.click(within(draftPanel).getByRole('button', { name: /add criterion/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled())
    const [calledId, data] = vi.mocked(updateTask).mock.calls[0]
    expect(calledId).toBe('task-1')
    expect(data.dod).toHaveLength(2)
    expect(data.dod?.[1]).toEqual(expect.objectContaining({ text: 'A second DoD item' }))
    expect(data.criteria).toBeUndefined()
  })
})
