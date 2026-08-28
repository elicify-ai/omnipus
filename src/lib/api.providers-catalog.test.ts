/**
 * api.providers-catalog.test.ts — ADR-068 T068-18.
 *
 * Covers the SPA half of FR-037: the catalog comes from
 * GET /api/v1/providers/catalog, re-validated with `If-None-Match` on the
 * ADR-067 A-1 cadence, and MAJ-004's assertion — at most one `200` per ETag
 * value (304s are expected requests, a second full body for an unchanged
 * document is not). Also covers the "Catalog unavailable in the picker" BDD
 * error path: a 500 must surface as a rejection the picker can render.
 *
 * The document under test is the 190-entry fixture every other ADR-068 SPA
 * test renders against, so a fixture that stops satisfying the generated
 * schema fails here too.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  fetchProvidersCatalog,
  resetProvidersCatalogCache,
  ApiError,
  ApiSchemaError,
  isApiError,
} from './api'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'

const CATALOG_URL = '/api/v1/providers/catalog'
const ETAG_1 = '"sha256-aaaa"'
const ETAG_2 = '"sha256-bbbb"'

function catalog200(etag: string, body: unknown = PROVIDERS_CATALOG): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json', ETag: etag },
  })
}

function catalog304(): Response {
  // A real 304 carries no body.
  return new Response(null, { status: 304, headers: { ETag: ETAG_1 } })
}

function headerOf(call: unknown[], name: string): string | undefined {
  const init = call[1] as RequestInit | undefined
  const headers = (init?.headers ?? {}) as Record<string, string>
  return headers[name]
}

let fetchSpy: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetProvidersCatalogCache()
  fetchSpy = vi.fn()
  vi.stubGlobal('fetch', fetchSpy)
})

afterEach(() => {
  resetProvidersCatalogCache()
  vi.unstubAllGlobals()
})

describe('fetchProvidersCatalog — first fetch', () => {
  it('GETs the catalog endpoint without If-None-Match and returns the document', async () => {
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_1))

    const doc = await fetchProvidersCatalog()

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(String(fetchSpy.mock.calls[0][0])).toContain(CATALOG_URL)
    expect(headerOf(fetchSpy.mock.calls[0], 'If-None-Match')).toBeUndefined()
    expect(doc.schema_version).toBe('2.0.0')
    expect(doc.providers).toHaveLength(190)
    expect(doc.served_from).toBe('embedded')
  })
})

describe('fetchProvidersCatalog — ETag re-validation (FR-037 / MAJ-004)', () => {
  it('sends If-None-Match with the stored ETag on the next call', async () => {
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_1))
    await fetchProvidersCatalog()

    fetchSpy.mockResolvedValueOnce(catalog304())
    await fetchProvidersCatalog()

    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(headerOf(fetchSpy.mock.calls[1], 'If-None-Match')).toBe(ETAG_1)
  })

  it('a 304 keeps the cached document — the same object, never re-parsed', async () => {
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_1))
    const first = await fetchProvidersCatalog()

    fetchSpy.mockResolvedValueOnce(catalog304())
    const second = await fetchProvidersCatalog()

    expect(second).toBe(first)
    expect(second.providers).toHaveLength(190)
  })

  it('serves at most one 200 per ETag value across repeated re-validations', async () => {
    fetchSpy
      .mockResolvedValueOnce(catalog200(ETAG_1))
      .mockResolvedValueOnce(catalog304())
      .mockResolvedValueOnce(catalog304())
      .mockResolvedValueOnce(catalog304())

    const docs = [
      await fetchProvidersCatalog(),
      await fetchProvidersCatalog(),
      await fetchProvidersCatalog(),
      await fetchProvidersCatalog(),
    ]

    expect(fetchSpy).toHaveBeenCalledTimes(4)
    const statuses = await Promise.all(
      fetchSpy.mock.results.map(async (r) => ((await r.value) as Response).status),
    )
    expect(statuses.filter((s) => s === 200)).toHaveLength(1)
    // Every caller got the identical document.
    expect(new Set(docs).size).toBe(1)
  })

  it('a changed ETag installs the new document and is replayed next time', async () => {
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_1))
    const first = await fetchProvidersCatalog()

    const updated = { ...PROVIDERS_CATALOG, version: 'v2026.8.23' }
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_2, updated))
    const second = await fetchProvidersCatalog()

    expect(second).not.toBe(first)
    expect(second.version).toBe('v2026.8.23')

    fetchSpy.mockResolvedValueOnce(catalog304())
    await fetchProvidersCatalog()
    expect(headerOf(fetchSpy.mock.calls[2], 'If-None-Match')).toBe(ETAG_2)
  })

  it('re-requests unconditionally when a 304 arrives with nothing cached', async () => {
    fetchSpy.mockResolvedValueOnce(catalog304()).mockResolvedValueOnce(catalog200(ETAG_1))

    const doc = await fetchProvidersCatalog()

    expect(doc.providers).toHaveLength(190)
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(headerOf(fetchSpy.mock.calls[1], 'If-None-Match')).toBeUndefined()
  })
})

describe('fetchProvidersCatalog — error propagation (Catalog unavailable)', () => {
  it('rejects with a 500 ApiError the picker can render', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'catalog unavailable' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    await expect(fetchProvidersCatalog()).rejects.toSatisfy(
      (err: unknown) => isApiError(err) && err.status === 500,
    )
  })

  it('a failed re-validation leaves the cached document intact', async () => {
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_1))
    const first = await fetchProvidersCatalog()

    fetchSpy.mockResolvedValueOnce(new Response('boom', { status: 500 }))
    await expect(fetchProvidersCatalog()).rejects.toBeInstanceOf(ApiError)

    fetchSpy.mockResolvedValueOnce(catalog304())
    const recovered = await fetchProvidersCatalog()
    expect(recovered).toBe(first)
    expect(headerOf(fetchSpy.mock.calls[2], 'If-None-Match')).toBe(ETAG_1)
  })

  it('surfaces a transport failure as a status-0 ApiError', async () => {
    fetchSpy.mockRejectedValueOnce(new TypeError('Failed to fetch'))

    await expect(fetchProvidersCatalog()).rejects.toSatisfy(
      (err: unknown) => isApiError(err) && err.status === 0,
    )
  })

  it('rejects a schema-invalid 200 and stores no ETag for it', async () => {
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_1, { schema_version: '1.0.0', providers: [] }))

    await expect(fetchProvidersCatalog()).rejects.toBeInstanceOf(ApiSchemaError)

    // The bad response must not have installed an ETag — otherwise the next
    // 304 would resolve with a document we never validated.
    fetchSpy.mockResolvedValueOnce(catalog200(ETAG_2))
    await fetchProvidersCatalog()
    expect(headerOf(fetchSpy.mock.calls[1], 'If-None-Match')).toBeUndefined()
  })
})
