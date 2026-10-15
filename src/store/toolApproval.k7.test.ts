// toolApproval.k7.test.ts — additive K7 fields (omnipus-system-agent K7; L1
// amendment §1.2 R6, §1.3): the root chat travels with every approval, and a
// complete snapshot restores a complete (approvable) entry after a reload.
import { describe, it, expect, beforeEach } from 'vitest'
import type { SessionStateFrame, ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { approvalChatId, useToolApprovalStore } from './toolApproval'

function live(over: Partial<ToolApprovalRequiredFrame> = {}): ToolApprovalRequiredFrame {
  return {
    type: 'tool_approval_required', approval_id: 'ap-1', tool_call_id: 'tc-1', tool_name: 'bash',
    args: { command: 'ls' }, agent_id: 'omnipus', session_id: 'helper-1', turn_id: 'turn-1', expires_in_ms: 60_000,
    root_session_id: 'omnipus-main', acting_agent_name: 'Omnipus helper', origin_label: 'asked by Omnipus', ...over,
  }
}
function snapshot(pending: SessionStateFrame['pending_approvals']): SessionStateFrame {
  return { type: 'session_state', user_id: 'u', pending_approvals: pending } as SessionStateFrame
}

describe('tool approval store — K7', () => {
  beforeEach(() => useToolApprovalStore.setState({ queue: [], resolvedIds: [] }))

  it('keeps the root chat and the acting agent from the live frame', () => {
    useToolApprovalStore.getState().enqueue(live())
    const [entry] = useToolApprovalStore.getState().queue
    expect(approvalChatId(entry)).toBe('omnipus-main')
    expect(entry.actingAgentName).toBe('Omnipus helper')
    expect(entry.originLabel).toBe('asked by Omnipus')
  })

  it('falls back to the acting session when the server omits the root', () => {
    useToolApprovalStore.getState().enqueue(live({ root_session_id: undefined }))
    expect(approvalChatId(useToolApprovalStore.getState().queue[0])).toBe('helper-1')
  })

  it('a complete snapshot restores a complete entry after a reload, not a Deny-only stub', () => {
    useToolApprovalStore.getState().reconcileWithSessionState(snapshot([{
      approval_id: 'ap-2', session_id: 'helper-1', tool_name: 'bash', agent_id: 'omnipus', expires_in_ms: 30_000,
      tool_call_id: 'tc-2', turn_id: 'turn-2', args: { command: 'pwd' }, root_session_id: 'omnipus-main',
    }]))
    const [entry] = useToolApprovalStore.getState().queue
    expect(entry.toolCallId).toBe('tc-2')
    expect(entry.turnId).toBe('turn-2')
    expect(entry.args).toEqual({ command: 'pwd' })
    expect(approvalChatId(entry)).toBe('omnipus-main')
  })

  it('a snapshot without the details still yields the honest stub (until bundle B3)', () => {
    useToolApprovalStore.getState().reconcileWithSessionState(snapshot([{
      approval_id: 'ap-3', session_id: 's', tool_name: 'bash', agent_id: 'mia', expires_in_ms: 30_000,
    }]))
    const [entry] = useToolApprovalStore.getState().queue
    expect(entry.toolCallId).toBe('')
    expect(entry.turnId).toBe('')
    expect(entry.args).toEqual({})
  })

  it('a live frame upgrades a stub to the full request', () => {
    useToolApprovalStore.getState().reconcileWithSessionState(snapshot([{
      approval_id: 'ap-1', session_id: 'helper-1', tool_name: 'bash', agent_id: 'omnipus', expires_in_ms: 30_000,
    }]))
    useToolApprovalStore.getState().enqueue(live())
    const [entry] = useToolApprovalStore.getState().queue
    expect(entry.toolCallId).toBe('tc-1')
    expect(entry.args).toEqual({ command: 'ls' })
  })
})
