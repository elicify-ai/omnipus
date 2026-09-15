// KB-5 — the active workspace must be expanded in the sidebar accordion by
// default, switching workspaces must never collapse the one just left, and a
// deliberate manual collapse of the active workspace must survive further
// re-renders.
//
// Traces to: docs/internal/defect-list-knowledge-base-ux-2026-09-08.md KB-5.
// src/components/layout/Sidebar.tsx — expandedWorkspaceIds seed + the
// active-workspace-change effect.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
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

const workspaces = [
  { id: 'ws-1', name: 'Test Workspace One', is_default: false, pinned: false },
  { id: 'ws-2', name: 'Test Workspace Two', is_default: false, pinned: false },
]

vi.mock('@/lib/api', () => ({
  fetchWorkspaces: () => Promise.resolve(workspaces),
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

vi.mock('@/store/session', () => ({
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
    aside: ({ children, className, style, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} style={style} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div className={className} onClick={onClick} {...rest}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { Sidebar } from './Sidebar'

function makeWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: false })
  })
  mockWorkspacesState.activeWorkspaceId = null
  mockWorkspacesState.setActiveWorkspaceId.mockReset()
})

describe('Sidebar — KB-5 active workspace expansion', () => {
  it('expands the active workspace on load, leaving other workspaces collapsed', async () => {
    mockWorkspacesState.activeWorkspaceId = 'ws-1'
    render(<Sidebar />, { wrapper: makeWrapper() })

    const activeToggle = await screen.findByLabelText('Collapse Test Workspace One sessions')
    expect(activeToggle.getAttribute('aria-expanded')).toBe('true')

    const inactiveToggle = screen.getByLabelText('Expand Test Workspace Two sessions')
    expect(inactiveToggle.getAttribute('aria-expanded')).toBe('false')
  })

  it('does not expand anything when no workspace is active', async () => {
    mockWorkspacesState.activeWorkspaceId = null
    render(<Sidebar />, { wrapper: makeWrapper() })

    const toggleOne = await screen.findByLabelText('Expand Test Workspace One sessions')
    const toggleTwo = screen.getByLabelText('Expand Test Workspace Two sessions')
    expect(toggleOne.getAttribute('aria-expanded')).toBe('false')
    expect(toggleTwo.getAttribute('aria-expanded')).toBe('false')
  })

  it('does not collapse the previously-active workspace when switching to a new one', async () => {
    mockWorkspacesState.activeWorkspaceId = 'ws-1'
    const { rerender } = render(<Sidebar />, { wrapper: makeWrapper() })
    await screen.findByLabelText('Collapse Test Workspace One sessions')

    mockWorkspacesState.activeWorkspaceId = 'ws-2'
    await act(async () => { rerender(<Sidebar />) })

    const toggleOne = await screen.findByLabelText('Collapse Test Workspace One sessions')
    const toggleTwo = screen.getByLabelText('Collapse Test Workspace Two sessions')
    expect(toggleOne.getAttribute('aria-expanded')).toBe('true')
    expect(toggleTwo.getAttribute('aria-expanded')).toBe('true')
  })

  it('a manual collapse of the active workspace survives a re-render', async () => {
    mockWorkspacesState.activeWorkspaceId = 'ws-1'
    const { rerender } = render(<Sidebar />, { wrapper: makeWrapper() })

    const toggle = await screen.findByLabelText('Collapse Test Workspace One sessions')
    expect(toggle.getAttribute('aria-expanded')).toBe('true')

    await act(async () => { fireEvent.click(toggle) })
    const collapsedToggle = screen.getByLabelText('Expand Test Workspace One sessions')
    expect(collapsedToggle.getAttribute('aria-expanded')).toBe('false')

    // Re-render with the SAME active workspace — a naive "always expand the
    // active workspace" effect would re-open it here; the gated one-shot
    // effect must not, since activeWorkspaceId never changed.
    await act(async () => { rerender(<Sidebar />) })

    const stillCollapsed = screen.getByLabelText('Expand Test Workspace One sessions')
    expect(stillCollapsed.getAttribute('aria-expanded')).toBe('false')
  })
})
