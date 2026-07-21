// ADR-052 Wave 2 — session-list `include_verifier` param contract tests
// (agent "qa-spa").
//
// FR-036: verifier-role sessions are excluded by default from
// `GET /api/v1/sessions` unless `include_verifier=true` is passed. Sidebar
// and SearchModal MUST NOT pass it (exclude verifier sessions); UsageScreen
// MUST pass it (verifier LLM spend visible in cost reporting, SC-014).
//
// The backend contract side of this is already regenerated and confirmed
// present: `contracts/openapi.yaml:1012` defines the `include_verifier` query
// param on GET /sessions, and `src/lib/api/generated/schemas.ts:5704`/
// `openapi-types.ts:8781` carry it through. What was checked (via
// `grep -n "fetchSessions"` across src/lib/api.ts and every caller)
// immediately before writing this file:
//   `fetchSessions(agentId?: string, type?: Session['type'])` — src/lib/api.ts:1360
//   has NO includeVerifier parameter at all, and every current caller
//   (Sidebar.tsx:165, SearchModal.tsx:358, UsageScreen.tsx:248) calls
//   `fetchSessions()` with zero args — UsageScreen is IDENTICAL to
//   Sidebar/SearchModal today, so verifier LLM spend is NOT yet visible in
//   Usage (FR-036/SC-014 not yet satisfied client-side; this is squarely
//   frontend-lead's remaining wave, not a qa-lead fix).
//
// Test 1 below is real and passing: it locks in the (currently trivially
// true) invariant that fetchSessions never sends include_verifier — so if a
// future edit adds the param but wires the default backwards, this test
// starts failing loudly. Test 2 (the UsageScreen opt-in path) is `it.todo`
// rather than a real assertion, because there is no `includeVerifier`
// parameter to call yet — writing a real call would either not type-check
// (extra argument) or silently test nothing (calling with no args can't
// prove an opt-in path that doesn't exist). Flagged honestly rather than
// faked.
//
// STRICT OWNERSHIP: new file only; does not modify src/lib/api.ts or any
// existing test.
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

describe('ADR-052 FR-036 — fetchSessions never sends include_verifier today (Sidebar/SearchModal/UsageScreen all identical pre-wave)', () => {
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

  // BLOCKED / TODO: once fetchSessions grows an includeVerifier param (or a
  // dedicated fetchSessionsWithVerifierSpend()-style export) so UsageScreen
  // can request `include_verifier=true` while Sidebar/SearchModal keep
  // omitting it, write these as real assertions:
  it.todo('fetchSessions(..., { includeVerifier: true }) sends include_verifier=true in the query string')
  it.todo('UsageScreen.tsx calls fetchSessions with includeVerifier:true (verifier LLM spend visible, SC-014)')
  it.todo('Sidebar.tsx and SearchModal.tsx still call fetchSessions with includeVerifier omitted/false after the wave lands')
})
