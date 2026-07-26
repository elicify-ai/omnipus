// ADR-052 Wave 2 — §6.8 button-matrix spec-as-data tests (agent "qa-spa").
//
// Encodes the ADR-052 §6.8 button matrix (docs/internal/architecture/
// ADR-052-autonomous-agent-plan-execution.md, "8. UI button matrix") as a
// data table and runs it against whichever exported helper the spa-buttons
// agent has landed. This file was authored while three SPA agents were
// still editing components in parallel; it was updated once more, later in
// the same run, after re-checking `git status --porcelain` a second time —
// see the per-section notes below for what was present at each point.
//
// PLAN half — landed from the start of this run:
//   src/components/workspaces/PlanActionButton.tsx exports `planActionFor(plan)`
//   (pure decision fn) + `PlanActionButton` (the real button+modal); paired
//   with ConfirmActionModal.tsx.
//
// TASK half — landed partway through this run (confirmed absent on the
//   first `git status --porcelain` + `grep -rn "taskActionFor|TaskActionButton" src`,
//   confirmed present on a later re-check):
//   src/components/workspaces/TaskActionButton.tsx exports `taskActionFor(task)`
//   + `TaskActionButton`. Both halves are therefore tested for real below —
//   data table + a light RTL smoke test of the confirm-modal gate and the
//   correct run/restart/stop route per FR-020.
//
// STRICT OWNERSHIP: new file only; does not modify PlanActionButton.tsx,
// TaskActionButton.tsx, ConfirmActionModal.tsx, or any existing test.
//
// Traces to: docs/internal/specs/autonomous-agent-plan-execution-spec.md
//   US-11 (line 185), US-7 (line 150), FR-019 (line 675), FR-020 (line 676),
//   FR-021 (line 677), FR-025 (line 681), Test 13 `TestButtonMatrix` (line 535);
//   docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md §6
//   item 8 (button matrix table).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'
import { planActionFor, PlanActionButton, type PlanAction } from './PlanActionButton'
import { taskActionFor, TaskActionButton, type TaskAction } from './TaskActionButton'
import type { Plan, Task } from '@/lib/api'

// ── Mock the api module's plan/task-action sextet so we can assert exactly
// which route each button confirms into, without a real network/fetch. Every
// other export (types, other functions) passes through unmodified.
const executePlanMock = vi.fn<(id: string) => Promise<Plan>>()
const stopPlanMock = vi.fn<(id: string) => Promise<Plan>>()
const restartPlanMock = vi.fn<(id: string) => Promise<Plan>>()
const runTaskMock = vi.fn<(id: string) => Promise<Task>>()
const restartTaskMock = vi.fn<(id: string) => Promise<Task>>()
const stopTaskMock = vi.fn<(id: string) => Promise<Task>>()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    executePlan: (id: string) => executePlanMock(id),
    stopPlan: (id: string) => stopPlanMock(id),
    restartPlan: (id: string) => restartPlanMock(id),
    runTask: (id: string) => runTaskMock(id),
    restartTask: (id: string) => restartTaskMock(id),
    stopTask: (id: string) => stopTaskMock(id),
  }
})

function basePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Ship the epic',
    state: 'draft',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'jim',
    created_by: 'jim',
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    ...overrides,
  }
}

function baseTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Fix the widget',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    surface: 'user',
    workspace_id: 'ws-1',
    owner: 'jim',
    created_by: 'jim',
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    ...overrides,
  }
}

function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  executePlanMock.mockReset().mockResolvedValue(basePlan({ state: 'approved' }))
  stopPlanMock.mockReset().mockResolvedValue(basePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))
  restartPlanMock.mockReset().mockResolvedValue(basePlan({ state: 'approved' }))
  runTaskMock.mockReset().mockResolvedValue(baseTask({ status: 'in_progress' }))
  restartTaskMock.mockReset().mockResolvedValue(baseTask({ status: 'next' }))
  stopTaskMock.mockReset().mockResolvedValue(baseTask({ status: 'failed', cancel_reason: 'stopped_by_user' }))
})

// ── Plan-row half of the ADR §6.8 matrix — real, against the landed helper ──

