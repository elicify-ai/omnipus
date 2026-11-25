// L1 amendment §1.2 R6 / §1.5: inline container replaces the modal.
// All non-container preview/action/security oracles retained; B3 stub deletion deferred.
// ToolApprovalModal.escalation.test.tsx — inline surface — ADR-092 D7/D8 Auto pre-flight
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

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen } from '@testing-library/react'
import { act } from 'react'

import { useToolApprovalStore } from '@/store/toolApproval'
import type { ComponentType } from 'react'
import { renderWithClient as render, loadApprovalPanel, resetSharedState, installApprovalNetwork, cleanupApprovalState } from '../../../tests/l1-ui-red-u23/support'
let ToolApprovalPanel: ComponentType

beforeEach(async () => {
  resetSharedState('sess-001')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
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
          // C-24 / C-26: the answers the server lists when it raises a bash
          // card (an ordinary tool: Approve Once, the time choices, Deny and
          // Cancel). Both lists are required by ToolApprovalRequiredFrame.
          offeredActions: ['allow_once', 'allow_for', 'deny', 'cancel'],
          allowDurations: ['1h', '3h', '1d'],
        },
      ],
    })
  })
}

describe('ToolApprovalPanel — Auto pre-flight escalation explanation', () => {
  it('L1 §1.2 R6: explains a filesystem (D7) escalation in plain language', () => {
    queueApproval({
      command: 'echo x > /etc/hosts',
      adr092_kind: 'fs_preflight',
      note: '/etc/hosts needs write access the sandbox does not currently grant: outside workspace root',
    })
    render(<ToolApprovalPanel />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/wants to reach outside the workspace/i)
    expect(banner).toHaveTextContent('/etc/hosts needs write access')
  })

  it('L1 §1.2 R6: explains a network (D8) escalation in plain language, matching "wants network access"', () => {
    queueApproval({
      command: 'curl https://example.com',
      adr092_kind: 'network_preflight',
      note: 'this command needs outbound network access the sandbox does not currently grant',
    })
    render(<ToolApprovalPanel />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/wants network access/i)
  })

  it('L1 §1.2 R6: explains a blind-spot (D7 unparseable) escalation distinctly', () => {
    queueApproval({
      command: 'sh -c "$(cat weird)"',
      adr092_kind: 'fs_preflight_blind',
      note: 'the command could not be parsed for filesystem references',
    })
    render(<ToolApprovalPanel />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/could not be classified/i)
  })

  it('L1 §1.2 R6: shows no rule banner for a rule_ask card with no note (describeEscalation requires both fields)', () => {
    queueApproval({
      command: 'git status',
      adr092_kind: 'rule_ask',
    })
    render(<ToolApprovalPanel />)

    expect(screen.queryByTestId('auto-escalation-explanation')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: explains a D3 rule_ask prompt as an operator rule, not a sandbox escalation', () => {
    queueApproval({
      command: 'rm -rf /tmp/build',
      adr092_kind: 'rule_ask',
      note: 'matches an operator rule that requires approval (binary="rm" arg_prefix="rm -rf")',
    })
    render(<ToolApprovalPanel />)

    const banner = screen.getByTestId('auto-escalation-explanation')
    expect(banner).toHaveTextContent(/operator command rule requires approval/i)
    expect(banner).toHaveTextContent('matches an operator rule that requires approval (binary="rm" arg_prefix="rm -rf")')
    expect(banner).not.toHaveTextContent(/needs more access than the sandbox/i)
  })

  it('L1 §1.2 R6: shows no escalation banner for an ordinary bash approval with no adr092_kind at all', () => {
    queueApproval({ command: 'npm test' })
    render(<ToolApprovalPanel />)

    expect(screen.queryByTestId('auto-escalation-explanation')).not.toBeInTheDocument()
  })

  // Regression row "Always Allow on approval cards" (skills-tools-redesign-spec,
  // Regression Test Requirements): C-26 point 1 / S-119 removed Always Allow
  // from every card; its replacement is the three timed choices. The R6 case
  // keeps what it was for - the primary action is labelled Approve Once, a
  // one-time affordance - and the thing it was distinct from is now the time
  // limit ("Allow for 1 hour" and the others), with no Always Allow at all.
  it('L1 §1.2 R6 → C-26: labels the primary action "Approve Once" — a one-time affordance distinct from the timed "Allow for" choices', () => {
    queueApproval({ command: 'npm test' })
    render(<ToolApprovalPanel />)

    expect(screen.getByRole('button', { name: /^Approve Once$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Allow for 1 hour$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Allow for 3 hours$/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Allow for 1 day$/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Always Allow/i })).not.toBeInTheDocument()
  })
})

// Real stores/QueryClient/auth survive the test boundary; only fetch/socket doubled.
afterEach(async () => { await cleanupApprovalState() })
