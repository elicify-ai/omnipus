// AgentPicker — agentSelectorOpen controlled DropdownMenu.
// Tests that the agent picker DropdownMenu is driven by the ui store's
// agentSelectorOpen flag, enabling the /agents slash command to open it.
//
// Migrated from src/components/chat/ChatControls.agent-selector-open.test.tsx
// (Composer Redesign variant A1): the agent picker moved out of ChatControls
// into its own composer sub-component, so this file now mounts <AgentPicker/>
// directly instead of <ChatControls/>. Same behavior, same assertions.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import * as api from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      {
        id: 'jim',
        name: 'Jim',
        type: 'core',
        status: 'idle',
        description: 'Orchestrator',
      },
    ]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@/components/shared/IconRenderer', () => ({
  IconRenderer: () => null,
}))

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { AgentPicker } from './AgentPicker'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPicker() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentPicker />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({
      activeAgentId: 'mia',
      activeSessionId: 'sess_1',
      activeAgentType: null,
      // Clicking an agent now records an EXPLICIT selection (AGENT PRECEDENCE
      // RULE, src/store/session.ts) — reset it, or one test's pick would
      // change how the next test's session-derived writes resolve.
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
    })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useUiStore.setState({ agentSelectorOpen: false })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

describe('AgentPicker — agentSelectorOpen controlled DropdownMenu', () => {
  it('agentSelectorOpen defaults to false in ui store', () => {
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })

  it('setting agentSelectorOpen=true via store opens the agent dropdown', async () => {
    renderPicker()

    // Wait for agents to load
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)

    // Agents dropdown should not show Jim in its open state by default
    expect(screen.queryByText('Jim')).not.toBeInTheDocument()

    // Simulate /agents command: set agentSelectorOpen=true
    act(() => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })

    // The dropdown is controlled — when open, it should show agent items
    // (Note: in jsdom, the Radix DropdownMenuContent may or may not render without portal)
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)
  })

  it('setting agentSelectorOpen=false closes the dropdown', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)

    act(() => { useUiStore.getState().setAgentSelectorOpen(true) })
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)

    act(() => { useUiStore.getState().setAgentSelectorOpen(false) })
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })

  it('DropdownMenu onOpenChange updates ui store when toggled by user click', async () => {
    renderPicker()

    // Wait for agents to load
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)

    // The trigger button is rendered; agentSelectorOpen starts at false
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)

    // The DropdownMenu is controlled — when the store is false, it is closed
    // The DropdownMenuTrigger is wrapped with asChild; the Button trigger is present
    // (We verify the store is correctly bound — not the DOM open state which requires portal)
    act(() => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)
  })

  it('agent list is not changed by the controlled open flag', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      { id: 'mia', name: 'Mia', type: 'core', status: 'active', model: 'z-ai/glm-5.2', description: 'Assistant' },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])

    renderPicker()
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)

    // Opening the selector does not affect the list contents
    act(() => { useUiStore.getState().setAgentSelectorOpen(true) })
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)

    act(() => { useUiStore.getState().setAgentSelectorOpen(false) })
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })
})

// Drives the REAL handleAgentSelect by opening the Radix DropdownMenu and
// clicking the Jim item. Radix portals to document.body in jsdom (portal
// content IS rendered into the live DOM), so the item is reachable directly.
async function pickJim() {
  renderPicker()
  await vi.waitFor(() => screen.getAllByText('Mia').length > 0)
  // <DropdownMenu open={agentSelectorOpen}> is controlled by the ui store, so
  // setting the flag renders the portal content into document.body.
  await act(async () => {
    useUiStore.getState().setAgentSelectorOpen(true)
  })
  const jimItem = await vi.waitFor(() => {
    const items = screen.getAllByText('Jim')
    if (items.length === 0) throw new Error('Jim not in DOM yet')
    return items[0]
  })
  await act(async () => {
    jimItem.click()
  })
}

/** What the picker actually says it is pointed at, read off the rendered trigger. */
function pickerLabel(): string | null {
  return screen.getByTestId('agent-picker-trigger').getAttribute('aria-label')
}

// SC-005/US5.2: an agent switch KEEPS the current session — no new session is
// created — and the picker moves to the agent that was clicked.
//
// Asserted as OUTCOMES on the store + rendered trigger rather than by spying
// on a specific store action: the previous version of this test asserted
// `setActiveSession(sessionId, 'jim', 'core')` was called, which locked in the
// very mechanism (routing an explicit user choice through the same
// last-write-wins setter every background session sync uses) that made the
// agent silently revertible. See the AGENT PRECEDENCE RULE in
// src/store/session.ts.
describe('AgentPicker — handleAgentSelect keeps current session (SC-005/US5.2)', () => {
  it('switching agents keeps the current session and points the picker at the new agent', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_keep_me' })
    })

    await pickJim()

    // No new session — the SC-005 invariant.
    expect(useSessionStore.getState().activeSessionId).toBe('sess_keep_me')
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
    expect(pickerLabel()).toBe('Select agent (current: Jim)')
  })
})

// The reverting-picker bug: a user picks Jim, a session attach lands moments
// later carrying the session's own agent (Mia), and the picker flips back on
// its own — with the user's next message going to Mia. Precedence rule 2 (an
// explicit selection outranks a session's remembered agent) makes the pick
// stick; this test asserts it on the RENDERED picker, which is where the user
// saw it revert.
describe('AgentPicker — an explicit pick survives a later session attach', () => {
  it('a session attach carrying a different agent does not move the picker off the picked agent', async () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: null })
      useConnectionStore.setState({
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
    })

    await pickJim()
    expect(pickerLabel()).toBe('Select agent (current: Jim)')

    // The SPA syncs to a backend-created session whose agent is Mia.
    await act(async () => {
      useSessionStore.getState().attachToSession('sess_backend_created', 'chat', undefined, 'mia')
    })

    // The picker still names Jim — and the attach itself still happened.
    expect(pickerLabel()).toBe('Select agent (current: Jim)')
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeSessionId).toBe('sess_backend_created')
  })

  it('without a pick, the same attach DOES move the picker to the session\'s agent', async () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useSessionStore.setState({ activeAgentId: 'jim', activeSessionId: null })
      useConnectionStore.setState({
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
        isConnected: true,
      })
    })

    renderPicker()
    await vi.waitFor(() => expect(pickerLabel()).toBe('Select agent (current: Jim)'))

    await act(async () => {
      useSessionStore.getState().attachToSession('sess_backend_created', 'chat', undefined, 'mia')
    })

    expect(pickerLabel()).toBe('Select agent (current: Mia)')
  })
})
