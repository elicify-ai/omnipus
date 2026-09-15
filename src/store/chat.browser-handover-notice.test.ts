/**
 * chat.browser-handover-notice.test.ts — ADR-085 BROWSER-FR-042 (render
 * half) / FR-044 (SPA idempotency half), wave B8.
 *
 * `BrowserHandoverNoticeFrame` (contracts/components/schemas/
 * BrowserHandoverNoticeFrame.yaml) is delivered live to the open thread
 * whenever a person takes the wheel or the agent calls `browser_handover`
 * (BROWSER-FR-041). Fix mirrors the shipped goal-ack-line pattern
 * (`chat.goal-ack-line.test.ts`) exactly: the FIRST time the store sees a
 * frame carrying a given `message_id`, it inserts one synthetic `role:
 * 'system'` message into the thread carrying that same id — and NEVER a
 * second time for the same `message_id` (idempotent — BROWSER-FR-044:
 * exactly one notice per unbroken held period; the server derives
 * `message_id` from `(session_id, holdStartedAtUnixNano)`, reused verbatim
 * for every emission within one unbroken hold, so a WS re-attach
 * re-delivering the same live frame, or the FR-043a replay of the same
 * persisted transcript entry, must never duplicate the line).
 *
 * Unlike the goal-ack line, this notice always appends at the tail — there
 * is no "/goal <condition>" anchor text to search the thread for — and its
 * de-dup key is the server-computed `message_id` itself (`browserHandover
 * NoticeId`), not a client-derived id from some other field.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import type { BrowserHandoverNoticeFrame } from '@/lib/api/generated/asyncapi-types'

const SID = 'browser-handover-notice-test-sid'
const MESSAGE_ID = 'browser-handover-01J3ZQK8N2H8VXNRP5T7C9M4WE'
const NOTICE_TEXT = "A person has taken control of the browser. Send a message to get it back."

function makeFrame(overrides: Partial<BrowserHandoverNoticeFrame> = {}): BrowserHandoverNoticeFrame {
  return {
    type: 'browser_handover_notice',
    session_id: SID,
    message_id: MESSAGE_ID,
    text: NOTICE_TEXT,
    ...overrides,
  }
}

/** Seeds a full bucket at SID with the given messages already present —
 * mirrors chat.goal-ack-line.test.ts's seedBucket helper. */
function seedBucket(seedMessages: ChatMessage[] = []): void {
  act(() => {
    useChatStore.setState(() => ({
      sessionsById: {
        [SID]: {
          ...makeBucketMessages(seedMessages),
          toolCalls: {},
          toolCallOrder: [],
          textAtToolCallStart: {},
          isStreaming: false,
          isReplaying: false,
          replayCompletedForSession: null,
          sessionTokens: 0,
          sessionCost: 0,
          rateLimitEvent: null,
          lastUserMessageAt: null,
          cancelStage: null,
          lastReceivedEventTime: null,
          spanByParentCallId: {},
        },
      },
      messages: seedMessages,
    }))
  })
}

function resetStores(): void {
  act(() => {
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      goalStatus: null,
      goalPills: {},
    })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  })
}

beforeEach(resetStores)

function userMessage(id: string, content: string): ChatMessage {
  return { id, role: 'user', content, timestamp: new Date().toISOString(), status: 'done' }
}

describe('browser-handover-notice — insertion (ADR-085 BROWSER-FR-041/042)', () => {
  it('inserts a system-role notice message carrying the frame text on first arrival', () => {
    seedBucket([userMessage('u1', 'take the wheel please')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const noticeId = bucket.messageOrder.find(
      (id) => bucket.messagesById[id]?.browserHandoverNoticeId === MESSAGE_ID,
    )
    expect(noticeId).toBeTruthy()
    const noticeMsg = bucket.messagesById[noticeId as string]
    expect(noticeMsg.role).toBe('system')
    expect(noticeMsg.content).toBe(NOTICE_TEXT)
  })

  it('stamps the message id with the server-computed message_id, not a client-derived value', () => {
    seedBucket()

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.messagesById[MESSAGE_ID]).toBeTruthy()
    expect(bucket.messagesById[MESSAGE_ID].browserHandoverNoticeId).toBe(MESSAGE_ID)
  })

  it('appends at the current tail — no anchor-message search, unlike the goal-ack line', () => {
    seedBucket([userMessage('u1', 'hello'), userMessage('u2', 'take the wheel please')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const lastId = bucket.messageOrder[bucket.messageOrder.length - 1]
    expect(bucket.messagesById[lastId]?.browserHandoverNoticeId).toBe(MESSAGE_ID)
  })

  it('drops a duplicate browser-handover notice with an id already in the store (BROWSER-FR-044)', () => {
    seedBucket()

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      // A WS re-attach re-delivering the identical live frame within the
      // same unbroken hold — same message_id, same session_id.
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const count = bucket.messageOrder.filter(
      (id) => bucket.messagesById[id]?.browserHandoverNoticeId === MESSAGE_ID,
    ).length
    expect(count).toBe(1)
  })

  it('inserts a SECOND, independent notice for a genuinely new hold (a different message_id)', () => {
    seedBucket()
    const secondId = 'browser-handover-01J3ZQK9XYZ0000000000000AB'

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      useChatStore.getState().handleFrame(makeFrame({ message_id: secondId, text: 'A person has taken control again.' }))
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.messagesById[MESSAGE_ID]).toBeTruthy()
    expect(bucket.messagesById[secondId]).toBeTruthy()
    expect(bucket.messagesById[secondId].content).toBe('A person has taken control again.')
  })

  it('does not stamp browserHandoverNoticeId on an ordinary user message', () => {
    seedBucket([userMessage('u1', 'hello there')])

    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.messagesById['u1'].browserHandoverNoticeId).toBeUndefined()
  })

  it('routes to the frame\'s own session_id, not necessarily the active session', () => {
    const OTHER_SID = 'browser-handover-notice-other-sid'
    act(() => {
      useChatStore.setState((state) => ({
        sessionsById: {
          ...state.sessionsById,
          [OTHER_SID]: {
            ...makeBucketMessages([]),
            toolCalls: {},
            toolCallOrder: [],
            textAtToolCallStart: {},
            isStreaming: false,
            isReplaying: false,
            replayCompletedForSession: null,
            sessionTokens: 0,
            sessionCost: 0,
            rateLimitEvent: null,
            lastUserMessageAt: null,
            cancelStage: null,
            lastReceivedEventTime: null,
            spanByParentCallId: {},
          },
        },
      }))
    })
    seedBucket() // (re)seeds SID, the active session

    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ session_id: OTHER_SID }))
    })

    const activeBucket = useChatStore.getState().sessionsById[SID]
    expect(
      activeBucket.messageOrder.some((id) => activeBucket.messagesById[id]?.browserHandoverNoticeId === MESSAGE_ID),
    ).toBe(false)
    const otherBucket = useChatStore.getState().sessionsById[OTHER_SID]
    expect(
      otherBucket.messageOrder.some((id) => otherBucket.messagesById[id]?.browserHandoverNoticeId === MESSAGE_ID),
    ).toBe(true)
  })
})
