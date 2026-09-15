// useVaultSearch.test.ts — library-b-c-design-2026-09-07 §C1.
//
// Mirrors useKnowledgeSearch.test.ts's debounce/ordering discipline, plus the
// one thing that hook does not need: resolving WHICH collection a query is
// scoped to, from the folder currently browsed, before a request can be sent
// at all. Fixtures are parsed through the generated zod schemas so a test can
// never be built on a payload the server (or KnowledgeBaseInfo) could not
// produce.

import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  VaultSearchResponse as VaultSearchResponseSchema,
  KnowledgeBaseInfo as KnowledgeBaseInfoSchema,
} from '@/lib/api/generated/schemas'
import {
  useVaultSearch,
  VAULT_SEARCH_DEBOUNCE_MS,
  classifyVaultCoverage,
  vaultClampOf,
  isNotesCappedAtLimit,
  type VaultSearchResponse,
  type KnowledgeBaseInfo,
} from './useVaultSearch'

function vaultInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  const base: KnowledgeBaseInfo = {
    workspace_id: 'ws-1',
    root_path: 'vault',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: 'kb_1',
    ...over,
  }
  return KnowledgeBaseInfoSchema.parse(base) as KnowledgeBaseInfo
}

function response(over: Partial<VaultSearchResponse> = {}): VaultSearchResponse {
  const base: VaultSearchResponse = {
    collection_id: 'kb_1',
    complete: true,
    notes: [{ path: 'a.md', title: 'A' }],
    records: [],
    views: [],
    ...over,
  }
  return VaultSearchResponseSchema.parse(base) as VaultSearchResponse
}

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
}

const settle = (ms: number) => act(async () => { await new Promise((r) => setTimeout(r, ms)) })

// ─────────────────────────────────────────────────────────────────────────────
// Collection resolution — this hook's own job, unlike useKnowledgeSearch's
// (which receives collectionId as a prop)
// ─────────────────────────────────────────────────────────────────────────────

