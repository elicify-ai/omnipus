// useSlashMenu.commands-recovery.test.ts — regression test for the
// slash-palette silent-empty bugfix.
//
// Confirmed bug: GET /api/v1/commands (['commands','web'], this hook) is
// behind withAuth and may 401 before the session cookie exists (fresh
// install race). The query then sits permanently errored — nothing in the
// app ever refetches it — so the palette silently stays limited to the two
// hardcoded client-only entries (/resume, /workspace) for the entire page
// session, recoverable only by a hard reload.
//
// The fix: login.tsx and onboarding.tsx now call
// `queryClient.invalidateQueries({ queryKey: ['commands'] })` at the exact
// point the session becomes valid. This test proves the MECHANISM that fix
// relies on — that invalidating the shared ['commands', ...] cache entry
// while useSlashMenu is already mounted (no remount) clears `commandsError`
// and repopulates `slashItems` with the backend list — using the REAL
// `@tanstack/react-query` (unlike useSlashMenu.test.ts, which fully mocks
// `useQuery` and therefore cannot exercise real cache invalidation).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import * as React from 'react'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComposerRuntime } from '@assistant-ui/react'
import { useSlashMenu } from './useSlashMenu'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

const mockBackendCommands = [
  { name: 'cancel', label: '/cancel', description: 'Cancel the current turn', delivery: 'client', available_while_streaming: true },
  { name: 'goal', label: '/goal', description: 'Set a goal', delivery: 'agent', available_while_streaming: false },
]

// Mutable per-test fetchCommands mock — starts rejecting (simulating the
// pre-cookie 401), later swapped to resolve (simulating a retry after the
// session becomes valid).
const fetchCommandsMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchCommands: (...args: unknown[]) => fetchCommandsMock(...args),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

function makeComposerRuntime(): ComposerRuntime & { setText: ReturnType<typeof vi.fn> } {
  return {
    getState: () => ({ text: '' }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  } as unknown as ComposerRuntime & { setText: ReturnType<typeof vi.fn> }
}

function makeQueryClient(): QueryClient {
  // retry:false — isolates this test from the global queryClient.ts retry/
  // backoff timing (already covered separately); the point here is proving
  // invalidateQueries triggers a refetch on an already-mounted observer, not
  // re-testing retry semantics.
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

describe('useSlashMenu — commands cache recovers after invalidateQueries (no remount)', () => {
  beforeEach(() => {
    fetchCommandsMock.mockReset()
    act(() => {
      useSessionStore.setState({
        activeAgentId: null,
        activeSessionId: null,
        activeAgentType: null,
        attachedSessionType: null,
        attachedTaskTitle: null,
      })
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('a 401-style rejection followed by invalidateQueries repopulates the palette WITHOUT unmounting the hook', async () => {
    // Step 1: simulate the pre-cookie 401 — fetchCommands rejects.
    fetchCommandsMock.mockRejectedValue(Object.assign(new Error('unauthorized'), { status: 401 }))

    const client = makeQueryClient()
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      React.createElement(QueryClientProvider, { client }, children)

    const { result } = renderHook(
      () =>
        useSlashMenu({
          isStreaming: false,
          isReplaying: false,
          inputEnabled: true,
          composerRuntime: makeComposerRuntime(),
          appendMessage: vi.fn(),
          startNewSession: vi.fn(),
          cancelIfStreaming: vi.fn(),
        }),
      { wrapper },
    )

    // Wait for the errored query to settle.
    await waitFor(() => {
      expect(result.current.commandsError).toBe(true)
    })

    // Open the "/" menu — only the two hardcoded client-only commands are
    // present; the backend list never arrived.
    act(() => { result.current.onInputChange('/') })
    const labelsBeforeRecovery = result.current.slashItems.map((i) => i.label)
    expect(labelsBeforeRecovery).toContain('/resume')
    expect(labelsBeforeRecovery).toContain('/workspace')
    expect(labelsBeforeRecovery).not.toContain('/cancel')
    expect(labelsBeforeRecovery).not.toContain('/goal')

    // Step 2: the session becomes valid — fetchCommands now succeeds, same
    // as after a real login/onboarding completion establishes the cookie.
    fetchCommandsMock.mockResolvedValue(mockBackendCommands)

    // Step 3: the fix — invalidate the shared cache entry (exactly what
    // login.tsx / onboarding.tsx now do) on the SAME renderHook instance —
    // no unmount/remount anywhere in this test.
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['commands'] })
    })

    await waitFor(() => {
      expect(result.current.commandsError).toBe(false)
    })

    const labelsAfterRecovery = result.current.slashItems.map((i) => i.label)
    expect(labelsAfterRecovery).toContain('/cancel')
    expect(labelsAfterRecovery).toContain('/goal')
    // The client-only entries still coexist with the recovered backend list.
    expect(labelsAfterRecovery).toContain('/resume')
    expect(labelsAfterRecovery).toContain('/workspace')
  })

  it('without invalidateQueries, the errored query stays broken forever (proves the bug this fix addresses)', async () => {
    fetchCommandsMock.mockRejectedValue(Object.assign(new Error('unauthorized'), { status: 401 }))

    const client = makeQueryClient()
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      React.createElement(QueryClientProvider, { client }, children)

    const { result } = renderHook(
      () =>
        useSlashMenu({
          isStreaming: false,
          isReplaying: false,
          inputEnabled: true,
          composerRuntime: makeComposerRuntime(),
          appendMessage: vi.fn(),
          startNewSession: vi.fn(),
          cancelIfStreaming: vi.fn(),
        }),
      { wrapper },
    )

    await waitFor(() => {
      expect(result.current.commandsError).toBe(true)
    })

    // The session becomes valid, but nothing invalidates the cache.
    fetchCommandsMock.mockResolvedValue(mockBackendCommands)

    act(() => { result.current.onInputChange('/') })
    // Give react-query's staleTime/GC a moment; without an explicit
    // invalidate, the query stays in its errored state (60s staleTime).
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(result.current.commandsError).toBe(true)
    const labels = result.current.slashItems.map((i) => i.label)
    expect(labels).not.toContain('/cancel')
    expect(labels).not.toContain('/goal')
  })
})
