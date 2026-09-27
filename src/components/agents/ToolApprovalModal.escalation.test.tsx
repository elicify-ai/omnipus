// ToolApprovalModal.escalation.test.tsx — ADR-092 D7/D8 Auto pre-flight
// escalation explanation (code-review finding, item 3), plus the D3
// "rule_ask" operator-rule prompt.
//
// pkg/tools/shell_permission_mode.go's requestPreflightApproval sets
// args.adr092_kind ("fs_preflight" | "fs_preflight_blind" |
// "network_preflight") and args.note on the tool_approval_required frame
// when Auto mode itself already tried and failed to clear the call. Before
// this fix the card looked exactly like an ordinary ask-policy bash
// approval, with nothing telling the human WHY it escalated.
//
// The D3 "rule_ask" kind is a different thing — an ordinary operator
// command-rule ask, not an Auto escalation. pkg/agent/loop_policy.go's
// ruleAskRequestArgs sets adr092_kind "rule_ask" together with a `note`
// naming the matched rule whenever the call matched a genuine ask rule, so
// rule_ask DOES carry a note (a build that queues rule_ask with no note at
// all — an older wire shape, or a rule match that named no rule — still
// shows no banner; describeEscalation requires both fields).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act } from 'react'

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    submitToolApproval: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn((selector) => {
    const state = { addToast: vi.fn(), toasts: [], removeToast: vi.fn() }
    return selector ? selector(state) : state
  }),
}))

vi.mock('@/lib/authLogout', () => ({
  forceLogout: vi.fn(),
}))

import { useToolApprovalStore } from '@/store/toolApproval'
import { ToolApprovalModal } from './ToolApprovalModal'

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
  vi.clearAllMocks()
})

function queueApproval(args: Record<string, unknown>) {
  act(() => {
    useToolApprovalStore.setState({
      queue: [
        {
          approvalId: 'appr-esc-001',
          toolCallId: 'call-esc-001',
          toolName: 'bash',
          args,
          agentId: 'agent-main',
          sessionId: 'sess-001',
          turnId: 'turn-001',
          expiresAt: Date.now() + 300_000,
        },
      ],
    })
  })
}

describe('ToolApprovalModal — Auto pre-flight escalation explanation', () => {
  it('explains a filesystem (D7) escalation in plain language', () => {
    queueApproval({
      command: 'echo x > /etc/hosts',
      adr092_kind: 'fs_preflight',
      note: '/etc/hosts needs write access the sandbox does not currently grant: outside workspace root',
    })
    render(<ToolApprovalModal />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/wants to reach outside the workspace/i)
    expect(banner).toHaveTextContent('/etc/hosts needs write access')
  })

  it('explains a network (D8) escalation in plain language, matching "wants network access"', () => {
    queueApproval({
      command: 'curl https://example.com',
      adr092_kind: 'network_preflight',
      note: 'this command needs outbound network access the sandbox does not currently grant',
    })
    render(<ToolApprovalModal />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/wants network access/i)
  })

  it('explains a blind-spot (D7 unparseable) escalation distinctly', () => {
    queueApproval({
      command: 'sh -c "$(cat weird)"',
      adr092_kind: 'fs_preflight_blind',
      note: 'the command could not be parsed for filesystem references',
    })
    render(<ToolApprovalModal />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/could not be classified/i)
  })

  it('shows no rule banner for a rule_ask card with no note (describeEscalation requires both fields)', () => {
    queueApproval({
      command: 'git status',
      adr092_kind: 'rule_ask',
    })
    render(<ToolApprovalModal />)

    expect(screen.queryByTestId('auto-escalation-explanation')).not.toBeInTheDocument()
  })

  it('explains a D3 rule_ask prompt as an operator rule, not a sandbox escalation', () => {
    queueApproval({
      command: 'rm -rf /tmp/build',
      adr092_kind: 'rule_ask',
      note: 'matches an operator rule that requires approval (binary="rm" arg_prefix="rm -rf")',
    })
    render(<ToolApprovalModal />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/operator command rule requires approval/i)
    expect(banner).toHaveTextContent('matches an operator rule that requires approval (binary="rm" arg_prefix="rm -rf")')
    expect(banner).not.toHaveTextContent(/needs more access than the sandbox/i)
  })

  it('shows no escalation banner for an ordinary bash approval with no adr092_kind at all', () => {
    queueApproval({ command: 'npm test' })
    render(<ToolApprovalModal />)

    expect(screen.queryByTestId('auto-escalation-explanation')).not.toBeInTheDocument()
  })

  it('labels the primary action "Approve Once" — a one-time affordance distinct from "Always Allow"', () => {
    queueApproval({ command: 'npm test' })
    render(<ToolApprovalModal />)

    expect(screen.getByRole('button', { name: /Approve Once/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Always Allow$/i })).toBeInTheDocument()
  })
})
