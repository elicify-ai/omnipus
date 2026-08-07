/**
 * chat.judge-verdict-frame.test.ts — ADR-049 D2/D4/US-13/SD-C11.
 *
 * `judge_verdict` is a GLOBAL frame (no `session_id` — correlated by
 * task_id/plan_id, R3). Mirrors `chat.notification-frame.test.ts`'s pattern:
 * asserts `handleFrame` routes the frame into the dedicated
 * `useJudgeActivityStore` (the #283/#264 whatsapp_pairing/notification
 * pattern — accessed via getState() at frame time, never a session bucket),
 * which `useRunningActivity` reads to build a `JudgeActivityItem` for the
 * ActivityPanel.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useJudgeActivityStore } from './judgeActivity'
import type { JudgeVerdictFrame } from '@/lib/api/generated/asyncapi-types'

function makeFrame(overrides: Partial<JudgeVerdictFrame> = {}): JudgeVerdictFrame {
  return {
    type: 'judge_verdict',
    id: 'verdict-1',
    scope: 'task',
    task_id: 'task-1',
    round: 2,
    met: false,
    per_criterion: [
      { criterion_id: 'crit-1', met: true, reason: 'passes' },
      { criterion_id: 'crit-2', met: false, reason: 'go test still failing' },
    ],
    model: 'z-ai/glm-5-turbo',
    judged_at: '2026-07-19T12:05:00Z',
    judge_agent_id: 'judge',
    ...overrides,
  }
}

beforeEach(() => {
  act(() => {
    useJudgeActivityStore.getState().reset()
  })
})

describe('chat handleFrame → judge activity store (ADR-049 D2/D4)', () => {
  it('routes a judge_verdict frame into the global judge activity store', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })
    const s = useJudgeActivityStore.getState()
    expect(s.verdicts).toHaveLength(1)
    expect(s.verdicts[0].id).toBe('verdict-1')
    expect(s.verdicts[0].met).toBe(false)
    expect(s.verdicts[0].per_criterion).toHaveLength(2)
  })

  it('de-dupes by verdict id (a WS reconnect could re-deliver the same push)', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ id: 'dup-1' }))
      useChatStore.getState().handleFrame(makeFrame({ id: 'dup-1', met: true }))
    })
    const s = useJudgeActivityStore.getState()
    expect(s.verdicts.filter((v) => v.id === 'dup-1')).toHaveLength(1)
    expect(s.verdicts.find((v) => v.id === 'dup-1')?.met).toBe(true)
  })

  it('caps retained verdicts at JUDGE_VERDICT_CAP (shares the panel cap, SD-C11)', () => {
    act(() => {
      for (let i = 0; i < 10; i++) {
        useChatStore.getState().handleFrame(makeFrame({ id: `v-${i}` }))
      }
    })
    expect(useJudgeActivityStore.getState().verdicts.length).toBeLessThanOrEqual(8)
    // The most recent one must survive the cap.
    expect(useJudgeActivityStore.getState().verdicts.some((v) => v.id === 'v-9')).toBe(true)
  })

  it('does not touch any chat-store session bucket (global frame, no session_id)', () => {
    act(() => {
      useChatStore.setState({ sessionsById: {} })
      useChatStore.getState().handleFrame(makeFrame())
    })
    expect(useChatStore.getState().sessionsById).toEqual({})
  })
})
