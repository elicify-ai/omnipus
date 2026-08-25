/**
 * providersCatalog.test.ts — ADR-067 spec test T45, the SPA half of FR-017.
 *
 * Scenarios: US-7.AC5 ("the SPA consumes only the generated type"),
 * US-7.AC1 (the serving envelope), US-7.AC4 (503 → a rendered failure, never
 * an empty catalog) and A-1 (the client cache rule).
 *
 * DIVISION OF LABOUR — this file deliberately does NOT restate what
 * `src/lib/api.providers-catalog.test.ts` (ADR-068 T068-18) already pins. That
 * file owns the ETag TRANSPORT mechanics: If-None-Match replay, 304 → the same
 * parsed object, at most one 200 per ETag value, a changed ETag installing a
 * new document, a failed re-validation leaving the cache intact. Duplicating
 * them here would give two suites that fail together and prove one thing.
 *
 * What this file owns instead is the ADR-067 CONTRACT the transport carries:
 *
 *   1. the generated zod schema is the SPA's only admission gate, and it
 *      REJECTS an envelope that FR-017 says must be present — asserted with
 *      negative controls, because a parse test whose fixture always passes
 *      cannot tell a real schema from `z.any()`;
 *   2. an ETag is a property of a VALIDATED document — a body the schema
 *      rejects must never install one (the corollary of rule 1: otherwise the
 *      next 304 would resolve with a stale document under a wrong version);
 *   3. 503 (US-7.AC4 "catalog unavailable") reaches the caller as a rejection
 *      carrying the gateway's typed error, so the picker can render it.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { fetchProvidersCatalog, resetProvidersCatalogCache, ApiError, ApiSchemaError } from '../api'
import { ProvidersCatalog as ProvidersCatalogSchema } from '../api/generated/schemas'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import {
  catalogEndpointHint,
  catalogEntryById,
  catalogLabel,
  catalogLogoSlug,
  catalogSubtitle,
} from '../catalogDisplay'
import { PROVIDER_HINTS } from '../constants'

const CATALOG_URL = '/api/v1/providers/catalog'

function jsonResponse(status: number, body: unknown, etag?: string): Response {
  const headers = new Headers({ 'Content-Type': 'application/json' })
  if (etag) headers.set('ETag', etag)
  return new Response(JSON.stringify(body), { status, headers })
}

/** A deep clone of the fixture, so a mutation in one case cannot leak. */
function catalogDoc(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(PROVIDERS_CATALOG)) as Record<string, unknown>
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetProvidersCatalogCache()
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  resetProvidersCatalogCache()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// ---------------------------------------------------------------------------
// 1. The generated schema is the admission gate (US-7.AC5, FR-024)
// ---------------------------------------------------------------------------

describe('ProvidersCatalog — generated zod schema (US-7.AC5)', () => {
  it('accepts the served document, envelope and nested models included', () => {
    const parsed = ProvidersCatalogSchema.safeParse(catalogDoc())
    expect(parsed.success, JSON.stringify(parsed.success ? [] : parsed.error.issues.slice(0, 3))).toBe(true)

    const doc = catalogDoc()
    expect(doc.schema_version).toBe('2.0.0')
    expect(typeof doc.version).toBe('string')
    expect(typeof doc.updated_at).toBe('string')
    expect(typeof doc.source).toBe('string')
    expect(['embedded', 'pulled']).toContain(doc.served_from)
    expect(typeof doc.stale).toBe('boolean')
    const providers = doc.providers as Array<{ models: unknown[] }>
    expect(providers.length).toBeGreaterThan(0)
    expect(Array.isArray(providers[0]!.models)).toBe(true)
  })

  // Negative controls. Each drops or corrupts exactly one thing FR-017 makes
  // required; a schema that accepted any of them would let the picker render a
  // document whose provenance the SPA cannot state.
  it.each([
    ['served_from missing', (d: Record<string, unknown>) => delete d.served_from],
    ['served_from outside the enum', (d: Record<string, unknown>) => (d.served_from = 'elsewhere')],
    ['stale missing', (d: Record<string, unknown>) => delete d.stale],
    ['stale not a boolean', (d: Record<string, unknown>) => (d.stale = 'no')],
    ['schema_version not 2.0.0', (d: Record<string, unknown>) => (d.schema_version = '1.9.0')],
    ['version not vYYYY.M.D', (d: Record<string, unknown>) => (d.version = '2026-08-22')],
    ['source empty', (d: Record<string, unknown>) => (d.source = '')],
    ['providers empty', (d: Record<string, unknown>) => (d.providers = [])],
  ])('rejects a document with %s', (_label, mutate) => {
    const doc = catalogDoc()
    mutate(doc)
    expect(ProvidersCatalogSchema.safeParse(doc).success).toBe(false)
  })

  // NOT asserted here: `additionalProperties: false`. openapi-zod-client emits
  // a non-strict object, so the generated schema ADMITS an unknown top-level
  // key — verified while writing this file. That constraint is enforced on the
  // producing side (pkg/api/generated/contract_test.go validates the gateway's
  // JSON against the YAML, which is strict); pinning the SPA's laxity as if it
  // were a guarantee would be a false green.
})

