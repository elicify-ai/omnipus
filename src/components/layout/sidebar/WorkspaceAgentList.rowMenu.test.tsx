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

const { apiMocks, seam, entrySpy, entryResult, telemetryMocks } = vi.hoisted(() => ({
  apiMocks: {
    updateWorkspace: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchWorkspaceDelegation: vi.fn(),
  },
  seam: {
    mains: new Map<object, string>(),
    attention: new Map<string, 'on' | 'off' | 'unknown'>(),
  },
  entrySpy: vi.fn(),
  // The recovery entry's OUTCOME, controllable per test: resolves by default,
  // rejects when a test needs a failed follow-up after a successful removal.
  entryResult: vi.fn(() => Promise.resolve()),
  telemetryMocks: {
    logDiagnostic: vi.fn(),
  },
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  updateWorkspace: apiMocks.updateWorkspace,
  fetchWorkspace: apiMocks.fetchWorkspace,
  fetchWorkspaceDelegation: apiMocks.fetchWorkspaceDelegation,
}))

vi.mock('@/lib/telemetry', async (importOriginal) => ({
  // Real module except logDiagnostic: the production gate inside it (DEV /
  // MODE checks) would no-op under vitest, so the spy pins the RECORD the
  // row actions write — whether a diagnostic was attempted at all — while
  // the gate's own write-through behaviour stays the telemetry module's.
  ...await importOriginal<typeof import('@/lib/telemetry')>(),
  logDiagnostic: telemetryMocks.logDiagnostic,
}))

vi.mock('@/store/session/workspaceEntryFlow', async (importOriginal) => ({
  // Real module except the recovery entry itself: the spy records the
  // workspace's remembered descriptor AT CALL TIME so the test can pin the
  // removed member's binding being invalidated BEFORE recovery runs, and the
  // outcome comes from entryResult so a test can fail the follow-up.
  ...await importOriginal<typeof import('@/store/session/workspaceEntryFlow')>(),
  runWorkspaceEntry: (workspaceId: string) => {
    entrySpy(workspaceId, useSessionStore.getState().sessionByWorkspace[workspaceId] ?? null)
    return entryResult()
  },
}))

vi.mock('@/components/agents/AgentProfile', () => ({ AgentProfile: () => null }))

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useLocation: () => ({ pathname: '/' }),
  Link: ({ children }: { children: ReactNode }) => children,
}))

