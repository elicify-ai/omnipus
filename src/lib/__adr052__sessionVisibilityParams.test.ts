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
//   `fetchSessions(agentId?, type?, opts?: FetchSessionsOptions)`
//   — src/lib/api.ts — grew a 3rd, additive-only `opts` param.
//   Sidebar.tsx and SearchModal.tsx still call `fetchSessions()` with zero
//   args (unchanged — verifier sessions stay excluded there by construction,
//   since `opts` is simply undefined, and they get the default roots-only
//   page). UsageScreen.tsx's "By session" tab now calls
//   `fetchSessions(undefined, undefined, { includeVerifier: true, flat: true })`,
//   so individual verifier session rows AND delegated-child rows are listed
//   there (see UsageScreen.test.tsx). SC-014 was ALREADY partially
//   satisfied before this wave — the hero/by-agent/by-model views derive
//   from GET /token-stats, which aggregates every session type unfiltered —
//   this wave closes the per-session ROW list gap, so FR-036/SC-014 are now
//   fully satisfied client-side.
//
// ADR-057 US-19/FR-091/FR-092/FR-098/FR-104 (W16d/W16h, U12): `opts` grew
// four more fields — parentSessionId, flat, limit, offset — added below
// without touching the six pre-existing include_verifier cases' assertions
// (only the mocked response shape changes, from a bare array to the new
// SessionPage envelope {sessions, next_cursor?, partial_errors?} that
// replaced the retired two-variant oneOf — FR-091/grill2 M2-10).
//
// All tests below are real assertions — no it.todo remains in this file.
//
// STRICT OWNERSHIP: this file, src/lib/api.ts, src/components/screens/
// UsageScreen.tsx(.test.tsx) only. Does not touch Sidebar.tsx/SearchModal.tsx
// (their zero-arg call sites needed no code change) or their tests.
//
// Traces to: docs/internal/specs/autonomous-agent-plan-execution-spec.md
//   FR-036 (line 692), SC-014 (line 713), US-13 Acceptance 6 (line 212),
//   BDD "verifier sessions are hidden by default but auditable" (line 438);
//   docs/internal/specs/adr-057-session-unification-spec.md FR-091, FR-092,
//   FR-098, FR-104.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

function makeOkResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

// ADR-057 FR-091: GET /sessions always returns the named SessionPage
// envelope now — a bare array is no longer a valid response shape, so every
// mock below wraps its rows in {sessions: [...]}.
function makeSessionPageResponse(sessions: unknown[] = [], extra?: { next_cursor?: string; partial_errors?: string[] }): Response {
  return makeOkResponse({ sessions, ...extra })
}

describe('ADR-052 FR-036 — fetchSessions() include_verifier opt-in (default omitted, explicit true sent)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(makeSessionPageResponse())
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

// ADR-057 US-19/FR-091/FR-092/FR-098/FR-104 (W16d/W16h) — the four new
// query params fetchSessions() grew to support nested session listing,
// paging, and the flat usage-accounting view.
describe('ADR-057 — fetchSessions() paging/hierarchy params (parent_session_id, flat, limit, offset)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(makeSessionPageResponse())
    vi.stubGlobal('fetch', fetchSpy)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetchSessions() with no args sends none of parent_session_id/flat/limit/offset — default roots-only, first page', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions()

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).not.toContain('parent_session_id')
    expect(url).not.toContain('flat')
    expect(url).not.toContain('limit')
    expect(url).not.toContain('offset')
  })

  it('fetchSessions(..., { parentSessionId }) sends parent_session_id in the query string (FR-091/US-19)', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { parentSessionId: 'parent-abc' })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('parent_session_id=parent-abc')
    expect(url).not.toContain('flat')
  })

  it('fetchSessions(..., { flat: true }) sends flat=true (FR-104)', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { flat: true })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('flat=true')
  })

  it('fetchSessions(..., { flat: false }) omits flat — an explicit false is still the roots-only default', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { flat: false })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).not.toContain('flat')
  })

  it('fetchSessions(..., { limit, offset }) sends both as numeric query params, including offset:0 explicitly (FR-092/FR-098)', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { limit: 20, offset: 0 })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('limit=20')
    // offset:0 is a meaningful explicit first-page value, not "omitted" —
    // must not be dropped the way a falsy check would drop it.
    expect(url).toContain('offset=0')
  })

  it('fetchSessions(..., { parentSessionId, flat: true }) passes both through — the server enforces the 400 for this combination, the client does not guess at it', async () => {
    const { fetchSessions } = await import('@/lib/api')
    await fetchSessions(undefined, undefined, { parentSessionId: 'parent-abc', flat: true })

    const [url] = fetchSpy.mock.calls[0] as [string]
    expect(url).toContain('parent_session_id=parent-abc')
    expect(url).toContain('flat=true')
  })

  it('parses next_cursor and partial_errors off the SessionPage envelope via fetchSessionPage (FR-098)', async () => {
    fetchSpy.mockResolvedValue(
      makeSessionPageResponse([], { next_cursor: '20', partial_errors: ['agent=x: session_list_failed'] }),
    )
    const { fetchSessionPage } = await import('@/lib/api')
    const page = await fetchSessionPage(undefined, undefined, { limit: 20 })

    expect(page.nextCursor).toBe('20')
    expect(page.partialErrors).toEqual(['agent=x: session_list_failed'])
    expect(page.sessions).toEqual([])
  })
})
