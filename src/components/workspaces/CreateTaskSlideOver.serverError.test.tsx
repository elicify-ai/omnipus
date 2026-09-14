/**
 * CreateTaskSlideOver.serverError.test.tsx
 *
 * Live-UAT defect (lane L2b, scenario A-11 step 4): a 400 rejection from
 * `POST /api/v1/tasks` — e.g. GOAL-FR-021/FR-048's "a definition-of-done
 * item must be distinct from every acceptance criterion" check — rendered
 * NOTHING in the dialog. No toast, no inline message, no field highlight;
 * only the console logged the 400. This file locks in the fix: the
 * server's own validation message is always shown, routed inline next to
 * the named `criteria[N]`/`dod[N]` editor when the message identifies one,
 * otherwise into a submit-error banner — for both Create and Create & Run
 * (including the latter's PATCH-to-start step), and for non-400 failures
 * too. The dialog stays open and the user's input is never discarded.
 *
 * Per CLAUDE.md Constraint #2: the SPA does not re-implement the
 * distinctness rule — the server is the sole authority. This file only
 * checks that the server's message reaches the user; it never asserts a
 * client-side duplicate check.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'
import { fieldFromValidationError } from './taskValidationError'

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
    // isApiError/getErrorMessage/ApiError all fall through to the real
    // implementation (via `...actual`) — `fakeApiError` below constructs a
    // real `ApiError` instance, so `err instanceof ApiError` (what both
    // helpers actually check) is genuinely true, same as production.
  }
})

import { createTask, updateTask, ApiError } from '@/lib/api'

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

/** A real `ApiError` instance, exactly as `request()` would throw it. */
function fakeApiError(userMessage: string) {
  return new ApiError(400, userMessage)
}

function renderSlideOver() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-test" />
    </QueryClientProvider>,
  )
}

function addItem(inputLabel: RegExp, text: string) {
  const input = screen.getByLabelText(inputLabel)
  fireEvent.change(input, { target: { value: text } })
  const draftPanel = input.parentElement as HTMLElement
  fireEvent.click(within(draftPanel).getByRole('button', { name: /add criterion/i }))
}

const CRITERION_INPUT = /what must be true when this is done\?/i
const DOD_INPUT = /definition of done item/i

// The exact rejection body reproduced in live UAT A-11 step 4.
const DOD_RESTATES_CRITERION_MESSAGE =
  'task validation: dod[0]: "greet.py exists and prints Hello World" restates the acceptance ' +
  'criterion "greet.py exists and prints Hello World" — a definition-of-done item must be ' +
  'distinct from every acceptance criterion (GOAL-FR-021/FR-048). Say what must be TRUE of the ' +
  'finished work that the criteria do not already say'

function fillValidForm() {
  fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Greet script' } })
  fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Write greet.py.' } })
  addItem(CRITERION_INPUT, 'greet.py exists and prints Hello World')
  addItem(DOD_INPUT, 'greet.py exists and prints Hello World')
}

beforeEach(() => {
  vi.mocked(createTask).mockReset()
  vi.mocked(updateTask).mockReset()
  mockAddToast.mockReset()
})

// ── fieldFromValidationError (pure helper) ─────────────────────────────────────

describe('fieldFromValidationError', () => {
  it('identifies a dod[N] validation message', () => {
    expect(fieldFromValidationError(DOD_RESTATES_CRITERION_MESSAGE)).toBe('dod')
  })

  it('identifies a criteria[N] validation message', () => {
    expect(fieldFromValidationError('task validation: criteria[2]: text is required')).toBe(
      'criteria',
    )
  })

  it('returns null for a message naming no field', () => {
    expect(
      fieldFromValidationError('agent "builder" is not a member of workspace "proj-test"'),
    ).toBeNull()
  })

  it('returns null for a generic server error', () => {
    expect(fieldFromValidationError('The server is unavailable. Please try again in a moment.')).toBeNull()
  })
})

// ── Create ───────────────────────────────────────────────────────────────────

describe('CreateTaskSlideOver — surfaces server validation rejections (Create)', () => {
  it('renders the server dod[0] message inline and keeps the dialog open with input intact', async () => {
    vi.mocked(createTask).mockRejectedValueOnce(fakeApiError(DOD_RESTATES_CRITERION_MESSAGE))
    renderSlideOver()

    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    // The server's own message renders verbatim, inline (matches the
    // dodError paragraph already used for the client-side "add at least
    // one" check, per CLAUDE.md's "no duplicated logic" — the server
    // decided this, the SPA only displays it).
    expect(await screen.findByText(DOD_RESTATES_CRITERION_MESSAGE)).toBeInTheDocument()

    // It also still reaches the toast (unchanged existing behaviour).
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: DOD_RESTATES_CRITERION_MESSAGE, variant: 'error' }),
    )

    // Dialog stayed open (title input is still mounted and still has what
    // the user typed) and nothing was persisted.
    expect(screen.getByLabelText(/title/i)).toHaveValue('Greet script')
    expect(screen.getByLabelText(/^goal/i)).toHaveValue('Write greet.py.')
    expect(vi.mocked(createTask)).toHaveBeenCalledOnce()
  })

  it('clears the inline dod error once the user edits the DoD list', async () => {
    vi.mocked(createTask).mockRejectedValueOnce(fakeApiError(DOD_RESTATES_CRITERION_MESSAGE))
    renderSlideOver()

    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(DOD_RESTATES_CRITERION_MESSAGE)).toBeInTheDocument()

    addItem(DOD_INPUT, 'The script exits with status 0.')

    await waitFor(() =>
      expect(screen.queryByText(DOD_RESTATES_CRITERION_MESSAGE)).toBeNull(),
    )
  })

  it('renders a visible, honest message for a 5xx failure instead of staying silent', async () => {
    vi.mocked(createTask).mockRejectedValueOnce(
      fakeApiError('The server is unavailable. Please try again in a moment.'),
    )
    renderSlideOver()

    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(
      await screen.findByText(/the server is unavailable\. please try again in a moment\./i),
    ).toBeInTheDocument()
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    )
  })

  it('renders a visible message for a transport-level (network) failure', async () => {
    vi.mocked(createTask).mockRejectedValueOnce(
      fakeApiError('Network unavailable. Check your connection.'),
    )
    renderSlideOver()

    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(
      await screen.findByText(/network unavailable\. check your connection\./i),
    ).toBeInTheDocument()
  })
})

// ── Create & Run (including its PATCH-to-start step) ────────────────────────

describe('CreateTaskSlideOver — surfaces server validation rejections (Create & Run)', () => {
  it('renders the server dod[0] message inline for Create & Run and keeps the dialog open', async () => {
    vi.mocked(createTask).mockRejectedValueOnce(fakeApiError(DOD_RESTATES_CRITERION_MESSAGE))
    renderSlideOver()

    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /create & run/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    expect(await screen.findByText(DOD_RESTATES_CRITERION_MESSAGE)).toBeInTheDocument()
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
    expect(screen.getByLabelText(/title/i)).toHaveValue('Greet script')
  })

  it('renders a visible message when the create step succeeds but the start-now PATCH fails', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    vi.mocked(updateTask).mockRejectedValueOnce(
      fakeApiError('The server is unavailable. Please try again in a moment.'),
    )
    renderSlideOver()

    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /create & run/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(
      await screen.findByText(/the server is unavailable\. please try again in a moment\./i),
    ).toBeInTheDocument()
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    )
  })
})
