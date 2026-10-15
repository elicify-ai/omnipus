// Pure helper for the "waiting in another chat" notice (L1 amendment §1.2 R6,
// BDD-11.3; supersedes the workspace-only scope of the founder decision of
// 2026-09-14). The inline approval card renders only in the approval's ROOT
// chat (approvalChatId), so an approval rooted anywhere else — another
// workspace, another chat in the same workspace, a chat in no workspace, or
// Omnipus's chat — is otherwise invisible. This module counts those and names
// the first chat to go to; the banner (CrossWorkspaceApprovalBanner.tsx)
// renders it. Kept pure so the copy logic is unit-testable.

import { approvalChatId, type PendingToolApproval } from '@/store/toolApproval'

export interface ApprovalsElsewhereSummary { // not-wire-format: client-side UI summary computed from the tool-approval store, never serialized
  /** Approvals whose root chat is not the open one. */
  count: number
  /** Distinct root chats holding them, in arrival order. */
  chatIds: string[]
  /** The chat "Go there" opens — the first one, in arrival order. */
  targetChatId: string
  /** The first approval for the target chat (its workspace and agent). */
  target: Pick<PendingToolApproval, 'rootSessionId' | 'sessionId' | 'workspaceId' | 'agentId'>
  /** Human-readable notice text, plural-aware. */
  label: string
}

/**
 * Summarizes pending approvals whose root chat differs from `activeChatId`,
 * or returns null when there is nothing to show. `describeChat` names a chat
 * for the notice (e.g. "Omnipus" or "Jim's chat in Sales") and never returns
 * a raw id when a name is known.
 */
export function summarizeApprovalsElsewhere(
  queue: Pick<PendingToolApproval, 'rootSessionId' | 'sessionId' | 'workspaceId' | 'agentId'>[],
  activeChatId: string | null,
  describeChat: (approval: Pick<PendingToolApproval, 'rootSessionId' | 'sessionId' | 'workspaceId' | 'agentId'>) => string,
): ApprovalsElsewhereSummary | null {
  const chatIds: string[] = []
  let count = 0
  let target: ApprovalsElsewhereSummary['target'] | null = null
  for (const approval of queue) {
    const chatId = approvalChatId(approval)
    if (chatId === activeChatId) continue
    count += 1
    if (!chatIds.includes(chatId)) chatIds.push(chatId)
    if (!target) target = approval
  }
  if (count === 0 || !target) return null
  const approvalWord = count === 1 ? 'approval' : 'approvals'
  const label =
    chatIds.length === 1
      ? `${count} ${approvalWord} waiting in ${describeChat(target)}`
      : `${count} ${approvalWord} waiting in ${chatIds.length} other chats`
  return { count, chatIds, targetChatId: chatIds[0], target, label }
}
