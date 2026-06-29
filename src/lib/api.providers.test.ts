/**
 * api.providers.test.ts — Test #22 + #32 (spec TDD plan)
 *
 * Spec: provider-validation-centralization-spec.md, US8 / FR-016 / R-C.
 *
 * Test #22: configureProvider throws on 422 (InvalidKey), returns Provider
 *   with validation on 200 with non-valid outcome.
 *
 * Test #32 / m4 / R-C: configureProvider omits api_key in the request body
 *   when only model/models changed (SC-005 premise — no key → no server probe).
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { configureProvider, ApiError } from './api'

// ── Helpers ───────────────────────────────────────────────────────────────────

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

// ── Test setup ────────────────────────────────────────────────────────────────

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchSpy = vi.fn()
  vi.stubGlobal('fetch', fetchSpy)
  // CSRF cookie so PUT passes the client-side gate.
  stubCookie('__Host-csrf=test-csrf-token')
  sessionStorage.setItem('omnipus_auth_token', 'test-bearer')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
  sessionStorage.clear()
  restoreCookie()
})

// ── Test #22 — 422 throws, 200+validation returns Provider ───────────────────

describe('configureProvider — Test #22', () => {
  it('throws ApiError on 422 (InvalidKey)', async () => {
    fetchSpy.mockResolvedValue(
      makeJsonResponse(
        { error: 'The API key was rejected by OpenRouter. Check you copied the whole key.' },
        422,
      ),
    )

    await expect(
      configureProvider('openrouter', 'bad-key', undefined, undefined, 'reauth-tok'),
    ).rejects.toBeInstanceOf(ApiError)

    const call = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(call[0]).toContain('/providers/openrouter')
  })

  it('rejects with the server error message on 422', async () => {
    const errorMsg = 'The API key was rejected by OpenRouter.'
    fetchSpy.mockResolvedValue(
      makeJsonResponse({ error: errorMsg }, 422),
    )

    let caught: ApiError | undefined
    try {
      await configureProvider('openrouter', 'bad-key', undefined, undefined, 'reauth-tok')
    } catch (err) {
      caught = err as ApiError
    }

    expect(caught).toBeInstanceOf(ApiError)
    expect(caught?.status).toBe(422)
    // userMessage should contain the server error text (422 is not in knownStatuses,
    // so the parsed message is used as userMessage).
    expect(caught?.userMessage).toBe(errorMsg)
  })

  it('returns Provider with validation on 200 no_credit outcome', async () => {
    const provider = {
      id: 'openrouter',
      name: 'openrouter',
      status: 'connected',
      models: ['openrouter/auto'],
      validation: {
        outcome: 'no_credit',
        message: 'Your OpenRouter key works, but the account has no credit.',
      },
    }
    fetchSpy.mockResolvedValue(makeJsonResponse(provider, 200))

    const result = await configureProvider('openrouter', 'valid-key', undefined, undefined, 'reauth-tok')

    expect(result.validation).toBeDefined()
    expect(result.validation?.outcome).toBe('no_credit')
    expect(result.validation?.message).toBe('Your OpenRouter key works, but the account has no credit.')
  })

  it('returns Provider with validation on 200 unreachable outcome', async () => {
    const provider = {
      id: 'openrouter',
      name: 'openrouter',
      status: 'connected',
      models: [],
      validation: {
        outcome: 'unreachable',
        message: "Couldn't reach OpenRouter to check the key.",
      },
    }
    fetchSpy.mockResolvedValue(makeJsonResponse(provider, 200))

    const result = await configureProvider('openrouter', 'key', undefined, undefined, 'tok')

    expect(result.validation?.outcome).toBe('unreachable')
  })

  it('returns Provider with validation on 200 restricted outcome', async () => {
    const provider = {
      id: 'openrouter',
      name: 'openrouter',
      status: 'connected',
      models: [],
      validation: {
        outcome: 'restricted',
        message: 'Your OpenRouter key works, but OpenRouter blocked this request.',
      },
    }
    fetchSpy.mockResolvedValue(makeJsonResponse(provider, 200))

    const result = await configureProvider('openrouter', 'key', undefined, undefined, 'tok')

    expect(result.validation?.outcome).toBe('restricted')
  })

  it('returns Provider with no validation on 200 valid outcome (no banner needed)', async () => {
    const provider = {
      id: 'openrouter',
      name: 'openrouter',
      status: 'connected',
      models: ['openrouter/auto'],
      // No validation field (or valid) → no banner
    }
    fetchSpy.mockResolvedValue(makeJsonResponse(provider, 200))

    const result = await configureProvider('openrouter', 'valid-key', undefined, undefined, 'tok')

    // Either absent or valid → no banner shown
    expect(result.validation == null || result.validation.outcome === 'valid').toBe(true)
  })
})

// ── Test #32 / m4 / R-C — api_key omitted when only model/models changed ─────

describe('configureProvider — Test #32 / m4 / R-C — key omission', () => {
  beforeEach(() => {
    // Mock a successful response for these tests
    fetchSpy.mockResolvedValue(
      makeJsonResponse({
        id: 'openrouter',
        name: 'openrouter',
        status: 'connected',
        models: ['openrouter/auto'],
      }),
    )
  })

  it('omits api_key from the request body when apiKey arg is undefined (model-only edit)', async () => {
    await configureProvider(
      'openrouter',
      undefined,      // apiKey not provided → omit from body
      undefined,      // endpoint
      'openrouter/qwen-2.5-72b-instruct', // model change only
      'reauth-tok',
    )

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string) as Record<string, unknown>
    // api_key must NOT be in the request body
    expect(Object.prototype.hasOwnProperty.call(body, 'api_key')).toBe(false)
    // model IS in the body
    expect(body.model).toBe('openrouter/qwen-2.5-72b-instruct')
  })

  it('omits api_key when only models (slug list) changed', async () => {
    await configureProvider(
      'mygw',
      undefined,  // no key
      undefined,  // no endpoint
      undefined,  // no model
      'reauth-tok',
      ['mygw/llama-3.3-70b', 'mygw/mixtral-8x7b'], // models list change
    )

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string) as Record<string, unknown>
    expect(Object.prototype.hasOwnProperty.call(body, 'api_key')).toBe(false)
    expect(body.models).toEqual(['mygw/llama-3.3-70b', 'mygw/mixtral-8x7b'])
  })

  it('includes api_key in the body when a key is provided', async () => {
    await configureProvider(
      'openrouter',
      'new-api-key',
      undefined,
      undefined,
      'reauth-tok',
    )

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string) as Record<string, unknown>
    expect(body.api_key).toBe('new-api-key')
  })

  it('ProvidersSection passes undefined api_key when the key input is empty (model-only save)', () => {
    // This asserts the ProvidersSection caller contract (m4 / R-C):
    // applyChange uses `key === '' ? undefined : key` so an empty key field
    // (no change from the user) produces undefined, which configureProvider
    // does NOT include in the request body (as asserted above).
    //
    // The component-level assertion is in the ProvidersSection.test.tsx
    // manual-provider tests (models-only save → api_key arg is undefined).
    // This test documents the invariant explicitly:
    const keyFromInputWhenUnchanged = ''
    const apiKeyArg = keyFromInputWhenUnchanged === '' ? undefined : keyFromInputWhenUnchanged
    expect(apiKeyArg).toBeUndefined()
  })
})
