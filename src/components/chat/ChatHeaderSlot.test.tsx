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
 *     only, never Omnipus's chat, never a helper/task chat;
 *   - the old bar's replacement carries no border and no background;
 *   - a narrow header line truncates the breadcrumb and keeps the full text
 *     in its title;
 *   - the breadcrumb's parent is the link back to the parent chat.
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
  return render(<QueryClientProvider client={qc}><ChatHeaderSlot /></QueryClientProvider>)
}

beforeEach(() => {
  selectSession.mockReset()
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
    const line = label.querySelector('span')
    expect(line?.getAttribute('title')).toBe('Mia - Main Session / Helper')
    // Truncation, not overflow: the line clips and the slot can shrink.
    expect(line?.className).toMatch(/truncate/)
    expect(line?.className).toMatch(/max-w-\[32ch\]/)
    const slot = screen.getByTestId('chat-header-slot')
    expect(slot.className).toMatch(/shrink/)
    expect(slot.className).not.toMatch(/w-\[/)
  })
})
