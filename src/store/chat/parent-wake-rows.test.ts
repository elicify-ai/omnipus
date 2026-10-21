/**
 * Parent-wake visibility — store rows (spec Tests 24, 25 and 33).
 *
 * Oracle: docs/internal/specs/parent-wake-visibility-spec.md
 *   §9.3, §10, §18, §19, FR-004, FR-022.
 *   Planned tests TestChatStore_HelperEventInsertsOneRow,
 *   TestChatStore_ParentGoalOutcomeRendersInParentBucket,
 *   TestGoalOutcome_ColdRestLiveReplayOneRow.
 *
 * A helper_event frame and a replay of the same id are one row. The row
 * text does not change when activity moves from queued to running to finished.
 * An empty thread does not show the pending-save sentence. A goal_outcome
 * frame whose session is the parent renders in that bucket with a headline
 * that starts "Goal met".
 *
 * Step 4 items 2–3 are deferred to CHECK.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import type { ServerFrame } from '@/lib/ws'
import type { GoalOutcomeFrame } from '@/lib/api/generated/asyncapi-types'

const PARENT = 'parent-wake-sid'
const RESTART_SENTENCE = 'Interrupted by a restart.'
const PENDING_SENTENCE = "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work."
const ROW_ID = 'stopped-notice:parent-wake-sid:helper-1:1:1'

function helperEventFrame(messageId = ROW_ID): ServerFrame {
  return {
    type: 'helper_event',
    session_id: PARENT,
    message_id: messageId,
    helper_event: {
      helper_session_id: 'helper-1',
      kind: 'restarted',
      text: RESTART_SENTENCE,
      created_at: '2026-10-15T12:00:00Z',
    },
  } as unknown as ServerFrame
}

function texts(): string[] {
  const bucket = useChatStore.getState().sessionsById[PARENT]
  if (!bucket) return []
  return bucket.messageOrder.map((id) => bucket.messagesById[id]?.content ?? '')
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: PARENT })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
})

describe('parent wake helper_event rows', () => {
  it('keeps an empty thread empty, then one restarted row across live, replay and activity changes', () => {
    expect(texts().join('\n')).not.toContain(PENDING_SENTENCE)
    expect(texts()).not.toContain(RESTART_SENTENCE)

    act(() => {
      useChatStore.getState().handleFrame(helperEventFrame())
    })
    const afterLive = texts().filter((text) => text.includes(RESTART_SENTENCE))
    expect(afterLive, 'one helper_event row from the live frame (FR-022 / §10)').toEqual([RESTART_SENTENCE])

    act(() => {
      useChatStore.getState().handleFrame(helperEventFrame())
    })
    expect(texts().filter((text) => text.includes(RESTART_SENTENCE)), 'replay of the same id is still one row').toEqual([
      RESTART_SENTENCE,
    ])

    for (const state of ['queued', 'running', 'finished']) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'subagent_state',
          session_id: PARENT,
          state,
        } as unknown as ServerFrame)
      })
    }
    expect(texts().filter((text) => text.includes(RESTART_SENTENCE)), 'activity must not edit or duplicate the restart row').toEqual([
      RESTART_SENTENCE,
    ])
  })
})

describe('parent goal outcome bucket', () => {
  it('renders a Goal met headline in the parent session bucket', () => {
    const frame: GoalOutcomeFrame = {
      type: 'goal_outcome',
      session_id: PARENT,
      message_id: `goal-outcome-${PARENT}-goal-1-1`,
      outcome: {
        goal_id: 'goal-1',
        goal_text: 'write the marker file',
        ending: 'met',
        rounds_used: 1,
        max_rounds: 5,
        criteria_total: 1,
        judge_reason: 'The marker file was there.',
        ended_at: '2026-10-15T12:00:00Z',
      },
    }
    act(() => {
      useChatStore.getState().handleFrame(frame)
    })
    const bucket = useChatStore.getState().sessionsById[PARENT]
    expect(bucket, 'the frame session is the parent bucket').toBeTruthy()
    const row = bucket.messagesById[frame.message_id]
    expect(row?.content ?? '', 'headline starts with Goal met (FR-004 / Test 25)').toMatch(/^Goal met — /)
  })
})
