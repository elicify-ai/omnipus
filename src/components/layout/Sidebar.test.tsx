import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { fetchWorkspaces, fetchSessions, fetchSessionPage, logout } from '@/lib/api'
import type { Session } from '@/lib/api'

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

// Mock TanStack Router — Sidebar uses useLocation and useNavigate.
// mockNavigate is a stable module-level spy (not a fresh vi.fn() per call)
// so tests (e.g. the FR-020 sign-out test below) can assert on it.
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => mockNavigate,
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
//
// ADR-057 U24/W16f: `importOriginal` + spread (rather than a hand-rolled
// object, as before) so the REAL, pure session-tree helpers
// (`buildSessionTree`, `attachSessionChildren`, `findSessionNode`,
// `insertOrphanSessionAsRoot`) that SessionTree.tsx imports from this same
// module stay wired — Sidebar.tsx's new WorkspaceSessionTree renders through
// them on every expand, so a hand-rolled mock lacking them would throw the
// moment any test expands a workspace whose root session has children.
// `fetchSessionPage` (the network call `useSessionForest` fires per expanded
// node) is mocked explicitly, same as `fetchSessions`.
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

// Capturable spies for the workspace/session store actions + the selectSession
// handler. Declared via vi.hoisted so the vi.mock factories below (which run
// before module-level const declarations) can close over stable references —
// a plain `const` referenced directly in the mock body would be re-created
// fresh on every hook call, making it impossible for a test to assert on
// calls made from inside a later user interaction (e.g. a click handler
// captured at render time).
const { mockSetActiveWorkspaceId, mockStartNewSession, mockSelectSession } = vi.hoisted(() => ({
  mockSetActiveWorkspaceId: vi.fn(),
  mockStartNewSession: vi.fn(),
  mockSelectSession: vi.fn(),
}))

// Mock useWorkspacesStore used by Sidebar
vi.mock('@/store/workspacesStore', () => {
  const state = { activeWorkspaceId: null as string | null, setActiveWorkspaceId: mockSetActiveWorkspaceId }
  return {
    useWorkspacesStore: (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state),
  }
})

// Mock useSessionStore (used by the workspace accordion's active-session highlight
// and the "New chat" row's direct `useSessionStore.getState().startNewSession()`
// call — the mock must expose a `.getState()` static, mirroring the real
// zustand hook shape, or that call throws).
vi.mock('@/store/session', () => {
  const state = { activeSessionId: null as string | null, startNewSession: mockStartNewSession }
  const useSessionStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useSessionStore.getState = () => state
  return { useSessionStore }
})
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => mockSelectSession,
}))

// Mock useAuthStore used by Sidebar (handleLogout + username hook)
vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

// Mock useUiStore — Sidebar calls toggleNotificationPanel via the selector-
// less hook call, and (library-spec.md D-3) openLibraryPanel/openSearchModal
// via useUiStore.getState() from click handlers — the mock needs a `.getState`
// static mirroring the real zustand hook shape, or those calls throw.
// vi.hoisted (not a plain const): the vi.mock factory below is hoisted above
// this file's top-level declarations by Vitest's static transform, so a
// plain `const` here would be a temporal-dead-zone ReferenceError when the
// factory runs — same reasoning as mockSetActiveWorkspaceId etc. above.
const { mockOpenLibraryPanel, mockOpenSearchModal } = vi.hoisted(() => ({
  mockOpenLibraryPanel: vi.fn(),
  mockOpenSearchModal: vi.fn(),
}))
vi.mock('@/store/ui', () => {
  const state = {
    toggleNotificationPanel: vi.fn(),
    openLibraryPanel: mockOpenLibraryPanel,
    openSearchModal: mockOpenSearchModal,
  }
  const useUiStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useUiStore.getState = () => state
  return { useUiStore }
})

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
  mockNavigate.mockClear()
  vi.mocked(logout).mockClear()
  vi.mocked(logout).mockResolvedValue(undefined)
  vi.mocked(fetchSessions).mockResolvedValue([])
  vi.mocked(fetchSessionPage).mockReset().mockResolvedValue({ sessions: [] })
  mockSetActiveWorkspaceId.mockReset()
  mockStartNewSession.mockReset()
  mockSelectSession.mockReset()
})

