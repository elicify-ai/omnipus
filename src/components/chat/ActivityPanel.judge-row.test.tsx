// ActivityPanel.judge-row.test.tsx — the judge-verdict row (ADR-049
// D2/D4/US-13/SD-C11) within the Activity Bar's slide-out panel. Companion
// to ActivityPanel.test.tsx (which covers agent/bash rows) — this file
// isolates the `kind: 'judge'` row: the per-criterion met/unmet summary, the
// model/judged-by line, and the panel-only-by-default policy (a judge row
// always renders here regardless of verbose chat — the THREAD's separate
// `shouldRenderJudgeVerdictInThread` gate, covered by
// JudgeVerdictThreadCard.test.tsx and ChatScreen's own tests, is what hides
// it from the thread by default).

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { ActivityPanel } from './ActivityPanel'
import type { JudgeActivityItem } from '@/hooks/useRunningActivity'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderJudgeVerdictInThread } from '@/lib/toolVisibility'

beforeEach(() => {
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

function makeJudgeItem(overrides: Partial<JudgeActivityItem> = {}): JudgeActivityItem {
  return {
    kind: 'judge',
    key: overrides.key ?? 'verdict_1',
    scope: overrides.scope ?? 'task',
    taskId: overrides.taskId ?? 'task-1',
    planId: overrides.planId,
    round: overrides.round ?? 1,
    met: overrides.met ?? true,
    criterionVerdicts: overrides.criterionVerdicts ?? [
      { text: 'crit-1', met: true, reason: 'tests pass' },
    ],
    model: overrides.model ?? 'z-ai/glm-5-turbo',
    judgeAgentId: overrides.judgeAgentId ?? 'judge',
    judgedAt: overrides.judgedAt ?? '2026-07-19T12:05:00Z',
    status: overrides.status ?? 'success',
    durationMs: undefined,
  }
}

describe('ActivityPanel — judge row composition', () => {
  it('renders the "Judge · <scope> round <n>" label and no duration (judge carries none)', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeJudgeItem({ scope: 'plan', round: 3 })]}
      />,
    )
    expect(screen.getByText('Judge · plan round 3')).toBeInTheDocument()
    // JudgeActivityItem.durationMs is always undefined — formatDuration(undefined) renders nothing.
    expect(screen.queryByText(/ms$/)).not.toBeInTheDocument()
  })

  it('a judge row is always expandable, even with zero criterionVerdicts', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeJudgeItem({ criterionVerdicts: [] })]}
      />,
    )
    const toggle = screen.getByRole('button', { expanded: false })
    expect(toggle).not.toBeDisabled()
    fireEvent.click(toggle)
    expect(screen.getByText('No per-criterion detail recorded.')).toBeInTheDocument()
  })
})

describe('ActivityPanel — judge row per-criterion met/unmet summary', () => {
  it('shows each criterion id and reason once expanded', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[
          makeJudgeItem({
            criterionVerdicts: [
              { text: 'crit-uuid-1', met: true, reason: 'go test output shows all passing.' },
              { text: 'crit-uuid-2', met: false, reason: '2 tests still failing.' },
            ],
          }),
        ]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.getByTestId('judge-verdict-detail')).toBeInTheDocument()
    expect(screen.getByText('crit-uuid-1')).toBeInTheDocument()
    expect(screen.getByText('go test output shows all passing.')).toBeInTheDocument()
    expect(screen.getByText('crit-uuid-2')).toBeInTheDocument()
    expect(screen.getByText('2 tests still failing.')).toBeInTheDocument()
  })

  it('collapses again on a second click, hiding the per-criterion detail', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeJudgeItem({ status: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    const toggle = screen.getByRole('button', { expanded: false })
    fireEvent.click(toggle)
    expect(screen.getByTestId('judge-verdict-detail')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { expanded: true }))
    expect(screen.queryByTestId('judge-verdict-detail')).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — judge row model/judged-by line', () => {
  it('renders "<model> · judged by <agent>" once expanded', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeJudgeItem({ model: 'anthropic/claude-3.5-haiku', judgeAgentId: 'judge' })]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.getByText('anthropic/claude-3.5-haiku · judged by judge')).toBeInTheDocument()
  })

  // Deliberately mirrors JudgeVerdictThreadCard's own intentional spec
  // deviation (spec Test 20 mentioned a spend/cost line; neither carrier —
  // JudgeVerdictFrame nor the panel's JudgeActivityItem — has a tokens/cost
  // field, see useRunningActivity.ts's JudgeActivityItem doc comment). This
  // pins the panel row to the same no-spend-line behavior, not just the
  // thread card.
  it('renders no spend/cost line — JudgeActivityItem carries no tokens/cost field', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeJudgeItem()]}
      />,
    )
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.queryByText(/spend/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/\$/)).not.toBeInTheDocument()
  })
})

describe('ActivityPanel — judge row is panel-only-by-default (renders regardless of verbose chat)', () => {
  it('renders the judge row when verbose chat is OFF (the panel is not gated by shouldRenderJudgeVerdictInThread)', () => {
    // beforeEach already sets verboseChatEnabled: false.
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeJudgeItem({ status: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText(/Judge · task round/)).toBeInTheDocument()
  })

  it('still renders the judge row when verbose chat is ON (panel visibility is unconditional either way)', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeJudgeItem({ status: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    expect(screen.getByText(/Judge · task round/)).toBeInTheDocument()
  })

  it('contrast: the THREAD gate (shouldRenderJudgeVerdictInThread) hides the same verdict by default — only the panel is unconditional', () => {
    expect(shouldRenderJudgeVerdictInThread(false)).toBe(false)
    expect(shouldRenderJudgeVerdictInThread(true)).toBe(true)
  })
})

describe('ActivityPanel — judge row status coloring', () => {
  it('a running judge row shows the running indicator', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[makeJudgeItem({ status: 'running' })]}
        recentlyFinished={[]}
      />,
    )
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'running')
    expect(screen.getByText('running')).toBeInTheDocument()
  })

  it('an errored judge row shows the failed indicator, distinct from success', () => {
    render(
      <ActivityPanel
        open
        onOpenChange={() => {}}
        running={[]}
        recentlyFinished={[makeJudgeItem({ status: 'error', met: false })]}
      />,
    )
    const row = screen.getByTestId('activity-row')
    expect(row).toHaveAttribute('data-status', 'error')
    expect(screen.getByText('failed')).toBeInTheDocument()
  })
})
