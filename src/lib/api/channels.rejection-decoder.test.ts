/**
 * T-30b / S-83 / FR-090 — positive instrument controls for the REAL transport.
 * These decoder-only controls may already PASS; they do not prove server-side
 * sanitization or section rendering. Those belong to T-30 / T-30c.
 * Oracles: channels-spec-r8 contract table and S-83 (500/23/120/10/54/4096).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-error'
import {
  ChannelOutboundRejection as RejectionSchema,
  ChannelOutboundUpdateRequest as UpdateSchema,
  ChannelOutboundWriteResponse as WriteSchema,
} from '@/lib/api/generated/schemas'
import { request } from './http'

const PATH = '/channels/discord.qa/outbound'
const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
  document.cookie = 'csrf=t30b-synthetic; path=/'
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

function maximalRejection() {
  // Deliberately schema-maximal consumer fixture, not a claim about a producer's
  // invalid-entry ordering. Positions 54..63 are the ten largest request slots.
  return RejectionSchema.parse({
    code: 'invalid_targets',
    error: 'E'.repeat(500),
    target_errors: Array.from({ length: 10 }, (_, i) => ({
      index: 54 + i,
      target: `${'h'.repeat(20)}...`,
      code: 'unsupported_channel_kind',
      reason: `reason${i}`.padEnd(120, 'r'),
    })),
    omitted_count: 54,
  })
}

async function refuseThroughRealTransport(raw: string, contentLength: boolean) {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  if (contentLength) headers.set('Content-Length', String(new TextEncoder().encode(raw).byteLength))
  const response = new Response(raw, { status: 400, headers })
  expect(response.headers.has('Content-Length')).toBe(contentLength)
  fetchMock.mockResolvedValueOnce(response)
  const body = UpdateSchema.parse({
    policy: 'agent_may_start',
    targets: Array.from({ length: 64 }, (_, i) => `raw-${i}`),
    expected_revision: 't30b-synthetic-revision',
  })
  try {
    await request(PATH, { method: 'PUT', body: JSON.stringify(body) }, WriteSchema)
  } catch (error) {
    expect(error).toBeInstanceOf(ApiError)
    if (!(error instanceof ApiError)) throw error
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe(`/api/v1${PATH}`)
    expect(init?.method).toBe('PUT')
    expect(init?.credentials).toBe('include')
    expect(JSON.parse(String(init?.body))).toEqual(body)
    return error
  }
  throw new Error('instrument: a raw HTTP 400 must reject through request()')
}

describe('T-30b decoder instrument — not producer mutation proof', () => {
  it.each([true, false])('retains all ten schema-maximal items, Content-Length=%s', async (declaredLength) => {
    const expected = maximalRejection()
    const raw = JSON.stringify(expected)
    expect(new TextEncoder().encode(raw).byteLength).toBeLessThan(4096)
    const error = await refuseThroughRealTransport(raw, declaredLength)
    expect(error.status).toBe(400)
    expect(error.code).toBe('invalid_targets')
    expect(error.userMessage).toBe(expected.error)
    expect(error.body).toBe(raw)
    expect(RejectionSchema.parse(JSON.parse(error.body ?? 'null'))).toEqual(expected)
    expect(expected.target_errors).toHaveLength(10)
    expect(expected.omitted_count).toBe(54)
  })

  it.each([
    { name: 'control-origin', hint: 'alpha' },
    { name: 'escaping-origin', hint: 'safe' },
  ])('retains the sanitized $name fixture with no binary or escaping expansion', async ({ hint }) => {
    // S-83 case 2 fixtures are ALREADY sanitized. The Go producer tests, not
    // this constant, must prove that raw controls/Unicode are actually dropped.
    const expected = RejectionSchema.parse({
      error: 'invalid targets', code: 'invalid_targets',
      target_errors: [{ index: 1, target: hint, code: 'invalid_id', reason: 'invalid id' }],
    })
    const raw = JSON.stringify(expected)
    expect([...raw].filter((character) => character.charCodeAt(0) < 0x20 || character.charCodeAt(0) > 0x7e)).toEqual([])
    expect(raw).not.toContain('\\u')
    const error = await refuseThroughRealTransport(raw, false)
    expect(error.code).toBe('invalid_targets')
    expect(error.body).toBe(raw)
    expect(RejectionSchema.parse(JSON.parse(error.body ?? 'null'))).toEqual(expected)
  })

  it.each([true, false])('oversized-body discard control, Content-Length=%s', async (declaredLength) => {
    const raw = JSON.stringify({ error: 'oversize'.repeat(600), code: 'invalid_targets' })
    expect(new TextEncoder().encode(raw).byteLength).toBeGreaterThan(4096)
    const error = await refuseThroughRealTransport(raw, declaredLength)
    expect(error.status).toBe(400)
    expect(error.code).toBeUndefined()
    expect(error.body).toBeUndefined()
    expect(error.userMessage).not.toContain('oversize')
  })

  it('raw-echo binary-sniff control loses the rejection, unlike the sanitized short envelope', async () => {
    const safe = RejectionSchema.parse({
      error: 'invalid', code: 'invalid_targets',
      target_errors: [{ index: 0, target: '', code: 'invalid_id', reason: 'invalid id' }],
    })
    const safeRaw = JSON.stringify(safe)
    const intact = await refuseThroughRealTransport(safeRaw, false)
    expect(intact.code).toBe('invalid_targets')
    expect(intact.body).toBe(safeRaw)
    expect(RejectionSchema.parse(JSON.parse(intact.body ?? 'null')).target_errors).toEqual(safe.target_errors)

    fetchMock.mockReset()
    const rawEcho = JSON.stringify({
      ...safe, target_errors: [{ ...safe.target_errors?.[0], target: '\u007f'.repeat(20) }],
    })
    const sample = rawEcho.slice(0, 256)
    const delCount = [...sample].filter((character) => character === '\u007f').length
    expect(delCount).toBe(20)
    expect(delCount / sample.length).toBeGreaterThan(0.05)
    expect(RejectionSchema.safeParse(JSON.parse(rawEcho)).success).toBe(false)
    const discarded = await refuseThroughRealTransport(rawEcho, false)
    expect(discarded.status).toBe(400)
    expect(discarded.code).toBeUndefined()
    expect(discarded.body).toBeUndefined()
    expect(discarded.userMessage).not.toBe(safe.error)
  })

  it.each(['error', 'target', 'reason'])('generated schema instrument rejects unsafe %s', (field) => {
    const item = { index: 0, target: 'safe', code: 'invalid_id', reason: 'invalid id' }
    const body = { error: 'invalid', code: 'invalid_targets', target_errors: [item] }
    expect(RejectionSchema.safeParse(body).success).toBe(true)
    const unsafe = field === 'error'
      ? { ...body, error: 'bad\u007f' }
      : { ...body, target_errors: [{ ...item, [field]: 'bad\u007f' }] }
    expect(RejectionSchema.safeParse(unsafe).success).toBe(false)
  })
})
