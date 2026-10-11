/**
 * The sidebar agent-row "⋯" menu (DECISIONS.md D1–D9, ADR-hide-from-sidebar).
 *
 * Oracles, in decision order:
 *   D1  ONE "⋯" trigger per row ("More actions for <Agent>"); the "Past
 *       sessions with X" / "New chat with X" buttons are gone.
 *   D2  Menu items in order: History · Hide from sidebar · Edit · Remove.
 *       Keyboard (Arrow/Escape) comes from the catalogued Radix primitive —
 *       this file mounts the REAL dropdown-menu (no stub).
 *   D3  History opens the same past-chats modal as the old button:
 *       openSearchModal(workspace.id, agentId).
 *   D5  Hide PUTs the minimal member_configs body with the workspace's
 *       revision; 409 refetches and retries ONCE; a second conflict shows an
 *       error toast; the hidden row leaves the list and "N hidden · Show"
 *       appears; Show un-hides in one PUT; a hidden agent's attention still
 *       counts on the collapsed workspace row.
 *   D4/D8 Remove names the agent AND the workspace, says membership ends but
 *       history and Agents-list presence stay, PUTs the Team-tab shape
 *       (core_team minus the agent + delegation minus its edges — never a
 *       session delete), cancel does nothing; Remove is disabled with a
 *       visible reason for a locked built-in on the DEFAULT workspace and for
 *       the Admin row (Admin additionally gets no Hide item).
 *   D6  Edit opens the unified edit flow via the same store entry the Team
 *       tab uses (editAgentId + editAgentWorkspaceId).
 *
 * REAL: WorkspaceAgentList, AgentRowMenu, the Radix dropdown primitive,
 * ConfirmDialog, the ui/session/workspaces/chat stores, newChatFlow,
 * beginPairExtra. MOCK: the router, the session-core seam, the AgentProfile
 * slide-over (its own tests cover it), and the three workspace API calls the
 * menu's actions make (updateWorkspace / fetchWorkspace /
 * fetchWorkspaceDelegation).
 */
import type { ReactNode } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-error'
import type {
  Session,
  Workspace,
  WorkspaceDelegation,
  WorkspaceMemberConfig,
} from '@/lib/api'
import { makeAgent } from '@/test/factories'
import { queryClient } from '@/lib/queryClient'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { WorkspaceAgentList } from './WorkspaceAgentList'

const WS = 'product-launch'
const WS_NAME = 'Product launch'
const REV = '0'.repeat(64)
const FRESH_REV = '1'.repeat(64)
const MIA_MAIN = 'seam-main-mia'
const JIM_MAIN = 'seam-main-jim'
const ADMIN_MAIN = 'seam-main-admin'

const miaMember: WorkspaceMemberConfig = {}
const jimMember: WorkspaceMemberConfig = {}
const adminMember: WorkspaceMemberConfig = {}

const { apiMocks, seam } = vi.hoisted(() => ({
  apiMocks: {
    updateWorkspace: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchWorkspaceDelegation: vi.fn(),
  },
  seam: {
    mains: new Map<object, string>(),
    attention: new Map<string, 'on' | 'off' | 'unknown'>(),
  },
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  updateWorkspace: apiMocks.updateWorkspace,
  fetchWorkspace: apiMocks.fetchWorkspace,
  fetchWorkspaceDelegation: apiMocks.fetchWorkspaceDelegation,
}))

vi.mock('@/components/agents/AgentProfile', () => ({ AgentProfile: () => null }))

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: ReactNode }) => children,
}))

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  // Real seam for everything the stubs don't override (notably
  // adminMainSessionIdOfWorkspace: the Admin-main rule stays real).
  ...await importOriginal<typeof import('@/lib/nav/sessionCoreSeam')>(),
  mainSessionIdOfMember: (member: object) => seam.mains.get(member),
  isMainSession: (session: unknown) => {
    const id = session && typeof session === 'object' && 'id' in session
      ? String((session as { id: unknown }).id)
      : ''
    return [MIA_MAIN, JIM_MAIN, ADMIN_MAIN].includes(id)
  },
  sessionAttention: (session: unknown) => {
    const id = session && typeof session === 'object' && 'id' in session
      ? String((session as { id: unknown }).id)
      : ''
    return seam.attention.get(id) ?? 'off'
  },
  attachAckFields: () => ({}),
  attentionBoundOfFrame: (_frame: unknown) => {
    void _frame
    return undefined
  },
}))

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  })
}
if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

