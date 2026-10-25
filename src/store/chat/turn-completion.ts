// A terminal frame owns its turn, not whichever turn is running when it arrives.
import { produce } from 'immer'
import type { DoneFrame } from '@/lib/api/generated/asyncapi-types'
import { normalizeTruncationReason } from '@/lib/truncation'
import { endTurnState, findLastAssistantMessageId } from './messages'
import { bakeToolCallsByOwner } from './session'
import { markTurnFinished } from './routing'
import { mergeRecordedSessionTokens } from './session-usage'
import type { SessionChatState } from './types'

export function doneOwnsRunningTurn(bucket: SessionChatState, turnId: string | undefined): boolean {
  if (!turnId) return true
  const tailId = findLastAssistantMessageId(bucket.messageOrder, bucket.messagesById)
  const tail = tailId ? bucket.messagesById[tailId] : undefined
  const current = bucket.activeTurnId ?? (bucket.isStreaming ? tail?.turnId : undefined)
  if (current) return current === turnId
  // An unstamped new-send placeholder is not owned by an earlier named turn.
  return !tail?.isStreaming || !!tail.turnId || !Object.values(bucket.messagesById).some(message => message.role === 'assistant' && message.turnId === turnId)
}

export function doneAlreadyApplied(bucket: SessionChatState, turnId: string | undefined): boolean {
  if (!turnId || bucket.activeTurnId === turnId) return false
  const messages = Object.values(bucket.messagesById).filter(message => message.role === 'assistant' && message.turnId === turnId)
  return messages.length > 0 && messages.every(message => !message.isStreaming && message.status !== 'streaming' && !message.closedBySteer)
}

export function completeDoneTurn(bucket: SessionChatState, sid: string, frame: DoneFrame, clearReplayingNow: boolean): Partial<SessionChatState> {
  if (doneAlreadyApplied(bucket, frame.turn_id)) return {}
  return produce(bucket, draft => {
    const turnId = frame.turn_id
    const tailId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
    const ownsRunningTurn = doneOwnsRunningTurn(bucket, turnId)
    const turnIds = draft.messageOrder.filter(id => draft.messagesById[id]?.role === 'assistant' && draft.messagesById[id].turnId === turnId)
    const lastMsgId = turnId && turnIds.length
      ? turnIds.at(-1)!
      : frame.message_id && draft.messagesById[frame.message_id]
        ? frame.message_id
        : ownsRunningTurn ? tailId : null
    for (const id of draft.messageOrder) {
      const message = draft.messagesById[id]
      if (message?.role !== 'assistant') continue
      // Unstamped optimistic/tool-only bubbles may close with the current turn.
      // A stamped bubble belonging to any other turn is never swept along.
      if (turnId && message.turnId && message.turnId !== turnId) continue
      if (!ownsRunningTurn && message.turnId !== turnId) continue
      if (id !== lastMsgId && !message.isStreaming && message.status !== 'streaming') continue
      message.isStreaming = false
      if (message.status !== 'interrupted' && message.status !== 'error') message.status = 'done'
      message.pendingTextBoundary = false
      if (id === lastMsgId && frame.stats?.truncated) {
        message.truncated = true
        message.truncationReason = normalizeTruncationReason(frame.stats.truncated, frame.stats.truncation_reason)
      }
    }
    const callIds = ownsRunningTurn ? draft.toolCallOrder : draft.toolCallOrder.filter(id => {
      const owner = draft.toolCallOwnerMessageId?.[id]
      return owner && draft.messagesById[owner]?.turnId === turnId
    })
    if (callIds.length) {
      bakeToolCallsByOwner(draft.messagesById, callIds, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, lastMsgId, draft.textAtToolCallStart)
      const completed = new Set(callIds)
      draft.toolCallOrder = draft.toolCallOrder.filter(id => !completed.has(id))
      for (const id of callIds) {
        delete draft.toolCalls[id]
        delete draft.textAtToolCallStart[id]
        if (draft.toolCallOwnerMessageId) delete draft.toolCallOwnerMessageId[id]
      }
    }
    draft.sessionTokens = mergeRecordedSessionTokens(draft.sessionTokens, frame.stats?.session_tokens_total)
    draft.sessionCost += frame.stats?.cost ?? 0
    if (!ownsRunningTurn) return
    markTurnFinished(sid, draft.activeTurnId)
    draft.isStreaming = false
    draft.replayCompletedForSession = draft.isReplaying ? sid : draft.replayCompletedForSession
    draft.cancelStage = null
    endTurnState(draft)
    if (clearReplayingNow) draft.isReplaying = false
  }) as Partial<SessionChatState>
}
