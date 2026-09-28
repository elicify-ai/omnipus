// Sidebar.panelGuard.test.tsx — side-panel-shell-spec.md §12 #8, the sidebar
// entry point of FR-013. ChatControls and the header Close are covered in
// their own files. This one pins the sidebar LIBRARY button.
//
// Written against the §8.1 single-slice store (batch-2 squad-lead ruling):
// `activePanel: { id, context } | null` with `openPanel(id, context)` /
// `closePanel()` — "single slice replacing browserPanel/libraryPanel"
// (side-panel-shell-spec.md §8.1/SP-7). The pack previously seeded the
// RETIRED `libraryPanel` slice; that slice no longer exists on GREEN and the
// sidebar entry point rides the single slice.
//
// RED on this pre-GREEN tree: the §8.1 API does not exist yet, so the
// beforeEach gate fails every test with an explicit BLOCKED naming it (never
// an unexplained TypeError). On GREEN the behavioural RED is: Sidebar.tsx's
// library button replaces a dirty Library without consulting the
// beforeLeave/confirmDiscardLibraryEdits guard (FR-013).
//
// Oracle: FR-013 — every entry point, sidebar included, runs the outgoing
// panel's leave guard; cancel leaves the panel and the edit untouched.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import {
  setLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: () => ({
    matches: true,
    media: '',
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
})

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, onClick, className, ...rest }: {
    children: React.ReactNode
    to: string
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => (
    <a href={to} onClick={onClick} {...rest} data-fixture-class={className ? 'present' : undefined}>{children}</a>
  ),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    logout: vi.fn().mockResolvedValue(undefined),
    fetchSessions: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionPage: vi.fn().mockResolvedValue({ sessions: [] }),
    fetchAppState: vi.fn().mockResolvedValue({
      onboarding_complete: true,
      dev_mode_bypass: false,
      identity: { mode: 'platform', edition: 'hosted', signed_in: true },
    }),
    fetchGodMode: vi.fn().mockResolvedValue({
      enabled: false,
      available: true,
      supported: true,
      persisted: false,
    }),
    workspacesQueryKeys: { list: (params?: unknown) => ['workspaces', params] },
  }
})

vi.mock('@/store/workspacesStore', () => {
  const state = { activeWorkspaceId: 'ws-1' as string | null, setActiveWorkspaceId: vi.fn() }
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

vi.mock('@/components/chat/useSelectSession', () => ({ useSelectSession: () => vi.fn() }))

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'dana', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

vi.mock('@/store/notifications', () => ({
  useNotificationsStore: (selector?: (s: { unreadCount: number }) => unknown) => {
    const state = { unreadCount: 0 }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/components/ui/dropdown-menu', async () => {
  const { Button } = await vi.importActual<typeof import('@/components/ui/button')>('@/components/ui/button')
  return {
    DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
    DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuItem: ({ children, onSelect }: { children: React.ReactNode; onSelect?: () => void }) => (
      <Button variant="ghost" onClick={onSelect}>{children}</Button>
    ),
    DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
    DropdownMenuSeparator: () => <hr />,
  }
})

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({ NewWorkspaceSlideOver: () => null }))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside {...rest} data-fixture-class={className ? 'present' : undefined}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div onClick={onClick} {...rest} data-fixture-class={className ? 'present' : undefined}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { Sidebar } from './Sidebar'

function renderSidebar() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Sidebar />
    </QueryClientProvider>,
  )
}

/** §8.1 single-slice view of the ui store (defensive: the slice does not
 * exist on this pre-GREEN tree — see the BLOCKED gate below). */
type Section81Store = {
  activePanel: { id: string; context?: Record<string, unknown> } | null
  openPanel: (id: string, context?: Record<string, unknown>) => void
  closePanel: () => void
}
const s81 = () => useUiStore.getState() as unknown as Section81Store

/** Fails every test with a STATED reason while the §8.1 slice is missing. */
function requireSection81Api() {
  const s = useUiStore.getState() as unknown as Record<string, unknown>
  if (typeof s.openPanel !== 'function' || typeof s.closePanel !== 'function' || !('activePanel' in s)) {
    throw new Error(
      'BLOCKED: ui store has no §8.1 single-slice API (activePanel / openPanel(id, context) / closePanel()) — ' +
        'required by side-panel-shell-spec.md §8.1/SP-7 (ONE slice replacing the retired libraryPanel/browserPanel)',
    )
  }
}

beforeEach(() => {
  requireSection81Api()
  act(() => {
    useSidebarStore.setState({ isOpen: true, isPinned: true })
    s81().openPanel('library', { workspaceId: 'ws-1' })
  })
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

afterEach(() => {
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

describe('Sidebar LIBRARY button — leave guard (FR-013, §12 #8)', () => {
  it('RED — a dirty Library opens the discard prompt instead of being replaced', async () => {
    setLibraryEditorDirty(true)
    renderSidebar()
    fireEvent.click(screen.getByTestId('sidebar-library-button'))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    expect(s81().activePanel).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
  })

  it('RED — cancelling the prompt leaves the Library panel and the edit in place', async () => {
    setLibraryEditorDirty(true)
    renderSidebar()
    fireEvent.click(screen.getByTestId('sidebar-library-button'))
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))
    act(() => {
      resolveDiscardConfirmDialog(false)
    })
    expect(s81().activePanel).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })
})
