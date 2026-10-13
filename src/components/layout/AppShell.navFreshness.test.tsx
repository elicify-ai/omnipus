/**
 * T-11 / FR-010 / BDD-01.4: AppShell, not the agent picker, refreshes the
 * agent roster and the workspace membership query on focus and on becoming
 * visible, even while those queries are still inside their stale time.
 * The membership query is the existing workspace list
 * (workspacesQueryKeys.list({ status: 'active' })), not a second store.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, NotificationList } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'

vi.mock('./Sidebar', () => ({ Sidebar: () => null }))
vi.mock('./NotificationPanel', () => ({ NotificationPanel: () => null }))
vi.mock('@/components/ui/toast-container', () => ({ ToastContainer: () => null }))
vi.mock('@/components/agents/ToolApprovalModal', () => ({ ToolApprovalModal: () => null }))
vi.mock('@/components/chat/MediaLightbox', () => ({ MediaLightbox: () => null }))
vi.mock('@/components/chat/OmnipusRuntimeProvider', () => ({
  OmnipusRuntimeProvider: ({ children }: { children?: React.ReactNode }) => children ?? null,
}))
vi.mock('@/hooks/useVersionCheck', () => ({ useVersionCheck: vi.fn() }))
vi.mock('@tanstack/react-router', () => ({
  Outlet: () => null,
  useRouter: () => ({ navigate: vi.fn() }),
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: React.ReactNode }) => React.createElement('span', null, children),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    fetchNotifications: vi.fn(),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchGodMode: vi.fn().mockResolvedValue({ enabled: false, available: false, supported: true, persisted: false }),
  }
})

import * as api from '@/lib/api'
import { AppShell } from './AppShell'

const APP_STATE: AppState = {
  onboarding_complete: true,
  dev_mode_bypass: false,
  identity: { mode: 'local', edition: 'core', signed_in: false, blocked_reason: 'signed_out' },
}

function setVisibility(state: DocumentVisibilityState) {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(state)
}

function renderShell() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } })
  return render(
    <QueryClientProvider client={client}>
      <AppShell />
    </QueryClientProvider>,
  )
}

describe('AppShell roster and member freshness (T-11, FR-010)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    setVisibility('visible')
    vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE)
    vi.mocked(api.fetchNotifications).mockResolvedValue({ notifications: [], unread_count: 0 } satisfies NotificationList)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('focus inside staleTime invalidates agents and workspace membership, and does not mount the picker', async () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderShell()
    invalidate.mockClear()

    act(() => {
      fireEvent(window, new Event('focus'))
    })

    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['agents'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
    expect(screen.queryByTestId('agent-picker-trigger')).toBeNull()
  })

  it('becoming visible inside staleTime invalidates agents and workspace membership', () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderShell()
    invalidate.mockClear()

    setVisibility('visible')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })

    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['agents'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
  })

  it('a hidden tab does not invalidate after a visible focus did', () => {
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderShell()
    invalidate.mockClear()

    act(() => {
      fireEvent(window, new Event('focus'))
    })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['agents'] })
    const afterFocus = invalidate.mock.calls.length

    setVisibility('hidden')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })
    expect(invalidate.mock.calls.length).toBe(afterFocus)
  })
})
