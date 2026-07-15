import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { fetchWorkspaces } from '@/lib/api'

// JSDOM does not implement window.matchMedia — Sidebar uses it for pin breakpoint detection.
// Return matches: true so canPin=true and the pin toggle button renders in tests.
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

// Radix DropdownMenu polyfills for jsdom (the username popup uses DropdownMenu)
if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
}

// Helper: open the username dropdown (Radix DropdownMenu opens on pointerDown)
async function openUserMenu() {
  const trigger = screen.getByTestId('sidebar-profile-trigger')
  await act(async () => {
    fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
    fireEvent.pointerUp(trigger, { button: 0, pointerType: 'mouse' })
  })
}

// Mock TanStack Router — Sidebar uses useLocation and useNavigate
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

// Mock SVG URL import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

// Mock fetchWorkspaces so the Sidebar's useQuery never hits the network in tests.
// Using vi.fn() so individual tests can override the resolved value with mockResolvedValueOnce.
vi.mock('@/lib/api', () => ({
  fetchWorkspaces: vi.fn().mockResolvedValue([]),
  fetchSessions: vi.fn().mockResolvedValue([]),
  fetchAgents: vi.fn().mockResolvedValue([]),
  workspacesQueryKeys: {
    list: (params?: unknown) => ['workspaces', params],
  },
}))

// Mock useWorkspacesStore used by Sidebar
vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: (s: unknown) => unknown) => {
    const state = { activeWorkspaceId: null, setActiveWorkspaceId: vi.fn() }
    return selector ? selector(state) : state
  },
}))

// Mock useSessionStore (used by the workspace accordion's active-session highlight)
vi.mock('@/store/session', () => ({
  useSessionStore: (selector?: (s: unknown) => unknown) => {
    const state = { activeSessionId: null, startNewSession: vi.fn() }
    return selector ? selector(state) : state
  },
}))
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => vi.fn(),
}))

// Mock useAuthStore used by Sidebar (handleLogout + username hook)
vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

// Mock useUiStore — Sidebar calls toggleNotificationPanel
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { toggleNotificationPanel: () => void }) => unknown) => {
    const state = { toggleNotificationPanel: vi.fn() }
    return selector ? selector(state) : state
  },
}))

// Mock useNotificationsStore — Sidebar reads unreadCount
vi.mock('@/store/notifications', () => ({
  useNotificationsStore: (selector?: (s: { unreadCount: number }) => unknown) => {
    const state = { unreadCount: 0 }
    return selector ? selector(state) : state
  },
}))

// Mock DropdownMenu primitives — jsdom doesn't support Radix portals
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

// Mock NewWorkspaceSlideOver — it imports more dependencies we don't need in these tests
vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({
  NewWorkspaceSlideOver: () => null,
}))

function makeWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

// Mock Framer Motion — AnimatePresence/motion renders children without animation
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

beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
  // Reset fetchWorkspaces to the default empty-list response before each test.
  vi.mocked(fetchWorkspaces).mockResolvedValue([])
})

