/**
 * AgentPicker — worker-session lock (UAT defect 004).
 *
 * The defect: opening a chat session that belongs to a WORKER agent left the
 * picker naming somebody else. `chatAgents` (src/hooks/useChatAgents.ts)
 * deliberately excludes workers — they are background task runners, not chat
 * partners you pick out of a list — so the worker could never be found by the
 * trigger's own lookup, which either showed "Select agent" or (when the agent
 * list resolved before the session attach landed) let auto-select fill the
 * gap with the first ordinary chat agent.
 *
 * The fix is deliberately picker-local: the shared `chatAgents` filter is
 * UNCHANGED, so nothing new reaches the dropdown or the composer's "@"
 * mention menu; only the trigger widens its lookup to the unfiltered list,
 * and only when the miss was caused by a worker.
 *
 * Covers:
 *   - A worker session pre-selects THAT worker in the trigger
 *   - The trigger is disabled (locked) and carries no dropdown at all
 *   - `/agents` (agentSelectorOpen) cannot force a menu over a worker session
 *   - A normal session still auto-selects the first chat agent, still never a
 *     worker, and still renders a live dropdown
 *   - The "Task:" banner (attachedSessionType/attachedTaskTitle) survives
 *   - The shared list the "@" mention menu consumes still excludes workers
 *
 * Fixtures use `makeAgent` (src/test/factories.ts) with the real wire enum
 * value `type: 'Subagent'` — NOT the legacy config-only value `'worker'` that
 * the older fixtures in AgentPicker.test.tsx still carry (gap 12: the gateway
 * never emits "worker" on the wire).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, renderHook } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { makeAgent } from '@/test/factories'
import * as api from '@/lib/api'

// ── API mocks ─────────────────────────────────────────────────────────────────
//
// Two ordinary chat agents plus one worker. `mia` sorts FIRST so the
// normal-session auto-select assertion below has a deterministic answer, and
// so a worker-session assertion naming `builder` cannot be satisfied by
// accident.

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
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

// Static imports after the mocks are in place
import { AgentPicker } from './AgentPicker'
import { useChatAgents } from '@/hooks/useChatAgents'

const MIA = makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', description: 'Assistant' })
const JIM = makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle', description: 'Orchestrator' })
const BUILDER = makeAgent({
  id: 'builder',
  name: 'Builder Worker',
  type: 'Subagent',
  status: 'active',
  description: 'Labour agent',
})

// ── Render helpers ────────────────────────────────────────────────────────────

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

// ── Store reset ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchAgents).mockResolvedValue([MIA, JIM, BUILDER])
  vi.mocked(api.fetchWorkspaces).mockResolvedValue([])
  act(() => {
    useSessionStore.setState({
      activeAgentId: null,
      activeSessionId: null,
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
      // AGENT PRECEDENCE RULE (src/store/session.ts): a leaked 'user' source
      // would make the session-derived agent hints below be refused.
      agentSelectionSource: 'auto',
      agentSelectionWorkspaceId: null,
    })
    useUiStore.setState({ agentSelectorOpen: false })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

/**
 * Puts the store in the state every "open a session that belongs to this
 * agent" path leaves it in: the session id is active and the session's own
 * agent (`active_agent_id ?? agent_id`) has been adopted as the active agent.
 * That is what useSelectSession/attachToSession, the /sessions/{id} deep
 * link, and workspace re-entry all produce.
 */
function openSessionForAgent(sessionId: string, agentId: string) {
  act(() => {
    useSessionStore.setState({ activeSessionId: sessionId, activeAgentId: agentId })
  })
}

// ── Worker session: pre-selection ─────────────────────────────────────────────

describe('AgentPicker — worker session pre-selects the worker', () => {
  it('names the worker the session belongs to, not the first chat agent', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Builder Worker')
    })
    // The pre-fix rendering was "Select agent" (the worker is absent from
    // `chatAgents`), and the failure mode the defect report described was the
    // first ordinary chat agent showing up instead. Neither may appear.
    expect(screen.queryByText(/select agent/i)).not.toBeInTheDocument()
    expect(screen.getByTestId('agent-picker-trigger')).not.toHaveTextContent('Mia')
  })

  it('does not overwrite the worker in the session store with a chat agent', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Builder Worker')
    })
    // Give the auto-select effect a tick to (not) fire.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(useSessionStore.getState().activeAgentId).toBe('builder')
  })

  it('announces the lock in its accessible name', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger').getAttribute('aria-label')).toBe(
        'Agent locked to Builder Worker for this worker session',
      )
    })
  })

  // The worker must be reported even when no ordinary agent is chat-eligible:
  // the all-draft branch ("All agents are in draft status…") would be both
  // wrong and unactionable for a session you are already talking to a worker
  // in, which is why the lock branch is evaluated first.
  it('reports the worker even when every ordinary agent is in draft status', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValue([
      makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'draft' }),
      BUILDER,
    ])
    openSessionForAgent('sess_delegate_1', 'builder')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Builder Worker')
    })
    expect(
      screen.queryByText('All agents are in draft status. Configure an agent to start chatting.'),
    ).not.toBeInTheDocument()
  })
})

// ── Worker session: the selector is locked ────────────────────────────────────

