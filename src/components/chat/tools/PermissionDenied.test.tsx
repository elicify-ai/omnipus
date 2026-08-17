// PermissionDenied sentinel rendering — issue #618.
//
// Before this fix, permission_denied had NO SPA detector at all: the payload
// (already schema-valid JSON from the backend fix) fell through to the
// generic plain-result JSON dump instead of the distinct, human-readable
// treatment its two siblings (delegation_denied, file_exists) already get.
// Mirrors GenericToolCall.test.tsx's "file-exists refusal sentinel" and
// ToolCallBadge.test.tsx's "delegation denied" describe blocks — a new file
// per this task's SPA test-ownership split (a sibling agent owns the
// existing GenericToolCall.test.tsx / ToolCallBadge.test.tsx files).

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { GenericToolCall } from './GenericToolCall'
import { isPermissionDenied } from './toolResultSentinels'
import { ToolCallBadge } from '../ToolCallBadge'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { ToolCall } from '@/lib/api'

const COMPLETE_STATUS: MessagePartStatus = { type: 'complete' }

type ToolCallWithId = ToolCall & { call_id: string }

function makeToolCall(overrides: Partial<ToolCallWithId>): ToolCallWithId {
  return {
    id: 'tc_1',
    call_id: 'tc_1',
    tool: 'write_file',
    params: { path: '/w/a.txt' },
    status: 'running',
    ...overrides,
  }
}

const permanentDenial = {
  error: 'permission_denied' as const,
  message: 'Access to this path is denied by filesystem policy.',
  tool: 'write_file',
  reason: 'access denied: path is outside the effective filesystem scope',
  permanent: true,
}

const retryableDenial = {
  error: 'permission_denied' as const,
  message: 'Too many approval requests were already pending. This one may be retried later.',
  tool: 'bash',
  reason: 'saturated',
  permanent: false,
}

describe('isPermissionDenied detector', () => {
  it('recognizes a well-formed PermissionDenied payload', () => {
    expect(isPermissionDenied(permanentDenial)).toBe(true)
  })

  it('rejects an unrelated error payload', () => {
    expect(isPermissionDenied({ error: 'timeout', reason: 'took too long' })).toBe(false)
  })

  it('rejects a payload missing the reason field', () => {
    expect(isPermissionDenied({ error: 'permission_denied', message: 'denied' })).toBe(false)
  })

  it('rejects non-object values', () => {
    expect(isPermissionDenied('permission_denied')).toBe(false)
    expect(isPermissionDenied(null)).toBe(false)
    expect(isPermissionDenied(undefined)).toBe(false)
  })
})

describe('GenericToolCall — permission-denied sentinel (issue #618)', () => {
  it('renders the message, tool, and reason in a distinct block, not raw JSON', () => {
    render(
      <GenericToolCall
        toolName="write_file"
        result={permanentDenial}
        status={COMPLETE_STATUS}
        error={permanentDenial.message}
      />
    )
    fireEvent.click(screen.getByRole('button'))

    const block = screen.getByTestId('result-permission-denied')
    expect(block).toBeInTheDocument()
    expect(block).toHaveTextContent('Access to this path is denied by filesystem policy.')
    expect(block).toHaveTextContent('write_file')
    expect(block).toHaveTextContent('outside the effective filesystem scope')
  })

  it('labels the collapsed header "Permission denied", not generic "Failed"', () => {
    render(
      <GenericToolCall
        toolName="write_file"
        result={permanentDenial}
        status={COMPLETE_STATUS}
        error={permanentDenial.message}
      />
    )
    expect(screen.getByText('Permission denied')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })

  it('shows "Not this turn" for a permanent denial', () => {
    render(
      <GenericToolCall
        toolName="write_file"
        result={permanentDenial}
        status={COMPLETE_STATUS}
        error={permanentDenial.message}
      />
    )
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByTestId('result-permission-denied')).toHaveTextContent('Not this turn')
  })

  it('shows "May succeed later this turn" for a retryable ("saturated") denial', () => {
    render(
      <GenericToolCall
        toolName="bash"
        result={retryableDenial}
        status={COMPLETE_STATUS}
        error={retryableDenial.message}
      />
    )
    fireEvent.click(screen.getByRole('button'))
    expect(screen.getByTestId('result-permission-denied')).toHaveTextContent('May succeed later this turn')
  })

  it('keeps the payload out of the plain-result JSON dump', () => {
    const { container } = render(
      <GenericToolCall
        toolName="write_file"
        result={permanentDenial}
        status={COMPLETE_STATUS}
        error={permanentDenial.message}
      />
    )
    fireEvent.click(screen.getByRole('button'))
    expect(container.textContent).not.toContain('permission_denied')
  })

  it('does not claim an unrelated error payload', () => {
    render(
      <GenericToolCall
        toolName="write_file"
        result={{ error: 'file_exists', reason: 'already there', tool: 'write_file', path: '/w/a.txt' }}
        status={COMPLETE_STATUS}
        error="already there"
      />
    )
    expect(screen.queryByTestId('result-permission-denied')).not.toBeInTheDocument()
  })
})

describe('ToolCallBadge — permission-denied sentinel (issue #618)', () => {
  it('renders amber "Permission denied" and a warning-colored dot, not generic "Failed"', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'write_file',
          status: 'error',
          result: permanentDenial,
        })}
      />
    )
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Permission denied')
    expect(badge).not.toHaveTextContent(/\bFailed\b/)
    const dot = badge.querySelector('.bg-\\[var\\(--color-warning\\)\\]')
    expect(dot).toBeTruthy()
  })

  it('a denial with a non-error status still renders the amber chip (result shape drives the branch, not status alone)', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'edit_file',
          status: 'success',
          result: permanentDenial,
        })}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toHaveTextContent('Permission denied')
  })
})
