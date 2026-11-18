/**
 * http.planted-cookie.test.ts — RED test, ADR-094 TDD Plan order 27
 * (TestPlantedCookieRetryToast, round-2 MAJ-008).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md
 *   S-4.2 + FR-015(4) + the typed-error bullet (MAJ-008): an ApiError with
 *   code planted_cookie_cleared (thrown in src/lib/api/http.ts) raises
 *   exactly ONE toast via src/store/ui.ts::addToast carrying the human
 *   message and a Retry action (Toast.action) that re-issues the same
 *   request once. Duplicate toasts for one event FAIL the test.
 *
 * INSTRUMENT NOTE (judgment call): the order is filed as "Component (vitest)"
 * but the spec pins the CHAIN ENDPOINTS (ApiError source: http.ts::request;
 * toast sink: ui.ts::addToast), not an internal home for the trigger. The
 * test drives the real request() against a mocked fetch — the process edge —
 * and asserts the full chain: typed ApiError, ONE toast, Retry action,
 * single re-issue of the identical request. If GREEN homes the toast raise
 * inside request() itself, this test exercises it directly; if GREEN homes
 * it in a request()-adjacent wrapper, the wrapper must be on the path this
 * call exercises (the same path every JSON API caller rides).
 *
 * RED (current failure mode): zero toasts — no SPA mechanism connects a
 * planted_cookie_cleared ApiError to addToast today. The fetch re-issue
 * assertions (exactly one retry) are pins that must hold post-GREEN.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { request, CSRF_HEADER_NAME } from './http'
import { ApiError } from '@/lib/api-error'
import { useUiStore } from '@/store/ui'

const ENVELOPE_BODY = {
  code: 'planted_cookie_cleared',
  error: 'planted_cookie_cleared',
  // FR-015(4)/S-4.2 verbatim message:
  message: 'Omnipus cleared cookies set by a preview — please retry',
}

const PATH = '/library/ws-attack-evidence/mkdir'

async function callOnce(): Promise<void> {
  await request(PATH, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', [CSRF_HEADER_NAME]: 'test-csrf' },
    body: JSON.stringify({ path: 'attack-evidence-dir' }),
  })
}

beforeEach(() => {
  // request()\'s client-side CSRF gate: state-changing calls need a csrf
  // cookie PRESENT (value irrelevant to the gate). Set one before each call.
  document.cookie = 'csrf=retry-test-e2e; path=/'
  useUiStore.setState({ toasts: [] })
})

describe('planted-cookie typed error => ONE retry toast (order 27, S-4.2)', () => {
  it('raises exactly ONE addToast carrying the human message and a Retry action', async () => {
    const toastSpy = vi.fn()
    useUiStore.setState({ addToast: toastSpy })

    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(JSON.stringify(ENVELOPE_BODY), {
          status: 403,
          headers: { 'Content-Type': 'application/json' },
        }),
      )

    await expect(callOnce()).rejects.toBeInstanceOf(ApiError)
    expect(fetchSpy).toHaveBeenCalledTimes(1)

    expect(toastSpy).toHaveBeenCalledTimes(1)
    const toast = toastSpy.mock.calls[0][0] as {
      message: string
      action?: { label: string; onClick: () => void }
    }
    expect(toast.message).toContain(
      'Omnipus cleared cookies set by a preview — please retry',
    )
    const action = toast.action
    expect(action, 'S-4.2: the toast carries a Retry action').toBeDefined()
    if (!action) throw new Error('unreachable: action asserted defined')
    expect(action.label).toMatch(/retry/i)

    // Invoke the Retry action — it must re-issue THE SAME request exactly
    // once: the wire saw the original call plus ONE identical re-issue.
    await action.onClick()
    expect(
      fetchSpy,
      'S-4.2: Retry re-issues the request ONCE (original + one re-issue)',
    ).toHaveBeenCalledTimes(2)
    const calls = fetchSpy.mock.calls
    expect(calls).toHaveLength(2)
    const [input, init] = calls[1]
    const url =
      typeof input === 'string'
        ? input
        : input instanceof URL
          ? input.href
          : input.url
    expect(url).toBe('/api/v1' + PATH)
    expect(init?.method).toBe('POST')
    expect(init?.body).toBe(JSON.stringify({ path: 'attack-evidence-dir' }))

    // S-4.2: exactly one toast for the event — invoking Retry must NOT
    // stack a second toast (duplicate toasts fail the test).
    expect(toastSpy).toHaveBeenCalledTimes(1)
    fetchSpy.mockRestore()
  })

  it('control: a non-planted 403 envelope raises NO toast (typed mechanism must be selective)', async () => {
    const toastSpy = vi.fn()
    useUiStore.setState({ addToast: toastSpy })
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(JSON.stringify({ code: 'other_code', message: 'nope' }), {
          status: 403,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    await expect(callOnce()).rejects.toBeInstanceOf(ApiError)
    expect(toastSpy).not.toHaveBeenCalled()
    fetchSpy.mockRestore()
  })

  it('control: the error carries code planted_cookie_cleared for callers to branch on', async () => {
    useUiStore.setState({ addToast: vi.fn() })
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(JSON.stringify(ENVELOPE_BODY), {
          status: 403,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    let caught: unknown = null
    try {
      await callOnce()
    } catch (e) {
      caught = e
    }
    expect(caught).toBeInstanceOf(ApiError)
    expect((caught as ApiError).status).toBe(403)
    expect((caught as ApiError).code).toBe('planted_cookie_cleared')
    fetchSpy.mockRestore()
  })
})

/**
 * Wave-2 PTA-3 (fix round 5): the 5 s dedup window on the planted-cookie
 * toast. Spec source: the UI-states row + order 27 ("exactly ONE toast per
 * event — a parallel burst of planted failures collapses into one toast via
 * a 5 s dedup window"). Two properties pinned: a rapid burst of planted
 * failures raises exactly ONE toast, and a failure AFTER the window raises
 * a second (the window re-arms — it is not a permanent mute).
 *
 * Isolation note (no timestamp leaks in either direction): the dedup
 * timestamp is module-level state in http.ts. These tests drive FRESH
 * module instances (vi.resetModules + dynamic imports), so the timestamp
 * neither inherits the real-clock toasts the earlier tests raised nor
 * suppresses them afterwards. The fresh module's timestamp starts at 0, so
 * the faked clock starts at 100_000 ms — far enough past 0 for the first
 * raise to clear the window, far enough from any real clock to never
 * collide.
 */
