import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { ExecApprovalBlock } from './ExecApprovalBlock'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import type { ExecApprovalRequestFrame } from '@/lib/api/generated/asyncapi-types'

// test_exec_approval_block (test #9)
// test_exec_approval_actions (test #10)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Approval block renders with command details
//             wave5a-wire-ui-spec.md — Scenario Outline: User responds to approval prompt

beforeEach(() => {
  act(() => {
    useChatStore.setState({
      pendingApprovals: [],
      messages: [],
      isStreaming: false,
      toolCalls: {},
    })
    useConnectionStore.setState({ connection: null, isConnected: false })
  })
})

describe('ExecApprovalBlock — rendering (test #9)', () => {
  it('renders command, working directory, matched policy, and three action buttons', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Approval block renders with command details (AC1)
    render(
      <ExecApprovalBlock
        approval={{
          id: 'appr_1',
          command: 'git pull origin main',
          working_dir: '~/projects/omnipus',
          matched_policy: 'tools.exec.approval=ask',
          status: 'pending',
        }}
      />
    )
    // Command is split across spans (binary highlighted separately from args) so
    // getByText regex won't match a single element. Assert via pre element textContent.
    const pre = document.querySelector('pre')
    expect(pre?.textContent).toMatch(/git pull origin main/i)
    expect(screen.getByText(/~\/projects\/omnipus/i)).toBeInTheDocument()
    expect(screen.getByText(/tools\.exec\.approval=ask/i)).toBeInTheDocument()
    // Use anchored regex: /^Allow$/i to avoid matching "Always Allow"
    expect(screen.getByRole('button', { name: /^Allow$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Deny$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Always Allow$/i })).toBeInTheDocument()
  })

  it('renders with a warning border/background when status is pending', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC5: warning border for pending approval
    const { container } = render(
      <ExecApprovalBlock
        approval={{
          id: 'appr_1',
          command: 'rm -rf /tmp',
          status: 'pending',
        }}
      />
    )
    // Should have warning styling — component uses CSS variable class border-[var(--color-warning)]
    const block = container.firstElementChild as HTMLElement
    expect(block?.className).toMatch(/warning/)
  })

  it('shows "Allowed" state when approval has been allowed', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: User responds to approval (AC2 status)
    render(
      <ExecApprovalBlock
        approval={{
          id: 'appr_1',
          command: 'git pull origin main',
          status: 'allowed',
        }}
      />
    )
    expect(screen.getByText(/Allowed/i)).toBeInTheDocument()
    // Buttons should be hidden when resolved
    expect(screen.queryByRole('button', { name: /^Allow$/i })).toBeNull()
  })

  it('shows "Denied" state when approval has been denied', () => {
    render(
      <ExecApprovalBlock
        approval={{
          id: 'appr_1',
          command: 'git pull origin main',
          status: 'denied',
        }}
      />
    )
    expect(screen.getByText(/Denied/i)).toBeInTheDocument()
  })
})

describe('ExecApprovalBlock — user actions (test #10)', () => {
  it('clicking Allow calls respondToApproval(id, "allow")', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: User responds to approval (AC2)
    // We mock the connection so respondToApproval can actually call connection.send
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
      useChatStore.getState().addApprovalRequest({
        type: 'exec_approval_request',
        id: 'appr_action_1',
        command: 'git pull origin main',
        session_id: 'sess_test',
      })
    })

    render(
      <ExecApprovalBlock
        approval={{ id: 'appr_action_1', command: 'git pull origin main', status: 'pending' }}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /^Allow$/i }))
    expect(mockSend).toHaveBeenCalledWith({ type: 'exec_approval_response', id: 'appr_action_1', decision: 'allow' })
  })

  it('clicking Deny calls respondToApproval(id, "deny")', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: User responds to approval (AC3)
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
    })

    render(
      <ExecApprovalBlock
        approval={{ id: 'appr_action_2', command: 'rm -rf /tmp', status: 'pending' }}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /deny/i }))
    expect(mockSend).toHaveBeenCalledWith({ type: 'exec_approval_response', id: 'appr_action_2', decision: 'deny' })
  })

  it('clicking Always Allow calls respondToApproval(id, "always")', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: User responds to approval (AC4)
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
    })

    render(
      <ExecApprovalBlock
        approval={{ id: 'appr_action_3', command: 'git pull origin main', status: 'pending' }}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /always allow/i }))
    expect(mockSend).toHaveBeenCalledWith({ type: 'exec_approval_response', id: 'appr_action_3', decision: 'always' })
  })
})

