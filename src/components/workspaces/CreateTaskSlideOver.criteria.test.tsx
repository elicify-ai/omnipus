/**
 * CreateTaskSlideOver.criteria.test.tsx
 *
 * GOAL-FR-047 / FR-053 (create half) — acceptance criteria and definition of
 * done both become mandatory on the create form. D-B (operator decision) is
 * NOT about this file: that is the Judge's grounding-report behaviour, not
 * interface validation. This file is purely about whether the FORM will
 * accept a submission with an empty list, and whether it says which list is
 * missing — the joint delivery plan's U4 row (test-matrix items 47 and the
 * create half of 53).
 *
 * Test Matrix: item 47 "CreateTaskSlideOver blocks submit without criteria
 * and DoD", item 53 (create half) "both forms mark criteria and DoD
 * required and carry no fallback hint".
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'

// ── API mock ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: {
      list: () => ['workspaces'],
      delegation: (id: string) => ['workspaces', id, 'delegation'],
    },
    isApiError: vi.fn().mockReturnValue(false),
  }
})

import { createTask, updateTask } from '@/lib/api'

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function makeCreatedTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'new-task',
    title: 'New task',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'proj-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderSlideOver() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-test" />
    </QueryClientProvider>,
  )
}

// Add one criterion/DoD item: type into the input identified by its
// accessible name, then click the "Add criterion" button scoped to that
// same draft panel. Acceptance criteria and Definition of Done render two
// separate instances of the same editor, each with its own identically
// labelled "Add criterion" button, so the query must be scoped.
function addItem(inputLabel: RegExp, text: string) {
  const input = screen.getByLabelText(inputLabel)
  fireEvent.change(input, { target: { value: text } })
  const draftPanel = input.parentElement as HTMLElement
  fireEvent.click(within(draftPanel).getByRole('button', { name: /add criterion/i }))
}

const CRITERION_INPUT = /what must be true when this is done\?/i
const DOD_INPUT = /definition of done item/i

beforeEach(() => {
  vi.mocked(createTask).mockReset()
  vi.mocked(updateTask).mockReset()
  mockAddToast.mockReset()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CreateTaskSlideOver — acceptance criteria and DoD are required (GOAL-FR-047/FR-053)', () => {
  it('marks both Acceptance criteria and Definition of Done as required with the same asterisk Title uses', () => {
    renderSlideOver()

    const criteriaLabel = screen.getByText(/^acceptance criteria/i)
    expect(within(criteriaLabel).getByText('*')).toBeInTheDocument()

    const dodLabel = screen.getByText(/^definition of done/i)
    expect(within(dodLabel).getByText('*')).toBeInTheDocument()
  })

  it('never renders the retired D5 soft-tier empty-state sentence', () => {
    renderSlideOver()

    // GOAL-FR-053: a grep for this exact sentence must return zero hits.
    expect(
      screen.queryByText(/no criteria added — this task will be judged against its title and description/i),
    ).toBeNull()
    // The create form's replacement is a plain instruction.
    expect(screen.getAllByText(/add at least one\.?$/i).length).toBeGreaterThan(0)
  })

  it('blocks submission and names BOTH missing lists when criteria and DoD are both empty', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'No lists' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do the thing.' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/add at least one acceptance criterion\./i)).toBeInTheDocument()
    expect(await screen.findByText(/add at least one definition-of-done item\./i)).toBeInTheDocument()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })

  it('blocks submission and names only DoD when criteria is present but DoD is empty', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Half done' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do the thing.' } })
    addItem(CRITERION_INPUT, 'The update names the three partners.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/add at least one definition-of-done item\./i)).toBeInTheDocument()
    expect(screen.queryByText(/add at least one acceptance criterion\./i)).toBeNull()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })

  it('blocks submission and names only criteria when DoD is present but criteria is empty', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Half done' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do the thing.' } })
    addItem(DOD_INPUT, 'Nothing ships with a placeholder in it.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/add at least one acceptance criterion\./i)).toBeInTheDocument()
    expect(screen.queryByText(/add at least one definition-of-done item\./i)).toBeNull()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })

  it('blocks submission and reports Goal is required when the goal field is empty', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'No goal' } })
    addItem(CRITERION_INPUT, 'A criterion.')
    addItem(DOD_INPUT, 'A DoD item.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/goal is required/i)).toBeInTheDocument()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })

  it('submits with title, goal, one criterion and one DoD item, sending both lists in the body', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Complete task' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Ship the update.' } })
    addItem(CRITERION_INPUT, 'The update names the three partners.')
    addItem(DOD_INPUT, 'Nothing ships with a placeholder in it.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]

    expect(body.criteria).toHaveLength(1)
    expect(body.criteria?.[0].text).toBe('The update names the three partners.')
    expect(body.dod).toHaveLength(1)
    expect(body.dod?.[0].text).toBe('Nothing ships with a placeholder in it.')
    // The two lists never mix.
    expect(body.criteria?.[0].text).not.toBe(body.dod?.[0].text)

    // No validation errors remain once the submission succeeds.
    expect(screen.queryByText(/add at least one acceptance criterion\./i)).toBeNull()
    expect(screen.queryByText(/add at least one definition-of-done item\./i)).toBeNull()
  })

  it('clears the criteria/DoD validation error as soon as an item is added', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Fixes itself' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do the thing.' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/add at least one acceptance criterion\./i)).toBeInTheDocument()

    addItem(CRITERION_INPUT, 'Now satisfied.')

    await waitFor(() =>
      expect(screen.queryByText(/add at least one acceptance criterion\./i)).toBeNull(),
    )
  })
})
