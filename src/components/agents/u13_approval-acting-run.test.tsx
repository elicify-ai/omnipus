// U13 / FR-034 / BDD-10.2, retargeted by L1 amendment §1.2 R6 / §1.5.
// Binding quotes: "Card renders ... chat whose session id equals
// root_session_id (or session_id when root_session_id is omitted)";
// "U13's 'Open run' link to the acting session is kept."
// One inline root-chat card replaces one modal; acting identity and the
// exact Open-run target contract remain. B3 is not changed by this file.

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen, within } from '@testing-library/react'
import { act, type ComponentType } from 'react'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useSessionStore } from '@/store/session'
import {
  loadApprovalPanel, renderWithClient, resetSharedState,
  installApprovalNetwork, cleanupApprovalState, approvalPosts,
} from '../../../tests/l1-ui-red-u23/support'

const ROOT_CHAT = 'sess-owning-root'
const HELPER_APPROVAL: ToolApprovalRequiredFrame = {
  type: 'tool_approval_required',
  approval_id: 'appr-helper-001',
  tool_call_id: 'call-helper-001',
  tool_name: 'bash',
  args: { command: 'echo hi' },
  agent_id: 'agent-helper',
  session_id: 'sess-helper-run-7',
  root_session_id: ROOT_CHAT,
  turn_id: 'turn-001',
  expires_in_ms: 300_000, // same five-minute fixture window as the legacy case
}
let ToolApprovalPanel: ComponentType

beforeEach(async () => {
  resetSharedState(ROOT_CHAT)
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
})
afterEach(async () => { await cleanupApprovalState() })

function showHelperApproval() {
  act(() => useToolApprovalStore.getState().enqueue(HELPER_APPROVAL))
  renderWithClient(<ToolApprovalPanel />)
}

describe('U13 / FR-034 / BDD-10.2 / R6 — inline root-chat card attributes the acting helper/run', () => {
  it('names the acting execution and exposes an Open control to its run session', () => {
    showHelperApproval()

    // BDD-10.2 retains "no duplicate queue/card"; the amended container is
    // inline, not a dialog. Root placement must not retarget Open to the root.
    expect(screen.getAllByTestId('tool-approval-panel')).toHaveLength(1)
    expect(screen.queryAllByRole('dialog')).toHaveLength(0)
    expect(useToolApprovalStore.getState().queue.map(a => a.approvalId)).toEqual(['appr-helper-001'])
    expect(useSessionStore.getState().activeSessionId).toBe(ROOT_CHAT)
    const open = screen.getByTestId('approval-open-run')
    expect(open).toBeInTheDocument()
    expect(open).toHaveAttribute('data-session-id', 'sess-helper-run-7')
    expect(open).not.toHaveAttribute('data-session-id', ROOT_CHAT)
    expect(approvalPosts()).toEqual([])
  })

  it('still names the acting agent (positive control — unchanged behaviour)', () => {
    showHelperApproval()
    const panel = screen.getAllByTestId('tool-approval-panel')[0]
    // No acting_agent_name and no agent cache: the existing raw-id fallback
    // remains exact. The owning root must not replace the acting identity.
    expect(within(panel).getByText('agent-helper')).toBeInTheDocument()
    expect(approvalPosts()).toEqual([])
  })
})