describe('useVaultSearch — collection resolution', () => {
  it('resolves collectionId from the folder and scopes the request to it', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(response())
    const { result } = renderHook(
      () =>
        useVaultSearch({
          workspaceId: 'ws-1',
          folderPath: 'vault/sub',
          query: 'landlock',
          loadCollectionInfo,
          searchFn,
        }),
      { wrapper: wrapper() },
    )

    await waitFor(() => expect(searchFn).toHaveBeenCalled())
    expect(loadCollectionInfo).toHaveBeenCalledWith('ws-1', 'vault/sub')
    expect(searchFn.mock.calls[0]?.[1]).toMatchObject({ collection_id: 'kb_1', query: 'landlock', limit: 20 })
    await waitFor(() => expect(result.current.collectionId).toBe('kb_1'))
    expect(result.current.collectionRootPath).toBe('vault')
  })

  it('issues no request when the folder is not inside a vault', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo({ is_knowledge_base: false, collection_id: undefined, marker: 'none' }))
    const searchFn = vi.fn().mockResolvedValue(response())
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: '', query: 'landlock', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )

    await waitFor(() => expect(loadCollectionInfo).toHaveBeenCalled())
    await settle(VAULT_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
    expect(result.current.collectionId).toBeUndefined()
  })

  it('issues no request while a detection error is present, even if is_knowledge_base is true', async () => {
    // E-9's rule, applied here: a detection error must never be silently
    // treated as "search is fine".
    const loadCollectionInfo = vi.fn().mockResolvedValue(
      vaultInfo({ detection_error: { code: 'root_unreadable', message: 'permission denied' } }),
    )
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'landlock', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )
    await settle(VAULT_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })

  it('finding F-I: surfaces detection_error.message so the caller can show it, rather than dropping it', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(
      vaultInfo({ detection_error: { code: 'root_unreadable', message: 'cannot read vault: permission denied' } }),
    )
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'landlock', loadCollectionInfo }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.detectionError).toBe('cannot read vault: permission denied'))
  })

  it('finding F-I: surfaces the collection-info REQUEST failing outright, not only detection_error', async () => {
    const loadCollectionInfo = vi.fn().mockRejectedValue(new Error('network error'))
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'landlock', loadCollectionInfo }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.detectionError).toBe('network error'))
  })

  it('leaves detectionError undefined for an ordinary, successful detection', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'landlock', loadCollectionInfo }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(loadCollectionInfo).toHaveBeenCalled())
    expect(result.current.detectionError).toBeUndefined()
  })

  it('issues no lookup at all at the virtual root (workspaceId null)', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    renderHook(
      () => useVaultSearch({ workspaceId: null, folderPath: '', query: 'landlock', loadCollectionInfo }),
      { wrapper: wrapper() },
    )
    await settle(VAULT_SEARCH_DEBOUNCE_MS * 3)
    expect(loadCollectionInfo).not.toHaveBeenCalled()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Debounce and ordering — same discipline as useKnowledgeSearch
// ─────────────────────────────────────────────────────────────────────────────

describe('useVaultSearch — debounce and ordering', () => {
  it('collapses a burst of keystrokes into ONE request, for the final term', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(response())
    const { rerender } = renderHook(
      ({ q }: { q: string }) =>
        useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: q, loadCollectionInfo, searchFn }),
      { wrapper: wrapper(), initialProps: { q: '' } },
    )

    rerender({ q: 'l' })
    rerender({ q: 'la' })
    rerender({ q: 'lan' })
    rerender({ q: 'land' })
    rerender({ q: 'landl' })

    await waitFor(() => expect(searchFn).toHaveBeenCalled())
    await settle(VAULT_SEARCH_DEBOUNCE_MS * 2)

    expect(searchFn.mock.calls.map((c) => (c[1] as { query: string }).query)).toEqual(['landl'])
  })

  it('issues no request at all for a blank or whitespace-only query', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: '   ', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )
    await settle(VAULT_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })

  it('a slow answer to an EARLIER query never overwrites the results of a later one', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const resolvers: Record<string, (r: VaultSearchResponse) => void> = {}
    const searchFn = vi.fn(
      (_ws: string, body: { query: string }) =>
        new Promise<VaultSearchResponse>((resolve) => {
          resolvers[body.query] = resolve
        }),
    )

    const { result, rerender } = renderHook(
      ({ q }: { q: string }) =>
        useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: q, loadCollectionInfo, searchFn }),
      { wrapper: wrapper(), initialProps: { q: '' } },
    )

    rerender({ q: 'land' })
    await waitFor(() => expect(resolvers['land']).toBeDefined())

    rerender({ q: 'landlock' })
    await waitFor(() => expect(resolvers['landlock']).toBeDefined())

    await act(async () => {
      resolvers['landlock']?.(response({ notes: [{ path: 'landlock.md', title: 'Landlock' }] }))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.counts.notes).toBe(1))
    expect(result.current.response?.notes[0]?.path).toBe('landlock.md')

    await act(async () => {
      resolvers['land']?.(
        response({ notes: [{ path: 'land-a.md', title: 'Land A' }, { path: 'land-b.md', title: 'Land B' }] }),
      )
      await Promise.resolve()
    })
    await settle(60)

    expect(result.current.debouncedQuery).toBe('landlock')
    expect(result.current.response?.notes.map((n) => n.path)).toEqual(['landlock.md'])
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Counts — the segmented filter's per-kind numbers
// ─────────────────────────────────────────────────────────────────────────────

describe('useVaultSearch — counts', () => {
  it('counts each kind independently and sums them into "all"', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(
      response({
        notes: [{ path: 'a.md', title: 'A' }, { path: 'b.md', title: 'B' }],
        records: [{ path: 'acme.md', title: 'Acme', cells: [] }],
        views: [{ view: 'open-deals', label: 'Open deals' }],
      }),
    )
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'a', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )

    await waitFor(() => expect(result.current.counts.all).toBe(4))
    expect(result.current.counts).toEqual({ all: 4, notes: 2, records: 1, views: 1, attachments: 0 })
  })

  it('is all-zero before any response has arrived', () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: '', loadCollectionInfo }),
      { wrapper: wrapper() },
    )
    expect(result.current.counts).toEqual({ all: 0, notes: 0, records: 0, views: 0, attachments: 0 })
    expect(result.current.isActive).toBe(false)
  })

  it('counts attachments and folds them into "all" (US-1/MV-9 — ported attachment search)', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(
      response({
        notes: [{ path: 'a.md', title: 'A' }],
        attachments: [{ path: 'img/diagram.png', name: 'diagram.png' }],
      }),
    )
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'diagram', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.counts.attachments).toBe(1))
    expect(result.current.counts).toEqual({ all: 2, notes: 1, records: 0, views: 0, attachments: 1 })
  })

  it('treats an absent attachments group as [] (MV-9 platform carve-out)', async () => {
    // A propindex-less build sends no `attachments` key at all rather than an
    // empty array — this must never surface as a count of undefined/NaN.
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(response({ notes: [{ path: 'a.md', title: 'A' }] }))
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'a', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.counts.notes).toBe(1))
    expect(result.current.counts.attachments).toBe(0)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Honesty port (unified-search-and-grep-spec.md US-1, MV-9, FR-036, FR-037) —