// ── Phase 5: edge-case render tests ────────────────────────────────────────────
//
// These tests verify that ExecApprovalBlock does not crash when a valid-shape
// but degenerate-value ExecApprovalRequestFrame drives the component. Props are
// mapped from the generated ExecApprovalRequestFrame type — the same wire format
// the gateway emits. No hand-written wire types.
//
// The approval prop shape in ExecApprovalBlock is a subset of ExecApprovalRequestFrame:
//   ExecApprovalRequestFrame.id       → approval.id
//   ExecApprovalRequestFrame.command  → approval.command
//   ExecApprovalRequestFrame.working_dir → approval.working_dir
//   ExecApprovalRequestFrame.matched_policy → approval.matched_policy
//
// Each test: render ExecApprovalBlock with approval derived from wire frame → assert no throw.

// Minimal valid ExecApprovalRequestFrame (required fields per AsyncAPI spec)
const baseWireFrame: ExecApprovalRequestFrame = {
  type: 'exec_approval_request',
  session_id: 'sess-edge',
  id: 'appr-edge-001',
  command: 'git pull origin main',
}

// Edge cases: [label, partial override of ExecApprovalRequestFrame fields]
type WireFrameOverrides = Partial<Omit<ExecApprovalRequestFrame, 'type'>>

const execEdgeCases: Array<[string, WireFrameOverrides]> = [
  // Empty command string — binaryIndex logic must not crash on empty input
  ['empty command string', { command: '' }],
  // Command with only env var prefix (KEY=value) — binaryIndex skips env prefix
  ['command is only env var assignment', { command: 'FOO=bar' }],
  // Multiple env var prefixes before binary
  ['command has multiple env var prefixes', { command: 'A=1 B=2 git pull' }],
  // Very long command — tests whitespace-pre-wrap overflow rendering
  ['very long command', { command: 'echo ' + 'x'.repeat(10_000) }],
  // Command with no spaces — single token, no args
  ['command with no spaces (single token)', { command: 'ls' }],
  // Command with unicode characters
  ['unicode in command', { command: 'echo "\u{1F680} \u{1F30D}"' }],
  // working_dir provided
  ['working_dir provided', { working_dir: '/home/user/projects/omnipus' }],
  // Very long working_dir
  ['very long working_dir', { working_dir: '/home/' + 'x'.repeat(500) }],
  // matched_policy provided
  ['matched_policy provided', { matched_policy: 'tools.exec.approval=ask' }],
  // Very long matched_policy
  ['very long matched_policy', { matched_policy: 'policy.' + 'x'.repeat(300) }],
  // Both working_dir and matched_policy — all metadata rendered
  ['working_dir and matched_policy both provided', { working_dir: '/tmp', matched_policy: 'exec.ask' }],
  // id is an empty string — extreme degenerate
  ['id is empty string', { id: '' }],
  // id with special characters
  ['id with special characters', { id: 'appr/edge?01&x=1' }],
]

describe.each(execEdgeCases)(
  'ExecApprovalBlock renders "%s" without throwing',
  (_label, overrides) => {
    it('renders without throwing', () => {
      const frame: ExecApprovalRequestFrame = { ...baseWireFrame, ...overrides }
      // Map wire frame fields to the approval prop shape
      const approval = {
        id: frame.id,
        command: frame.command,
        working_dir: frame.working_dir,
        matched_policy: frame.matched_policy,
        status: 'pending' as const,
      }
      expect(() =>
        render(
          <ExecApprovalBlock
            approval={approval}
            onDecision={vi.fn()}
          />,
        ),
      ).not.toThrow()
    })
  },
)
