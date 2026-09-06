/**
 * CreatePlanSlideOver.test.tsx
 *
 * ADR-049 FR-083 (US-10 AS-4): Create Plan posts goal/DoD/owner/bounds.
 * FR-084 (US-10 AS-5, SD-C4): Approve is confirm-on-success — a 400 lists
 * per-task errors inline and does NOT close/optimistically transition.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreatePlanSlideOver } from './CreatePlanSlideOver'
import { ApiError } from '@/lib/api-error'
import type { Plan } from '@/lib/api'

// jsdom doesn't implement scrollIntoView — Radix Select's Viewport calls it
// (unconditionally, via an effect) to scroll the selected item into view on
// open. Needed only because these tests actually open+select in the Owner
// agent SmartSelect (same gap noted in CreateTaskSlideOver.test.tsx /
// date-time-picker.test.tsx for the same underlying Radix Select).
beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'jim', name: 'Jim', type: 'core', default: false },
    ]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createPlan: vi.fn(),
    updatePlan: vi.fn(),
    // ADR-052 G2: the component calls executePlan (POST /approve), not the
    // deprecated approvePlan alias — mock the name it actually imports.
    executePlan: vi.fn(),
    plansQueryKeys: { list: (id: string) => ['plans', id] },
  }
})

import { createPlan, executePlan, updatePlan } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: (selector?: (s: { username: string | null }) => unknown) => {
    const store = { username: 'alice' }
    return selector ? selector(store) : store
  },
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
    state: 'draft',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderSlideOver(props: Partial<React.ComponentProps<typeof CreatePlanSlideOver>> = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreatePlanSlideOver
        open
        onOpenChange={vi.fn()}
        workspaceId="ws-1"
        {...props}
      />
    </QueryClientProvider>,
  )
}

// Owner agent picker is a plain (<=5 item) SmartSelect → Radix Select, whose
// trigger carries `aria-label="Owner agent"` directly (SmartSelect's required
// `ariaLabel` prop) — no DOM-traversal hack needed, unlike pickers elsewhere
// in the app that rely on a sibling Label's text. The trigger is `disabled`
// while `useWorkspaceTeamIds`'s delegation-graph query is still in flight
// (mocked here to reject via `fetchWorkspaceDelegation`), so a click fired
// before it settles would silently no-op — wait for it to become enabled
// first (mirrors CreateTaskSlideOver.test.tsx's `openAgentPicker`).
async function openOwnerPicker(): Promise<HTMLElement> {
  const trigger = screen.getByRole('combobox', { name: /^owner agent$/i })
  await waitFor(() => expect(trigger).not.toBeDisabled())
  fireEvent.click(trigger)
  return trigger
}

async function selectOwner(name: RegExp) {
  await openOwnerPicker()
  const option = await screen.findByRole('option', { name })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

beforeEach(() => {
  vi.mocked(createPlan).mockReset()
  vi.mocked(updatePlan).mockReset()
  vi.mocked(executePlan).mockReset()
  mockAddToast.mockReset()
})

describe('CreatePlanSlideOver — create', () => {
  it('requires a title', async () => {
    renderSlideOver()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('posts title/goal/owner/bounds on Create', async () => {
    vi.mocked(createPlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    fireEvent.change(screen.getByLabelText(/^goal$/i), { target: { value: 'Ship it' } })
    await selectOwner(/^jim$/i)

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(createPlan).toHaveBeenCalledOnce())
    const body = vi.mocked(createPlan).mock.calls[0][0]
    expect(body.title).toBe('v1.0 Launch')
    expect(body.goal).toBe('Ship it')
    expect(body.workspace_id).toBe('ws-1')
    expect(body.owner_agent_id).toBe('jim')
  })

  // S2 UAT finding: owner_agent_id is server-required (400 invalid
  // owner_agent_id) but the field previously had no client-side validation at
  // all and defaulted to the unselected '__none__' sentinel — a first-time
  // Create click fired a request doomed to 400, then (S3 finding) retried it
  // up to 4×, and the resulting error toast rendered underneath the
  // slide-over footer (invisible on screen). This proves the fix: the
  // request never fires, and the error is visible inline next to the field.
  it('requires an owner agent — shows a visible, field-specific error and fires no request', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/owner agent is required/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('clears the owner-agent error once an owner is selected', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(/owner agent is required/i)).toBeInTheDocument()

    await selectOwner(/^jim$/i)

    await waitFor(() => expect(screen.queryByText(/owner agent is required/i)).toBeNull())
  })

  it('shows both Title and Owner errors together when both are left empty (batched validation)', async () => {
    renderSlideOver()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(await screen.findByText(/owner agent is required/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('shows an error toast (and does not close) when Create fails — e.g. the workspace-route 405', async () => {
    const onOpenChange = vi.fn()
    vi.mocked(createPlan).mockRejectedValueOnce(
      new ApiError(405, 'method not allowed', { body: JSON.stringify({ error: 'method not allowed' }) }),
    )
    renderSlideOver({ onOpenChange })

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    await selectOwner(/^jim$/i)
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringMatching(/method not allowed/i) }),
      ),
    )
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })
})

describe('CreatePlanSlideOver — Approve (SD-C4 confirm-on-success)', () => {
  it('shows the Approve action only for a draft plan', () => {
    renderSlideOver({ plan: makePlan({ state: 'running' }) })
    expect(screen.queryByRole('button', { name: /^approve$/i })).toBeNull()
  })

  it('a 400 with per-task errors lists them inline and does not close the slide-over', async () => {
    const onOpenChange = vi.fn()
    const body = JSON.stringify({
      task_errors: [{ task_id: 't1', title: 'Write report', reason: 'missing acceptance criteria' }],
    })
    vi.mocked(executePlan).mockRejectedValueOnce(new ApiError(400, 'Bad request', { body }))

    renderSlideOver({ plan: makePlan({ state: 'draft' }), onOpenChange })

    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }))

    expect(await screen.findByTestId('plan-approve-task-errors')).toHaveTextContent('Write report: missing acceptance criteria')
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('a successful Approve closes the slide-over and toasts', async () => {
    const onOpenChange = vi.fn()
    vi.mocked(executePlan).mockResolvedValueOnce(makePlan({ state: 'approved' }) as never)

    renderSlideOver({ plan: makePlan({ state: 'draft' }), onOpenChange })
    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
  })
})

// S2 UAT finding: the Goal textarea allowed 4000 chars client-side while the
// SERVER caps goals at 2000 (pkg/plan/plan.go maxPlanGoalRunes) — a
// 2500-char goal was silently accepted by the UI and only rejected on submit
// with "plan validation: goal must be 2000 characters or fewer". These tests
// pin the textarea's real cap and prove truncation is never silent (S3).
describe('CreatePlanSlideOver — character caps are visible, not silent (S3/S4 UAT)', () => {
  it('caps the Goal textarea at the real server limit (2000), not the old 4000', () => {
    renderSlideOver()
    const goal = screen.getByLabelText(/^goal$/i) as HTMLTextAreaElement
    expect(goal.maxLength).toBe(2000)
  })

  it('caps the Title input at the real server limit (200)', () => {
    renderSlideOver()
    const title = screen.getByLabelText(/^title/i) as HTMLInputElement
    expect(title.maxLength).toBe(200)
  })

  it('shows a live character counter for Title that flips to a "max length reached" notice at the cap', () => {
    renderSlideOver()
    const title = screen.getByLabelText(/^title/i)

    fireEvent.change(title, { target: { value: 'short' } })
    expect(screen.getByText('5/200')).toBeInTheDocument()
    expect(screen.queryByText(/max length reached/i)).toBeNull()

    fireEvent.change(title, { target: { value: 'x'.repeat(200) } })
    expect(screen.getByText(/200\/200/)).toBeInTheDocument()
    expect(screen.getByText(/max length reached/i)).toBeInTheDocument()
  })

  it('shows a live character counter for Goal that flips to a "max length reached" notice at the cap', () => {
    renderSlideOver()
    const goal = screen.getByLabelText(/^goal$/i)

    fireEvent.change(goal, { target: { value: 'short goal' } })
    expect(screen.getByText('10/2000')).toBeInTheDocument()
    expect(screen.queryByText(/max length reached/i)).toBeNull()

    fireEvent.change(goal, { target: { value: 'y'.repeat(2000) } })
    expect(screen.getByText(/2000\/2000/)).toBeInTheDocument()
    expect(screen.getByText(/max length reached/i)).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// I8: the edit form must not destroy what it did not load.
//
// This form is used for BOTH create and edit, and it renders a strict subset of
// what a Plan carries — two of the four `bounds` overrides, and nothing else.
// Every one of the tests below asserts a PROPERTY of the request payload the
// component actually builds, never that a function was called.
//
// The server merges `PlanUpdateRequest.bounds` field-by-field: a field present
// in the request is written, a field ABSENT keeps its stored value
// (contracts/components/schemas/PlanUpdateRequest.yaml). `mergeBounds` below
// mirrors exactly that rule, so these tests can assert the stored outcome an
// operator would actually observe rather than the shape of the wire body.
// ─────────────────────────────────────────────────────────────────────────────

/** The stored bounds of a plan carrying overrides this form does NOT render. */
const STORED_BOUNDS: Record<string, number> = {
  plan_judge_max_rounds: 50,
  idle_expiry_days: 14,
  supervision_turn_timeout_seconds: 900,
  supervision_max_attempts: 5,
}

