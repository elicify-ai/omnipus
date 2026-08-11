/**
 * toolResultSentinels.precedence.test.tsx
 *
 * F1 (second review wave on branch fix/615-617-618-hardening): toolResultSentinels
 * .ts's own doc comment states that GenericToolCall.tsx and ToolCallBadge.tsx
 * deliberately order sentinel detection DIFFERENTLY relative to the four
 * ordinary statuses (running/success/error/cancelled):
 *
 *   - ToolCallBadge puts sentinel detection ABOVE all four ordinary statuses
 *     — a sentinel result always wins, even over 'running'/'cancelled'.
 *   - GenericToolCall puts isRunning/isCancelled ABOVE sentinel detection —
 *     a still-running or cancelled call never shows a sentinel label even if
 *     `result` already carries one (e.g. a stale/replayed partial result).
 *
 * Neither existing test suite (GenericToolCall.test.tsx, ToolCallBadge.test.tsx,
 * PermissionDenied.test.tsx) pins BOTH sides of this contract with a
 * running/cancelled status paired against a sentinel-shaped result — so a
 * refactor that accidentally flipped one caller's precedence (e.g. wiring
 * both through the shared module's `statusConfig` in the SAME chain
 * position) would pass the whole existing suite. These tests pin exactly
 * that: each assertion fails if its caller's precedence were reversed.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GenericToolCall } from './GenericToolCall'
import { ToolCallBadge } from '../ToolCallBadge'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { ToolCall } from '@/lib/api'

const delegationDenied = {
  error: 'delegation_denied' as const,
  reason: 'Scout is not in your trust set for delegation.',
  policy: 'trust_set' as const,
  tool: 'delegate',
}

describe('GenericToolCall — isRunning/isCancelled beat sentinel detection (F1)', () => {
  it('a RUNNING call whose result already carries a delegation-denied sentinel shows "Running...", not the sentinel label', () => {
    const RUNNING_STATUS: MessagePartStatus = { type: 'running' }
    render(
      <GenericToolCall
        toolName="run_diagnostic"
        result={delegationDenied}
        status={RUNNING_STATUS}
      />
    )
    expect(screen.getByText('Running...')).toBeInTheDocument()
    expect(screen.queryByText(/Delegation denied/)).toBeNull()
  })

  it('a CANCELLED call whose result already carries a delegation-denied sentinel shows "Cancelled", not the sentinel label', () => {
    const CANCELLED_STATUS: MessagePartStatus = { type: 'incomplete', reason: 'cancelled' }
    render(
      <GenericToolCall
        toolName="run_diagnostic"
        result={delegationDenied}
        status={CANCELLED_STATUS}
        isError={false}
      />
    )
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText(/Delegation denied/)).toBeNull()
  })
})

describe('ToolCallBadge — sentinel detection beats every ordinary status, including "running" (F1)', () => {
  function makeToolCall(overrides: Partial<ToolCall & { call_id: string }>): ToolCall & { call_id: string } {
    return {
      id: 'tc_precedence',
      call_id: 'tc_precedence',
      tool: 'delegate',
      params: { action: 'kill' },
      status: 'running',
      ...overrides,
    }
  }

  it('status:"running" with a delegation-denied result still renders the sentinel label, not "Running..."', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'running', result: delegationDenied })}
      />
    )
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Trust set')
    expect(badge).not.toHaveTextContent('Running...')
  })

  it('status:"cancelled" with a delegation-denied result still renders the sentinel label, not "Cancelled"', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'cancelled', result: delegationDenied })}
      />
    )
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Trust set')
    expect(badge).not.toHaveTextContent(/\bCancelled\b/)
  })
})
