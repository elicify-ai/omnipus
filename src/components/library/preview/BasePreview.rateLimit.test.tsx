// BasePreview.rateLimit.test.tsx — F4/F5b (SILENT-FAILURES-rate-limits-dd25339bf.md).
//
// F4: the view-result query previously retried a 429 on the app-wide default
// curve (2s/4s/8s, ignoring Retry-After), showed the generic "Evaluating
// view…" spinner throughout, and — once retries were exhausted — the generic
// "Could not evaluate this view." with no mention of throttling. These tests
// reproduce both defects against the real component, then assert the fix:
// retries honour (a capped) Retry-After, a throttled state names the real
// wait while retrying, and the final exhausted state says the view is
// rate-limited with a Retry action.
//
// F5b: a BACKGROUND refetch failure (e.g. a library_changed reload refused
// by the same limiter while a reader has an editor open) must not tear down
// the pane — the last-good rows (and any open RecordFieldEditor) must stay
// mounted, with a small non-blocking banner instead of the full error state.

import { describe, it, expect, vi } from 'vitest'
import { act } from 'react'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS } from '@/lib/queryClient'
import { BasePreview } from './BasePreview'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))

function makeClient() {
  // Deliberately NOT `retry: false` here — the fix wires retry/retryDelay
  // directly onto the view-result query itself (so it's correct in
  // production regardless of the ambient QueryClient), and this suite needs
  // the REAL retry timing to exercise it. A permissive client default keeps
  // every OTHER query in this suite behaving as it always has.
  return new QueryClient()
}

function entry(): LibraryEntry {
  return {
    name: 'Invoices.base',
    path: 'vault/Invoices.base',
    is_dir: false,
    is_hidden: false,
    size: 10,
    modified_at: '2026-09-01T10:00:00Z',
    is_text_editable: true,
  }
}

function baseViews(): KnowledgeBaseViews {
  return {
    base_path: 'vault/Invoices.base',
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'Invoices.base',
    views: [{ name: 'invoices--outstanding', label: 'Outstanding' }],
    unloadable_count: 0,
  }
}

function result(over: Partial<ViewResult> = {}): ViewResult {
  return {
    view: 'invoices--outstanding',
    label: 'Outstanding',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: 'a.md', title: 'INV-A', cells: [], joins: [] }],
    complete: true,
    problems: [],
    ...over,
  }
}

function renderBase(loadViewResult: (ws: string, collectionId: string, view: string) => Promise<ViewResult>) {
  const loadContent = vi.fn().mockResolvedValue({ content: '', is_text: true, too_large: false })
  const loadBaseViews = vi.fn().mockResolvedValue(baseViews())
  const loadGraph = vi.fn().mockResolvedValue({
    collection_id: 'kb_1',
    kind: 'links' as const,
    nodes: [],
    edges: [],
    skipped: [],
    truncated: false,
  })
  render(
    <QueryClientProvider client={makeClient()}>
      <BasePreview
        workspaceId="ws-1"
        entry={entry()}
        loadContent={loadContent}
        loadBaseViews={loadBaseViews}
        loadViewResult={loadViewResult}
        loadGraph={loadGraph}
      />
    </QueryClientProvider>,
  )
}

