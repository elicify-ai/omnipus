/**
 * api.channel-instance.test.ts
 *
 * Unit tests for the channel-instance CRUD API client functions:
 *   - createChannelInstance(body): POST /api/v1/channels
 *   - deleteChannelInstance(id): DELETE /api/v1/channels/{id}
 *
 * Traces to: channel-instance-workspace-binding-spec.md US-6 / US-10 / US-11
 *
 * Test groups:
 *   1. createChannelInstance — valid slug → POST fires with {type, slug}; returns 201
 *   2. createChannelInstance — 409 conflict → rejects with ApiError status 409
 *   3. createChannelInstance — 400 bad slug → rejects with ApiError status 400
 *   4. deleteChannelInstance — confirm → DELETE fires for the correct id
 *   5. deleteChannelInstance — 404 already gone → rejects with ApiError status 404
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// ── Helpers ────────────────────────────────────────────────────────────────────

function stubCookie(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCookie() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

function makeJsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function makeEmptyResponse(status: number): Response {
  return new Response(null, { status })
}

// ── Test setup ─────────────────────────────────────────────────────────────────

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchSpy = vi.fn()
  vi.stubGlobal('fetch', fetchSpy)
  // Provide a valid CSRF cookie so state-changing calls pass the CSRF gate.
  stubCookie('__Host-csrf=test-csrf-token')
  // Provide a bearer token so auth headers are populated.
  sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  sessionStorage.clear()
  restoreCookie()
})

// ── createChannelInstance ─────────────────────────────────────────────────────

describe('createChannelInstance', () => {
  it('sends POST /api/v1/channels with {type, slug} and returns the created instance', async () => {
    // BDD: Given a valid type + slug,
    // When createChannelInstance({type: 'whatsapp', slug: 'eu'}) is called,
    // Then POST /api/v1/channels is requested with the correct body,
    // And the returned object has id="whatsapp.eu", type="whatsapp", enabled=false.
    // Traces to: US-6 / AC-1 — create N instances per type
    const responsePayload = {
      id: 'whatsapp.eu',
      type: 'whatsapp',
      enabled: false,
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(responsePayload, 201))

    const { createChannelInstance } = await import('./api')
    const result = await createChannelInstance({ type: 'whatsapp', slug: 'eu' })

    // Verify POST was sent to the correct URL
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toMatch(/\/api\/v1\/channels$/)
    expect(init.method).toBe('POST')

    // Verify the request body
    const sentBody = JSON.parse(init.body as string) as unknown
    expect(sentBody).toEqual({ type: 'whatsapp', slug: 'eu' })

    // Verify the response shape
    expect(result.id).toBe('whatsapp.eu')
    expect(result.type).toBe('whatsapp')
    expect(result.enabled).toBe(false)
  })

  it('sends POST with hyphenated slug and type', async () => {
    // BDD: Given a slug with hyphens (legal per FR-017),
    // When createChannelInstance({type: 'telegram', slug: 'main-bot'}) is called,
    // Then POST fires with {type: 'telegram', slug: 'main-bot'}.
    // Traces to: DS-2 row 5 — hyphen legal in slug
    const responsePayload = {
      id: 'telegram.main-bot',
      type: 'telegram',
      enabled: false,
    }
    fetchSpy.mockResolvedValueOnce(makeJsonResponse(responsePayload, 201))

    const { createChannelInstance } = await import('./api')
    const result = await createChannelInstance({ type: 'telegram', slug: 'main-bot' })

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const sentBody = JSON.parse(init.body as string) as unknown
    expect(sentBody).toEqual({ type: 'telegram', slug: 'main-bot' })
    expect(result.id).toBe('telegram.main-bot')
  })

  it('rejects with ApiError status 409 when the instance key already exists', async () => {
    // BDD: Given an instance "whatsapp.eu" already exists,
    // When createChannelInstance({type: 'whatsapp', slug: 'eu'}) is called,
    // Then the backend returns 409 and the client rejects with ApiError{status: 409}.
    // Traces to: US-6 — 409 conflict error path
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse({ error: 'instance key whatsapp.eu already exists' }, 409),
    )

    const { createChannelInstance, isApiError } = await import('./api')
    await expect(createChannelInstance({ type: 'whatsapp', slug: 'eu' })).rejects.toSatisfy(
      (err: unknown) => isApiError(err) && (err as { status: number }).status === 409,
    )
  })

  it('rejects with ApiError status 400 when the type is unknown or slug is malformed', async () => {
    // BDD: Given an unknown channel type,
    // When createChannelInstance({type: 'unknown', slug: 'x'}) is called,
    // Then the backend returns 400 and the client rejects with ApiError{status: 400}.
    // Traces to: US-11 — 400 for malformed/unknown inputs
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse({ error: 'unknown channel type: unknown' }, 400),
    )

    const { createChannelInstance, isApiError } = await import('./api')
    await expect(createChannelInstance({ type: 'unknown', slug: 'x' })).rejects.toSatisfy(
      (err: unknown) => isApiError(err) && (err as { status: number }).status === 400,
    )
  })
})

// ── deleteChannelInstance ─────────────────────────────────────────────────────

describe('deleteChannelInstance', () => {
  it('sends DELETE /api/v1/channels/{id} and resolves on 204', async () => {
    // BDD: Given a confirmed delete for "whatsapp.eu",
    // When deleteChannelInstance('whatsapp.eu') is called,
    // Then DELETE /api/v1/channels/whatsapp.eu is requested.
    // Traces to: US-10 / AC-2 — instance delete removes config+creds+store
    fetchSpy.mockResolvedValueOnce(makeEmptyResponse(204))

    const { deleteChannelInstance } = await import('./api')
    await expect(deleteChannelInstance('whatsapp.eu')).resolves.toBeUndefined()

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toMatch(/\/api\/v1\/channels\/whatsapp\.eu$/)
    expect(init.method).toBe('DELETE')
  })

  it('URL-encodes the instance id in the DELETE path', async () => {
    // BDD: Given an instance id with a dot (the standard namespaced format),
    // When deleteChannelInstance('telegram.main-bot') is called,
    // Then the URL path encodes it correctly.
    // Traces to: US-11 — endpoint accepts per-instance IDs
    fetchSpy.mockResolvedValueOnce(makeEmptyResponse(204))

    const { deleteChannelInstance } = await import('./api')
    await deleteChannelInstance('telegram.main-bot')

    const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
    // encodeURIComponent('telegram.main-bot') = 'telegram.main-bot' (dot is not encoded)
    expect(url).toMatch(/\/api\/v1\/channels\/telegram\.main-bot$/)
  })

  it('rejects with ApiError status 404 when the instance is already gone', async () => {
    // BDD: Given an instance that was already deleted,
    // When deleteChannelInstance('whatsapp.eu') is called,
    // Then the backend returns 404 and the client rejects with ApiError{status: 404}.
    // Traces to: US-10 — 404 handled gracefully (already gone → refresh)
    fetchSpy.mockResolvedValueOnce(
      makeJsonResponse({ error: 'instance not found: whatsapp.eu' }, 404),
    )

    const { deleteChannelInstance, isApiError } = await import('./api')
    await expect(deleteChannelInstance('whatsapp.eu')).rejects.toSatisfy(
      (err: unknown) => isApiError(err) && (err as { status: number }).status === 404,
    )
  })

  it('sends DELETE to correct URL for a bare-type (legacy) channel id', async () => {
    // BDD: Given a legacy single-instance channel key "telegram",
    // When deleteChannelInstance('telegram') is called,
    // Then DELETE /api/v1/channels/telegram is requested.
    // Traces to: DS-2 row 7 — bare type = legacy single instance
    fetchSpy.mockResolvedValueOnce(makeEmptyResponse(204))

    const { deleteChannelInstance } = await import('./api')
    await deleteChannelInstance('telegram')

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toMatch(/\/api\/v1\/channels\/telegram$/)
    expect(init.method).toBe('DELETE')
  })
})
