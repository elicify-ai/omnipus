import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { fetchWorkspaces, fetchSessions, logout } from '@/lib/api'

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
vi.mock('@/lib/api', () => ({
  fetchWorkspaces: vi.fn().mockResolvedValue([]),
  logout: vi.fn().mockResolvedValue(undefined),
  fetchSessions: vi.fn().mockResolvedValue([]),
  fetchAgents: vi.fn().mockResolvedValue([]),
  workspacesQueryKeys: {
    list: (params?: unknown) => ['workspaces', params],
  },
}))

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
  mockNavigate.mockClear()
  vi.mocked(logout).mockClear()
  vi.mocked(logout).mockResolvedValue(undefined)
  vi.mocked(fetchSessions).mockResolvedValue([])
  mockSetActiveWorkspaceId.mockReset()
  mockStartNewSession.mockReset()
  mockSelectSession.mockReset()
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