// ---------------------------------------------------------------------------
// 2. Only a validated document may claim an ETag (A-1)
// ---------------------------------------------------------------------------

describe('fetchProvidersCatalog — the ETag belongs to the validated document (A-1)', () => {
  it('a schema-invalid 200 rejects, and the NEXT call is unconditional', async () => {
    const bad = catalogDoc()
    delete bad.served_from
    fetchMock.mockResolvedValueOnce(jsonResponse(200, bad, '"sha256-bad"'))

    await expect(fetchProvidersCatalog()).rejects.toBeInstanceOf(ApiSchemaError)

    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc(), '"sha256-good"'))
    await fetchProvidersCatalog()

    // Second request must NOT replay the rejected body's ETag: nothing valid
    // was ever cached under it, so a 304 for it would have no document.
    const secondInit = fetchMock.mock.calls[1]![1] as RequestInit
    const headers = new Headers(secondInit.headers as HeadersInit)
    expect(headers.get('If-None-Match')).toBeNull()

    // ...and once a VALID document is in hand, its ETag is replayed.
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 304 }))
    await fetchProvidersCatalog()
    const thirdInit = fetchMock.mock.calls[2]![1] as RequestInit
    expect(new Headers(thirdInit.headers as HeadersInit).get('If-None-Match')).toBe('"sha256-good"')
  })

  it('a 200 with no ETag header leaves the next request unconditional', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc()))
    await fetchProvidersCatalog()

    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc(), '"sha256-1"'))
    await fetchProvidersCatalog()
    const secondInit = fetchMock.mock.calls[1]![1] as RequestInit
    expect(new Headers(secondInit.headers as HeadersInit).get('If-None-Match')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// 3. Catalog unavailable (US-7.AC4) — a rejection, never an empty catalog
// ---------------------------------------------------------------------------

describe('fetchProvidersCatalog — 503 catalog unavailable (US-7.AC4)', () => {
  it('rejects with the gateway’s typed error instead of resolving empty', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(503, { error: 'provider catalog unavailable' }))

    const err = await fetchProvidersCatalog().then(
      () => null,
      (e: unknown) => e,
    )
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(503)
    // The gateway's typed reason is retained verbatim on `body`. `userMessage`
    // is deliberately the friendly 5xx default (api-error.ts keeps
    // server-internal phrasing out of toasts) — asserted so a future change
    // that drops the body has to face this test.
    expect((err as ApiError).body).toContain('provider catalog unavailable')
    expect((err as ApiError).isServerError()).toBe(true)
  })

  it('a 503 caches nothing — the retry after it is a full unconditional GET', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(503, { error: 'provider catalog unavailable' }))
    await expect(fetchProvidersCatalog()).rejects.toBeInstanceOf(ApiError)

    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc(), '"sha256-after-503"'))
    const doc = await fetchProvidersCatalog()

    expect(fetchMock.mock.calls[1]![0]).toContain(CATALOG_URL)
    const retryInit = fetchMock.mock.calls[1]![1] as RequestInit
    expect(new Headers(retryInit.headers as HeadersInit).get('If-None-Match')).toBeNull()
    expect(doc.providers.length).toBeGreaterThan(0)
  })
})

