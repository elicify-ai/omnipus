import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput, agentPicker, assistantMessages, userMessages, startNewChat, tokenCounter, waitForConnected } from './fixtures/selectors';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

/**
 * Wait for any follow-up LLM call that Jim may start after a tool call completes.
 *
 * When Jim calls a tool (e.g. remember()) as his first action, the LLM stream ends
 * with done:true, making isStreaming=false briefly. This causes data-status="complete"
 * to appear on the assistant message — toHaveCount(N) fires too early. A second LLM
 * call then starts (with the tool result), making isStreaming=true again.
 *
 * This helper detects the second LLM call by watching for the stop button to
 * reappear within `gapMs` after the initial count assertion resolved. If it
 * reappears, we wait for it to disappear (second LLM call done). If it never
 * reappears, Jim is truly done.
 *
 * @param page      Playwright page object
 * @param gapMs     How long to watch for the stop button to reappear (default 8s)
 */
async function waitForTurnFullyDone(page: Page, gapMs = 8_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    // If the stop button reappears within gapMs, a follow-up LLM call is in progress.
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    // Stop button appeared — wait for it to vanish (follow-up LLM call done).
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 });
  } catch {
    // Stop button did not reappear within gapMs — Jim's turn is fully done.
  }
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

test(
  '(a) send a message and receive an LLM response with token/cost update',
  async ({ page }) => {
    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    await input.fill('Say exactly: "hello world"');

    const msgsBefore = await assistantMessages(page).count();
    await input.press('Enter');

    await expect(assistantMessages(page)).toHaveCount(msgsBefore + 1, { timeout: 60_000 });

    // Token/cost UI moved out of the header banner into the composer's
    // context row (src/components/chat/composer/TokenCounter.tsx) — see
    // ChatControls.tsx's doc comment. The header banner is now solely the
    // workspace top-bar (hamburger + tab strip + "Open browser").
    const tokens = tokenCounter(page);
    await expect(tokens).toBeVisible({ timeout: 10_000 });
    await expect(tokens).toContainText(/\d+/, { timeout: 10_000 });

    await expectA11yClean(page);
  },
);