function workspaceWith(
  members: Record<string, WorkspaceMemberConfig>,
  overrides: Partial<Workspace> = {},
): Workspace {
  return {
    id: WS,
    name: WS_NAME,
    is_default: false,
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    revision: REV,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    core_team: ['mia', 'jim'],
    member_configs: members,
    ...overrides,
  }
}

const mia = makeAgent({ id: 'mia', name: 'Mia', type: 'core' })
const jim = makeAgent({ id: 'jim', name: 'Jim', type: 'core' })
const admin = makeAgent({ id: 'admin', name: 'Admin', type: 'core' })

function chatSession(id: string, agentId: string): Session {
  return {
    id,
    type: 'chat',
    agent_id: agentId,
    title: id,
    workspace_id: WS,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 0,
  }
}

function delegation(edges: WorkspaceDelegation['edges']): WorkspaceDelegation {
  return { revision: REV, workspace_id: WS, edges, default_depth: 3 }
}

let invalidateSpy: ReturnType<typeof vi.spyOn>

function renderList(options: {
  workspace: Workspace
  agents?: typeof mia[]
  sessions?: Session[]
  expandedIds?: Set<string>
}): { rerender: (next: Workspace) => void } {
  const view = render(
    <WorkspaceAgentList
      projects={[options.workspace]}
      expandedIds={options.expandedIds ?? new Set([WS])}
      activeId={WS}
      agents={options.agents ?? [mia, jim]}
      rosterState="fresh"
      sessions={options.sessions ?? [chatSession(MIA_MAIN, 'mia'), chatSession(JIM_MAIN, 'jim')]}
      onToggle={() => {}}
      onOpen={() => {}}
      onOverlayClose={() => {}}
    />,
  )
  return {
    rerender: (next: Workspace) =>
      view.rerender(
        <WorkspaceAgentList
          projects={[next]}
          expandedIds={options.expandedIds ?? new Set([WS])}
          activeId={WS}
          agents={options.agents ?? [mia, jim]}
          rosterState="fresh"
          sessions={options.sessions ?? [chatSession(MIA_MAIN, 'mia'), chatSession(JIM_MAIN, 'jim')]}
          onToggle={() => {}}
          onOpen={() => {}}
          onOverlayClose={() => {}}
        />,
      ),
  }
}

function register(member: WorkspaceMemberConfig, mainId: string) {
  seam.mains.set(member, mainId)
}

/** Opens the row's ⋯ menu the way a pointer does (Radix opens on pointer down).
 * The row and trigger are resolved BEFORE opening: a Radix modal menu marks
 * content outside its portal aria-hidden, so the row leaves the accessibility
 * tree while the menu is open. */
async function openRowMenu(agentName: string) {
  const row = screen.getByRole('group', { name: agentName })
  const trigger = within(row).getByRole('button', { name: `More actions for ${agentName}` })
  await act(async () => {
    fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
    fireEvent.pointerUp(trigger, { button: 0, pointerType: 'mouse' })
  })
  return trigger
}

function menuItems(): HTMLElement[] {
  return screen.getAllByRole('menuitem')
}

beforeEach(() => {
  seam.mains.clear()
  seam.attention.clear()
  register(miaMember, MIA_MAIN)
  register(jimMember, JIM_MAIN)
  apiMocks.updateWorkspace.mockReset()
  apiMocks.fetchWorkspace.mockReset()
  apiMocks.fetchWorkspaceDelegation.mockReset()
  invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue([] as never)
  act(() => {
    useUiStore.setState({
      toasts: [],
      searchModalOpen: false,
      searchModalWorkspaceFilter: null,
      searchModalMode: 'sessions',
      editAgentId: null,
      editAgentWorkspaceId: null,
    })
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
  })
})

afterEach(() => {
  cleanup()
  invalidateSpy.mockRestore()
  act(() => {
    useUiStore.setState({
      toasts: [],
      searchModalOpen: false,
      searchModalWorkspaceFilter: null,
      editAgentId: null,
      editAgentWorkspaceId: null,
    })
  })
})

