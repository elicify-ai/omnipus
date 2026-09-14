/**
 * chat.goal-outcome.test.ts — the store half of the goal outcome line
 * (founder decision 2026-09-14).
 *
 * The same ending reaches the SPA up to three ways, all carrying one id:
 *   - the live `goal_outcome` WS frame at the ending,
 *   - the replayed `goal_outcome` frame (WS re-attach / page reload),
 *   - the persisted `system_subtype: goal_outcome` entry on a cold REST load.
 * They must converge on exactly ONE thread line. A genuinely different
 * ending (a new id — e.g. a re-run task goal ending again) gets its own line.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, makeBucketMessages, type ChatMessage } from './chat'
import { useSessionStore } from './session'
import { useChatPreferencesStore } from './chatPreferences'
import type { GoalOutcomeFrame } from '@/lib/api/generated/asyncapi-types'

const SID = 'goal-outcome-test-sid'
const MESSAGE_ID = 'goal-outcome-goal_C-1789367371829960000'
const GOAL_TEXT = 'write pill-test.txt containing exactly PILLTEST'

function makeFrame(overrides: Partial<GoalOutcomeFrame> = {}): GoalOutcomeFrame {
  return {
    type: 'goal_outcome',
    session_id: SID,
    message_id: MESSAGE_ID,
    outcome: {
      goal_id: 'goal_C',
      goal_text: GOAL_TEXT,
      ending: 'met',
      rounds_used: 1,
      max_rounds: 20,
      criteria_total: 4,
      ended_at: '2026-09-14T05:30:34Z',
    },
    ...overrides,
  }
}

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

beforeEach(() => {
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
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

function userMessage(id: string, content: string): ChatMessage {
  return { id, role: 'user', content, timestamp: '2026-09-14T05:29:00Z', status: 'done' }
}

function outcomeMessageIds(): string[] {
  const bucket = useChatStore.getState().sessionsById[SID]
  return bucket.messageOrder.filter((id) => bucket.messagesById[id]?.goalOutcome !== undefined)
}

describe('goal_outcome frame — insertion', () => {
  it('inserts one system message carrying the outcome at the tail, with Verbose chat off', () => {
    seedBucket([userMessage('u1', `/goal ${GOAL_TEXT}`)])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket.messageOrder).toEqual(['u1', MESSAGE_ID])
    const msg = bucket.messagesById[MESSAGE_ID]
    expect(msg.role).toBe('system')
    expect(msg.goalOutcome).toEqual(makeFrame().outcome)
  })
})

describe('goal_outcome frame — de-duplication', () => {
  it('a live push followed by the replay of the same ending yields one line', () => {
    seedBucket([userMessage('u1', `/goal ${GOAL_TEXT}`)])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame()) // live
      useChatStore.getState().handleFrame(makeFrame()) // replay of the persisted entry
    })

    expect(outcomeMessageIds()).toEqual([MESSAGE_ID])
    expect(useChatStore.getState().sessionsById[SID].messageOrder).toHaveLength(2)
  })

  it('a cold-loaded (REST) outcome entry followed by the replayed frame yields one line, left as loaded', () => {
    const coldLoaded: ChatMessage = {
      id: MESSAGE_ID,
      role: 'system',
      status: 'done',
      content: 'Goal met — persisted entry content',
      timestamp: '2026-09-14T05:30:34Z',
      goalOutcome: makeFrame().outcome,
    }
    seedBucket([userMessage('u1', `/goal ${GOAL_TEXT}`), coldLoaded])

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })

    expect(outcomeMessageIds()).toEqual([MESSAGE_ID])
    expect(useChatStore.getState().sessionsById[SID].messagesById[MESSAGE_ID].content).toBe(
      'Goal met — persisted entry content',
    )
  })

  it('a different ending of the same goal (new id, e.g. a re-run task goal) gets its own line', () => {
    seedBucket()
    const secondId = 'goal-outcome-goal_C-1789367999000000000'

    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      useChatStore.getState().handleFrame(
        makeFrame({
          message_id: secondId,
          outcome: { ...makeFrame().outcome, ending: 'rounds_exhausted', rounds_used: 5, max_rounds: 5 },
        }),
      )
    })

    expect(outcomeMessageIds()).toEqual([MESSAGE_ID, secondId])
  })
})