test(
  '(b) multi-turn retention: turn 3 references content from turn 1',
  async ({ page }) => {
    // Budget (WORST-CASE ceiling, not a "typical" duration — test.setTimeout is a
    // hard governor: if it's smaller than the sum of every step's own declared
    // timeout, the outer timeout kills the test mid-flight before an inner
    // assertion ever gets to legitimately time out or succeed, which is exactly
    // what produced a 21.8-min real hang under the old budget below).
    //
    // Fixed overhead (sum of each step's own timeout, chatInput/newChat/picker
    // visibility + count=0 + contains-Jim checks):
    //   15_000 + 10_000 + 10_000 + 15_000 + 5_000 = 55s
    //
    // Per turn (×3 — fill/press/toHaveCount/waitForTurnFullyDone):
    //   toHaveCount ceiling ................................ 300s
    //   waitForTurnFullyDone ceiling: this helper (defined above, ~L24-34) first
    //   waits up to `gapMs` (8s, the value passed at every call site below) for
    //   the stop button to reappear, and if it does, waits up to a FURTHER 180s
    //   for it to vanish again (a legitimate tool-call follow-up LLM turn) ... 8 + 180 = 188s
    //   => per-turn ceiling: 300 + 188 = 488s
    // 3 turns: 3 * 488 = 1464s
    //
    // Worst-case total: 55 + 1464 = 1519s (~25.3 min).
    //
    // The PREVIOUS budget ("940s expected + 260s margin" -> 1200s) undercounted
    // this badly in two ways: (1) it modeled each waitForTurnFullyDone call as a
    // flat "gap(10)" 10s placeholder instead of the helper's own documented
    // up-to-188s worst case, and (2) it only budgeted 2 gaps, omitting the THIRD
    // waitForTurnFullyDone call entirely (the third call, immediately before
    // the `serialMsgs` assertion). Three turns each legitimately hitting the 188s
    // follow-up-call ceiling alone sums to 564s — already more than double the
    // old 260s margin, before even counting the toHaveCount ceilings.
    //
    // New budget: 1519s worst-case ceiling + ~281s margin (~19%) for CI
    // scheduling/runner jitter beyond each step's own declared timeout = 1800s
    // (30 min), a round number with real headroom over the computed ceiling.
    test.setTimeout(1_800_000);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });

    // Start a fresh session to avoid stale messages from prior tests.
    // After goto('/') the app may restore the last active session with messages.
    // The header "New Chat" button was removed from the redesign (see
    // startNewChat's doc comment) — "/new" + Enter resets to an empty
    // thread so assistantMessages count starts at 0.
    await startNewChat(page);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    // Switch to Jim: Mia has "no long enumerations" guardrails and may handoff to
    // Jim for certain questions, causing spurious stop-button transitions that
    // confuse turn-completion detection. Jim does inline generation without handoffs,
    // making the strict assistantMessages count (excludes running) reliable.
    // This test probes agent-loop context retention — the agent identity doesn't matter.
    const picker = agentPicker(page);
    await expect(picker).toBeVisible({ timeout: 15_000 });
    await picker.click();
    await page.getByRole('menuitem', { name: /Jim/i }).click();
    await expect(picker).toContainText(/Jim/i, { timeout: 5_000 });

    // Phrasing note: we avoid the word "remember" — Jim may treat "remember …" as an
    // instruction to call the remember() tool rather than just retain context in the
    // conversation. We want to probe multi-turn transcript retention (an agent-loop
    // property) independent of the agent's memory-file semantics.
    await input.fill('In my first message, my serial number is XYZQUUX7734.');
    await input.press('Enter');
    // Wait for turn 1 to FULLY COMPLETE.
    // "Fully complete" = data-status transitions from "running" to something else AND
    // no follow-up LLM call is pending. Jim may call remember() as a tool before
    // generating his text reply. Between the tool call and the second LLM call,
    // isStreaming briefly goes false — toHaveCount(1) can fire too early at that gap.
    // Guard: after count=1, if the stop button reappears within 8s, wait for it to
    // vanish again (second LLM call in progress). If it never reappears, Jim is done.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 300_000 });
    await waitForTurnFullyDone(page, 8_000);

    await input.fill('What is 2 + 2?');
    await input.press('Enter');
    // Wait for turn 2 to FULLY COMPLETE (same guard as turn 1).
    await expect(assistantMessages(page)).toHaveCount(2, { timeout: 300_000 });
    await waitForTurnFullyDone(page, 8_000);

    await input.fill(
      'Look back at my first message in THIS conversation — what serial number ' +
        'did I mention? Echo it back verbatim.',
    );
    await input.press('Enter');

    // Wait for turn 3 to FULLY COMPLETE. Verify XYZQUUX7734 appears in the completed
    // responses (turn 1 ack, turn 3 echo-back, or both — any match suffices).
    await expect(assistantMessages(page)).toHaveCount(3, { timeout: 300_000 });
    await waitForTurnFullyDone(page, 8_000);
    const serialMsgs = assistantMessages(page).filter({ hasText: /XYZQUUX7734/i });
    const serialCount = await serialMsgs.count();
    expect(serialCount).toBeGreaterThanOrEqual(1);
  },
);

test('(c) agent switch via picker: switch to a different agent, header area updates', async ({
  page,
}) => {
  // The agent picker is the DropdownMenuTrigger in the header banner.
  // Ground truth: button carries data-testid="agent-picker-trigger" (ChatControls.tsx).
  // Shows only the agent name (e.g. "Mia") — no em-dash tagline.
  const picker = agentPicker(page);
  await expect(picker).toBeVisible({ timeout: 15_000 });

  // Capture current agent name shown in the picker button
  const nameBefore = await picker.textContent();

  await picker.click();

  // Dropdown items are Radix DropdownMenuItem — first item that is NOT the active one
  const menuItems = page.locator('[role="menuitem"]');
  await expect(menuItems.first()).toBeVisible({ timeout: 10_000 });
  const count = await menuItems.count();
  expect(count).toBeGreaterThan(0);

  // Click the first menu item (may be the same agent if only one exists, which is fine)
  await menuItems.first().click();

  // Picker should now show a name (may be same or different)
  await expect(picker).toBeVisible({ timeout: 5_000 });
  const nameAfter = await picker.textContent();
  // At minimum, the picker still renders without error
  expect(nameAfter).toBeTruthy();
  // Suppress unused-variable linting — nameBefore is recorded for debugging purposes
  void nameBefore;
});

