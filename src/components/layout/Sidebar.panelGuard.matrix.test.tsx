// Sidebar.panelGuard.matrix.test.tsx — gap pack from pr-test-analyzer F4:
// completes the Sidebar guard matrix that Sidebar.panelGuard.test.tsx opened
// (which this file deliberately does not edit). The existing pack pins
// prompt-on-dirty and cancel-keeps; the two missing cells are:
//
//   confirm-proceeds  — answering DISCARD must run the deferred open (the
//                       sidebar's virtual-root Library, D-3): the ws-1-scoped
//                       panel is replaced by { library, context: {} } and the
//                       edit is gone (US-4 AS-4's discarded arm + AS-5's
//                       immediate arm; §7: no path silently discards, a
//                       CONFIRMED discard is not silent).
//   clean-immediate   — a clean Library is replaced immediately with no
//                       prompt (US-4 AS-5: "the replacement happens
//                       immediately with no prompt"; §11 clean-replace BDD
//                       row).
//
// Oracle: side-panel-shell-spec.md US-4 AS-4/AS-5, FR-013, §12 #8; the
// virtual-root scope of the sidebar entry point from §2.1 ("Sidebar
// 'Library' (virtual root)") and D-3.
//
// Characterisation pack: GREEN implements this. Unit boundary: REAL Sidebar →
// REAL leaveGate → REAL unsavedGuard; dialog answered via the guard module's
// public API. Same mock scaffold as Sidebar.panelGuard.test.tsx (router,
// api, sibling stores, framer-motion) — edges only, the unit stays real.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import {
  setLibraryEditorDirty,
  isLibraryEditorDirty,
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
    <a href={to} onClick={onClick} className={className} {...rest}>{children}</a>
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

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onSelect }: { children: React.ReactNode; onSelect?: () => void }) => (
    <button onClick={onSelect}>{children}</button>
  ),
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}))

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({ NewWorkspaceSlideOver: () => null }))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => (
      <aside className={className} {...rest}>{children}</aside>
    ),
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => (
      <div className={className} onClick={onClick} {...rest}>{children}</div>
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

type Section81Store = {
  activePanel: { id: string; context?: Record<string, unknown> } | null
  openPanel: (id: string, context?: Record<string, unknown>) => void
  closePanel: () => void
}
const s81 = () => useUiStore.getState() as unknown as Section81Store

beforeEach(() => {
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

describe('Sidebar LIBRARY button — guard matrix completion (US-4 AS-4/AS-5)', () => {
  it('confirm-proceeds: DISCARD runs the deferred virtual-root open and clears the edit', async () => {
    setLibraryEditorDirty(true)
    renderSidebar()
    fireEvent.click(screen.getByTestId('sidebar-library-button'))

    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true))

    act(() => {
      resolveDiscardConfirmDialog(true)
    })

    // go() ran: the ws-1-scoped panel was replaced by the sidebar's
    // virtual-root Library (context {}), not left as it was.
    await waitFor(() => expect(s81().activePanel).toEqual({ id: 'library', context: {} }))
    // A confirmed discard consumes the edit (the operator chose to lose it).
    expect(isLibraryEditorDirty()).toBe(false)
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })

  it('clean-immediate: a CLEAN Library is replaced by the virtual-root open with no prompt', () => {
    expect(isLibraryEditorDirty()).toBe(false)
    renderSidebar()

    fireEvent.click(screen.getByTestId('sidebar-library-button'))

    // Synchronous — no dialog round-trip, no waitFor.
    expect(s81().activePanel).toEqual({ id: 'library', context: {} })
    expect(getDiscardConfirmDialogOpen()).toBe(false)
  })
})
