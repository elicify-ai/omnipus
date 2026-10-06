// ADR-20260928 MAJ-009 + T26 — Sidebar session rows show the exact
// lifecycle_state (RED pack).
//
// The sidebar's session rows (Sidebar.tsx::SidebarSessionRow) must render the
// session's `lifecycle_state` as one of exactly six labels — working,
// waiting for answer, done, failed, stopped, interrupted (F0929-2 vocabulary
// plus interrupted, founder 2026-10-06; wire enum
// `working|waiting_for_answer|done|failed|stopped|interrupted`, generated Session schema)
// — and a stopped row must additionally show its `stop_note.cause` (the
// lasting who/when/why, MAJ-009). `Session.status` (active/archived/failed)
// is COARSE transcript metadata and must never drive the label: the ADR's own
// example is a stopped helper with `status: active`, `lifecycle_state:
// stopped`. A session with NO lifecycle_state (no lifecycle record) renders
// no label at all — and must not crash the row.
//
// RED status (2026-10-02): SidebarSessionRow renders only the title, the HB
// badge and the child count today — no lifecycle label exists anywhere in
// the row, so every label test fails on the absent text. The absent-field
// pin passes trivially today and becomes the GREEN guard (it fails the
// moment a label is rendered for sessions that have no lifecycle_state).
// Fixture note: the SPA Session type (src/lib/api/sessions.ts) does not
// carry lifecycle_state/stop_note yet — passing those wire fields through
// rawToSession() is part of GREEN; the mock here feeds the row component
// directly, exactly what the wire will provide.
//
// Oracle: ADR-20260928 F0929-2/D2 vocabulary table, MAJ-009 Session row,
// T26; label WORDS pinned case-insensitively (the ADR's prose is
// sentence-case; exact capitalisation is presentation, not contract).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSidebarStore } from '@/store/sidebar'
import { fetchWorkspaces, fetchSessions, type Session } from '@/lib/api'
import type { Session as WireSession } from '@/lib/api/generated/openapi-types'

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

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to, search, onClick, className, ...rest }: {
    children: React.ReactNode
    to: string
    search?: Record<string, string>
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => (
    <a
      href={search ? `${to}?${new URLSearchParams(search).toString()}` : to}
      onClick={onClick}
      className={className}
      {...rest}
    >
      {children}
    </a>
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
    } as never),
    fetchGodMode: vi.fn().mockResolvedValue({
      enabled: false,
      available: true,
      supported: true,
      persisted: false,
    }),
    workspacesQueryKeys: {
      list: (params?: unknown) => ['workspaces', params],
    },
  }
})

const { mockSetActiveWorkspaceId, mockStartNewSession, mockSelectSession } = vi.hoisted(() => ({
  mockSetActiveWorkspaceId: vi.fn(),
  mockStartNewSession: vi.fn(),
  mockSelectSession: vi.fn(),
}))

vi.mock('@/store/workspacesStore', () => {
  const state = { activeWorkspaceId: null as string | null, setActiveWorkspaceId: mockSetActiveWorkspaceId }
  return {
    useWorkspacesStore: (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state),
  }
})

vi.mock('@/store/session', () => {
  const state = { activeSessionId: null as string | null, startNewSession: mockStartNewSession }
  const useSessionStore = (selector?: (s: typeof state) => unknown) => (selector ? selector(state) : state)
  useSessionStore.getState = () => state
  return { useSessionStore }
})
vi.mock('@/components/chat/useSelectSession', () => ({
  useSelectSession: () => mockSelectSession,
}))

vi.mock('@/store/auth', () => {
  const mockState = { clearAuth: vi.fn(), username: 'testuser', token: null, role: null }
  const useAuthStore = (selector?: (s: typeof mockState) => unknown) =>
    selector ? selector(mockState) : mockState
  useAuthStore.getState = () => mockState
  return { useAuthStore }
})

