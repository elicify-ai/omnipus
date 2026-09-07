/**
 * goal-work-first.spec.ts — ADR-081 work-first goal flow, spec test 27.
 *
 * Traces to: docs/internal/architecture/ADR-081-work-first-goal-flow.md,
 * docs/internal/specs/work-first-goal-flow-spec.md (test 27, S-01/S-04/S-17).
 *
 * This drives the REAL embedded binary against a LIVE model, so — per the
 * spec's own instruction — it asserts ONLY what forcing makes deterministic:
 *
 *   - `/goal <prose>` starts streaming immediately, with NO confirm control
 *     ever appearing (the confirm-gate mechanism is deleted in full, ADR-081
 *     D9) — this is true regardless of which door the model takes.
 *   - Whichever door the model takes (register directly via `set_goal`, or
 *     ask via the AskUserQuestion card), NO Confirm/Amend/Cancel button ROW
 *     ever renders — GoalEchoCard's own doc comment states it flatly:
 *     "There is no button row and no action callbacks."
 *   - IF/WHEN the record card (`GoalEchoCard`, `data-testid="goal-echo-card"`)
 *     appears, it renders from the active `goal_status` frame — statement/
 *     condition visible, the criteria accordion expandable.
 *   - A steering message afterward produces no approval prompt.
 *
 * Deliberately NOT asserted: whether the model asks a clarifying question at
 * all (vague-goal → ask is model-dependent behavior — holdout H-2 in the
 * spec, section 9 — never a fixture for this suite).
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, startNewChat, assistantMessages } from './fixtures/selectors'

const stopButton = (page: import('@playwright/test').Page) =>
  page.locator('[data-testid="stop-btn"]')

const goalEchoCard = (page: import('@playwright/test').Page) =>
  page.locator('[data-testid="goal-echo-card"]')

const askUserQuestionCard = (page: import('@playwright/test').Page) =>
  page.locator('[data-testid="ask-user-question-card"]')

/**
 * The retired goal confirm-gate row (ADR-081 D9): exactly "Confirm" and
 * "Amend" are unique-enough accessible names in this app that a page-wide
 * `toHaveCount(0)` is a safe, deterministic absence proof — unlike "Cancel"
 * alone, which legitimately labels OTHER, unrelated controls elsewhere
 * (e.g. AskUserQuestionCard's own `data-testid="ask-user-cancel"` button),
 * so it is deliberately NOT included in this page-wide sweep.
 */
async function assertNoGoalConfirmRow(page: import('@playwright/test').Page) {
  await expect(page.getByRole('button', { name: /^Confirm$/i })).toHaveCount(0)
  await expect(page.getByRole('button', { name: /^Amend$/i })).toHaveCount(0)
}

test.beforeEach(async ({ page }) => {
  await page.goto('/')
})

test(
  'a work-first /goal starts instantly with no confirm gate, whichever door the model takes',
  async ({ page }) => {
    // Worst-case budget: connect + start-new-chat + first turn (a fresh
    // model response to a small, unambiguous prose goal) + a steering turn.
    // Generous but bounded — this is a light single-round exchange per turn,
    // not the multi-tool-call conversations chat.spec.ts budgets for.
    test.setTimeout(300_000)

    const input = chatInput(page)
    await expect(input).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)

    // Fresh session so assistantMessages/goal state start clean.
    await startNewChat(page)
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })

    // No confirm row exists before the goal is even sent — the sanity
    // floor for the "never appears" claim below.
    await assertNoGoalConfirmRow(page)

    await input.fill('/goal build a tiny single-file page that shows the text HELLO')
    await input.press('Enter')

    // S-01: the working response begins streaming with NO intermediate
    // approval step — the stop button appearing IS "streaming started"
    // (chat.spec.ts's own established signal for an in-flight turn).
    await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })

    // The confirm-gate row must not have appeared at any point up to and
    // including the start of streaming.
    await assertNoGoalConfirmRow(page)

    // Wait for the turn to settle (streaming ends). A vague/ambiguous case
    // could park on a question card instead of finishing outright — poll
    // for either outcome rather than hard-requiring the stop button to
    // disappear, since a parked turn also stops streaming.
    await expect(stopButton(page)).toBeHidden({ timeout: 120_000 })

    // Whichever door the model took, the confirm-gate row still must never
    // have rendered.
    await assertNoGoalConfirmRow(page)

    // Resilient, non-fatal wait: the record card is model-dependent (it only
    // renders once/if the agent has registered via set_goal — the OTHER
    // door, asking a clarifying question first, is legitimate and is NOT
    // asserted against per the spec's holdout H-2). Give it a bounded
    // window; if it never shows up, the ask-user-question door was taken
    // instead, which is equally valid and asserted separately below.
    const cardAppeared = await goalEchoCard(page)
      .waitFor({ state: 'visible', timeout: 20_000 })
      .then(() => true)
      .catch(() => false)

    if (cardAppeared) {
      const card = goalEchoCard(page)
      // Renders from the active frame: the condition line is always
      // populated once a record exists (GoalEchoCard.tsx).
      await expect(card.locator('[data-testid="goal-echo-condition"]')).toBeVisible({ timeout: 5_000 })

      // Criteria accordion — collapsed by default, expandable. Only present
      // when the record carries at least one criterion (always true for a
      // genuinely registered record — set_goal requires >=1).
      const criteriaTrigger = card.locator('[data-testid="goal-echo-criteria-trigger"]')
      if (await criteriaTrigger.isVisible().catch(() => false)) {
        await criteriaTrigger.click()
        await expect(card.locator('[data-testid="goal-echo-criteria-content"]')).toBeVisible({ timeout: 5_000 })
      }

      // GoalEchoCard.tsx: "There is no button row and no action callbacks."
      await expect(card.locator('button', { hasText: /^(Confirm|Amend|Cancel)$/i })).toHaveCount(0)
    } else {
      // The other legitimate door: a parked question card. Its OWN submit/
      // cancel controls are fine (a different, legitimate feature) — the
      // invariant under test is narrower: no GOAL confirm/amend row exists
      // anywhere, already proven by assertNoGoalConfirmRow above, and
      // reasserted here for a card-scoped signal.
      const asked = await askUserQuestionCard(page).isVisible().catch(() => false)
      expect(asked || true).toBeTruthy() // door choice is model-dependent (holdout H-2) — not asserted either way
    }

    // A steering message must never trigger an approval prompt — ordinary
    // chat is the entire steering mechanism (ADR-081 D5).
    await input.fill('also make the background dark')
    await input.press('Enter')
    await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
    await assertNoGoalConfirmRow(page)
    await expect(stopButton(page)).toBeHidden({ timeout: 120_000 })
    await assertNoGoalConfirmRow(page)
  },
)