// test_sidebar_overlay_rendering
// Traces to: wave0-brand-design-spec.md Scenario: Sidebar opens as overlay (US-5 AC2, FR-011)
describe('Sidebar — overlay rendering when open', () => {
  it('renders the Workspaces section + Library when sidebar is open', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    // + a Library section (Agents, Skills & Tools, Connectors) + username trigger.
    expect(screen.getByRole('group', { name: 'Workspaces' })).toBeTruthy()
    expect(screen.getByRole('group', { name: 'Library' })).toBeTruthy()

    expect(screen.getByText('Agents')).toBeTruthy()
    expect(screen.getByText('Skills & Tools')).toBeTruthy()
    expect(screen.getByText('Connectors')).toBeTruthy()
    expect(screen.getByTestId('sidebar-profile-trigger')).toBeTruthy()

    // Removed entries must NOT be present.
    expect(screen.queryByText('Chat')).toBeNull()
    expect(screen.queryByText('Automations')).toBeNull()
    expect(screen.queryByText('Command Center')).toBeNull()
  })

  it('shows "Omnipus" brand name in sidebar', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    expect(screen.getByText('Omnipus')).toBeTruthy()
  })

  it('renders nothing visible when sidebar is closed', () => {
    // Sidebar closed + unpinned: overlay motion aside should not render
    act(() => { useSidebarStore.setState({ isOpen: false, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    // Library labels should not be in the DOM when closed.
    expect(screen.queryByText('Agents')).toBeNull()
    expect(screen.queryByText('Connectors')).toBeNull()
  })
})

// test_sidebar_pin_icon_hidden_mobile
// Traces to: wave0-brand-design-spec.md Scenario: Pin icon hidden on phone breakpoint (US-5 AC7, FR-015)
describe('Sidebar — pin icon visibility on mobile', () => {
  // DELETED: The "hidden md:flex" CSS class test was written for an older implementation.
  // The component now uses a JS `canPin` guard (window.matchMedia) to conditionally render
  // the pin button rather than a Tailwind responsive class. The CSS-based assertion is no
  // longer valid and has been removed.

  it('shows PushPinSlash icon title when pinned', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
    const { container } = render(<Sidebar />, { wrapper: makeWrapper() })

    const pinButton = container.querySelector('button[title="Unpin sidebar"]')
    expect(pinButton).not.toBeNull()
    expect(pinButton!.getAttribute('title')).toBe('Unpin sidebar')
  })

  it('shows PushPin icon title when unpinned', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const { container } = render(<Sidebar />, { wrapper: makeWrapper() })

    const pinButton = container.querySelector('button[title="Pin sidebar"]')
    expect(pinButton).not.toBeNull()
  })
})

// Traces to: wave0-brand-design-spec.md Scenario: Pinned sidebar stays open on nav (US-5 AC6, FR-014)
describe('Sidebar — pinned mode rendering', () => {
  it('renders pinned sidebar as aside element', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
    const { container } = render(<Sidebar />, { wrapper: makeWrapper() })

    // Pinned mode renders a permanent aside (not inside AnimatePresence).
    // The old test looked for 'aside.hidden.md:flex' — that CSS class no longer exists;
    // the component uses JS conditional rendering (effectivelyPinned) instead.
    // We now verify that pinned sidebar content is present in the document.
    expect(container.querySelector('aside')).not.toBeNull()
    expect(container.querySelector('nav[aria-label="Main navigation"]')).not.toBeNull()
  })
})

// ── sprint/258 — Connectors nav item ──────────────────────────────────────────
// Traces to: sprint/258-jun-2026 — Sidebar "Connectors" nav item + /connectors route.

