/**
 * chat.judge-verdict-history-merge.test.ts — ADR-049 D2/D4/SD-C10.
 *
 * `mergeJudgeVerdictHistory` backfills `type: judge_verdict` entries from a
 * REST-fetched transcript into a session bucket, independent of the normal
 * `setMessages` overwrite gate. It exists because the live/replayed
 * `judge_verdict` WS frame never inserts a thread message (see
 * `chat.judge-verdict-frame.test.ts` — it is a deliberately GLOBAL frame fed
 * only to `useJudgeActivityStore`), and — reproduced live against a real
 * gateway — WS replay populating the bucket races ahead of and gates off
 * ChatScreen.tsx's REST `historyData` → `setMessages` fallback, so without
 * this backfill a verdict card never appears even after a hard reload.
 *
 * Positioning is anchored on TURN IDS, not timestamps and not raw entry ids
 * — both alternatives were live-verified broken against a real gateway:
 *   - ids: WS replay's assistant-bubble coalescing does not preserve the
 *     transcript entry's own id on the resulting ChatMessage, so an
 *     id-neighbor scan found no match and dropped every verdict at index 0;
 *   - timestamps: replay frames carry NO timestamp (pkg/gateway/replay.go
 *     sets Role/Content/AgentId/TurnId/Model only), so replay-created
 *     messages are stamped with ARRIVAL time — every bucket "timestamp" is
 *     newer than every persisted verdict, so a timestamp comparison also
 *     dumped every card at index 0.
 * The fixtures below mirror those real shapes: bucket messages carry
 * turnIds (from ReplayMessageFrame.turn_id) and junk arrival timestamps;
 * REST history entries carry turnIds (from Message.turn_id, forwarded by
 * rawToMessage) and true transcript timestamps.
 *
 * BDD:
 *   Given a bucket WS replay already populated (no verdict entry in it)
 *   When  mergeJudgeVerdictHistory runs with a REST history that DOES
 *         carry judge_verdict entries
 *   Then  each verdict is inserted immediately after the judged turn's
 *         assistant message (turn-id anchor), even when that turn's reply
 *         text repeats verbatim across rounds
 *
 *   Given the verdict entry is already in the bucket (e.g. a repeat fetch)
 *   Then  merging again is a no-op (idempotent, no duplicate)
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore, getMessages, makeBucketMessages } from './chat'
import type { ChatMessage } from './chat'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'

const SID = 'test-session-verdict-merge'

function resetStore() {
  act(() => {
    useChatStore.getState().clearStreamingState()
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
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

// ── Fixture builders mirroring the real carriers ─────────────────────────────

/** Bucket (WS-replay) message: turnId present, timestamp = ARRIVAL junk. */
function replayUserMsg(id: string, content: string): ChatMessage {
  return { id, role: 'user', content, timestamp: '2026-09-14T13:40:07.123Z', status: 'done' }
}
function replayAssistantMsg(id: string, content: string, turnId: string): ChatMessage {
  return { id, role: 'assistant', content, timestamp: '2026-09-14T13:40:08.456Z', status: 'done', turnId }
}

/** REST (rawToMessage) entry: turnId present (from Message.turn_id), true transcript timestamp. */
function restUserMsg(id: string, content: string, timestamp: string): ChatMessage {
  return { id, role: 'user', content, timestamp, status: 'done' }
}
function restAssistantMsg(id: string, content: string, timestamp: string, turnId: string): ChatMessage {
  return { id, role: 'assistant', content, timestamp, status: 'done', turnId }
}

function makeVerdictPayload(id: string, judgedAt: string, met: boolean): NonNullable<ChatMessage['verdict']> {
  return {
    id,
    scope: 'goal',
    round: 1,
    met,
    per_criterion: [{ criterion_id: 'crit-1', met, reason: met ? 'confirmed' : 'not yet' }],
    model: 'z-ai/glm-5.3',
    judged_at: judgedAt,
    judge_agent_id: 'judge',
  }
}
function verdictMsg(id: string, judgedAt: string, met: boolean): ChatMessage {
  return {
    id,
    role: 'system',
    status: 'done',
    content: JSON.stringify(makeVerdictPayload(`${id}-payload`, judgedAt, met)),
    timestamp: judgedAt,
    type: 'judge_verdict',
    verdict: makeVerdictPayload(`${id}-payload`, judgedAt, met),
  }
}

/** Seed the bucket the way WS replay leaves it — no verdict entries. */
function seedWSReplayedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        spanByParentCallId: {},
        trimmedCount: 0,
      },
    },
    messages,
    replayCompletedForSession: SID,
  }))
}