const { mockOpenPanel, mockOpenSearchModal } = vi.hoisted(() => ({
  mockOpenPanel: vi.fn(),
  mockOpenSearchModal: vi.fn(),
}))
vi.mock('@/store/ui', () => {
  const state = {
    toggleNotificationPanel: vi.fn(),
    openPanel: mockOpenPanel,
    openSearchModal: mockOpenSearchModal,
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

// ── Fixtures ────────────────────────────────────────────────────────────────
// Titles deliberately avoid all five label words so a label assertion can
// never match the title itself.

const lcWorkspace = {
  id: 'ws-lc-1',
  name: 'Lifecycle Workspace',
  is_default: false,
  status: 'active' as const,
  pinned: false,
  pin_order: 0,
  task_count: 0,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
}

let lcRowCounter = 0

// A session row as the sidebar consumes it once GREEN lands: the SPA Session
// (what fetchSessions resolves today) plus the lifecycle_state/stop_note wire
// fields, whose TYPES are lifted from the generated wire schema
// (src/lib/api/generated/openapi-types.ts::Session) — nothing hand-written.
// The SPA Session gains those fields as part of GREEN (rawToSession
// pass-through of the generated schema); the mock feeds the row directly.
type LifecycleSessionRow = Session & Pick<WireSession, 'lifecycle_state' | 'stop_note'>

// One session fixture per test; overrides carry the wire fields under test.
function lcSession(overrides: Partial<LifecycleSessionRow>): LifecycleSessionRow {
  lcRowCounter += 1
  return {
    id: `sess-lc-${lcRowCounter}`,
    agent_id: 'agent-1',
    active_agent_id: 'agent-1',
    title: `Session Row ${lcRowCounter}`,
    type: 'chat',
    workspace_id: lcWorkspace.id,
    channel: 'webchat',
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T02:00:00Z',
    message_count: 1,
    ...overrides,
  }
}

// Renders the sidebar with one workspace expanded and `session` as its only
// session row; resolves once the row's title is visible.
async function renderRowFor(session: LifecycleSessionRow) {
  vi.mocked(fetchWorkspaces).mockResolvedValue([lcWorkspace] as never)
  vi.mocked(fetchSessions).mockResolvedValue([session])
  act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
  render(<Sidebar />, { wrapper: makeWrapper() })

  const expandButton = await screen.findByLabelText('Expand Lifecycle Workspace sessions')
  act(() => { fireEvent.click(expandButton) })

  return await screen.findByText(session.title)
}

// The five label words, pinned case-insensitively (vocabulary is the
// contract; capitalisation is presentation).
const FIVE_LABEL_PATTERNS = [
  /working/i,
  /waiting for answer/i,
  /\bdone\b/i,
  /\bfailed\b/i,
  /\bstopped\b/i,
]

describe('Sidebar session rows — lifecycle_state labels (ADR-20260928 MAJ-009, T26)', () => {
  beforeEach(() => {
    act(() => { useSidebarStore.setState({ isOpen: false, isPinned: false }) })
    lcRowCounter = 0
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchSessions).mockReset().mockResolvedValue([])
  })

  it('RED row with lifecycle_state working shows the Working label', async () => {
    const row = await renderRowFor(lcSession({ lifecycle_state: 'working' }))
    const rowText = row.closest('button')?.textContent ?? ''
    expect(rowText).toMatch(/working/i)
  })

  it('RED row with lifecycle_state waiting_for_answer shows Waiting for answer', async () => {
    const row = await renderRowFor(lcSession({ lifecycle_state: 'waiting_for_answer' }))
    const rowText = row.closest('button')?.textContent ?? ''
    expect(rowText).toMatch(/waiting for answer/i)
  })

  it('RED row with lifecycle_state done shows Done', async () => {
    const row = await renderRowFor(lcSession({ lifecycle_state: 'done' }))
    const rowText = row.closest('button')?.textContent ?? ''
    expect(rowText).toMatch(/\bdone\b/i)
  })

  it('RED row with lifecycle_state failed shows Failed', async () => {
    const row = await renderRowFor(lcSession({ lifecycle_state: 'failed' }))
    const rowText = row.closest('button')?.textContent ?? ''
    expect(rowText).toMatch(/\bfailed\b/i)
  })

  it('RED row with lifecycle_state stopped shows Stopped', async () => {
    const row = await renderRowFor(lcSession({
      lifecycle_state: 'stopped',
      stop_note: { at: '2026-04-01T03:00:00Z', by: 'human:user-1', seq: 1, cause: 'stop' },
    }))
    const rowText = row.closest('button')?.textContent ?? ''
    expect(rowText).toMatch(/\bstopped\b/i)
  })

  it('RED stopped row shows its stop_note cause', async () => {
    const row = await renderRowFor(lcSession({
      lifecycle_state: 'stopped',
      stop_note: { at: '2026-04-01T03:00:00Z', by: 'human:user-1', seq: 1, cause: 'cascade' },
    }))
    const rowText = row.closest('button')?.textContent ?? ''
    // MAJ-009: the lasting reason is visible on the stopped row — the cause
    // value from stop_note (here "cascade", the Stop-all descendant cause).
    expect(rowText).toMatch(/cascade/i)
  })

  it('RED stopped label is driven by lifecycle_state, never by coarse Session.status', async () => {
    // MAJ-009's exact case: a stopped helper has status 'active' (coarse
    // metadata) and lifecycle_state 'stopped' (exact). The row must show
    // Stopped because of lifecycle_state — status never drives the label.
    const row = await renderRowFor(lcSession({
      status: 'active',
      lifecycle_state: 'stopped',
      stop_note: { at: '2026-04-01T03:00:00Z', by: 'human:user-1', seq: 1, cause: 'stop' },
    }))
    const rowText = row.closest('button')?.textContent ?? ''
    expect(rowText).toMatch(/\bstopped\b/i)
  })

  it('RED row with lifecycle_state interrupted shows Interrupted, not Failed, in the neutral muted colour', async () => {
    // Founder decision 2026-10-06: a session cut off by a server restart is
    // "interrupted" (sixth status) — it does not fail. Not the error red.
    const row = await renderRowFor(lcSession({ lifecycle_state: 'interrupted' }))
    const rowButton = row.closest('button')
    const rowText = rowButton?.textContent ?? ''
    expect(rowText).toMatch(/\bInterrupted\b/)
    expect(rowText).not.toMatch(/\bfailed\b/i)
    const label = Array.from(rowButton?.querySelectorAll('span') ?? []).find(
      (el) => el.textContent?.trim() === 'Interrupted',
    )
    expect(label).toBeTruthy()
    expect(label?.className).toContain('text-[var(--color-muted)]')
    expect(label?.className).not.toContain('text-[var(--color-error)]')
  })

  it('PIN session without lifecycle_state renders its row with no lifecycle label and no crash', async () => {
    // Absent lifecycle_state (no lifecycle record) → no five-state label.
    // Passes trivially today (no labels exist yet); it is the GREEN guard:
    // it fails the moment a label is rendered for sessions that have no
    // lifecycle_state.
    const row = await renderRowFor(lcSession({}))
    expect(row).toBeTruthy()
    const rowText = row.closest('button')?.textContent ?? ''
    for (const pattern of FIVE_LABEL_PATTERNS) {
      expect(rowText).not.toMatch(pattern)
    }
  })
})