vi.mock('@/lib/nav/sessionCoreSeam', async (importOriginal) => ({
  // Real seam for everything the stubs don't override. (WU-11 GREEN removed the
  // Admin-main seam reader with the retired Admin row — FR-008.)
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
// The retired Admin agent fixture is deleted with the Admin row (FR-008).

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
  return { revision: REV, workspace_id: WS, edges, default_depth: 10 }
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
  entrySpy.mockReset()
  entryResult.mockReset()
  entryResult.mockImplementation(() => Promise.resolve())
  telemetryMocks.logDiagnostic.mockReset()
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
    // RTL's ByRole name option already full-matches the accessible name.
    expect(screen.getByText('1 hidden')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Show' })).toBeTruthy()
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
      fireEvent.click(screen.getByRole('button', { name: 'Show' }))
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

  it('a NON-conflict hide failure surfaces a visible error toast naming the agent (S4-06)', async () => {
    apiMocks.updateWorkspace.mockRejectedValue(new ApiError(500, 'storage exploded'))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')

    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Hide from sidebar' }))
    })

    // A 500 is NOT a conflict: setHiddenFromSidebar rethrows anything that is
    // not 409, so there is no refetch and no retry — the failure goes straight
    // to the caller's toast.
    expect(apiMocks.fetchWorkspace).not.toHaveBeenCalled()
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    const toast = useUiStore.getState().toasts.at(-1)
    expect(toast?.variant).toBe('error')
    expect(toast?.message).toContain('Could not hide Mia')
    expect(toast?.message).toContain('storage exploded')
  })

  it('"Show" recovers from a 409 exactly as Hide does: refetch, retry once with the fresh revision (S4-06)', async () => {
    apiMocks.updateWorkspace
      .mockRejectedValueOnce(new ApiError(409, 'workspace changed elsewhere'))
      .mockResolvedValueOnce(workspaceWith({ mia: miaMember, jim: jimMember }))
    const miaHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    const jimHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    // The FRESH record must still show both members HIDDEN, or the retry is
    // correctly skipped: setHiddenFromSidebar only re-sends the ids the fresh
    // record still disagrees about, and "already agrees" means "not hidden".
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaHidden, jim: jimHidden }, { revision: FRESH_REV }),
    )
    register(miaHidden, MIA_MAIN)
    register(jimHidden, JIM_MAIN)
    renderList({ workspace: workspaceWith({ mia: miaHidden, jim: jimHidden }) })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Show' }))
    })

    expect(apiMocks.fetchWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(2)
    expect(apiMocks.updateWorkspace).toHaveBeenLastCalledWith(WS, {
      revision: FRESH_REV,
      member_configs: {
        mia: { hidden_from_sidebar: false },
        jim: { hidden_from_sidebar: false },
      },
    })
    expect(useUiStore.getState().toasts, 'a recovered conflict is not an error').toEqual([])
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

  it('a 409 whose fresh record ALREADY agrees succeeds without a second PUT and without an error (explicit, S4-08)', async () => {
    apiMocks.updateWorkspace
      .mockRejectedValueOnce(new ApiError(409, 'workspace changed elsewhere'))
    const miaHidden: WorkspaceMemberConfig = { hidden_from_sidebar: true }
    register(miaHidden, MIA_MAIN)
    // The refetched record already carries hidden_from_sidebar for Mia —
    // another writer applied the hide between our PUT and its conflict.
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaHidden, jim: jimMember }, { revision: FRESH_REV }),
    )
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')

    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Hide from sidebar' }))
    })

    // Exactly ONE PUT (the conflicted attempt): the fresh record agreeing is
    // SUCCESS, not a skipped retry — no second PUT is sent, no toast.
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.fetchWorkspace).toHaveBeenCalledTimes(1)
    expect(useUiStore.getState().toasts, 'already-applied hide is not an error').toEqual([])
  })

  it('a failed hide writes a persistent diagnostic record, not only a 4 s toast (S4-03)', async () => {
    apiMocks.updateWorkspace.mockRejectedValue(new Error('network down'))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')

    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Hide from sidebar' }))
    })

    expect(telemetryMocks.logDiagnostic).toHaveBeenCalledWith(
      'agentRowActionFailed',
      expect.objectContaining({ action: 'Could not hide Mia', detail: 'network down' }),
    )
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

