// sessionActivity.test.ts — FE-5 Agent-View mid-span frame store.
//
// The store receives the flat UI projections SubagentMessageFrame and
// SubagentStateFrame (forwarded from chat.ts handleFrame, judgeActivity
// precedent) and holds them keyed by `span_id` for the ActivityPanel
// session-list join. Tests cover: dedupe, cap, last-write-wins on state,
// steering-receipt ack, and the span-eviction accounting.

import { describe, it, expect, beforeEach } from 'vitest'
import { useSessionActivityStore } from './sessionActivity'
import type {
  SubagentMessageFrame,
  SubagentStateFrame,
} from '@/lib/api/generated/asyncapi-types'

function msgFrame(overrides: Partial<SubagentMessageFrame> = {}): SubagentMessageFrame {
  return {
    type: 'subagent_message',
    session_id: 'sess-parent',
    span_id: 'span_1',
    message_id: 'sm_' + Math.random().toString(36).slice(2, 8),
    kind: 'progress',
    sender_identity: 'ray',
    untrusted_origin: true,
    created_at: '2026-07-22T10:00:00Z',
    text: 'scanning files',
    ...overrides,
  } as SubagentMessageFrame
}

function stateFrame(overrides: Partial<SubagentStateFrame> = {}): SubagentStateFrame {
  return {
    type: 'subagent_state',
    session_id: 'sess-parent',
    span_id: 'span_1',
    state: 'running',
    created_at: '2026-07-22T10:00:00Z',
    ...overrides,
  } as SubagentStateFrame
}

beforeEach(() => {
  useSessionActivityStore.getState().reset()
})

describe('sessionActivity store — message ingestion', () => {
  it('apply() dispatches a subagent_message into messagesBySpan keyed by span_id', () => {
    const f = msgFrame({ span_id: 'spanA', message_id: 'sm_1', text: 'halfway', pct: 50, kind: 'progress' })
    useSessionActivityStore.getState().apply(f)
    const rows = useSessionActivityStore.getState().messagesBySpan['spanA']
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      messageId: 'sm_1',
      kind: 'progress',
      text: 'halfway',
      pct: 50,
      senderIdentity: 'ray',
      untrustedOrigin: true,
    })
  })

  it('de-dupes by message_id (at-least-once delivery / WS reconnect replay)', () => {
    const f = msgFrame({ message_id: 'sm_dup' })
    useSessionActivityStore.getState().applyMessage(f)
    useSessionActivityStore.getState().applyMessage(f)
    expect(useSessionActivityStore.getState().messagesBySpan['span_1']).toHaveLength(1)
  })

  it('caps retained messages per span at MSG_CAP_PER_SPAN (most-recent-last)', () => {
    for (let i = 0; i < 60; i++) {
      useSessionActivityStore.getState().applyMessage(msgFrame({ message_id: 'sm_' + i }))
    }
    const rows = useSessionActivityStore.getState().messagesBySpan['span_1']
    // cap is 50; the first 10 dropped, the most-recent 50 retained.
    expect(rows).toHaveLength(50)
    expect(rows[0].messageId).toBe('sm_10')
    expect(rows.at(-1)?.messageId).toBe('sm_59')
  })

  it('keeps each span independent', () => {
    useSessionActivityStore.getState().applyMessage(msgFrame({ span_id: 'spanA', message_id: 'sm_a' }))
    useSessionActivityStore.getState().applyMessage(msgFrame({ span_id: 'spanB', message_id: 'sm_b' }))
    expect(useSessionActivityStore.getState().messagesBySpan['spanA']).toHaveLength(1)
    expect(useSessionActivityStore.getState().messagesBySpan['spanB']).toHaveLength(1)
  })
})

describe('sessionActivity store — state ingestion', () => {
  it('apply() dispatches a subagent_state into stateBySpan keyed by span_id', () => {
    useSessionActivityStore.getState().apply(
      stateFrame({ span_id: 'spanA', state: 'needs_input', session_id: 'sess-child-1' }),
    )
    expect(useSessionActivityStore.getState().stateBySpan['spanA']).toMatchObject({
      state: 'needs_input',
      sessionId: 'sess-child-1',
      spanId: 'spanA',
      updatedAt: '2026-07-22T10:00:00Z',
    })
  })

  it('last-write-wins by createdAt — an older out-of-order ping is ignored', () => {
    useSessionActivityStore.getState().applyState(
      stateFrame({ span_id: 'spanA', state: 'running', created_at: '2026-07-22T10:01:00Z' }),
    )
    useSessionActivityStore.getState().applyState(
      stateFrame({ span_id: 'spanA', state: 'queued', created_at: '2026-07-22T10:00:00Z' }),
    )
    expect(useSessionActivityStore.getState().stateBySpan['spanA'].state).toBe('running')
  })

  it('records a steering-receipt ack (INV-3) and retains it across a later non-receipt ping', () => {
    useSessionActivityStore.getState().applyState(
      stateFrame({
        span_id: 'spanA',
        steering_receipt: { correlation_id: 'corr_1', applied_at: '2026-07-22T10:00:30Z' },
      }),
    )
    expect(useSessionActivityStore.getState().stateBySpan['spanA'].steeringReceipt).toEqual({
      correlationId: 'corr_1',
      appliedAt: '2026-07-22T10:00:30Z',
    })
    // a later state ping without a steering_receipt keeps the prior ack
    useSessionActivityStore.getState().applyState(
      stateFrame({ span_id: 'spanA', state: 'running', created_at: '2026-07-22T10:00:35Z' }),
    )
    expect(useSessionActivityStore.getState().stateBySpan['spanA'].steeringReceipt).toBeDefined()
  })
})

describe('sessionActivity store — forgetSpan / reset', () => {
  it('forgetSpan drops one span without touching siblings', () => {
    useSessionActivityStore.getState().applyMessage(msgFrame({ span_id: 'spanA', message_id: 'sm_a' }))
    useSessionActivityStore.getState().applyMessage(msgFrame({ span_id: 'spanB', message_id: 'sm_b' }))
    useSessionActivityStore.getState().forgetSpan('spanA')
    expect(useSessionActivityStore.getState().messagesBySpan['spanA']).toBeUndefined()
    expect(useSessionActivityStore.getState().messagesBySpan['spanB']).toHaveLength(1)
  })

  it('reset clears both maps', () => {
    useSessionActivityStore.getState().applyMessage(msgFrame({}))
    useSessionActivityStore.getState().applyState(stateFrame({}))
    useSessionActivityStore.getState().reset()
    expect(useSessionActivityStore.getState().messagesBySpan).toEqual({})
    expect(useSessionActivityStore.getState().stateBySpan).toEqual({})
  })
})
