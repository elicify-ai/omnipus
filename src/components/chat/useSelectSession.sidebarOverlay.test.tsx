// Founder Q81 (#1266 UAT lane V, candidate 57d1352e0): after "Open session"
// from the Sessions dialog (or an agent row's History, which opens the same
// SearchModal) with the sidebar UNPINNED, the sidebar overlay stayed open and
// its full-viewport click-outside layer (Sidebar.tsx, div.absolute.inset-0.z-30,
// aria-hidden) covered the chat and swallowed the next click.
//
// Real Sidebar, real SearchModal, real useSelectSession and real stores. Only
// the network (@/lib/api reads), the router and framer-motion's animation
// wrappers are replaced. The session lives in the ALREADY-ACTIVE workspace, so
// the route path does not change: a route-change listener could not catch it.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

// Wide window (1440px in the UAT capture): the sidebar can dock, so an
// unpinned sidebar is the overlay and a pinned one is docked.
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: true, media: query, onchange: null,
    addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
  }),
})

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {}
  HTMLElement.prototype.hasPointerCapture ??= () => false
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} }
})

const { navigate } = vi.hoisted(() => ({ navigate: vi.fn() }))
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useLocation: () => ({ pathname: '/workspaces/ws-1/chat' }),
    useNavigate: () => navigate,
    // Navigation targets are not under test here; a plain link keeps the
    // sidebar's rows rendering without a router context.
    Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

// Animation wrappers render their element directly, so presence equals state.
vi.mock('framer-motion', () => ({
  motion: {
    aside: ({ children, className, ...rest }: React.HTMLAttributes<HTMLElement>) => <aside className={className} {...rest}>{children}</aside>,
    div: ({ children, className, onClick, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div className={className} onClick={onClick} {...rest}>{children}</div>,
  },
  AnimatePresence: ({ children }: { children: ReactNode }) => <>{children}</>,
}))

vi.mock('@/components/workspaces/NewWorkspaceSlideOver', () => ({ NewWorkspaceSlideOver: () => null }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn(),
    fetchSessions: vi.fn(),
    fetchAgents: vi.fn(),
    fetchSessionPage: vi.fn().mockResolvedValue({ sessions: [] }),
    logout: vi.fn().mockResolvedValue(undefined),
    fetchAppState: vi.fn().mockResolvedValue({
      onboarding_complete: true, dev_mode_bypass: false, identity: { mode: 'platform', edition: 'hosted', signed_in: true },
    } as never),
    fetchGodMode: vi.fn().mockResolvedValue({ enabled: false, available: true, supported: true, persisted: false }),
  }
})

import { fetchAgents, fetchSessions, fetchWorkspaces } from '@/lib/api'
import { Sidebar } from '@/components/layout/Sidebar'
import { SearchModal } from '@/components/search/SearchModal'

const WORKSPACE: Workspace = {
  id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0, task_count: 0,
  created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z', revision: '0'.repeat(64),
}
const AGENT = { id: 'mia', name: 'Mia', type: 'core', revision: '0'.repeat(64) } as unknown as Agent
const SESSION: Session = {
  id: 'session-q81', agent_id: 'mia', active_agent_id: 'mia', title: 'Q81 overlay check', type: 'chat',
  created_at: '2026-10-24T06:00:00Z', updated_at: '2026-10-24T06:05:00Z', message_count: 2, workspace_id: 'ws-1',
}

// The Sidebar's click-outside layer, located by its own markup.
function clickCatcher(): Element | null {
  return document.querySelector('div.absolute.inset-0.z-30[aria-hidden="true"]')
}

function renderShell() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Sidebar />
      <SearchModal />
    </QueryClientProvider>,
  )
}

const attachToSession = vi.fn<(...args: unknown[]) => boolean>()
const realAttach = useSessionStore.getState().attachToSession

beforeEach(() => {
  navigate.mockReset()
  attachToSession.mockReset().mockReturnValue(true)
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([WORKSPACE])
  vi.mocked(fetchSessions).mockReset().mockResolvedValue([SESSION])
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([AGENT])
  act(() => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachToSession: attachToSession as unknown as typeof realAttach })
  })
})

afterEach(() => {
  cleanup()
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
    useUiStore.getState().closeSearchModal()
    useSessionStore.setState({ attachToSession: realAttach })
  })
})

async function openSessionFromSearchModal() {
  // The Sessions entry point: the sidebar's own search action opens the
  // shared SearchModal over the still-open sidebar.
  act(() => { useUiStore.getState().openSearchModal() })
  renderShell()
  const row = await screen.findByRole('button', { name: /^Open Q81 overlay check/ })
  await userEvent.setup().click(row)
}

describe('Q81: opening a session from the search modal closes the unpinned sidebar overlay', () => {
  it('leaves no click-catcher over the chat after a same-workspace session switch', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })

    await openSessionFromSearchModal()

    expect(attachToSession).toHaveBeenCalledTimes(1)
    expect(attachToSession.mock.calls[0][0]).toBe('session-q81')
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' } }))
    expect(useUiStore.getState().searchModalOpen).toBe(false)
    await waitFor(() => expect(clickCatcher()).toBeNull())
    expect(useSidebarStore.getState()).toMatchObject({ isOpen: false, isPinned: false })
  })

  it('keeps a docked sidebar docked: there is no overlay and no click-catcher', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: true }) })

    await openSessionFromSearchModal()

    expect(attachToSession).toHaveBeenCalledTimes(1)
    expect(useUiStore.getState().searchModalOpen).toBe(false)
    expect(clickCatcher()).toBeNull()
    expect(useSidebarStore.getState()).toMatchObject({ isOpen: true, isPinned: true })
    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeInTheDocument()
  })

  it('changes nothing when the attach fails: the modal and the overlay both stay', async () => {
    attachToSession.mockReturnValue(false)
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })

    await openSessionFromSearchModal()

    expect(attachToSession).toHaveBeenCalledTimes(1)
    expect(navigate).not.toHaveBeenCalled()
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(useSidebarStore.getState()).toMatchObject({ isOpen: true, isPinned: false })
    expect(clickCatcher()).not.toBeNull()
  })
})