describe('Sidebar — Connectors nav item (sprint/258)', () => {
  it('renders a "Connectors" nav link pointing to /connectors', () => {
    // Content test: the Connectors item is present and links to the correct route.
    //
    // Note: The Sidebar.test.tsx Link mock renders <a href={to}> where to="/connectors".
    // In the real app, TanStack Router HashRouter renders "/#/connectors" — the mock
    // uses the plain route path. We assert on href="/connectors" to match the mock.
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const { container } = render(<Sidebar />, { wrapper: makeWrapper() })

    // The label text must be "Connectors".
    expect(screen.getByText('Connectors')).toBeTruthy()

    // The anchor element must have href="/connectors" (mock renders to= as href).
    const link = container.querySelector('a[href="/connectors"]') as HTMLAnchorElement | null
    expect(link).not.toBeNull()
  })

  it('Connectors link is NOT rendered when sidebar is closed', () => {
    // Differentiation test: nav items only appear in the open sidebar.
    act(() => { useSidebarStore.setState({ isOpen: false, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    expect(screen.queryByText('Connectors')).toBeNull()
  })
})

// ── Wave 2b — Archive section (F7-F06) ────────────────────────────────────────
// Traces to: project-task-management-level1-spec.md line 1033 (FR-029)
//            A8: Archive section as collapsible "▸ Archive" at bottom of sidebar Projects area; hidden by default

describe('Sidebar — Archive section (F7-F06)', () => {
  it('Archive toggle button is present but archived projects not shown by default', () => {
    // BDD: Given sidebar is open
    // BDD: When rendered with no interaction
    // BDD: Then the "Archive" toggle button exists (it is always rendered) but expanded=false
    // Traces to: project-task-management-level1-spec.md line 1033 — "hidden by default"
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    // The Archive toggle button is always present in the DOM
    const archiveToggle = screen.getByRole('button', { name: /archive/i })
    expect(archiveToggle).toBeTruthy()

    // It must NOT be expanded by default
    expect(archiveToggle.getAttribute('aria-expanded')).toBe('false')
  })

  it('Archive section shows archived project names when toggle is clicked', async () => {
    // BDD: Given sidebar is open and there is one archived project "Old Project"
    // BDD: When user clicks the Archive toggle button
    // BDD: Then "Old Project" appears in the sidebar
    // Traces to: project-task-management-level1-spec.md line 1033 (FR-029 — archived projects shown in sidebar section)

    // Active-projects query returns empty; archived-projects query returns one project.
    // fetchWorkspaces is called with { status: 'active' } for the main list and
    // { status: 'archived' } for the archive section (enabled only after archiveOpen=true).
    vi.mocked(fetchWorkspaces).mockImplementation((params?: { status?: string }) => {
      if (params?.status === 'archived') {
        return Promise.resolve([
          {
            id: 'p-archived',
            name: 'Old Project',
            status: 'archived',
            pinned: false,
            pin_order: 0,
            task_count: 0,
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
          },
        ])
      }
      return Promise.resolve([])
    })

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    // Each test needs a fresh QueryClient so the archived-projects query is not cached.
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>
    )

    // Differentiation test — before clicking, "Old Project" must NOT be visible
    expect(screen.queryByText('Old Project')).toBeNull()

    // Click the Archive toggle to expand the section
    const archiveToggle = screen.getByRole('button', { name: /archive/i })
    act(() => { fireEvent.click(archiveToggle) })

    // Content test — "Old Project" must now appear after the query resolves
    await waitFor(() => {
      expect(screen.getByText('Old Project')).toBeTruthy()
    })

    // The toggle must now show aria-expanded=true
    expect(archiveToggle.getAttribute('aria-expanded')).toBe('true')
  })
})


// ── Bottom section: Notifications + Profile ────────────────────────────────────
// Traces to: feat/0.1.0-uat-fixes — sidebar bottom section (notifications + profile)

describe('Sidebar — username popup: Notifications', () => {
  it('renders a Notifications item in the username popup', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    await openUserMenu()
    // Radix DropdownMenuItem doesn't forward data-testid; query by text
    expect(screen.getByText('Notifications')).toBeTruthy()
  })

  it('does not show the unread badge when unreadCount is 0', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    // unreadCount is 0 in the mock — the badge must not appear on the trigger
    expect(screen.queryByTestId('sidebar-notification-badge')).toBeNull()
  })

  it('Notifications item does not throw when clicked', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    await openUserMenu()
    const notifItem = screen.getByText('Notifications')
    expect(() => act(() => { fireEvent.click(notifItem) })).not.toThrow()
  })
})

describe('Sidebar — username popup: User + Sign out', () => {
  it('renders the username on the trigger button', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    const trigger = screen.getByTestId('sidebar-profile-trigger')
    expect(trigger.textContent).toContain('testuser')
  })

  it('renders Sign out in the popup when opened', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    await openUserMenu()
    expect(screen.getByText('Sign out')).toBeTruthy()
  })
})
