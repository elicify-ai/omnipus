/**
 * ChatControls tests.
 *
 * ChatControls was stripped down to solely the Open browser launcher per the
 * Composer Redesign (variant A1) — the Agent picker, Model selector, and
 * Token counter moved into the composer's context row (above the card)
 * (src/components/chat/composer/{AgentPicker,ModelPicker,TokenCounter}.tsx),
 * New Chat moved to the sidebar row + /new, and Sessions was superseded by
 * the sidebar accordion + SearchModal. Their assertions moved with them (see
 * AgentPicker.test.tsx for the agent-scoping coverage,
 * AgentPicker.agent-selector-open.test.tsx for SC-005, and
 * ModelPicker.test.tsx / TokenCounter.test.tsx for the rest).
 *
 * Covers what <ChatControls> renders today:
 *   1. Open browser button (ADR-039 D-A1) — the sole control
 *   2. Absence of New Chat and the old "More chat controls" kebab
 *   3. Composer tab-ring position 7 (last stop of the closed ring)
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
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
  })
})

// ── Control: New Chat ─────────────────────────────────────────────────────────
//
// New Chat always shows icon + "New Chat" label in the single inline cluster.

// ── All controls render in the single inline cluster ─────────────────────────

describe('ChatControls — all controls present (single cluster)', () => {
  it('renders Open browser and NOT New Chat (removed — sidebar + /new cover it)', async () => {
    renderControls()

    // Open browser is the sole remaining control
    const openBrowser = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    expect(openBrowser).toBeInTheDocument()

    // New Chat was removed from the header (redundant with the sidebar's
    // per-workspace New chat row and the /new slash command).
    expect(screen.queryByRole('button', { name: /new chat/i })).not.toBeInTheDocument()

    // Negative guard: the pickers must NEVER come back to the header cluster.
    expect(screen.queryByTestId('agent-picker-trigger')).not.toBeInTheDocument()
    expect(screen.queryByTestId('composer-model-selector')).not.toBeInTheDocument()
    expect(screen.queryByTestId('session-token-counter')).not.toBeInTheDocument()
  })
})

// ── No kebab / "More" popover ─────────────────────────────────────────────────

describe('ChatControls — no More/kebab popover', () => {
  it('does NOT render the old "More chat controls" kebab trigger', async () => {
    renderControls()
    await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    expect(screen.queryByTestId('chat-controls-more-trigger')).toBeNull()
  })

  it('does NOT render any DotsThreeVertical icon (no kebab)', async () => {
    const { container } = renderControls()
    await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    expect(container.querySelector('[data-phosphor-icon="DotsThreeVertical"]')).toBeNull()
  })
})

// ── Control: Open browser (ADR-039 D-A1) ────────────────────────────────────

describe('ChatControls — Open browser launcher', () => {
  beforeEach(() => {
    act(() => {
      useUiStore.setState({ browserPanel: null, toasts: [] })
      // Explicit, not incidental: every case in this block asserts the exact
      // workspace argument the launcher sends, so the "no workspace" cases
      // must start from a real null rather than whatever a prior test left.
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
    vi.mocked(api.createSession).mockReset()
  })

  it('renders an "Open browser" button', async () => {
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    expect(btn).toBeInTheDocument()
  })

  // Composer tab ring, position 7 (last stop of the closed ring: skip-link=1
  // → chat input=2 → agent=3 → model=4 → attach=5 → send=6 → browser=7, see
  // the full map comment above this button in ChatControls.tsx). A
  // well-meaning renumber that drops or shifts this value should fail here
  // rather than silently break the operator-mandated tab order.
  it('carries the composer tab ring position 7 (last stop)', async () => {
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))
    expect(btn).toHaveAttribute('tabindex', '7')
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
      expect(api.createSession).toHaveBeenCalledWith('mia', undefined)
    })
    await vi.waitFor(() => {
      expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess_new', agentId: 'mia' })
    })
    expect(useSessionStore.getState().activeSessionId).toBe('sess_new')
    // No error toast — this is the success path.
    expect(useUiStore.getState().toasts.some((t) => t.variant === 'error')).toBe(false)
  })

  // U2. The launcher's session is the ONE the live browser panel then attaches
  // against, and the gateway decides which workspace's browser — and whose
  // live logins — that panel shows by reading the workspace off this session's
  // own meta, server-side (ADR-072 FR-016/FR-017). Creating it with agent_id
  // alone left the panel nothing to read: an agent on more than one
  // workspace's team was refused and told to "open this panel from a chat that
  // belongs to the workspace you mean" — from inside exactly such a chat. The
  // route named the workspace and the store held it; it just never travelled
  // with the create. Asserting the argument, not the eventual refusal, is
  // deliberate: this is the only place in the SPA that can carry it.
  it('creates that session in the workspace the chat belongs to', async () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null, activeAgentId: 'mia' })
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws_alpha' })
    })
    vi.mocked(api.createSession).mockResolvedValue({
      id: 'sess_new',
      agent_id: 'mia',
      title: 'New chat',
      type: 'chat',
      created_at: '2026-07-11T00:00:00Z',
      updated_at: '2026-07-11T00:00:00Z',
      message_count: 0,
      total_tokens: 0,
      total_cost: 0,
    })
    renderControls()
    const btn = await vi.waitFor(() => screen.getByRole('button', { name: /open browser/i }))

    fireEvent.click(btn)

    await vi.waitFor(() => {
      expect(api.createSession).toHaveBeenCalledWith('mia', 'ws_alpha')
    })
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
