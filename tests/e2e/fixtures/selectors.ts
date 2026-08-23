import { type Locator, type Page, expect } from '@playwright/test';

/**
 * Chat composer input — AssistantUI renders ComposerPrimitive.Input as a
 * <textarea> with aria-label="Message input" (ChatScreen.tsx:631).
 */
export const chatInput = (page: Page) =>
  page.locator('textarea[aria-label="Message input"]');

/**
 * Reconnect banner — ChatScreen.tsx renders `data-testid="reconnect-banner"`
 * in exactly three mutually-exclusive branches, together covering EVERY
 * state where the WebSocket is not genuinely open:
 *   - `reconnectPhase === 'gave_up'`
 *   - `reconnectPhase === 'reconnecting' | 'slow'`
 *   - `!isConnected && reconnectPhase === null` (brief pre-first-connect /
 *     just-dropped window)
 * `useConnectionStore.setConnected` (src/store/connection.ts) atomically
 * clears `reconnectPhase` to `null` in the SAME update that flips
 * `isConnected` true, so there is no window where the banner is hidden yet
 * the socket isn't really open. Its absence is therefore an exact,
 * already-shipped (not test-only) observable for "the WebSocket is
 * genuinely connected" — see `waitForConnected` below.
 */
export const reconnectBanner = (page: Page) =>
  page.locator('[data-testid="reconnect-banner"]');

/**
 * Wait for a GENUINE, wire-ready WebSocket connection — not merely for the
 * composer's `disabled` attribute to clear.
 *
 * Regression history (2fa26e6a, issue #105 fix): ChatScreen's `inputEnabled`
 * used to require strict `isConnected`, so `chatInput(page)` reporting
 * `toBeEnabled()` was a reliable (if accidental) proxy for "the socket is
 * open" — every readiness gate in this suite leaned on that side effect.
 * The #105 fix correctly ALSO enables the composer while
 * `reconnectPhase` is `'reconnecting'`/`'slow'`, so a user can type during a
 * transient outage — the message is buffered in the outbound queue and
 * drained automatically once the connection recovers (this is the CORRECT,
 * intentional product behavior — see ws-reconnect.spec.ts's
 * `online_event_triggers_reconnect`, which deliberately asserts the
 * composer STAYS enabled during that window; do not "fix" that test).
 *
 * `toBeEnabled()` alone therefore no longer implies "connected": a message
 * filled and sent while only enabled-but-reconnecting lands in the outbound
 * queue, not the wire, and a test awaiting a real LLM reply then hangs to
 * its full timeout instead of failing fast. Call this ALONGSIDE (not
 * instead of) a `toBeEnabled()` check wherever a test previously used
 * `toBeEnabled()` alone as its "safe to type and send" gate.
 */
export async function waitForConnected(page: Page, opts?: { timeout?: number }): Promise<void> {
  await expect(reconnectBanner(page)).toBeHidden({ timeout: opts?.timeout ?? 15_000 });
}

/**
 * Send button — ComposerPrimitive.Send rendered with aria-label="Send message"
 * (ChatScreen.tsx:698). Only visible when not streaming.
 */
export const sendButton = (page: Page) =>
  page.locator('button[aria-label="Send message"]').first();

/**
 * Agent picker button — rendered inside the composer card's context row
 * (src/components/chat/composer/AgentPicker.tsx), not the workspace top-bar
 * banner, with data-testid="agent-picker-trigger". The button shows only the
 * agent name (e.g. "Jim", "Mia") — NOT the old "Name — Tagline" format.
 *
 * Ground truth: composer/AgentPicker.tsx DropdownMenuTrigger > Button carries
 * data-testid="agent-picker-trigger". Scoped directly by testid (no banner
 * ancestor) — the composer card renders below the banner, so a
 * getByRole('banner') scope would never match it.
 */
export const agentPicker = (page: Page) =>
  page.locator('[data-testid="agent-picker-trigger"]');