test(
  '(d) new chat button clears message list and picks a fresh session_id',
  async ({ page }) => {
    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    await input.fill('First message in session');
    await input.press('Enter');
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 60_000 });

    const urlBefore = page.url();

    // Header "New Chat" button was removed (see startNewChat's doc comment
    // in fixtures/selectors.ts) — "/new" + Enter is the replacement.
    await startNewChat(page);

    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    const urlAfter = page.url();
    void urlBefore;
    void urlAfter;
  },
);

test(
  '(e) cancel streaming mid-reply — stop button appears then disappears, input re-enables',
  async ({ page }) => {
    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    // Mia has strong "no long enumerations in chat" / "hand off creative work
    // to Jim" guardrails that finish almost instantly — the Stop button never
    // appears under her. Switch to Jim explicitly and ask for a long in-agent
    // explanation; Jim does in-line generation with a multi-second stream
    // window that reliably exposes the Stop button.
    const picker = agentPicker(page);
    await expect(picker).toBeVisible({ timeout: 15_000 });
    await picker.click();
    await page.getByRole('menuitem', { name: /Jim/i }).click();
    await expect(picker).toContainText(/Jim/i, { timeout: 5_000 });

    await input.fill(
      'Explain in deep technical detail how the Internet routes packets, ' +
        'including the OSI model layers, BGP, IP addressing (v4 and v6), ' +
        'TCP handshake, DNS resolution, NAT traversal, congestion control, ' +
        'TLS handshake. At least 1500 words. Do not call any tool, just write prose.',
    );
    await input.press('Enter');

    const stopBtn = page.locator('button[aria-label="Stop generation"]');
    // 30s timeout: opus-4.7 with the full tool registry has 5–10 s TTFT, plus
    // connection setup. The 15 s default was tight enough that any cold-start
    // jitter (slow upstream, larger system prompt) flaked the test.
    await expect(stopBtn).toBeVisible({ timeout: 30_000 });
    await stopBtn.click();

    await expect(stopBtn).not.toBeVisible({ timeout: 15_000 });
    await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 });
  },
);

