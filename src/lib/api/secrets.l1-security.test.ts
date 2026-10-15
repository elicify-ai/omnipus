// FR-062/075/079/083, K5/K6/K49. Real secret client; only fetch is doubled.
import { it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { SecretRequestState } from '@/lib/api/generated/openapi-types'
import { ApiError } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { installNetwork, jsonResponse, loadSecretApi, requests, resetSharedState } from '../../../tests/l1-ui-red-u23/support'

const sentinel = 'SECRET_API_SENTINEL_24f9'
const submitted: SecretRequestState = { request_id: 'sec-api', state: 'submitted', reference_names: ['openai_key.v1'], updated_at: '2026-10-14T09:00:00Z' }
let api: Awaited<ReturnType<typeof loadSecretApi>>
beforeEach(async () => {
  resetSharedState()
  api = await loadSecretApi()
  installNetwork(() => jsonResponse(submitted))
})
afterEach(() => { queryClient.clear(); vi.unstubAllGlobals() })

it('K5 / FR-062/083: submit is authenticated, destination-bound, no arbitrary key or password step', async () => {
  const result = await api.submitSecretRequest('sec-api', { values: { api_key: sentinel } })
  expect(result).toEqual({ kind: 'accepted', state: submitted })
  expect(requests.map(c => ({ path: c.path, method: c.method, body: JSON.parse(c.body!), credentials: c.credentials }))).toEqual([
    { path: '/api/v1/secret-requests/sec-api/submit', method: 'POST', body: { values: { api_key: sentinel } }, credentials: 'include' },
  ])
  expect(requests[0].headers.get('X-Reauth-Token')).toBeNull()
})

it('K5 / FR-079: value-free GET does not send values or a submit body', async () => {
  expect(await api.getSecretRequest('sec-api')).toEqual(submitted)
  expect(requests.map(c => ({ path: c.path, method: c.method, body: c.body, credentials: c.credentials }))).toEqual([
    { path: '/api/v1/secret-requests/sec-api', method: 'GET', body: undefined, credentials: 'include' },
  ])
})

it('K5 / FR-074/079: 409 carries real terminal state, not a generic success or automatic retry', async () => {
  const cancelled = { ...submitted, state: 'cancelled' as const, reference_names: undefined }
  installNetwork(() => jsonResponse(cancelled, 409))
  expect(await api.submitSecretRequest('sec-api', { values: { api_key: sentinel } })).toEqual({ kind: 'not-pending', state: cancelled })
  expect(requests).toHaveLength(1)
})

it.each([401, 404, 500])('FR-079: HTTP %i is a specific refusal, not submitted', async status => {
  installNetwork(() => jsonResponse({ error: 'secret request unavailable', code: 'unavailable' }, status))
  await expect(api.getSecretRequest('sec-api')).rejects.toBeInstanceOf(ApiError)
  // Call separately only for inspecting the exact public error; no state-changing replay.
  // FR-079 specifies refusal/status, not the generic transport's mapped prose.
  // team-lead approved oracle correction: assert exact server code and body.
  await expect(api.getSecretRequest('sec-api')).rejects.toMatchObject({
    status, code: 'unavailable', body: JSON.stringify({ error: 'secret request unavailable', code: 'unavailable' }),
  })
  expect(requests.map(c => c.method)).toEqual(['GET', 'GET'])
})

it('FR-075: CSRF rejection never replays a secret-bearing POST (unlike generic safe-to-retry mutations)', async () => {
  installNetwork(call => call.method === 'POST' ? jsonResponse({ error: 'csrf token mismatch' }, 403) : jsonResponse({ ready: true }))
  await expect(api.submitSecretRequest('sec-api', { values: { api_key: sentinel } })).rejects.toMatchObject({ status: 403 })
  expect(requests.filter(c => c.method === 'POST')).toHaveLength(1)
})

it('FR-075: network loss never re-sends automatically or caches mutation variables', async () => {
  installNetwork(() => Promise.reject(new TypeError('lost transport')))
  await expect(api.submitSecretRequest('sec-api', { values: { api_key: sentinel } })).rejects.toMatchObject({ status: 0 })
  expect(requests).toHaveLength(1)
  expect(queryClient.getMutationCache().getAll()).toEqual([])
  expect(queryClient.getQueryCache().getAll()).toEqual([])
})

it.each([null, {}, { state: 'submitted' }])('FR-079: malformed/absent request state %j cannot become accepted', async body => {
  installNetwork(() => jsonResponse(body))
  await expect(api.submitSecretRequest('sec-api', { values: { api_key: sentinel } })).rejects.toBeInstanceOf(Error)
  expect(requests).toHaveLength(1)
})
