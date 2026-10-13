/**
 * Sidebar overlay × Escape — the topmost dismissable layer wins (CI job
 * 114998058497, tests/e2e/sidebar-agent-row-menu.spec.ts D8 "Remove disabled
 * WITH a reason").
 *
 * The bug this pins: Sidebar.tsx::handleKeydown is a WINDOW-level keydown that
 * closed the whole overlay on every Escape — including the Escape a user
 * pressed to dismiss a dismissable layer opened from inside the sidebar (the
 * agent row's ⋯ menu). Radix's DismissableLayer consumes that Escape first —
 * its document-level CAPTURE listener closes the layer and calls
 * event.preventDefault() — so the window-level bubble listener can (and must)
 * stand down when the event arrives already defaultPrevented. Expected:
 * Escape closes only the topmost layer; a SECOND Escape, with nothing else
 * open, closes the overlay as before.
 *
 * The dropdown here is the REAL catalogued Radix primitive (no stub): the
 * preventDefault-then-dismiss ordering under test lives in
 * @radix-ui/react-dismissable-layer, so a stubbed DropdownMenu could never
 * exercise it. Stores are the real zustand stores; the router, the network
 * seam and framer-motion are stubbed exactly as Sidebar.test.tsx stubs them
 * (motion is irrelevant here — every assertion reads the store and the
 * mounted overlay panel, not an animation).
 */
import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore } from '@/store/session'
import { fetchAgents, fetchAppState, fetchGodMode, fetchSessionPage, fetchSessions, fetchWorkspaces, logout } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'

const WS = 'ws-overlay-escape'
const REV = '0'.repeat(64)
const MIA_MAIN = 's-mia-main-overlay-escape'

const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })

// Mia is a member with a resolvable main (sessionCoreSeam reads
// member_configs.<id>.main_session_id and Session.type === 'main'), so her
// row renders as a normal member row with its ⋯ trigger.
const workspace: Workspace = {
  id: WS,
  name: 'Escape Cove',
  is_default: false,
  status: 'active',
  pinned: false,
  pin_order: 0,
  task_count: 0,
  revision: REV,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  core_team: ['mia'],
  member_configs: { mia: { main_session_id: MIA_MAIN } },
}

const miaMain: Session = {
  id: MIA_MAIN,
  type: 'main',
  agent_id: 'mia',
  title: 'Mia',
  workspace_id: WS,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  message_count: 0,
}

// Narrow viewport: below the 1024px pin breakpoint the sidebar is ALWAYS an
// overlay — the exact mode the failing e2e runs in.
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
})

// Radix DropdownMenu polyfills for jsdom (the row menu and the profile menu
// are real Radix DropdownMenus here — see the header comment).
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
  Link: ({ children, to, onClick, className, ...rest }: {
    children: ReactNode
    to: string
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => (
    <a href={to} onClick={onClick} className={className} {...rest}>{children}</a>
  ),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // Bare spies — the resolved values (the workspace/session/agent records
    // above) are pinned per-test in beforeEach, because this factory is
    // hoisted above the module-level constants and cannot reference them.
    fetchWorkspaces: vi.fn(),
    fetchSessions: vi.fn(),
    fetchAgents: vi.fn(),
    fetchSessionPage: vi.fn(),
    logout: vi.fn(),
    fetchAppState: vi.fn(),
    fetchGodMode: vi.fn(),
  }
})

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

// Mock framer-motion — AnimatePresence/motion render children without
// animation. Everything under test here is event wiring, not motion.
vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} {...rest}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div className={className} onClick={onClick} {...rest}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: ReactNode }) => <>{children}</>,
}))

function makeWrapper() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

/** Renders the Sidebar with the overlay open (isOpen, never pinned —
 * matchMedia answers false, so canPin=false makes the overlay unconditional). */
function renderOpenOverlay() {
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: false })
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
  })
  render(<Sidebar />, { wrapper: makeWrapper() })
  // The overlay panel is on screen before anything else is asked of it.
  expect(document.getElementById('sidebar-overlay-panel')).not.toBeNull()
}

/** Opens Mia's row ⋯ menu the way a pointer does (Radix opens on pointer
 * down). Resolves the row and trigger BEFORE opening: an open modal menu
 * marks content outside its portal aria-hidden, so the row leaves the
 * accessibility tree while the menu is up. */
async function openMiaRowMenu() {
  // findBy: the sidebar's workspaces/agents queries resolve asynchronously —
  // the row exists only after they land.
  const row = await screen.findByRole('group', { name: 'Mia' })
  const trigger = within(row).getByRole('button', { name: 'More actions for Mia' })
  await act(async () => {
    fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
    fireEvent.pointerUp(trigger, { button: 0, pointerType: 'mouse' })
  })
  return screen.getByRole('menu')
}

function overlayPanel(): HTMLElement | null {
  return document.getElementById('sidebar-overlay-panel')
}

import { Sidebar } from './Sidebar'

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchWorkspaces).mockResolvedValue([workspace])
  vi.mocked(fetchSessions).mockResolvedValue([miaMain])
  vi.mocked(fetchAgents).mockResolvedValue([mia])
  vi.mocked(fetchSessionPage).mockResolvedValue({ sessions: [] })
  vi.mocked(logout).mockResolvedValue(undefined)
  vi.mocked(fetchAppState).mockResolvedValue({
    onboarding_complete: true,
    dev_mode_bypass: false,
    identity: { mode: 'platform', edition: 'hosted', signed_in: true },
  } as never)
  vi.mocked(fetchGodMode).mockResolvedValue({
    enabled: false,
    available: true,
    supported: true,
    persisted: false,
  })
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
    useSessionStore.setState(useSessionStore.getInitialState(), true)
  })
})

afterEach(() => {
  cleanup()
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
})

describe('Sidebar overlay — Escape dismisses only the topmost layer', () => {
  it('Escape with Mia’s ⋯ menu open closes ONLY the menu — the overlay sidebar stays open; a second Escape then closes it', async () => {
    renderOpenOverlay()
    const menu = await openMiaRowMenu()

    // One Escape: the row menu's own dismissal.
    fireEvent.keyDown(menu, { key: 'Escape' })

    // The menu is gone…
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    // …and the overlay sidebar is STILL open — the bug closed it here, with
    // the exit animation unmounting the panel under the user's next click.
    expect(useSidebarStore.getState().isOpen).toBe(true)
    expect(overlayPanel()).not.toBeNull()

    // A second Escape, with nothing else open, closes the overlay as today.
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(useSidebarStore.getState().isOpen).toBe(false)
    await waitFor(() => expect(overlayPanel()).toBeNull())
  })

  it('Escape with no inner layer open closes the overlay (unchanged behaviour)', () => {
    renderOpenOverlay()

    fireEvent.keyDown(window, { key: 'Escape' })

    expect(useSidebarStore.getState().isOpen).toBe(false)
    expect(overlayPanel()).toBeNull()
  })

  it('Escape while the inline new-workspace field is open cancels only the field — the overlay sidebar stays open', () => {
    renderOpenOverlay()

    // Open the inline creation field from the Workspaces section header.
    fireEvent.click(screen.getByRole('button', { name: 'New workspace' }))
    const input = screen.getByPlaceholderText('Workspace name…')

    // Escape cancels the field; the sidebar must survive it.
    fireEvent.keyDown(input, { key: 'Escape' })

    expect(screen.queryByPlaceholderText('Workspace name…')).toBeNull()
    expect(useSidebarStore.getState().isOpen).toBe(true)
    expect(overlayPanel()).not.toBeNull()
  })
})
