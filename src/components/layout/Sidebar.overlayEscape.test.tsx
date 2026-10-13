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
import { useChatStore } from '@/store/chat'
import { fetchAgents, fetchAppState, fetchGodMode, fetchSessionPage, fetchSessions, fetchWorkspaces, logout } from '@/lib/api'
import type { Session, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { useCancelState } from '@/hooks/useCancelState'

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

// The OTHER Escape owner (combined-review R3 N6): useCancelState registers a
// DOCUMENT-level bubble keydown that cancels a streaming turn. The probe mounts
// it for real alongside the Sidebar so the two listeners compete for the same
// native keydown exactly as they do in the app — dispatching on a target inside
// the document bubbles document → window, so both handlers run in DOM order.
let cancelProbe: ReturnType<typeof useCancelState> | null = null
const cancelStreamSpy = vi.fn(() => true)
function CancelProbe({ isStreaming }: { isStreaming: boolean }) {
  cancelProbe = useCancelState(isStreaming, cancelStreamSpy)
  return null
}

/** Renders the Sidebar overlay AND the cancel-state probe with a streaming
 * turn (both the hook prop and the live chat-store read the handler consults).
 * Same store setup as renderOpenOverlay, plus the streaming turn. */
function renderOpenOverlayWhileStreaming() {
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: false })
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
    useChatStore.setState({ isStreaming: true })
  })
  render(
    <>
      <Sidebar />
      <CancelProbe isStreaming />
    </>,
    { wrapper: makeWrapper() },
  )
  expect(document.getElementById('sidebar-overlay-panel')).not.toBeNull()
}

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

// Combined-review R3 N6 (the regression the nav-ci3 fix 18f04543e introduced):
// useCancelState's global handler is a DOCUMENT-level bubble listener that
// cancelS a streaming turn and then preventDefaults the key — so the Sidebar's
// WINDOW-level listener saw the event arrive already defaultPrevented and
// stood down. The overlay stayed open while the turn died, and repeated
// Escapes escalated to a Stop-all confirmation. The rule this pins (team-lead
// ruling): Escape acts on the TOPMOST layer only — a Radix layer above the
// sidebar wins; else the overlay sidebar wins (and the turn is NOT touched);
// only with nothing else open does Escape cancel the turn.
describe('Sidebar overlay — Escape precedence over the turn-cancel handler (R3 N6)', () => {
  beforeEach(() => {
    cancelStreamSpy.mockClear()
    cancelProbe = null
  })

  afterEach(() => {
    act(() => {
      useChatStore.setState({ isStreaming: false })
    })
  })

  it('STREAMING + overlay open: Escape closes ONLY the sidebar — the turn keeps streaming and no Stop window is armed', async () => {
    renderOpenOverlayWhileStreaming()

    // The keydown must originate INSIDE the document (a real keypress's
    // target) so it bubbles document → window through BOTH handlers; a
    // dispatch straight on window would skip the document listener entirely.
    fireEvent.keyDown(overlayPanel()!, { key: 'Escape' })

    // The sidebar closed…
    expect(useSidebarStore.getState().isOpen).toBe(false)
    await waitFor(() => expect(overlayPanel()).toBeNull())
    // …and the turn was NOT cancelled: no cancel frame, no armed Stop-all
    // window. (The pre-fix bug died here twice over: the turn was cancelled
    // AND the overlay stayed open.)
    expect(cancelStreamSpy).not.toHaveBeenCalled()
    expect(cancelProbe?.stopAllArmed).toBe(false)

    // A SECOND Escape, with the overlay gone, is a first (session-scoped)
    // cancel — proof the first Escape did not silently arm the window whose
    // confirmation would have escalated to Stop all.
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(cancelStreamSpy).toHaveBeenCalledTimes(1)
    expect(cancelStreamSpy).toHaveBeenCalledWith()
    expect(cancelProbe?.stopAllArmed).toBe(true)
  })

  it('STREAMING + row menu open: Escape closes ONLY the menu — sidebar stays open, the turn is not cancelled', async () => {
    renderOpenOverlayWhileStreaming()
    const menu = await openMiaRowMenu()

    fireEvent.keyDown(menu, { key: 'Escape' })

    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    expect(useSidebarStore.getState().isOpen).toBe(true)
    expect(overlayPanel()).not.toBeNull()
    // The Radix layer ranks above BOTH the sidebar and the turn-cancel: the
    // menu's dismissal preventDefaults the key before the document-level
    // cancel handler ever sees it.
    expect(cancelStreamSpy).not.toHaveBeenCalled()
    expect(cancelProbe?.stopAllArmed).toBe(false)
  })

  it('nothing else open + STREAMING: Escape cancels the turn as today (unchanged behaviour)', () => {
    // Sidebar closed — the cancel handler owns Escape alone.
    render(
      <CancelProbe isStreaming />,
      { wrapper: makeWrapper() },
    )
    expect(overlayPanel()).toBeNull()

    fireEvent.keyDown(document.body, { key: 'Escape' })

    expect(cancelStreamSpy).toHaveBeenCalledTimes(1)
    expect(cancelStreamSpy).toHaveBeenCalledWith()
    expect(cancelProbe?.stopAllArmed).toBe(true)
  })

  it('an armed Stop-all window is NOT confirmed by an Escape that only closes the overlay', () => {
    renderOpenOverlayWhileStreaming()

    // First activation (the Stop button's twin): session cancel + the 3 s
    // confirmation window, with the overlay still open.
    act(() => cancelProbe?.cancelUnconditional())
    expect(cancelStreamSpy).toHaveBeenCalledTimes(1)
    expect(cancelProbe?.stopAllArmed).toBe(true)

    // Escape now belongs to the TOPMOST layer — the overlay. It closes the
    // sidebar and must NOT be the confirming second activation.
    fireEvent.keyDown(overlayPanel()!, { key: 'Escape' })
    expect(useSidebarStore.getState().isOpen).toBe(false)
    expect(cancelStreamSpy).toHaveBeenCalledTimes(1)
    expect(cancelProbe?.stopAllArmed).toBe(true)
  })
})

