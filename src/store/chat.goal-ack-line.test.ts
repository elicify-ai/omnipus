/**
 * chat.goal-ack-line.test.ts — operator-reported UX fix, 2026-09-08 (GX-C).
 *
 * `/goal <text>` activates a goal INSTANTLY (ADR-081 D1, zero LLM calls),
 * but the operator reported watching only the generic thinking indicator
 * for 17 minutes with no sign the goal had registered, while a tool call
 * had actually failed off-screen. Fix #1: the FIRST time the store observes
 * an `active` `goal_status` frame for a given `goal_id`, it inserts one
 * quiet synthetic system message ("Goal set. Working out what done looks
 * like.") into the thread at the goal's chronological position — right
 * after the user's own `/goal <condition>` message — and never a second
 * time for the same goal_id (idempotent, including a re-emission of the
 * SAME frame on WS reattach — see pkg/agent/goal_record_wiring.go's
 * EmitGoalStatusRehydrate, extended by this same wave to cover exactly this
 * window).
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

const SID = 'goal-ack-line-test-sid'
const GOAL_ID = 'goal_01ACKLINE0000000000000000'
const ACK_TEXT = 'Goal set. Working out what done looks like.'

function makeFrame(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: SID,
    goal_id: GOAL_ID,
    condition: 'ship the release notes',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

/** Seeds a full bucket at SID with the given messages already present —
 * mirrors chat.error-terminal.test.ts's seedBucket helper. */
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

describe('goal-ack-line — insertion (operator UX fix, 2026-09-08)', () => {
  it('inserts the ack line immediately after the matching /goal user message on first active frame', () => {
    seedBucket([userMessage('u1', '/goal ship the release notes')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const order = bucket.messageOrder
    const ackId = order.find((id) => bucket.messagesById[id]?.goalAckGoalId === GOAL_ID)
    expect(ackId).toBeTruthy()
    const ackMsg = bucket.messagesById[ackId as string]
    expect(ackMsg.role).toBe('system')
    expect(ackMsg.content).toBe(ACK_TEXT)
    // Chronological position: directly after the /goal command message.
    expect(order.indexOf(ackId as string)).toBe(order.indexOf('u1') + 1)
  })

  it('does not insert a second ack line for the same goal_id on a repeated active frame (idempotent, mirrors a WS-reattach rehydrate re-emission)', () => {
    seedBucket([userMessage('u1', '/goal ship the release notes')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      useChatStore.getState().handleFrame(makeFrame({ round: 0 })) // e.g. the rehydrate re-emission
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const ackCount = bucket.messageOrder.filter((id) => bucket.messagesById[id]?.goalAckGoalId === GOAL_ID).length
    expect(ackCount).toBe(1)
  })

  it('the ack line survives once the goal record populates (later criteria-carrying frames never remove it)', () => {
    seedBucket([userMessage('u1', '/goal ship the release notes')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame()) // empty-record activation
      useChatStore.getState().handleFrame(
        makeFrame({
          criteria: [{ id: 'c1', kind: 'prose', text: 'release notes are published', judgment: 'boolean', status: 'pending', author: { kind: 'agent', id: 'tester' } }],
        }),
      ) // record now populated — goal-echo-card renders separately, driven by set_goal's own result
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const ackId = bucket.messageOrder.find((id) => bucket.messagesById[id]?.goalAckGoalId === GOAL_ID)
    expect(ackId).toBeTruthy()
    expect(bucket.messagesById[ackId as string].content).toBe(ACK_TEXT)
  })

  it('falls back to appending at the tail when no matching /goal user message is found', () => {
    seedBucket([userMessage('u1', 'hello there')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const lastId = bucket.messageOrder[bucket.messageOrder.length - 1]
    expect(bucket.messagesById[lastId]?.goalAckGoalId).toBe(GOAL_ID)
  })

  it('does not insert an ack line for a frame with no goal_id (legacy/compat emission)', () => {
    seedBucket([userMessage('u1', '/goal ship the release notes')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ goal_id: undefined }))
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const found = bucket.messageOrder.some((id) => bucket.messagesById[id]?.goalAckGoalId)
    expect(found).toBe(false)
  })

  it('does not insert an ack line for a non-active state (e.g. a terminal frame arriving without ever having been active)', () => {
    seedBucket([userMessage('u1', '/goal ship the release notes')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ state: 'done' }))
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const found = bucket.messageOrder.some((id) => bucket.messagesById[id]?.goalAckGoalId)
    expect(found).toBe(false)
  })
})

describe('goal-ack-line — reload durability (EmitGoalStatusRehydrate extension)', () => {
  it('reconstructs the ack line when a fresh bucket (simulated reload) replays the /goal message and then receives the rehydrated activation frame', () => {
    // Simulates: page reload → REST/replay repopulates the transcript
    // (the /goal user message reappears) → WS attach fires
    // EmitGoalStatusRehydrate, which (after this wave's Go fix) re-emits
    // the SAME criteria-less `active` frame even though no set_goal record
    // exists yet. Nothing here is a client-side guess — it is the identical
    // server-pushed frame shape the live path already handles.
    seedBucket([userMessage('u1', '/goal ship the release notes')])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    const ackId = bucket.messageOrder.find((id) => bucket.messagesById[id]?.goalAckGoalId === GOAL_ID)
    expect(ackId).toBeTruthy()
  })
})
