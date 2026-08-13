// selectors.test.ts — vitest coverage for waitForLiveSink's pure polling logic
// (SMALL-2 fix, external review 2026-08-13).
//
// This fixture lives under tests/e2e/ (Playwright, not vitest's normal scope
// per vite.config.ts's `include: ['src/**/*.test.{ts,tsx}']`) — vitest is run
// directly against this file's explicit path, not discovered via that glob.
// `waitForLiveSink` only ever calls `.locator()`, `.isVisible()`, `.first()`,
// `.textContent()`, and `page.waitForTimeout()` — all duck-typeable against a
// plain fake object, so no real Playwright browser/page is ever launched here.
//
// Bug being proven fixed: `data-testid="browser-live-img"` (the JPEG
// fallback sink) was deleted outright by ADR-061 and can never appear again,
// so `waitForLiveSink`'s only way to end early used to be the (also
// unreachable) `img` branch — every genuine WebRTC failure, even one
// BrowserLiveView had already reported via its own Retry-button error state
// within seconds, rode out the FULL polling budget (default 90s) before
// falling through to a `null` "capture-path failure" verdict that actively
// misdiagnosed what happened. The fix polls for that Retry-button state and
// throws immediately with the real reason instead.

import { describe, it, expect, vi } from 'vitest'
import type { Page } from '@playwright/test'
import { waitForLiveSink } from './selectors'

interface FakeLocator {
  isVisible: () => Promise<boolean>
  first: () => FakeLocator
  locator: (selector: string) => FakeLocator
  textContent: () => Promise<string | null>
}

function makeFakeLocator(overrides: Partial<FakeLocator> = {}): FakeLocator {
  const loc: FakeLocator = {
    isVisible: async () => false,
    first: () => loc,
    locator: () => makeFakeLocator(),
    textContent: async () => null,
    ...overrides,
  }
  return loc
}

/** Maps a testid substring (as it appears in the CSS attribute selector
 * `waitForLiveSink`/its helpers build) to the fake locator that selector
 * should resolve to — a plain substring match is enough since every selector
 * this file builds embeds the literal testid string. */
function makeFakePage(
  byTestId: Record<string, FakeLocator>,
  waitForTimeout: () => Promise<void> = vi.fn(async () => undefined),
): Page {
  return {
    locator: (selector: string) => {
      for (const [testId, loc] of Object.entries(byTestId)) {
        if (selector.includes(testId)) return loc
      }
      return makeFakeLocator()
    },
    waitForTimeout,
  } as unknown as Page
}

describe('waitForLiveSink — SMALL-2 fix (fail fast + honest diagnosis on a reported WebRTC failure)', () => {
  it('throws immediately with the real reported reason once the Retry button is visible — never rides out the polling budget', async () => {
    // A real (if tiny) delay, not an instantly-resolving stub: if this ever
    // regresses back to the pre-fix behavior (which never returns early on a
    // Retry sighting), the loop below would otherwise spin as fast as the
    // event loop allows for the full `timeoutMs`, which reliably OOMs the
    // process rather than just failing the assertion — confirmed by actually
    // reverting the fix and observing exactly that crash while writing this
    // test. `timeoutMs` is kept small too, so an unbounded-but-real-paced
    // regression here fails in ~2s rather than 90s.
    const waitForTimeout = vi.fn(() => new Promise<void>((resolve) => setTimeout(resolve, 5)))
    const retryLoc = makeFakeLocator({
      isVisible: async () => true,
      locator: () =>
        makeFakeLocator({
          textContent: async () => '  Live video connection failed (ice-failed). Retry, or reload the page.  Retry ',
        }),
    })
    const page = makeFakePage(
      {
        'browser-live-video': makeFakeLocator({ isVisible: async () => false }),
        'browser-live-retry': retryLoc,
        'browser-live-img': makeFakeLocator({ isVisible: async () => false }),
      },
      waitForTimeout,
    )

    await expect(waitForLiveSink(page, 2_000)).rejects.toThrow(/ice-failed/)
    // FAIL FAST: the old code could only ever conclude by polling in
    // 500ms steps until the full budget elapsed. Proving this never once
    // calls the poll-delay is what proves it did NOT ride out any part of
    // that budget — it threw on the very first pass.
    expect(waitForTimeout).not.toHaveBeenCalled()
  })

  it('returns "video" the moment the video sink is visible, even with a stale Retry button also present', async () => {
    const page = makeFakePage({
      'browser-live-video': makeFakeLocator({ isVisible: async () => true }),
      'browser-live-retry': makeFakeLocator({ isVisible: async () => true }),
    })

    await expect(waitForLiveSink(page, 2_000)).resolves.toBe('video')
  })

  it('returns null after the budget elapses when neither the video sink nor a reported error ever appears (genuine capture-path stall)', async () => {
    const page = makeFakePage(
      {
        'browser-live-video': makeFakeLocator({ isVisible: async () => false }),
        'browser-live-retry': makeFakeLocator({ isVisible: async () => false }),
        'browser-live-img': makeFakeLocator({ isVisible: async () => false }),
      },
      vi.fn(async () => undefined), // stubbed so the real 500ms polling delay never actually elapses
    )

    await expect(waitForLiveSink(page, 5)).resolves.toBeNull()
  })
})
