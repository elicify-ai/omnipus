// BasePreview.pool.test.tsx — the bounded view-evaluation pool (ADR-083
// spec ~line 901 / ADR ~line 661, M1 of the review that found this gap):
// "No more than 4 view evaluations may be in flight page-wide at any
// instant... Beyond that, an embed shows 'queued' — visible... never a
// silent wait."
//
// WHY THIS FILE EXISTS SEPARATELY, mirroring LibraryPdfPreview.pool.test.tsx's
// own header: lazy mounting alone does NOT bound this (LazyEmbedMount has no
// cap of its own, by design — every embed inside the mount margin mounts at
// once), so a dashboard with more than 4 `.base` modules inside the margin
// would otherwise fire that many concurrent `fetchKnowledgeViewResult` calls
// at the single Go binary. A test that only asserts "5 BasePreviews render"
// proves nothing about this — every one of them already renders fine with no
// ceiling at all. The assertion that actually requires a real ceiling is the
// COUNT of `loadViewResult` calls made (not settled) while several
// BasePreview instances are mounted and none has resolved.
//
// Each instance gets its OWN QueryClient (the same convention
// BasePreview.test.tsx's own `renderBase` uses) so query-key caching between
// instances is never a confound — the pool being tested is the ONE
// module-level `viewEvaluationPool` singleton, shared page-wide regardless of
// how many QueryClients or BasePreview instances exist, exactly like
// production (many `.base` embeds, one app-wide QueryClient).

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'
import { BasePreview } from './BasePreview'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function entryFor(n: number): LibraryEntry {
  return {
    name: `Dashboard${n}.base`,
    path: `vault/Dashboard${n}.base`,
    is_dir: false,
    is_hidden: false,
    size: 128,
    modified_at: '2026-09-01T10:00:00Z',
    is_text_editable: true,
  }
}

function baseViewsFor(n: number): KnowledgeBaseViews {
  return {
    base_path: `vault/Dashboard${n}.base`,
    is_knowledge_base: true,
    collection_id: `kb_${n}`,
    collection_root: 'vault',
    source: `Dashboard${n}.base`,
    views: [{ name: `dash${n}--main`, label: 'Main' }],
    unloadable_count: 0,
  }
}

function resultFor(n: number): ViewResult {
  return {
    view: `dash${n}--main`,
    label: 'Main',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [],
    complete: true,
    problems: [],
  }
}

// Every `loadViewResult` call, across every mounted instance, parks its
// resolver here in call order rather than auto-resolving — the same "park,
// don't auto-resolve" shape LibraryPdfPreview.pool.test.tsx's `pendingDocs`
// uses, and for the same reason: an auto-resolving mock would settle (and
// therefore release its lease) before the test can ever observe more than
// one in flight at once, proving nothing about the ceiling.
let pendingResolvers: Array<() => void> = []
let callOrder: number[] = []

function makeLoadViewResult(n: number) {
  return (): Promise<ViewResult> =>
    new Promise((resolve) => {
      callOrder.push(n)
      pendingResolvers.push(() => resolve(resultFor(n)))
    })
}

interface Mounted {
  n: number
  container: HTMLElement
  unmount: () => void
}

function mountDashboard(n: number): Mounted {
  const { container, unmount } = render(
    <QueryClientProvider client={makeClient()}>
      <BasePreview
        workspaceId="ws-1"
        entry={entryFor(n)}
        loadBaseViews={() => Promise.resolve(baseViewsFor(n))}
        loadViewResult={makeLoadViewResult(n)}
      />
    </QueryClientProvider>,
  )
  return { n, container, unmount }
}

beforeEach(() => {
  pendingResolvers = []
  callOrder = []
})