describe('BasePreview — view-result query, 429 retry timing (F4)', () => {
  it('THE DEFECT: does not retry a 429 as fast as the blind exponential curve would — it honours (a capped) Retry-After', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      let calls = 0
      const loadViewResult = vi.fn(async () => {
        calls += 1
        if (calls === 1) {
          throw new ApiError(429, 'at most 60 per 1m0s. Retry in 5s.', { retryAfterMs: 5_000 })
        }
        return result()
      })
      renderBase(loadViewResult)

      await vi.waitFor(() => expect(loadViewResult).toHaveBeenCalledTimes(1))

      // The OLD (blind) curve's first retry delay is ~1000ms — well inside
      // this window. If the fix is not in place, a second call has already
      // happened by 1200ms. The fix must NOT have retried yet this early.
      await vi.advanceTimersByTimeAsync(1_200)
      expect(loadViewResult).toHaveBeenCalledTimes(1)

      // Advance past the honoured (capped) Retry-After window and confirm
      // the retry does eventually happen and succeeds.
      await vi.advanceTimersByTimeAsync(RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS + 1_000)
      await vi.waitFor(() => expect(loadViewResult).toHaveBeenCalledTimes(2))
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a throttled state naming the real wait while retrying, instead of the generic "Evaluating view…"', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      let calls = 0
      const loadViewResult = vi.fn(async () => {
        calls += 1
        if (calls === 1) {
          throw new ApiError(429, 'rate limited', { retryAfterMs: 4_000 })
        }
        return result()
      })
      renderBase(loadViewResult)

      await vi.waitFor(() => expect(loadViewResult).toHaveBeenCalledTimes(1))

      const throttled = await vi.waitFor(() => screen.getByTestId('base-preview-result-throttled'))
      expect(throttled.textContent).toMatch(/retrying in 4s/i)
      // The generic evaluating spinner must not ALSO be shown.
      expect(screen.queryByText('Evaluating view…')).not.toBeInTheDocument()

      await vi.advanceTimersByTimeAsync(4_500)
      await vi.waitFor(() => expect(screen.getByTestId('viewpart-table')).toBeInTheDocument())
    } finally {
      vi.useRealTimers()
    }
  })

  it('after exhausting retries, states the view is rate-limited (not the generic failure) and offers Retry', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const loadViewResult = vi.fn(async () => {
        throw new ApiError(429, 'at most 60 per 1m0s. Retry in 2s.', { retryAfterMs: 2_000 })
      })
      renderBase(loadViewResult)

      // 3 retries at ~2000ms each (capped well under the ceiling) plus the
      // initial attempt — generous margin.
      await vi.advanceTimersByTimeAsync(2_000 * 4 + 2_000)
      await vi.waitFor(() => expect(loadViewResult.mock.calls.length).toBeGreaterThanOrEqual(4))

      const rateLimited = await vi.waitFor(() => screen.getByTestId('base-preview-result-rate-limited'))
      expect(rateLimited.textContent?.toLowerCase()).toContain('rate')
      expect(screen.queryByTestId('base-preview-result-error')).not.toBeInTheDocument()
      expect(screen.queryByText('Could not evaluate this view.')).not.toBeInTheDocument()

      // Retry button manually re-triggers the query.
      const retryButton = within(rateLimited).getByRole('button', { name: /retry/i })
      const callsBeforeManualRetry = loadViewResult.mock.calls.length
      fireEvent.click(retryButton)
      await vi.waitFor(() => expect(loadViewResult.mock.calls.length).toBeGreaterThan(callsBeforeManualRetry))
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('BasePreview — background refetch failure keeps last-good rows (F5b)', () => {
  it('a background refetch refused by the rate limiter keeps rendering the last-good table, with a non-blocking banner — not the full error state', async () => {
    // Retry-After kept small (real timers, no fake-timer juggling needed for
    // this test) — what matters here is the FIRST background failure's
    // effect on the render, not the retry timing itself (already covered
    // above). A real 429 always carries SOME Retry-After, so this still
    // exercises the isRateLimited() branch of the banner text.
    let calls = 0
    const loadViewResult = vi.fn(async () => {
      calls += 1
      if (calls === 1) return result()
      throw new ApiError(429, 'at most 60 per 1m0s. Retry in 1s.', { retryAfterMs: 300 })
    })
    const loadContent = vi.fn().mockResolvedValue({ content: '', is_text: true, too_large: false })
    const loadBaseViews = vi.fn().mockResolvedValue(baseViews())
    const loadGraph = vi.fn().mockResolvedValue({
      collection_id: 'kb_1',
      kind: 'links' as const,
      nodes: [],
      edges: [],
      skipped: [],
      truncated: false,
    })
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <BasePreview
          workspaceId="ws-1"
          entry={entry()}
          loadContent={loadContent}
          loadBaseViews={loadBaseViews}
          loadViewResult={loadViewResult}
          loadGraph={loadGraph}
        />
      </QueryClientProvider>,
    )

    // Initial successful load.
    await screen.findByTestId('viewpart-table')
    expect(screen.getByText('INV-A')).toBeInTheDocument()

    // Simulate a background refetch (e.g. triggered by a library_changed
    // frame or focus event) that the rate limiter refuses.
    await act(async () => {
      await client.refetchQueries({
        queryKey: ['library', 'ws-1', 'knowledge', 'view-result', 'kb_1', 'invoices--outstanding'],
      })
    })

    // The table must STILL be showing the last-good row — not torn down.
    expect(await screen.findByTestId('viewpart-table')).toBeInTheDocument()
    expect(screen.getByText('INV-A')).toBeInTheDocument()
    // The full-pane error state must never have replaced it.
    expect(screen.queryByTestId('base-preview-result-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('base-preview-result-rate-limited')).not.toBeInTheDocument()

    // A small, non-blocking notice must say the refresh was refused/rate-limited.
    const banner = await screen.findByTestId('base-preview-result-refresh-failed')
    expect(banner.textContent?.toLowerCase()).toMatch(/rate|limit|throttl|refused/)
  })
})
