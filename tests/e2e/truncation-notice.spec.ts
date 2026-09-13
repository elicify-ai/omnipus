/**
 * truncation-notice.spec.ts — ADR-087 D1/WP F.
 *
 * A truncated assistant entry (the provider's output-token limit cut it off
 * before it finished) must render a "(cut off at the output limit)" suffix
 * on reload — the WS-replay-path counterpart to `MessageItem.test.tsx` /
 * `ChatScreen.truncation-notice.test.tsx`'s unit-level coverage of the same
 * rule (ADR-087 §3 D1).
 *
 * Seeding technique: writes a synthetic `transcript.jsonl` entry directly to
 * the session directory under `$OMNIPUS_HOME` (bypassing the LLM entirely —
 * `truncated`/`truncation_reason` are persisted fields this fixture can set
 * without ever provoking a real provider truncation), then opens the session
 * via its deep-link route to drive a real WS `attach_session` + replay. This
 * is the exact pattern `replay-fidelity.spec.ts`, `handoff.spec.ts`, and
 * `multi-turn-render.spec.ts` already use for the sibling "(interrupted)"
 * label (found via `grep 'text=(interrupted)' tests/e2e/*.spec.ts`) —
 * `seedAndOpenSession`/`seedTranscript` in `./fixtures/session-setup.ts`.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

test(
  'a truncated (max_output_tokens) assistant entry shows the cut-off notice after reload',
  async ({ page }) => {
    const now = Date.now()
    await seedAndOpenSession(page, 'truncation-notice-cutoff', [
      {
        id: 'user-trunc-1',
        role: 'user',
        content: 'Write a very long explanation.',
        timestamp: new Date(now - 4000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-trunc-1',
        role: 'assistant',
        content: 'Here is the start of a long explanation that gets cut off before',
        timestamp: new Date(now - 3000).toISOString(),
        agent_id: 'mia',
        truncated: true,
        truncation_reason: 'max_output_tokens',
      },
    ])

    // Same locator shape the "(interrupted)" e2e coverage uses
    // (cancel-cross-channel.spec.ts) — the InterruptedMessageMarkers
    // fallback (src/components/chat/ChatScreen.tsx) renders this text
    // OUTSIDE the scroll viewport specifically so Playwright can find it
    // without scrolling.
    const cutOffLabel = page.locator('text=(cut off at the output limit)')
    await expect(cutOffLabel.first()).toBeVisible({ timeout: 10_000 })

    // Never both — the interrupted label must not also render for a
    // max_output_tokens truncation (ADR-087 D1 precedence).
    await expect(page.locator('text=(interrupted)')).toHaveCount(0)
  },
)

test(
  'D1 precedence: a legacy truncated entry with NO reason shows "(interrupted)", not the cutoff notice',
  async ({ page }) => {
    const now = Date.now()
    await seedAndOpenSession(page, 'truncation-notice-legacy-cancel', [
      {
        id: 'user-trunc-2',
        role: 'user',
        content: 'Do something long.',
        timestamp: new Date(now - 4000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-trunc-2',
        role: 'assistant',
        content: 'Working on it and then',
        timestamp: new Date(now - 3000).toISOString(),
        agent_id: 'mia',
        // Legacy shape: truncated:true, no truncation_reason at all — every
        // entry written before this field existed predates it and was
        // always a cancel (ADR-087 D2's legacy-default rule).
        truncated: true,
      },
    ])

    const interruptedLabel = page.locator('text=(interrupted)')
    await expect(interruptedLabel.first()).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('text=(cut off at the output limit)')).toHaveCount(0)
  },
)