// The D4/D8 Remove suite, split into focused describe blocks (function-size
// budget: a describe callback is a function to scripts/tsfunlen.cjs). Test
// order and every assertion are unchanged from the pre-split single block.
describe('D4/D8 — Remove · dialog and the Team-tab PUT', () => {
  it('the dialog names the agent AND the workspace and says membership, not history, ends', async () => {
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    const dialog = screen.getByRole('alertdialog', { name: 'Remove Mia from Product launch?' })
    const description = dialog.textContent ?? ''
    // Copy re-pin (WU-11 GREEN 9871cbc66, FR-108): the dialog no longer names
    // the retired Agents list; it states exactly what ends and what remains.
    expect(description).toContain("Mia leaves only this workspace's team; the agent still exists, stays in its other workspaces and keeps its chat history.")
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

  it('confirm PUTs the Team-tab shape — core_team minus the agent, delegation minus its edges, incoming and outgoing alike — and never deletes sessions', async () => {
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(
      delegation([
        { from_agent: 'mia', to_agent: 'jim', modes: ['direct'] },
        { from_agent: 'jim', to_agent: 'mia', modes: ['direct'] },
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
})

describe('D4/D8 — Remove · failure surfaces (S4-05 rejection, S4-01 refusal)', () => {
  it('a FAILED Remove surfaces the rejection and does not claim success (S4-05)', async () => {
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(delegation([]))
    // removeWorkspaceMember's 409 path refetches the workspace and retries the
    // PUT ONCE (S4-07's shared conflict helper), so the fresh record must
    // resolve — an unmocked fetch leaves removalBody reading core_team off
    // undefined and the toast carries THAT TypeError instead of the real one.
    // The fresh record must STILL contain Mia: a record that no longer does
    // counts as "already removed elsewhere" (S4-08) and resolves as success
    // without a second PUT, which is not the failure this test is about.
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { core_team: ['mia', 'jim'] }),
    )
    apiMocks.updateWorkspace.mockRejectedValue(new ApiError(409, 'workspace changed elsewhere'))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    // The rejection must NOT be swallowed: dropping confirmRemove's .catch
    // would leave a 409/422/network failure completely silent, which is
    // exactly what the review flagged.
    const toast = useUiStore.getState().toasts.at(-1)
    expect(toast?.variant).toBe('error')
    expect(toast?.message).toContain('Could not remove Mia')
    expect(toast?.message).toContain('workspace changed elsewhere')
    // ...and a failed removal must not refresh the roster as if it had landed.
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('a cached record WITHOUT core_team refetches and builds the body from the fresh list — never core_team: [] (S4-01)', async () => {
    // The cached record carries no core_team (optional on the wire), so the
    // roster renders from member_configs keys — exactly the stale-cache shape
    // the review flagged. The fresh record HAS the team.
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { revision: FRESH_REV }),
    )
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(
      delegation([{ from_agent: 'jim', to_agent: 'jim', modes: ['task'] }]),
    )
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ jim: jimMember }))
    renderList({
      workspace: workspaceWith({ mia: miaMember, jim: jimMember }, { core_team: undefined }),
    })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    expect(apiMocks.fetchWorkspace).toHaveBeenCalledWith(WS)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    // core_team is a full-replacement field: the body MUST carry the fresh
    // list minus Mia, never a list rebuilt from the missing data.
    expect(apiMocks.updateWorkspace).toHaveBeenCalledWith(WS, {
      revision: FRESH_REV,
      core_team: ['jim'],
      delegation: [{ from_agent: 'jim', to_agent: 'jim', modes: ['task'] }],
    })
  })

  it('a member_configs-only row — no core_team even after the refetch — finishes the removal by clearing the stale config (CR-04)', async () => {
    // CR-04 (= SFH-02): the server omits core_team only when the stored team
    // is EMPTY (rest_workspaces.go toWire gates on len(CoreTeam) > 0), yet
    // workspaceRoster.ts::memberRoster still renders a row from the
    // member_configs key — a stale config that survived its member's removal.
    // The R2-S4-02 refusal was truthful but a dead end: the same error every
    // time, the row forever. The fresh read (a missing list is never trusted)
    // IS the empty team, so Remove completes through the EXISTING PUT
    // contract: an EMPTY member_configs merge map, which drives the server's
    // own GC — entries whose agent is off the (empty) team are pruned
    // (rest_workspaces.go applyUpdate) — and the row loses its fallback key.
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { core_team: undefined, revision: FRESH_REV }),
    )
    apiMocks.updateWorkspace.mockResolvedValue(
      workspaceWith({}, { core_team: undefined, revision: '2'.repeat(64) }),
    )
    renderList({
      workspace: workspaceWith({ mia: miaMember, jim: jimMember }, { core_team: undefined }),
    })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    // The fresh record is read first (the cached record's missing list is
    // never trusted)…
    expect(apiMocks.fetchWorkspace).toHaveBeenCalledWith(WS)
    // …and exactly ONE PUT rides the existing contract: revision + the empty
    // member_configs map. core_team is deliberately NOT re-asserted — it is
    // already empty, and a PUT equal to the stored team sets no changed flag,
    // so the server would skip the write and lose the GC.
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledWith(WS, {
      revision: FRESH_REV,
      member_configs: {},
    })
    // The team path's delegation strip has nothing to do here: no team
    // membership means no edges are stripped and the delegation seam is
    // never read. (R2's ruling stands: no session is ever released.)
    expect(apiMocks.fetchWorkspaceDelegation).not.toHaveBeenCalled()
    // Success surfaces: the roster refresh lands and no error toast — the
    // dead end is gone.
    expect(invalidateSpy).toHaveBeenCalled()
    expect(useUiStore.getState().toasts, 'a completed removal is not an error').toEqual([])
  })
})

