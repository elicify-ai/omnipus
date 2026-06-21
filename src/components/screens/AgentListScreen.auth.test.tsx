/**
 * AgentListScreen.auth.test.tsx — MIN-9 auth-guard test.
 *
 * Verifies that /api/v1/system/cli-detect is NOT fetched when the auth token
 * is absent (token=null), and IS fetched once the token is present.
 *
 * This file lives separately from AgentListScreen.test.tsx so it can define
 * its own controllable useAuthStore mock (the main test file hard-codes a
 * token, which masks the guard).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── useAuthStore is a spy so individual tests can set token=null or a real value.
const authStoreSpy = vi.fn()
vi.mock('@/store/auth', () => ({
  useAuthStore: (selector: (s: { token: string | null; role: string | null; username: string | null }) => unknown) =>
    authStoreSpy(selector),
}))

// ── Global fetch spy so we can assert on /api/v1/system/cli-detect calls.
const fetchSpy = vi.fn()
vi.stubGlobal('fetch', fetchSpy)

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents: vi.fn().mockResolvedValue([]), updateAgent: vi.fn(), testAgentRunner: vi.fn() }
})

vi.mock('@/components/agents/CreateAgentModal', () => ({
  CreateAgentModal: () => null,
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
    Link: ({ children, ...rest }: { children: React.ReactNode } & Record<string, unknown>) => (
      <a {...rest}>{children}</a>
    ),
  }
})

import { AgentListScreen } from './AgentListScreen'

function setToken(token: string | null) {
  authStoreSpy.mockImplementation(
    (selector: (s: { token: string | null; role: string | null; username: string | null }) => unknown) =>
      selector({ token, role: token ? 'admin' : null, username: token ? 'admin' : null }),
  )
}

function renderScreen() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AgentListScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  // Default: successful cli-detect response.
  fetchSpy.mockResolvedValue({
    ok: true,
    json: async () => ({ hasClaude: true, hasCodex: true, hasOpencode: true }),
  })
})

describe('AgentListScreen — MIN-9 auth guard for cli-detect', () => {
  it('does NOT call /api/v1/system/cli-detect when token is null', async () => {
    setToken(null)
    renderScreen()

    // Give the useEffect a full async cycle to run (it shouldn't fire the fetch).
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50))
    })

    const cliDetectCalls = fetchSpy.mock.calls.filter(
      (args: unknown[]) =>
        typeof args[0] === 'string' &&
        (args[0] as string).includes('cli-detect'),
    )
    expect(cliDetectCalls).toHaveLength(0)
  })

  it('calls /api/v1/system/cli-detect when a token is present', async () => {
    setToken('real-token')
    renderScreen()

    await waitFor(() => {
      const cliDetectCalls = fetchSpy.mock.calls.filter(
        (args: unknown[]) =>
          typeof args[0] === 'string' &&
          (args[0] as string).includes('cli-detect'),
      )
      expect(cliDetectCalls.length).toBeGreaterThan(0)
    })
  })
})