describe('planted-cookie toast 5 s dedup window (wave-2 PTA-3)', () => {
  // Fresh instances — rebound per test by the beforeEach below. The thrown
  // error is an instance of the FRESH ApiError class (resetModules
  // re-evaluates the class), so instanceof must use the fresh binding too.
  let freshRequest: typeof import('./http')['request']
  let FreshApiError: typeof import('@/lib/api-error')['ApiError']
  let freshUiStore: typeof import('@/store/ui')['useUiStore']

  beforeEach(async () => {
    vi.resetModules()
    const httpModule = await import('./http')
    const apiErrorModule = await import('@/lib/api-error')
    const uiModule = await import('@/store/ui')
    freshRequest = httpModule.request
    FreshApiError = apiErrorModule.ApiError
    freshUiStore = uiModule.useUiStore
  })

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(100_000)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  async function plantedCall(): Promise<never> {
    return freshRequest(PATH, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', [CSRF_HEADER_NAME]: 'test-csrf' },
      body: JSON.stringify({ path: 'attack-evidence-dir' }),
    }) as Promise<never>
  }

  function stubPlantedFetch(): ReturnType<typeof vi.spyOn> {
    // A FRESH Response per call: a Response body can be consumed only once,
    // so a shared instance would leave the second/third planted failure with
    // an unreadable body and an untyped error — not the failure shape the
    // dedup window is specified against (each real planted failure carries
    // its own envelope).
    return vi
      .spyOn(globalThis, 'fetch')
      .mockImplementation(() =>
        Promise.resolve(
          new Response(JSON.stringify(ENVELOPE_BODY), {
            status: 403,
            headers: { 'Content-Type': 'application/json' },
          }),
        ),
      )
  }

  it('collapses a rapid burst into ONE toast, then re-arms after the 5 s window', async () => {
    const toastSpy = vi.fn()
    freshUiStore.setState({ addToast: toastSpy })
    const fetchSpy = stubPlantedFetch()

    // Phase 1 — the burst: two rapid planted failures from a refetch storm.
    const first = plantedCall()
    const second = plantedCall()
    await expect(first).rejects.toBeInstanceOf(FreshApiError)
    await expect(second).rejects.toBeInstanceOf(FreshApiError)
    expect(toastSpy, 'a rapid burst of planted failures collapses into exactly ONE toast').toHaveBeenCalledTimes(1)
    expect(toastSpy.mock.calls[0][0].message).toContain(
      'Omnipus cleared cookies set by a preview — please retry',
    )

    // Phase 2 — after the window: the dedup must RE-ARM (it mutes a burst,
    // it is not a permanent mute). 5_001 ms past the first toast.
    vi.advanceTimersByTime(5_001)
    await expect(plantedCall()).rejects.toBeInstanceOf(FreshApiError)
    expect(toastSpy, 'a planted failure AFTER the dedup window raises a second toast').toHaveBeenCalledTimes(2)
    expect(toastSpy.mock.calls[1][0].message).toContain(
      'Omnipus cleared cookies set by a preview — please retry',
    )

    fetchSpy.mockRestore()
  })

  it('control: every burst call still rejects with the typed planted error (dedup mutes the toast, not the error)', async () => {
    const toastSpy = vi.fn()
    freshUiStore.setState({ addToast: toastSpy })
    const fetchSpy = stubPlantedFetch()

    const first = plantedCall()
    const second = plantedCall()
    let firstCode: unknown
    let secondCode: unknown
    await first.catch((e: unknown) => {
      firstCode = (e as { code?: unknown }).code
    })
    await second.catch((e: unknown) => {
      secondCode = (e as { code?: unknown }).code
    })

    expect(firstCode).toBe('planted_cookie_cleared')
    expect(secondCode).toBe('planted_cookie_cleared')
    expect(toastSpy).toHaveBeenCalledTimes(1)
    fetchSpy.mockRestore()
  })
})