/**
 * Completed assistant messages — only counts messages whose data-status is not
 * "running". AssistantUI creates a placeholder element with data-message-id as
 * soon as the user sends a message (before the LLM responds). Excluding
 * data-status="running" ensures tests wait for the LLM to actually complete
 * its response rather than matching the in-progress placeholder.
 *
 * Ground truth: ChatScreen sets data-status={message.status?.type ?? 'complete'}
 * on AssistantMessage's MessagePrimitive.Root, and data-message-id on all
 * message roots. User messages have flex-row-reverse (right-aligned bubbles);
 * assistant messages do not.
 */
export const assistantMessages = (page: Page) =>
  page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])');

/**
 * User messages — complement of assistantMessages; row uses `flex-row-reverse`.
 */
export const userMessages = (page: Page) =>
  page.locator('[data-message-id].flex-row-reverse');

/**
 * Nav link helper — sidebar must be open before calling this.
 * Returns the anchor inside the nav for a given href.
 * The sidebar renders nav[aria-label="Main navigation"] ONLY while open.
 *
 * HashRouter: TanStack Router generates href="/#/<path>" links.
 * Call with the full hash-prefixed href, e.g. navLink(page, '/#/agents').
 */
export const navLink = (page: Page, href: string) =>
  page.locator(`nav[aria-label="Main navigation"] a[href="${href}"]`);

/**
 * Agent cards on the roster page — AgentCard renders a <button> with
 * aria-label="View agent {name}" (AgentCard.tsx:29).
 * Ground truth: "View agent Mia — Omnipus Guide" (em-dash, not regular dash).
 */
export const agentCards = (page: Page) =>
  page.locator('button[aria-label^="View agent "]');

/**
 * New-chat button — rendered in the header banner with accessible name "New Chat".
 * Ground truth confirmed via Playwright MCP: button "New Chat" (not title="New chat").
 *
 * STALE as of the workspace top-bar redesign — kept only so existing callers
 * that already defensively guard on `.isVisible().catch(() => false)` keep
 * compiling. The header no longer has a "New Chat" button at all
 * (src/components/chat/ChatControls.tsx: "New Chat was removed from the
 * header — three paths for one action was redundant... It lives where the
 * user already is: the sidebar's per-workspace 'New chat' row and the /new
 * slash command."). For new code, use `startNewChat` below instead — it
 * drives the actual replacement mechanism.
 */
export const newChatButton = (page: Page) =>
  page.getByRole('banner').getByRole('button', { name: 'New Chat' });

/**
 * Start a new chat via the "/new" client-delivery slash command — the
 * replacement for the removed header "New Chat" button (see `newChatButton`
 * doc comment above for the ground truth citation).
 *
 * Typing "/new" then Enter is intercepted client-side (src/hooks/useSlashMenu.ts:
 * `interceptClientCommand` / `runClientCommand('new')` both call
 * `startNewSession()`), converging with selecting "/new" from the slash palette.
 * This is the most direct E2E-reachable equivalent of the old header button — no
 * need to navigate the sidebar's per-workspace accordion to reach its own
 * "New chat" row.
 *
 * IMPORTANT — two things here are load-bearing; do not "simplify" them back:
 *
 *  1. `pressSequentially`, NOT `fill`. `fill` sets the value without firing the
 *     input events the composer listens to, so the slash palette never opens and
 *     the command is never recognised as one. T22 in cancel-cross-channel.spec.ts
 *     already types rather than fills for this reason.
 *  2. We wait for the palette entry before submitting. The interception looks the
 *     command up in the list fetched by GET /api/v1/commands?surface=web; a miss
 *     while that request is still in flight used to fall through and send "/new"
 *     to the backend AS A CHAT MESSAGE. Observed in CI: the fetch was issued at
 *     t=3424ms and Enter pressed at t=3660ms, 236ms later — the backend answered
 *     it as a server-side command, minting a session bound to the wrong agent and
 *     failing the test 150s later on an unrelated assertion.
 *
 * The SPA now holds an early slash command until the list resolves (5157e378),
 * so (2) is belt-and-braces — but it keeps this helper honest against the real
 * user path rather than depending on that guard.
 */
