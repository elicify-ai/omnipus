/**
 * chat.judge-verdict-frame.test.ts — ADR-049 D2/D4/US-13/SD-C11, plus the
 * live-thread-card fix (2026-09-14).
 *
 * `judge_verdict` was originally a GLOBAL frame with no `session_id` at all
 * (correlated by task_id/plan_id, R3). It now OPTIONALLY carries
 * `session_id` for scope=task/scope=goal
 * (contracts/components/schemas/JudgeVerdictFrame.yaml). Every frame —
 * with or without `session_id` — STILL routes into the dedicated
 * `useJudgeActivityStore` (the #283/#264 whatsapp_pairing/notification
 * pattern — accessed via getState() at frame time, never a session bucket),
 * which `useRunningActivity` reads to build a `JudgeActivityItem` for the
 * ActivityPanel; the tests below pin that this is UNCONDITIONAL. When
 * `session_id` is ALSO present, `handleFrame` additionally inserts the
 * verdict as a thread message into that session's bucket
 * (`buildJudgeVerdictInsertion`, src/lib/judgeVerdictThread.ts) — the
 * 'thread card' describe block below pins that path and its de-dup.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages } from './chat'
import { useJudgeActivityStore } from './judgeActivity'
import { useSessionStore } from './session'
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

// ── Live-thread-card fix (2026-09-14): session_id → thread message ─────────

const THREAD_SID = 'session-judge-verdict-thread'

describe('chat handleFrame → judge_verdict thread card (live-thread-card fix)', () => {
  beforeEach(() => {
    act(() => {
      useJudgeActivityStore.getState().reset()
      useChatStore.setState({ sessionsById: {} })
      useSessionStore.setState({ activeSessionId: THREAD_SID, activeAgentId: 'agent-1', activeAgentType: null })
    })
  })

  it('a live frame WITH session_id inserts exactly one thread card AND still feeds the panel', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ id: 'v-thread-1', session_id: THREAD_SID, round: 1 }))
    })

    const bucket = useChatStore.getState().sessionsById[THREAD_SID]
    expect(bucket).toBeDefined()
    const verdictMsgs = getMessages(bucket).filter((m) => m.type === 'judge_verdict')
    expect(verdictMsgs).toHaveLength(1)
    expect(verdictMsgs[0].verdict?.id).toBe('v-thread-1')
    expect(verdictMsgs[0].id).toBe('task-1-judge-1') // task_executor.go's writeJudgeVerdictTranscript id shape

    // The GLOBAL panel path is unconditional — still fed too.
    const s = useJudgeActivityStore.getState()
    expect(s.verdicts).toHaveLength(1)
    expect(s.verdicts[0].id).toBe('v-thread-1')
  })

  it('live then replay of the SAME round still shows exactly one thread card (id-based dedup)', () => {
    const frame = makeFrame({ id: 'v-thread-2', session_id: THREAD_SID, round: 2 })
    act(() => {
      useChatStore.getState().handleFrame(frame) // live
      useChatStore.getState().handleFrame({ ...frame }) // replay re-delivers the same round
    })

    const verdictMsgs = getMessages(useChatStore.getState().sessionsById[THREAD_SID])
      .filter((m) => m.type === 'judge_verdict')
    expect(verdictMsgs).toHaveLength(1)
    expect(verdictMsgs[0].id).toBe('task-1-judge-2')
  })

  it('a frame WITHOUT session_id stays panel-only — no session bucket is created or touched', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ id: 'v-panel-only' }))
    })

    expect(useChatStore.getState().sessionsById).toEqual({})
    expect(useJudgeActivityStore.getState().verdicts).toHaveLength(1)
    expect(useJudgeActivityStore.getState().verdicts[0].id).toBe('v-panel-only')
  })

  it('a scope=goal frame derives the goal-session id shape and inserts one card', () => {
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({
        id: 'v-thread-goal-1', scope: 'goal', task_id: undefined, session_id: THREAD_SID, round: 3,
      }))
    })

    const verdictMsgs = getMessages(useChatStore.getState().sessionsById[THREAD_SID])
      .filter((m) => m.type === 'judge_verdict')
    expect(verdictMsgs).toHaveLength(1)
    // goal_loop.go's writeGoalVerdictTranscript id shape: "goal-<session>-judge-<round>".
    expect(verdictMsgs[0].id).toBe(`goal-${THREAD_SID}-judge-3`)
  })
})