// test_sidebar_overlay_rendering
// Traces to: wave0-brand-design-spec.md Scenario: Sidebar opens as overlay (US-5 AC2, FR-011)
describe('Sidebar — overlay rendering when open', () => {
  it('renders the Workspaces section + Assets when sidebar is open', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    // + an Assets section (Agents, Skills & Tools, Connectors, Library) + username trigger.
    // library-spec.md D-7: "Library" renamed the SECTION to "Assets"; the new
    // nav entry (opens the Library panel) took the "Library" name instead.
    expect(screen.getByRole('group', { name: 'Workspaces' })).toBeTruthy()
    expect(screen.getByRole('group', { name: 'Assets' })).toBeTruthy()

    expect(screen.getByText('Agents')).toBeTruthy()
    expect(screen.getByText('Skills & Tools')).toBeTruthy()
    expect(screen.getByText('Connectors')).toBeTruthy()
    expect(screen.getByTestId('sidebar-library-button')).toBeTruthy()
    expect(screen.getByTestId('sidebar-profile-trigger')).toBeTruthy()

    // Removed entries must NOT be present.
    expect(screen.queryByText('Chat')).toBeNull()
    expect(screen.queryByText('Automations')).toBeNull()
    expect(screen.queryByText('Command Center')).toBeNull()
  })

  it('the Library nav entry opens the Library panel at the virtual root (D-3 sidebar entry point)', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    mockOpenLibraryPanel.mockClear()
    fireEvent.click(screen.getByTestId('sidebar-library-button'))

    // Called with no argument — undefined workspaceId means the virtual root.
    expect(mockOpenLibraryPanel).toHaveBeenCalledWith()
  })

  it('shows the "omnipus.ai" wordmark in sidebar (gold .ai)', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    // The wordmark splits "omnipus" and ".ai" (gold) into separate spans.
    expect(screen.getByText('omnipus')).toBeTruthy()
    expect(screen.getByText('.ai')).toBeTruthy()
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

// ADR-052 FR-036: verifier-role sessions must stay hidden from the sidebar's
// session list by default. GET /sessions defaults to excluding type
// "verifier" unless include_verifier=true is passed — this is a regression
// guard that the sidebar's session query never opts in.
//
// ADR-057 W22b [grill2 M2-9, spec "SPA tests that MUST be deliberately
// inverted, never deleted"]: this test's OLD assertion was
// `toHaveBeenCalledWith()` — literally "no arguments at all". That shape
// breaks the moment FR-091/FR-092/FR-104's paging lands on fetchSessions's
// call sites, so it is DELIBERATELY INVERTED here (not deleted) to assert
// the new, still-narrower contract that actually matters: the sidebar's
// top-level query calls fetchSessions with ZERO opts (so the roots-only
// default and the excluded-verifier default both still apply by
// construction) — `toHaveBeenCalledWith()` still holds today because
// Sidebar.tsx's top-level `fetchSessions()` call is genuinely unchanged
// (paging happens one level down, per-node, via fetchSessionPage in
// useSessionForest — see the BDD-104 tree test below for that call shape).
// Restated as an explicit positive assertion instead of the previous
// "called with literally nothing" phrasing so a future paging change to
// THIS call site is caught here rather than silently drifting.
describe('Sidebar — ADR-052 FR-036: verifier sessions excluded by default', () => {
  it('calls the top-level fetchSessions() with zero options — no include_verifier, no explicit paging (roots-only default applies)', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })
    expect(vi.mocked(fetchSessions)).toHaveBeenCalledWith()
    // Positive lower bound (Rule 4): prove the mock is actually wired to the
    // component's real query, not merely never called.
    expect(vi.mocked(fetchSessions).mock.calls.length).toBeGreaterThan(0)
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

// ── Workspace session accordion ────────────────────────────────────────────
//
// Previously zero coverage: fetchSessions was mocked to [] and useSelectSession
// was a bare `() => vi.fn()` in every existing test, so the accordion's session
// rows, "New chat" row, and "More…" entry never actually rendered or fired.

const accordionWorkspace = {
  id: 'ws-accordion-1',
  name: 'Accordion Workspace',
  is_default: false,
  status: 'active' as const,
  pinned: false,
  pin_order: 0,
  task_count: 0,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
}

const accordionSession = {
  id: 'sess-accordion-1',
  agent_id: 'agent-1',
  active_agent_id: 'agent-1',
  title: 'Accordion Session One',
  type: 'chat' as const,
  workspace_id: accordionWorkspace.id,
  channel: 'webchat',
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T02:00:00Z',
  message_count: 1,
}

// A second, regular (non-heartbeat) session, MORE recently updated than
// `accordionSession` — used to prove "More…" still lands last with >=2
// session rows (the previous fixture only ever had one).
const accordionSessionTwo = {
  id: 'sess-accordion-2',
  agent_id: 'agent-1',
  active_agent_id: 'agent-1',
  title: 'Accordion Session Two',
  type: 'chat' as const,
  workspace_id: accordionWorkspace.id,
  channel: 'webchat',
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T03:00:00Z',
  message_count: 1,
}

// A heartbeat-type session, deliberately OLDER (by updated_at) than both
// regular sessions above — Sidebar.tsx's accordion sort (:382-383) sorts
// heartbeat sessions first regardless of recency, then falls back to
// recent-first. This fixture proves the sort is type-driven, not just
// recency-driven (a heartbeat session with a newer updated_at would pass a
// weaker "heartbeat sorts by recency too" test even if the type-priority
// branch were deleted).
const accordionSessionHeartbeat = {
  id: 'sess-accordion-hb',
  agent_id: 'agent-1',
  active_agent_id: 'agent-1',
  title: 'Accordion Heartbeat Session',
  type: 'heartbeat' as const,
  workspace_id: accordionWorkspace.id,
  channel: 'webchat',
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:30:00Z',
  message_count: 1,
}

// Locates the expanded accordion body (the sibling div rendered right after
// the workspace's header row) from its "Expand … sessions" toggle button.
function getAccordionBody(workspaceName: string): HTMLElement {
  // The toggle's aria-label flips between "Expand …" and "Collapse …" once
  // expanded, so match either.
  const toggleButton = screen.getByLabelText(new RegExp(`(Expand|Collapse) ${workspaceName} sessions`))
  const headerRow = toggleButton.closest('div')
  const body = headerRow?.nextElementSibling
  if (!body) throw new Error('accordion body not found — workspace group is not expanded')
  return body as HTMLElement
}

async function renderAndExpandAccordion() {
  vi.mocked(fetchWorkspaces).mockResolvedValue([accordionWorkspace] as never)
  act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
  render(<Sidebar />, { wrapper: makeWrapper() })

  const expandButton = await screen.findByLabelText('Expand Accordion Workspace sessions')
  act(() => { fireEvent.click(expandButton) })
  return expandButton
}

describe('Sidebar — workspace session accordion', () => {
  it('renders the workspace sessions once the workspace is expanded', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([accordionSession] as never)
    await renderAndExpandAccordion()

    expect(await screen.findByText('Accordion Session One')).toBeTruthy()
  })

  it('does not render sessions before the workspace is expanded', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([accordionSession] as never)
    vi.mocked(fetchWorkspaces).mockResolvedValue([accordionWorkspace] as never)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    await screen.findByText('Accordion Workspace')
    expect(screen.queryByText('Accordion Session One')).toBeNull()
  })

  it('renders the "More…" entry LAST in the accordion body, after the New-chat row and every session row', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([accordionSession] as never)
    await renderAndExpandAccordion()
    await screen.findByText('Accordion Session One')

    const body = getAccordionBody('Accordion Workspace')
    const buttons = Array.from(body.querySelectorAll('button'))
    expect(buttons.length).toBeGreaterThanOrEqual(3) // New chat, the session row, More…

    const last = buttons[buttons.length - 1]
    expect(last.textContent).toBe('More…')

    // Every other button (New chat + session rows) must precede it.
    for (const btn of buttons.slice(0, -1)) {
      expect(btn.textContent).not.toBe('More…')
    }
  })

  it('renders "More…" LAST with >=2 session rows (not just the single-session fixture above)', async () => {
    vi.mocked(fetchSessions).mockResolvedValue(
      [accordionSession, accordionSessionTwo] as never,
    )
    await renderAndExpandAccordion()
    await screen.findByText('Accordion Session One')
    await screen.findByText('Accordion Session Two')

    const body = getAccordionBody('Accordion Workspace')
    const buttons = Array.from(body.querySelectorAll('button'))
    // New chat, session one, session two, More… — at least 4 rows.
    expect(buttons.length).toBeGreaterThanOrEqual(4)

    const last = buttons[buttons.length - 1]
    expect(last.textContent).toBe('More…')
    for (const btn of buttons.slice(0, -1)) {
      expect(btn.textContent).not.toBe('More…')
    }
  })

  it('sorts heartbeat-type sessions before regular sessions regardless of recency (heartbeat-first, Sidebar.tsx accordion sort)', async () => {
    // accordionSessionHeartbeat.updated_at is OLDER than both regular
    // sessions — if the sort were purely recency-based, it would sort last,
    // not first. This proves the `s.type === 'heartbeat'` branch is real.
    vi.mocked(fetchSessions).mockResolvedValue(
      [accordionSession, accordionSessionTwo, accordionSessionHeartbeat] as never,
    )
    await renderAndExpandAccordion()
    await screen.findByText('Accordion Heartbeat Session')
    await screen.findByText('Accordion Session One')
    await screen.findByText('Accordion Session Two')

    const body = getAccordionBody('Accordion Workspace')
    const rowTitles = Array.from(body.querySelectorAll('button'))
      .map((b) => (b.textContent ?? '').trim())
      .filter((t) => t !== 'New chat' && t !== 'More…')

    // Heartbeat session must be the FIRST session row (right after "New chat").
    expect(rowTitles[0]).toContain('Accordion Heartbeat Session')
    // The two regular sessions follow, most-recently-updated first.
    expect(rowTitles[1]).toContain('Accordion Session Two')
    expect(rowTitles[2]).toContain('Accordion Session One')

    // The heartbeat row also carries the "HB" badge (the other cue for the
    // same underlying state).
    const hbRow = screen.getByText('Accordion Heartbeat Session').closest('button')
    expect(hbRow?.textContent).toContain('HB')
  })

  it('does not offer a delete/remove action for any session row in the accordion (none exists in Sidebar.tsx today)', async () => {
    // Session.protected (see src/lib/api.ts) documents that a protected
    // (heartbeat-backed) session should have its delete button HIDDEN — but
    // the accordion never renders a delete/trash action for ANY session,
    // protected or not, so there is nothing to conditionally disable here.
    // Asserting the absence directly (rather than skipping this) keeps a
    // future "add session delete to the sidebar" change honest about also
    // wiring the protected-session guard.
    vi.mocked(fetchSessions).mockResolvedValue(
      [accordionSession, accordionSessionHeartbeat] as never,
    )
    await renderAndExpandAccordion()
    await screen.findByText('Accordion Session One')
    await screen.findByText('Accordion Heartbeat Session')

    const body = getAccordionBody('Accordion Workspace')
    const interactive = Array.from(body.querySelectorAll('button, [role="button"]'))
    expect(interactive.length).toBeGreaterThan(0)

    for (const el of interactive) {
      const signal = [
        el.getAttribute('aria-label') ?? '',
        el.getAttribute('title') ?? '',
        el.textContent ?? '',
      ]
        .join(' ')
        .toLowerCase()
      expect(signal).not.toMatch(/delete|remove|trash/)
    }
  })

  it('the New-chat row exists and triggers startNewSession + workspace activation on click', async () => {
    const expandButton = await renderAndExpandAccordion()
    void expandButton

    const newChatButton = await screen.findByText('New chat')
    act(() => { fireEvent.click(newChatButton) })

    expect(mockSetActiveWorkspaceId).toHaveBeenCalledWith(accordionWorkspace.id)
    expect(mockStartNewSession).toHaveBeenCalledTimes(1)
    // Overlay (unpinned) sidebar closes after the action.
    expect(useSidebarStore.getState().isOpen).toBe(false)
  })

  it('clicking a session row calls the select-session handler with that session', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([accordionSession] as never)
    await renderAndExpandAccordion()

    const sessionRow = await screen.findByText('Accordion Session One')
    act(() => { fireEvent.click(sessionRow) })

    expect(mockSelectSession).toHaveBeenCalledTimes(1)
    expect(mockSelectSession).toHaveBeenCalledWith(
      expect.objectContaining({ id: accordionSession.id, title: accordionSession.title }),
    )
  })
})