// Combined-review R4 (M1 = R4-QA-01 / R4-S5-PTA-02, PTA-06): the two gaps the
// round-4 CHECK left. (1) The predicate's DOCKED branch — a saved pin on a
// viewport ≥ the 1024px breakpoint — was never exercised: every earlier case
// here runs unpinned on a narrow stub, so a mutant flipping the wide-viewport
// term survived all five suites while Escape stopped cancelling a streaming
// turn for the default desktop setup. (2) The profile menu in the sidebar
// footer is a Radix layer exactly like the row menu, and now has the same
// pin. Mutation receipts: receipts/nav-r4-mut-*.log.
describe('Escape precedence — docked sidebar and the profile menu (R4 M1 / PTA-06)', () => {
  /** A MediaQueryList-shaped answer for the file's matchMedia stub. */
  function mediaList(matches: boolean) {
    return (query: string) => ({
      matches,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })
  }

  /** The file's stub answers narrow for every query; these cases re-point it
   * and always restore the narrow default for the suites above. */
  function mockViewport(matches: boolean) {
    ;(window.matchMedia as unknown as { mockImplementation: (i: (q: string) => unknown) => void })
      .mockImplementation(mediaList(matches))
  }

  beforeEach(() => {
    cancelStreamSpy.mockClear()
    cancelProbe = null
  })

  afterEach(() => {
    mockViewport(false)
    act(() => {
      useChatStore.setState({ isStreaming: false })
    })
  })

  it('DOCKED sidebar (pinned + wide viewport) + STREAMING: Escape cancels the turn — the dock is untouched', () => {
    // Wide viewport: the saved pin IS honoured, so the sidebar docks (no
    // overlay panel exists) — the normal desktop layout this file never ran.
    mockViewport(true)
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: true })
      useWorkspacesStore.setState({ activeWorkspaceId: WS })
      useChatStore.setState({ isStreaming: true })
    })
    render(
      <>
        <Sidebar />
        <CancelProbe isStreaming />
      </>,
      { wrapper: makeWrapper() },
    )
    // Positive controls: DOCKED, not overlay — the overlay panel is absent
    // and the docked sidebar body is on screen.
    expect(overlayPanel()).toBeNull()
    expect(screen.getByTestId('sidebar-library-button')).toBeTruthy()

    // A real keypress's target inside the document bubbles document → window
    // through BOTH Escape owners.
    fireEvent.keyDown(document.body, { key: 'Escape' })

    // The dock is NOT a surface Escape closes: the turn-cancel handler owns
    // the key (this is the mutant test — flipping the wide-viewport term to
    // "overlay" stands the cancel handler down and leaves the turn running).
    expect(cancelStreamSpy).toHaveBeenCalledTimes(1)
    expect(cancelStreamSpy).toHaveBeenCalledWith()
    expect(cancelProbe?.stopAllArmed).toBe(true)
    // …and the docked sidebar stays exactly as it was.
    expect(useSidebarStore.getState().isOpen).toBe(true)
    expect(useSidebarStore.getState().isPinned).toBe(true)
    expect(overlayPanel()).toBeNull()
  })

  it('STREAMING + the profile menu (sidebar footer) open: Escape closes ONLY the menu — sidebar stays open, the turn is not cancelled', async () => {
    renderOpenOverlayWhileStreaming()

    // Open the profile menu the way a pointer does (Radix opens on pointer
    // down) — the sidebar-footer DropdownMenu, not the row ⋯ menu.
    const trigger = screen.getByTestId('sidebar-profile-trigger')
    await act(async () => {
      fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
      fireEvent.pointerUp(trigger, { button: 0, pointerType: 'mouse' })
    })
    const menu = screen.getByRole('menu')

    fireEvent.keyDown(menu, { key: 'Escape' })

    // The Radix layer consumed the Escape: the menu is gone…
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    // …the overlay sidebar stays open…
    expect(useSidebarStore.getState().isOpen).toBe(true)
    expect(overlayPanel()).not.toBeNull()
    // …and the turn was never touched — the menu's dismissal preventDefaults
    // the key before the document-level cancel handler sees it.
    expect(cancelStreamSpy).not.toHaveBeenCalled()
    expect(cancelProbe?.stopAllArmed).toBe(false)
  })
})
