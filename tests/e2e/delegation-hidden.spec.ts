/**
 * delegation-hidden.spec.ts — E2E regression net for the DEFAULT (non-verbose)
 * chat display policy introduced by commit 8e1bf1b9 (Fix 2, user-approved
 * 2026-07-16): a completed delegation turn's SubagentBlock card is hidden
 * from the chat thread unless "Verbose chat" is turned on
 * (`shouldRenderSubagentSpan`, src/lib/toolVisibility.ts — gates on
 * `verboseChatEnabled`, default false, src/store/chatPreferences.ts's
 * `useChatPreferencesStore`).
 *
 * The four sibling specs affected by 8e1bf1b9 (subagent.spec.ts,
 * handoff.spec.ts, replay-fidelity.spec.ts test (b), and
 * cancel-cross-channel.spec.ts) all opt INTO verbose chat
 * (tests/e2e/fixtures/verbose-chat.ts's `enableVerboseChat`) so their own
 * sub-turn MECHANICS assertions (spawn/expand/nesting/step-count/cancel/
 * replay) keep working against the thread-based
 * `[data-testid="subagent-collapsed"]` signal. None of them assert the
 * DEFAULT policy itself — this file is that test: no card by default, card
 * appears once verbose chat is turned on.
 *
 * ── Why this is a LIVE-delegation test, not a transcript-seeded one ────────
 *
 * The original design for this file used the same deterministic,
 * transcript-seeded WS-replay seam replay-fidelity.spec.ts's test (b)
 * established (write transcript.jsonl directly with a `spawn` call bracketing
 * a nested `shell` call via `parent_tool_call_id`, then open the session so
 * pkg/gateway/replay.go's streamReplay reconstructs the subagent span).
 *
 * That seam was tried and DID NOT reconstruct a span in this checkout, verified
 * two independent ways against a real scratch gateway (build/omnipus,
 * commit 8e1bf1b9, 2026-07-16):
 *   1. DOM inspection after opening a session seeded with the exact dataset
 *      replay-fidelity.spec.ts test (b) uses: the nested entry
 *      (`entry-nested-1`, empty content, one `shell` tool call carrying
 *      `parent_tool_call_id: "c1"`) rendered as its OWN top-level
 *      `[data-testid="assistant-message"]` bubble with its own "Copy message"
 *      button — not as a nested step inside a
 *      `[data-testid="subagent-collapsed"]` card. No `subagent-collapsed`,
 *      `subagent-expanded`, or `activity-bar` testid appeared anywhere in the
 *      DOM.
 *   2. Gateway `--debug` logs: navigating directly from `/` to
 *      `/#/sessions/{id}` (the same deep-link seam tool-order.spec.ts's
 *      `openSessionByDeepLink` uses) never produced a `"ws: replay_start"`
 *      structured log line — i.e. `pkg/gateway/websocket.go`'s
 *      `handleAttachSession` (and therefore `streamReplay`'s span-bracketing
 *      logic) was never invoked for that navigation at all. The rendered
 *      content most likely came from `fetchSessionDetail`'s REST response via
 *      the route loader (`src/routes/_app/sessions.$sessionId.tsx`), not from
 *      WS replay — plausibly because the loader's own `setActiveSession` call
 *      makes `SessionRoute`'s attach-effect guard
 *      (`activeSessionId !== session.id`) see the session as "already
 *      attached" and skip sending `attach_session` entirely.
 *
 * This reproduced identically across three separate sessions/attempts, and
 * `pkg/gateway/replay.go`'s own `buildSpawnIDsWithChildren` /
 * `streamReplay` source (read directly) looks correct for this exact
 * dataset — so the gap is not in that function, and not something this
 * spec's data could route around. Root-causing further (whether the
 * fix belongs in `SessionRoute`'s attach guard or elsewhere) is out of
 * qa-lead scope (test-only, no src/pkg changes) — flagged as a finding in
 * this run's report instead of worked around, per this task's explicit
 * instruction to "report the limitation honestly" if replay doesn't rebuild
 * spans reliably. Notably, replay-fidelity.spec.ts's own test (b) — which
 * depends on this exact mechanism — has never been observed to actually pass
 * end-to-end in this checkout: it's independently blocked before reaching
 * this code path by session-setup.ts's `openSession()` clicking a button
 * ("Open sessions panel") that no longer exists in current `src/` (the same
 * drift tool-order.spec.ts's header documents), so this replay gap was never
 * surfaced by CI.
 *
 * Given that, this spec uses the LIVE delegation code path instead (the same
 * one subagent.spec.ts / handoff.spec.ts already rely on and that this
 * whole task treats as trustworthy) — but only ONE live delegate call, not
 * two: it never reloads or re-opens the session. A live delegation's span
 * lives in `useChatStore`'s in-memory state the instant `subagent_start`/
 * `subagent_end` arrive over the WS (`pkg/gateway/websocket.go`'s
 * `eventForwarder`, a different code path from replay's `streamReplay` and
 * unaffected by the gap above). Toggling verbose chat via Settings and
 * navigating back is a same-page, client-side HashRouter route change (no
 * `page.reload()`, no re-navigation to `/#/sessions/{id}`) — the Zustand
 * store's in-memory `message.spans` survives untouched, so the SAME
 * already-completed live span is what `shouldRenderSubagentSpan` re-evaluates
 * against on each side of the toggle. This isolates exactly the variable
 * under test (the verbose-chat gate) without re-exercising the broken replay
 * path a second time.
 *
 * Cost note: like subagent.spec.ts / handoff.spec.ts / cancel-cross-channel.spec.ts,
 * this test requires OPENROUTER_API_KEY_CI and a real delegate round-trip
 * (observed 150-300s under suite load per those files' own comments) — it is
 * NOT a cheap/deterministic test despite living next to ones that are. Not
 * run as part of this change's verification pass for the same reason the
 * four live-LLM specs above weren't re-run — `npx playwright test
 * delegation-hidden.spec.ts --list` was used instead to confirm it parses.
 */

