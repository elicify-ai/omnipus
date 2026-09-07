// api.knowledge.test.ts — the FOUR knowledge clients, exercised for real
// (ADR-067 FR-020, FR-021, FR-035, FR-036, FR-037, FR-051, FR-062, FR-080).
//
// ── Why this file exists ─────────────────────────────────────────────────────
// Every knowledge component takes its network call as an injected seam, and
// every component test injects a `vi.fn()`. That is the right boundary for
// those tests — and it meant the PRODUCTION fetchers were executed by nothing
// at all. The URL, the HTTP method, the CSRF header, the credentials mode and
// the zod validation of the response could each have been wrong in any respect
// and 229 component tests would still have passed.
//
// So this file mocks `fetch` — the real transport boundary — and calls the
// exported clients directly. What is asserted is derived from
// contracts/openapi.yaml (the paths, the methods, the required parameters) and
// from Constraint #8's edge-validation rule, not from reading the client back.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  fetchKnowledgeBaseInfo,
  fetchKnowledgeGraph,
  fetchKnowledgeOutline,
  searchKnowledge,
  ApiSchemaError,
  ApiError,
} from './api'
import type {
  KnowledgeBaseInfo,
  KnowledgeGraphResponse,
  KnowledgeOutline,
  KnowledgeSearchResponse,
} from './api/generated/openapi-types'

let fetchSpy: ReturnType<typeof vi.fn>

function stubCookie(value: string) {
  Object.defineProperty(document, 'cookie', { configurable: true, get: () => value })
}
function restoreCookie() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

function ok(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

const INFO: KnowledgeBaseInfo = {
  workspace_id: 'ws_7f3a',
  root_path: 'notes/vault',
  is_knowledge_base: true,
  marker: 'omnipus_vault',
  collection_id: 'kb_3d1c9a7e5b2f4806',
}

const SEARCH: KnowledgeSearchResponse = {
  collection_id: 'kb_3d1c9a7e5b2f4806',
  hits: [{ path: 'architecture/sandboxing.md', title: 'Sandboxing', score: 3.5, kind: 'note' }],
  incompleteness: {
    complete: true,
    total_known: true,
    statement: 'Searched the whole of this knowledge base; its index was complete at query time.',
  },
  limit_applied: 20,
  limit_clamped: false,
}

const OUTLINE: KnowledgeOutline = {
  path: 'notes/vault/architecture/sandboxing.md',
  is_knowledge_base: true,
  collection_id: 'kb_3d1c9a7e5b2f4806',
  headings: [{ level: 1, text: 'Sandboxing', slug: 'sandboxing' }],
}

const GRAPH: KnowledgeGraphResponse = {
  collection_id: 'kb_3d1c9a7e5b2f4806',
  kind: 'backlinks',
  source_path: 'architecture/sandboxing.md',
  nodes: [{ path: 'index.md', title: 'Index', exists: true }],
  edges: [
    {
      from_path: 'index.md',
      to_path: 'architecture/sandboxing.md',
      resolution: 'exact_path',
      ambiguous: false,
    },
  ],
  skipped: [],
  truncated: false,
}

/** The one URL argument every assertion below reads. */
function calledUrl(): string {
  return (fetchSpy.mock.calls[0] as [string, RequestInit])[0]
}
function calledInit(): RequestInit {
  return (fetchSpy.mock.calls[0] as [string, RequestInit])[1]
}

beforeEach(() => {
  stubCookie('__Host-csrf=test-csrf-token')
})

afterEach(() => {
  vi.unstubAllGlobals()
  restoreCookie()
})

describe('fetchKnowledgeBaseInfo — GET /library/{ws}/knowledge', () => {
  it('sends the required path parameter, even when it is empty', async () => {
    // The contract makes `path` REQUIRED on this operation, and '' is the
    // work-tree root — a legitimate folder to ask about, not "no folder". A
    // client that omits the parameter when the value is falsy asks a different
    // question from the one the caller wrote.
    fetchSpy = vi.fn().mockResolvedValue(ok({ ...INFO, root_path: '.' }))
    vi.stubGlobal('fetch', fetchSpy)

    await fetchKnowledgeBaseInfo('ws_7f3a', '')

    expect(calledUrl()).toContain('/api/v1/library/ws_7f3a/knowledge?path=')
    expect(calledInit().credentials).toBe('include')
    expect((calledInit().method ?? 'GET').toUpperCase()).toBe('GET')
  })

  it('percent-encodes the workspace id and the path', async () => {
    fetchSpy = vi.fn().mockResolvedValue(ok(INFO))
    vi.stubGlobal('fetch', fetchSpy)

    await fetchKnowledgeBaseInfo('ws/7f3a', 'notes/my vault')

    expect(calledUrl()).toContain('/api/v1/library/ws%2F7f3a/knowledge?')
    expect(calledUrl()).toContain('path=notes%2Fmy+vault')
  })

  it('validates the response and refuses a payload the contract forbids', async () => {
    // Constraint #8: a drifted payload must surface as an error with telemetry,
    // never be handed to the UI as though it were the contract's shape.
    fetchSpy = vi.fn().mockResolvedValue(ok({ workspace_id: 'ws_7f3a' })) // missing required fields
    vi.stubGlobal('fetch', fetchSpy)

    await expect(fetchKnowledgeBaseInfo('ws_7f3a', 'notes')).rejects.toBeInstanceOf(ApiSchemaError)
  })

  it('turns a non-2xx into an ApiError rather than a parse failure', async () => {
    fetchSpy = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify({ error: 'workspace not found' }), { status: 404 }))
    vi.stubGlobal('fetch', fetchSpy)

    await expect(fetchKnowledgeBaseInfo('ws_missing', '')).rejects.toBeInstanceOf(ApiError)
  })
})

