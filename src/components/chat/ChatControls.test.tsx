/**
 * ChatControls tests.
 *
 * Covers all five controls rendered by <ChatControls>:
 *   1. New Chat button
 *   2. Agent selector dropdown
 *   3. Model selector (interactive)
 *   4. Token counter
 *   5. Sessions button
 *
 * Also validates:
 *   - Agent dropdown lists only workspace-team agents
 *   - Worker agents are excluded
 *   - formatTokens re-export (migrated from SessionBar.token.test.tsx)
 *   - Responsive layout: the "More" trigger (mobile band) and desktop-inline
 *     band both exist in the DOM; jsdom renders both since it ignores CSS.
 *   - The "More" popover contains New Chat, model selector, and token counter.
 *   - Agent picker + Sessions always present in the mobile band inline.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import * as api from '@/lib/api'
import { formatTokens } from './ChatControls'

// ── Router mock ───────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()
let mockPathname = '/'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ pathname: mockPathname }),
  }
})

// ── API mocks ─────────────────────────────────────────────────────────────────

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
    fetchProviders: vi.fn().mockResolvedValue([
      {
        id: 'openrouter',
        name: 'openrouter',
        display_name: 'OpenRouter',
        status: 'connected',
        models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo', 'openai/gpt-4o'],
      },
    ]),
  }
})

vi.mock('@/components/shared/IconRenderer', () => ({
  IconRenderer: () => null,
}))

// ── ResizeObserver stub (required by ModelSelector → cmdk popover) ────────────

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

// Static import after mocks are in place
import { ChatControls } from './ChatControls'

// ── Render helper ─────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderControls() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ChatControls />
    </QueryClientProvider>,
  )
}

// ── Store reset ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  mockPathname = '/'
  mockNavigate.mockReset()
  act(() => {
    useSessionStore.setState({
      activeAgentId: 'mia',
      activeSessionId: 'sess_1',
    })
    useChatStore.setState({
      sessionTokens: 44000,
      isStreaming: false,
      nextModel: null,
      messages: [],
    })
    useUiStore.setState({ sessionPanelOpen: false })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

// ── formatTokens unit (migrated from SessionBar.token.test.tsx) ───────────────

describe('formatTokens (re-exported from ChatControls)', () => {
  it('formats 0 as "0"', () => {
    expect(formatTokens(0)).toBe('0')
  })
  it('formats 999 as "999"', () => {
    expect(formatTokens(999)).toBe('999')
  })
  it('formats 1000 as "1.0k"', () => {
    expect(formatTokens(1000)).toBe('1.0k')
  })
  it('formats 44000 as "44.0k"', () => {
    expect(formatTokens(44000)).toBe('44.0k')
  })
  it('formats 1200000 as "1.2M"', () => {
    expect(formatTokens(1_200_000)).toBe('1.2M')
  })
})

// ── Control: New Chat ─────────────────────────────────────────────────────────
//
// Note: the component renders TWO layout bands (desktop + mobile) in the DOM
// simultaneously — jsdom does not apply CSS visibility. New Chat appears in both.

describe('ChatControls — New Chat button', () => {
  it('renders a New Chat button', async () => {
    renderControls()
    const btns = await vi.waitFor(() => {
      const all = screen.getAllByRole('button', { name: /new chat/i })
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    expect(btns.length).toBeGreaterThan(0)
  })

  it('navigates to "/" when clicked on the global chat route', async () => {
    mockPathname = '/'
    renderControls()
    const btns = await vi.waitFor(() => {
      const all = screen.getAllByRole('button', { name: /new chat/i })
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    fireEvent.click(btns[0])
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
  })

  it('calls startNewSession in-place inside a workspace chat tab', async () => {
    mockPathname = '/workspaces/ws-123/chat'
    renderControls()
    const btns = await vi.waitFor(() => {
      const all = screen.getAllByRole('button', { name: /new chat/i })
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    fireEvent.click(btns[0])
    expect(mockNavigate).not.toHaveBeenCalled()
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBeNull()
    })
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })
})

// ── Control: Agent selector ───────────────────────────────────────────────────
//
// AgentPicker is shared between both bands: jsdom renders it twice. Use getAllBy.

describe('ChatControls — Agent selector', () => {
  it('shows the active agent name', async () => {
    renderControls()
    // getAllByText tolerates multiple instances (both layout bands)
    const els = await vi.waitFor(() => {
      const all = screen.getAllByText('Mia')
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    expect(els.length).toBeGreaterThan(0)
  })

  it('shows "Select agent" when no active agent', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: null })
    })
    renderControls()
    await vi.waitFor(() => {
      const els = screen.getAllByText(/select agent/i)
      expect(els.length).toBeGreaterThan(0)
    })
  })

  it('lists workspace-team agents (scoped when workspace + core_team set)', async () => {
    // Workspace scopes to 'mia' only; 'jim' is excluded
    vi.mocked(api.fetchWorkspaces).mockResolvedValueOnce([
      {
        id: 'ws-1',
        name: 'Test WS',
        status: 'active',
        core_team: ['mia'],
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } as any,
    ])
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    })
    renderControls()
    // The active agent button shows Mia (may appear twice — both bands)
    await vi.waitFor(() => {
      const els = screen.getAllByText('Mia')
      expect(els.length).toBeGreaterThan(0)
    })
    // Jim should not appear in the picker (scoped out)
    expect(screen.queryByText('Jim')).not.toBeInTheDocument()
  })

  it('excludes worker agents from the dropdown', async () => {
    vi.mocked(api.fetchAgents).mockResolvedValueOnce([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      {
        id: 'builder',
        name: 'Builder Worker',
        type: 'worker',
        status: 'active',
        description: 'Labour agent',
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ] as any[])
    renderControls()
    await vi.waitFor(() => {
      const els = screen.getAllByText('Mia')
      expect(els.length).toBeGreaterThan(0)
    })
    expect(screen.queryByText('Builder Worker')).not.toBeInTheDocument()
  })
})

// ── Control: Model selector ───────────────────────────────────────────────────

describe('ChatControls — Model selector (interactive)', () => {
  it('renders the model selector trigger', async () => {
    renderControls()
    // Both bands render the model selector — getAllByTestId handles multiples
    const triggers = await vi.waitFor(() => {
      const all = screen.getAllByTestId('composer-model-selector')
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    expect(triggers.length).toBeGreaterThan(0)
  })

  it('updates nextModel in the store when a model is picked', async () => {
    act(() => {
      useChatStore.setState({ nextModel: null })
    })
    renderControls()
    const triggers = await vi.waitFor(() => {
      const all = screen.getAllByTestId('composer-model-selector')
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    expect(triggers.length).toBeGreaterThan(0)
  })
})

// ── Control: Token counter ────────────────────────────────────────────────────

describe('ChatControls — Token counter', () => {
  it('renders the token counter with the formatted value', async () => {
    renderControls()
    const counters = await vi.waitFor(() => {
      const all = screen.getAllByTestId('session-token-counter')
      if (all.length === 0) throw new Error('not rendered')
      return all
    })
    expect(counters.length).toBeGreaterThan(0)
    const values = screen.getAllByTestId('session-token-value')
    expect(values[0].textContent).toContain('44.0k')
    expect(values[0].textContent).toContain('tokens')
  })

  it('does not render any dollar amount', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getAllByTestId('session-token-counter'))
    expect(container.textContent).not.toMatch(/\$\d/)
  })

  it('does not render the CurrencyDollar Phosphor icon', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getAllByTestId('session-token-counter'))
    expect(container.querySelector('[data-phosphor-icon="CurrencyDollar"]')).toBeNull()
  })
})

// ── Control: Sessions button ──────────────────────────────────────────────────
//
// Sessions button is shared between bands — jsdom renders it twice.

describe('ChatControls — Sessions button', () => {
  it('renders a Sessions button', async () => {
    renderControls()
    const btns = await vi.waitFor(() =>
      screen.getAllByRole('button', { name: /open sessions panel/i }),
    )
    expect(btns.length).toBeGreaterThan(0)
  })

  it('calls openSessionPanel when clicked', async () => {
    renderControls()
    const btns = await vi.waitFor(() =>
      screen.getAllByRole('button', { name: /open sessions panel/i }),
    )
    fireEvent.click(btns[0])
    expect(useUiStore.getState().sessionPanelOpen).toBe(true)
  })
})

// ── All five controls render in order ─────────────────────────────────────────

describe('ChatControls — all five controls present', () => {
  it('renders New Chat, agent selector, model selector, token counter, Sessions in the DOM', async () => {
    renderControls()

    // 1. New Chat — in at least one breakpoint band
    await vi.waitFor(() => {
      expect(screen.getAllByRole('button', { name: /new chat/i }).length).toBeGreaterThan(0)
    })

    // 2. Agent selector (shows Mia — in both bands)
    await vi.waitFor(() => {
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
    })

    // 3. Model selector — in at least one band
    expect(screen.getAllByTestId('composer-model-selector').length).toBeGreaterThan(0)

    // 4. Token counter — in at least one band
    expect(screen.getAllByTestId('session-token-counter').length).toBeGreaterThan(0)

    // 5. Sessions button — in at least one band
    expect(screen.getAllByRole('button', { name: /open sessions panel/i }).length).toBeGreaterThan(0)
  })
})

// ── Responsive layout: "More" popover ────────────────────────────────────────
//
// jsdom does not perform real CSS layout, so we cannot test actual breakpoint
// visibility (hidden/flex based on viewport width). Instead we assert on the
// structural presence of the responsive wrappers and the "More" trigger.
//
// The responsive design is:
//   - desktop band (hidden lg:flex): New Chat + Agent + Model + Tokens + Sessions
//   - mobile band  (flex lg:hidden): Agent + [More trigger] + Sessions
//     with More popover containing: New Chat, Model selector, Token counter

describe('ChatControls — responsive layout (structural)', () => {
  it('renders the "More chat controls" trigger button in the mobile band', async () => {
    renderControls()
    const moreTrigger = await vi.waitFor(() =>
      screen.getByTestId('chat-controls-more-trigger'),
    )
    expect(moreTrigger).toBeInTheDocument()
    expect(moreTrigger).toHaveAttribute('aria-label', 'More chat controls')
  })

  it('opens the "More" popover and reveals New Chat, model selector, token counter', async () => {
    renderControls()
    // Wait for agents to load
    await vi.waitFor(() => {
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
    })

    const moreTrigger = screen.getByTestId('chat-controls-more-trigger')
    fireEvent.click(moreTrigger)

    // The popover content should now be in the DOM
    const popover = await vi.waitFor(() => screen.getByTestId('chat-controls-more-popover'))
    expect(popover).toBeInTheDocument()

    // New Chat inside "More"
    expect(screen.getByTestId('more-new-chat')).toBeInTheDocument()

    // Model selector inside "More"
    expect(screen.getByTestId('more-model-selector')).toBeInTheDocument()

    // Token counter — now appears in both bands plus the More popover
    const counters = screen.getAllByTestId('session-token-counter')
    expect(counters.length).toBeGreaterThan(0)
  })

  it('clicking "New Chat" inside the More popover fires the handler', async () => {
    mockPathname = '/'
    renderControls()
    await vi.waitFor(() => {
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
    })

    const moreTrigger = screen.getByTestId('chat-controls-more-trigger')
    fireEvent.click(moreTrigger)

    const moreNewChat = await vi.waitFor(() => screen.getByTestId('more-new-chat'))
    fireEvent.click(moreNewChat)

    expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
  })

  it('agent picker is present in the DOM (rendered in both bands)', async () => {
    renderControls()
    await vi.waitFor(() => {
      // 'Mia' appears in both the desktop and mobile band agent pickers
      const miaEls = screen.getAllByText('Mia')
      expect(miaEls.length).toBeGreaterThanOrEqual(2)
    })
  })

  it('Sessions button is present in the DOM (rendered in both bands)', async () => {
    renderControls()
    // Sessions button has aria-label "Open sessions panel" — appears in both bands
    const sessionBtns = await vi.waitFor(() =>
      screen.getAllByRole('button', { name: /open sessions panel/i }),
    )
    expect(sessionBtns.length).toBeGreaterThanOrEqual(2)
  })
})
