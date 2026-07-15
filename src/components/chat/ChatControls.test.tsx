/**
 * ChatControls tests.
 *
 * ChatControls was stripped down to New Chat · Sessions · Open browser per
 * the Composer Redesign (variant A1) — the Agent picker, Model selector, and
 * Token counter moved into the composer's context row (above the card)
 * (src/components/chat/composer/{AgentPicker,ModelPicker,TokenCounter}.tsx).
 * Their assertions moved with them (see AgentPicker.test.tsx for the
 * agent-scoping coverage, AgentPicker.agent-selector-open.test.tsx for SC-005,
 * and ModelPicker.test.tsx / TokenCounter.test.tsx for the rest).
 *
 * Covers the three controls still rendered by <ChatControls>:
 *   1. New Chat button (always icon + "New Chat" label)
 *   2. Sessions button (always visible)
 *   3. Open browser button (ADR-039 D-A1)
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import * as api from '@/lib/api'

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
    createSession: vi.fn(),
  }
})

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
    useUiStore.setState({ sessionPanelOpen: false })
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

// ── All controls render in the single inline cluster ─────────────────────────

describe('ChatControls — all controls present (single cluster)', () => {
  it('renders New Chat and Open browser in the DOM', async () => {
    renderControls()

    // 1. New Chat — single button with visible label
    const newChat = await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
    expect(newChat.textContent).toMatch(/new chat/i)

    // 2. Open browser button
    expect(screen.getByRole('button', { name: /open browser/i })).toBeInTheDocument()

    // Negative guard: the pickers must NEVER come back to the header cluster.
    expect(screen.queryByTestId('agent-picker-trigger')).not.toBeInTheDocument()
    expect(screen.queryByTestId('composer-model-selector')).not.toBeInTheDocument()
    expect(screen.queryByTestId('session-token-counter')).not.toBeInTheDocument()
  })
})

// ── No kebab / "More" popover ─────────────────────────────────────────────────
//
// The old More popover has been removed. No chat-controls-more-trigger in DOM.

describe('ChatControls — no More/kebab popover', () => {
  it('does NOT render the old "More chat controls" kebab trigger', async () => {
    renderControls()
    await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
    expect(screen.queryByTestId('chat-controls-more-trigger')).toBeNull()
  })

  it('does NOT render any DotsThreeVertical icon (no kebab)', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getByRole('button', { name: /new chat/i }))
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
})

// ── Control: Open browser (ADR-039 D-A1) ────────────────────────────────────

describe('ChatControls — Open browser launcher', () => {
  beforeEach(() => {
    act(() => {
      useUiStore.setState({ browserPanel: null, toasts: [] })
    })
    vi.mocked(api.createSession).mockReset()
  })

  it('renders an "Open browser" button', async () => {
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    expect(btn).toBeInTheDocument()
  })

  it('opens the browser panel with the active session/agent when clicked (session already exists)', async () => {
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))

    expect(useUiStore.getState().browserPanel).toBeNull()
    fireEvent.click(btn)
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess_1', agentId: 'mia' })
    // No session existed to create — createSession must not be called.
    expect(api.createSession).not.toHaveBeenCalled()
  })

  // UAT finding FE-1: a brand-new chat (zero messages sent yet) has
  // activeSessionId === null. Per ADR-039 D-A1 the launcher must still open a
  // ready/blank browser rather than erroring — it now ensures a real session
  // exists first (mirroring attachment-adapter.ts's ensureSession), exactly
  // like the composer's own first-send path does.
  it('creates a session and opens the panel with it when there is no active session yet', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null, activeAgentId: 'mia' })
    })
    const createdSession: Awaited<ReturnType<typeof api.createSession>> = {
      id: 'sess_new',
      agent_id: 'mia',
      title: 'New chat',
      type: 'chat',
      created_at: '2026-07-11T00:00:00Z',
      updated_at: '2026-07-11T00:00:00Z',
      message_count: 0,
      total_tokens: 0,
      total_cost: 0,
    }
    vi.mocked(api.createSession).mockResolvedValue(createdSession)
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))

    fireEvent.click(btn)

    await vi.waitFor(() => {
      expect(api.createSession).toHaveBeenCalledWith('mia')
    })
    await vi.waitFor(() => {
      expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess_new', agentId: 'mia' })
    })
    expect(useSessionStore.getState().activeSessionId).toBe('sess_new')
    // No error toast — this is the success path.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(false)
  })

  it('toasts an error instead of opening the panel when there is no active agent at all', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
    })
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))

    fireEvent.click(btn)
    expect(useUiStore.getState().browserPanel).toBeNull()
    expect(useUiStore.getState().toasts.some((t) => /select an agent/i.test(t.message))).toBe(true)
    expect(api.createSession).not.toHaveBeenCalled()
  })

  it('toasts an error and does not open the panel when session creation fails', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null, activeAgentId: 'mia' })
    })
    vi.mocked(api.createSession).mockRejectedValue(new Error('network down'))
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))

    fireEvent.click(btn)

    await vi.waitFor(() => {
      expect(useUiStore.getState().toasts.some((t) => t.variant === 'error' && /network down/i.test(t.message))).toBe(true)
    })
    expect(useUiStore.getState().browserPanel).toBeNull()
  })
})