export const startNewChat = async (page: Page) => {
  const input = chatInput(page);
  await input.click();
  await input.pressSequentially('/new');
  // The palette entry appearing proves the command list has loaded and "/new"
  // resolved against it, so Enter cannot escape to the backend as chat.
  await page
    .getByRole('option', { name: /new/i })
    .or(page.locator('[data-testid="slash-menu"]').getByText('/new'))
    .first()
    .waitFor({ state: 'visible', timeout: 10_000 });
  await input.press('Enter');
};

/**
 * Session token/cost counter — moved out of the header banner into the
 * composer's context row (src/components/chat/composer/TokenCounter.tsx),
 * per ChatControls.tsx: "The Agent picker, Model selector, and Token counter
 * used to live here but moved into the composer's context row... so they sit
 * next to the input they scope." Rendered unconditionally but hidden below
 * the composer's own `@2xl` container-query breakpoint (~42rem/672px) — the
 * default Playwright viewport (1280×720) is wide enough for it to show.
 */
export const tokenCounter = (page: Page) =>
  page.locator('[data-testid="session-token-counter"]');

/**
 * Switch the active chat agent via the composer's agent picker.
 *
 * Delegate-dependent E2E specs must run against a general-purpose task agent
 * (default: Jim) rather than the default agent Mia: Mia's "guide" persona makes
 * the model REFUSE to emit `delegate` ("My role is to explain… not to delegate
 * to subagents"), so delegate-expecting assertions never see a SubagentBlock.
 *
 * Reuses the established picker pattern from chat.spec.ts (open menu →
 * click menuitem → assert the picker label updated).
 */
export const selectAgent = async (page: Page, name: string | RegExp = /Jim/i) => {
  const picker = agentPicker(page);
  await picker.waitFor({ state: 'visible', timeout: 15_000 });
  await picker.click();
  await page.getByRole('menuitem', { name }).click();
  // Assert the picker label updated so we know the switch took effect.
  // AgentPicker shows only the agent name (no em-dash tagline).
  await expect(picker).toContainText(name, { timeout: 5_000 });
};

// ── Live browser panel (ADR-038/039/040/041/047) ──────────────────────────
//
// Ground truth (src/components/browser/BrowserLiveView.tsx):
//   - "Watch live" launcher — aria-label="Watch live" (BrowserNavigateBlock /
//     BrowserToolBlock, src/components/chat/tools/), opens the app-root
//     BrowserLivePanel onto the globally-active session/agent.
//   - data-testid="browser-live-panel-docked" — the docked panel's <aside>
//     root (BrowserLivePanel.tsx).
//   - data-testid="browser-live-frame" — the pointer/keyboard capture
//     surface (role="application"), present once the first frame (JPEG or
//     WebRTC) has arrived.
//   - data-testid="browser-live-video" — the WebRTC <video> sink. ONLY
//     rendered when a live MediaStream is actually attached (mediaStream
//     truthy) — its presence is the one honest signal that this session is
//     really running the WebRTC video/audio path, not the JPEG fallback.
//   - data-testid="browser-live-img" — the JPEG-screencast <img> sink. ADR-061
//     (JPEG-fallback removal, "Retired surfaces" in the root CLAUDE.md)
//     deleted this OUTRIGHT from BrowserLiveView.tsx — it no longer exists
//     anywhere in `src/`, and reintroducing it is a regression, not a
//     legitimate merge-conflict resolution. WebRTC is the only live-video
//     path now: a failure surfaces as a persistent, honest error + Retry
//     button (data-testid="browser-live-retry" / "browser-live-retry-overlay"),
//     never a silent degrade to a second sink. `waitForLiveSink` below still
//     polls for this selector as a CANARY for that exact regression (it
//     should never match), not because the element is expected to appear.

/** The "Watch live" launcher rendered on a browser tool-call row in chat. */
export const watchLiveButton = (page: Page) =>
  page.getByRole('button', { name: 'Watch live' }).first();

