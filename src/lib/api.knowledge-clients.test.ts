// api.knowledge-clients.test.ts — the THREE surviving knowledge clients,
// exercised for real (ADR-067 FR-020, FR-021, FR-035, FR-036, FR-037,
// FR-051, FR-062, FR-080; ADR-081 workstream A retired knowledge search —
// see api.knowledge.test.ts's history for the retired searchKnowledge
// coverage, not reproduced here).
//
// ── Why this file exists ─────────────────────────────────────────────────────
// Every knowledge component takes its network call as an injected seam, and
// every component test injects a `vi.fn()`. That is the right boundary for
// those tests — and it meant the PRODUCTION fetchers were executed by nothing
// at all. The URL, the HTTP method, the CSRF header, the credentials mode and
// the zod validation of the response could each have been wrong in any respect
// and the component suites would still have passed.
//
// So this file mocks `fetch` — the real transport boundary — and calls the
// exported clients directly. What is asserted is derived from
// contracts/openapi.yaml (the paths, the methods, the required parameters) and
// from Constraint #8's edge-validation rule, not from reading the client back.
//
// Scope: fetchKnowledgeBaseInfo, fetchKnowledgeOutline, fetchKnowledgeGraph
// only — the three GET clients that still exist. `searchKnowledge` (POST
// /knowledge/search) was retired in favour of the unified /knowledge/find
// endpoint (ADR-081) and is not covered here.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { fetchKnowledgeBaseInfo, fetchKnowledgeOutline, fetchKnowledgeGraph, ApiSchemaError, ApiError } from './api'
import type { KnowledgeBaseInfo, KnowledgeGraphResponse, KnowledgeOutline } from './api/generated/openapi-types'

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
      heading_found: false,
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
