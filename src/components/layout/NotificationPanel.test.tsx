import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'

import { NotificationPanel } from './NotificationPanel'
import { useUiStore } from '@/store/ui'
import { useNotificationsStore } from '@/store/notifications'
import type { NotificationFrame } from '@/lib/api/generated/asyncapi-types'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

const markNotificationRead = vi.fn((_id: string) => Promise.resolve())
const markAllNotificationsRead = vi.fn(() => Promise.resolve())
vi.mock('@/lib/api', () => ({
  markNotificationRead: (id: string) => markNotificationRead(id),
  markAllNotificationsRead: () => markAllNotificationsRead(),
}))

function frame(over: Partial<NotificationFrame> & { id: string }): NotificationFrame {
  return {
    type: 'notification',
    notification_type: 'schedule_failed',
    title: 'Schedule failed',
    severity: 'error',
    read: false,
    created_at_ms: Date.now(),
    ...over,
  }
}

describe('NotificationPanel (#264)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useNotificationsStore.setState({ byId: {}, order: [], unreadCount: 0 })
    useUiStore.setState({ notificationPanelOpen: true })
  })

  it('renders an empty state when there are no notifications', () => {
    render(<NotificationPanel />)
    expect(screen.getByText('No notifications yet')).toBeInTheDocument()
  })

  it('renders items newest-first with title and body', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a', title: 'Older', created_at_ms: 1 }))
    store.apply(frame({ id: 'b', title: 'Newer', body: 'details', created_at_ms: 2 }))

    render(<NotificationPanel />)
    const buttons = screen.getAllByRole('button')
    // First list item button (after the "Mark all read" header button) is newest.
    expect(screen.getByText('Newer')).toBeInTheDocument()
    expect(screen.getByText('details')).toBeInTheDocument()
    expect(buttons.length).toBeGreaterThan(0)
  })

  it('mark-all-read calls the api and clears unread', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a' }))
    store.apply(frame({ id: 'b' }))

    render(<NotificationPanel />)
    fireEvent.click(screen.getByText('Mark all read'))

    expect(markAllNotificationsRead).toHaveBeenCalledTimes(1)
    expect(useNotificationsStore.getState().unreadCount).toBe(0)
  })

  it('clicking an item marks it read and navigates to its session', () => {
    useNotificationsStore.getState().apply(frame({ id: 'a', title: 'Go', session_id: 'sess-3' }))

    render(<NotificationPanel />)
    fireEvent.click(screen.getByText('Go'))

    expect(markNotificationRead).toHaveBeenCalledWith('a')
    expect(useNotificationsStore.getState().byId['a'].read).toBe(true)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/sessions/$sessionId',
      params: { sessionId: 'sess-3' },
    })
  })

  it('clicking a schedule notification navigates to command-center', () => {
    useNotificationsStore.getState().apply(frame({ id: 's', title: 'Sched', schedule_id: 'sched-1' }))

    render(<NotificationPanel />)
    fireEvent.click(screen.getByText('Sched'))

    expect(mockNavigate).toHaveBeenCalledWith({ to: '/command-center' })
  })

  it('does not render the mark-all-read action when nothing is unread', () => {
    const store = useNotificationsStore.getState()
    store.apply(frame({ id: 'a' }))
    store.markAllRead()

    render(<NotificationPanel />)
    expect(screen.queryByText('Mark all read')).not.toBeInTheDocument()
    // The read item still renders.
    const region = screen.getByText('Schedule failed')
    expect(within(region.closest('button')!).getByText('Schedule failed')).toBeInTheDocument()
  })
})