describe('mergeJudgeVerdictHistory', () => {
  it('anchors each verdict after the judged turn despite repeating reply text and junk arrival timestamps', () => {
    // Live-shape regression: three turns all replying the SAME marker text
    // (exactly what the real gateway test produced). Turn-id anchoring must
    // interleave round 1's unmet verdict between turn 2 and turn 3, and
    // round 2's met verdict after turn 3 — content matching alone would
    // resolve every "OK-DONE-VERDICT-TEST" to the LAST match, and timestamp
    // comparison would put everything at index 0 (arrival junk is newer
    // than every true verdict timestamp).
    seedWSReplayedBucket([
      replayUserMsg('u1', '/goal reply with the exact text OK-DONE-VERDICT-TEST'),
      replayAssistantMsg('a1', 'OK-DONE-VERDICT-TEST', 'mia-turn-1'),
      replayAssistantMsg('a2', 'OK-DONE-VERDICT-TEST', 'mia-turn-2'),
      replayAssistantMsg('a3', 'OK-DONE-VERDICT-TEST', 'mia-turn-3'),
    ])

    const historyMessages: ChatMessage[] = [
      restUserMsg('u1', '/goal reply with the exact text OK-DONE-VERDICT-TEST', '2026-09-14T10:47:19Z'),
      restAssistantMsg('a1', 'OK-DONE-VERDICT-TEST', '2026-09-14T10:47:54Z', 'mia-turn-1'),
      restAssistantMsg('a2', 'OK-DONE-VERDICT-TEST', '2026-09-14T10:49:46Z', 'mia-turn-2'),
      verdictMsg('goal-verdict-round1', '2026-09-14T10:49:50Z', false),
      restAssistantMsg('a3', 'OK-DONE-VERDICT-TEST', '2026-09-14T10:50:39Z', 'mia-turn-3'),
      verdictMsg('goal-verdict-round2', '2026-09-14T10:50:45Z', true),
    ]

    act(() => {
      useChatStore.getState().mergeJudgeVerdictHistory(SID, historyMessages)
    })

    const msgs = getMessages(useChatStore.getState().sessionsById[SID])
    expect(msgs.map((m) => m.id)).toEqual([
      'u1', 'a1', 'a2', 'goal-verdict-round1', 'a3', 'goal-verdict-round2',
    ])
    expect(msgs[3].type).toBe('judge_verdict')
    expect(msgs[5].type).toBe('judge_verdict')
  })

  it('falls back to content anchoring when no turn ids exist (legacy transcript)', () => {
    seedWSReplayedBucket([
      replayUserMsg('u1', '/goal do the thing'),
      replayAssistantMsg('a1', 'working on it', 'turn-irrelevant-to-rest'),
    ])

    // REST entries carry NO turnId (legacy), so anchor 2 (content) fires:
    // the verdict's nearest preceding content-bearing entry is a1.
    const historyMessages: ChatMessage[] = [
      restUserMsg('u1', '/goal do the thing', '2026-09-14T06:00:00Z'),
      restAssistantMsg('a1', 'working on it', '2026-09-14T06:01:00Z', 'turn-legacy'),
      verdictMsg('goal-verdict-1', '2026-09-14T06:02:00Z', false),
    ]
    // strip turnIds to simulate the legacy REST shape
    const legacyHistory = historyMessages.map((m) => ({ ...m, turnId: undefined }))

    act(() => {
      useChatStore.getState().mergeJudgeVerdictHistory(SID, legacyHistory)
    })

    const msgs = getMessages(useChatStore.getState().sessionsById[SID])
    expect(msgs.map((m) => m.id)).toEqual(['u1', 'a1', 'goal-verdict-1'])
  })

  it('appends at the end when no anchor matches anything in the bucket', () => {
    seedWSReplayedBucket([replayUserMsg('u1', 'hello')])

    const historyMessages: ChatMessage[] = [
      restUserMsg('x1', 'unrelated user message', '2026-09-14T06:00:00Z'),
      verdictMsg('goal-verdict-1', '2026-09-14T06:02:00Z', false),
    ]

    act(() => {
      useChatStore.getState().mergeJudgeVerdictHistory(SID, historyMessages)
    })

    const msgs = getMessages(useChatStore.getState().sessionsById[SID])
    expect(msgs.map((m) => m.id)).toEqual(['u1', 'goal-verdict-1'])
  })

  it('is idempotent — merging the same verdict twice does not duplicate it', () => {
    seedWSReplayedBucket([
      replayUserMsg('u1', '/goal do the thing'),
      replayAssistantMsg('a1', 'working on it', 'mia-turn-1'),
    ])
    const historyMessages: ChatMessage[] = [
      restUserMsg('u1', '/goal do the thing', '2026-09-14T06:00:00Z'),
      restAssistantMsg('a1', 'working on it', '2026-09-14T06:01:00Z', 'mia-turn-1'),
      verdictMsg('goal-verdict-1', '2026-09-14T06:02:00Z', false),
    ]

    act(() => {
      useChatStore.getState().mergeJudgeVerdictHistory(SID, historyMessages)
      useChatStore.getState().mergeJudgeVerdictHistory(SID, historyMessages)
    })

    const msgs = getMessages(useChatStore.getState().sessionsById[SID])
    expect(msgs.filter((m) => m.id === 'goal-verdict-1')).toHaveLength(1)
  })

  it('is a no-op when the REST history carries no judge_verdict entry', () => {
    seedWSReplayedBucket([replayUserMsg('u1', 'hello')])
    const before = getMessages(useChatStore.getState().sessionsById[SID])

    act(() => {
      useChatStore.getState().mergeJudgeVerdictHistory(SID, [
        restUserMsg('u1', 'hello', '2026-09-14T06:00:00Z'),
        restAssistantMsg('a1', 'hi', '2026-09-14T06:01:00Z', 'mia-turn-1'),
      ])
    })

    const after = getMessages(useChatStore.getState().sessionsById[SID])
    expect(after).toEqual(before)
  })
})
