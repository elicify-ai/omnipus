/**
 * steered-midturn-tool-rows.spec.ts - founder-reported 2026-09-26 hotfix:
 * "tool rows vanish when a message is sent mid-turn".
 *
 * Evidence pack: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-evidence/steer-tools-2026-09-26/
 * - ist-repro.mjs (the investigation script this spec ports) measured the
 * live UI going 3 -> 0 tool rows the instant a mid-turn steer landed, and
 * new-a1-log.txt / new-a1-ws-frames.jsonl show the rows staying absent until
 * the turn done (then re-appearing stuck under the wrong bubble).
 *
 * The unit/store nets for the fix live in src/store/chat.steer-tools-visible.test.ts
 * and src/lib/omnipus-runtime.steer-ownership.test.ts. This spec re-drives the
 * SAME prompt/steer sequence against a real server + real model through a real
 * browser, and asserts the discriminating property: the tool rows the closing
 * reply already showed must still be rendered after the steer lands (pre-fix
 * they collapsed to 0 within one frame) and at turn end.
 *
 * Real-model spec: needs OPENROUTER_API_KEY_CI (UAT provider/model ruling:
 * openrouter + z-ai/glm-5.3-flash; Omnipus sends tools every request), and is
 * long-running, so it rides the opt-in `e2e` CI gate, not the default map.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { restoreAdminSession } from './fixtures/admin-api'
import { waitForConnected } from './fixtures/selectors'

const ROWS = '[data-testid="virtualized-message-list"] button[aria-expanded]'

function requireApiKey(): void {
  if (!process.env.OPENROUTER_API_KEY_CI) {
    throw new Error('BLOCKED: OPENROUTER_API_KEY_CI is required for steered-midturn-tool-rows')
  }
}

test('tool rows of the closing reply survive a mid-turn steer', async ({ page }) => {
  requireApiKey()
  test.setTimeout(420_000)

  await restoreAdminSession(page)
  await page.goto('/')
  await page.waitForSelector('[data-testid="chat-input"]', { timeout: 30_000 })
  await waitForConnected(page)

  // Tap the WS frames so the steer is timed from real tool_call_start
  // traffic (never from wall-clock guesses).
  let toolStarts = 0
  let dones = 0
  page.on('websocket', (ws) => {
    ws.on('framereceived', (ev) => {
      try {
        const j = JSON.parse(typeof ev.payload === 'string' ? ev.payload : '')
        if (j.type === 'tool_call_start') toolStarts++
        if (j.type === 'done') dones++
      } catch { /* non-JSON frame */ }
    })
  })

  const PROMPT = 'Do these steps strictly in order, one tool call per step, never skip one: ' +
    '(1) list the files in your workspace directory; ' +
    '(2) run the shell command: sleep 2; echo step-two ; ' +
    '(3) run the shell command: sleep 30; echo step-three ; ' +
    '(4) run the shell command: sleep 10; echo step-four ; ' +
    '(5) run the shell command: sleep 5; echo step-five ; ' +
    '(6) finally reply with a short summary of the outputs. You MUST complete all five tool steps before replying; do not stop early.'
  await page.locator('[data-testid="chat-input"]').fill(PROMPT)
  await page.locator('[data-testid="chat-input"]').press('Enter')

  // Wait until at least 3 tool calls have started (the evidence scenario:
  // two finished + one still running at steer time) and the turn is still
  // in flight, then let the DOM settle briefly.
  const t0 = Date.now()
  while (Date.now() - t0 < 180_000) {
    const approve = page.getByRole('button', { name: /^(approve|allow|allow once|run)$/i })
    if (await approve.count()) await approve.first().click().catch(() => {})
    if (dones > 0) throw new Error('INVALID: turn ended before the steer could be sent')
    if (toolStarts >= 3) break
    await page.waitForTimeout(250)
  }
  expect(toolStarts, 'the prompt must produce at least 3 tool calls before the steer').toBeGreaterThanOrEqual(3)
  await page.waitForTimeout(1500)
  if (dones > 0) throw new Error('INVALID: turn ended during settle')
  const beforeSteer = await page.locator(ROWS).count()
  expect(beforeSteer, 'tool rows must be visible before the steer').toBeGreaterThanOrEqual(1)

  // The steer.
  await page.locator('[data-testid="chat-input"]').fill('Also include the word BANANA in your final summary.')
  await page.locator('[data-testid="chat-input"]').press('Enter')

  // THE FIX UNDER TEST: the already-rendered rows must NOT collapse when the
  // steer closes the reply bubble (pre-fix: 3 -> 0 instantly). Poll the row
  // count for the first seconds after the steer; ANY sample below the
  // pre-steer count is the bug.
  await page.waitForTimeout(300)
  for (let i = 0; i < 12; i++) {
    const during = await page.locator(ROWS).count()
    expect(during, `tool rows must not collapse after the steer (sample ${i})`).toBeGreaterThanOrEqual(beforeSteer)
    await page.waitForTimeout(250)
  }

  // Wait for the turn to finish (stop button gone for 5s) or 240s.
  const t1 = Date.now()
  let idleSince: number | null = null
  while (Date.now() - t1 < 240_000) {
    const approve = page.getByRole('button', { name: /^(approve|allow|allow once|run)$/i })
    if (await approve.count()) await approve.first().click().catch(() => {})
    const running = await page.locator('[data-testid="stop-btn"]').count()
    if (!running) {
      idleSince ??= Date.now()
      if (Date.now() - idleSince > 5_000) break
    } else {
      idleSince = null
    }
    await page.waitForTimeout(500)
  }

  // Turn ended: the first reply keeps its rows (bake-in-place), the second
  // reply shows its own new calls - the total never dips below the
  // pre-steer count.
  const afterTurn = await page.locator(ROWS).count()
  expect(afterTurn, 'tool rows must survive to turn end').toBeGreaterThanOrEqual(beforeSteer)
})
