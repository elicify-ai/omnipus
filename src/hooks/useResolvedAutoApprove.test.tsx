/**
 * useResolvedAutoApprove tests (ADR-092).
 *
 * Founder decision (2026-09-24): Auto-approve no longer requires an
 * enforcing kernel sandbox — `resolved` must be true whenever the session/
 * pending/global resolution (chat modifier, else global default) says so, REGARDLESS of
 * `kernel_sandbox_active`. `kernelSandboxActive` is exposed only as a
 * separate, warning-only flag for callers (the composer Auto button)
 * to build a caution from — it must never gate `resolved` here.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSandboxStatus: vi.fn(),
  }
})

import * as api from '@/lib/api'
import type { SandboxStatus } from '@/lib/api'
import { useResolvedAutoApprove } from './useResolvedAutoApprove'

function sandboxStatus(overrides: Partial<SandboxStatus> = {}): SandboxStatus {
  return {
    backend: 'landlock',
    available: true,
    kernel_level: true,
    policy_applied: true,
    seccomp_enabled: true,
    bind_ports_count: 0,
    ...overrides,
  }
}

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

beforeEach(() => {
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1', activeAgentType: 'core' })
    useChatStore.setState({ autoApproveEffective: undefined, sessionsById: {}, pendingAutoApproveChoice: null })
  })
})

describe('useResolvedAutoApprove — Auto is effective without a sandbox (2026-09-24)', () => {
  it('resolves true from the global default even when kernel_sandbox_active is false', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: false }),
    )
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => {
      expect(result.current.resolved).toBe(true)
      expect(result.current.kernelSandboxActive).toBe(false)
    })
  })

  it('resolves true from a per-chat session override even when kernel_sandbox_active is false', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: false }),
    )
    act(() => {
      useChatStore.setState({ autoApproveEffective: true })
    })
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => {
      expect(result.current.resolved).toBe(true)
      expect(result.current.kernelSandboxActive).toBe(false)
    })
  })

  it('resolves true from a pending pre-session choice even when kernel_sandbox_active is false', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: false }),
    )
    act(() => {
      useSessionStore.setState({ activeSessionId: null })
      useChatStore.setState({ pendingAutoApproveChoice: true })
    })
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => {
      expect(result.current.resolved).toBe(true)
      expect(result.current.kernelSandboxActive).toBe(false)
    })
  })

  it('resolves false (Ask) when Auto-approve itself is off, independent of kernel_sandbox_active', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true }),
    )
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => {
      expect(result.current.resolved).toBe(false)
      expect(result.current.kernelSandboxActive).toBe(true)
    })
  })
})

describe('useResolvedAutoApprove — two levels only: chat modifier, else global', () => {
  it('a chat modifier of false wins over a global of true', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }),
    )
    act(() => {
      useChatStore.setState({ autoApproveEffective: false })
    })
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => expect(api.fetchSandboxStatus).toHaveBeenCalled())
    await waitFor(() => expect(result.current.resolved).toBe(false))
  })

  it('a chat modifier of true wins over a global of false', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: false, kernel_sandbox_active: true }),
    )
    act(() => {
      useChatStore.setState({ autoApproveEffective: true })
    })
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => expect(result.current.resolved).toBe(true))
  })

  it('with no chat modifier it resolves to the global value', async () => {
    vi.mocked(api.fetchSandboxStatus).mockResolvedValue(
      sandboxStatus({ auto_approve_effective: true, kernel_sandbox_active: true }),
    )
    const { result } = renderHook(() => useResolvedAutoApprove(), { wrapper })
    await waitFor(() => expect(result.current.resolved).toBe(true))
  })
})
