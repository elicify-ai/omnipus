// RED — spec test row 138; US-17 AS-13; FR-207/208; BDD-17.24/25.
// Public props from team-lead (2026-10-14); all behavioural oracles from spec.
// REAL component/queries/API/router/stores; only fetch is doubled.
import type { ComponentType } from 'react'
import { act } from 'react'
import { vi } from 'vitest'

// Stage 1 hides the main-chat entry point; retain its future-enabled oracle.
vi.mock('@/lib/featureSwitches', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/featureSwitches')>(),
  ROOMS_ENABLED: true,
}))

import { ChatScreen } from './ChatScreen'
import { ChatHeaderSlot } from './ChatHeaderSlot'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Room } from '@/lib/api/generated/openapi-types'
import { wireHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import {
  json, MAIN_ID, mountAt, Network, requiredExport, resetRealStores, room,
  ROOM_ID, stopHarness, TestSocket, WS, workspace,
} from '../../../tests/l1-room-ui/harness'

// not-wire-format: confirmed React props, not a parallel wire type.
type Target = { kind: 'main'; workspaceId: string; agentId: string; sessionId: string }
  | { kind: 'room'; room: Room }

const main: Target = { kind: 'main', workspaceId: WS, agentId: 'mia', sessionId: MAIN_ID }
let net: Network
beforeEach(() => { resetRealStores(); net = new Network() })
afterEach(() => stopHarness())

async function show(target: Target = main) {
  const AddParticipant = await requiredExport<ComponentType<{ target: Target }>>('/src/components/chat/AddParticipant.tsx', 'AddParticipant', 'FR-207/208; BDD-17.24/25; test row 138')
  return mountAt(<AddParticipant target={target} />, `/workspaces/${WS}/chat`)
}

async function picker() {
  await userEvent.setup().click(screen.getByRole('button', { name: 'Add participant' }))
  return screen.findByRole('dialog')
}

function candidateNames(view: HTMLElement) {
  return within(view).getAllByRole('option').map((option) => option.textContent)
}

describe('Add participant — agents only, one workspace, no main-chat conversion', () => {
  it('[FR-207; BDD-17.24/25] a main-chat picker offers only Jim and Researcher2, excluding owner/workers/system/Omnipus/other workspace', async () => {
    await show()
    const view = await picker()
    await within(view).findByRole('option', { name: /Jim/ })
    expect(candidateNames(view)).toHaveLength(2)
    expect(within(view).getByRole('option', { name: /Jim/ })).toBeVisible()
    expect(within(view).getByRole('option', { name: /Researcher2/ })).toBeVisible()
    for (const name of ['Mia', 'Ray', 'GP Worker', 'External Worker', 'Judge', 'Omnipus', 'Anna']) {
      expect(within(view).queryByRole('option', { name: new RegExp(name) })).toBeNull()
    }
    expect(net.writes()).toEqual([])
  })

  it('[FR-207; BDD-17.24; K24] picking Jim creates an ad-hoc owner+Jim room, preserves main state and opens the room route', async () => {
    useChatStore.getState().appendMessage({ id: 'original-main-message', session_id: MAIN_ID, role: 'user', content: 'Private one-to-one text', timestamp: '2026-10-14T09:00:00Z', status: 'done' })
    const mainBefore = structuredClone(useChatStore.getState().sessionsById[MAIN_ID])
    const created = room({ id: 'new-adhoc', kind: 'adhoc', members: ['mia', 'jim'], origin_session_id: MAIN_ID })
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json(created, 201))
    const mounted = await show()
    const view = await picker()
    await userEvent.setup().click(await within(view).findByRole('option', { name: /Jim/ }))
    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes().map(({ method, path, body }) => ({ method, path, body }))).toEqual([{
      method: 'POST', path: '/api/v1/workspaces/sales/rooms',
      body: { kind: 'adhoc', agent_ids: ['mia', 'jim'], origin_session_id: MAIN_ID },
    }])
    await waitFor(() => expect(mounted.router.state.location.pathname).toBe('/workspaces/sales/rooms/new-adhoc'))
    expect(useChatStore.getState().sessionsById[MAIN_ID]).toEqual(mainBefore)
    expect(net.writes().filter((write) => write.path.includes('/sessions/'))).toEqual([])
  })

  it.each(['squad', 'adhoc'] as const)('[FR-208; BDD-17.25] in a %s, picks only a current nonmember and adds it without creating another room', async (kind) => {
    const current = room({ kind, members: ['mia', 'jim'] })
    net.respond('PATCH', `/api/v1/rooms/${ROOM_ID}/members`, () => json(room()))
    await show({ kind: 'room', room: current })
    const view = await picker()
    await within(view).findByRole('option', { name: /Researcher2/ })
    expect(candidateNames(view)).toHaveLength(1)
    for (const name of ['Mia', 'Jim', 'Ray', 'Omnipus', 'GP Worker', 'External Worker', 'Judge']) {
      expect(within(view).queryByRole('option', { name: new RegExp(name) })).toBeNull()
    }
    await userEvent.setup().click(within(view).getByRole('option', { name: /Researcher2/ }))
    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes().map(({ method, path, body }) => ({ method, path, body }))).toEqual([{
      method: 'PATCH', path: '/api/v1/rooms/launch/members', body: { action: 'add', agent_ids: ['researcher2'] },
    }])
  })

  it('[FR-208; BDD-17.25] the team-room control is disabled with the exact workspace-members explanation', async () => {
    await show({ kind: 'room', room: room({ kind: 'team', name: 'Team room' }) })
    const trigger = screen.getByRole('button', { name: 'Add participant' })
    expect(trigger).toBeDisabled()
    const text = "Team room members are the workspace's agents — add the agent to the workspace"
    // Explanation may be persistent copy or an accessible description/tooltip.
    const accessibleDescription = trigger.getAttribute('aria-description')
      ?? trigger.getAttribute('title')
      ?? trigger.getAttribute('aria-describedby')?.split(' ').map((id) => document.getElementById(id)?.textContent).join(' ')
      ?? screen.queryByText(text, { exact: true })?.textContent
    if (accessibleDescription !== undefined && accessibleDescription !== null) {
      expect(accessibleDescription).toBe(text)
    } else {
      await userEvent.setup().hover(trigger)
      expect(await screen.findByText(text, { exact: true })).toBeVisible()
    }
    await userEvent.setup().click(trigger)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(net.writes()).toEqual([])
  })

  it('[FR-207/208; BDD-17.24/25] the chat body no longer carries its own Add participant row — the control lives in the header slot', async () => {
    net.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wireHistoryPage(MAIN_ID, [], 0)))
    net.respond('GET', '/api/v1/skills', () => json([]))
    net.respond('GET', '/api/v1/commands', () => json([]))
    net.respond('GET', '/api/v1/providers', () => json([]))
    TestSocket.instances = []
    vi.stubGlobal('WebSocket', TestSocket)
    useSessionStore.setState({ attachedSessionType: 'main' })
    await mountAt(<OmnipusRuntimeProvider><ChatScreen /></OmnipusRuntimeProvider>, `/workspaces/${WS}/chat`)
    // #1449 D13: the control moved into the workspace header line's
    // right-end slot; the chat body renders no row of its own.
    expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull()
  })

  it('[#1449 D13] the header slot icon opens the picker, and picking an agent starts the group', async () => {
    // The header slot is the REAL entry point now: an icon-only ghost button
    // in the workspace header line. Its own click path must open the same
    // picker the labelled button opened.
    useSessionStore.setState({ attachedSessionType: 'main' })
    net.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wireHistoryPage(MAIN_ID, [], 0)))
    net.respond('GET', '/api/v1/skills', () => json([]))
    net.respond('GET', '/api/v1/commands', () => json([]))
    net.respond('GET', '/api/v1/providers', () => json([]))
    const created = room({ id: 'new-adhoc', kind: 'adhoc', members: ['mia', 'jim'], origin_session_id: MAIN_ID })
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => json(created, 201))

    const mounted = await mountAt(<ChatHeaderSlot workspaceId={WS} />, `/workspaces/${WS}/chat`)
    const control = await screen.findByRole('button', { name: 'Add participant' })
    expect(control.textContent, 'the header control is icon-only').toBe('')
    await userEvent.setup().click(control)

    const view = await screen.findByRole('dialog')
    await within(view).findByRole('option', { name: /Jim/ })
    expect(candidateNames(view)).toEqual(['Jim', 'Researcher2'])
    await userEvent.setup().click(within(view).getByRole('option', { name: /Jim/ }))

    await waitFor(() => expect(net.writes()).toHaveLength(1))
    expect(net.writes().map(({ method, path, body }) => ({ method, path, body }))).toEqual([{
      method: 'POST', path: '/api/v1/workspaces/sales/rooms',
      body: { kind: 'adhoc', agent_ids: ['mia', 'jim'], origin_session_id: MAIN_ID },
    }])
    await waitFor(() => expect(mounted.router.state.location.pathname).toBe('/workspaces/sales/rooms/new-adhoc'))
    expect(net.unexpected).toEqual([])
  })

  it('[FR-207/208; BDD-17.24/25] the real header slot shows Add participant in a main chat and removes it for Omnipus', async () => {
    await requiredExport('/src/components/chat/AddParticipant.tsx', 'AddParticipant', 'FR-207/208; row 138 header reachability')
    net.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(wireHistoryPage(MAIN_ID, [], 0)))
    net.respond('GET', '/api/v1/sessions/main-omnipus/messages', () => json(wireHistoryPage('main-omnipus', [], 0)))
    net.respond('GET', '/api/v1/skills', () => json([]))
    net.respond('GET', '/api/v1/commands', () => json([]))
    net.respond('GET', '/api/v1/providers', () => json([]))
    TestSocket.instances = []
    vi.stubGlobal('WebSocket', TestSocket)
    useSessionStore.setState({ attachedSessionType: 'main' })
    // #1449 D13: the app renders the chat body AND the header slot; the
    // control is reachable from the header slot (the workspace container
    // mounts it in the header line), not from the chat body.
    await mountAt(
      <OmnipusRuntimeProvider>
        <ChatHeaderSlot workspaceId={WS} />
        <ChatScreen />
      </OmnipusRuntimeProvider>,
      `/workspaces/${WS}/chat`,
    )
    expect(await screen.findByRole('button', { name: 'Add participant' })).toBeVisible()
    act(() => useSessionStore.setState({ activeAgentId: 'omnipus', activeSessionId: 'main-omnipus', attachedSessionType: 'main' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Add participant' })).toBeNull())
    expect(net.writes()).toEqual([])
    expect(net.unexpected).toEqual([])
  })

  it('[§6.3; FR-207] a workspace with no other eligible agents says No other agents in this workspace', async () => {
    net.workspaces = [workspace({ core_team: ['mia', 'gp', 'judge'], member_configs: { mia: { main_session_id: MAIN_ID } } })]
    await show()
    const view = await picker()
    expect(await within(view).findByText('No other agents in this workspace', { exact: true })).toBeVisible()
    expect(within(view).queryByRole('option')).toBeNull()
    expect(net.writes()).toEqual([])
  })

  it('[§6.3] a failed agents read shows Retry, never a successful empty picker', async () => {
    net.respond('GET', '/api/v1/agents', () => json({ error: 'roster unavailable' }, 500))
    await show()
    const view = await picker()
    expect(await within(view).findByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(within(view).queryByText('No other agents in this workspace', { exact: true })).toBeNull()
    expect(within(view).queryByRole('option')).toBeNull()
    expect(net.writes()).toEqual([])
  })

  it('[§6.3; FR-207] an uncertain create is not confirmed, offers Retry, and does not navigate as success', async () => {
    net.respond('POST', `/api/v1/workspaces/${WS}/rooms`, () => { throw new TypeError('network disconnected') })
    const mounted = await show()
    const view = await picker()
    await userEvent.setup().click(await within(view).findByRole('option', { name: /Jim/ }))
    expect(await screen.findByText(/not confirmed/i)).toBeVisible()
    expect(screen.getByRole('button', { name: /^retry$/i })).toBeEnabled()
    expect(mounted.router.state.location.pathname).toBe('/workspaces/sales/chat')
    expect(net.writes()).toHaveLength(1)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  })
})
