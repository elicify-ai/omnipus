/**
 * goal-card-position.spec.ts — ADR-082 D9, spec test T-23 (S-15).
 *
 * The goal card renders IN PLACE of its `set_goal` tool call, so after a
 * follow-up message it must sit ABOVE that follow-up in the thread (live)
 * and stay there after a reload (replay). Door choice (register vs ask) is
 * model-dependent (holdout H-2 of the ADR-081 spec): when the model asks
 * instead of registering, the ordering invariant is asserted on the
 * AskUserQuestion card's absence of a goal card and the test ends early.
 */

import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, startNewChat, assistantMessages, userMessages } from './fixtures/selectors'

const goalEchoCard = (page: Page) => page.locator('[data-testid="goal-echo-card"]')
const askCard = (page: Page) => page.locator('[data-testid="ask-user-question-card"]')
const stopButton = (page: Page) => page.locator('[data-testid="stop-btn"]')

async function endTurnDeterministically(page: Page) {
  const stop = stopButton(page)
  if (await stop.isVisible().catch(() => false)) {
    await stop.click().catch(() => {})
  }
  await expect(stop).toBeHidden({ timeout: 30_000 })
}

/** DOM order: card must precede the LAST user message and follow the FIRST one. */
async function assertCardBetween(page: Page) {
  const ordering = await page.evaluate(() => {
    const card = document.querySelector('[data-testid="goal-echo-card"]')
    const users = Array.from(document.querySelectorAll('[data-message-id].flex-row-reverse'))
    if (!card || users.length < 2) return { ok: false, reason: `card=${!!card} users=${users.length}` }
    const first = users[0], last = users[users.length - 1]
    const afterFirst = !!(first.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING)
    const beforeLast = !!(card.compareDocumentPosition(last) & Node.DOCUMENT_POSITION_FOLLOWING)
    return { ok: afterFirst && beforeLast, reason: `afterFirst=${afterFirst} beforeLast=${beforeLast}` }
  })
  expect(ordering.ok, ordering.reason).toBeTruthy()
}

test.beforeEach(async ({ page }) => {
  await page.goto('/')
})

test('S-15 goal card sits where the goal was set, live and after reload', async ({ page }) => {
  test.setTimeout(300_000)
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await startNewChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })

  await input.fill('/goal build a tiny single-file page that shows the text HELLO')
  await input.press('Enter')
  await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })

  // No `.catch(() => false)` here: if BOTH cards fail to appear within the
  // timeout, both branches of the race reject and the race itself rejects —
  // that must FAIL the test (goal card never rendered = regression), not be
  // silently remapped onto the "model asked" branch, whose assertions would
  // then pass vacuously (S5 fix — a double timeout used to read as "asked").
  const registered = await Promise.race([
    goalEchoCard(page).first().waitFor({ state: 'visible', timeout: 90_000 }).then(() => true),
    askCard(page).first().waitFor({ state: 'visible', timeout: 90_000 }).then(() => false),
  ])
  await endTurnDeterministically(page)

  if (!registered) {
    // Model asked instead of registering (holdout H-2): assert the
    // AskUserQuestion card actually rendered (not merely that the goal card
    // didn't), so a broken ask-card render can't hide behind this branch.
    await expect(askCard(page).first()).toBeVisible()
    await expect(goalEchoCard(page)).toHaveCount(0)
    return
  }

  // Follow-up message: the card must NOT move to the tail.
  await input.fill('also make the background dark')
  await input.press('Enter')
  await expect(userMessages(page)).toHaveCount(2, { timeout: 30_000 })
  await assertCardBetween(page)
  await endTurnDeterministically(page)
  await assertCardBetween(page)

  // Replay path: same order after reload.
  await page.reload()
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await expect(userMessages(page)).toHaveCount(2, { timeout: 30_000 })
  await expect(goalEchoCard(page).first()).toBeVisible({ timeout: 15_000 })
  await assertCardBetween(page)
})
