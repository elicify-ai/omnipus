import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, selectAgent, waitForConnected } from './fixtures/selectors'

const LABEL_A = 'ADR-091 child A'
const LABEL_B = 'ADR-091 child B'
const LABEL_C = 'ADR-091 child C'
const CHILD_ONLY_SENTINEL = 'ADR091_CHILD_VIEW_ONLY'

function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error('BLOCKED: OPENROUTER_API_KEY_CI is required for steered-session reachability')
  }
}

test('steered session is reachable in its own live view without leaking child output into the parent chat', async ({ page, context }) => {
  requireApiKey()
  test.setTimeout(420_000)

  await page.goto('/')
  await selectAgent(page, /Jim/i)
  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 15_000 })
  await waitForConnected(page, { timeout: 15_000 })

  await input.fill([
    'Call delegate exactly once with agent_id="worker" and label="ADR-091 child A".',
    'Give it this task verbatim:',
    `Call delegate exactly once with agent_id="worker" and label="${LABEL_B}".`,
    'The B task must call delegate exactly once with agent_id="worker" and',
    `label="${LABEL_C}". C must work for at least two tool steps, report progress,`,
    `and finish with exactly ${CHILD_ONLY_SENTINEL}.`,
    'Do not repeat or paraphrase any child output in this parent chat.',
  ].join('\n'))
  await input.press('Enter')

  const activityBar = page.locator('[data-testid="activity-bar"]')
  await expect(activityBar).toBeVisible({ timeout: 180_000 })
  const parentSurface = page.locator('[data-active-session-id]').first()
  await expect(parentSurface).toHaveAttribute('data-active-session-id', /.+/)
  const parentSessionID = await parentSurface.getAttribute('data-active-session-id')
  if (!parentSessionID) {
    throw new Error('root chat did not expose its active session id')
  }
  const parentURL = new URL(`/#/sessions/${encodeURIComponent(parentSessionID)}`, page.url()).toString()
  await activityBar.click()

  const childRow = page.locator('[data-testid="activity-row"]').filter({ hasText: LABEL_A })
  await expect(childRow).toBeVisible({ timeout: 60_000 })
  await expect(childRow).toContainText(/queued|running|working/i)
  const openControl = childRow.getByRole('button', { name: /open/i })
  await expect(openControl).toBeVisible()
  await openControl.click()

  await expect(page).toHaveURL(/sessions\//, { timeout: 15_000 })
  const childURL = page.url()
  expect(childURL).not.toBe(parentURL)
  const childSessionID = childURL.match(/sessions\/([^/?#]+)/)?.[1]
  expect(childSessionID, 'the open control must navigate to the child session route').toBeTruthy()

  await expect(page.locator('[data-active-session-id]').first()).toHaveAttribute(
    'data-active-session-id',
    childSessionID!,
    { timeout: 15_000 },
  )
  await expect(page.getByText(CHILD_ONLY_SENTINEL, { exact: false })).toBeVisible({ timeout: 240_000 })

  const childInput = chatInput(page)
  await expect(childInput).toBeEnabled({ timeout: 30_000 })
  await waitForConnected(page, { timeout: 15_000 })
  await childInput.fill('Steering update: acknowledge with "steer received" in this child session only.')
  await childInput.press('Enter')
  await expect(page.getByText('steer received', { exact: false })).toBeVisible({ timeout: 120_000 })

  const parentView = await context.newPage()
  await parentView.goto(parentURL)
  await expect(parentView.locator('[data-testid="chat-input"]').first()).toBeVisible({ timeout: 15_000 })
  await expect(parentView.getByText(CHILD_ONLY_SENTINEL, { exact: false })).toHaveCount(0)
  await expect(parentView.getByText('steer received', { exact: false })).toHaveCount(0)
  await expect(parentView.locator('[data-testid="tool-call-badge"][data-tool="delegate"]')).toHaveCount(1)

  await parentView.reload()
  const replayedActivityBar = parentView.locator('[data-testid="activity-bar"]')
  await expect(replayedActivityBar).toBeVisible({ timeout: 30_000 })
  await replayedActivityBar.click()
  await expect(parentView.locator('[data-testid="activity-row"]').filter({ hasText: LABEL_A })).toBeVisible()
})
