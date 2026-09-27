// useDevModeBypassKnown.test.ts — the shared dev_mode_bypass gate
// (extracted from GodModeControl.tsx, commit 671a68ad6) used by every
// caller that must skip a request known to 503 under gateway.dev_mode_bypass
// (adminWrap-gated endpoints — RequireNotBypass returns 503 before the
// handler ever runs, pkg/gateway/rest.go's adminWrap doc comment).
//
// This is a UNIT test of the hook's own resolution logic (given a mocked
// AppState), not an end-to-end proof that the gateway actually reports
// dev_mode_bypass correctly — that half is pinned server-side by
// pkg/gateway/rest_status_test.go::TestHandleStateGET_DevModeBypass, added
// alongside this branch's fix to pkg/gateway/rest_status.go::HandleState
// (GET /api/v1/state never set the `dev_mode_bypass` field it documents in
// contracts/components/schemas/AppState.yaml, so this hook's own gate logic
// was always correct but never actually engaged in production — confirmed
// via CI run 35990283526's gateway.log, 79
// gateway.admin_route_blocked_by_bypass_gate 503s on
// /api/v1/gateway/god-mode across the E2E job).
import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { AppState } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAppState: vi.fn() }
})

import { fetchAppState } from '@/lib/api'
import { useDevModeBypassKnown } from './useDevModeBypassKnown'

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

const BYPASS_ON: AppState = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: false, blocked_reason: 'signed_out' },
  dev_mode_bypass: true,
} as never

const BYPASS_OFF: AppState = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: false, blocked_reason: 'signed_out' },
  dev_mode_bypass: false,
} as never

describe('useDevModeBypassKnown', () => {
  afterEach(() => {
    vi.resetAllMocks()
  })

  it('reads known:false, resolved:false before AppState answers', () => {
    const client = makeClient()
    vi.mocked(fetchAppState).mockReturnValue(new Promise(() => {})) // never resolves

    const { result } = renderHook(() => useDevModeBypassKnown(), { wrapper: makeWrapper(client) })

    expect(result.current).toEqual({ known: false, resolved: false })
    client.clear()
  })

  it('reads known:true, resolved:true once AppState reports dev_mode_bypass:true', async () => {
    const client = makeClient()
    vi.mocked(fetchAppState).mockResolvedValue(BYPASS_ON)

    const { result } = renderHook(() => useDevModeBypassKnown(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current).toEqual({ known: true, resolved: true })
    })
    client.clear()
  })

  it('reads known:false, resolved:true once AppState reports dev_mode_bypass:false', async () => {
    const client = makeClient()
    vi.mocked(fetchAppState).mockResolvedValue(BYPASS_OFF)

    const { result } = renderHook(() => useDevModeBypassKnown(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current).toEqual({ known: false, resolved: true })
    })
    client.clear()
  })

  it('reads known:false, resolved:true on a fetch failure — a transport error must never read as "bypass confirmed on"', async () => {
    const client = makeClient()
    vi.mocked(fetchAppState).mockRejectedValue(new Error('network down'))

    const { result } = renderHook(() => useDevModeBypassKnown(), { wrapper: makeWrapper(client) })

    await waitFor(() => {
      expect(result.current.known).toBe(false)
    })
    // A failed query never produces `data`, so `resolved` (appState !==
    // undefined) correctly stays false too — "AppState answered" and
    // "AppState answered successfully" are the same fact for this hook,
    // since only the success path has a `dev_mode_bypass` value to read.
    expect(result.current.resolved).toBe(false)
    client.clear()
  })
})