describe('D1/D2 — one ⋯ trigger and the catalogued menu', () => {
  it('renders exactly one ⋯ trigger per row and none of the two old buttons', () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    const miaRow = screen.getByRole('group', { name: 'Mia' })
    expect(within(miaRow).getAllByRole('button', { name: 'More actions for Mia' })).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Past sessions with Mia' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'New chat with Mia' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Past sessions with Jim' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'New chat with Jim' })).toBeNull()
  })

  it('opens the menu with History, Hide from sidebar, Edit, Remove in that order', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    expect(menuItems().map((item) => item.textContent)).toEqual([
      'History',
      'Hide from sidebar',
      'Edit',
      'Remove',
    ])
  })

  it('Arrow keys move focus between items and Escape closes the menu (catalogued primitive)', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    const menu = screen.getByRole('menu')

    // Radix's roving focus moves focus inside a setTimeout (react-roving-focus
    // RovingFocusGroupItem.onKeyDown), so the outcome is awaited, not assumed
    // synchronous. A real keyboard's keydown lands on the FOCUSED element:
    // the first ArrowDown enters from the menu content, later ones must fire
    // on the focused item — the group handler ignores events whose target is
    // not itself, and the ITEM's handler is what computes "next from here".
    fireEvent.keyDown(menu, { key: 'ArrowDown' })
    await waitFor(() => expect(menuItems()[0]).toHaveFocus())

    fireEvent.keyDown(menuItems()[0], { key: 'ArrowDown' })
    await waitFor(() => expect(menuItems()[1]).toHaveFocus())

    fireEvent.keyDown(menu, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
  })
})

describe('D3 — History', () => {
  it('opens the past-chats modal scoped to this workspace and agent', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'History' }))
    })
    const ui = useUiStore.getState()
    expect(ui.searchModalOpen).toBe(true)
    expect(ui.searchModalWorkspaceFilter).toBe(WS)
    expect(ui.searchModalAgentFilter).toBe('mia')
  })
})

describe('D5 — Hide from sidebar', () => {
  it('PUTs the minimal hidden_from_sidebar body with the workspace revision; the row leaves the list and "1 hidden · Show" appears', async () => {
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ mia: miaMember }))
    const { rerender } = renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')

    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Hide from sidebar' }))
    })

    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledWith(WS, {
      revision: REV,
      member_configs: { mia: { hidden_from_sidebar: true } },
    })
    expect(invalidateSpy).toHaveBeenCalled()

    const miaHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    register(miaHidden, MIA_MAIN)
    rerender(workspaceWith({ mia: miaHidden, jim: jimMember }))

    expect(screen.queryByRole('group', { name: 'Mia' })).toBeNull()
    // The quiet line: the count as text, and a control named exactly "Show"
    // (the e2e contract — AGENT_ROW_MENU_ITEMS/spec — reads both surfaces).
    expect(screen.getByText('1 hidden')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Show', exact: true })).toBeTruthy()
  })

  it('"Show" un-hides every hidden agent of the workspace in one PUT', async () => {
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ mia: miaMember, jim: jimMember }))
    const miaHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    const jimHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    register(miaHidden, MIA_MAIN)
    register(jimHidden, JIM_MAIN)
    renderList({ workspace: workspaceWith({ mia: miaHidden, jim: jimHidden }) })
    expect(screen.getByText('2 hidden')).toBeTruthy()

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Show', exact: true }))
    })

    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledWith(WS, {
      revision: REV,
      member_configs: {
        mia: { hidden_from_sidebar: false },
        jim: { hidden_from_sidebar: false },
      },
    })
  })

  it('on 409 refetches the workspace and retries ONCE with the fresh revision', async () => {
    apiMocks.updateWorkspace
      .mockRejectedValueOnce(new ApiError(409, 'workspace changed elsewhere'))
      .mockResolvedValueOnce(workspaceWith({ mia: miaMember }))
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { revision: FRESH_REV }),
    )
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')

    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Hide from sidebar' }))
    })

    expect(apiMocks.fetchWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(2)
    expect(apiMocks.updateWorkspace).toHaveBeenLastCalledWith(WS, {
      revision: FRESH_REV,
      member_configs: { mia: { hidden_from_sidebar: true } },
    })
    expect(useUiStore.getState().toasts, 'a recovered conflict is not an error').toEqual([])
  })

  it('a second 409 shows a visible error toast and stops', async () => {
    const conflict = () => new ApiError(409, 'workspace changed elsewhere')
    apiMocks.updateWorkspace.mockRejectedValue(conflict())
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { revision: FRESH_REV }),
    )
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')

    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Hide from sidebar' }))
    })

    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(2)
    expect(apiMocks.fetchWorkspace).toHaveBeenCalledTimes(1)
    const toast = useUiStore.getState().toasts.at(-1)
    expect(toast?.variant).toBe('error')
    expect(toast?.message).toContain('workspace changed elsewhere')
  })

  it("a hidden agent's attention still counts on the collapsed workspace row", () => {
    const miaHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    register(miaHidden, MIA_MAIN)
    seam.attention.set(MIA_MAIN, 'on')
    renderList({
      workspace: workspaceWith({ mia: miaHidden, jim: jimMember }),
      expandedIds: new Set<string>(),
    })
    expect(screen.getByRole('status', { name: '1 main chat needs your attention' })).toBeTruthy()
  })
})

