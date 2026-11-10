/**
 * http.planted-cookie-get.test.ts — #950 review F5 (frontend half), the GET
 * side of ADR-094 planted-cookie recovery.
 *
 * Spec: docs/internal/specs/adr-094-preview-isolation-spec.md — UI-states row
 * "Planted cookie cleared" and the typed-error bullet under FR-015: a planted
 * duplicate on a GET is answered 401 with the clear lines; the SPA raises
 * exactly ONE toast (the gateway's human message + a Retry action) that
 * re-issues the same request ONCE when the user clicks it; request() never
 * auto-retries; a failed retry never stacks a second toast; a burst collapses
 * into one toast.
 *
 * Wire shape (agreed with the backend fix for F5): 401 with the standard
 * error envelope { code, error, message }, code planted_cookie_cleared. The
 * existing http.planted-cookie.test.ts pins only the 403 POST shape; the
 * review found no test that drives a GET 401 through request().
 *
 * Seam: only fetch is replaced (the process edge); request(), ApiError and the
 * real toast store run as in the app.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const PLANTED_401_BODY = {
  code: 'planted_cookie_cleared',
  error: 'planted_cookie_cleared',
  message: 'Omnipus cleared cookies set by a preview — please retry',
}

const GET_PATH = '/agents'

function planted401(): Response {
  return new Response(JSON.stringify(PLANTED_401_BODY), {
    status: 401,
    headers: { 'Content-Type': 'application/json' },
  })
}

function plain401(): Response {
  return new Response(JSON.stringify({ error: 'unauthorized: missing Bearer token' }), {
    status: 401,
    headers: { 'Content-Type': 'application/json' },
  })
}

function ok200(): Response {
  return new Response(JSON.stringify([{ id: 'a1' }]), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

type ToastArg = { message: string; variant?: string; action?: { label: string; onClick: () => void } }

// Fresh module instances per test: the 5 s dedup timestamp is module state in
// http.ts, so sharing one instance would let one test's toast mute the next.
let request: typeof import('./http')['request']
let ApiErrorCls: typeof import('@/lib/api-error')['ApiError']
let useUiStore: typeof import('@/store/ui')['useUiStore']
let toastSpy: ReturnType<typeof vi.fn<(t: ToastArg) => void>>

beforeEach(async () => {
  vi.resetModules()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(200_000)
  request = (await import('./http')).request
  ApiErrorCls = (await import('@/lib/api-error')).ApiError
  useUiStore = (await import('@/store/ui')).useUiStore
  toastSpy = vi.fn<(t: ToastArg) => void>()
  useUiStore.setState({ toasts: [], addToast: toastSpy as never })
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function stubFetch(...replies: Array<() => Response>) {
  let i = 0
  const fetchSpy = vi.fn(async () => {
    const make = replies[Math.min(i, replies.length - 1)]
    i++
    return make()
  })
  vi.stubGlobal('fetch', fetchSpy)
  return fetchSpy
}

function urlOf(call: unknown[]): string {
  const input = call[0]
  return typeof input === 'string' ? input : input instanceof URL ? input.href : (input as Request).url
}

async function settle(): Promise<void> {
  // Let the Retry action's un-awaited re-issue run to completion.
  for (let n = 0; n < 10; n++) await Promise.resolve()
  await new Promise((r) => setTimeout(r, 0))
}

describe('planted-cookie GET 401 => one Retry toast (F5, FR-015 GET side)', () => {
  it('raises exactly ONE toast with the gateway message and a Retry action, and the error keeps code + status', async () => {
    const fetchSpy = stubFetch(planted401)
    let caught: unknown
    try {
      await request(GET_PATH)
    } catch (e) {
      caught = e
    }
    expect(caught).toBeInstanceOf(ApiErrorCls)
    expect((caught as InstanceType<typeof ApiErrorCls>).status).toBe(401)
    expect((caught as InstanceType<typeof ApiErrorCls>).code).toBe('planted_cookie_cleared')

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(toastSpy).toHaveBeenCalledTimes(1)
    const toast = toastSpy.mock.calls[0][0]
    expect(toast.message).toBe('Omnipus cleared cookies set by a preview — please retry')
    expect(toast.variant).toBe('error')
    expect(toast.action?.label).toBe('Retry')
  })

  it('never auto-retries: without a click the GET is sent exactly once', async () => {
    const fetchSpy = stubFetch(planted401, ok200)
    await expect(request(GET_PATH)).rejects.toBeInstanceOf(ApiErrorCls)
    await settle()
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })

  it('Retry re-issues the SAME GET exactly once', async () => {
    const fetchSpy = stubFetch(planted401, ok200)
    await expect(request(GET_PATH)).rejects.toBeInstanceOf(ApiErrorCls)
    const action = toastSpy.mock.calls[0][0].action
    if (!action) throw new Error('Retry action missing')
    action.onClick()
    await settle()

    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(urlOf(fetchSpy.mock.calls[1] as unknown[])).toBe('/api/v1' + GET_PATH)
    const init = (fetchSpy.mock.calls[1] as unknown[])[1] as RequestInit | undefined
    expect((init?.method ?? 'GET').toUpperCase()).toBe('GET')
    expect(init?.body).toBeUndefined()
    expect(toastSpy).toHaveBeenCalledTimes(1)
  })

  it('a failed Retry (the preview re-planted) raises no second toast and sends nothing further', async () => {
    const fetchSpy = stubFetch(planted401, planted401, planted401)
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    await expect(request(GET_PATH)).rejects.toBeInstanceOf(ApiErrorCls)
    const action = toastSpy.mock.calls[0][0].action
    if (!action) throw new Error('Retry action missing')
    action.onClick()
    await settle()

    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(toastSpy).toHaveBeenCalledTimes(1)
  })

  it('a parallel burst of planted GETs (refetch storm) collapses into ONE toast; every call still rejects typed', async () => {
    const fetchSpy = stubFetch(planted401)
    const results = await Promise.allSettled([request('/agents'), request('/sessions'), request('/state')])
    expect(fetchSpy).toHaveBeenCalledTimes(3)
    for (const r of results) {
      expect(r.status).toBe('rejected')
      const reason = (r as PromiseRejectedResult).reason as InstanceType<typeof ApiErrorCls>
      expect(reason.status).toBe(401)
      expect(reason.code).toBe('planted_cookie_cleared')
    }
    expect(toastSpy).toHaveBeenCalledTimes(1)
  })

  it('control: an ordinary 401 behaves as before — no toast, no code, session-expired message, sent once', async () => {
    const fetchSpy = stubFetch(plain401)
    let caught: unknown
    try {
      await request(GET_PATH)
    } catch (e) {
      caught = e
    }
    expect(caught).toBeInstanceOf(ApiErrorCls)
    const err = caught as InstanceType<typeof ApiErrorCls>
    expect(err.status).toBe(401)
    expect(err.code).toBeUndefined()
    expect(err.userMessage).toBe('Your session has expired. Please log in again.')
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(toastSpy).not.toHaveBeenCalled()
  })
})
