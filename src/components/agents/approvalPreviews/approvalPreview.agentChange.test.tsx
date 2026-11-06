import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, type ComponentType } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useToolApprovalStore } from '@/store/toolApproval'
import { TOOL_APPROVAL_PREVIEWS } from './registry'
import { renderWithClient, loadApprovalPanel, resetSharedState, installApprovalNetwork, cleanupApprovalState } from '../../../../tests/l1-ui-red-u23/support'

// Oracle: tool-parity r17 17.1/17.3/17.7. Real approval panel/summary/store;
// only HTTP/WebSocket process boundaries use the established fixture.
let Panel: ComponentType
beforeEach(async () => {
  resetSharedState('sess-001')
  installApprovalNetwork()
  Panel = await loadApprovalPanel()
})
afterEach(() => cleanupApprovalState())

function show(toolName: string, extra: Record<string, unknown> = {}) {
  act(() => useToolApprovalStore.setState({ queue: [{
    approvalId: 't8-approval', toolCallId: 't8-call', toolName,
    agentId: 'agent-main', sessionId: 'sess-001', turnId: 'turn-001',
    expiresAt: Date.now() + 300_000,
    args: { id: 'worker', env_overrides: { TOKEN: '••••••' }, cli_args: '--model x --verbose', ...extra },
  }] }))
  return renderWithClient(<Panel />)
}

describe('agent change approval is additive, masked and keyboard-stable', () => {
  it.each(['create_agent', 'update_agent'])('17.1 %s summary masks names and splits flags', (tool) => {
    show(tool)
    expect(TOOL_APPROVAL_PREVIEWS[tool]?.mode, 'T8 summary entry must exist and remain additive').toBe('additive')
    expect(screen.getByText('TOKEN')).toBeInTheDocument()
    expect(screen.getByText(/value hidden/i)).toBeInTheDocument()
    expect(screen.getByText('--model x')).toBeInTheDocument()
    expect(screen.getByText('--verbose')).toBeInTheDocument()
    // The transport fixture is already masked; raw-secret redaction is
    // deliberately not claimed here (requires the real-loop SR-1 test).
  })

  it('17.3 keeps an unknown argument visible in the raw block alongside the summary', () => {
    show('update_agent', { zz_probe: 'must remain visible' })
    expect(screen.getByText(/value hidden/i)).toBeInTheDocument()
    expect(screen.getByText(/zz_probe/)).toBeInTheDocument()
    expect(screen.getByText(/must remain visible/)).toBeInTheDocument()
  })

  it('17.7 retains Approve then Deny and gives the mask a readable name', async () => {
    show('update_agent')
    const approve = screen.getByRole('button', { name: /^Approve Once$/i })
    const deny = screen.getByRole('button', { name: /^Deny$/i })
    expect(approve.compareDocumentPosition(deny) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
    approve.focus()
    expect(approve).toHaveFocus()
    await userEvent.setup().tab()
    expect(deny).toHaveFocus()
    expect(screen.getByText(/value hidden/i)).toBeInTheDocument()
  })
})
