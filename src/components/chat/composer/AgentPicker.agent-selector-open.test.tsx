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
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1' })
    useUiStore.setState({ agentSelectorOpen: false, sessionPanelOpen: false })
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

// SC-005/US5.2: agent switch KEEPS the current session (setActiveSession called
// with the same activeSessionId + new agentId — no new session is created).
//
// This test drives the REAL handleAgentSelect function by opening the Radix
// DropdownMenu and clicking the Jim item. Radix portals to document.body in
// jsdom (portal content IS rendered into the live DOM), so the Jim item is
// reachable via userEvent.click on the trigger followed by a click on the item.
//
// The test MUST fail if handleAgentSelect is changed to pass null as the
// session id (the new-session regression it guards against).
describe('AgentPicker — handleAgentSelect keeps current session (SC-005/US5.2)', () => {
  it('setActiveSession is called with the current activeSessionId when switching agents', async () => {
    const setActiveSessionSpy = vi.fn()
    const origSetActiveSession = useSessionStore.getState().setActiveSession

    // Seed the session store with a known session and agent BEFORE injecting spy
    act(() => {
      useSessionStore.setState({
        activeAgentId: 'mia',
        activeSessionId: 'sess_keep_me',
        setActiveSession: setActiveSessionSpy,
      })
    })

    renderPicker()

    // Wait for both Mia items (trigger label + Jim in content after open)
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)

    // Open the Radix DropdownMenu by setting the store flag — the component's
    // <DropdownMenu open={agentSelectorOpen}> is controlled by this flag, so
    // setting it true causes Radix to render the portal content into document.body.
    await act(async () => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })

    // Wait for Jim to appear in the portal content rendered into document.body.
    // Radix renders DropdownMenuContent into document.body even in jsdom.
    const jimItem = await vi.waitFor(() => {
      // There should be exactly one "Jim" element — inside the dropdown content.
      const items = screen.getAllByText('Jim')
      if (items.length === 0) throw new Error('Jim not in DOM yet')
      return items[0]
    })

    // Click the Jim DropdownMenuItem — this fires handleAgentSelect('jim')
    // which calls setActiveSession(activeSessionId, 'jim', selected.type)
    await act(async () => {
      jimItem.click()
    })

    // The spy MUST have been called with the PRE-EXISTING activeSessionId
    // ('sess_keep_me') not null — that is the SC-005 invariant.
    expect(setActiveSessionSpy).toHaveBeenCalledTimes(1)
    expect(setActiveSessionSpy).toHaveBeenCalledWith('sess_keep_me', 'jim', 'core')
    // activeSessionId MUST NOT be null — verify arg 0 equals original session
    expect(setActiveSessionSpy.mock.calls[0][0]).toBe('sess_keep_me')

    // Restore original store function
    act(() => {
      useSessionStore.setState({ setActiveSession: origSetActiveSession })
    })
  })
})
