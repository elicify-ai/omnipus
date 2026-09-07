/**
 * notifications.knowledge-drift.test.ts — ADR-067 FR-038a, the SPA half.
 *
 * A notification the UI drops is the same defect one layer along. The drift
 * report has to survive THREE gates before an operator sees it, and every one
 * of them was closed before this change:
 *
 *   1. the generated zod guard in `parseFrameSafe` — a NotificationFrame whose
 *      `notification_type` was not `schedule_failed` failed strict validation
 *      and was counted as a dropped frame. Silently. No bell item, no error.
 *   2. `chat.handleFrame`'s `notification` case, which forwards to the store.
 *   3. the notifications store, which normalises the WS field name.
 *
 * Each is asserted here against the requirement — "the drift check reports when
 * something is wrong" is worth nothing if the report cannot reach the screen.
 */

import { describe, it, expect, beforeEach } from 'vitest'

import { parseFrameSafe, resetDroppedFrameCount, getDroppedFrameCount } from '@/lib/ws'
import { useChatStore } from './chat'
import { useNotificationsStore } from './notifications'
import type { NotificationFrame } from '@/lib/api/generated/asyncapi-types'

const driftFrame: NotificationFrame = {
  type: 'notification',
  id: 'kdrift-1',
  notification_type: 'knowledge_drift',
  title: 'Search index for "team-vault" was out of date',
  body:
    'Omnipus checks each knowledge base against its folder on a schedule. ' +
    'Your files were not changed — only Omnipus’s own index of them was wrong.',
  severity: 'warning',
  read: false,
  created_at_ms: 1_760_000_000_000,
}

beforeEach(() => {
  resetDroppedFrameCount()
  useNotificationsStore.setState({ byId: {}, order: [], unreadCount: 0 })
})

describe('knowledge_drift notification — the SPA edge (ADR-067 FR-038a)', () => {
  it('survives the generated zod guard instead of being dropped', () => {
    const parsed = parseFrameSafe(JSON.stringify(driftFrame))

    expect(parsed).not.toBeNull()
    expect(parsed?.type).toBe('notification')
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('still rejects a notification_type outside the contract enum', () => {
    // The guard has to stay a guard: widening the enum by one value must not
    // turn it into "accept anything", or the next real drift would be
    // indistinguishable from a malformed frame.
    const parsed = parseFrameSafe(
      JSON.stringify({ ...driftFrame, notification_type: 'not_a_real_notification_type' }),
    )

    expect(parsed).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('reaches the notification centre with its type, severity and words intact', () => {
    useChatStore.getState().handleFrame(driftFrame)

    const s = useNotificationsStore.getState()
    expect(s.order).toEqual(['kdrift-1'])
    expect(s.unreadCount).toBe(1)

    const item = s.byId['kdrift-1']
    expect(item.type).toBe('knowledge_drift')
    expect(item.severity).toBe('warning')
    expect(item.title).toContain('team-vault')
    // The reassurance is the part a non-engineer actually needs; losing the
    // body would leave a bell item that says only "out of date".
    expect(item.body).toContain('Your files were not changed')
    expect(item.createdAtMs).toBe(1_760_000_000_000)
  })

  it('carries no schedule or session click-through, and does not invent one', () => {
    useChatStore.getState().handleFrame(driftFrame)

    const item = useNotificationsStore.getState().byId['kdrift-1']
    expect(item.scheduleId).toBeUndefined()
    expect(item.sessionId).toBeUndefined()
  })
})
