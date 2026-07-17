import { type Page, expect } from '@playwright/test';

/**
 * Chat composer input — AssistantUI renders ComposerPrimitive.Input as a
 * <textarea> with aria-label="Message input" (ChatScreen.tsx:631).
 */
export const chatInput = (page: Page) =>
  page.locator('textarea[aria-label="Message input"]');

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
