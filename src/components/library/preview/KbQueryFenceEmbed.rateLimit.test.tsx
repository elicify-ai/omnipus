// KbQueryFenceEmbed.rateLimit.test.tsx — F4 (SILENT-FAILURES-rate-limits-
// dd25339bf.md), the query-fence embed's half. Mirrors
// BasePreview.rateLimit.test.tsx's coverage: the fence's search query
// previously retried a 429 on the blind app-wide curve and, once exhausted,
// showed the same generic "Could not run this query." every other failure
// gets — no mention of throttling anywhere.

import { describe, it, expect, beforeEach } from 'vitest'
import { vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import { RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS } from '@/lib/queryClient'
import { KbQueryFenceEmbed } from './KbQueryFenceEmbed'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, searchVault: vi.fn() }
})

import { searchVault } from '@/lib/api'

function renderEmbed() {
  const client = new QueryClient()
  render(
    <QueryClientProvider client={client}>
      <KbQueryFenceEmbed workspaceId="ws-1" collectionId="kb_1" query="invoices" />
    </QueryClientProvider>,
  )
}

describe('KbQueryFenceEmbed — 429 retry timing and throttled state (F4)', () => {
  beforeEach(() => {
    vi.mocked(searchVault).mockReset()
  })

  it('THE DEFECT: does not retry a 429 as fast as the blind exponential curve would — it honours (a capped) Retry-After', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      let calls = 0
      vi.mocked(searchVault).mockImplementation(async () => {
        calls += 1
        if (calls === 1) throw new ApiError(429, 'rate limited', { retryAfterMs: 5_000 })
        return { collection_id: 'kb-test', notes: [], records: [], views: [], attachments: [], complete: true }
      })
      renderEmbed()

      await vi.waitFor(() => expect(searchVault).toHaveBeenCalledTimes(1))

      // Old blind curve's first retry is ~1000ms; must not have fired yet.
      await vi.advanceTimersByTimeAsync(1_200)
      expect(searchVault).toHaveBeenCalledTimes(1)

      await vi.advanceTimersByTimeAsync(RATE_LIMITED_QUERY_RETRY_DELAY_CAP_MS + 1_000)
      await vi.waitFor(() => expect(searchVault).toHaveBeenCalledTimes(2))
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a throttled state naming the real wait while retrying, instead of the generic "Searching…"', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      let calls = 0
      vi.mocked(searchVault).mockImplementation(async () => {
        calls += 1
        if (calls === 1) throw new ApiError(429, 'rate limited', { retryAfterMs: 3_000 })
        return { collection_id: 'kb-test', notes: [], records: [], views: [], attachments: [], complete: true }
      })
      renderEmbed()

      await vi.waitFor(() => expect(searchVault).toHaveBeenCalledTimes(1))
      const throttled = await vi.waitFor(() => screen.getByTestId('kb-query-fence-throttled'))
      expect(throttled.textContent).toMatch(/retrying in 3s/i)
      expect(screen.queryByTestId('kb-query-fence-loading')).not.toBeInTheDocument()

      await vi.advanceTimersByTimeAsync(3_500)
      await vi.waitFor(() => expect(screen.getByTestId('kb-query-fence-no-results')).toBeInTheDocument())
    } finally {
      vi.useRealTimers()
    }
  })

  it('after exhausting retries, states the query is rate-limited (not the generic failure) and offers Retry', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      vi.mocked(searchVault).mockImplementation(async () => {
        throw new ApiError(429, 'rate limited', { retryAfterMs: 2_000 })
      })
      renderEmbed()

      await vi.advanceTimersByTimeAsync(2_000 * 4 + 2_000)
      await vi.waitFor(() => expect(vi.mocked(searchVault).mock.calls.length).toBeGreaterThanOrEqual(4))

      const rateLimited = await vi.waitFor(() => screen.getByTestId('kb-query-fence-rate-limited'))
      expect(rateLimited.textContent?.toLowerCase()).toContain('rate')
      expect(screen.queryByTestId('kb-query-fence-error')).not.toBeInTheDocument()
      expect(screen.queryByText('Could not run this query.')).not.toBeInTheDocument()

      const retryButton = within(rateLimited).getByRole('button', { name: /retry/i })
      const before = vi.mocked(searchVault).mock.calls.length
      retryButton.click()
      await vi.waitFor(() => expect(vi.mocked(searchVault).mock.calls.length).toBeGreaterThan(before))
    } finally {
      vi.useRealTimers()
    }
  })
})
