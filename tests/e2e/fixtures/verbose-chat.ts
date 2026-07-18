/**
 * verbose-chat.ts — shared helper to seed the "Verbose chat" preference ON
 * before app load.
 *
 * BACKGROUND: commit 8e1bf1b9 hid delegation SubagentBlock cards from the
 * chat thread by default — `shouldRenderSubagentSpan` (src/lib/toolVisibility.ts)
 * now returns `verboseChatEnabled`, which defaults to `false`
 * (src/store/chatPreferences.ts's `useChatPreferencesStore`). Specs that use
 * `[data-testid="subagent-collapsed"]` (or a sibling SubagentBlock testid) as
 * a THREAD-based signal must opt into verbose chat first, or the card never
 * renders and the wait hangs to its full timeout.
 *
 * STORAGE KEY / SHAPE — verified directly against src/store/chatPreferences.ts
 * (the zustand `persist` middleware's `name` option) and
 * src/store/chatPreferences.test.ts's own read/write persistence assertions
 * (lines ~63-91), which round-trip exactly this shape:
 *
 *   localStorage key:   "omnipus-chat-preferences"
 *   localStorage value: {"state":{"verboseChatEnabled":true},"version":0}
 *
 * This is zustand persist's standard wire format: `state` holds the
 * `partialize`d slice (chatPreferences.ts only persists `verboseChatEnabled`
 * — see its `partialize` option), and `version` defaults to `0` because the
 * store's `persist()` config does not pass a `version` option.
 *
 * MECHANISM: `page.addInitScript()` runs before the page's own scripts on
 * every subsequent navigation (including reload) for the rest of that page's
 * lifetime — the same technique tests/e2e/profile-fontsize.spec.ts already
 * uses to pre-seed a different localStorage-backed preference
 * (`omnipus_pref_font_size`) ahead of first mount. Call this BEFORE the
 * test's first `page.goto()` so the store rehydrates with verbose chat
 * already on — no UI toggle round-trip required.
 */

import type { Page } from '@playwright/test'

/** The zustand `persist` middleware's storage key for chat display preferences. */
export const CHAT_PREFERENCES_STORAGE_KEY = 'omnipus-chat-preferences'

/**
 * Seed localStorage so `useChatPreferencesStore`'s persist middleware
 * rehydrates with `verboseChatEnabled: true` on every navigation from this
 * point on (including reload / re-open-session round trips).
 *
 * Idempotent and safe to call more than once on the same page — each call
 * registers another init script, and all of them write the identical value.
 *
 * Call this BEFORE the first `page.goto()` in a test — an init script only
 * applies to navigations that happen after it is registered.
 */
export async function enableVerboseChat(page: Page): Promise<void> {
  await page.addInitScript(() => {
    localStorage.setItem(
      'omnipus-chat-preferences',
      JSON.stringify({ state: { verboseChatEnabled: true }, version: 0 }),
    )
  })
}
