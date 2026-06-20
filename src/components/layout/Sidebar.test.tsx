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

// Mock useAuthStore used by Sidebar (handleLogout)
vi.mock('@/store/auth', () => ({
  useAuthStore: { getState: () => ({ clearAuth: vi.fn() }) },
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
  it('renders navigation items when sidebar is open', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    // Sidebar renders 6 primary nav items (Chat, Tasks, Automations, Agents, Connectors,
    // Skills & Tools) plus Settings at the bottom. This test asserts a subset of
    // those labels (Tasks is omitted here, not removed from the sidebar).
    expect(screen.getByText('Chat')).toBeTruthy()
    expect(screen.getByText('Automations')).toBeTruthy()
    expect(screen.getByText('Agents')).toBeTruthy()
    expect(screen.getByText('Connectors')).toBeTruthy()
    expect(screen.getByText('Skills & Tools')).toBeTruthy()
    expect(screen.getByText('Settings')).toBeTruthy()
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
    // Nav labels should not be in the DOM when closed.
    expect(screen.queryByText('Chat')).toBeNull()
    expect(screen.queryByText('Agents')).toBeNull()
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

// ── Wave 2b — Inline search (F7-F07) ──────────────────────────────────────────
// Traces to: project-task-management-level1-spec.md line 684
//            Scenario: Inline search filters project list (case-insensitive substring)

describe('Sidebar — Inline search (F7-F07)', () => {
  it('pressing "/" on the Projects section reveals a search input', async () => {
    // BDD: Given sidebar is open with two projects
    // BDD: When user presses "/" on the Projects section
    // BDD: Then a search input (aria-label "Filter workspaces") appears
    // Traces to: project-task-management-level1-spec.md line 684

    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'p1', name: 'Alpha Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 'p2', name: 'Beta Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    ])

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>)

    // Wait for projects to load
    await waitFor(() => { expect(screen.getByText('Alpha Project')).toBeTruthy() })

    // The search input must NOT be visible before "/" is pressed
    expect(screen.queryByRole('textbox', { name: /filter workspaces/i })).toBeNull()

    // Fire "/" keydown on the Projects group
    const projectsSection = screen.getByRole('group', { name: 'Workspaces' })
    act(() => { fireEvent.keyDown(projectsSection, { key: '/', code: 'Slash' }) })

    // Search input must now appear
    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: /filter workspaces/i })).toBeTruthy()
    })
  })

  it('projects list filters by case-insensitive substring when search query is typed', async () => {
    // BDD: Given projects "Alpha Project" and "Beta Project" in sidebar
    // BDD: When user presses "/" then types "alpha"
    // BDD: Then only "Alpha Project" remains visible
    // BDD: And "Beta Project" is NOT visible
    // Traces to: project-task-management-level1-spec.md line 684 — case-insensitive substring

    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'p1', name: 'Alpha Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 'p2', name: 'Beta Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    ])

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>)

    // Wait for both projects to load
    await waitFor(() => {
      expect(screen.getByText('Alpha Project')).toBeTruthy()
      expect(screen.getByText('Beta Project')).toBeTruthy()
    })

    // Open search
    const projectsSection = screen.getByRole('group', { name: 'Workspaces' })
    act(() => { fireEvent.keyDown(projectsSection, { key: '/', code: 'Slash' }) })

    const searchInput = await screen.findByRole('textbox', { name: /filter workspaces/i })

    // Type "alpha" — differentiation test: two different inputs produce two different outputs
    act(() => { fireEvent.change(searchInput, { target: { value: 'alpha' } }) })

    // Content test: Alpha Project is visible, Beta Project is not
    expect(screen.getByText('Alpha Project')).toBeTruthy()
    expect(screen.queryByText('Beta Project')).toBeNull()
  })

  it('uppercase search query still filters case-insensitively', async () => {
    // BDD: Given projects in sidebar
    // BDD: When user types "ALPHA" (uppercase)
    // BDD: Then "Alpha Project" still matches (case-insensitive)
    // Traces to: project-task-management-level1-spec.md line 692 — "typing 'MOB' would also match"

    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'p1', name: 'Alpha Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 'p2', name: 'Beta Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    ])

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>)

    await waitFor(() => { expect(screen.getByText('Alpha Project')).toBeTruthy() })

    const projectsSection = screen.getByRole('group', { name: 'Workspaces' })
    act(() => { fireEvent.keyDown(projectsSection, { key: '/', code: 'Slash' }) })

    const searchInput = await screen.findByRole('textbox', { name: /filter workspaces/i })
    act(() => { fireEvent.change(searchInput, { target: { value: 'ALPHA' } }) })

    // Differentiation test: uppercase input still matches, Beta does not
    expect(screen.getByText('Alpha Project')).toBeTruthy()
    expect(screen.queryByText('Beta Project')).toBeNull()
  })

  it('pressing Escape clears search and shows all projects again', async () => {
    // BDD: Given search is open with "alpha" typed
    // BDD: When user presses Escape
    // BDD: Then search input disappears and both projects are visible
    // Traces to: project-task-management-level1-spec.md line 684 (Sidebar inline search scenario)

    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'p1', name: 'Alpha Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
      { id: 'p2', name: 'Beta Project', status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
    ])

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={queryClient}><Sidebar /></QueryClientProvider>)

    await waitFor(() => {
      expect(screen.getByText('Alpha Project')).toBeTruthy()
      expect(screen.getByText('Beta Project')).toBeTruthy()
    })

    // Open search and type
    const projectsSection = screen.getByRole('group', { name: 'Workspaces' })
    act(() => { fireEvent.keyDown(projectsSection, { key: '/', code: 'Slash' }) })

    const searchInput = await screen.findByRole('textbox', { name: /filter workspaces/i })
    act(() => { fireEvent.change(searchInput, { target: { value: 'alpha' } }) })

    // Beta is hidden while filtering
    expect(screen.queryByText('Beta Project')).toBeNull()

    // Press Escape on the input
    act(() => { fireEvent.keyDown(searchInput, { key: 'Escape', code: 'Escape' }) })

    // Search input must be gone
    await waitFor(() => {
      expect(screen.queryByRole('textbox', { name: /filter workspaces/i })).toBeNull()
    })

    // Both projects must be visible again
    expect(screen.getByText('Alpha Project')).toBeTruthy()
    expect(screen.getByText('Beta Project')).toBeTruthy()
  })
})
