import { describe, expect, it } from 'vitest'
import type { DelegationEvent } from './delegationEvents.types'
import { delegationEventsAfterMessage, delegationEventsAtEnd } from './delegationEventPlacement'

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
})
