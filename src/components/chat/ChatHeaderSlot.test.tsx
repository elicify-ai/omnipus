/**
 * ChatHeaderSlot — the workspace header line's right-end slot (#1449 D13,
 * founder 2026-10-28: "the existing header should stay on the left and not
 * change location"; "I do not want to see header borders or anything; stick
 * to our minimal design").
 *
 * The slot carries the chat's own chrome and nothing else: the chat label /
 * helper breadcrumb (the old grey, bottom-bordered bar — now un-bordered
 * text) and the chat controls ("Add participant" as an icon-only ghost
 * button). No row, no bar, no border, no background of its own.
 *
 * The rules under test:
 *   - "Add participant" keeps its visibility rules — a member's MAIN chat
 *     only, never Omnipus's chat, never a helper/task chat — and stays
 *     hidden while the workspace entry is still being resolved (a switch
 *     keeps the PREVIOUS workspace's session until the new one lands);
 *   - a ROOM page shows no chat chrome at all: the room owns its own
 *     controls until the group Participants button lands;
 *   - the old bar's replacement carries no border and no background, and the
 *     honest "agent names unavailable" warning is never clipped away with
 *     the truncating identity;
 *   - a narrow header line truncates the breadcrumb and keeps the full text
 *     in its title.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { fetchAgents, fetchSessions, fetchWorkspaces } from '@/lib/api'
import type { Agent, Session } from '@/lib/api'

const selectSession = vi.hoisted(() => vi.fn())
vi.mock('./useSelectSession', () => ({ useSelectSession: () => selectSession }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: vi.fn(),
  fetchSessions: vi.fn(),
  fetchWorkspaces: vi.fn(),
}))

// The slot reads the ROUTE to decide whether the chat on screen is this
// workspace's session chat. Only the location primitive is needed here.
let mockPathname = '/workspaces/ws_slot_test/chat'
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname, search: {} }),
  useNavigate: () => vi.fn(),
}))

import { ChatHeaderSlot } from './ChatHeaderSlot'

const WORKSPACE_ID = 'ws_slot_test'
const MAIN_ID = 'sid-main'
const CHILD_ID = 'sid-child'
const PARENT_ID = 'sid-parent'

const mia = { id: 'mia', name: 'Mia', type: 'core', locked: true, figure: {}, role: {}, revision: {} } as unknown as Agent

function session(overrides: Partial<Session>): Session {
  return { id: 'x', type: 'chat', title: '', status: 'active', agent_id: 'mia', ...overrides } as Session
}

/** A member's main chat in the workspace. */
function setMainChat(agentId = 'mia'): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: MAIN_ID,
    activeAgentId: agentId,
    attachedSessionType: 'main',
    attachedTaskTitle: null,
    sessionByWorkspace: { [WORKSPACE_ID]: { id: MAIN_ID, type: 'main', title: 'Mia - Main Session', agentId } },
  })
}

/** A helper chat opened from a parent chat. */
function setHelperChat(): void {
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
  useSessionStore.setState({
    activeSessionId: CHILD_ID,
    activeAgentId: 'mia',
    attachedSessionType: 'delegate',
    attachedTaskTitle: null,
    sessionByWorkspace: { [WORKSPACE_ID]: { id: CHILD_ID, type: 'delegate', title: 'child', agentId: 'mia' } },
  })
}

function renderSlot() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}><ChatHeaderSlot workspaceId={WORKSPACE_ID} /></QueryClientProvider>)
}

/**
 * The workspace-entry gates, cleared before EVERY case.
 *
 * Both gates hide the Add participant control, so a case that leaves one
 * behind would pass for the wrong reason: the failed-entry case would be
 * satisfied by the preceding resolving-entry flag and would prove nothing
 * about the failed-entry branch. Each case sets exactly the state it means
 * to test.
 */
function clearEntryGates(): void {
  useSessionStore.setState({ resolvingSessionForWorkspace: {}, workspaceEntry: null })
}

beforeEach(() => {
  selectSession.mockReset()
  mockPathname = `/workspaces/${WORKSPACE_ID}/chat`
  clearEntryGates()
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([mia])
  vi.mocked(fetchWorkspaces).mockReset().mockResolvedValue([])
  vi.mocked(fetchSessions).mockReset().mockResolvedValue([])
})