describe('D4/D8 — Remove', () => {
  it('the dialog names the agent AND the workspace and says membership, not history, ends', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    const dialog = screen.getByRole('alertdialog', { name: 'Remove Mia from Product launch?' })
    const description = dialog.textContent ?? ''
    expect(description).toContain('Agents list')
    expect(description).toContain('chat history')
  })

  it('cancel closes the dialog and calls nothing', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    })
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(apiMocks.updateWorkspace).not.toHaveBeenCalled()
  })

  it('confirm PUTs the Team-tab shape — core_team minus the agent, delegation minus its edges — and never deletes sessions', async () => {
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(
      delegation([
        { from_agent: 'mia', to_agent: 'jim', modes: ['direct'] },
        { from_agent: 'jim', to_agent: 'jim', modes: ['task'] },
      ]),
    )
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ jim: jimMember }, { core_team: ['jim'] }))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    expect(apiMocks.fetchWorkspaceDelegation).toHaveBeenCalledWith(WS)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledWith(WS, {
      revision: REV,
      core_team: ['jim'],
      delegation: [{ from_agent: 'jim', to_agent: 'jim', modes: ['task'] }],
    })
    // The only mutation the menu makes is the one workspace PUT above; the
    // three API seams this suite mocks are the entire request surface the
    // action can reach (R2: removal never deletes the main chat).
    expect(apiMocks.fetchWorkspace).not.toHaveBeenCalled()
  })

  it('Remove is disabled with a visible reason for a locked built-in on the default workspace', async () => {
    const lockedMia = makeAgent({ id: 'mia', name: 'Mia', type: 'core', locked: true })
    renderList({
      workspace: workspaceWith({ mia: miaMember, jim: jimMember }, { is_default: true }),
      agents: [lockedMia, jim],
    })
    await openRowMenu('Mia')
    const remove = screen.getByRole('menuitem', { name: /Remove/ })
    expect(remove).toHaveAttribute('data-disabled')
    expect(remove.textContent).toContain('default workspace')
    await act(async () => {
      fireEvent.click(remove)
    })
    expect(apiMocks.updateWorkspace).not.toHaveBeenCalled()
  })

  it('Remove is disabled with a visible reason for the workspace default agent (Team-panel parity)', async () => {
    const defaultJim = makeAgent({ id: 'jim', name: 'Jim', type: 'core', default: true })
    renderList({
      workspace: workspaceWith({ mia: miaMember, jim: jimMember }),
      agents: [mia, defaultJim],
    })
    await openRowMenu('Jim')
    const remove = screen.getByRole('menuitem', { name: /Remove/ })
    expect(remove).toHaveAttribute('data-disabled')
    expect(remove.textContent).toContain('Jim is the default agent')
    await act(async () => {
      fireEvent.click(remove)
    })
    expect(apiMocks.updateWorkspace).not.toHaveBeenCalled()
  })

  it('the Admin row gets History and Edit but no Hide, and a Remove disabled with a reason', async () => {
    register(adminMember, ADMIN_MAIN)
    renderList({
      workspace: workspaceWith(
        { mia: miaMember },
        {
          is_default: true,
          core_team: ['mia'],
          admin_main_session_id: ADMIN_MAIN,
        },
      ),
      agents: [mia, admin],
      sessions: [chatSession(MIA_MAIN, 'mia'), chatSession(ADMIN_MAIN, 'admin')],
    })
    await openRowMenu('Admin')

    const names = menuItems().map((item) => item.textContent)
    expect(names).toContain('History')
    expect(names).toContain('Edit')
    expect(names).not.toContain('Hide from sidebar')
    const remove = screen.getByRole('menuitem', { name: /Remove/ })
    expect(remove).toHaveAttribute('data-disabled')
    expect(remove.textContent).toContain('not a workspace member')
  })
})

describe('D6 — Edit', () => {
  it('opens the unified edit flow with the workspace context, the Team-tab entry point', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Edit' }))
    })
    expect(useUiStore.getState().editAgentId).toBe('mia')
    expect(useUiStore.getState().editAgentWorkspaceId).toBe(WS)
  })
})
