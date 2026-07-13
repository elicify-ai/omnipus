// useWorkspaceTeamIds.test.ts — Fix B's assignee-scoping hook.
//
// Covers the telemetry contract documented at useWorkspaceTeamIds.ts:39-47:
// a failed delegation-graph fetch is not silent — the hook itself logs a
// rate-limited `workspaceTeamIdsFetchFailed` telemetry event once per error
// transition (`logError`, see src/lib/telemetry.ts) — while a successful
// fetch never logs anything.
//
// Idiom choice: a dedicated hook test rather than folding this into
// CreateTaskSlideOver.test.tsx / TaskDetailPanel.test.tsx. Both of those
// already vi.mock('@/lib/api', ...) to drive the team-scoping DEGRADE
// (disabled picker, "team unavailable" hint, unscoped fallback), but neither
// touches '@/lib/telemetry' — asserting this hook's logError contract
// through either component would mean bolting a telemetry spy onto a full
// rendered SlideOver/Panel (open the Agent picker, wait for the debounced
// query to settle, etc.) just to observe a concern that belongs to the hook
// itself, not its callers. This directory already has the two precedents
// needed to test it directly and cleanly:
//   - useFileUpload.test.ts: `import * as telemetry from '@/lib/telemetry'`
//     + `vi.spyOn(telemetry, 'logError')` to assert a hook's telemetry call
//     (works here because useWorkspaceTeamIds.ts is a DIFFERENT module than
//     telemetry.ts calling the imported `logError` — not the same-module
//     call session.diagnostics.test.ts/chat.outbound-validation.test.ts
//     document as unspyable).
//   - useRunningActivity.test.ts: the QueryClientProvider `renderHook`
//     wrapper for a useQuery-backed hook.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import * as telemetry from '@/lib/telemetry'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchWorkspaceDelegation: vi.fn() }
})

import { fetchWorkspaceDelegation } from '@/lib/api'
import { useWorkspaceTeamIds } from './useWorkspaceTeamIds'

// ── Wrapper (mirrors src/hooks/useRunningActivity.test.ts's QueryClientProvider pattern) ──

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

beforeEach(() => {
  vi.mocked(fetchWorkspaceDelegation).mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useWorkspaceTeamIds — fetch-failure telemetry', () => {
  it('logs workspaceTeamIdsFetchFailed exactly once, with the event name and workspace id, when the delegation query fails', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValue(new Error('network down'))
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const { result } = renderHook(() => useWorkspaceTeamIds('ws-1'), {
      wrapper: makeWrapper(makeClient()),
    })

    await waitFor(() => expect(result.current.isError).toBe(true))

    expect(logErrorSpy).toHaveBeenCalledTimes(1)
    expect(logErrorSpy).toHaveBeenCalledWith({
      event: 'workspaceTeamIdsFetchFailed',
      workspaceId: 'ws-1',
    })
  })

  it('does NOT call logError when the delegation query succeeds', async () => {
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-2',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    const { result } = renderHook(() => useWorkspaceTeamIds('ws-2'), {
      wrapper: makeWrapper(makeClient()),
    })

    await waitFor(() => expect(result.current.teamIds).toEqual(new Set(['mia'])))
    expect(result.current.isError).toBe(false)

    expect(logErrorSpy).not.toHaveBeenCalled()
  })
})
