// M5 Frontend — Sidebar responsive + ARIA tests
//
// Covers three M5 accessibility and responsive behaviors:
//   1. Sidebar is overlay-only at <1024px (pin button hidden when canPin=false)
//   2. Sidebar allows pinning at ≥1024px (pin button visible when canPin=true)
//   3. ARIA labels on interactive navigation elements (sign out, pin, nav links)
//   4. ARIA attributes: aria-modal, aria-label, aria-current on active routes
//
// BDD scenarios inferred from M5 changes in src/components/layout/Sidebar.tsx.
// Traces to: src/components/layout/Sidebar.tsx — SIDEBAR_PIN_BREAKPOINT, canPin state

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import React from 'react'

// Mock TanStack Router
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, onClick, 'aria-label': ariaLabel, 'aria-current': ariaCurrent, className }: {
    children: React.ReactNode
    to: string
    onClick?: () => void
    'aria-label'?: string
    'aria-current'?: 'page' | 'step' | 'location' | 'date' | 'time' | 'true' | 'false' | boolean
    className?: string
  }) => (
    <a href={to} onClick={onClick} aria-label={ariaLabel} aria-current={ariaCurrent} className={className}>
      {children}
    </a>
  ),
}))

// Mock SVG URL import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

// Mock auth store
vi.mock('@/store/auth', () => ({
  useAuthStore: { getState: () => ({ clearAuth: vi.fn() }) },
}))

// Mock fetchWorkspaces so the Sidebar's useQuery never hits the network in tests.
vi.mock('@/lib/api', () => ({
  fetchWorkspaces: () => Promise.resolve([]),
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
    aside: ({ children, className, style, role, 'aria-modal': ariaModal, 'aria-label': ariaLabel, ...rest }: React.HTMLAttributes<HTMLElement> & { 'aria-modal'?: string; 'aria-label'?: string }) => (
      <aside className={className} style={style} role={role} aria-modal={ariaModal} aria-label={ariaLabel} {...rest}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div className={className} onClick={onClick} {...rest}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

// Import Sidebar after mocks are set up
import { Sidebar } from './Sidebar'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function mockMatchMedia(matches: boolean) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation(() => ({
      matches,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  })
}

beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
  // Default: wide viewport (≥1024px), pin is available
  mockMatchMedia(true)
})

// ---------------------------------------------------------------------------
// M5-1: Sidebar responsive — overlay <1024px, pinnable ≥1024px
// ---------------------------------------------------------------------------

// BDD: Given the viewport is narrow (<1024px),
// When the sidebar is open,
// Then the pin button is NOT rendered (canPin=false).
// Traces to: src/components/layout/Sidebar.tsx — canPin state + SIDEBAR_PIN_BREAKPOINT
describe('Sidebar — responsive: pin button hidden on narrow viewports', () => {
  it('does not render pin button when viewport < 1024px (matchMedia returns false)', () => {
    mockMatchMedia(false) // Simulate narrow viewport

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    // The pin button relies on canPin being true — at narrow width it must not appear.
    const pinButton = document.querySelector('button[aria-label="Pin sidebar"]') as HTMLButtonElement | null
    const unpinButton = document.querySelector('button[aria-label="Unpin sidebar"]') as HTMLButtonElement | null
    expect(pinButton).toBeNull()
    expect(unpinButton).toBeNull()
  })

  it('renders pin button when viewport ≥ 1024px (matchMedia returns true)', () => {
    mockMatchMedia(true) // Simulate wide viewport

    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const pinButton = screen.getByRole('button', { name: 'Pin sidebar' })
    expect(pinButton).toBeTruthy()
  })
})

// ---------------------------------------------------------------------------
// M5-2: Sidebar responsive — pinned mode renders as aside (not overlay)
// ---------------------------------------------------------------------------

// BDD: Given the viewport is ≥1024px and the sidebar is pinned,
// When the sidebar renders,
// Then it renders as a permanent aside element (not a dialog/overlay).
// Traces to: src/components/layout/Sidebar.tsx — effectivelyPinned aside element
describe('Sidebar — responsive: pinned mode renders permanent aside', () => {
  it('renders a non-overlay aside when pinned and canPin=true', () => {
    mockMatchMedia(true)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
    const { container } = render(<Sidebar />, { wrapper: makeWrapper() })

    // The pinned aside does NOT have role="dialog" — it's a permanent panel.
    const dialogAside = container.querySelector('aside[role="dialog"]')
    expect(dialogAside).toBeNull() // No dialog aside when pinned
  })

  it('renders overlay dialog aside when NOT pinned', () => {
    mockMatchMedia(true)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const { container } = render(<Sidebar />, { wrapper: makeWrapper() })

    const dialogAside = container.querySelector('aside[role="dialog"]')
    expect(dialogAside).not.toBeNull()
    expect(dialogAside?.getAttribute('aria-modal')).toBe('true')
  })
})

// ---------------------------------------------------------------------------
// M5-3: ARIA labels on interactive elements
// ---------------------------------------------------------------------------

// BDD: Given an open sidebar,
// When it renders,
// Then the "Sign out" button has aria-label="Sign out".
// Traces to: src/components/layout/Sidebar.tsx — sign out button aria-label
describe('Sidebar — ARIA labels on interactive elements', () => {
  it('sign out button has aria-label="Sign out"', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const signOutBtn = screen.getByRole('button', { name: 'Sign out' })
    expect(signOutBtn).toBeTruthy()
    expect(signOutBtn.getAttribute('aria-label')).toBe('Sign out')
  })

  it('nav element has aria-label="Main navigation"', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(nav).toBeTruthy()
  })

  it('pin button has aria-label="Pin sidebar" when unpinned', () => {
    mockMatchMedia(true)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const pinBtn = screen.getByRole('button', { name: 'Pin sidebar' })
    expect(pinBtn).toBeTruthy()
    expect(pinBtn.getAttribute('aria-label')).toBe('Pin sidebar')
  })

  it('pin button has aria-label="Unpin sidebar" when pinned', () => {
    mockMatchMedia(true)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const unpinBtn = screen.getByRole('button', { name: 'Unpin sidebar' })
    expect(unpinBtn).toBeTruthy()
    expect(unpinBtn.getAttribute('aria-label')).toBe('Unpin sidebar')
  })

  it('pin button has aria-pressed="true" when pinned', () => {
    mockMatchMedia(true)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const unpinBtn = screen.getByRole('button', { name: 'Unpin sidebar' })
    expect(unpinBtn.getAttribute('aria-pressed')).toBe('true')
  })

  it('Agents library link has aria-label="Agents"', () => {
    // IA reframe (Sprint 4): the top-level Chat link is removed; the sidebar's
    // global-library links (Agents/Connectors/Skills & Tools) carry aria-labels.
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const agentsLink = screen.getByRole('link', { name: 'Agents' })
    expect(agentsLink).toBeTruthy()
  })
})

