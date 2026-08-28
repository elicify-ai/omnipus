/**
 * PlansFilterBand.test.tsx — ADR-051 D2/D3 (Plans-as-Filter band) + ADR-052
 * §6.8 (▶/■ plan action button, orange "Cancelled" marker).
 *
 * Covers:
 *   1. The leading "All tasks" tile selects (onSelectPlan(null)) and reflects
 *      `selectedPlanId === null` as its selected/aria-pressed state.
 *   2. A plan tile's body click selects that plan (onSelectPlan(id)).
 *   3. Re-clicking the ALREADY-active plan tile clears the filter
 *      (onSelectPlan(null)) — toggle-off.
 *   4. The pencil (edit) button fires onEditPlan and NEVER onSelectPlan.
 *   5. The ADR-052 ▶/■/Play action button and the ⋯ menu's Clear fire their
 *      own effects and NEVER onSelectPlan, including through the portalled
 *      confirm dialogs.
 *   6. Selected state (ring + aria-pressed) reflects `selectedPlanId`.
 *   7. ＋ New Plan fires onNewPlan.
 *   8. The orange "Cancelled" marker is textually/visually distinct from the
 *      red "Failed" marker (FR-015/US-8).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PlansFilterBand } from './PlansFilterBand'
import type { Agent, Plan, Task } from '@/lib/api'
import { PLAN_CANCELLED_COLOR, PLAN_STATE_COLORS, planDisplayColor } from '@/lib/planStateColors'

// Minimal stub so the ⋯ trigger renders its content without Radix portal
// internals (mirrors PlanCard.test.tsx's dropdown stub).
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="plan-tile-menu">{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    disabled,
    className,
  }: {
    children: React.ReactNode
    onClick?: () => void
    disabled?: boolean
    className?: string
  }) => (
    <button type="button" onClick={onClick} disabled={disabled} className={className}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

const executePlanMock = vi.fn()
const stopPlanMock = vi.fn()
const restartPlanMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    executePlan: (id: string) => executePlanMock(id),
    stopPlan: (id: string) => stopPlanMock(id),
    restartPlan: (id: string) => restartPlanMock(id),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: typeof mockAddToast }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

beforeEach(() => {
  executePlanMock.mockReset().mockResolvedValue(undefined)
  stopPlanMock.mockReset().mockResolvedValue(undefined)
  restartPlanMock.mockReset().mockResolvedValue(undefined)
  mockAddToast.mockReset()
})

// ── Fixtures ─────────────────────────────────────────────────────────────────

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

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
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
  }
}

const agents: Agent[] = [
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  { id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50, memory_enabled: true, needs_model: false },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderBand(overrides: Partial<React.ComponentProps<typeof PlansFilterBand>> = {}) {
  const onSelectPlan = vi.fn()
  const onNewPlan = vi.fn()
  const onEditPlan = vi.fn()
  const onClearPlan = vi.fn()
  const props = {
    plans: [makePlan()],
    tasks: [] as Task[],
    agents,
    selectedPlanId: null,
    onSelectPlan,
    onNewPlan,
    onEditPlan,
    onClearPlan,
    ...overrides,
  }
  const utils = render(
    <QueryClientProvider client={makeClient()}>
      <PlansFilterBand {...props} />
    </QueryClientProvider>,
  )
  return { onSelectPlan, onNewPlan, onEditPlan, onClearPlan, ...utils }
}

// ── All tasks tile ───────────────────────────────────────────────────────────

describe('PlansFilterBand — All tasks tile', () => {
  it('renders first and selects (onSelectPlan(null)) when clicked', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({ selectedPlanId: 'plan-1' })
    await user.click(screen.getByRole('button', { name: /All tasks/ }))
    expect(onSelectPlan).toHaveBeenCalledWith(null)
  })

  it('is aria-pressed when selectedPlanId is null, and not when a plan is selected', () => {
    const { rerender } = renderBand({ selectedPlanId: null })
    expect(screen.getByRole('button', { name: /All tasks/ })).toHaveAttribute('aria-pressed', 'true')

    rerender(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand
          plans={[makePlan()]}
          tasks={[]}
          agents={agents}
          selectedPlanId="plan-1"
          onSelectPlan={vi.fn()}
          onNewPlan={vi.fn()}
          onEditPlan={vi.fn()}
          onClearPlan={vi.fn()}
        />
      </QueryClientProvider>,
    )
    expect(screen.getByRole('button', { name: /All tasks/ })).toHaveAttribute('aria-pressed', 'false')
  })

  it('shows the total task count', () => {
    renderBand({
      tasks: [makeTask({ id: 't1' }), makeTask({ id: 't2' }), makeTask({ id: 't3' })],
    })
    expect(screen.getByText('3 tasks')).toBeInTheDocument()
  })

  it('renders only real plans — no ghost placeholder tiles when there are zero plans', () => {
    renderBand({ plans: [] })
    expect(screen.getByRole('button', { name: /All tasks/ })).toBeInTheDocument()
    expect(screen.queryByTestId(/^plan-filter-tile-/)).not.toBeInTheDocument()
  })
})

// ── Plan tile select / toggle ────────────────────────────────────────────────

describe('PlansFilterBand — plan tile select / toggle', () => {
  it('clicking an unselected plan tile selects it (onSelectPlan(id))', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({ plans: [makePlan({ id: 'plan-9', title: 'Payments revamp' })] })
    await user.click(screen.getByRole('button', { name: 'Payments revamp' }))
    expect(onSelectPlan).toHaveBeenCalledWith('plan-9')
  })

  it('re-clicking the ALREADY-active plan tile clears the filter (onSelectPlan(null))', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-9', title: 'Payments revamp' })],
      selectedPlanId: 'plan-9',
    })
    await user.click(screen.getByRole('button', { name: 'Payments revamp' }))
    expect(onSelectPlan).toHaveBeenCalledWith(null)
  })

  it('the selected plan tile exposes aria-pressed=true; others false', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-a', title: 'A' }), makePlan({ id: 'plan-b', title: 'B' })],
      selectedPlanId: 'plan-a',
    })
    expect(screen.getByRole('button', { name: 'A' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'B' })).toHaveAttribute('aria-pressed', 'false')
  })
})

// ── Progress / owner display ─────────────────────────────────────────────────

describe('PlansFilterBand — plan tile progress + owner', () => {
  it('computes done/total from top-level member tasks (excludes subtasks)', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-1' })],
      tasks: [
        makeTask({ id: 't1', plan_id: 'plan-1', status: 'done' }),
        makeTask({ id: 't2', plan_id: 'plan-1', status: 'in_progress' }),
        makeTask({ id: 't3', plan_id: 'plan-1', status: 'done', parent_task_id: 't1' }), // subtask — excluded
        makeTask({ id: 't4', plan_id: 'plan-2', status: 'done' }), // different plan — excluded
      ],
    })
    expect(screen.getByText('1/2')).toBeInTheDocument()
    expect(screen.getByLabelText('Progress: 1 of 2 tasks done')).toBeInTheDocument()
  })

  it("renders the owning agent's short name", () => {
    renderBand({ plans: [makePlan({ owner_agent_id: 'jim' })] })
    expect(screen.getByText('Jim')).toBeInTheDocument()
  })

  it('shows 0/0 (never NaN) when a plan has zero member tasks', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-empty' })],
      tasks: [],
    })
    expect(screen.getByText('0/0')).toBeInTheDocument()
    expect(screen.getByLabelText('Progress: 0 of 0 tasks done')).toBeInTheDocument()
    // Guard against a regression to `Math.round(NaN)` — no "NaN" anywhere.
    expect(screen.queryByText(/NaN/)).not.toBeInTheDocument()
  })
})

// ── Edit isolation (pencil never filters) ────────────────────────────────────

describe('PlansFilterBand — edit pencil isolation', () => {
  it('the pencil fires onEditPlan and NOT onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onEditPlan, onSelectPlan } = renderBand({ plans: [makePlan({ id: 'plan-x', title: 'Nightly sync' })] })
    await user.click(screen.getByRole('button', { name: 'Edit plan Nightly sync' }))
    expect(onEditPlan).toHaveBeenCalledTimes(1)
    expect(onSelectPlan).not.toHaveBeenCalled()
  })
})

// ── ADR-052 §6.8 plan action button (Execute/Stop/Play) ─────────────────────

describe('PlansFilterBand — ▶/■/Play action button (ADR-052 §6.8)', () => {
  it('shows Execute for a draft plan; confirming calls executePlan directly (never the old PUT-based approve) and NOT onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({ plans: [makePlan({ id: 'plan-x', state: 'draft' })] })
    await user.click(screen.getByRole('button', { name: 'Execute plan Launch' }))
    const dialog = screen.getByRole('alertdialog')
    expect(executePlanMock).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Execute' }))
    expect(executePlanMock).toHaveBeenCalledWith('plan-x')
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('does not show Execute for a non-draft plan', () => {
    renderBand({ plans: [makePlan({ state: 'running' })] })
    expect(screen.queryByRole('button', { name: /Execute plan/ })).not.toBeInTheDocument()
  })

  it('shows Stop for a running plan; confirming calls stopPlan but NEVER onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'running' })],
    })
    await user.click(screen.getByRole('button', { name: 'Stop plan Launch' }))
    const dialog = screen.getByRole('alertdialog')
    expect(stopPlanMock).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    expect(stopPlanMock).toHaveBeenCalledWith('plan-x')
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('Cancel dismisses the Stop confirm dialog without calling stopPlan', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({ plans: [makePlan({ state: 'running' })] })
    await user.click(screen.getByRole('button', { name: 'Stop plan Launch' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(stopPlanMock).not.toHaveBeenCalled()
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('shows Restart for a cancelled plan; confirming calls restartPlan (S4 UAT — vocabulary is Restart everywhere, not Play)', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'failed', failed_reason: 'stopped_by_user' })],
    })
    await user.click(screen.getByRole('button', { name: 'Restart plan Launch' }))
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText('Restart this plan?')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Restart' }))
    expect(restartPlanMock).toHaveBeenCalledWith('plan-x')
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('shows no action button for a genuinely-failed plan (judge_rounds_exhausted)', () => {
    renderBand({ plans: [makePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' })] })
    expect(screen.queryByRole('button', { name: /Restart plan/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Execute plan/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Stop plan/ })).not.toBeInTheDocument()
  })

  it('shows no action button for a done plan', () => {
    renderBand({ plans: [makePlan({ state: 'done' })] })
    expect(screen.queryByRole('button', { name: /Restart plan/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Execute plan/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Stop plan/ })).not.toBeInTheDocument()
  })
})

// ── S2 UAT finding — a STUCK plan (`plan_phase: awaiting_supervision`)
// rendered as plain "Running", indistinguishable from a plan making real
// progress (testers saw 4-5 plans parked at once with no signal). Reuses
// `planPhaseChip` (ADR-053 FE-2 §7), which WorkspaceGraphTab already wired up
// — this tile was the surface it was missing from.

describe('PlansFilterBand — plan-phase chip distinguishes parked from running (S2 UAT)', () => {
  it('a normally-running plan (plan_phase idle) shows plain "Running" with NO phase chip and NO explanation', () => {
    renderBand({ plans: [makePlan({ id: 'plan-run', state: 'running', plan_phase: 'idle' })] })
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.queryByTestId('plan-phase-chip-plan-run')).not.toBeInTheDocument()
    expect(screen.queryByTestId('plan-phase-explanation-plan-run')).not.toBeInTheDocument()
  })

  it('a plan parked at awaiting_supervision renders a distinguishable warning chip alongside (not instead of) "Running"', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-parked', state: 'running', plan_phase: 'awaiting_supervision' })],
    })
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByTestId('plan-phase-chip-plan-parked')).toHaveTextContent(
      'Re-planning — awaiting supervision',
    )
  })

  it('the parked chip comes with an ALWAYS-VISIBLE plain-language explanation — not a hover-only tooltip', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-parked2', state: 'running', plan_phase: 'awaiting_supervision' })],
    })
    // The tooltip this fix replaces lived on the CHIP (not the explanation
    // text below) — assert directly on the element that used to carry it.
    const chip = screen.getByTestId('plan-phase-chip-plan-parked2')
    expect(chip).not.toHaveAttribute('title')

    const explanation = screen.getByTestId('plan-phase-explanation-plan-parked2')
    // Must be genuinely visible — not merely present in the DOM with content
    // but hidden via `hidden`/`display:none` (which `getByTestId` +
    // `.textContent` alone would not catch).
    expect(explanation).toBeVisible()
    expect(explanation.textContent).toMatch(/supervisor/i)
    // Pins the RETIRED copy as gone. Corrections now work, so telling the
    // reader there is "no in-app action" and to create a new plan is false —
    // asserting only the new wording would let a revert pass silently.
    expect(explanation.textContent).not.toMatch(/no in-app action|create a new one/i)
    // Names the one REAL control available (Stop) instead of inventing UI for
    // the three corrective verbs, which have no exposed route/control yet.
    expect(explanation.textContent).toMatch(/stop this plan/i)
    expect(explanation.textContent).not.toMatch(/append a tail|supersede|targeted.retry/i)
  })

  it('a quieter info sub-phase (e.g. dispatching) renders a chip with NO explanation (only the warning phase gets one)', () => {
    renderBand({ plans: [makePlan({ id: 'plan-disp', state: 'running', plan_phase: 'dispatching' })] })
    expect(screen.getByTestId('plan-phase-chip-plan-disp')).toHaveTextContent('Dispatching')
    expect(screen.queryByTestId('plan-phase-explanation-plan-disp')).not.toBeInTheDocument()
  })

  // Swimlane-board UAT fix — a plan the engine cannot currently dispatch or
  // that has nothing in flight (`plan_phase: 'stalled'`) gets its OWN warning
  // chip + explanation, distinct from the awaiting_supervision one above
  // — testers must not see two different stuck reasons rendered identically.
  it('a stalled plan renders its own distinguishable warning chip + explanation, distinct from awaiting_supervision', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-stalled', state: 'running', plan_phase: 'stalled' })],
    })
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByTestId('plan-phase-chip-plan-stalled')).toHaveTextContent('Stalled — needs a correction')

    const explanation = screen.getByTestId('plan-phase-explanation-plan-stalled')
    expect(explanation).toBeVisible()
    expect(explanation.textContent).toMatch(/supervisor/i)
    // Pins the RETIRED copy as gone. Corrections now work, so telling the
    // reader there is "no in-app action" and to create a new plan is false —
    // asserting only the new wording would let a revert pass silently.
    expect(explanation.textContent).not.toMatch(/no in-app action|create a new one/i)
    expect(explanation.textContent).toMatch(/stop this plan/i)
    // Must never render the awaiting_supervision copy or a raw task ID.
    expect(explanation.textContent).not.toMatch(/dead end/i)
    expect(explanation.textContent).not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)
  })
})

// ── S3 UAT finding — a FAILED plan never explained why. `failed_reason` was
// on the wire but never rendered; the tile showed only "Failed" with no next
// step. US-9 Acceptance 2 (no Restart for a genuine failure) must still hold.

describe('PlansFilterBand — failed_reason rendering (S3 UAT)', () => {
  it('a genuinely-failed plan (judge_rounds_exhausted) renders its failure reason in human language', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-jre', state: 'failed', failed_reason: 'judge_rounds_exhausted' })],
    })
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByTestId('plan-failed-reason-plan-jre')).toHaveTextContent('judge rounds exhausted')
  })

  it('a genuinely-failed plan (idle_expired) renders its failure reason in human language', () => {
    renderBand({ plans: [makePlan({ id: 'plan-ie', state: 'failed', failed_reason: 'idle_expired' })] })
    expect(screen.getByTestId('plan-failed-reason-plan-ie')).toHaveTextContent('idle expired')
  })

  it('a cancelled plan (stopped_by_user) does NOT duplicate the reason — "Cancelled" already says why', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-cancelled', state: 'failed', failed_reason: 'stopped_by_user' })],
    })
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByTestId('plan-failed-reason-plan-cancelled')).not.toBeInTheDocument()
  })

  it('a failed plan with no recorded failed_reason renders no reason line (nothing to show)', () => {
    renderBand({ plans: [makePlan({ id: 'plan-nr', state: 'failed' })] })
    expect(screen.queryByTestId('plan-failed-reason-plan-nr')).not.toBeInTheDocument()
  })

  it('still offers NO Restart button for a genuinely-failed plan even though it now explains why (US-9 Acceptance 2 must still hold)', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-jre2', state: 'failed', failed_reason: 'judge_rounds_exhausted' })],
    })
    expect(screen.getByTestId('plan-failed-reason-plan-jre2')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Restart plan/ })).not.toBeInTheDocument()
  })
})

// ── ⋯ menu isolation (Clear only — never filters) ────────────────────────────

describe('PlansFilterBand — ⋯ menu (Clear only)', () => {
  it('Clear opens a confirm dialog naming the plan; confirming calls onClearPlan but NEVER onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onClearPlan, onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft', title: 'Old plan' })],
    })
    await user.click(screen.getByRole('button', { name: 'Clear' }))
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Old plan/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Clear' }))
    expect(onClearPlan).toHaveBeenCalledTimes(1)
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('Clear is disabled while the plan is running', () => {
    renderBand({ plans: [makePlan({ state: 'running' })] })
    expect(screen.getByRole('button', { name: 'Clear' })).toBeDisabled()
  })

  it('the ⋯ menu no longer offers Approve (superseded by the Execute action button)', () => {
    renderBand({ plans: [makePlan({ state: 'draft' })] })
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
  })

  it('names the ⋯ trigger after the plan', () => {
    renderBand({ plans: [makePlan({ title: 'Nightly sync' })] })
    expect(screen.getByRole('button', { name: 'Plan actions for Nightly sync' })).toBeInTheDocument()
  })
})

// ── New Plan affordance ───────────────────────────────────────────────────────

describe('PlansFilterBand — New Plan affordance', () => {
  it('fires onNewPlan when clicked', async () => {
    const user = userEvent.setup()
    const { onNewPlan } = renderBand()
    await user.click(screen.getByRole('button', { name: 'New plan' }))
    expect(onNewPlan).toHaveBeenCalledTimes(1)
  })
})

// ── Cancelled vs Failed discrimination (ADR-052 FR-015/US-8) ────────────────

describe('PlansFilterBand — orange "Cancelled" vs red "Failed" (ADR-052 FR-015/US-8)', () => {
  it('a plan Stopped by the user (failed_reason: stopped_by_user) renders "Cancelled", not "Failed"', () => {
    renderBand({ plans: [makePlan({ state: 'failed', failed_reason: 'stopped_by_user' })] })
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })

  it('a genuinely-failed plan (judge_rounds_exhausted) renders "Failed", not "Cancelled"', () => {
    renderBand({ plans: [makePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' })] })
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Cancelled')).not.toBeInTheDocument()
  })

  it('a genuinely-failed plan (idle_expired) renders "Failed", not "Cancelled"', () => {
    renderBand({ plans: [makePlan({ state: 'failed', failed_reason: 'idle_expired' })] })
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Cancelled')).not.toBeInTheDocument()
  })

  it('"Cancelled" and "Failed" render in visually distinct (non-equal) colours', () => {
    const { rerender } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'failed', failed_reason: 'stopped_by_user' })],
    })
    const cancelledColor = screen.getByText('Cancelled').style.color

    rerender(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand
          plans={[makePlan({ id: 'plan-x', state: 'failed', failed_reason: 'judge_rounds_exhausted' })]}
          tasks={[]}
          agents={agents}
          selectedPlanId={null}
          onSelectPlan={vi.fn()}
          onNewPlan={vi.fn()}
          onEditPlan={vi.fn()}
          onClearPlan={vi.fn()}
        />
      </QueryClientProvider>,
    )
    const failedColor = screen.getByText('Failed').style.color
    expect(cancelledColor).not.toBe(failedColor)
    expect(cancelledColor).not.toBe('')
    expect(failedColor).not.toBe('')
  })

  // Gate-2 finding #1 regression: PlansFilterBand.tsx:331 builds the tile's
  // state-badge tint fill as `` `${displayColor}1a` `` — a bare string
  // concat, not a CSS function call. If PLAN_CANCELLED_COLOR were ever a
  // `var(--x)` reference again, the result (`var(--color-warning)1a`) is not
  // valid CSS (`var()` doesn't accept a trailing token), so the whole
  // `backgroundColor` declaration would be silently dropped and the
  // "Cancelled" badge would render with orange text but NO tint —
  // inconsistent with the red "Failed" badge. jsdom won't reliably validate/
  // reject that invalid CSS for us, so this asserts directly on the STRING
  // the component builds, not on DOM style-parsing behavior.
  it('PLAN_CANCELLED_COLOR is a literal hex, not a var(...) reference (Gate-2 finding #1)', () => {
    expect(PLAN_CANCELLED_COLOR).not.toMatch(/^var\(/)
    expect(PLAN_CANCELLED_COLOR).toMatch(/^#[0-9a-fA-F]{6}$/)
  })

  it('both the cancelled and failed badge tints (`${displayColor}1a`) are valid 8-digit hex strings', () => {
    const cancelledTint = `${planDisplayColor(makePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))}1a`
    const failedTint = `${planDisplayColor(makePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' }))}1a`
    expect(cancelledTint).toMatch(/^#[0-9a-fA-F]{8}$/)
    expect(failedTint).toMatch(/^#[0-9a-fA-F]{8}$/)
    // And they share the exact literal-hex mechanism (PLAN_STATE_COLORS.failed
    // is also a literal hex) — one consistent tint mechanism for both badges.
    expect(PLAN_STATE_COLORS.failed).toMatch(/^#[0-9a-fA-F]{6}$/)
  })
})
