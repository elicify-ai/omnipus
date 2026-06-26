/**
 * SessionBar token counter tests.
 *
 * Verifies:
 *   1. No dollar sign or CurrencyDollar in SessionBar
 *   2. Token counter renders with formatTokens
 *   3. formatTokens helper works correctly
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { formatTokens } from './SessionBar'

// ── Mocks ──────────────────────────────────────────────────────────────────────

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
    useLocation: () => ({ pathname: '/' }),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@/store/chat', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat')>()
  return {
    ...actual,
    useChatStore: vi.fn((selector?: (state: Record<string, unknown>) => unknown) => {
      const state = { sessionTokens: 44000, isStreaming: false }
      return selector ? selector(state as unknown as Parameters<typeof selector>[0]) : state
    }),
  }
})

vi.mock('@/store/session', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session')>()
  return {
    ...actual,
    useSessionStore: vi.fn(() => ({
      activeAgentId: null,
      activeSessionId: null,
      setActiveSession: vi.fn(),
      attachToSession: vi.fn(),
      setActiveAgentType: vi.fn(),
    })),
  }
})

vi.mock('@/store/workspacesStore', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/workspacesStore')>()
  return {
    ...actual,
    useWorkspacesStore: vi.fn(() => null),
  }
})

import { SessionBar } from './SessionBar'

function renderSessionBar() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SessionBar />
    </QueryClientProvider>,
  )
}

// ── formatTokens unit ─────────────────────────────────────────────────────────

describe('formatTokens (SessionBar)', () => {
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

// ── SessionBar render ─────────────────────────────────────────────────────────

describe('SessionBar', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders a token counter with formatted value', () => {
    renderSessionBar()
    // sessionTokens = 44000 → "44.0k tokens"
    const counter = screen.getByTestId('session-token-counter')
    expect(counter).toBeInTheDocument()
    const value = screen.getByTestId('session-token-value')
    expect(value.textContent).toContain('44.0k')
    expect(value.textContent).toContain('tokens')
  })

  it('does not render any dollar amount', () => {
    const { container } = renderSessionBar()
    expect(container.textContent).not.toMatch(/\$\d/)
  })

  it('does not use CurrencyDollar icon (no phosphor dollar icon rendered)', () => {
    const { container } = renderSessionBar()
    // CurrencyDollar renders with data-phosphor-icon="CurrencyDollar"
    expect(container.querySelector('[data-phosphor-icon="CurrencyDollar"]')).toBeNull()
  })
})
