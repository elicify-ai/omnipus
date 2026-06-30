import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SessionPanel } from '../chat/SessionPanel'
import { useUiStore } from '@/store/ui'
import { useSessionStore } from '@/store/session'

// test_session_panel_component (test #20) — Session history panel
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session panel shows history grouped by agent
//             wave5a-wire-ui-spec.md — US-5 AC2: switching sessions restores context

// Note: SessionPanel lives at src/components/chat/SessionPanel.tsx (not layout/)

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'general-assistant', name: 'General Assistant', type: 'core', status: 'active', description: 'General purpose' },
      { id: 'researcher', name: 'Researcher', type: 'core', status: 'idle', description: 'Research agent' },
    ]),
    fetchSessions: vi.fn().mockResolvedValue([
      { id: 'sess_1', agent_id: 'general-assistant', title: 'First session', created_at: '2026-03-29T09:00:00Z', updated_at: '2026-03-29T10:00:00Z', message_count: 5 },
      { id: 'sess_2', agent_id: 'general-assistant', title: 'Second session', created_at: '2026-03-29T08:00:00Z', updated_at: '2026-03-29T09:00:00Z', message_count: 3 },
      { id: 'sess_3', agent_id: 'researcher', title: 'Research session', created_at: '2026-03-29T07:00:00Z', updated_at: '2026-03-29T08:00:00Z', message_count: 10 },
    ]),
    createSession: vi.fn(),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPanel() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SessionPanel />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({ sessionPanelOpen: true, createAgentModalOpen: false })
    useSessionStore.setState({
      activeAgentId: 'general-assistant',
      activeSessionId: 'sess_1',
    })
  })
})

describe('SessionPanel — rendering (test #20)', () => {
  it('renders "Sessions" header when panel is open', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Session panel opens with agent groups
    renderPanel()
    await screen.findByText('Sessions')
    expect(screen.getByText('Sessions')).toBeInTheDocument()
  })

  it('groups sessions under their first-agent sub-group headers', async () => {
    // Traces to: wave5a-wire-ui-spec.md — AC1: sessions grouped by agent.
    // Sessions are nested workspace -> first-agent: each agent that opened a
    // conversation gets a sub-group header (its visible name), and all its
    // sessions render beneath it.
    renderPanel()
    await screen.findByText('First session')
    // Agent sub-group headers are now visible text nodes.
    expect(screen.getByText('General Assistant')).toBeInTheDocument()
    expect(screen.getByText('Researcher')).toBeInTheDocument()
    // Sessions from both agents are shown.
    expect(screen.getByText('Research session')).toBeInTheDocument()
  })

  it('shows session titles within the agent group', async () => {
    // Traces to: wave5a-wire-ui-spec.md — AC2: session list shows titles
    renderPanel()
    await screen.findByText('First session')
    expect(screen.getByText('Second session')).toBeInTheDocument()
  })

  it('does not render panel when sessionPanelOpen is false', () => {
    // Traces to: wave5a-wire-ui-spec.md — panel hidden when closed
    act(() => { useUiStore.setState({ sessionPanelOpen: false }) })
    renderPanel()
    expect(screen.queryByText('Sessions')).toBeNull()
  })
})

describe('SessionPanel — session switching (test #20)', () => {
  it('calls setActiveSession when a session is clicked', async () => {
    // Traces to: wave5a-wire-ui-spec.md — US-5 AC2: switching sessions
    renderPanel()
    await screen.findByText('Second session')
    fireEvent.click(screen.getByText('Second session'))
    expect(useSessionStore.getState().activeSessionId).toBe('sess_2')
    // Panel closes after selection
    expect(useUiStore.getState().sessionPanelOpen).toBe(false)
  })
})
