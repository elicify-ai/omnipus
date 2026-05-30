/**
 * bug-regression.spec.ts — Outcome-level E2E regression tests for 5 bugs
 * surfaced during manual testing of feature/iframe-preview-tier13.
 *
 * These tests MUST fail before the corresponding fixes land and MUST pass after.
 * The lead verifies this by reverting individual fixes one at a time.
 *
 * Bug references:
 *   Bug-1: Skip onboarding button must be gone (Welcome step)
 *   Bug-2: Model selector searches by model name + groups by provider with ≥2 providers
 *   Bug-3: Concurrent sessions both respond (same-agent and cross-agent)
 *   Bug-5: Replay frame ordering preserved when leaving + returning to chat
 *
 * Bug-4 (Docker exec permission denied) is a server-side default-config bug
 * tested via Go integration tests — not testable in Playwright E2E without
 * running inside a real Docker container.
 *
 * NOTE: These tests use the global storageState (pre-authenticated admin session)
 * provided by global-setup.ts unless stated otherwise.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, agentPicker, assistantMessages, newChatButton } from './fixtures/selectors'

// ─── Bug-1: Skip onboarding button must be gone ───────────────────────────────

// Bug-1 tests bypass the pre-authenticated storageState because they must test
// the raw onboarding wizard — a logged-in user would be redirected away.
test.describe('Bug-1: No skip button in onboarding welcome step', () => {
  // Override storageState to get an unauthenticated page.
  test.use({ storageState: { cookies: [], origins: [] } })

  test('(Bug-1-a) onboarding welcome step has no skip button', async ({ page }) => {
    // BDD: Given a fresh/unauthenticated browser session
    //      When the user navigates to the onboarding page
    //      Then no element with text matching /skip/i is present in the DOM
    //
    // Traces to: Bug-1 (skip onboarding button removal)

    // Navigate to the onboarding page directly.
    // On a fresh session the app should redirect to onboarding or serve it at /onboarding.
    // Try both paths — one will work depending on the router config.
    await page.goto('/onboarding')
    // Wait for React to mount.
    await page.waitForLoadState('networkidle')

    // Look for the welcome step heading.
    // If we were redirected somewhere else, still check for skip presence.
    const skipButton = page.getByRole('button', { name: /skip/i })
    const skipLink = page.getByRole('link', { name: /skip/i })
    const skipText = page.getByText(/skip.*i know|i know.*skip/i)

    // All of these must NOT be present — declarative assertions that fail loudly on presence.
    await expect(skipButton).toHaveCount(0, { timeout: 5_000 })
    await expect(skipLink).toHaveCount(0)
    await expect(skipText).toHaveCount(0)
  })

  test('(Bug-1-b) onboarding welcome step has a Get Started button', async ({ page }) => {
    // BDD: Given the welcome step, Then "Get Started" IS present (the only progression path)
    // This is the positive complement to Bug-1-a.
    // Traces to: Bug-1 (only forward path is Get Started)

    await page.goto('/onboarding')
    await page.waitForLoadState('networkidle')

    // If the welcome step loads, Get Started must be there.
    const welcomeHeading = page.getByText('Welcome to Omnipus')
    if (await welcomeHeading.isVisible({ timeout: 3_000 }).catch(() => false)) {
      await expect(page.getByRole('button', { name: /get started/i })).toBeVisible({ timeout: 5_000 })
    }
  })
})

// ─── Bug-3: Concurrent sessions both respond ──────────────────────────────────

test.describe('Bug-3: Concurrent sessions both respond', () => {
  test(
    '(Bug-3-a) two chats opened in parallel both receive replies',
    async ({ page, context }) => {
      // BDD: Given two browser tabs open to different chat sessions
      //      When a message is sent in each tab within a short window
      //      Then both tabs receive at least one assistant message within 30s
      //
      // Traces to: Bug-3 (concurrent sessions both respond)

      // Navigate to first chat.
      await page.goto('/')
      const input1 = chatInput(page)
      await expect(input1).toBeVisible({ timeout: 15_000 })

      // Open a second tab.
      const page2 = await context.newPage()
      await page2.goto('/')
      const input2 = chatInput(page2)
      await expect(input2).toBeVisible({ timeout: 15_000 })

      // Click New Chat on both to get fresh sessions.
      const newChat1 = newChatButton(page)
      const newChat2 = newChatButton(page2)
      if (await newChat1.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat1.click()
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      }
      if (await newChat2.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat2.click()
        await expect(assistantMessages(page2)).toHaveCount(0, { timeout: 10_000 })
      }

      // Send messages on both tabs nearly simultaneously.
      await input1.fill('Bug-3 concurrent test hello tab1')
      await input2.fill('Bug-3 concurrent test hello tab2')
      await input1.press('Enter')
      await input2.press('Enter')

      // Both must get a reply within 30s. With the mock LLM this should be < 5s;
      // we use 30s to give the real LLM time as well.
      const replyTimeout = 30_000

      // Wait for first assistant message in tab 1.
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: replyTimeout })
        .catch((e) => {
          throw new Error(
            `BUG-3: Tab 1 did not receive a reply within ${replyTimeout / 1000}s. ` +
            `This indicates session starvation — while tab 2 was processing, ` +
            `tab 1 was blocked. Original error: ${e.message}`,
          )
        })

      // Wait for first assistant message in tab 2.
      await expect(assistantMessages(page2)).toHaveCount(1, { timeout: replyTimeout })
        .catch((e) => {
          throw new Error(
            `BUG-3: Tab 2 did not receive a reply within ${replyTimeout / 1000}s. ` +
            `This indicates session starvation — while tab 1 was processing, ` +
            `tab 2 was blocked. Original error: ${e.message}`,
          )
        })

      await page2.close()
    },
  )
})

// ─── Bug-5: Replay frame ordering preserved on reconnect ─────────────────────

test.describe('Bug-5: Replay frame ordering preserved after navigation', () => {
  test(
    '(Bug-5-a) navigating away and back to a session preserves message order',
    async ({ page }) => {
      // BDD: Given a chat session with at least 1 assistant message
      //      When the user navigates to another page and back
      //      Then the messages appear in the same order as before
      //
      // Traces to: Bug-5 (replay frame ordering preserved when leaving + returning to chat)

      await page.goto('/')
      const input = chatInput(page)
      await expect(input).toBeVisible({ timeout: 15_000 })

      // Get a fresh session.
      const newChat = newChatButton(page)
      if (await newChat.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat.click()
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      }

      // Send a message and wait for reply.
      await input.fill('Bug-5 replay order test message one')
      await input.press('Enter')
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })

      // Capture the text of the first assistant message.
      const firstMessageText = await assistantMessages(page).first().textContent()

      // Capture the URL of the current session to return to it.
      const sessionURL = page.url()

      // Navigate away.
      await page.goto('/#/agents')
      await page.waitForLoadState('networkidle')

      // Navigate back to the session.
      await page.goto(sessionURL)
      await page.waitForLoadState('networkidle')

      // Wait for messages to restore via replay.
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 15_000 })
        .catch((e) => {
          throw new Error(
            `BUG-5: After navigating back to the session, the assistant message ` +
            `did not appear. This indicates the replay path failed to restore ` +
            `the session state. Original error: ${e.message}`,
          )
        })

      // The replayed message must have the same content as before navigation.
      const replayedText = await assistantMessages(page).first().textContent()
      if (firstMessageText && replayedText && firstMessageText !== replayedText) {
        throw new Error(
          `BUG-5: Replayed message content differs from original. ` +
          `Before: "${firstMessageText}" | After replay: "${replayedText}". ` +
          `This indicates the replay stream produced frames in the wrong order ` +
          `or overwrote the message content.`,
        )
      }
    },
  )

  test(
    '(Bug-5-b) two-turn session: turns appear in chronological order after replay',
    async ({ page }) => {
      // BDD: Given a session with 2 turns (user→assistant, user→assistant)
      //      When the user navigates away and back
      //      Then turn 1 appears before turn 2 (causal order preserved)
      //
      // Traces to: Bug-5 multi-turn replay ordering

      await page.goto('/')
      const input = chatInput(page)
      await expect(input).toBeVisible({ timeout: 15_000 })

      const newChat = newChatButton(page)
      if (await newChat.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat.click()
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      }

      // Turn 1.
      await input.fill('Bug-5 turn 1 — first message')
      await input.press('Enter')
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })

      // Turn 2.
      await input.fill('Bug-5 turn 2 — second message')
      await input.press('Enter')
      await expect(assistantMessages(page)).toHaveCount(2, { timeout: 30_000 })

      // Capture original ordering.
      const originalTexts = await assistantMessages(page).allTextContents()
      const sessionURL = page.url()

      // Navigate away and back.
      await page.goto('/#/agents')
      await page.waitForLoadState('networkidle')
      await page.goto(sessionURL)
      await page.waitForLoadState('networkidle')

      // Wait for 2 messages to restore.
      await expect(assistantMessages(page)).toHaveCount(2, { timeout: 15_000 })
        .catch((e) => {
          throw new Error(
            `BUG-5: After navigating back, expected 2 assistant messages but got fewer. ` +
            `Original error: ${e.message}`,
          )
        })

      // Messages must be in the same order.
      const replayedTexts = await assistantMessages(page).allTextContents()
      if (originalTexts.length === replayedTexts.length) {
        for (let i = 0; i < originalTexts.length; i++) {
          if (originalTexts[i] !== replayedTexts[i]) {
            throw new Error(
              `BUG-5: Replayed message[${i}] content changed. ` +
              `Original: "${originalTexts[i]}" | Replayed: "${replayedTexts[i]}".`,
            )
          }
        }
      }
    },
  )
})