// ---------------------------------------------------------------------------
// 4. Cross-surface consistency, rewritten against the GET
//
// REPLACES src/lib/__tests__/catalog-consistency.test.ts, which locked the same
// property over the deleted bundled catalog constant (deliberately not spelled
// out here — no-bundled-catalog.test.ts greps src/ for that identifier and a
// mention in prose is a hit): onboarding and
// Settings render the SAME object, so terminology and logos are identical by
// construction and a corrupt row fails both surfaces at once. The object is now
// the SERVED document, and the display strings the old file asserted directly
// (label / subtitle / logoSlug / endpointHint) are derived — so the invariants
// move onto the document's inputs and onto catalogDisplay.ts's derivations.
//
// Dropped with the bundled constant and NOT re-created: the fixed "23 curated
// entries" size, the label wording rules and the `wire`/`anthropic_id` field
// invariants. Those describe hand-authored fields the 2.0.0 document does not
// carry; the registry's own assembly job owns the data now (FR-002).
// ---------------------------------------------------------------------------

describe('served catalog — cross-surface consistency (US-7.AC5, SC-002 successor)', () => {
  it('every served provider is renderable by both surfaces', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc(), '"sha256-consistency"'))
    const doc = await fetchProvidersCatalog()

    const ids = doc.providers.map((p) => p.id)
    expect(new Set(ids).size, 'provider ids must be unique — they are the identity').toBe(ids.length)

    for (const entry of doc.providers) {
      expect(entry.id, `id for ${JSON.stringify(entry.name)}`).toBeTruthy()
      expect(entry.company, `company for ${entry.id}`).toBeTruthy()
      expect(entry.name, `name for ${entry.id}`).toBeTruthy()
      // The four derived display strings shared by onboarding and Settings.
      expect(catalogLabel(entry), `label for ${entry.id}`).toBeTruthy()
      expect(catalogSubtitle(entry), `subtitle for ${entry.id}`).toBeTruthy()
      expect(catalogLogoSlug(entry), `logoSlug for ${entry.id}`).toBeTruthy()
      // The subtitle names the endpoint, so a row can never claim a host it
      // does not have (the old file's "subtitle contains endpointHint" rule).
      expect(catalogSubtitle(entry), `subtitle names the endpoint for ${entry.id}`).toContain(
        catalogEndpointHint(entry),
      )
      // Identity is exact: looking a served id up in the served document must
      // return that same row, never a different one via aliases[] (FR-030).
      expect(catalogEntryById(doc.providers, entry.id)?.id).toBe(entry.id)
    }
  })

  it('every API-key hint is keyed by a served catalog id (FR-011)', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc(), '"sha256-hints"'))
    const doc = await fetchProvidersCatalog()
    const served = new Set(doc.providers.map((p) => p.id))

    // A hint keyed by anything else is dead UI: the Sheet looks it up by the
    // stored provider id, which is exact. This is the assertion that catches a
    // retired id (`z-ai`, `gemini`) being re-added to the map.
    for (const key of Object.keys(PROVIDER_HINTS)) {
      expect(served.has(key), `PROVIDER_HINTS key "${key}" is not a catalog id`).toBe(true)
    }
    expect(Object.keys(PROVIDER_HINTS).sort()).toEqual(['anthropic', 'groq', 'openai', 'openrouter'])
  })

  it('an alias never resolves to a row (FR-030 — aliases[] is search-only)', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, catalogDoc(), '"sha256-aliases"'))
    const doc = await fetchProvidersCatalog()

    const served = new Set(doc.providers.map((p) => p.id))
    const aliasesThatAreNotIds = doc.providers
      .flatMap((p) => p.aliases)
      .filter((a) => !served.has(a))
    expect(aliasesThatAreNotIds.length, 'the fixture must carry alias-only strings').toBeGreaterThan(0)

    for (const alias of aliasesThatAreNotIds) {
      expect(catalogEntryById(doc.providers, alias), `alias "${alias}" resolved`).toBeUndefined()
    }
  })
})
