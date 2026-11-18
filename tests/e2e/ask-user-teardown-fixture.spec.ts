// #1769 — lifecycle test of the REAL cancelOnTeardown fixture (console-errors.ts).
//
// The vitest cases in fixtures/ask-user-cleanup.test.ts cover the helper with a
// fake page; they cannot prove the fixture runs the helper after every test, is
// still `auto`, and reports. This spec does: each test leaves a synthetic
// AskUserQuestion card (same data-testids as AskUserQuestionCard) open, and an
// observer fixture, torn down AFTER cancelOnTeardown, asserts what teardown
// did to the page and to the real TestInfo. No gateway, no model: the DOM is
// set on about:blank, so the spec is free and deterministic.
import { test as base, expect, mergeTests } from '@playwright/test'
import { test as consoleErrorsTest } from './fixtures/console-errors'

type Scenario = 'two-removable' | 'none' | 'stuck' | 'overlay'

const CARD = '[data-testid="ask-user-question-card"]'
const LEFT_OPEN = 'ask-user-left-open'

const cardHtml = (removable: boolean) =>
  `<div data-testid="ask-user-question-card"><button data-testid="ask-user-cancel" ` +
  (removable ? `onclick="this.parentElement.remove()"` : '') +
  `>Cancel</button></div>`

/** The observer is merged FIRST, so it is set up first and torn down LAST, after cancelOnTeardown. */
const observer = base.extend<{ expectAfterTeardown: void }>({
  expectAfterTeardown: [
    async ({ page }, use, testInfo) => {
      await use()
      const scenario = testInfo.annotations.find((a) => a.type === 'scenario')?.description as Scenario
      const remaining = await page.locator(CARD).count()
      const leftOpen = testInfo.annotations.filter((a) => a.type === LEFT_OPEN)
      switch (scenario) {
        case 'two-removable':
        case 'none':
          expect(remaining, `${scenario}: teardown must leave no pending card`).toBe(0)
          expect(leftOpen, `${scenario}: nothing to report`).toHaveLength(0)
          break
        case 'stuck':
        case 'overlay':
          expect(remaining, `${scenario}: the card really could not be cancelled`).toBeGreaterThan(0)
          expect(leftOpen, `${scenario}: teardown must report the card it could not cancel`).toHaveLength(1)
          break
        default:
          throw new Error(`observer: unknown scenario ${String(scenario)}`)
      }
    },
    { auto: true },
  ],
})

const test = mergeTests(observer, consoleErrorsTest)

test.describe('cancelOnTeardown cancels open AskUserQuestion cards (#1769)', () => {
  test.use({ storageState: { cookies: [], origins: [] } })

  test('two simultaneous cards are both cancelled', async ({ page }) => {
    test.info().annotations.push({ type: 'scenario', description: 'two-removable' })
    await page.setContent(cardHtml(true) + cardHtml(true))
    await expect(page.locator(CARD)).toHaveCount(2)
  })

  test('positive control: no question open means nothing is clicked or reported', async ({ page }) => {
    test.info().annotations.push({ type: 'scenario', description: 'none' })
    await page.setContent('<div>no question</div>')
  })

  test('a card whose Cancel does nothing is reported, not silently ignored', async ({ page }) => {
    test.info().annotations.push({ type: 'scenario', description: 'stuck' })
    await page.setContent(cardHtml(false))
    await expect(page.locator(CARD)).toHaveCount(1)
  })

  test('a Cancel covered by an overlay is reported, not silently ignored', async ({ page }) => {
    test.info().annotations.push({ type: 'scenario', description: 'overlay' })
    await page.setContent(
      cardHtml(true) + '<div style="position:fixed;inset:0;background:rgba(0,0,0,.1)"></div>',
    )
    await expect(page.locator(CARD)).toHaveCount(1)
  })
})