/** The docked live-browser panel's root element. */
export const browserLivePanel = (page: Page) =>
  page.locator('[data-testid="browser-live-panel-docked"]');

/** The pointer/keyboard capture surface inside the live panel (present once a frame has arrived). */
export const browserLiveFrame = (page: Page) =>
  page.locator('[data-testid="browser-live-frame"]');

/** The WebRTC `<video>` sink — presence proves a real live MediaStream is attached (not the JPEG fallback). */
export const browserLiveVideo = (page: Page) =>
  page.locator('[data-testid="browser-live-video"]');

/**
 * The JPEG-screencast `<img>` fallback sink locator. ADR-061 deleted the
 * element this targets outright (see the ground-truth comment above) — this
 * locator can never match anything in the current codebase. Kept ONLY as the
 * canary `waitForLiveSink` polls for below; do not use it to assert "video is
 * live" (browserLiveVideo is the honest signal for that).
 */
export const browserLiveImgFallback = (page: Page) =>
  page.locator('[data-testid="browser-live-img"]');

/**
 * The "Retry" affordance BrowserLiveView.tsx renders the instant it has a
 * real, honest WebRTC failure to report — either data-testid="browser-live-retry"
 * (shown before the stream ever attaches) or "browser-live-retry-overlay"
 * (shown after attach, while still waiting for the first decoded frame).
 * Both only render when `displayError` is non-null (ADR-061 — every WebRTC
 * failure surfaces with a real reason, never silently) — so either becoming
 * visible is proof the outcome for THIS attempt is already known.
 */
const browserLiveRetryAny = (page: Page) =>
  page.locator('[data-testid="browser-live-retry"], [data-testid="browser-live-retry-overlay"]');

/**
 * Poll until the live view settles on the WebRTC `<video>` sink, or the
 * deadline expires — then report what it actually settled on: `"video"`,
 * `"img"` (JPEG fallback — see below, effectively unreachable now), or
 * `null` (neither ever appeared, i.e. a capture-path failure rather than a
 * WebRTC-specific one).
 *
 * FIX (SMALL-2, external review 2026-08-13): ADR-061 deleted the JPEG `<img>`
 * fallback sink outright (see `browserLiveImgFallback`'s own doc comment) —
 * `data-testid="browser-live-img"` no longer exists anywhere in `src/`, so
 * the `'img'` verdict below was unreachable dead code, and every WebRTC
 * failure fell through to the bare `null` verdict ("capture-path failure")
 * ONLY after riding out the FULL `timeoutMs` (default 90s) budget — even
 * though BrowserLiveView.tsx had already reported the real, specific reason
 * (`disabled` / `lite_build` / `ice-failed` / `answer-timeout` / ...) within
 * seconds, via its Retry-button error state. That misdiagnosed a WebRTC
 * failure as an unrelated capture-path stall, and did so as slowly as
 * possible. Polling for that same Retry-button error state now lets this
 * FAIL FAST — in seconds, not 90s — with the real reason in the thrown
 * error, the moment BrowserLiveView itself has already concluded this
 * attempt is dead. Retained the `img`-polling below (rather than deleting
 * it) as a CANARY: reintroducing the JPEG sink is a documented regression
 * (see CLAUDE.md's "Retired surfaces"), not a legitimate merge resolution,
 * and this is what would catch it coming back.
 *
 * CRITICAL — it must NOT return on the first `img` sighting (were it ever to
 * reappear). JPEG used to be architecturally guaranteed to paint FIRST on
 * every healthy cold start, so returning early on it would false-red
 * essentially every run:
 *
 *   - `BrowserLiveView.tsx` renders `mediaStream ? <video> : <img>` in the
 *     pre-ADR-061 world, and `mediaStream` is non-null only once WebRTC
 *     negotiation COMPLETES.
 *   - WebRTC negotiation legitimately takes seconds to tens of seconds:
 *     `waitForTracksTimeout` is 15s and the SPA's cold-start
 *     `firstAnswerTimeoutMs` is 30s.
 *
 * Deliberately NOT a `Promise.race` of separate `waitFor` calls: both
 * timeouts would still be in flight when the first resolves, and racing two
 * "resolve to null after timeoutMs" promises cannot tell you which signal was
 * actually visible. A manual poll gives an unambiguous answer.
 */
