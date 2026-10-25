// SCRATCH DRAFT ONLY. Intended new primary:
// src/components/layout/NotificationPanel.goal-paused-b1.test.tsx.
// No generated future enum constant or handwritten wire interface; exercise runtime validation.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { NotificationFrame as NotificationFrameSchema } from '@/lib/api/generated/ws-schemas'
import { parseFrameSafe } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useNotificationsStore } from '@/store/notifications'
import { useUiStore } from '@/store/ui'
import { NotificationPanel } from './NotificationPanel'

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), markRead: vi.fn().mockResolvedValue(undefined), markAll: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@tanstack/react-router', async (original) => {
  const actual = await original<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mocks.navigate }
})
vi.mock('@/lib/api', () => ({ markNotificationRead: (id: string) => mocks.markRead(id), markAllNotificationsRead: () => mocks.markAll() }))

function input(kind: string) {
  return { type: 'notification', id: 'b1-runtime-frame', notification_type: kind,
    title: 'B1 paused fixture chat', body: 'blocked', severity: 'warning', read: false,
    created_at_ms: 42, session_id: 'b1-session', agent_id: 'b1-agent' }
}

describe('B1 generated closed enum to actual notification panel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useNotificationsStore.setState({ byId: {}, order: [], unreadCount: 0 })
    useUiStore.setState({ notificationPanelOpen: true })
  })
  it('positively validates an existing known notification before trusting the new-type test', () => {
    const raw = input('knowledge_drift')
    expect(NotificationFrameSchema.safeParse(raw).success).toBe(true)
    const parsed = parseFrameSafe(raw)
    expect(parsed).not.toBeNull()
    if (parsed === null || parsed.type !== 'notification') throw new Error('SETUP: actual validator did not accept known notification')
    useChatStore.getState().handleFrame(parsed)
    expect(useNotificationsStore.getState().order).toEqual(['b1-runtime-frame'])
  })
  it('accepts goal_paused at runtime then renders and links through the existing generic panel', () => {
    const raw = input('goal_paused')
    // Expected success comes from the B1 contract requirement, not today's generated enum.
    const validated = NotificationFrameSchema.safeParse(raw)
    expect(validated.success).toBe(true)
    const parsed = parseFrameSafe(raw)
    expect(parsed).not.toBeNull()
    if (parsed === null || parsed.type !== 'notification') throw new Error('B1 goal_paused rejected by actual generated runtime validator')
    useChatStore.getState().handleFrame(parsed)
    const state = useNotificationsStore.getState()
    expect(state.order).toEqual(['b1-runtime-frame'])
    expect(state.unreadCount).toBe(1)
    expect(state.byId['b1-runtime-frame']).toEqual({ id: raw.id, type: 'goal_paused', title: raw.title,
      body: raw.body, severity: 'warning', read: false, createdAtMs: 42,
      sessionId: raw.session_id, agentId: raw.agent_id, scheduleId: undefined })
    render(<NotificationPanel />)
    expect(screen.getByText(raw.title)).toBeInTheDocument()
    fireEvent.click(screen.getByText(raw.title))
    expect(mocks.markRead).toHaveBeenCalledWith(raw.id)
    expect(mocks.navigate).toHaveBeenCalledWith({ to: '/sessions/$sessionId', params: { sessionId: raw.session_id } })
    expect(useNotificationsStore.getState().byId[raw.id].read).toBe(true)
  })
  it('rejects an unrelated unknown enum so success is not an always-accepting validator', () => {
    expect(NotificationFrameSchema.safeParse(input('b1-unknown-kind')).success).toBe(false)
    expect(parseFrameSafe(input('b1-unknown-kind'))).toBeNull()
  })
})
