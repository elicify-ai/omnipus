// A replayed terminal outcome (replay_message.terminal_outcome === true) is its
// own message: reload must render it as a distinct bubble that keeps the
// frame's durable id, matching what the live path shows (live closes the
// narration bubble with `done` before the terminal notice streams).
//
// Oracle: contracts/components/schemas/ReplayMessageFrame.yaml, field
// `terminal_outcome`, and the e2e terminal-outcome spec ("one distinct durable
// terminal entry"; rendered data-message-id equals the durable entry id).
// Frames WITHOUT the field keep the same-turn merge (control test).

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { getMessages, useChatStore } from './chat'
import { WsFrame } from '@/lib/api/generated/ws-schemas'
import { useSessionStore } from './session'

const SID = 'sess-terminal-outcome'
const TURN = 'turn-1'
const AGENT = 'agent-ray'

function replay(id: string, content: string, extra: Record<string, unknown> = {}) {
  act(() => {
    useChatStore.getState().handleFrame({
      type: 'replay_message', role: 'assistant', content, id,
      agent_id: AGENT, turn_id: TURN, session_id: SID, ...extra,
    } as never)
  })
}

const assistants = () => useChatStore.getState().messages.filter((m) => m.role === 'assistant')

describe('replay_message terminal_outcome', () => {
  beforeEach(() => {
    useSessionStore.setState({ ...useSessionStore.getState(), activeSessionId: SID })
    act(() => { useChatStore.getState().resetSession() })
  })

  it('a terminal_outcome entry after same-turn narration stays a separate message with its own id', () => {
    replay('entry-narration', 'I will load the tool.')
    replay('entry-terminal', "I've reached this agent's limit.", { terminal_outcome: true })

    const list = assistants()
    expect(list.map((m) => m.id)).toEqual(['entry-narration', 'entry-terminal'])
    expect(list.map((m) => m.content)).toEqual(['I will load the tool.', "I've reached this agent's limit."])
  })

  it('control: the same pair without the field is merged into the first bubble, as before', () => {
    replay('entry-narration', 'I will load the tool.')
    replay('entry-terminal', "I've reached this agent's limit.")

    const list = assistants()
    expect(list.map((m) => m.id)).toEqual(['entry-narration'])
    expect(list[0].content).toBe("I will load the tool.\n\nI've reached this agent's limit.")
  })

  it('terminal_outcome: false behaves like an absent field', () => {
    replay('entry-narration', 'A')
    replay('entry-next', 'B', { terminal_outcome: false })
    expect(assistants().map((m) => m.id)).toEqual(['entry-narration'])
  })
})

// Oracle: SEQ-RULING.md::Decision / Implementation acceptance and chief addendum 2.
// New cases use the real generated validator, never a cast around the wire contract.
const SYSTEM_NOTICE = {
  type: 'replay_message', role: 'system', session_id: SID, id: 'system-notice',
  content: 'Harness-authored sequence fixture notice.', agent_id: AGENT, turn_id: TURN,
}

function applyValidatedFrame(raw: unknown) {
  const frame = WsFrame.parse(raw)
  if (frame.type !== 'replay_message' && frame.type !== 'session_snapshot' && frame.type !== 'catch_up_complete') {
    throw new Error('BLOCKED: replay sequence fixture did not produce a replay or snapshot frame')
  }
  act(() => useChatStore.getState().handleFrame(frame))
}

function noticeRows() {
  return getMessages(useChatStore.getState().sessionsById[SID])
    .map(({ id, role, content }) => ({ id, role, content }))
}

const EXPECTED_NOTICE = [{ id: SYSTEM_NOTICE.id, role: 'system', content: SYSTEM_NOTICE.content }]

describe('generated ReplayMessageFrame sequence contract', () => {
  it('accepts the minimum positive integer seq', () => {
    expect(WsFrame.parse({ ...SYSTEM_NOTICE, seq: 1 })).toEqual({ ...SYSTEM_NOTICE, seq: 1 })
  })

  it('accepts an unnumbered transcript/snapshot message', () => {
    expect(WsFrame.parse(SYSTEM_NOTICE)).toEqual(SYSTEM_NOTICE)
  })

  it.each([0, -1, 1.5, '1'])('rejects invalid seq %j without weakening generated validation', (seq) => {
    expect(WsFrame.safeParse({ ...SYSTEM_NOTICE, seq }).success).toBe(false)
  })

  it('rejects an unknown key even when the remaining unnumbered message is valid', () => {
    expect(WsFrame.parse(SYSTEM_NOTICE)).toEqual(SYSTEM_NOTICE)
    expect(WsFrame.safeParse({ ...SYSTEM_NOTICE, unexpected: true }).success).toBe(false)
  })
})

describe('system replay notice cursor and overlap', () => {
  beforeEach(() => {
    act(() => {
      useChatStore.getState().clearStreamingState()
      useChatStore.setState(useChatStore.getInitialState(), true)
      useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID }, true)
    })
    // #823 N4: only completed catch-up establishes a cursor; a later snapshot preserves it.
    applyValidatedFrame({
      type: 'catch_up_complete', session_id: SID, mode: 'incremental', seq: 5, boot_id: 'bundle2-boot',
    })
    applyValidatedFrame({
      type: 'session_snapshot', session_id: SID, reason: 'unknown_position', seq: 5, boot_id: 'bundle2-boot',
    })
    expect(useChatStore.getState().sessionsById[SID].cursor).toEqual({ bootId: 'bundle2-boot', seq: 5 })
  })

  it('a numbered notice advances the session cursor and retains one exact entry', () => {
    applyValidatedFrame({ ...SYSTEM_NOTICE, seq: 6 })
    expect(useChatStore.getState().sessionsById[SID].cursor).toEqual({ bootId: 'bundle2-boot', seq: 6 })
    expect(noticeRows()).toEqual(EXPECTED_NOTICE)
  })

  it('an unnumbered notice leaves the prior cursor untouched', () => {
    applyValidatedFrame(SYSTEM_NOTICE)
    expect(useChatStore.getState().sessionsById[SID].cursor).toEqual({ bootId: 'bundle2-boot', seq: 5 })
    expect(noticeRows()).toEqual(EXPECTED_NOTICE)
  })

  it('snapshot/live same-ID overlap advances before ID dedup and old-seq redelivery never adds a row', () => {
    applyValidatedFrame(SYSTEM_NOTICE)
    expect(noticeRows()).toEqual(EXPECTED_NOTICE)
    expect(useChatStore.getState().sessionsById[SID].cursor).toEqual({ bootId: 'bundle2-boot', seq: 5 })
    applyValidatedFrame({ ...SYSTEM_NOTICE, seq: 6 })
    expect(useChatStore.getState().sessionsById[SID].cursor).toEqual({ bootId: 'bundle2-boot', seq: 6 })
    expect(noticeRows()).toEqual(EXPECTED_NOTICE)
    applyValidatedFrame({ ...SYSTEM_NOTICE, seq: 6 })
    expect(noticeRows()).toEqual(EXPECTED_NOTICE)
    // Distinguish sequence rejection from ID-only deduplication: an old number
    // cannot add a row even if a different ID would otherwise evade the ID gate.
    applyValidatedFrame({ ...SYSTEM_NOTICE, seq: 6, id: 'old-seq-must-not-render' })
    expect(noticeRows()).toEqual(EXPECTED_NOTICE)
    expect(useChatStore.getState().sessionsById[SID].cursor).toEqual({ bootId: 'bundle2-boot', seq: 6 })
  })
})
