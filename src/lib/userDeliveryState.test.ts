// S2-09: the user-message delivery states are ONE union. The three places that
// need it (REST adapter UserMessage, store ChatMessage, ConnectionStatus copy)
// must all be that same type, so adding a state in one place cannot leave a
// copy that silently rejects or cannot render it.
import { describe, expect, expectTypeOf, it } from 'vitest'
import { USER_DELIVERY_STATES, type UserDeliveryState } from './userDeliveryState'
import type { UserMessage } from '@/lib/api'
import type { ChatMessage } from '@/store/chat/types'

describe('UserDeliveryState', () => {
  it('is the same type in the REST adapter and the store', () => {
    expectTypeOf<NonNullable<UserMessage['deliveryStatus']>>().toEqualTypeOf<UserDeliveryState>()
    expectTypeOf<NonNullable<ChatMessage['deliveryStatus']>>().toEqualTypeOf<UserDeliveryState>()
  })

  it('lists every state exactly once', () => {
    expect([...USER_DELIVERY_STATES].sort()).toEqual(
      ['discarded', 'failed', 'held', 'not-performed', 'queued', 'received', 'sending', 'unconfirmed', 'working'],
    )
    expect(new Set(USER_DELIVERY_STATES).size).toBe(USER_DELIVERY_STATES.length)
  })
})
