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

import { beforeEach, describe, expect, it, vi } from 'vitest'
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