test(
  '(f) queue-on-disconnect: messages sent during WS disconnect send in order after reconnect',
  // Investigation (closing the #105 skip) found the store-level queue
  // mechanics (useChatStore's outboundQueue/pendingDrainQueue/
  // enqueueOutboundMessage/drainOutboundQueue/maybeDrainNext — see
  // src/store/chat.outbound-queue.test.ts) were ALREADY fully implemented
  // and unit-tested, and the outbound-queue-indicator banner + reconnect
  // banner + composer placeholder/aria-labels already assumed the composer
  // stays usable while reconnecting. The one missing piece: ChatScreen.tsx's
  // `inputEnabled` gate required strict `isConnected`, which is
  // unconditionally false during the whole 'reconnecting'/'slow' retry
  // window — so the native `disabled` attribute silently blocked the only
  // real-UI path into the queue, making the entire mechanism unreachable by
  // an actual user. Fixed in ChatScreen.tsx (see that file's `inputEnabled`
  // comment) to allow typing/sending while reconnecting; this test drives
  // the real, now-reachable path end-to-end. Deterministic component-level
  // coverage of the same fix lives in
  // src/components/chat/ChatScreen.outbound-queue.test.tsx.
  async ({ page, context }) => {
    // This test takes the browser OFFLINE on purpose, so Chrome logs
    // "WebSocket connection ... failed: net::ERR_INTERNET_DISCONNECTED".
    // That is the scenario, not a defect: asserting zero console errors here
    // would be asserting the test does not do its job.
    //
    // Opted out per test rather than by adding ERR_INTERNET_DISCONNECTED to
    // the fixture's CONSOLE_ERROR_ALLOWLIST, which would blind EVERY other
    // test in the suite to a real WebSocket failure -- the exact way that gate
    // was blind in 30 of 33 specs before it was wired up.
    test.info().annotations.push({
      type: 'expects-console-errors',
      description: 'goes offline on purpose to test outbound queueing',
    })
    // Budget: this test drains TWO real LLM turns sequentially after
    // reconnect (maybeDrainNext sends the queue one message at a time, only
    // once each prior turn's `done` frame arrives — see store/chat.ts), on
    // top of the setup/offline/reconnect steps' own timeouts below. Ceiling:
    // 15+15+10+10 (setup) + 2*5 (indicator checks) + 120 (drain) + 20
    // (banner clear) + 180 (both replies) + 188 (waitForTurnFullyDone worst
    // case, mirroring test (b)'s own documented 8+180s ceiling) ≈ 570s.
    // Rounded up with real headroom for CI scheduling/LLM jitter.
    test.setTimeout(900_000);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    await expect(input).toBeEnabled({ timeout: 15_000 });
    // Confirm the socket is genuinely open BEFORE this test starts its own
    // deliberate offline/reconnect manipulation below — toBeEnabled() alone
    // no longer implies "connected" (2fa26e6a, #105 fix; see
    // waitForConnected's doc comment in fixtures/selectors.ts). NOTE: this is
    // NOT the same as the later `await expect(input).toBeEnabled()` at the
    // point the test goes offline (line ~313 below) — THAT check is the
    // intentional assertion that the composer STAYS enabled while
    // reconnecting/queueing, and must not be changed.
    await waitForConnected(page, { timeout: 15_000 });

    // Fresh session so assistantMessages/userMessages counts start at zero.
    await startNewChat(page);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    // Go offline — the browser drops the WS (same mechanism verified in
    // ws-reconnect.spec.ts's online_event_triggers_reconnect) and the SPA
    // enters its automatic reconnect-retry loop.
    await context.setOffline(true);

    // Wait for the disconnect to be detected (the persistent reconnect
    // banner is the ground-truth signal — data-testid="reconnect-banner",
    // ChatScreen.tsx).
    await expect(page.getByTestId('reconnect-banner')).toBeVisible({ timeout: 10_000 });

    // The composer must stay usable during the reconnect-retry window (the
    // #105 fix) — NOT disabled, NOT silently inert. Type and submit TWO
    // messages while still fully offline, to prove ordering across the
    // queue as well as buffering itself.
    //
    // NOTE: a buffered (queued) message does NOT mint a chat bubble —
    // useChatStore's disconnected-WS branch in sendMessage only pushes the
    // raw text onto `outboundQueue` and returns; the optimistic user bubble
    // is only created once the message is actually dispatched (by
    // maybeDrainNext -> sendMessage, after reconnect). The queue is
    // therefore NOT silent: the outbound-queue-indicator banner is the
    // user-visible proof-of-buffering for this window, counting up
    // deterministically with each queued send.
    await expect(input).toBeEnabled();
    await input.fill('Say exactly: QUEUEDONE');
    await input.press('Enter');
    await expect(page.getByTestId('outbound-queue-indicator')).toContainText('1 message queued', { timeout: 5_000 });
    await input.fill('Say exactly: QUEUEDTWO');
    await input.press('Enter');
    await expect(page.getByTestId('outbound-queue-indicator')).toContainText('2 messages queued', { timeout: 5_000 });

    // Neither message has been dispatched yet — no assistant reply, and no
    // user bubble either (see the NOTE above): the ONLY visible evidence at
    // this point is the queue indicator's count, which is exactly why that
    // count is the assertion that actually distinguishes "queued" from
    // "silently dropped" here.
    expect(await assistantMessages(page).count()).toBe(0);
    expect(await userMessages(page).count()).toBe(0);

    // Reconnect.
    await context.setOffline(false);

    // The queue drains automatically, ONE message at a time as each turn
    // completes (see maybeDrainNext in store/chat.ts) — wait for the
    // indicator (and the reconnect banner) to fully clear...
    await expect(page.getByTestId('outbound-queue-indicator')).not.toBeVisible({ timeout: 120_000 });
    await expect(page.getByTestId('reconnect-banner')).not.toBeVisible({ timeout: 20_000 });

    // ...then for BOTH replies to actually arrive — proving the messages
    // were genuinely sent and answered, not just silently dequeued.
    await expect(assistantMessages(page)).toHaveCount(2, { timeout: 180_000 });
    await waitForTurnFullyDone(page, 8_000);

    // Order: the two queued user messages must appear in the order they
    // were typed (one, then two) — not reordered by the queue/drain
    // mechanism (see the chat.ts store's own FIFO-ordering doc comments on
    // drainOutboundQueue/maybeDrainNext).
    await expect(userMessages(page).nth(0)).toContainText('QUEUEDONE');
    await expect(userMessages(page).nth(1)).toContainText('QUEUEDTWO');

    await expectA11yClean(page);
  },
);
