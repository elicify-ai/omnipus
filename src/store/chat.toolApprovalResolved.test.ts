// chat.ts frame dispatch for tool_approval_resolved: the server's resolution
// frame must reach the tool approval store (and must not be treated as an
// unknown frame type).

import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useToolApprovalStore } from './toolApproval'

const SID = 'approval-resolved-session'

beforeEach(() => {
  act(() => {
    useChatStore.setState({ sessionsById: {} })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('chat handleFrame — tool_approval_resolved', () => {
  it('drops the approval, and a stale session_state snapshot cannot bring it back', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_approval_required',
        approval_id: 'appr-x',
        tool_call_id: 'call-x',
        tool_name: 'write_file',
        args: { path: 'e3-marker.txt' },
        agent_id: 'agent-x',
        session_id: SID,
        turn_id: 'turn-x',
        expires_in_ms: 300_000,
      })
    })
    expect(useToolApprovalStore.getState().queue.map((a) => a.approvalId)).toEqual(['appr-x'])

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_approval_resolved',
        approval_id: 'appr-x',
        state: 'denied_cancel',
        session_id: SID,
      })
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    expect(useToolApprovalStore.getState().resolvedIds).toContain('appr-x')

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_state',
        user_id: 'user-1',
        emitted_at: new Date().toISOString(),
        pending_approvals: [
          { approval_id: 'appr-x', session_id: SID, tool_name: 'write_file', agent_id: 'agent-x', expires_in_ms: 1_000 },
        ],
      })
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)

    const unknownFrameWarnings = warn.mock.calls.filter((c) => c[0] === '[chat] Unknown frame type')
    expect(unknownFrameWarnings).toEqual([])
  })
})
