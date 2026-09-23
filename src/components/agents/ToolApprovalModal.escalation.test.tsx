// ToolApprovalModal.escalation.test.tsx — ADR-092 D7/D8 Auto pre-flight
// escalation explanation (code-review finding, item 3).
//
// pkg/tools/shell_permission_mode.go's requestPreflightApproval sets
// args.adr092_kind ("fs_preflight" | "fs_preflight_blind" |
// "network_preflight") and args.note on the tool_approval_required frame
// when Auto mode itself already tried and failed to clear the call. Before
// this fix the card looked exactly like an ordinary ask-policy bash
// approval, with nothing telling the human WHY it escalated. The D3
// "rule_ask" kind (an ordinary ask-rule prompt, not an Auto escalation)
// carries adr092_kind with no `note` field — must NOT show the banner.

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

  it('shows no escalation banner for an ordinary ask-rule prompt (rule_ask carries no note)', () => {
    queueApproval({
      command: 'git status',
      adr092_kind: 'rule_ask',
    })
    render(<ToolApprovalModal />)

    expect(screen.queryByTestId('auto-escalation-explanation')).not.toBeInTheDocument()
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
