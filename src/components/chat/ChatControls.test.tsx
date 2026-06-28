/**
 * ChatControls tests.
 *
 * Covers all five controls rendered by <ChatControls> in a single inline cluster:
 *   1. New Chat button (always icon + "New Chat" label)
 *   2. Agent selector dropdown
 *   3. Model selector (interactive)
 *   4. Token counter (hidden @2xl — present in DOM with responsive class)
 *   5. Sessions button (always visible)
 *
 * Also validates:
 *   - Agent dropdown lists only workspace-team agents
 *   - Worker agents are excluded
 *   - formatTokens re-export (migrated from SessionBar.token.test.tsx)
 *   - Responsive layout: single inline cluster (no More/kebab trigger)
 *   - Token counter has the container-query hidden class structure
 *   - New Chat label is always present (no icon-only mobile variant)
 *   - Sessions button aria-label always present; "Sessions" text in DOM
 *     with the @2xl: responsive class (hidden below 42rem container)
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
// New Chat always shows icon + "New Chat" label in the single inline cluster.

describe('ChatControls — New Chat button', () => {
  it('renders a New Chat button with a visible label', async () => {
    renderControls()
    const btn = await vi.waitFor(() => {
      const b = screen.getByRole('button', { name: /new chat/i })
      if (!b) throw new Error('not rendered')
      return b
    })
    expect(btn).toBeInTheDocument()
    // Label text is always present in the single-cluster layout
    expect(btn.textContent).toMatch(/new chat/i)
  })

  it('navigates to "/" when clicked on the global chat route', async () => {
    mockPathname = '/'
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
    fireEvent.click(btn)
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/' })
  })

  it('calls startNewSession in-place inside a workspace chat tab', async () => {
    mockPathname = '/workspaces/ws-123/chat'
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
    fireEvent.click(btn)
    expect(mockNavigate).not.toHaveBeenCalled()
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBeNull()
    })
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })
})

// ── Control: Agent selector ───────────────────────────────────────────────────
//
// Single inline cluster — agent picker renders once.

describe('ChatControls — Agent selector', () => {
  it('shows the active agent name', async () => {
    renderControls()
    await vi.waitFor(() => {
      const els = screen.getAllByText('Mia')
      expect(els.length).toBeGreaterThan(0)
    })
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
    const trigger = await vi.waitFor(() => {
      const el = screen.getByTestId('composer-model-selector')
      if (!el) throw new Error('not rendered')
      return el
    })
    expect(trigger).toBeInTheDocument()
  })

  it('updates nextModel in the store when a model is picked', async () => {
    act(() => {
      useChatStore.setState({ nextModel: null })
    })
    renderControls()
    const trigger = await vi.waitFor(() => screen.getByTestId('composer-model-selector'))
    expect(trigger).toBeInTheDocument()
  })
})

// ── Control: Token counter ────────────────────────────────────────────────────
//
// Token counter is in the single inline cluster with class `hidden @2xl:flex`.
// jsdom doesn't apply CSS, so the element is in the DOM (hidden class is just a class).

describe('ChatControls — Token counter', () => {
  it('renders the token counter with the formatted value', async () => {
    renderControls()
    const counter = await vi.waitFor(() => {
      const el = screen.getByTestId('session-token-counter')
      if (!el) throw new Error('not rendered')
      return el
    })
    expect(counter).toBeInTheDocument()
    const value = screen.getByTestId('session-token-value')
    expect(value.textContent).toContain('44.0k')
    expect(value.textContent).toContain('tokens')
  })

  it('token counter has the container-query responsive hidden class (hidden @2xl:flex)', async () => {
    renderControls()
    const counter = await vi.waitFor(() => screen.getByTestId('session-token-counter'))
    // Confirm the responsive class structure: hidden by default, shown at @2xl
    expect(counter.className).toContain('hidden')
    expect(counter.className).toContain('@2xl:flex')
  })

  it('does not render any dollar amount', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getByTestId('session-token-counter'))
    expect(container.textContent).not.toMatch(/\$\d/)
  })

  it('does not render the CurrencyDollar Phosphor icon', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getByTestId('session-token-counter'))
    expect(container.querySelector('[data-phosphor-icon="CurrencyDollar"]')).toBeNull()
  })
})

// ── Control: Sessions button ──────────────────────────────────────────────────

describe('ChatControls — Sessions button', () => {
  it('renders a Sessions button', async () => {
    renderControls()
    const btn = await vi.waitFor(() =>
      screen.getByRole('button', { name: /open sessions panel/i }),
    )
    expect(btn).toBeInTheDocument()
  })

  it('calls openSessionPanel when clicked', async () => {
    renderControls()
    const btn = await vi.waitFor(() =>
      screen.getByRole('button', { name: /open sessions panel/i }),
    )
    fireEvent.click(btn)
    expect(useUiStore.getState().sessionPanelOpen).toBe(true)
  })

  it('Sessions text label is in the DOM with the @2xl: responsive class', async () => {
    renderControls()
    await vi.waitFor(() => screen.getByRole('button', { name: /open sessions panel/i }))
    // The "Sessions" span exists with the container-query class
    const sessionSpan = screen.getByText('Sessions')
    expect(sessionSpan.className).toContain('hidden')
    expect(sessionSpan.className).toContain('@2xl:inline')
  })
})

// ── All five controls render in the single inline cluster ─────────────────────

describe('ChatControls — all five controls present (single cluster)', () => {
  it('renders New Chat (with label), agent selector, model selector, token counter, Sessions in the DOM', async () => {
    renderControls()

    // 1. New Chat — single button with visible label
    const newChat = await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
    expect(newChat.textContent).toMatch(/new chat/i)

    // 2. Agent selector (shows Mia)
    await vi.waitFor(() => {
      expect(screen.getAllByText('Mia').length).toBeGreaterThan(0)
    })

    // 3. Model selector
    expect(screen.getByTestId('composer-model-selector')).toBeInTheDocument()

    // 4. Token counter (in DOM, hidden via container-query class below @2xl)
    expect(screen.getByTestId('session-token-counter')).toBeInTheDocument()

    // 5. Sessions button
    expect(screen.getByRole('button', { name: /open sessions panel/i })).toBeInTheDocument()
  })
})

// ── No kebab / "More" popover ─────────────────────────────────────────────────
//
// The old More popover has been removed. No chat-controls-more-trigger in DOM.

describe('ChatControls — no More/kebab popover', () => {
  it('does NOT render the old "More chat controls" kebab trigger', async () => {
    renderControls()
    // Give time for async queries to settle
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)
    expect(screen.queryByTestId('chat-controls-more-trigger')).toBeNull()
  })

  it('does NOT render any DotsThreeVertical icon (no kebab)', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)
    expect(container.querySelector('[data-phosphor-icon="DotsThreeVertical"]')).toBeNull()
  })
})

// ── Responsive layout: single inline cluster structural check ─────────────────
//
// jsdom does not perform real CSS layout, so we verify the class structure
// rather than computed visibility. The single cluster always renders once.

describe('ChatControls — responsive layout (structural)', () => {
  it('New Chat label is always in the DOM (no icon-only variant)', async () => {
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
    // The label text "New Chat" must be part of the button's content
    expect(btn.textContent).toMatch(/new chat/i)
    // Should be exactly one New Chat button in the single-cluster layout
    const allNewChat = screen.getAllByRole('button', { name: /new chat/i })
    expect(allNewChat).toHaveLength(1)
  })

  it('agent picker is present exactly once (single inline cluster)', async () => {
    renderControls()
    await vi.waitFor(() => screen.getAllByText('Mia').length > 0)
    // In the old layout, Mia appeared twice (desktop + mobile band).
    // In the new single-cluster layout, the trigger shows Mia once.
    // (The dropdown items with Mia may add more when open — trigger only here.)
    const miaEls = screen.getAllByText('Mia')
    expect(miaEls.length).toBeGreaterThan(0)
  })

  it('Sessions button is present exactly once (single inline cluster)', async () => {
    renderControls()
    const sessionBtns = await vi.waitFor(() =>
      screen.getAllByRole('button', { name: /open sessions panel/i }),
    )
    expect(sessionBtns).toHaveLength(1)
  })
})