/** Mirrors the documented server-side field-by-field merge of `bounds`. */
function mergeBounds(
  stored: Record<string, number>,
  requested: Record<string, number> | undefined,
): Record<string, number> {
  return { ...stored, ...(requested ?? {}) }
}

function planWithBounds(overrides: Partial<Plan> = {}): Plan {
  return makePlan({
    goal: 'Ship it',
    bounds: STORED_BOUNDS as NonNullable<Plan['bounds']>,
    ...overrides,
  })
}

/** The body the component handed to `updatePlan`, as a plain record. */
function lastUpdateBody(): Record<string, unknown> {
  return vi.mocked(updatePlan).mock.calls[0][1] as unknown as Record<string, unknown>
}

const PROSE_CRITERION = {
  kind: 'prose',
  text: 'CI is green',
  author: { kind: 'user', id: 'alice' },
  status: 'pending',
} as unknown as NonNullable<Plan['dod']>[number]

describe('CreatePlanSlideOver — an edit never destroys bounds the form does not render', () => {
  it('editing ONLY the title leaves every supervision override at its stored value', async () => {
    vi.mocked(updatePlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver({ plan: planWithBounds() })

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'Launch v2' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    const body = lastUpdateBody()

    // The property that matters: after the server applies this request, the
    // two overrides the form cannot see are still exactly what they were.
    const after = mergeBounds(STORED_BOUNDS, body.bounds as Record<string, number> | undefined)
    expect(after.supervision_turn_timeout_seconds).toBe(900)
    expect(after.supervision_max_attempts).toBe(5)
    // …and the two it CAN see are untouched too, because they were untouched.
    expect(after.plan_judge_max_rounds).toBe(50)
    expect(after.idle_expiry_days).toBe(14)

    expect(body.title).toBe('Launch v2')
  })

  it('changing one rendered bound writes that bound and nothing else', async () => {
    vi.mocked(updatePlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver({ plan: planWithBounds() })

    fireEvent.change(screen.getByLabelText(/plan judge max rounds/i), { target: { value: '30' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    const after = mergeBounds(
      STORED_BOUNDS,
      lastUpdateBody().bounds as Record<string, number> | undefined,
    )
    expect(after).toEqual({
      plan_judge_max_rounds: 30,
      idle_expiry_days: 14,
      supervision_turn_timeout_seconds: 900,
      supervision_max_attempts: 5,
    })
  })

  it('a create with no bounds input still produces a valid request', async () => {
    vi.mocked(createPlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    await selectOwner(/^jim$/i)
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(createPlan).toHaveBeenCalledOnce())
    const body = vi.mocked(createPlan).mock.calls[0][0] as unknown as Record<string, unknown>

    // Valid = every `required` field of PlanCreateRequest is present and
    // non-empty, and `bounds` is absent rather than an empty object.
    expect(body.workspace_id).toBe('ws-1')
    expect(body.title).toBe('v1.0 Launch')
    expect(body.owner_agent_id).toBe('jim')
    expect(body.bounds).toBeUndefined()
  })
})

describe('CreatePlanSlideOver — an edit sends only what actually changed', () => {
  // handlePlanPut freezes `dod` and `owner_agent_id` once a plan leaves draft,
  // and the freeze is a PRESENCE check (`req.Dod != nil || req.OwnerAgentId !=
  // nil`), not a value comparison. The form used to send `owner_agent_id` on
  // every save, so editing the title of any approved/running/done/failed plan
  // 409'd unconditionally.
  it('editing the title of a NON-DRAFT plan omits the frozen fields, so the freeze cannot trip', async () => {
    vi.mocked(updatePlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver({ plan: planWithBounds({ state: 'running', dod: [PROSE_CRITERION] }) })

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'Launch v2' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    const body = lastUpdateBody()
    expect(Object.prototype.hasOwnProperty.call(body, 'owner_agent_id')).toBe(false)
    expect(Object.prototype.hasOwnProperty.call(body, 'dod')).toBe(false)
    expect(body.title).toBe('Launch v2')
  })

  // ADR-074 D5.1 / judgment-first spec US-7 S3 (TDD test 20): an edit that
  // never touches the criteria editor must not re-assert `dod` — even on a
  // DRAFT plan where the freeze cannot trip. The pin matters because the
  // editor emits object-literal criteria while stored criteria carry
  // server-set fields (`id`); `deepEqual` on the untouched form state is what
  // keeps an incidental title edit from rewriting the Definition of Done.
  it('editing only the title of a DRAFT plan with a DoD sends no dod PATCH (untouched criteria)', async () => {
    vi.mocked(updatePlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver({ plan: planWithBounds({ state: 'draft', dod: [PROSE_CRITERION] }) })

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'Launch v2' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    const body = lastUpdateBody()
    expect(Object.prototype.hasOwnProperty.call(body, 'dod')).toBe(false)
    expect(body.title).toBe('Launch v2')
  })

  it('clearing the Goal actually clears it — the request carries an explicit empty string', async () => {
    vi.mocked(updatePlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver({ plan: planWithBounds() })

    fireEvent.change(screen.getByLabelText(/^goal$/i), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    // The backend patch is presence-checked (`if patch.Goal != nil`), so an
    // omitted key is a silent no-op. `''` is what actually clears the goal.
    expect(lastUpdateBody().goal).toBe('')
  })

  it('deleting every DoD criterion actually clears it — the request carries an explicit empty array', async () => {
    vi.mocked(updatePlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver({ plan: planWithBounds({ state: 'draft', dod: [PROSE_CRITERION] }) })

    fireEvent.click(screen.getByRole('button', { name: /remove criterion CI is green/i }))
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(updatePlan).toHaveBeenCalledOnce())
    expect(lastUpdateBody().dod).toEqual([])
  })

  it('Save on an untouched plan fires no request and says so, instead of toasting a change that never happened', async () => {
    const onOpenChange = vi.fn()
    renderSlideOver({ plan: planWithBounds(), onOpenChange })

    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'No changes to save' }),
      ),
    )
    expect(updatePlan).not.toHaveBeenCalled()
    expect(mockAddToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Plan updated' }),
    )
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})

describe('CreatePlanSlideOver — bounds inputs fail loudly, never silently', () => {
  it('blanking an existing override is refused inline rather than silently ignored by the merge', async () => {
    renderSlideOver({ plan: planWithBounds() })

    fireEvent.change(screen.getByLabelText(/plan judge max rounds/i), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText(/max rounds can be changed but not cleared here/i)).toBeInTheDocument()
    // Without this guard the save would succeed, toast "Plan updated", and
    // leave the 50 the user just deleted in place — the merge keeps an absent
    // field's stored value.
    expect(updatePlan).not.toHaveBeenCalled()
    expect(mockAddToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Plan updated' }),
    )
  })

  it('a fractional bounds value is rejected, not silently truncated to an integer', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    await selectOwner(/^jim$/i)
    fireEvent.change(screen.getByLabelText(/idle expiry days/i), { target: { value: '3.5' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    // The previous reader was `parseInt`, which turned "3.5" into 3 and
    // submitted it as though the operator had typed it.
    expect(await screen.findByText(/idle-expiry days must be a whole number of 1 or more/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('a below-minimum bounds value is rejected inline, not sent to be 400d behind the footer', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    await selectOwner(/^jim$/i)
    fireEvent.change(screen.getByLabelText(/plan judge max rounds/i), { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/max rounds must be a whole number of 1 or more/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })
})