// ---------------------------------------------------------------------------
// M5-4: ARIA current on active nav items
// ---------------------------------------------------------------------------

// BDD: Given the sidebar is open and the current route is "/" (a workspace
// redirect, not a library route), When the library nav items render, Then none
// of them is marked aria-current="page" — the active state is route-accurate.
// Traces to: src/components/layout/Sidebar.tsx — aria-current={isActive ? 'page' : undefined}
describe('Sidebar — aria-current on active library items', () => {
  it('library links are NOT active at pathname "/" (no library route matches)', () => {
    // useLocation is mocked to return { pathname: '/' }
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const agentsLink = screen.getByRole('link', { name: 'Agents' })
    const connectorsLink = screen.getByRole('link', { name: 'Connectors' })
    expect(agentsLink.getAttribute('aria-current')).toBeNull()
    expect(connectorsLink.getAttribute('aria-current')).toBeNull()
  })

  it('Settings link is not active at pathname "/"', () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    render(<Sidebar />, { wrapper: makeWrapper() })

    const settingsLink = screen.getByRole('link', { name: 'Settings' })
    expect(settingsLink.getAttribute('aria-current')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// M5-5: Differentiation test — two different states produce different rendered output
// ---------------------------------------------------------------------------

// BDD: Pinned state produces different rendered markup than unpinned state.
// This ensures the pin implementation is not a no-op.
// Traces to: src/components/layout/Sidebar.tsx — effectivelyPinned conditional render
describe('Sidebar — differentiation: pinned vs unpinned renders different structure', () => {
  it('pinned sidebar renders a non-dialog aside; overlay renders a dialog aside', () => {
    mockMatchMedia(true)

    // Pinned rendering
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })
    const { container: pinnedContainer } = render(<Sidebar />, { wrapper: makeWrapper() })
    const hasPinnedDialog = !!pinnedContainer.querySelector('aside[role="dialog"]')

    // Overlay rendering
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const { container: overlayContainer } = render(<Sidebar />, { wrapper: makeWrapper() })
    const hasOverlayDialog = !!overlayContainer.querySelector('aside[role="dialog"]')

    // Differentiation: pinned has NO dialog; overlay HAS dialog
    expect(hasPinnedDialog).toBe(false)
    expect(hasOverlayDialog).toBe(true)
    // The two states must produce different markup — proves it's not hardcoded
    expect(hasPinnedDialog).not.toBe(hasOverlayDialog)
  })
})