describe('D4/D8 — Remove · bounded 409 recovery (S4-07)', () => {
  it('on 409 refetches the workspace and retries ONCE with the fresh revision (same bounded retry as Hide, S4-07)', async () => {
    apiMocks.updateWorkspace
      .mockRejectedValueOnce(new ApiError(409, 'workspace changed elsewhere'))
      .mockResolvedValueOnce(workspaceWith({ jim: jimMember }, { core_team: ['jim'] }))
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { revision: FRESH_REV }),
    )
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(
      delegation([{ from_agent: 'jim', to_agent: 'jim', modes: ['task'] }]),
    )
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    expect(apiMocks.fetchWorkspace).toHaveBeenCalledTimes(1)
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(2)
    expect(apiMocks.updateWorkspace).toHaveBeenLastCalledWith(WS, {
      revision: FRESH_REV,
      core_team: ['jim'],
      delegation: [{ from_agent: 'jim', to_agent: 'jim', modes: ['task'] }],
    })
    expect(useUiStore.getState().toasts, 'a recovered conflict is not an error').toEqual([])
  })

  it('a second 409 on Remove shows a visible error toast and stops (S4-07 bound)', async () => {
    const conflict = () => new ApiError(409, 'workspace changed elsewhere')
    apiMocks.updateWorkspace.mockRejectedValue(conflict())
    apiMocks.fetchWorkspace.mockResolvedValue(
      workspaceWith({ mia: miaMember, jim: jimMember }, { revision: FRESH_REV }),
    )
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(delegation([]))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(2)
    expect(apiMocks.fetchWorkspace).toHaveBeenCalledTimes(1)
    const toast = useUiStore.getState().toasts.at(-1)
    expect(toast?.variant).toBe('error')
    expect(toast?.message).toContain('workspace changed elsewhere')
  })
})

describe('D4/D8 — Remove · post-removal reporting and binding (S4-02/S4-03)', () => {
  it('a failed RECOVERY after a successful removal reports the true state — removed, not failed (S4-02)', async () => {
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(delegation([]))
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ jim: jimMember }, { core_team: ['jim'] }))
    entryResult.mockRejectedValueOnce(new Error('entry failed'))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    // Mia's main is the OPEN chat in this workspace, so recovery runs.
    act(() => {
      useSessionStore.setState({
        activeSessionId: MIA_MAIN,
        activeAgentId: 'mia',
      })
    })

    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    // The removal PUT succeeded — the toast must not say the removal failed.
    expect(apiMocks.updateWorkspace).toHaveBeenCalledTimes(1)
    const toast = useUiStore.getState().toasts.at(-1)
    expect(toast?.variant).toBe('error')
    expect(toast?.message).toContain('Removed Mia')
    expect(toast?.message).not.toContain('Could not remove Mia')
  })

  it('a failed REMOVAL itself still reports "Could not remove" (S4-02 control)', async () => {
    apiMocks.updateWorkspace.mockRejectedValue(new Error('422: bad request'))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    const toast = useUiStore.getState().toasts.at(-1)
    expect(toast?.variant).toBe('error')
    expect(toast?.message).toContain('Could not remove Mia')
  })

  it('a failed remove writes a persistent diagnostic record, not only a 4 s toast (S4-03)', async () => {
    // Every seam before the PUT works, so the failure the diagnostic records
    // is the removal itself, not an incidental crash on the way to it.
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(delegation([]))
    apiMocks.updateWorkspace.mockRejectedValue(new Error('network down'))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    expect(telemetryMocks.logDiagnostic).toHaveBeenCalledWith(
      'agentRowActionFailed',
      expect.objectContaining({ action: 'Could not remove Mia', detail: 'network down' }),
    )
  })

  it('removing the selected member clears the remembered binding BEFORE recovery runs, so entry does not short-circuit', async () => {
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(delegation([]))
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ jim: jimMember }, { core_team: ['jim'] }))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    // Mia's main is the OPEN chat and the workspace's remembered one.
    act(() => {
      useSessionStore.setState({
        activeSessionId: MIA_MAIN,
        activeAgentId: 'mia',
        sessionByWorkspace: {
          ...useSessionStore.getState().sessionByWorkspace,
          [WS]: { id: MIA_MAIN, type: 'chat', title: 'Mia main', agentId: 'mia' },
        },
      })
    })

    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    expect(entrySpy).toHaveBeenCalledWith(WS, null)
    // And the binding STAYS explicitly fresh afterwards.
    expect(useSessionStore.getState().sessionByWorkspace[WS] ?? null).toBeNull()
  })
})