describe('ChatHeaderSlot — the chat controls in the header line', () => {
  it('shows "Add participant" in a member main chat as an icon-only control', async () => {
    setMainChat()
    renderSlot()
    const control = screen.getByRole('button', { name: 'Add participant' })
    expect(control).toBeVisible()
    // Icon-only: no visible word inside the control, one Phosphor glyph.
    expect(control.textContent).toBe('')
    expect(control.querySelector('svg')).not.toBeNull()
    // The control lives in the header slot, not in a row of its own.
    expect(screen.getByTestId('chat-header-slot').contains(control)).toBe(true)
  })

  it('hides "Add participant" in Omnipus’s chat', () => {
    setMainChat('omnipus')
    renderSlot()
    expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull()
  })

  it('hides "Add participant" in a helper chat (it is a main-chat control)', () => {
    setHelperChat()
    renderSlot()
    expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull()
  })

  it('the control carries the catalogued tooltip "Add participant"', async () => {
    setMainChat()
    renderSlot()
    const control = screen.getByRole('button', { name: 'Add participant' })
    await userEvent.hover(control)
    const tooltip = await screen.findByRole('tooltip')
    expect(tooltip).toHaveTextContent('Add participant')
  })

  it('a task session shows its task label in the slot', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    useSessionStore.setState({
      activeSessionId: 'sid-task',
      activeAgentId: 'mia',
      attachedSessionType: 'task',
      attachedTaskTitle: 'Ship the deck',
      sessionByWorkspace: {},
    })
    renderSlot()
    expect(await screen.findByText('Task: Ship the deck')).toBeVisible()
  })

  it('the old grey bar is gone — the replacement carries no border and no background', async () => {
    setMainChat()
    renderSlot()
    const label = await screen.findByTestId('feed-kind-label')
    expect(label.className).not.toMatch(/border/)
    expect(label.className).not.toMatch(/bg-/)
    const slot = screen.getByTestId('chat-header-slot')
    expect(slot.className).not.toMatch(/border/)
    expect(slot.className).not.toMatch(/bg-/)
    // The bar is not a row of its own any more: the slot is the only place
    // the chat label is rendered.
    expect(screen.getAllByTestId('feed-kind-label')).toHaveLength(1)
  })

  it('a helper chat shows "‹ Parent / Helper" in the slot, with the parent as the link', async () => {
    const parent = session({ id: PARENT_ID, type: 'main', title: 'stale stored title' })
    vi.mocked(fetchSessions).mockResolvedValue([
      parent,
      session({ id: CHILD_ID, type: 'delegate', title: 'child', parent_session_id: PARENT_ID }),
    ])
    setHelperChat()
    renderSlot()
    const label = await screen.findByTestId('feed-kind-label')
    await waitFor(() => {
      expect(label.textContent).toBe('Mia - Main Session / Helper')
    })
    const parentLink = screen.getByRole('button', { name: 'Mia - Main Session' })
    expect(screen.getByTestId('chat-header-slot').contains(parentLink)).toBe(true)
    await userEvent.click(parentLink)
    expect(selectSession).toHaveBeenCalledTimes(1)
    expect(selectSession).toHaveBeenCalledWith(parent)
  })

  it('a narrow header line truncates the breadcrumb and keeps the full text in its title', async () => {
    vi.mocked(fetchSessions).mockResolvedValue([
      session({ id: PARENT_ID, type: 'main' }),
      session({ id: CHILD_ID, type: 'delegate', parent_session_id: PARENT_ID }),
    ])
    setHelperChat()
    renderSlot()
    const label = await screen.findByTestId('feed-kind-label')
    await waitFor(() => {
      expect(label.textContent).toBe('Mia - Main Session / Helper')
    })
    // jsdom has no layout engine, so the GEOMETRY of the narrow header (no
    // overflow, the workspace strip keeping the line) is proved by the
    // Playwright measurement, not here. What jsdom can prove is the part a
    // user loses when the line is clipped: the full breadcrumb stays
    // reachable through the title.
    const line = label.querySelector('span')
    expect(line?.getAttribute('title')).toBe('Mia - Main Session / Helper')
    // The DOM keeps the whole breadcrumb — the browser, not the data, clips it.
    expect(label.textContent).toBe('Mia - Main Session / Helper')
  })

  it('the honest "agent names unavailable" warning is never clipped with the identity', async () => {
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    setMainChat()
    renderSlot()
    const label = await screen.findByTestId('feed-kind-label')
    await waitFor(() => {
      expect(label.textContent).toBe('mia - Main Session — agent names unavailable')
    })
    // The status region names the identity AND the warning.
    const [identityId, warningId] = (label.getAttribute('aria-labelledby') ?? '').split(' ')
    const identity = document.getElementById(identityId ?? '')
    const warning = document.getElementById(warningId ?? '')
    expect(identity).not.toBeNull()
    expect(warning).not.toBeNull()
    // The warning is its OWN element, outside the truncating identity — a
    // clipped identity can never take the warning with it.
    expect(identity?.contains(warning as Node)).toBe(false)
    // Its VISIBLE form is a compact glyph that fits the capped slot; its
    // WORDS are the region's own accessible name, and the glyph is a separate,
    // named and focusable trigger beside them.
    expect(warning?.textContent).toContain('agent names unavailable')
    const trigger = screen.getByTestId('feed-kind-warning')
    expect(trigger.querySelector('svg')).not.toBeNull()
    expect(trigger).toHaveAttribute('aria-label', 'Agent names unavailable')
    expect(trigger.tabIndex).toBe(0)
    // The trigger is a SIBLING of the labelled words, not a child of them: a
    // named child would repeat itself in the region's accessible name.
    expect(warning?.contains(trigger)).toBe(false)
    // The full text stays reachable: the identity's title carries the whole
    // line, warning included.
    expect(identity?.getAttribute('title')).toBe('mia - Main Session — agent names unavailable')
  })

  it('the warning and the "Add participant" control are separate, individually reachable controls', async () => {
    // The roster failed (the warning) while the workspace entry settled with
    // no failure (the control): both sit on the header line at once, and each
    // must be reachable on its own.
    setMainChat()
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster down'))
    renderSlot()
    const trigger = await screen.findByTestId('feed-kind-warning')
    const control = screen.getByRole('button', { name: 'Add participant' })
    expect(trigger).toBeVisible()
    expect(control).toBeVisible()
    // Two tab stops, in reading order: the warning first, then the control —
    // the keyboard never lands on one while the other is the target.
    expect(trigger.tabIndex).toBe(0)
    expect(control.tabIndex).toBe(0)
    expect(trigger.compareDocumentPosition(control) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // The slot keeps the two touch targets apart on a coarse pointer: the
    // warning's box is the touch minimum (44px) and the participant
    // control's own expanded hit region reaches 6px past its 28px box where
    // the published package's [data-ds-action] expander applies, so the
    // ordinary 4px gap would let the later sibling win the overlap. The
    // GEOMETRY is measured in the browser; here the rule is pinned.
    expect(screen.getByTestId('chat-header-slot').className).toContain('pointer-coarse:gap-[var(--space-2)]')
  })

  it('a ROOM page shows no chat chrome: no label, no Add participant', async () => {
    mockPathname = `/workspaces/${WORKSPACE_ID}/rooms/room-1`
    setMainChat()
    renderSlot()
    // The slot region exists (the group Participants button plugs in here
    // later) but carries nothing of the PREVIOUS main chat.
    expect(screen.getByTestId('chat-header-slot')).toBeInTheDocument()
    expect(screen.getByTestId('chat-header-slot').textContent).toBe('')
    expect(screen.queryByTestId('feed-kind-label')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull()
  })

  it('hides "Add participant" while the workspace entry is still being resolved', () => {
    // A workspace switch keeps the PREVIOUS workspace's session until the
    // new one resolves. The control must not offer an action that pairs the
    // new workspace with the old session.
    setMainChat()
    useSessionStore.setState({ resolvingSessionForWorkspace: { [WORKSPACE_ID]: true } })
    renderSlot()
    expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull()
  })

  it('hides "Add participant" after a failed workspace entry', () => {
    setMainChat()
    // ONLY the failed-entry gate is set: the resolution has finished, so
    // this case can fail if (and only if) the failed-attempt branch stops
    // hiding the control.
    useSessionStore.setState({
      resolvingSessionForWorkspace: {},
      workspaceEntry: {
        status: 'failed-attempt',
        committed: null,
        attempted: { sessionId: MAIN_ID, sendEnabled: false },
        acknowledged: false,
        retry: true,
        fellBack: false,
      },
    })
    renderSlot()
    expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull()
  })

  it('shows "Add participant" once the workspace entry has settled with no failure', () => {
    // The counterpart of the two gates above: with neither gate set, the
    // control is offered. Without this, a gate that hid the control
    // UNCONDITIONALLY would pass both hiding cases.
    setMainChat()
    useSessionStore.setState({ resolvingSessionForWorkspace: {}, workspaceEntry: null })
    renderSlot()
    expect(screen.getByRole('button', { name: 'Add participant' })).toBeVisible()
  })

  it('a chat address with a trailing slash shows the same controls as the plain one', async () => {
    // The router matches "/workspaces/<id>/chat/" to the same chat route, so
    // the slot must read the same route — a trailing slash must not hide the
    // chat's identity or its controls.
    mockPathname = `/workspaces/${WORKSPACE_ID}/chat/`
    setMainChat()
    renderSlot()
    expect(screen.getByRole('button', { name: 'Add participant' })).toBeVisible()
    expect(await screen.findByTestId('feed-kind-label')).toBeInTheDocument()
  })
})
