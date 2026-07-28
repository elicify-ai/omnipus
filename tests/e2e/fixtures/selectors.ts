import { type Page, expect } from '@playwright/test';

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
 * Typing "/new" then Enter is intercepted client-side before it ever reaches
 * the backend (src/hooks/useSlashMenu.ts: `interceptClientCommand` /
 * `runClientCommand('new')` both call `startNewSession()`), converging with
 * selecting "/new" from the slash palette. This is the most direct
 * E2E-reachable equivalent of the old header button — no need to navigate
 * the sidebar's per-workspace accordion to reach its own "New chat" row.
 */
export const startNewChat = async (page: Page) => {
  const input = chatInput(page);
  await input.fill('/new');
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
//   - data-testid="browser-live-img" — the JPEG-screencast <img> sink,
//     rendered instead whenever no WebRTC stream is attached (capability
//     gate, negotiation failure, or a genuine regression). A test that wants
//     to prove "video is live" MUST see browser-live-video, never treat
//     browser-live-img as an acceptable substitute — see
//     browser-live-video.spec.ts's honesty gate for the full rationale.

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

/** The JPEG-screencast `<img>` fallback sink — presence WITHOUT `browserLiveVideo` means WebRTC never attached. */
export const browserLiveImgFallback = (page: Page) =>
  page.locator('[data-testid="browser-live-img"]');

/**
 * Poll until the live view settles on the WebRTC `<video>` sink, or the
 * deadline expires — then report what it actually settled on: `"video"`,
 * `"img"` (JPEG fallback only), or `null` (neither ever appeared, i.e. a
 * capture-path failure rather than a WebRTC-specific one).
 *
 * CRITICAL — it must NOT return on the first `img` sighting. JPEG is
 * architecturally guaranteed to paint FIRST on every healthy cold start, so
 * returning early on it would false-red essentially every run, including on a
 * fast machine:
 *
 *   - `BrowserLiveView.tsx` renders `mediaStream ? <video> : <img>`, and
 *     `mediaStream` is non-null only once WebRTC negotiation COMPLETES.
 *   - The JPEG frame arrives over a separate, always-on CDP screencast with
 *     `screencastEveryNthFrame = 1` (pkg/tools/browser/live.go) — sub-second
 *     first paint.
 *   - WebRTC negotiation legitimately takes seconds to tens of seconds:
 *     `waitForTracksTimeout` is 15s and the SPA's cold-start
 *     `firstAnswerTimeoutMs` is 30s.
 *
 * So the honest question is never "which appeared first" — it is "did video
 * EVER arrive within the budget". Seeing `img` is the expected intermediate
 * state, not a verdict. We keep polling past it and only conclude `"img"`
 * when the whole budget elapsed with the video sink never showing up.
 *
 * Deliberately NOT a `Promise.race` of two `waitFor` calls: both timeouts
 * would still be in flight when the first resolves, and racing two
 * "resolve to null after timeoutMs" promises cannot tell you which sink was
 * actually visible. A manual poll gives an unambiguous answer.
 */
export async function waitForLiveSink(
  page: Page,
  timeoutMs = 90_000,
): Promise<'video' | 'img' | null> {
  const video = browserLiveVideo(page);
  const img = browserLiveImgFallback(page);
  const deadline = Date.now() + timeoutMs;
  let sawImg = false;
  while (Date.now() < deadline) {
    // Video wins the moment it appears, whenever that happens — including
    // long after the JPEG sink has been showing.
    if (await video.isVisible().catch(() => false)) return 'video';
    if (!sawImg && (await img.isVisible().catch(() => false))) {
      sawImg = true;
    }
    await page.waitForTimeout(500);
  }
  // Budget exhausted. Distinguish "picture mode worked but video never came"
  // (a WebRTC-path failure) from "nothing rendered at all" (a capture-path
  // failure) — the two need very different investigations.
  return sawImg ? 'img' : null;
}