export async function waitForLiveSink(
  page: Page,
  timeoutMs = 90_000,
): Promise<'video' | 'img' | null> {
  const video = browserLiveVideo(page);
  const img = browserLiveImgFallback(page);
  const retry = browserLiveRetryAny(page);
  const deadline = Date.now() + timeoutMs;
  let sawImg = false;
  while (Date.now() < deadline) {
    // Video wins the moment it appears, whenever that happens — including
    // long after any interim state has been showing.
    if (await video.isVisible().catch(() => false)) return 'video';
    // FAIL FAST: BrowserLiveView has already concluded this attempt failed
    // (its Retry button + `displayError` text are up) — no point riding out
    // the rest of the budget for an outcome that's already known. Throwing
    // (rather than returning a value) lets the real, specific reason reach
    // the test failure message directly, instead of being flattened into
    // the generic `null` "capture-path failure" verdict below.
    if (await retry.first().isVisible().catch(() => false)) {
      const reported = await retry
        .first()
        .locator('..')
        .textContent()
        .catch(() => null);
      throw new Error(
        'WebRTC live view reported a real, specific failure before the video sink ever ' +
          `appeared: "${reported?.trim() ?? '(error text unavailable)'}" — see ` +
          'translateWebRTCFallbackReason (src/lib/browserWebRTC.ts) for the reason catalogue. ' +
          'This is a WebRTC-specific failure, not a capture-path stall — do not treat it as ' +
          'the generic "neither sink appeared" case.',
      );
    }
    if (!sawImg && (await img.isVisible().catch(() => false))) {
      sawImg = true;
    }
    await page.waitForTimeout(500);
  }
  // Budget exhausted with NEITHER a video sink NOR a reported error ever
  // appearing — a genuine capture-path stall (browser_attach/browser_status
  // round trip never completed), or the ADR-061 regression the `img` canary
  // above exists to catch.
  return sawImg ? 'img' : null;
}

// ── Native <select> helpers ───────────────────────────────────────────────

/**
 * Select an option from a NATIVE `<select>` by a label PATTERN.
 *
 * WHY THIS EXISTS: `Locator.selectOption({ label })` takes a **string**, not a
 * RegExp — the label is compared with `===` inside Playwright's injected page
 * script. A RegExp neither survives the wire serialization nor that comparison,
 * so `selectOption({ label: /Sales/i })` matches nothing and dies on the
 * "did not find some options" timeout. Seven call sites across
 * channel-routing.spec.ts and channels-routing.spec.ts were written exactly
 * that way. Nobody noticed because these are the `<select>` branch of a
 * SmartSelect fork, and SmartSelect renders a Radix combobox (not a native
 * `<select>`) for every roster size these specs actually build — so the broken
 * branch has never executed. It was also invisible to `npm run typecheck`,
 * which did not look at tests/ at all until tsconfig.tests.json existed.
 *
 * Resolving the pattern against the real option list here keeps the intended
 * case-insensitive/partial matching, and selecting by INDEX sidesteps the
 * `option.label`-vs-textContent ambiguity entirely. A non-match throws with the
 * full option list, so an activated-but-wrong branch fails loudly and legibly
 * instead of timing out anonymously.
 */
export async function selectNativeOptionByLabel(
  select: Locator,
  label: RegExp,
): Promise<void> {
  const texts = await select.locator('option').allTextContents();
  const index = texts.findIndex((t) => label.test(t.trim()));
  if (index === -1) {
    throw new Error(
      `selectNativeOptionByLabel: no <option> matches ${String(label)} — ` +
        `options present: ${JSON.stringify(texts.map((t) => t.trim()))}`,
    );
  }
  await select.selectOption({ index });
}