// ADR-057 US-19/FR-093/BDD-104 (operator decision 1 — nested under parent,
// not the `verifier` hidden-with-a-flag precedent): a wide delegation
// fan-out must never evict the parent chat from the sidebar's maxVisible=9
// root budget, because delegate children are never top-level rows in the
// first place — they render nested under their real parent, collapsed by
// default, fetched only on expand (test #101
// TestSidebarTree_ParentSurvivesWideFanOut).
describe('Sidebar — ADR-057 US-19/FR-093: session tree survives a wide delegation fan-out', () => {
  function makeChild(i: number, parentId: string): Session {
    return {
      id: `child-${i}`,
      agent_id: 'agent-1',
      active_agent_id: 'agent-1',
      title: `Delegated task ${i}`,
      type: 'delegate',
      workspace_id: accordionWorkspace.id,
      created_at: '2026-04-02T00:00:00Z',
      updated_at: '2026-04-02T00:00:00Z',
      message_count: 1,
      parent_session_id: parentId,
    }
  }

  it('the parent chat stays visible and its 24 children are collapsed behind an expand affordance, not counted against the root budget', async () => {
    // Positive lower bound (Rule 4): the fixture really does carry 24
    // children before asserting anything about them being hidden — a test
    // asserting "zero children rendered" against an empty fixture would pass
    // vacuously.
    const children = Array.from({ length: 24 }, (_, i) => makeChild(i, accordionSession.id))
    expect(children).toHaveLength(24)

    // The default (non-flat) session list is roots-only per FR-091 — the
    // fixture reflects that contract: only the ROOT parent chat comes back
    // from the top-level fetchSessions() call, carrying child_count.
    vi.mocked(fetchSessions).mockResolvedValue([
      { ...accordionSession, child_count: 24 },
    ] as never)
    await renderAndExpandAccordion()

    // The parent is present — the R-9 eviction this story exists to prevent
    // would instead show 9 delegate-child rows and no parent at all.
    expect(await screen.findByText('Accordion Session One')).toBeTruthy()

    // Its children are NOT inline — an expand affordance exists instead.
    const expandChildrenBtn = await screen.findByRole('button', {
      name: 'Expand Accordion Session One delegated sessions',
    })
    expect(expandChildrenBtn).toBeTruthy()
    expect(expandChildrenBtn.getAttribute('aria-expanded')).toBe('false')
    expect(screen.queryByText('Delegated task 0')).toBeNull()
    expect(screen.queryByText('Delegated task 23')).toBeNull()

    // Expanding fetches and reveals them, nested under the parent — a single
    // request scoped to this node (BDD-103: O(expanded nodes), not O(all
    // sessions)) — never a bulk load of every session up front.
    vi.mocked(fetchSessionPage).mockResolvedValueOnce({ sessions: children })
    act(() => { fireEvent.click(expandChildrenBtn) })

    expect(await screen.findByText('Delegated task 0')).toBeTruthy()
    expect(screen.getByText('Delegated task 23')).toBeTruthy()
    expect(vi.mocked(fetchSessionPage)).toHaveBeenCalledWith(
      undefined,
      undefined,
      expect.objectContaining({ parentSessionId: accordionSession.id }),
    )
    expect(expandChildrenBtn.getAttribute('aria-expanded')).toBe('true')

    // The parent is STILL present — expanding its children never displaces it.
    expect(screen.getByText('Accordion Session One')).toBeTruthy()
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

// ── D8: stale sidebar backdrop swallows the first click ─────────────────────
// Traces to: UAT v0.1.1 D7 findings — the Usage/Profile/Settings items in the
// profile popup never called the sidebar store's close(), unlike every other
// nav link (Library items :626-628, workspace rows :596-600/:466-467, and the
// dropdown's own Notifications item :679). Radix always closes the DROPDOWN
// itself on select, but the SIDEBAR's own `isOpen` stayed true, so the
// full-viewport `aria-hidden` backdrop (Sidebar.tsx, z-30, invisible — "no
// background dimming") kept rendering over the page the user just navigated
// to, and its onClick=close() silently swallowed the very next click
// anywhere on that page (including on a target control), before finally
// closing the (already-hidden) backdrop.
describe('Sidebar — D8: profile-menu Usage/Profile/Settings close the (unpinned) sidebar on select', () => {
  it.each(['Usage', 'Profile', 'Settings'])(
    'clicking "%s" in the profile menu closes the overlay sidebar (isOpen -> false) when unpinned',
    async (label) => {
      act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
      render(<Sidebar />, { wrapper: makeWrapper() })
      await openUserMenu()

      const item = screen.getByRole('button', { name: label })
      expect(useSidebarStore.getState().isOpen).toBe(true)
      act(() => { fireEvent.click(item) })

      // D8 fix: close() must fire — on the unfixed code isOpen stays true,
      // which is exactly what leaves the invisible backdrop covering the
      // newly-navigated-to page and swallowing the next click.
      expect(useSidebarStore.getState().isOpen).toBe(false)
    },
  )

  // NON-DISCRIMINATING GUARD (test-coverage gate on fix/uat-v0.1.1-defects,
  // GAP 5) — flagged honestly rather than left looking like proof, per the
  // AgentProfile.test.tsx "reachability probe" convention (see that file's
  // "Finding A" describe block for the same pattern applied to a different
  // fix).
  //
  // What this asserts: after clicking Usage/Profile/Settings while PINNED,
  // `isOpen` is still `true`.
  //
  // Why it can't tell the fix apart from its own absence: on TODAY's fixed
  // code, each item's `onSelect={() => { if (!effectivelyPinned) close() }}`
  // (Sidebar.tsx ~690-707) is reached and correctly SKIPS `close()` because
  // `effectivelyPinned` is true — `isOpen` stays `true` as an intended
  // outcome. But on the PRE-D8 code this test is meant to guard against, these
  // three items had NO `onSelect` prop at all (see the sibling "unpinned"
  // it.each above and its own D8 header comment) — `close()` was never
  // reachable in ANY pin state, so `isOpen` ALSO stays `true`, for a
  // completely unrelated reason (the handler doesn't exist, not "the handler
  // ran and correctly declined to act"). Both code paths produce the
  // identical `isOpen === true` outcome this test checks, so it passes
  // vacuously on the broken code too — it is a reachability/regression-shape
  // sanity check, not evidence the pinned-guard branch itself executes.
  //
  // Why it can't be strengthened from here: the ONLY observable effect of
  // these items' `onSelect` in production is the guarded `close()` call
  // itself (confirmed by reading Sidebar.tsx ~690-707) — no toast, no store
  // write, no distinguishable call, is made either way. The `Link` child
  // navigates via its own `href` regardless of whether `onSelect` exists at
  // all (this file's `@tanstack/react-router` mock, ~line 48, renders a
  // plain `<a href>` with no `onSelect` wiring), so asserting on navigation
  // doesn't discriminate either. Making this test load-bearing would require
  // a production-code change to add a distinguishable side effect inside the
  // guard — out of qa-lead's test-only scope; flagging here for
  // frontend-lead rather than leaving a false sense of coverage. The
  // "unpinned" it.each immediately above IS fully discriminating (it fails
  // outright on the pre-D8 code, since `isOpen` stays `true` there instead of
  // flipping to `false`) and is what actually protects the D8 fix.
  it.each(['Usage', 'Profile', 'Settings'])(
    'NON-DISCRIMINATING: clicking "%s" in the profile menu leaves isOpen=true while PINNED (guarded-skip and never-wired-at-all are indistinguishable here — see comment above)',
    async (label) => {
      // canPin is true in this test file's matchMedia stub, so isPinned:true
      // means effectivelyPinned:true — the `if (!effectivelyPinned) close()`
      // guard must skip close() here (mirrors the existing Notifications /
      // Library-link / workspace-row convention for pinned mode).
      act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
      render(<Sidebar />, { wrapper: makeWrapper() })
      await openUserMenu()

      const item = screen.getByRole('button', { name: label })
      act(() => { fireEvent.click(item) })

      expect(useSidebarStore.getState().isOpen).toBe(true)
    },
  )
})

// ── FR-020 (grafted from hotfix/v0.1.1): sign-out revokes the server session
// before clearing local state ──────────────────────────────────────────────
describe('Sidebar — FR-020 sign-out calls server logout before local teardown', () => {
  it('calls logout(), then clearAuth(), then navigates to /login, in that order', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const { useAuthStore } = await import('@/store/auth')
    const clearAuthMock = useAuthStore.getState().clearAuth as ReturnType<typeof vi.fn>
    clearAuthMock.mockClear()

    const signOutBtn = screen.getByRole('button', { name: 'Sign out' })
    await act(async () => {
      fireEvent.click(signOutBtn)
      // Flush the logout().catch().finally() microtask chain.
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(logout).toHaveBeenCalledOnce()
    expect(clearAuthMock).toHaveBeenCalledOnce()
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/login' })

    // Order matters: a client-side-only teardown would leave the
    // omnipus-session cookie valid and replayable (spec S14).
    const logoutOrder = vi.mocked(logout).mock.invocationCallOrder[0]
    const clearAuthOrder = clearAuthMock.mock.invocationCallOrder[0]
    const navigateOrder = mockNavigate.mock.invocationCallOrder[0]
    expect(logoutOrder).toBeLessThan(clearAuthOrder)
    expect(clearAuthOrder).toBeLessThan(navigateOrder)
  })

  it('still clears local state and navigates when the server logout call fails (best-effort, never stuck)', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const { useAuthStore } = await import('@/store/auth')
    const clearAuthMock = useAuthStore.getState().clearAuth as ReturnType<typeof vi.fn>
    clearAuthMock.mockClear()
    vi.mocked(logout).mockRejectedValueOnce(new Error('network unavailable'))

    const signOutBtn = screen.getByRole('button', { name: 'Sign out' })
    await act(async () => {
      fireEvent.click(signOutBtn)
      await new Promise((resolve) => setTimeout(resolve, 0))
    })

    expect(clearAuthMock).toHaveBeenCalledOnce()
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/login' })
  })
})
