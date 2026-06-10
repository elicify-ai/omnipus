import { type Page } from '@playwright/test';

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
 * Agent picker button — rendered in the <header> as a button whose text includes
 * the agent name followed by an em-dash (e.g. "Mia — Omnipus Guide").
 * Scoped to the banner landmark to avoid matching sidebar items.
 *
 * Ground truth: header structure confirmed via Playwright MCP live inspection.
 * The button carries the full "Name — Tagline" text — match via em-dash presence.
 * The <header> has implicit ARIA role "banner" — use getByRole to locate it.
 */
export const agentPicker = (page: Page) =>
  page.getByRole('banner').locator('button').filter({ hasText: '—' }).first();

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
 */
export const newChatButton = (page: Page) =>
  page.getByRole('banner').getByRole('button', { name: 'New Chat' });

/**
 * Switch the active chat agent via the header agent picker.
 *
 * Spawn-dependent E2E specs must run against a general-purpose task agent
 * (default: Jim) rather than the default agent Mia: Mia's "guide" persona makes
 * the model REFUSE to emit `spawn` ("My role is to explain… not to spawn
 * subagents"), so spawn-expecting assertions never see a SubagentBlock.
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
  const label = typeof name === 'string' ? new RegExp(name, 'i') : name;
  await page.getByRole('banner').locator('button').filter({ hasText: label }).first()
    .waitFor({ state: 'visible', timeout: 5_000 });
};
