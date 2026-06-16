/**
 * chat.notification-frame.test.ts — #264 frame-routing regression.
 *
 * Asserts that handleFrame({ type: 'notification', ... }) reaches the dedicated
 * notifications store (the WS → store wiring added next to the #283
 * whatsapp_pairing case).
 */

import { describe, it, expect, beforeEach } from 'vitest'

import { useChatStore } from './chat'
import { useNotificationsStore } from './notifications'
import type { NotificationFrame } from '@/lib/api/generated/asyncapi-types'

describe('chat handleFrame → notifications store (#264)', () => {
  beforeEach(() => {
    useNotificationsStore.setState({ byId: {}, order: [], unreadCount: 0 })
  })

  it('routes a notification frame into the notifications store and normalizes the type', () => {
    const frame: NotificationFrame = {
      type: 'notification',
      id: 'n-1',
      notification_type: 'schedule_failed',
      title: 'Daily report failed',
      body: 'provider overloaded',
      severity: 'error',
      read: false,
      created_at_ms: 42,
      schedule_id: 'sched-7',
    }

    useChatStore.getState().handleFrame(frame)

    const s = useNotificationsStore.getState()
    expect(s.order).toEqual(['n-1'])
    expect(s.unreadCount).toBe(1)
    expect(s.byId['n-1'].type).toBe('schedule_failed')
    expect(s.byId['n-1'].scheduleId).toBe('sched-7')
  })
})
