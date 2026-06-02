import { create } from 'zustand'

import type { Notification, NotificationList } from '@/lib/api/generated/openapi-types'
import type { NotificationFrame } from '@/lib/api/generated/asyncapi-types'

// #264 — dedicated Notifications store backing the header notification center
// (bell + unread badge + dropdown). It is fed from two sources that disagree on
// one field name, and normalizes both into a single internal NotifItem shape:
//
//   • REST `Notification` (openapi)   → event class is `type`
//   • WS   `NotificationFrame` (async)→ event class is `notification_type`
//     (its `type` is the WS discriminator literal "notification")
//
// hydrate() seeds from REST on mount; apply() prepends a live WS frame and bumps
// the unread count. markRead/markAllRead manage per-user read state optimistically
// (the REST POSTs are fired by the caller; this store only mutates local state).

export interface NotifItem {
  id: string
  /** Normalized event class (REST `type` / WS `notification_type`). */
  type: Notification['type']
  title: string
  body: string
  severity: Notification['severity']
  read: boolean
  createdAtMs: number
  scheduleId?: string
  sessionId?: string
  agentId?: string
}

function fromRest(n: Notification): NotifItem {
  return {
    id: n.id,
    type: n.type,
    title: n.title,
    body: n.body ?? '',
    severity: n.severity,
    read: n.read,
    createdAtMs: n.created_at_ms,
    scheduleId: n.schedule_id,
    sessionId: n.session_id,
    agentId: n.agent_id,
  }
}

function fromFrame(f: NotificationFrame): NotifItem {
  return {
    id: f.id,
    // Normalize the WS field-name difference: WS carries the event class in
    // `notification_type` (its `type` is the "notification" discriminator).
    type: f.notification_type,
    title: f.title,
    body: f.body ?? '',
    severity: f.severity,
    read: f.read,
    createdAtMs: f.created_at_ms,
    scheduleId: f.schedule_id,
    sessionId: f.session_id,
    agentId: f.agent_id,
  }
}

interface NotificationsStore {
  /** All items by id. */
  byId: Record<string, NotifItem>
  /** Ids newest-first; drives render order. */
  order: string[]
  /** Unread count for the badge. */
  unreadCount: number

  /** Apply a live WS frame: prepend (or replace existing id) + bump unread. */
  apply: (frame: NotificationFrame) => void
  /** Replace state from a REST list (initial hydrate / refetch). */
  hydrate: (list: NotificationList) => void
  /** Mark one read; idempotent (decrements unread at most once per item). */
  markRead: (id: string) => void
  /** Mark all read; unread → 0. */
  markAllRead: () => void
}

export const useNotificationsStore = create<NotificationsStore>((set) => ({
  byId: {},
  order: [],
  unreadCount: 0,

  apply: (frame) =>
    set((s) => {
      const item = fromFrame(frame)
      const existing = s.byId[item.id]
      const byId = { ...s.byId, [item.id]: item }
      // Newest-first: a brand-new id goes to the front; a coalesced update
      // (same id re-emitted) keeps its position but refreshes its content.
      const order = existing ? s.order : [item.id, ...s.order]
      // Only an unread item contributes to the badge. A coalesced update from
      // read→unread re-arms the badge; an already-unread update leaves it.
      const wasUnread = existing ? !existing.read : false
      const isUnread = !item.read
      const unreadCount = s.unreadCount + (isUnread ? 1 : 0) - (wasUnread ? 1 : 0)
      return { byId, order, unreadCount: Math.max(0, unreadCount) }
    }),

  hydrate: (list) =>
    set(() => {
      const byId: Record<string, NotifItem> = {}
      const order: string[] = []
      // REST returns newest-first; preserve that order.
      for (const n of list.notifications) {
        const item = fromRest(n)
        byId[item.id] = item
        order.push(item.id)
      }
      return { byId, order, unreadCount: Math.max(0, list.unread_count) }
    }),

  markRead: (id) =>
    set((s) => {
      const item = s.byId[id]
      if (!item || item.read) return s // idempotent: no-op if absent or already read
      return {
        byId: { ...s.byId, [id]: { ...item, read: true } },
        unreadCount: Math.max(0, s.unreadCount - 1),
      }
    }),

  markAllRead: () =>
    set((s) => {
      if (s.unreadCount === 0) return s
      const byId: Record<string, NotifItem> = {}
      for (const id of Object.keys(s.byId)) {
        byId[id] = s.byId[id].read ? s.byId[id] : { ...s.byId[id], read: true }
      }
      return { byId, unreadCount: 0 }
    }),
}))
