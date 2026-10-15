// L1 amendment §1.2 R6 / §1.5; system-agent BDD-11.3.
// Binding: "not this chat" and "points to the root chat"; root is
// root_session_id ?? session_id. Workspace membership is no longer scope.
// Retarget the nine legacy pure-summary cases, preserving counts, unique
// scopes, queue-order target, plural copy and honest unresolved-name fallback.
// Router/focus/navigation behavior is already covered by the existing real
// CrossWorkspaceApprovalBanner tests; do not duplicate those rows here.

import { describe, it, expect } from 'vitest'
import * as summaryModule from './crossWorkspaceApprovals'
import type { PendingToolApproval } from '@/store/toolApproval'

// not-wire-format: public in-browser helper input; optional root introduced
// by K7. The intersection permits compilation on the pre-change base too.
type NoticeApproval = Pick<PendingToolApproval, 'workspaceId' | 'sessionId' | 'agentId'> & {
  rootSessionId?: string
}
interface NoticeSummary { // not-wire-format: public client-side result, not API data
  count: number
  chatIds: string[]
  targetChatId: string
  target: NoticeApproval
  label: string
}
type SummaryFunction = (
  queue: NoticeApproval[], activeChatId: string | null,
  describeChat: (approval: NoticeApproval) => string,
) => NoticeSummary | null

function approval(overrides: Partial<NoticeApproval>): NoticeApproval {
  return { sessionId: 'chat-active', agentId: 'agent-fixture', ...overrides }
}
const CHAT_NAMES: Record<string, string> = {
  'root-a': "Jim's chat in UAT-T2",
  'root-b': "Mia's chat in Marketing Site",
}
// This is the caller-supplied fixture description, not a mock or an internal
// helper spy. Its root selection is independently specified by R6.
const describeChat = (entry: NoticeApproval) => {
  const root = entry.rootSessionId ?? entry.sessionId
  return CHAT_NAMES[root] ?? root
}
function summarize(queue: NoticeApproval[], activeChatId: string | null): NoticeSummary | null {
  const publicApi = summaryModule as unknown as { summarizeApprovalsElsewhere?: SummaryFunction }
  expect(typeof publicApi.summarizeApprovalsElsewhere,
    'BLOCKED: summarizeApprovalsElsewhere not implemented — required by L1 §1.2 R6 / BDD-11.3').toBe('function')
  // Never fall back to the obsolete workspace-only implementation.
  return publicApi.summarizeApprovalsElsewhere!(queue, activeChatId, describeChat)
}

describe('summarizeApprovalsElsewhere — L1 R6 / BDD-11.3 root-chat summary', () => {
  it('returns null when the queue is empty', () => {
    expect(summarize([], 'chat-active')).toBeNull()
  })

  it('returns null when every approval belongs to the active root chat, even across workspaces', () => {
    const queue = [
      approval({ sessionId: 'helper-here', rootSessionId: 'chat-active', workspaceId: 'ws-other' }),
      approval({ sessionId: 'chat-active', workspaceId: 'ws-active' }),
    ]
    expect(summarize(queue, 'chat-active')).toBeNull()
  })

  it('includes another chat with no workspaceId; omitted root uses the acting session', () => {
    const queue = [approval({ sessionId: 'root-a', workspaceId: undefined })]
    expect(summarize(queue, 'chat-active')).toEqual({
      count: 1, chatIds: ['root-a'], targetChatId: 'root-a', target: queue[0],
      label: "1 approval waiting in Jim's chat in UAT-T2",
    })
  })

  it('includes every queued root when no chat is active; there is no global modal scope', () => {
    const queue = [
      approval({ sessionId: 'helper-a', rootSessionId: 'root-a', workspaceId: 'ws-a' }),
      approval({ sessionId: 'helper-b', rootSessionId: 'root-b', workspaceId: 'ws-b' }),
    ]
    expect(summarize(queue, null)).toEqual({
      count: 2, chatIds: ['root-a', 'root-b'], targetChatId: 'root-a', target: queue[0],
      label: '2 approvals waiting in 2 other chats',
    })
  })

  it('singular copy for exactly one approval in one other root, resolving the chat name', () => {
    const queue = [approval({ sessionId: 'helper-a', rootSessionId: 'root-a', workspaceId: 'ws-a' })]
    const summary = summarize(queue, 'chat-active')
    expect(summary).not.toBeNull()
    expect(summary?.count).toBe(1)
    expect(summary?.chatIds).toEqual(['root-a'])
    expect(summary?.targetChatId).toBe('root-a')
    expect(summary?.target).toEqual(queue[0])
    expect(summary?.label).toBe("1 approval waiting in Jim's chat in UAT-T2")
  })

  it('plural copy for several helpers in the SAME other root chat, excluding only the viewed root', () => {
    const queue = [
      approval({ sessionId: 'helper-here', rootSessionId: 'chat-active', workspaceId: 'ws-a' }),
      approval({ sessionId: 'helper-a-1', rootSessionId: 'root-a', workspaceId: 'ws-active' }),
      approval({ sessionId: 'helper-a-2', rootSessionId: 'root-a', workspaceId: 'ws-a' }),
      approval({ sessionId: 'chat-active', workspaceId: undefined }),
    ]
    const summary = summarize(queue, 'chat-active')
    expect(summary?.count).toBe(2)
    expect(summary?.chatIds).toEqual(['root-a'])
    expect(summary?.targetChatId).toBe('root-a')
    expect(summary?.target).toEqual(queue[1])
    expect(summary?.label).toBe("2 approvals waiting in Jim's chat in UAT-T2")
  })

  it('uses N other chats copy when approvals span several roots, not helper sessions or workspaces', () => {
    const queue = [
      approval({ sessionId: 'helper-a', rootSessionId: 'root-a', workspaceId: 'ws-a' }),
      approval({ sessionId: 'helper-b-1', rootSessionId: 'root-b', workspaceId: 'ws-a' }),
      approval({ sessionId: 'helper-b-2', rootSessionId: 'root-b', workspaceId: 'ws-a' }),
    ]
    const summary = summarize(queue, 'chat-active')
    expect(summary?.count).toBe(3)
    expect(summary?.chatIds).toEqual(['root-a', 'root-b'])
    expect(summary?.targetChatId).toBe('root-a')
    expect(summary?.target).toEqual(queue[0])
    expect(summary?.label).toBe('3 approvals waiting in 2 other chats')
  })

  it('targets the FIRST root chat encountered in queue order', () => {
    const queue = [
      approval({ sessionId: 'helper-b', rootSessionId: 'root-b', workspaceId: 'ws-a' }),
      approval({ sessionId: 'helper-a', rootSessionId: 'root-a', workspaceId: 'ws-a' }),
    ]
    const summary = summarize(queue, 'chat-active')
    expect(summary?.targetChatId).toBe('root-b')
    expect(summary?.target).toEqual(queue[0])
    expect(summary?.chatIds).toEqual(['root-b', 'root-a'])
  })

  it('falls back to the raw root-chat id when the caller cannot resolve its name', () => {
    const queue = [approval({ sessionId: 'helper-unknown', rootSessionId: 'root-unknown', workspaceId: 'ws-known' })]
    const summary = summarize(queue, 'chat-active')
    expect(summary?.label).toBe('1 approval waiting in root-unknown')
    expect(summary?.targetChatId).toBe('root-unknown')
  })
})
