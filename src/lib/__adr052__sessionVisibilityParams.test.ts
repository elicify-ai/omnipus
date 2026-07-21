// ADR-052 Wave 2 — session-list `include_verifier` param contract tests
// (agent "qa-spa", closed out by fix-wave-2 agent E2).
//
// FR-036: verifier-role sessions are excluded by default from
// `GET /api/v1/sessions` unless `include_verifier=true` is passed. Sidebar
// and SearchModal MUST NOT pass it (exclude verifier sessions); UsageScreen
// MUST pass it (verifier LLM spend visible in cost reporting, SC-014).
//
// The backend contract side of this is regenerated and confirmed present:
// `contracts/openapi.yaml:1012` defines the `include_verifier` query param
// on GET /sessions, and `src/lib/api/generated/schemas.ts:5704`/
// `openapi-types.ts:8781` carry it through.
//
// Client-side status (accurate as of this wave):
//   `fetchSessions(agentId?, type?, opts?: { includeVerifier?: boolean })`
//   — src/lib/api.ts:1360 — grew a 3rd, additive-only `opts` param.
//   Sidebar.tsx and SearchModal.tsx still call `fetchSessions()` with zero
//   args (unchanged — verifier sessions stay excluded there by construction,
//   since `opts` is simply undefined). UsageScreen.tsx's "By session" tab
//   now calls `fetchSessions(undefined, undefined, { includeVerifier: true })`,
//   so individual verifier session rows are listed there (tagged
//   "Verifier" in the table; see UsageScreen.test.tsx). SC-014 was ALREADY
//   partially satisfied before this wave — the hero/by-agent/by-model views
//   derive from GET /token-stats, which aggregates every session type
//   unfiltered — this wave closes the one remaining gap, the per-session
//   ROW list, so FR-036/SC-014 are now fully satisfied client-side.
//
// All tests below are real assertions — no it.todo remains in this file.
//
// STRICT OWNERSHIP: this file, src/lib/api.ts, src/components/screens/
// UsageScreen.tsx(.test.tsx) only. Does not touch Sidebar.tsx/SearchModal.tsx
// (their zero-arg call sites needed no code change) or their tests.
//
// Traces to: docs/internal/specs/autonomous-agent-plan-execution-spec.md
//   FR-036 (line 692), SC-014 (line 713), US-13 Acceptance 6 (line 212),
//   BDD "verifier sessions are hidden by default but auditable" (line 438).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

function makeOkResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('ADR-052 FR-036 — fetchSessions() include_verifier opt-in (default omitted, explicit true sent)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(makeOkResponse([]))
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetchSessions() with no args does not send include_verifier in the query string', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions()

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).not.toContain('include_verifier')
  })

  it('fetchSessions(agentId) still does not send include_verifier — filtering by agent is orthogonal to verifier visibility', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions('jim')

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('agent_id=jim')
    expect(url).not.toContain('include_verifier')
  })

  it('fetchSessions(undefined, "task") still does not send include_verifier — type filtering is orthogonal too', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, 'task')

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('type=task')
    expect(url).not.toContain('include_verifier')
  })

  it('fetchSessions(..., { includeVerifier: true }) sends include_verifier=true in the query string', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { includeVerifier: true })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('include_verifier=true')
  })

  it('fetchSessions(..., { includeVerifier: false }) omits include_verifier — an explicit false is still the excluded default', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { includeVerifier: false })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).not.toContain('include_verifier')
  })

  it('fetchSessions(agentId, type, { includeVerifier: true }) combines all three params correctly', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions('jim', 'task', { includeVerifier: true })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('agent_id=jim')
    expect(url).toContain('type=task')
    expect(url).toContain('include_verifier=true')
  })
})
