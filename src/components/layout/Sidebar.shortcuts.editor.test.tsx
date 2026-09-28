/**
 * RED contract — global keyboard shortcuts (Cmd+B / Ctrl+B toggles the
 * sidebar) MUST NOT fire when the keystroke originates from inside an
 * editor / contenteditable target. Today the Sidebar's window-level
 * keydown listener in src/components/layout/Sidebar.tsx ignores the
 * event target entirely, so any Mod+b pressed inside the Mail draft
 * editor's Tiptap surface leaks to the sidebar toggle instead of
 * toggling bold (the editor loses the keystroke to a global consumer).
 *
 * This test exercises the exact window-level keydown path that the
 * Sidebar attaches, with a real contenteditable on the page (a portal-
 * mounted element, like Tiptap's EditorContent) — the failing case is
 * the unmitigated code; after the fix the listener short-circuits on
 * `target.closest('[contenteditable="true"], [contenteditable=""]')`
 * (and an editor-data attribute tag for surfaces that proxy their own
 * contenteditable like the chat composer) and the sidebar stays put.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, act, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'

// JSDOM matchMedia polyfill — the Sidebar uses it for pin breakpoint detection.
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

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to }: { children: React.ReactNode; to: string }) => (
    <a href={to}>{children}</a>
  ),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

vi.mock('@/lib/api', () => ({
  fetchWorkspaces: vi.fn().mockResolvedValue([]),
  logout: vi.fn().mockResolvedValue(undefined),
  fetchSessions: vi.fn().mockResolvedValue([]),
  fetchAgents: vi.fn().mockResolvedValue([]),
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
}))

vi.mock('@/store/workspacesStore', () => ({
  useWorkspacesStore: (selector?: (s: { activeWorkspaceId: string | null; setActiveWorkspaceId: () => void }) => unknown) => {
    const state = { activeWorkspaceId: null, setActiveWorkspaceId: vi.fn() }
    return selector ? selector(state) : state
  },
}))

vi.mock('@/store/session', () => {
  const state = { activeSessionId: null as string | null, startNewSession: vi.fn() }
  const useSessionStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useSessionStore.getState = () => state
  return { useSessionStore }
})
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => vi.fn(),
}))
vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})
vi.mock('@/store/ui', () => {
  const state = {
    toggleNotificationPanel: vi.fn(),
    openPanel: vi.fn(),
    openSearchModal: vi.fn(),
  }
  const useUiStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useUiStore.getState = () => state
  return { useUiStore }
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
  DropdownMenuItem: ({ children, onSelect, 'aria-label': ariaLabel }: {
    children: React.ReactNode
    onSelect?: () => void
    'aria-label'?: string
  }) => (
    <button onClick={onSelect} aria-label={ariaLabel}>{children}</button>
  ),
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuSeparator: () => <hr />,
}))

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({
  NewWorkspaceSlideOver: () => null,
}))

vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className }: { children: React.ReactNode; className?: string }) => (
      <aside className={className}>{children}</aside>
    ),
    div: ({ children, className, onClick }: { children: React.ReactNode; className?: string; onClick?: () => void }) => (
      <div className={className} onClick={onClick}>{children}</div>
    ),
  },
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { Sidebar } from './Sidebar'

beforeEach(() => {
  cleanup()
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
})

function mount() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <Sidebar />
    </QueryClientProvider>,
  )
}

/** A helper that mounts a real contenteditable outside the Sidebar tree —
 * mirrors how Tiptap's EditorContent portals its surface into the document. */
function mountWithContentEditable(): HTMLElement {
  mount()
  const editable = document.createElement('div')
  editable.setAttribute('contenteditable', 'true')
  editable.setAttribute('data-testid', 'tiptap-surface')
  document.body.appendChild(editable)
  return editable
}

describe('Sidebar — global Cmd+B shortcut ignores editor / contenteditable targets', () => {
  it('does NOT toggle the sidebar when Cmd+B fires on a contenteditable (Tiptap/Mail draft editor)', () => {
    const editable = mountWithContentEditable()
    const before = useSidebarStore.getState().isOpen

    fireEvent.keyDown(editable, { key: 'b', metaKey: true })

    // The shortcut must not leak to the sidebar — bold stays owned by
    // Tiptap, the sidebar stays put.
    expect(useSidebarStore.getState().isOpen).toBe(before)
  })

  it('does NOT toggle the sidebar when Ctrl+B fires on a contenteditable (non-mac hosts)', () => {
    const editable = mountWithContentEditable()
    const before = useSidebarStore.getState().isOpen

    fireEvent.keyDown(editable, { key: 'b', ctrlKey: true })

    expect(useSidebarStore.getState().isOpen).toBe(before)
  })

  it('STILL toggles the sidebar when Cmd+B fires on a plain page element (regression guard)', () => {
    mount()
    const plain = document.createElement('div')
    plain.setAttribute('data-testid', 'plain-surface')
    document.body.appendChild(plain)

    const before = useSidebarStore.getState().isOpen
    fireEvent.keyDown(plain, { key: 'b', metaKey: true })
    expect(useSidebarStore.getState().isOpen).toBe(!before)
  })
})
