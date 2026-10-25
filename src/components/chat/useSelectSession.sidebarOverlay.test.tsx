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
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { useSidebarStore } from '@/store/sidebar'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'

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
import { AgentRowMenu } from '@/components/layout/sidebar/AgentRowMenu'
import { ActivityBar } from '@/components/chat/ActivityBar'

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
      {/* The real History entry control for Mia's row, and the chat's own
          Activity bar, which opened the modal Activity panel after Open. */}
      <AgentRowMenu workspace={WORKSPACE} agent={AGENT} agentId="mia" agentName="Mia" />
      <main aria-label="Chat">
        <ActivityBar />
        {/* Composer stand-in: the target of the user's next click. */}
        <textarea aria-label="Message input" />
      </main>
    </QueryClientProvider>,
  )
}

const attachToSession = vi.fn<(...args: unknown[]) => boolean>()
const realAttach = useSessionStore.getState().attachToSession

beforeEach(() => {
  navigate.mockReset()
  // Like the real attach, a successful one makes the session the active chat.
  attachToSession.mockReset().mockImplementation((id) => { useSessionStore.setState({ activeSessionId: id as string }); return true })
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
    useSessionStore.setState({ activeSessionId: null })
    useSidebarStore.setState({ isOpen: false, isPinned: false })
    useUiStore.getState().closeSearchModal()
    useSessionStore.setState({ attachToSession: realAttach })
  })
})

async function openSessionFromHistory() {
  // The History entry point: the agent row's More actions menu → History opens
  // the same SearchModal filtered to this workspace and agent.
  renderShell()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'More actions for Mia' }))
  await user.click(await screen.findByRole('menuitem', { name: 'History' }))
  expect(useUiStore.getState()).toMatchObject({ searchModalOpen: true, searchModalWorkspaceFilter: 'ws-1', searchModalAgentFilter: 'mia' })
  await user.click(await screen.findByRole('button', { name: /^Open Q81 overlay check/ }))
}

async function expectStraightToChat() {
  // Founder ruling ("Straight to the chat"): after Open nothing modal covers
  // the chat, so the user's very next click lands in it.
  await waitFor(() => expect(useUiStore.getState().searchModalOpen).toBe(false))
  await act(async () => { await Promise.resolve() })
  expect(document.querySelector('[role="dialog"][aria-modal="true"]'), 'no modal layer is open').toBeNull()
  expect(document.body.style.pointerEvents, 'outside pointers are not locked').not.toBe('none')
  expect(screen.queryByText('No background activity yet.'), 'the Activity panel did not open by itself').toBeNull()
  const composer = screen.getByRole('textbox', { name: 'Message input' })
  await userEvent.setup().click(composer)
  expect(composer).toHaveFocus()
}

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

  it('agent History uses its real scoped menu and leaves no click-catcher after same-workspace Open', async () => {
    const main: Session = { ...SESSION, id: 'q81-mia-main', title: 'Mia main', type: 'main', needs_attention: false }
    const otherAgent: Session = { ...SESSION, id: 'q81-jim-other-history', title: 'Jim-only history control', agent_id: 'jim', active_agent_id: 'jim' }
    const withMember: Workspace = { ...WORKSPACE, core_team: ['mia'], member_configs: { mia: { main_session_id: main.id } } }
    vi.mocked(fetchWorkspaces).mockResolvedValue([withMember])
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core' }), makeAgent({ id: 'jim', name: 'Jim', type: 'core' }),
    ])
    vi.mocked(fetchSessions).mockResolvedValue([main, SESSION, otherAgent])
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    const user = userEvent.setup()
    renderShell()

    const actions = await within(screen.getByRole('navigation', { name: 'Main navigation' }))
      .findByRole('button', { name: 'More actions for Mia' })
    expect(clickCatcher(), 'instrument: this real History path starts with an open sidebar backdrop').not.toBeNull()
    await user.click(actions)
    await user.click(await screen.findByRole('menuitem', { name: 'History', exact: true }))
    const row = await screen.findByRole('button', { name: /^Open Q81 overlay check/ })
    expect(useUiStore.getState()).toMatchObject({ searchModalOpen: true, searchModalWorkspaceFilter: 'ws-1', searchModalAgentFilter: 'mia' })
    expect(screen.queryByRole('button', { name: /^Open Jim-only history control/ }), 'the real agent filter excludes another owner').toBeNull()
    expect(clickCatcher(), 'the backdrop must not have vanished before the History selection under test').not.toBeNull()
    await user.click(row)

    expect(attachToSession).toHaveBeenCalledTimes(1)
    expect(attachToSession.mock.calls[0][0]).toBe('session-q81')
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' } }))
    expect(useUiStore.getState().searchModalOpen).toBe(false)
    await waitFor(() => expect(clickCatcher(), 'M5: History must close the same unpinned overlay as Sessions').toBeNull())
    expect(useSidebarStore.getState()).toMatchObject({ isOpen: false, isPinned: false })
  })
})

describe('Founder ruling: Open from Sessions or History goes straight to the chat', () => {
  it('Sessions → Open: no panel opens by itself and the next click focuses the composer', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    await openSessionFromSearchModal()
    expect(useSessionStore.getState().activeSessionId).toBe('session-q81')
    expect(clickCatcher()).toBeNull()
    await expectStraightToChat()
  })

  it('History → Open: no panel opens by itself, no sidebar click-catcher, and the next click focuses the composer', async () => {
    act(() => { useSidebarStore.setState({ isOpen: true, isPinned: false }) })
    await openSessionFromHistory()
    expect(attachToSession).toHaveBeenCalledTimes(1)
    expect(useSessionStore.getState().activeSessionId).toBe('session-q81')
    await waitFor(() => expect(clickCatcher()).toBeNull())
    expect(useSidebarStore.getState()).toMatchObject({ isOpen: false, isPinned: false })
    await expectStraightToChat()
  })
})