// `viewEvaluationPool` is a MODULE-LEVEL singleton (matching pdfWorkerPool.ts
// — one pool, page-wide, for the whole process), so a lease this test never
// released would hold its slot into every LATER test in this file. RTL's
// global `cleanup()` (src/test/setup.ts) unmounts every component after each
// test, but unmounting alone does not release an ALREADY-GRANTED lease whose
// `loadViewResult` promise this suite deliberately never auto-resolves (see
// `makeLoadViewResult`'s own header — a real backend response always
// eventually arrives; this mock's cancellation-agnostic parking is what
// makes that "eventually" visible to a test, not a memory leak by itself).
// Draining every still-pending resolver here — regardless of whether the
// test body already called it, which is a harmless no-op the second time —
// is what actually returns the pool to `activeLeaseCountForTests === 0`
// before the next test's `beforeEach` runs.
afterEach(async () => {
  for (const resolve of pendingResolvers) resolve()
  pendingResolvers = []
  await new Promise((resolve) => setTimeout(resolve, 0))
})

describe('BasePreview — bounded view-evaluation pool (M1, ADR-083 ceiling 4)', () => {
  it('a fifth simultaneous view evaluation queues visibly, and at most 4 are EVER called at once', async () => {
    const docs = [1, 2, 3, 4].map(mountDashboard)
    await waitFor(() => expect(callOrder).toHaveLength(4))
    expect([...callOrder].sort((a, b) => a - b)).toEqual([1, 2, 3, 4])
    for (const d of docs) {
      expect(within(d.container).queryByTestId('base-preview-result-queued')).not.toBeInTheDocument()
    }

    const doc5 = mountDashboard(5)
    await waitFor(() =>
      expect(within(doc5.container).getByTestId('base-preview-result-queued')).toBeInTheDocument(),
    )

    // MUTATION THIS DIES ON: no ceiling at all (today's shape, absent this
    // pool) — the fifth instance would call `loadViewResult` immediately,
    // `callOrder` would include 5, and the queued notice would never render.
    expect(callOrder).not.toContain(5)
    expect(callOrder).toHaveLength(4)
    expect(within(doc5.container).getByTestId('base-preview-result-queued').textContent).toMatch(
      /only 4 views/i,
    )

    // Settling ONE of the first four frees a slot — the fifth is granted and
    // calls through for real.
    pendingResolvers[0]?.()
    await waitFor(() => expect(callOrder).toContain(5))
    await waitFor(() =>
      expect(within(doc5.container).queryByTestId('base-preview-result-queued')).not.toBeInTheDocument(),
    )
    expect(callOrder).toHaveLength(5)
  })

  it('releases a queued lease when the component unmounts before its turn, without ever calling loadViewResult for it', async () => {
    const docs = [1, 2, 3, 4].map(mountDashboard)
    await waitFor(() => expect(callOrder).toHaveLength(4))

    const doc5 = mountDashboard(5)
    await waitFor(() =>
      expect(within(doc5.container).getByTestId('base-preview-result-queued')).toBeInTheDocument(),
    )

    // Scrolled far out of view before its turn (EMB-065 applied to a
    // still-queued lease) — LazyEmbedMount would unmount it; this simulates
    // that directly at the component boundary this pool integrates with.
    doc5.unmount()

    // A genuine sixth mount, queued AFTER the abandoned fifth — while all
    // four original leases are STILL held (nothing has resolved yet), so
    // doc6 has nowhere to go but the queue too.
    const doc6 = mountDashboard(6)
    await waitFor(() =>
      expect(within(doc6.container).getByTestId('base-preview-result-queued')).toBeInTheDocument(),
    )
    expect(callOrder).not.toContain(5)
    expect(callOrder).not.toContain(6)

    // Freeing a slot now must grant doc6 — the real, still-waiting request —
    // not silently do nothing because doc5's abandoned entry is still lodged
    // ahead of it in the queue.
    pendingResolvers[0]?.()

    // MUTATION THIS DIES ON: forgetting to remove doc5's entry from the
    // queue on unmount — a leaked entry would either be (wrongly) granted a
    // worker nobody uses, or (wrongly) sit ahead of doc6 forever, and either
    // way doc6 would never reach `callOrder`.
    await waitFor(() => expect(callOrder).toContain(6))
    await waitFor(() =>
      expect(within(doc6.container).queryByTestId('base-preview-result-queued')).not.toBeInTheDocument(),
    )
    expect(callOrder).not.toContain(5)
    expect(callOrder).toHaveLength(5) // 1, 2, 3, 4, 6 — never 5

    for (const d of docs) d.unmount()
    doc6.unmount()
  })
})