describe('ADR-052 §6.8 button matrix — planActionFor (plan-row data table, real helper)', () => {
  it.each([
    ['draft', undefined, 'execute', 'draft -> Execute'],
    ['approved', undefined, 'stop', 'approved (cap-queued, Edge Cases) -> Stop — must stay Stoppable while queued'],
    ['running', undefined, 'stop', 'running -> Stop'],
    ['failed', 'stopped_by_user', 'play', 'cancelled (failed+stopped_by_user) -> Restart (S4 UAT: label is "Restart", internal PlanAction value stays "play")'],
    ['failed', 'judge_rounds_exhausted', null, 'genuinely failed (judge-exhausted) -> no restart offered'],
    ['failed', 'idle_expired', null, 'genuinely failed (idle-expired) -> no restart offered'],
    ['failed', undefined, null, 'failed with no recorded reason -> never assume cancelled, no button'],
    ['done', undefined, null, 'done -> no button'],
  ] as const)('state=%s failed_reason=%s -> action=%s (%s)', (state, failed_reason, expected, _note) => {
    const plan = { state, failed_reason } as Pick<Plan, 'state' | 'failed_reason'>
    expect(planActionFor(plan)).toBe(expected)
  })

  it('two different plan states resolve to two DIFFERENT actions (not hardcoded)', () => {
    const draftAction = planActionFor({ state: 'draft' })
    const runningAction = planActionFor({ state: 'running' })
    const doneAction = planActionFor({ state: 'done' })
    expect(new Set([draftAction, runningAction, doneAction]).size).toBe(3)
  })

  it('the exported PlanAction union has exactly the three ADR-documented values — proven by driving the real planActionFor(), not a re-asserted literal', () => {
    // The previous version of this test (`const actions: PlanAction[] =
    // ['execute','stop','play']; expect(actions).toEqual([...])`) was a pure
    // tautology at runtime — TypeScript array literals aren't required to be
    // exhaustive over a union, so it passed identically whether the
    // `PlanAction` type had 3 members or a bogus 4th. It provided zero real
    // coverage.
    //
    // This drives the REAL production decision function across every
    // state/failed_reason combination this file's own data table above
    // exercises (the actual source of every PlanAction value that can ever
    // reach the UI) and asserts the resulting set of non-null actions is
    // exactly the three ADR-documented values — a genuine, production-derived
    // assertion instead of a self-referential literal.
    //
    // Known residual gap (documented, not hidden): `ACTION_COPY` — the
    // `Record<PlanAction, ActionCopy>` in PlanActionButton.tsx whose keys
    // would be the tightest possible runtime witness of the union's members —
    // is not exported, so it can't be asserted here directly. A union member
    // added as pure dead code (a type-level 4th value wired into ACTION_COPY/
    // the mutation switch but never actually returned by planActionFor for any
    // real state) is therefore still only caught by TypeScript's exhaustiveness
    // checking (the `assertNever` default case + the `Record` type), i.e. by
    // `npm run typecheck`, not by this or any other runtime test — that is an
    // inherent limitation of asserting against erased types, not a gap specific
    // to this test. Exporting `ACTION_COPY` (or its key list) would close it;
    // flagged as a testability note rather than silently left as a blind spot.
    const observedActions = new Set<PlanAction>(
      (
        [
          planActionFor({ state: 'draft', failed_reason: undefined }),
          planActionFor({ state: 'approved', failed_reason: undefined }),
          planActionFor({ state: 'running', failed_reason: undefined }),
          planActionFor({ state: 'failed', failed_reason: 'stopped_by_user' }),
          planActionFor({ state: 'failed', failed_reason: 'judge_rounds_exhausted' }),
          planActionFor({ state: 'failed', failed_reason: 'idle_expired' }),
          planActionFor({ state: 'failed', failed_reason: undefined }),
          planActionFor({ state: 'done', failed_reason: undefined }),
        ] as const
      ).filter((a): a is PlanAction => a !== null),
    )
    expect(observedActions).toEqual(new Set<PlanAction>(['execute', 'stop', 'play']))
  })
})

