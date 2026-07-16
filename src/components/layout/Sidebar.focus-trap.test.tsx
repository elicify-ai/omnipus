// Sidebar — overlay focus management (Task 1, FW-3)
//
// The overlay sidebar declares aria-modal="true" but, before this fix,
// managed no focus at all: opening it via the hamburger left focus behind
// on the hamburger, so Tab walked straight into the background content the
// drawer had just covered (the drawer is DOM-earlier than the main
// content). This file covers the WAI-ARIA modal dialog pattern pieces added
// to close that gap:
//   1. On open, focus moves to the drawer's first focusable control.
//   2. While open, Tab from the last focusable control wraps to the first
//      (and Shift+Tab from the first wraps to the last) — a real trap.
//   3. Escape still closes the drawer and restores focus to the hamburger
//      (pre-existing behavior — confirmed here it still works alongside the
//      new trap).
//
// Traces to: src/components/layout/Sidebar.tsx L237-282 (the two effects:
// hamburger-restore, then the new focus-trap effect) and L697+ (the
// role="dialog" aria-modal="true" motion.aside, id="sidebar-overlay-panel").

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import React from 'react'

// Radix DropdownMenu polyfills for jsdom
if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
}

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

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

vi.mock('@/lib/api', () => ({
  fetchWorkspaces: () => Promise.resolve([]),
  fetchSessions: () => Promise.resolve([]),
  fetchAgents: () => Promise.resolve([]),
  workspacesQueryKeys: {
    list: (params?: unknown) => ['workspaces', params],
  },
}))

vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: (s: unknown) => unknown) => {
    const state = { activeWorkspaceId: null, setActiveWorkspaceId: vi.fn() }
    return selector ? selector(state) : state
  },
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

function makeWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

// Framer Motion mock — must forward `id` (via ...rest) since the fix keys
// the focus-trap effect's DOM lookup off id="sidebar-overlay-panel" rather
// than a ref (a plain function-component test double can't receive one).
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

// A minimal stand-in for ScreenHeader's real hamburger — Sidebar's own
// restore-on-close effect looks it up by id="sidebar-hamburger" regardless
// of which component renders it.
function Hamburger() {
  return <button id="sidebar-hamburger" type="button" aria-label="Toggle navigation sidebar" />
}

function renderOpenOverlay() {
  return render(
    <>
      <Hamburger />
      <Sidebar />
    </>,
    { wrapper: makeWrapper() },
  )
}

beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
  mockMatchMedia(true) // wide viewport — irrelevant here since isPinned stays false
})

describe('Sidebar overlay — focus enters the drawer on open', () => {
  it('moves focus to the drawer\'s first focusable control when opened', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: false })
    })
    renderOpenOverlay()

    const searchButton = screen.getByRole('button', { name: 'Search sessions' })
    expect(document.activeElement).toBe(searchButton)
  })

  it('does NOT move focus anywhere when the sidebar is closed', () => {
    renderOpenOverlay()
    // Nothing inside the (unrendered) drawer can have focus; the hamburger
    // itself was never focus()'d by Sidebar.
    const hamburger = document.getElementById('sidebar-hamburger')
    expect(document.activeElement).not.toBe(
      screen.queryByRole('button', { name: 'Search sessions' })
    )
    expect(document.activeElement).not.toBe(hamburger)
  })
})

describe('Sidebar overlay — Tab is trapped inside the drawer while open', () => {
  it('Tab on the last focusable control wraps to the first (Sign out → Search sessions)', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: false })
    })
    renderOpenOverlay()

    const firstButton = screen.getByRole('button', { name: 'Search sessions' })
    const lastButton = screen.getByRole('button', { name: 'Sign out' })

    act(() => {
      lastButton.focus()
    })
    expect(document.activeElement).toBe(lastButton)

    fireEvent.keyDown(lastButton, { key: 'Tab', bubbles: true })

    expect(document.activeElement).toBe(firstButton)
  })

  it('Shift+Tab on the first focusable control wraps to the last (Search sessions → Sign out)', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: false })
    })
    renderOpenOverlay()

    const firstButton = screen.getByRole('button', { name: 'Search sessions' })
    const lastButton = screen.getByRole('button', { name: 'Sign out' })

    // Opening already focused firstButton; re-assert to be explicit about intent.
    act(() => {
      firstButton.focus()
    })
    expect(document.activeElement).toBe(firstButton)

    fireEvent.keyDown(firstButton, { key: 'Tab', shiftKey: true, bubbles: true })

    expect(document.activeElement).toBe(lastButton)
  })

  it('Tab from a MIDDLE control (not first/last) is left alone — not intercepted', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: false })
    })
    renderOpenOverlay()

    // The pin toggle is neither the first nor last focusable control.
    const pinButton = screen.getByRole('button', { name: 'Pin sidebar' })
    act(() => {
      pinButton.focus()
    })

    fireEvent.keyDown(pinButton, { key: 'Tab', bubbles: true })

    // jsdom doesn't implement native Tab traversal, so the meaningful check
    // is that our trap handler did NOT force focus away from pinButton (it
    // only reroutes at the first/last edges) — a bug here would show up as
    // focus jumping to the first or last control instead of staying put.
    expect(document.activeElement).toBe(pinButton)
  })
})

describe('Sidebar overlay — Escape closes and restores focus to the hamburger', () => {
  it('closes the drawer and returns focus to #sidebar-hamburger', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: false })
    })
    renderOpenOverlay()

    // Confirm the trap moved focus inside first.
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Search sessions' }))

    act(() => {
      fireEvent.keyDown(window, { key: 'Escape' })
    })

    // The overlay dialog aside is gone.
    expect(screen.queryByRole('dialog')).toBeNull()
    // Focus returned to the hamburger.
    const hamburger = document.getElementById('sidebar-hamburger')
    expect(document.activeElement).toBe(hamburger)
  })
})
