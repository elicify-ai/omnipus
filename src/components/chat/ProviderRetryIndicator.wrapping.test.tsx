/**
 * ProviderRetryIndicator.wrapping.test.tsx — 6b UAT finding U3b
 * (validator-report.md issue 4, evidence U3b-b.png): with the Team panel
 * open the chat narrows and the retry line is CUT OFF — the validator saw
 * "fakeprov is rate-limiting requests right now. Retrying automatically in
 * 0:24 (attempt …" with the "(attempt N of 6)" tail unreadable. The visible
 * line span currently carries the Tailwind `truncate` class
 * (overflow-hidden + text-overflow: ellipsis + whitespace-nowrap), which is
 * exactly that clipping.
 *
 * Required behaviour (6b fix round brief): the full retry text — cause
 * clause, countdown, "(attempt N of 6)" — stays readable at the narrow chat
 * width the Team panel leaves: it WRAPS rather than truncates. The vitest
 * pin is the brief's own two-part oracle: (1) no truncation classes/ellipsis
 * on the text node, (2) the full string in the accessible text. jsdom cannot
 * prove the visual wrap; the browser-level confirmation is a UAT re-drive row
 * on the fixed build (stated in the RED report, not a hanging Playwright
 * spec).
 *
 * Fixture mirrors the UAT evidence: fakeprov, rate_limited, attempt 2 of 6,
 * 0:24 remaining. Countdown derivations use fake timers + setSystemTime (the
 * existing ProviderRetryIndicator.test.tsx pattern) so the shown mm:ss is
 * exact.
 */

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import { ProviderRetryIndicator } from './ProviderRetryIndicator'

const SENT_AT = new Date('2026-10-31T17:40:00.000Z')
const RETRY_AT_24 = new Date(SENT_AT.getTime() + 24_000).toISOString()

// The exact sentence U3b cut off — cause clause, countdown, "(attempt N of 6)".
const FULL_LINE =
  'fakeprov is rate-limiting requests right now. Retrying automatically in 0:24 (attempt 2 of 6).'

function renderTeamPanelWidthIndicator() {
  vi.useFakeTimers()
  vi.setSystemTime(SENT_AT) // client clock == sent_at → serverNow == sent_at → 24 s left
  return render(
    <ProviderRetryIndicator
      provider="fakeprov"
      model="fake-model"
      retryAt={RETRY_AT_24}
      sentAt={SENT_AT.toISOString()}
      receivedAt={SENT_AT.toISOString()} // live start: receipt == sent_at
      attempt={2}
      maxAttempts={6}
      errorCode="rate_limited"
    />,
  )
}

/** The span that renders the visible waiting line (not the sr-only copy, not the dot). */
function visibleLineSpan(container: HTMLElement): HTMLElement {
  const span = [...container.querySelectorAll('span')].find(
    (s) => s.textContent != null && s.textContent.includes('Retrying automatically'),
  )
  expect(span, 'instrument: the visible line span renders the waiting line').toBeDefined()
  return span as HTMLElement
}

afterEach(() => {
  vi.useRealTimers()
})

describe('ProviderRetryIndicator — the line wraps instead of truncating (6b U3b: Team panel width)', () => {
  it('the visible line span carries NO truncation classes — it must wrap, never ellipsize', () => {
    const { container } = renderTeamPanelWidthIndicator()
    const line = visibleLineSpan(container)
    // Bans the shorthand (`truncate`), its longhand ingredients, and
    // arbitrary-value spellings (`[text-overflow:ellipsis]`) alike.
    expect(line.className).not.toMatch(
      /truncate|text-ellipsis|ellipsis|whitespace-nowrap|overflow-hidden|text-overflow/,
    )
  })

  it('the full line — cause clause, countdown, "(attempt 2 of 6)" — is in the visible text', () => {
    const { container } = renderTeamPanelWidthIndicator()
    expect(visibleLineSpan(container).textContent).toBe(FULL_LINE)
  })

  it('the full line is in the live region too (MAJ-016: the announcement is the complete line)', () => {
    const { container } = renderTeamPanelWidthIndicator()
    const live = container.querySelector('[aria-live]')
    expect(live, 'instrument: the MAJ-016 live region exists').not.toBeNull()
    expect(live?.textContent).toContain(FULL_LINE)
  })
})