describe('AgentPicker — worker session locks the selector', () => {
  it('disables the trigger so the agent cannot be changed', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toBeDisabled()
    })
  })

  it('marks the trigger as locked for styling/e2e (data-agent-locked="worker")', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveAttribute(
        'data-agent-locked',
        'worker',
      )
    })
  })

  // `agentSelectorOpen` is controlled from OUTSIDE this component (the
  // `/agents` slash command sets it unconditionally — useSlashMenu.ts). A
  // disabled TRIGGER alone would not have been enough: the menu's open state
  // does not go through the trigger, so a latched flag could still have put a
  // live agent list over a locked session. The lock branch renders no
  // DropdownMenu at all, and resets the flag.
  it('cannot be opened by the /agents flag — no agent list appears and the flag is reset', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    act(() => {
      useUiStore.setState({ agentSelectorOpen: true })
    })
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Builder Worker')
    })
    // No dropdown item for any other agent — the only 'Builder Worker' node
    // is the locked trigger itself, and the ordinary agents are nowhere.
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    expect(screen.queryByText('Mia')).not.toBeInTheDocument()
    expect(screen.queryByText('Jim')).not.toBeInTheDocument()
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })
})

// ── Normal sessions are untouched ─────────────────────────────────────────────

describe('AgentPicker — normal sessions are unchanged by the worker lock', () => {
  it('still auto-selects the first chat agent when none is active, never the worker', async () => {
    // Worker sorted FIRST in the API response — auto-select must still skip it.
    vi.mocked(api.fetchAgents).mockResolvedValue([BUILDER, MIA, JIM])
    renderPicker()

    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    expect(useSessionStore.getState().activeAgentId).not.toBe('builder')
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()
  })

  it('leaves the trigger enabled and unlocked for an ordinary agent session', async () => {
    openSessionForAgent('sess_chat_1', 'mia')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Mia')
    })
    const trigger = screen.getByTestId('agent-picker-trigger')
    expect(trigger).not.toBeDisabled()
    expect(trigger).not.toHaveAttribute('data-agent-locked')
    expect(trigger.getAttribute('aria-label')).toBe('Select agent (current: Mia)')
  })

  it('still opens a dropdown listing the chat agents — and still no worker in it', async () => {
    openSessionForAgent('sess_chat_1', 'mia')
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Mia')
    })
    act(() => {
      useUiStore.getState().setAgentSelectorOpen(true)
    })
    await vi.waitFor(() => {
      // Trigger label + portal item = 2+ 'Mia' occurrences once open.
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(1)
    })
    expect(screen.getByText('Jim')).toBeInTheDocument()
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()
  })

  // Regression guard for the session-preservation + attached-context fixes the
  // auto-select effect documents: the worker lock must not have disturbed
  // either. setActiveSession unconditionally nulls attachedSessionType/
  // attachedTaskTitle, so auto-select captures and restores them — without
  // that, the "Task:" banner vanishes off a deep-linked task session.
  it('auto-select still preserves the deep-linked session and its Task banner', async () => {
    act(() => {
      useSessionStore.setState({
        activeAgentId: null,
        activeSessionId: 'sess_task_link',
        attachedSessionType: 'task',
        attachedTaskTitle: 'Migrate the database',
      })
    })
    renderPicker()

    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeAgentId).toBe('mia')
    })
    expect(useSessionStore.getState().activeSessionId).toBe('sess_task_link')
    expect(useSessionStore.getState().attachedSessionType).toBe('task')
    expect(useSessionStore.getState().attachedTaskTitle).toBe('Migrate the database')
  })

  // The Task banner must survive the WORKER path too — but for a different
  // reason: nothing in the lock branch writes to the session store at all, so
  // there is no setActiveSession call to null the pair in the first place.
  it('a worker session leaves the Task banner alone as well', async () => {
    act(() => {
      useSessionStore.setState({
        activeSessionId: 'sess_delegate_task',
        activeAgentId: 'builder',
        attachedSessionType: 'task',
        attachedTaskTitle: 'Migrate the database',
      })
    })
    renderPicker()

    await vi.waitFor(() => {
      expect(screen.getByTestId('agent-picker-trigger')).toHaveTextContent('Builder Worker')
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(useSessionStore.getState().attachedSessionType).toBe('task')
    expect(useSessionStore.getState().attachedTaskTitle).toBe('Migrate the database')
    expect(useSessionStore.getState().activeSessionId).toBe('sess_delegate_task')
  })
})

// ── The shared list the "@" mention menu reads is untouched ───────────────────

describe('useChatAgents — the "@" mention menu source still excludes workers', () => {
  // AgentPicker and useSlashMenu's "@" mention menu consume the SAME
  // `chatAgents` list (src/hooks/useChatAgents.ts). Widening the picker's
  // trigger lookup must not have widened that list, or workers would start
  // being offered as mention targets. Asserted against the hook directly —
  // the list is what the mention menu maps over — while a worker is the
  // ACTIVE agent, which is the exact state the lock introduces. The
  // end-to-end mention-menu rendering is covered by
  // src/hooks/useSlashMenu.test.ts ('a bare "@" opens the menu listing every
  // scoped chat agent (worker excluded)').
  it('keeps the worker out of chatAgents even while that worker is the active agent', async () => {
    openSessionForAgent('sess_delegate_1', 'builder')
    const client = makeClient()
    const { result } = renderHook(() => useChatAgents(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    await vi.waitFor(() => {
      expect(result.current.agents.length).toBe(3)
    })
    expect(result.current.chatAgents.map((a) => a.id)).toEqual(['mia', 'jim'])
    expect(result.current.chatAgents.some((a) => a.id === 'builder')).toBe(false)
  })
})
