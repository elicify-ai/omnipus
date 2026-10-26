// Oracle: docs/security.md::Set tool policies requires the server's actionable
// recovery message. OpenAPI updateGlobalToolPolicies 503 gives its exact text.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { updateGlobalToolPolicies } from '@/lib/api/tools'
import { request } from '@/lib/api/http'
import { ApiError } from '@/lib/api-error'
import type { GlobalToolPolicies } from '@/lib/api/generated/openapi-types'

const RECOVERY = 'The settings were saved, but could not be applied to running agents. Retry applying the change, or restart Omnipus.'
const GENERIC = 'The server is unavailable. Please try again in a moment.'
const INTERNAL = 'INTERNAL_RELOAD_ONLY: registry creation failed: PRIVATE_OPERATOR_CONTEXT'
const POLICIES: GlobalToolPolicies = { policies: { bash: 'deny' } }
const fetchMock = vi.fn<typeof fetch>()

function response(body: string, status = 503, contentType = 'application/json') {
  return new Response(body, { status, headers: { 'Content-Type': contentType, 'Retry-After': '3' } })
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  document.cookie = 'csrf=rr1-policy-proof; Path=/'
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; Path=/'
})

describe('real global-policy request recovery boundary', () => {
  it('rejects with the exact documented recovery message while retaining safe error metadata', async () => {
    const body = JSON.stringify({ error: RECOVERY, code: 'reload_failed', details: { reload_error: INTERNAL } })
    fetchMock.mockResolvedValueOnce(response(body))
    const pending = updateGlobalToolPolicies(POLICIES)
    await expect(pending).rejects.toBeInstanceOf(ApiError)
    await expect(pending).rejects.toMatchObject({
      status: 503, userMessage: RECOVERY, message: `503: ${RECOVERY}`,
      code: 'reload_failed', body, retryAfterMs: 3000, cause: expect.any(ApiError),
    })
    expect(fetchMock).toHaveBeenCalledExactlyOnceWith('/api/v1/security/tool-policies', expect.objectContaining({
      method: 'PUT', credentials: 'include', body: JSON.stringify(POLICIES),
    }))
  })

  it('returns the exact policy response on a successful save', async () => {
    fetchMock.mockResolvedValueOnce(response(JSON.stringify(POLICIES), 200))
    await expect(updateGlobalToolPolicies(POLICIES)).resolves.toEqual(POLICIES)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    { name: 'another 5xx', status: 500, body: JSON.stringify({ error: RECOVERY }) },
    { name: 'malformed JSON', status: 503, body: '{broken' },
    { name: 'missing error', status: 503, body: JSON.stringify({ details: { reload_error: INTERNAL } }) },
    { name: 'non-string error', status: 503, body: JSON.stringify({ error: 42 }) },
    { name: 'blank error', status: 503, body: JSON.stringify({ error: '   ' }) },
    { name: 'oversized body', status: 503, body: JSON.stringify({ error: RECOVERY, details: { private: 'x'.repeat(5000) } }) },
    { name: 'non-text body', status: 503, body: JSON.stringify({ error: RECOVERY }), contentType: 'application/octet-stream' },
  ])('keeps the safe generic error for $name', async ({ status, body, contentType }) => {
    fetchMock.mockResolvedValueOnce(response(body, status, contentType))
    await expect(updateGlobalToolPolicies(POLICIES)).rejects.toMatchObject({ status, userMessage: GENERIC, retryAfterMs: 3000 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('does not apply the global-policy translation to another endpoint', async () => {
    fetchMock.mockResolvedValueOnce(response(JSON.stringify({ error: RECOVERY, details: { reload_error: INTERNAL } })))
    await expect(request('/security/sandbox-config', { method: 'PUT', body: JSON.stringify({ auto_approve: false }) })).rejects.toMatchObject({ status: 503, userMessage: GENERIC })
    expect(fetchMock).toHaveBeenCalledExactlyOnceWith('/api/v1/security/sandbox-config', expect.objectContaining({ method: 'PUT' }))
  })
})