import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { chatInput, assistantMessages, selectAgent } from './fixtures/selectors';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).
// Deliberately does NOT call enableVerboseChat — this spec asserts the
// DEFAULT (verbose-off) policy for its first half.

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

// Helper: assert OPENROUTER_API_KEY_CI is present (mirrors subagent.spec.ts's
// requireApiKey — this test drives a real LLM delegate call).
function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error(
      'BLOCKED: OPENROUTER_API_KEY_CI not set. ' +
      'This test requires a real LLM. ' +
      'The key must be present in CI — see tests/e2e/README.md prerequisites.',
    );
  }
}

// Helper: start a fresh chat session and route to Jim (delegate-capable —
// mirrors subagent.spec.ts's startFreshChat; Mia's guide persona refuses to
// delegate, see that file's own comment).
async function startFreshChat(page: import('@playwright/test').Page): Promise<void> {
  const newChat = page.getByRole('banner').getByRole('button', { name: 'New Chat' });
  if (await newChat.isVisible({ timeout: 5_000 })) {
    await newChat.click();
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });
  }
  await selectAgent(page, /Jim/i);
}

test(
  'default policy: a completed live delegation shows no subagent-collapsed card with verbose chat OFF, then shows one once verbose chat is turned on (no reload)',
  async ({ page }) => {
    requireApiKey();
    // 300s budget: one live delegate round-trip (parent delegate + subagent's
    // bash echo) plus the Settings toggle + reopen. Matches the sibling
    // specs' documented 150-300s range for a single delegate call under
    // suite load (see handoff.spec.ts's (b) comment for the same figure).
    test.setTimeout(300_000);

    await startFreshChat(page);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });

    // Given: verbose chat is OFF — the default for a fresh browser context.
    // playwright.config.ts's storageState (tests/e2e/fixtures/.auth/admin.json)
    // seeds only auth-related localStorage keys; it carries no
    // "omnipus-chat-preferences" entry, and each Playwright test runs in its
    // own fresh browser context, so useChatPreferencesStore.verboseChatEnabled
    // rehydrates to its default, `false` (src/store/chatPreferences.ts).

    // When: a real delegation completes. Deterministic prompt mirrors
    // handoff.spec.ts's (b) — explicit tool name, exact arguments, no prose.
    await input.fill(
      [
        'Call the `delegate` tool exactly once, right now, with these arguments:',
        '  label: "delegation-hidden default-policy test"',
        '  task: "You are the subagent. Call the `bash` tool ONCE with action=\\"run\\" and command=\\"echo hello\\". Then reply with the single word \\"done\\". Do not use any other tool."',
        'Do not reply in prose. Do not call any other tool. Call delegate now.',
      ].join('\n'),
    );
    await input.press('Enter');

    // "Turn complete" signal — the parent's final assistant message lands.
    // 240s leaves 60s of the 300s test-level ceiling for the assertions below.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 240_000 });

    // Then: the thread renders no subagent-collapsed card by default, even
    // though a real delegation genuinely happened (the differentiation this
    // whole file exists to prove — not "nothing rendered because nothing was
    // constructed"). Assert-then-hold rules out a late-arriving card.
    const collapsedBlocks = page.locator('[data-testid="subagent-collapsed"]');
    await expect(collapsedBlocks).toHaveCount(0);
    await page.waitForTimeout(1_000);
    await expect(collapsedBlocks).toHaveCount(0);

    // When: verbose chat is turned ON via the real user-facing toggle
    // (Settings -> Chat) — a client-side HashRouter route change, NOT a
    // page.reload(): the Zustand chat store's in-memory spans for the turn
    // above are untouched by this navigation (see file header).
    await page.goto('/#/settings');
    const chatTab = page.locator('button[role="tab"]', { hasText: 'Chat' });
    await expect(chatTab).toBeVisible({ timeout: 10_000 });
    await chatTab.click();

    const verboseSwitch = page.getByRole('switch', { name: 'Verbose chat' });
    await expect(verboseSwitch).toBeVisible({ timeout: 10_000 });
    await expect(verboseSwitch).toHaveAttribute('aria-checked', 'false');
    await verboseSwitch.click();
    await expect(verboseSwitch).toHaveAttribute('aria-checked', 'true');

    // And when: navigating back to the SAME chat (browser back — restores
    // the workspace-scoped chat URL Jim's session lives at, no session id in
    // the URL per cancel-cross-channel.spec.ts's own comment on this IA — no
    // re-fetch, no re-open, no replay).
    await page.goBack();
    await expect(input).toBeVisible({ timeout: 15_000 });

    // Then: the SAME already-completed delegation's card now renders —
    // proving the card's visibility tracks verboseChatEnabled for identical
    // underlying span data, not some other confound.
    await expect(collapsedBlocks.first()).toBeVisible({ timeout: 15_000 });
  },
);
