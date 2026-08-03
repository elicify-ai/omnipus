// ADR-057 BDD-106 / post-review Defect 4: a session whose parent_session_id
// names a session that no longer resolves (parent deleted) is classified as
// a ROOT by the server (pkg/agent/loop.go's u9FilterSessionHierarchy: "no
// parent OR a parent that no longer resolves") — but the server does NOT
// null out that stale parent_session_id field on the wire. Sidebar.tsx used
// to re-filter its root list with `!s.parent_session_id`, which silently
// re-excluded exactly this case: an orphaned session became invisible in
// every workspace, permanently, even though the server had already
// correctly classified it as a root. SearchModal.tsx already got this right
// (`isDisplayRoot`); this file proves Sidebar now agrees.
//
// New file (not appended to Sidebar.test.tsx) per this repo's per-unit
// test-file convention — mirrors that file's mock setup for the pieces this
// scenario needs.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { fetchWorkspaces, fetchSessions } from '@/lib/api'
import type { Session } from '@/lib/api'

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
}

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, onClick, className }: {
    children: React.ReactNode
    to: string
    onClick?: () => void
    className?: string
  }) => (
    <a href={to} onClick={onClick} className={className}>
      {children}
    </a>
  ),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

// Same rationale as Sidebar.test.tsx: keep the real pure session-tree
// helpers (buildSessionTree etc.) that SessionTree.tsx imports from this
// module, mock only the network calls.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    logout: vi.fn().mockResolvedValue(undefined),
    fetchSessions: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionPage: vi.fn().mockResolvedValue({ sessions: [] }),
    workspacesQueryKeys: {
      list: (params?: unknown) => ['workspaces', params],
    },
  }
})

vi.mock('@/store/workspacesStore', () => {
  const state = { activeWorkspaceId: null as string | null, setActiveWorkspaceId: vi.fn() }
  return {
    useWorkspacesStore: (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state),
  }
})

vi.mock('@/store/session', () => {
  const state = { activeSessionId: null as string | null, startNewSession: vi.fn() }
  const useSessionStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useSessionStore.getState = () => state
  return { useSessionStore }
})
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => vi.fn(),
}))

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

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
  DropdownMenuItem: ({ children, onSelect, 'aria-label': ariaLabel, className }: {
    children: React.ReactNode
    onSelect?: () => void
    'aria-label'?: string
    className?: string
  }) => (
    <button onClick={onSelect} aria-label={ariaLabel} className={className}>
      {children}
    </button>
  ),
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}))

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({
  NewWorkspaceSlideOver: () => null,
}))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, style, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} style={style} {...rest}>{children}</aside>
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

const orphanWorkspace = {
  id: 'ws-orphan-1',
  name: 'Orphan Workspace',
  is_default: false,
  status: 'active' as const,
  pinned: false,
  pin_order: 0,
  task_count: 0,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
}

// Server-shaped fixture: FR-091's orphan clause classifies this as a ROOT
// (its parent no longer resolves) but does NOT null the field — this is
// exactly what pkg/agent/loop.go's u9FilterSessionHierarchy returns.
const orphanSession: Session = {
  id: 'sess-orphan-1',
  agent_id: 'agent-1',
  active_agent_id: 'agent-1',
  title: 'Orphaned Chat',
  type: 'chat',
  workspace_id: orphanWorkspace.id,
  channel: 'webchat',
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T05:00:00Z',
  message_count: 3,
  parent_session_id: 'deleted-parent-id',
}

// Ordinary root session, no parent at all — the common case, used as a
// positive control alongside the orphan so a test asserting "the orphan
// renders" can't pass vacuously if the whole accordion body were broken.
const normalRootSession: Session = {
  id: 'sess-normal-root',
  agent_id: 'agent-1',
  active_agent_id: 'agent-1',
  title: 'Normal Root Chat',
  type: 'chat',
  workspace_id: orphanWorkspace.id,
  channel: 'webchat',
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T01:00:00Z',
  message_count: 1,
}

beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
  vi.mocked(fetchWorkspaces).mockResolvedValue([orphanWorkspace] as never)
  vi.mocked(fetchSessions).mockResolvedValue([] as never)
})

describe('Sidebar — BDD-106/Defect 4: orphaned sessions render as roots, not permanently hidden', () => {
  it('shows a session whose parent_session_id points at a since-deleted (unresolvable) session', async () => {
    // Positive lower bound (Rule 4): the fixture genuinely carries a
    // non-empty parent_session_id — proving this isn't accidentally passing
    // because the field was already unset.
    expect(orphanSession.parent_session_id).toBe('deleted-parent-id')

    vi.mocked(fetchSessions).mockResolvedValue([orphanSession, normalRootSession] as never)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const expandButton = await screen.findByLabelText('Expand Orphan Workspace sessions')
    act(() => { expandButton.click() })

    // The normal root (no parent) has always worked — proves the accordion
    // itself is rendering, so the orphan assertion below isn't vacuous.
    expect(await screen.findByText('Normal Root Chat')).toBeTruthy()
    // The orphan — the actual regression — must ALSO appear.
    expect(await screen.findByText('Orphaned Chat')).toBeTruthy()
  })

  it('does not show a stale "No sessions yet" empty state when the workspace\'s only session is an orphan', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([orphanSession] as never)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const expandButton = await screen.findByLabelText('Expand Orphan Workspace sessions')
    act(() => { expandButton.click() })

    expect(await screen.findByText('Orphaned Chat')).toBeTruthy()
    expect(screen.queryByText('No sessions yet')).toBeNull()
  })
})
