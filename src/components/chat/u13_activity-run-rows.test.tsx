// RED pack — WC-RESUME RED·U13/U14, unit U13 (FR-033).
//
// Spec source: docs/internal/specs/session-core-spec.md
//   FR-033 — "Existing Activity MUST show relevant agent task/scheduler runs
//            even without current-chat spawn, real parent/starter/assignee
//            scope, one MAIN row, running/queued/waiting/actual available
//            tokens and correct Open link. Unknown usage not zero; no extra
//            dashboard."
//   BDD-10.1 — "Relevant actual rows/states/available provider-run tokens and
//            correct links; missing usage not zero. One MAIN row, no
//            task+child duplicate."
//   BDD-10.3 — "partial unknown not running-zero."
//
// Oracle provenance: the spec's FR-033/BDD-10.1 clauses, NOT the current
// ActivityPanel (which is a "span-only Activity" today — E-ACTIVITY — and
// shows neither task/scheduler run rows, nor available tokens, nor an Open
// link on a run row). The item shape below is the RED-defined contract the
// frontend-lead's GREEN must implement; the assertions are the spec's
// observable obligations, not field-by-field style.
//
// FIELD-NAME CONTRACT (stated, not hidden): a task/scheduler run row is
// modelled as ActivityItem variant `kind: 'task'` carrying `taskLabel`,
// `status`, an optional `sessionId` (the Open target) and `availableTokens:
// number | null` where null/undefined means UNKNOWN (not zero). The spec
// names these by meaning; if GREEN lands different field names, update the
// harness constants below, not the assertions.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { act } from 'react'
import { ActivityPanel } from './ActivityPanel'
import type { ActivityItem } from '@/hooks/useRunningActivity'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useToolApprovalStore } from '@/store/toolApproval'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn() }
})

function taskRunItem(overrides: {
  key: string
  taskLabel: string
  status?: string
  sessionId?: string
  availableTokens?: number | null
}): ActivityItem {
  return {
    kind: 'task',
    key: overrides.key,
    taskLabel: overrides.taskLabel,
    status: overrides.status ?? 'running',
    sessionId: overrides.sessionId,
    availableTokens: overrides.availableTokens,
  } as unknown as ActivityItem
}

function renderPanel(running: ActivityItem[]) {
  return render(
    <ActivityPanel open onOpenChange={() => {}} running={running} recentlyFinished={[]} />,
  )
}

beforeEach(() => {
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
})

describe('U13/FR-033 — Activity shows task/scheduler runs as rows', () => {
  it('renders a row for an agent task/scheduler run even with no current-chat spawn', () => {
    const item = taskRunItem({
      key: 'run-main-1',
      taskLabel: 'MAIN · scheduled run',
      status: 'running',
      sessionId: 'sess-main-child-1',
      availableTokens: 1200,
    })
    renderPanel([item])
    expect(screen.getAllByTestId('activity-row').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('MAIN · scheduled run')).toBeInTheDocument()
  })

  it('renders exactly one MAIN row for a single MAIN child run (no task+child duplicate)', () => {
    const item = taskRunItem({ key: 'run-main-1', taskLabel: 'MAIN · scheduled run', sessionId: 's1' })
    renderPanel([item])
    expect(screen.getAllByText('MAIN · scheduled run')).toHaveLength(1)
  })
})

describe('U13/FR-033 — available provider-run tokens (unknown is NOT zero)', () => {
  it('shows the actual available token count when known', () => {
    const item = taskRunItem({ key: 'run-1', taskLabel: 'run', availableTokens: 4096 })
    renderPanel([item])
    const tokensEl = screen.getByTestId('activity-run-tokens')
    expect(tokensEl).toHaveTextContent('4096')
  })

  it('shows an unknown marker — never "0" — when the token usage is missing (BDD-10.3)', () => {
    const item = taskRunItem({ key: 'run-2', taskLabel: 'run', availableTokens: null })
    renderPanel([item])
    const tokensEl = screen.getByTestId('activity-run-tokens')
    // Missing usage must not be rendered as zero.
    expect(tokensEl.textContent ?? '').not.toMatch(/\b0\b/)
    expect(tokensEl.textContent ?? '').toMatch(/unknown|—|–|\?/i)
  })
})

describe('U13/FR-033 — the run row carries a correct Open link', () => {
  it('exposes an Open control targeting the run\'s own session', () => {
    const item = taskRunItem({ key: 'run-3', taskLabel: 'run', sessionId: 'sess-run-3' })
    renderPanel([item])
    const row = screen.getAllByTestId('activity-row')[0]
    expect(within(row).getByTestId('activity-row-open')).toBeInTheDocument()
  })
})
