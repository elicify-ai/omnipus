// R8: the REAL Sidebar starts the active-workspace lookup through the shared
// query definition, so the saved-chat route's Retry (which cancels that exact
// query) aborts the sidebar's in-flight request. Mocks follow Sidebar.kb5.
// (was: KB-5 — the active workspace must be expanded in the sidebar accordion by
// default, switching workspaces must never collapse the one just left, and a
// deliberate manual collapse of the active workspace must survive further
// re-renders.
//
// Traces to: docs/internal/defect-list-knowledge-base-ux-2026-09-08.md KB-5.
// src/components/layout/Sidebar.tsx — expandedWorkspaceIds seed + the
// active-workspace-change effect.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { queryClient } from '@/lib/queryClient'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'
import React from 'react'

if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
}

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: true,
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })),
})

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, className }: { children: React.ReactNode; to: string; className?: string }) => (
    <a href={to} className={className}>{children}</a>
  ),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

const lookup = vi.hoisted(() => ({
  signals: [] as (AbortSignal | undefined)[],
  fetchWorkspaces: (params?: { status?: string; signal?: AbortSignal }) => {
    lookup.signals.push(params?.signal)
    // Stays open until the query aborts it.
    return new Promise((_, reject) => params?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
  },
}))

vi.mock('@/lib/api', () => ({
  // God Mode pill (GodModeIndicators -> useGodModeLiveStatus) reads these
  // two; this file's wholesale api mock must define every export the Sidebar
  // import graph touches, or Vitest throws on the missing binding. Stub to a
  // definitive "off" so the pill renders nothing here.
  fetchAppState: () => Promise.resolve({ onboarding_complete: true, dev_mode_bypass: false }),
  fetchGodMode: () => Promise.resolve({ enabled: false, available: true, supported: true, persisted: false }),
  fetchWorkspaces: lookup.fetchWorkspaces,
  fetchSessions: () => Promise.resolve([]),
  fetchAgents: () => Promise.resolve([]),
  workspacesQueryKeys: { list: (params?: unknown) => ['workspaces', params] },
  logout: vi.fn().mockResolvedValue(undefined),
}))

// A mutable, hoisted store double — unlike Sidebar.m5.test.tsx's fixed
// `activeWorkspaceId: null`, KB-5 needs a value tests can set BEFORE render
// and change BETWEEN renders to simulate a workspace switch.
const mockWorkspacesState = vi.hoisted(() => ({
  activeWorkspaceId: null as string | null,
  setActiveWorkspaceId: vi.fn(),
}))
vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: (s: typeof mockWorkspacesState) => unknown) =>
    selector ? selector(mockWorkspacesState) : mockWorkspacesState,
}))

vi.mock('@/store/session', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/store/session')>(),
  useSessionStore: (selector?: (s: unknown) => unknown) => {
    const state = { activeSessionId: null, startNewSession: vi.fn() }
    return selector ? selector(state) : state
  },
}))
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => vi.fn(),
}))

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { toggleNotificationPanel: () => void }) => unknown) => {
    const state = { toggleNotificationPanel: vi.fn() }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/store/notifications', () => ({
  useNotificationsStore: (selector?: (s: { unreadCount: number }) => unknown) => {
    const state = { unreadCount: 0 }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onSelect, className }: {
    children: React.ReactNode
    onSelect?: () => void
    className?: string
  }) => (
    <button onClick={onSelect} className={className}>{children}</button>
  ),
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}))

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({
  NewWorkspaceSlideOver: () => null,
}))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div className={className} onClick={onClick} {...rest}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { Sidebar } from './Sidebar'


describe('Sidebar workspace lookup is abortable', () => {
  beforeEach(() => {
    lookup.signals.length = 0
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
  })
  it('the sidebar-started request is aborted when the lookup query is cancelled (route Retry)', async () => {
    render(<QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>)
    await vi.waitFor(() => expect(lookup.signals.length, 'positive instrument: the real Sidebar started the lookup').toBeGreaterThan(0))
    const signal = lookup.signals[0]
    expect(signal, 'the sidebar forwards the query signal to the request').toBeInstanceOf(AbortSignal)
    expect(signal!.aborted).toBe(false)
    await act(async () => { await queryClient.cancelQueries({ queryKey: activeWorkspacesQueryOptions.queryKey, exact: true }) })
    expect(signal!.aborted, 'cancelling the lookup query aborts the sidebar request').toBe(true)
    queryClient.clear()
  })
})
