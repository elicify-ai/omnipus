// chat.catchup-fixtures.test.ts: BE-DESIGN.md §8.2 — feeds each catch-up
// fixture (src/store/__fixtures__/catchup/F*.json) through the REAL
// useChatStore.handleFrame, in order, and asserts the final visible
// transcript against the fixture's own `scenario.expected` block — which is
// written from the SCENARIO DEFINITION (BE-DESIGN.md §6.4's worked
// examples), never derived from the reducer's own output. This is the
// oracle-independence rule §8.2 requires.
//
// PROVISIONAL (see src/store/__fixtures__/catchup/README.md): these three
// fixtures are hand-derived from the design doc, not recorded by Lane A's
// real gateway (pkg/gateway/catchup_fixtures_test.go, which had not landed
// on this branch's base). When Lane A's fixtures land, only the JSON files
// need replacing — this driver is written to be indifferent to their
// provenance.

import { describe, it, expect, beforeEach } from 'vitest'
import { useChatStore } from '../chat/store'
import { useSessionStore } from '../session'
import type { WsReceiveFrame } from '@/lib/ws'
import f1 from '../__fixtures__/catchup/F1-live-turn.json'
import f2 from '../__fixtures__/catchup/F2-reconnect-incremental.json'
import f3 from '../__fixtures__/catchup/F3-reconnect-snapshot.json'

interface CatchupFixture {
  provisional: boolean
  source: string
  sessionId: string
  scenario: { description: string; expected: Record<string, unknown> }
  frames: unknown[]
}

function driveFixture(fixture: CatchupFixture): void {
  useSessionStore.setState({ activeSessionId: fixture.sessionId })
  useChatStore.setState({ sessionsById: {} } as never)
  for (const frame of fixture.frames) {
    useChatStore.getState().handleFrame(frame as WsReceiveFrame)
  }
}

function bucket(sessionId: string) {
  return useChatStore.getState().sessionsById[sessionId]
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: null })
  useChatStore.setState({ sessionsById: {} } as never)
})

describe('F1 — live turn, one tab, no reconnect (baseline shape)', () => {
  const fixture = f1 as CatchupFixture

  it('produces exactly the transcript the scenario defines', () => {
    driveFixture(fixture)
    const b = bucket(fixture.sessionId)
    const msgs = b.messageOrder.map((id) => b.messagesById[id])

    expect(msgs).toHaveLength(fixture.scenario.expected.finalMessageCount as number)
    expect(msgs[0].role).toBe('user')
    expect(msgs[0].content).toBe(fixture.scenario.expected.userMessageContent)
    expect(msgs[1].role).toBe('assistant')
    expect(msgs[1].content).toBe(fixture.scenario.expected.assistantMessageContent)
    expect(msgs[1].turnId).toBe(fixture.scenario.expected.assistantTurnId)
    expect((msgs[1].tool_calls ?? []).map((tc) => tc.id)).toEqual(fixture.scenario.expected.toolCallIds)
    expect(b.isStreaming).toBe(fixture.scenario.expected.isStreamingAfter)
    expect(b.cursor?.seq).toBe(fixture.scenario.expected.finalCursorSeq)
  })
})

describe('F2 — reconnect, incremental catch-up (cursor servable)', () => {
  const fixture = f2 as CatchupFixture

  it('the catch-up tail lands on the SAME bubble as the live continuation — no duplicate', () => {
    driveFixture(fixture)
    const b = bucket(fixture.sessionId)
    const msgs = b.messageOrder.map((id) => b.messagesById[id])

    expect(msgs).toHaveLength(1)
    expect(msgs[0].content).toBe(fixture.scenario.expected.assistantMessageContent)
    expect(msgs[0].turnId).toBe(fixture.scenario.expected.assistantTurnId)
    expect(b.isStreaming).toBe(fixture.scenario.expected.isStreamingAfter)
    expect(b.isReplaying).toBe(fixture.scenario.expected.isReplayingAfter)
    expect(b.awaitingCatchUp).toBe(fixture.scenario.expected.awaitingCatchUpAfter)
    expect(b.cursor?.seq).toBe(fixture.scenario.expected.finalCursorSeq)
  })
})

describe('F3 — reconnect, snapshot catch-up (cursor not servable)', () => {
  const fixture = f3 as CatchupFixture

  it('the completed prior turn replays as history; the in-flight turn continues from the snapshot projection', () => {
    driveFixture(fixture)
    const b = bucket(fixture.sessionId)
    const msgs = b.messageOrder.map((id) => b.messagesById[id])

    expect(msgs).toHaveLength(fixture.scenario.expected.finalMessageCount as number)
    expect(msgs[0].role).toBe('user')
    expect(msgs[0].content).toBe(fixture.scenario.expected.userMessageContent)
    expect(msgs[1].content).toBe(fixture.scenario.expected.firstAssistantMessageContent)
    expect(msgs[2].content).toBe(fixture.scenario.expected.secondAssistantMessageContent)
    expect(msgs[2].turnId).toBe(fixture.scenario.expected.assistantTurnId)
    expect(b.isStreaming).toBe(fixture.scenario.expected.isStreamingAfter)
    expect(b.awaitingCatchUp).toBe(fixture.scenario.expected.awaitingCatchUpAfter)
    expect(b.cursor?.seq).toBe(fixture.scenario.expected.finalCursorSeq)
  })
})
