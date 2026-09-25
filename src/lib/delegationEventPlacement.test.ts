import { describe, expect, it } from 'vitest'
import type { DelegationEvent } from './delegationEvents.types'
import { delegationEventsAfterMessage, delegationEventsAtEnd, splitAnchoredDelegationEvents } from './delegationEventPlacement'

function event(overrides: Partial<DelegationEvent> & Pick<DelegationEvent, 'id' | 'at'>): DelegationEvent {
  return {
    kind: 'finished',
    sessionId: 'parent',
    agentName: 'Mia',
    ...overrides,
  }
}

describe('delegation event placement', () => {
  const events = [
    event({ id: 'b', at: 20, anchorMessageId: 'm1' }),
    event({ id: 'a', at: 10, anchorMessageId: 'm1' }),
    event({ id: 'c', at: 5, anchorMessageId: 'missing' }),
    event({ id: 'd', at: 30 }),
    event({ id: 'e', at: 15, anchorMessageId: 'm2' }),
  ]

  it('groups a message\'s lines after that message, earliest first', () => {
    expect(delegationEventsAfterMessage(events, 'm1').map((e) => e.id)).toEqual(['a', 'b'])
    expect(delegationEventsAfterMessage(events, 'm2').map((e) => e.id)).toEqual(['e'])
    expect(delegationEventsAfterMessage(events, 'none')).toEqual([])
  })

  it('puts lines with no anchor, or an anchor that is not on screen, at the end in time order', () => {
    const ids = new Set(['m1', 'm2'])
    expect(delegationEventsAtEnd(events, ids).map((e) => e.id)).toEqual(['c', 'd'])
  })

  it('puts a delegation and its finish on the call that has a place in the answer, finish directly after', () => {
    const message = {
      spans: [{ spanId: 'span-wire', parentCallId: 'call-wire' }],
      toolCalls: [{ id: 'call-wire', tool: 'delegate', textOffset: 12 }],
    }
    const split = splitAnchoredDelegationEvents(
      [
        event({ id: 'finished:span-wire', kind: 'finished', at: 2, anchorMessageId: 'm1' }),
        event({ id: 'delegated:span-wire', kind: 'delegated', at: 1, anchorMessageId: 'm1' }),
        event({ id: 'loose', kind: 'steered', at: 3, anchorMessageId: 'm1' }),
      ],
      'm1',
      message,
    )
    expect([...(split.byCall.get('call-wire') ?? [])].map((item) => item.id)).toEqual([
      'delegated:span-wire',
      'finished:span-wire',
    ])
    expect(split.trailing.map((item) => item.id)).toEqual(['loose'])
  })

  it('leaves a call with no known position after the message', () => {
    const split = splitAnchoredDelegationEvents(
      [event({ id: 'delegated:span-wire', kind: 'delegated', at: 1, anchorMessageId: 'm1' })],
      'm1',
      {
        spans: [{ spanId: 'span-wire', parentCallId: 'call-wire' }],
        toolCalls: [{ id: 'call-wire', tool: 'delegate' }],
      },
    )
    expect(split.byCall.size).toBe(0)
    expect(split.trailing.map((item) => item.id)).toEqual(['delegated:span-wire'])
  })

  it('keeps a background command finish on the launch call, not the later poll', () => {
    const split = splitAnchoredDelegationEvents(
      [
        event({ id: 'bash_finished:bg-1', kind: 'bash_finished', at: 2, anchorMessageId: 'm1', command: 'npm test' }),
        event({ id: 'bash_launched:bg-1', kind: 'bash_launched', at: 1, anchorMessageId: 'm1', command: 'npm test' }),
      ],
      'm1',
      {
        toolCalls: [
          { id: 'poll-1', tool: 'bash', params: { action: 'poll' }, result: { sessionId: 'bg-1', status: 'exited' }, textOffset: 4 },
          { id: 'launch-1', tool: 'bash', params: { run_in_background: true }, result: { sessionId: 'bg-1', status: 'running' }, textOffset: 4 },
        ],
      },
    )
    expect([...(split.byCall.get('launch-1') ?? [])].map((item) => item.kind)).toEqual(['bash_launched', 'bash_finished'])
    expect(split.byCall.has('poll-1')).toBe(false)
  })
})
