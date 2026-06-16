import { beforeEach, describe, expect, it } from 'vitest'

import type { NotificationFrame } from '@/lib/api/generated/asyncapi-types'
import type { NotificationList } from '@/lib/api/generated/openapi-types'
import { useNotificationsStore } from '@/store/notifications'

function frame(over: Partial<NotificationFrame> & { id: string }): NotificationFrame {
  return {
    type: 'notification',
    notification_type: 'schedule_failed',
    title: 'Schedule failed',
    severity: 'error',
    read: false,
    created_at_ms: 1_000,
    ...over,
  }
}

describe('notifications store (#264)', () => {
  beforeEach(() => {
    useNotificationsStore.setState({ byId: {}, order: [], unreadCount: 0 })
  })

  it('apply() prepends, increments unread, and normalizes notification_type -> type', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a', schedule_id: 'sched-1', body: 'boom', created_at_ms: 1 }))
    store.apply(frame({ id: 'b', created_at_ms: 2 }))

    const s = useNotificationsStore.getState()
    expect(s.order).toEqual(['b', 'a']) // newest-first
    expect(s.unreadCount).toBe(2)
    // WS `notification_type` is normalized into the internal `type` field.
    expect(s.byId['a'].type).toBe('schedule_failed')
    expect(s.byId['a'].scheduleId).toBe('sched-1')
    expect(s.byId['a'].body).toBe('boom')
    expect(s.byId['a'].createdAtMs).toBe(1)
  })

  it('apply() coalesces a re-emitted id without duplicating order or double-counting unread', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a', title: 'first', created_at_ms: 1 }))
    store.apply(frame({ id: 'a', title: 'updated', created_at_ms: 5 }))

    const s = useNotificationsStore.getState()
    expect(s.order).toEqual(['a'])
    expect(s.unreadCount).toBe(1)
    expect(s.byId['a'].title).toBe('updated')
  })

  it('markRead() is idempotent and decrements unread at most once', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a' }))
    store.apply(frame({ id: 'b' }))
    expect(useNotificationsStore.getState().unreadCount).toBe(2)

    store.markRead('a')
    expect(useNotificationsStore.getState().unreadCount).toBe(1)
    expect(useNotificationsStore.getState().byId['a'].read).toBe(true)

    // Second mark is a no-op.
    store.markRead('a')
    expect(useNotificationsStore.getState().unreadCount).toBe(1)

    // Unknown id is a no-op.
    store.markRead('nope')
    expect(useNotificationsStore.getState().unreadCount).toBe(1)
  })

  it('markAllRead() clears unread and marks every item read', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a' }))
    store.apply(frame({ id: 'b' }))
    store.markAllRead()

    const s = useNotificationsStore.getState()
    expect(s.unreadCount).toBe(0)
    expect(s.byId['a'].read).toBe(true)
    expect(s.byId['b'].read).toBe(true)
  })

  it('hydrate() replaces state from a REST NotificationList (newest-first + unread_count)', () => {
    // Seed some stale live state first to prove hydrate replaces, not merges.
    useNotificationsStore.getState().apply(frame({ id: 'stale' }))

    const list: NotificationList = {
      notifications: [
        {
          id: 'n2',
          type: 'schedule_failed',
          title: 'newer',
          severity: 'warning',
          read: false,
          created_at_ms: 200,
          session_id: 'sess-9',
        },
        {
          id: 'n1',
          type: 'schedule_failed',
          title: 'older',
          severity: 'info',
          read: true,
          created_at_ms: 100,
        },
      ],
      unread_count: 1,
    }
    useNotificationsStore.getState().hydrate(list)

    const s = useNotificationsStore.getState()
    expect(s.order).toEqual(['n2', 'n1'])
    expect(s.byId['stale']).toBeUndefined()
    expect(s.unreadCount).toBe(1)
    // REST `type` maps straight onto the internal `type` field.
    expect(s.byId['n2'].type).toBe('schedule_failed')
    expect(s.byId['n2'].sessionId).toBe('sess-9')
    expect(s.byId['n1'].read).toBe(true)
  })
})