describe('searchKnowledge — POST /library/{ws}/knowledge/search', () => {
  it('POSTs the contract body with the CSRF header and the session cookie', async () => {
    // POST is state-changing as far as the gateway's CSRF middleware is
    // concerned, so the double-submit header must ride along or every search
    // 403s. This is the assertion the injected-seam component tests cannot make.
    fetchSpy = vi.fn().mockResolvedValue(ok(SEARCH))
    vi.stubGlobal('fetch', fetchSpy)

    await searchKnowledge('ws_7f3a', {
      query: 'landlock',
      collection_id: 'kb_3d1c9a7e5b2f4806',
      limit: 20,
      offset: 0,
    })

    expect(calledUrl()).toContain('/api/v1/library/ws_7f3a/knowledge/search')
    const init = calledInit()
    expect((init.method ?? '').toUpperCase()).toBe('POST')
    expect(init.credentials).toBe('include')
    const headers = new Headers(init.headers)
    expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
    expect(JSON.parse(String(init.body))).toEqual({
      query: 'landlock',
      collection_id: 'kb_3d1c9a7e5b2f4806',
      limit: 20,
      offset: 0,
    })
  })

  it('refuses a request body the contract forbids, before it reaches the wire', async () => {
    // `query` has a minimum length in the contract. Sending it anyway earns a
    // 400 with no context; failing here names the field.
    fetchSpy = vi.fn().mockResolvedValue(ok(SEARCH))
    vi.stubGlobal('fetch', fetchSpy)

    await expect(
      searchKnowledge('ws_7f3a', { query: '', collection_id: 'kb_3d1c9a7e5b2f4806', limit: 20, offset: 0 }),
    ).rejects.toBeTruthy()
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('validates the response against the generated schema', async () => {
    // An answer missing `incompleteness` is the single most dangerous drift
    // this endpoint could have: the UI would render results with no honesty
    // statement at all, which is the failure US-6 exists to prevent.
    fetchSpy = vi.fn().mockResolvedValue(
      ok({ collection_id: 'kb_1', hits: [], limit_applied: 20, limit_clamped: false }),
    )
    vi.stubGlobal('fetch', fetchSpy)

    await expect(
      searchKnowledge('ws_7f3a', { query: 'landlock', collection_id: 'kb_1', limit: 20, offset: 0 }),
    ).rejects.toBeInstanceOf(ApiSchemaError)
  })

  it('passes an AbortSignal through so a superseded query is cancelled', async () => {
    fetchSpy = vi.fn().mockResolvedValue(ok(SEARCH))
    vi.stubGlobal('fetch', fetchSpy)
    const controller = new AbortController()

    await searchKnowledge(
      'ws_7f3a',
      { query: 'landlock', collection_id: 'kb_1', limit: 20, offset: 0 },
      controller.signal,
    )

    expect(calledInit().signal).toBe(controller.signal)
  })
})

describe('fetchKnowledgeOutline — GET /library/{ws}/knowledge/outline', () => {
  it('sends the workspace-relative path as the required query parameter', async () => {
    fetchSpy = vi.fn().mockResolvedValue(ok(OUTLINE))
    vi.stubGlobal('fetch', fetchSpy)

    const result = await fetchKnowledgeOutline('ws_7f3a', 'notes/vault/architecture/sandboxing.md')

    expect(calledUrl()).toContain('/api/v1/library/ws_7f3a/knowledge/outline?path=')
    expect(calledUrl()).toContain('notes%2Fvault%2Farchitecture%2Fsandboxing.md')
    expect(calledInit().credentials).toBe('include')
    expect(result.is_knowledge_base).toBe(true)
    expect(result.headings).toHaveLength(1)
  })

  it('rejects a response with no headings array — "always an array, never null"', async () => {
    fetchSpy = vi
      .fn()
      .mockResolvedValue(ok({ path: 'a.md', is_knowledge_base: false }))
    vi.stubGlobal('fetch', fetchSpy)

    await expect(fetchKnowledgeOutline('ws_7f3a', 'a.md')).rejects.toBeInstanceOf(ApiSchemaError)
  })
})

describe('fetchKnowledgeGraph — GET /library/{ws}/knowledge/graph', () => {
  it('sends collection_id and kind, and the note path when one is given', async () => {
    fetchSpy = vi.fn().mockResolvedValue(ok(GRAPH))
    vi.stubGlobal('fetch', fetchSpy)

    await fetchKnowledgeGraph('ws_7f3a', {
      collectionId: 'kb_3d1c9a7e5b2f4806',
      kind: 'backlinks',
      path: 'architecture/sandboxing.md',
    })

    const url = calledUrl()
    expect(url).toContain('/api/v1/library/ws_7f3a/knowledge/graph?')
    expect(url).toContain('collection_id=kb_3d1c9a7e5b2f4806')
    expect(url).toContain('kind=backlinks')
    expect(url).toContain('path=architecture%2Fsandboxing.md')
  })

  it('omits path entirely for the collection-wide queries', async () => {
    // `unresolved` and `orphans` are collection-wide; the contract does not
    // take a path for them and sending an empty one is not the same request.
    fetchSpy = vi.fn().mockResolvedValue(ok({ ...GRAPH, kind: 'orphans', edges: [], source_path: undefined }))
    vi.stubGlobal('fetch', fetchSpy)

    await fetchKnowledgeGraph('ws_7f3a', { collectionId: 'kb_1', kind: 'orphans' })

    expect(calledUrl()).not.toContain('path=')
  })

  it('sends the bounds when the caller sets them (FR-054)', async () => {
    fetchSpy = vi.fn().mockResolvedValue(ok({ ...GRAPH, kind: 'neighbourhood' }))
    vi.stubGlobal('fetch', fetchSpy)

    await fetchKnowledgeGraph('ws_7f3a', {
      collectionId: 'kb_1',
      kind: 'neighbourhood',
      path: 'a.md',
      hops: 2,
      limit: 50,
    })

    expect(calledUrl()).toContain('hops=2')
    expect(calledUrl()).toContain('limit=50')
  })

  it('rejects a response missing `truncated` — the field that tells a clipped graph from a small one', async () => {
    fetchSpy = vi.fn().mockResolvedValue(
      ok({ collection_id: 'kb_1', kind: 'backlinks', nodes: [], edges: [], skipped: [] }),
    )
    vi.stubGlobal('fetch', fetchSpy)

    await expect(
      fetchKnowledgeGraph('ws_7f3a', { collectionId: 'kb_1', kind: 'backlinks', path: 'a.md' }),
    ).rejects.toBeInstanceOf(ApiSchemaError)
  })
})