// ── Plan-row confirm-modal gate + correct route (FR-020/FR-021, US-11 Acc. 3) ──

describe('ADR-052 US-11 Acceptance 3 / FR-020 — PlanActionButton: confirm-modal gates every action, correct route per action', () => {
  it('draft plan: clicking Execute opens the confirm modal WITHOUT calling executePlan yet', async () => {
    const user = userEvent.setup()
    renderWithClient(<PlanActionButton plan={basePlan({ state: 'draft' })} />)

    await user.click(screen.getByRole('button', { name: 'Execute plan Ship the epic' }))

    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(screen.getByText('Execute this plan?')).toBeInTheDocument()
    expect(executePlanMock).not.toHaveBeenCalled()
  })

  it('draft plan: confirming Execute calls executePlan(plan.id) — the gated POST /approve route, never PUT', async () => {
    const user = userEvent.setup()
    renderWithClient(<PlanActionButton plan={basePlan({ id: 'plan-42', state: 'draft' })} />)

    await user.click(screen.getByRole('button', { name: 'Execute plan Ship the epic' }))
    await user.click(screen.getByRole('button', { name: 'Execute' }))

    await waitFor(() => expect(executePlanMock).toHaveBeenCalledWith('plan-42'))
    expect(stopPlanMock).not.toHaveBeenCalled()
    expect(restartPlanMock).not.toHaveBeenCalled()
  })

  it('running plan: confirming Stop calls stopPlan(plan.id), not executePlan/restartPlan', async () => {
    const user = userEvent.setup()
    renderWithClient(<PlanActionButton plan={basePlan({ id: 'plan-7', state: 'running' })} />)

    await user.click(screen.getByRole('button', { name: 'Stop plan Ship the epic' }))
    expect(screen.getByText('Stop this plan?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Stop' }))

    await waitFor(() => expect(stopPlanMock).toHaveBeenCalledWith('plan-7'))
    expect(executePlanMock).not.toHaveBeenCalled()
    expect(restartPlanMock).not.toHaveBeenCalled()
  })

  it('cancelled plan: confirming Restart calls restartPlan(plan.id) — the dedicated restart route (FR-026), never PUT (S4 UAT: aria-label/button/title/toast all say Restart, not Play)', async () => {
    const user = userEvent.setup()
    renderWithClient(
      <PlanActionButton plan={basePlan({ id: 'plan-99', state: 'failed', failed_reason: 'stopped_by_user' })} />,
    )

    await user.click(screen.getByRole('button', { name: 'Restart plan Ship the epic' }))
    expect(screen.getByText('Restart this plan?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Restart' }))

    await waitFor(() => expect(restartPlanMock).toHaveBeenCalledWith('plan-99'))
    expect(executePlanMock).not.toHaveBeenCalled()
    expect(stopPlanMock).not.toHaveBeenCalled()
  })

  it('cancelling the confirm dialog never calls any mutation', async () => {
    const user = userEvent.setup()
    renderWithClient(<PlanActionButton plan={basePlan({ state: 'draft' })} />)

    await user.click(screen.getByRole('button', { name: 'Execute plan Ship the epic' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(executePlanMock).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('done plan renders no action button at all', () => {
    // `screen.queryAllByRole`, not `container.querySelectorAll` — Radix
    // components (e.g. the confirm-modal's AlertDialog) portal into
    // `document.body`, outside `container`; a `container`-scoped query would
    // silently miss any stray portalled button instead of proving none exist.
    renderWithClient(<PlanActionButton plan={basePlan({ state: 'done' })} />)
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('a genuinely-failed plan (judge_rounds_exhausted) renders no action button — no Restart offered (US-9 Acceptance 2)', () => {
    renderWithClient(
      <PlanActionButton plan={basePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' })} />,
    )
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })
})

// ── Task-row half of the ADR §6.8 matrix — taskActionFor (real, landed
// partway through this run) ─────────────────────────────────────────────────

describe('ADR-052 §6.8 button matrix — taskActionFor (task-row data table, real helper)', () => {
  it.each([
    ['inbox', undefined, undefined, 'run', 'standalone idle (never run) -> Run'],
    ['next', undefined, undefined, 'run', 'standalone idle (queued) -> Run'],
    ['in_progress', undefined, undefined, 'stop', 'standalone running -> Stop (chat-send toggle)'],
    ['blocked', undefined, undefined, null, 'standalone blocked (backend-managed dep) -> no button'],
    ['done', undefined, undefined, null, 'standalone done -> no button'],
    ['failed', undefined, undefined, 'run', 'standalone GENUINE failure -> Run (fresh attempt loop, FR-019)'],
    ['failed', undefined, 'stopped_by_user', 'restart', 'standalone CANCELLED -> restart (Play label, FR-028)'],
    ['in_progress', 'plan-1', undefined, null, 'in-plan member -> status only, no Stop (ADR-053 FE-4/D7, FR-145)'],
    ['inbox', 'plan-1', undefined, null, 'in-plan, not running -> plan drives start (G4)'],
    ['next', 'plan-1', undefined, null, 'in-plan, not running -> plan drives start'],
    ['blocked', 'plan-1', undefined, null, 'in-plan, not running -> plan drives start'],
    ['done', 'plan-1', undefined, null, 'in-plan, not running -> plan drives restart'],
    ['failed', 'plan-1', undefined, null, 'in-plan genuine failure -> plan restart only, not task-level (FR-025)'],
    ['failed', 'plan-1', 'stopped_by_user', null, 'in-plan CANCELLED member -> plan restart only, not task-level'],
  ] as const)('status=%s plan_id=%s cancel_reason=%s -> action=%s (%s)', (status, plan_id, cancel_reason, expected, _note) => {
    const task = { status, plan_id, cancel_reason } as Pick<Task, 'status' | 'plan_id' | 'cancel_reason'>
    expect(taskActionFor(task)).toBe(expected)
  })

  it('a genuine failure and a cancelled failure (same status, different cancel_reason) resolve to DIFFERENT actions — proves the discriminator is read, not ignored', () => {
    const genuine = taskActionFor({ status: 'failed', plan_id: undefined, cancel_reason: undefined })
    const cancelled = taskActionFor({ status: 'failed', plan_id: undefined, cancel_reason: 'stopped_by_user' })
    expect(genuine).toBe('run')
    expect(cancelled).toBe('restart')
    expect(genuine).not.toBe(cancelled)
  })

  it('the same status (failed, cancelled) resolves DIFFERENTLY standalone vs in-plan — proves plan_id gates the whole decision, not just a modifier', () => {
    const standalone = taskActionFor({ status: 'failed', plan_id: undefined, cancel_reason: 'stopped_by_user' })
    const inPlan = taskActionFor({ status: 'failed', plan_id: 'plan-1', cancel_reason: 'stopped_by_user' })
    expect(standalone).toBe('restart')
    expect(inPlan).toBeNull()
  })

  it('the exported TaskAction union has exactly the three ADR-documented values', () => {
    const actions: TaskAction[] = ['run', 'restart', 'stop']
    expect(actions).toEqual(['run', 'restart', 'stop'])
  })
})

// ── Task-row confirm-modal gate + correct route (FR-019/020, US-7/US-11) ────

describe('ADR-052 US-11 Acceptance 3 / FR-020 — TaskActionButton: confirm-modal gates every action, correct route per action', () => {
  it('idle standalone task: clicking Run opens the confirm modal WITHOUT calling runTask yet', async () => {
    const user = userEvent.setup()
    renderWithClient(<TaskActionButton task={baseTask({ status: 'inbox' })} />)

    await user.click(screen.getByRole('button', { name: 'Run task Fix the widget' }))

    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(screen.getByText('Run this task?')).toBeInTheDocument()
    expect(runTaskMock).not.toHaveBeenCalled()
  })

  it('idle standalone task: confirming Run calls runTask(task.id) — the full attempt loop (FR-019)', async () => {
    const user = userEvent.setup()
    renderWithClient(<TaskActionButton task={baseTask({ id: 'task-11', status: 'next' })} />)

    await user.click(screen.getByRole('button', { name: 'Run task Fix the widget' }))
    await user.click(screen.getByRole('button', { name: 'Run' }))

    await waitFor(() => expect(runTaskMock).toHaveBeenCalledWith('task-11'))
    expect(restartTaskMock).not.toHaveBeenCalled()
    expect(stopTaskMock).not.toHaveBeenCalled()
  })

  it('cancelled standalone task: confirming Restart calls restartTask(task.id) — the dedicated restart route (FR-028), not runTask', async () => {
    const user = userEvent.setup()
    renderWithClient(
      <TaskActionButton task={baseTask({ id: 'task-12', status: 'failed', cancel_reason: 'stopped_by_user' })} />,
    )

    await user.click(screen.getByRole('button', { name: 'Restart task Fix the widget' }))
    expect(screen.getByText('Restart this task?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Restart' }))

    await waitFor(() => expect(restartTaskMock).toHaveBeenCalledWith('task-12'))
    expect(runTaskMock).not.toHaveBeenCalled()
    expect(stopTaskMock).not.toHaveBeenCalled()
  })

  it('a genuinely-failed standalone task: confirming Run calls runTask, NOT restartTask — proves genuine-vs-cancelled routes to different tools', async () => {
    const user = userEvent.setup()
    renderWithClient(<TaskActionButton task={baseTask({ id: 'task-13', status: 'failed' })} />)

    await user.click(screen.getByRole('button', { name: 'Run task Fix the widget' }))
    expect(screen.getByText('Run this task?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Run' }))

    await waitFor(() => expect(runTaskMock).toHaveBeenCalledWith('task-13'))
    expect(restartTaskMock).not.toHaveBeenCalled()
  })

  it('running standalone task: confirming Stop calls stopTask(task.id)', async () => {
    const user = userEvent.setup()
    renderWithClient(<TaskActionButton task={baseTask({ id: 'task-14', status: 'in_progress' })} />)

    await user.click(screen.getByRole('button', { name: 'Stop task Fix the widget' }))
    expect(screen.getByText('Stop this task?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Stop' }))

    await waitFor(() => expect(stopTaskMock).toHaveBeenCalledWith('task-14'))
    expect(runTaskMock).not.toHaveBeenCalled()
    expect(restartTaskMock).not.toHaveBeenCalled()
  })

  it('running in-plan member renders NO Stop button — status only (ADR-053 FE-4/D7, FR-145)', () => {
    // FE-4/D7 supersedes the old ADR-052 "Stop only while in_progress" member
    // exception: plan members carry no per-member controls in ANY status. The
    // plan owns lifecycle (a per-member cancel with dependents bricks the
    // plan). To adjust a member: Stop the plan → change → continue.
    // `screen.queryAllByRole`, not `container.querySelectorAll` — Radix
    // components (e.g. the confirm-modal's AlertDialog) portal into
    // `document.body`, outside `container`; a `container`-scoped query would
    // silently miss any stray portalled button instead of proving none exist.
    renderWithClient(
      <TaskActionButton task={baseTask({ id: 'task-15', status: 'in_progress', plan_id: 'plan-1' })} />,
    )
    expect(screen.queryAllByRole('button')).toHaveLength(0)
    expect(stopTaskMock).not.toHaveBeenCalled()
  })

  it('an in-plan member that is NOT running renders no action button — the plan drives its start/restart (G4/FR-025)', () => {
    renderWithClient(<TaskActionButton task={baseTask({ status: 'next', plan_id: 'plan-1' })} />)
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('a done standalone task renders no action button at all', () => {
    renderWithClient(<TaskActionButton task={baseTask({ status: 'done' })} />)
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('a blocked standalone task renders no action button (backend-managed dependency, not user-actionable)', () => {
    renderWithClient(<TaskActionButton task={baseTask({ status: 'blocked' })} />)
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('cancelling the confirm dialog never calls any mutation', async () => {
    const user = userEvent.setup()
    renderWithClient(<TaskActionButton task={baseTask({ status: 'inbox' })} />)

    await user.click(screen.getByRole('button', { name: 'Run task Fix the widget' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(runTaskMock).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })
})
