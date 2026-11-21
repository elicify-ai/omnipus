// Sidebar — the Export & Import entry, spec order 16 (issue #1967 B1).
// Oracle: spec US-1 ("the Assets section shows Export & Import after
// Connectors and before Library"; "entry shows as the current page"; "no
// Export & Import entry" on desktop/hosted) and the UI-states row ("hidden
// while the edition is unknown — fail closed"). Edition source:
// src/lib/api/identityMode.ts::currentIdentityMode (local / platform / null).
//
// Harness mirrors Sidebar.kb5.test.tsx's mocking approach.
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
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

// A mutable pathname so the current-page row can point at /export-import.
const mockLocation = vi.hoisted(() => ({ pathname: '/' }))
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => mockLocation,
  useNavigate: () => vi.fn(),
  Link: ({ children, to, className, ...rest }: { children: React.ReactNode; to: string; className?: string } & Record<string, unknown>) => (
    <a href={to} className={className} {...rest}>
      {children}
    </a>
  ),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore, onSignOut: () => () => {} }
})

const workspaces = [
  { id: 'ws-1', name: 'Test Workspace One', is_default: false, pinned: false },
]

vi.mock('@/lib/api', () => ({
  fetchAppState: () => Promise.resolve({ onboarding_complete: true, dev_mode_bypass: false }),
  fetchGodMode: () => Promise.resolve({ enabled: false, available: true, supported: true, persisted: false }),
  fetchWorkspaces: () => Promise.resolve(workspaces),
  fetchSessions: () => Promise.resolve([]),
  fetchAgents: () => Promise.resolve([]),
  workspacesQueryKeys: { list: (params?: unknown) => ['workspaces', params] },
  logout: vi.fn().mockResolvedValue(undefined),
}))

const mockWorkspacesState = vi.hoisted(() => ({
  activeWorkspaceId: null as string | null,
  setActiveWorkspaceId: vi.fn(),
}))
vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: (s: typeof mockWorkspacesState) => unknown) =>
    selector ? selector(mockWorkspacesState) : mockWorkspacesState,
}))

vi.mock('@/store/session', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/store/session')>()),
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
vi.mock('@/components/ui/dropdown-menu', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui/dropdown-menu')>()),
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
import { useSidebarStore } from '@/store/sidebar'
import { rememberIdentityMode } from '@/lib/api/identityMode'

function renderSidebar() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  )
}

const ENTRY = 'Export & Import'

beforeEach(() => {
  mockLocation.pathname = '/'
  // The sidebar renders its content only when open (the default store state
  // is closed).
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: false })
  })
})

describe('Sidebar — the Export & Import entry (US-1)', () => {
  it('local edition: the Assets group shows Export & Import between Connectors and Library', async () => {
    rememberIdentityMode('local')
    renderSidebar()
    const assets = await screen.findByRole('group', { name: 'Assets' })
    const links = within(assets).getAllByRole('link')
    const labels = links.map((a) => a.textContent?.trim() ?? '')
    expect(labels).toContain(ENTRY)
    const at = labels.indexOf(ENTRY)
    expect(labels[at - 1]).toBe('Connectors')
    expect(labels[at + 1]).toBe('Library')
  })

  it('local edition: the entry links to /export-import', async () => {
    rememberIdentityMode('local')
    renderSidebar()
    const assets = await screen.findByRole('group', { name: 'Assets' })
    expect(within(assets).getByRole('link', { name: ENTRY })).toHaveAttribute('href', '/export-import')
  })

  it('local edition: on /export-import the entry shows as the current page', async () => {
    rememberIdentityMode('local')
    mockLocation.pathname = '/export-import'
    renderSidebar()
    const assets = await screen.findByRole('group', { name: 'Assets' })
    expect(within(assets).getByRole('link', { name: ENTRY })).toHaveAttribute('aria-current', 'page')
  })

  it('platform edition: no Export & Import entry', async () => {
    rememberIdentityMode('platform')
    renderSidebar()
    // Settle on a link the sidebar always shows, then assert the absence.
    await screen.findByRole('link', { name: 'Connectors' })
    const assets = screen.getByRole('group', { name: 'Assets' })
    expect(within(assets).queryByRole('link', { name: ENTRY })).toBeNull()
  })

  it('edition unknown (null): the entry is hidden — fail closed', async () => {
    rememberIdentityMode(undefined)
    renderSidebar()
    await screen.findByRole('link', { name: 'Connectors' })
    const assets = screen.getByRole('group', { name: 'Assets' })
    expect(within(assets).queryByRole('link', { name: ENTRY })).toBeNull()
  })
})