// every signal the retired KnowledgeSearch box carried, ported onto
// VaultSearchResponse's simpler wire shape. Expected values are derived from
// the CONTRACT and the spec's acceptance scenarios, not from the functions.
// ─────────────────────────────────────────────────────────────────────────────

describe('classifyVaultCoverage — US-1 AS-2/AS-3, FR-036', () => {
  it('is undefined once the answer is complete — nothing partial to disclose', () => {
    expect(classifyVaultCoverage(response({ complete: true, notes_searched: 4120 }))).toBeUndefined()
  })

  it('is undefined when the server sent no coverage numbers at all', () => {
    expect(classifyVaultCoverage(response({ complete: false }))).toBeUndefined()
  })

  it('is "ratio" when a partial answer states BOTH a searched count and a known total', () => {
    expect(
      classifyVaultCoverage(response({ complete: false, notes_searched: 4120, notes_total_known: 12880 })),
    ).toBe('ratio')
  })

  it('is "so-far" when a partial answer has a count but no total — never an invented denominator', () => {
    expect(classifyVaultCoverage(response({ complete: false, notes_searched: 4120 }))).toBe('so-far')
  })
})

describe('vaultClampOf — US-8-style clamp disclosure, FR-037', () => {
  it('returns null when nothing was clamped', () => {
    expect(vaultClampOf(response({ limit_clamped: false }))).toBeNull()
  })

  it('reports the clamp with the refused number, when the server echoed it', () => {
    expect(vaultClampOf(response({ limit_clamped: true, limit_requested: 400 }))).toEqual({ requested: 400 })
  })

  it('still reports the clamp when the server omitted the requested number', () => {
    expect(vaultClampOf(response({ limit_clamped: true }))).toEqual({})
  })
})

describe('isNotesCappedAtLimit — server truth over the length heuristic', () => {
  it('prefers the server-stated notes_capped_at_limit when present', () => {
    // The array is short, but the server says it was capped — trust it.
    expect(
      isNotesCappedAtLimit(response({ notes: [{ path: 'a.md', title: 'A' }], notes_capped_at_limit: true }), 20),
    ).toBe(true)
  })

  it('falls back to length-vs-limit when the server states nothing', () => {
    const notes = Array.from({ length: 20 }, (_, i) => ({ path: `n${i}.md`, title: `N${i}` }))
    expect(isNotesCappedAtLimit(response({ notes }), 20)).toBe(true)
  })

  it('is false for a short, unstated list', () => {
    expect(isNotesCappedAtLimit(response({ notes: [{ path: 'a.md', title: 'A' }] }), 20)).toBe(false)
  })
})

describe('useVaultSearch — coverage/clamp/notesCappedAtLimit surfaced on the hook result', () => {
  it('surfaces the classified coverage and clamp alongside the response', async () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const searchFn = vi.fn().mockResolvedValue(
      response({
        complete: false,
        notes: [{ path: 'a.md', title: 'A' }],
        notes_searched: 4120,
        notes_total_known: 12880,
        limit_clamped: true,
        limit_requested: 400,
        notes_capped_at_limit: true,
      }),
    )
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: 'x', loadCollectionInfo, searchFn }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.response).toBeDefined())
    expect(result.current.coverage).toBe('ratio')
    expect(result.current.clamp).toEqual({ requested: 400 })
    expect(result.current.notesCappedAtLimit).toBe(true)
  })

  it('is undefined/null/false before any response has arrived', () => {
    const loadCollectionInfo = vi.fn().mockResolvedValue(vaultInfo())
    const { result } = renderHook(
      () => useVaultSearch({ workspaceId: 'ws-1', folderPath: 'vault', query: '', loadCollectionInfo }),
      { wrapper: wrapper() },
    )
    expect(result.current.coverage).toBeUndefined()
    expect(result.current.clamp).toBeNull()
    expect(result.current.notesCappedAtLimit).toBe(false)
  })
})