describe('D4/D8 — Remove · disabled with a visible reason', () => {
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

  // The "workspace default agent" refusal test was deleted with the feature
  // (omnipus-system-agent K11/K12, bundle B1): there is no default agent any
  // more, so `removeDisabledReason` no longer carries that clause and the
  // Team-panel parity it asserted has no subject.
  // The "Admin row gets History and Edit but no Hide" test was deleted with
  // the Admin row itself (FR-008): post-GREEN the sidebar renders no Admin row
  // at all, so its menu has no subject; the member-row menu contract it
  // shared is pinned by the D1/D2, D3, D5 and locked-default cases above.
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

  // The "Admin's GLOBAL profile" test was deleted with the Admin row (FR-008):
  // the isAdminRow branch that opened the editor without workspace context is
  // removed, and every surviving row opens the editor with its workspace.
})

describe('S4-11/T4 — Remove, remembered-but-not-selected', () => {
  it('clears the remembered binding WITHOUT running entry recovery when the open chat belongs to another agent', async () => {
    apiMocks.fetchWorkspaceDelegation.mockResolvedValue(delegation([]))
    apiMocks.updateWorkspace.mockResolvedValue(workspaceWith({ jim: jimMember }, { core_team: ['jim'] }))
    renderList({ workspace: workspaceWith({ mia: miaMember, jim: jimMember }) })
    // The workspace REMEMBERS Mia's chat, but the OPEN chat is Jim's. The two
    // halves of AgentRowMenu::confirmRemove's branch disagree on purpose:
    // remembersRemoved=true, ownsSelectedChat=false.
    act(() => {
      useSessionStore.setState({
        activeSessionId: JIM_MAIN,
        activeAgentId: 'jim',
        sessionByWorkspace: {
          ...useSessionStore.getState().sessionByWorkspace,
          [WS]: { id: MIA_MAIN, type: 'chat', title: 'Mia main', agentId: 'mia' },
        },
      })
    })

    await openRowMenu('Mia')
    await act(async () => {
      fireEvent.click(screen.getByRole('menuitem', { name: 'Remove' }))
    })
    await act(async () => {
      fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    })

    // The stale pointer to the removed member's chat is cleared...
    expect(useSessionStore.getState().sessionByWorkspace[WS] ?? null).toBeNull()
    // ...but entry recovery must NOT run: the person is sitting on ANOTHER
    // agent's chat, and running recovery here would yank them off it.
    expect(
      entrySpy,
      'a remembered-but-not-selected removal must not re-run workspace entry',
    ).not.toHaveBeenCalled()
  })
})
