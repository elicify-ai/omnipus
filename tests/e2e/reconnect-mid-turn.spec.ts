/**
 * reconnect-mid-turn.spec.ts — ADR-082 D2/D3/D4/D5, spec test T-18 (S-08, S-09, S-10).
 *
 * Traces to: docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md,
 * docs/internal/specs/ui-independent-turns-spec.md.
 *
 * Drives the REAL embedded binary against a LIVE model, so it asserts only
 * structure, never content: after a reload / a second tab / an offline blip
 * mid-turn, the streaming bubble is present, Stop is visible while the turn
 * runs, exactly one assistant message exists at the end, and its text is at
 * least as long as what was visible before the disconnect (catch-up + live
 * tail, no gap). Content equality across two tabs IS asserted — both views
 * of one session must converge on the same persisted text.
 */

import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, assistantMessages } from './fixtures/selectors'
import { freshChat } from './fixtures/session-setup'
import { normalizedAssistantText } from './fixtures/assistant-text'

// Plain prose, no tools: a single long streaming round with a stable shape.
const LONG_PROMPT =
  'Do NOT use any tools. Plain prose only. Write eight short paragraphs about the sea, about 600 words total.'

const stopButton = (page: Page) => page.locator('[data-testid="stop-btn"]')
// Any assistant row, running or finished (assistantMessages() excludes running rows).
const anyAssistantRow = (page: Page) => page.locator('[data-message-id]:not(.flex-row-reverse)')

/**
 * The answer text streamed so far in a RUNNING assistant row, whitespace-
 * normalized: the row's answer body (the same container
 * fixtures/assistant-text.ts::normalizedAssistantText reads) without the
 * trailing reply-status mark ("Thinking…") that sits inside it while the turn
 * runs. Reading the whole row instead put the agent name and that mark into
 * the "before" text: CI run 39100617554 captured
 * "Mia The sea keeps its own calendar. Long before anyone tho Thinking
 * Thinking… Copy", whose first 60 characters ended in " T" and could never
 * prefix the finished answer ("…anyone thought …").
 */
async function streamedAnswerText(row: Locator): Promise<string> {
  return row.evaluate((el) => {
    const bodies = el.querySelectorAll(':scope > div > div[class~="text-[length:var(--type-body-compact-size)]"]')
    if (bodies.length !== 1) return ''
    const body = bodies[0] as HTMLElement
    let text = body.innerText
    for (const mark of Array.from(body.querySelectorAll<HTMLElement>('[data-testid="reply-status-indicator"]'))) {
      const markText = mark.innerText.trim()
      if (markText && text.trimEnd().endsWith(markText)) text = text.trimEnd().slice(0, -markText.length)
    }
    return text.replace(/\s+/g, ' ').trim()
  })
}

async function startLongTurn(page: Page): Promise<string> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await freshChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
  await input.fill(LONG_PROMPT)
  await input.press('Enter')
  await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
  // Wait until some ANSWER text has actually streamed (not the agent name or
  // the status mark) so "at least as long" is a real bar.
  const row = anyAssistantRow(page).first()
  await expect.poll(async () => (await streamedAnswerText(row).catch(() => '')).length, { timeout: 60_000 }).toBeGreaterThan(80)
  return streamedAnswerText(row)
}

async function waitTurnDone(page: Page) {
  await expect(stopButton(page)).toBeHidden({ timeout: 240_000 })
}

test.describe('reconnect mid-turn (ADR-082)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('S-08 reload mid-turn: bubble continues, Stop visible, single done', async ({ page }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)

    await page.reload()
    await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)

    // D4: the gateway emits session_state{active_turn} FIRST on attach, so if
    // the turn is still running Stop should reappear within ~5s of reconnect
    // — bounded here at 15s for CI jitter. The turn may also have already
    // finished during the reload, in which case a completed message exists
    // instead. Assert the ACTUAL branch explicitly (not a tautological
    // membership check that passes no matter which literal comes back).
    const stopOrDone = await Promise.race([
      stopButton(page).waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'stop' as const),
      assistantMessages(page).first().waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'done' as const),
    ])
    if (stopOrDone === 'stop') {
      await expect(stopButton(page)).toBeVisible()
      await expect(anyAssistantRow(page).first()).toBeVisible()
    } else {
      const doneRow = assistantMessages(page).first()
      await expect(doneRow).toBeVisible()
      expect((await doneRow.innerText()).trim().length).toBeGreaterThan(0)
    }

    await waitTurnDone(page)
    // ADR-082 D4: "Stop is back after reload" also means Stop goes away and
    // the composer re-enables once the turn genuinely ends — assert both,
    // bounded, rather than relying on waitTurnDone's 240s ceiling alone.
    await expect(stopButton(page)).toBeHidden({ timeout: 30_000 })
    await expect(chatInput(page)).toBeEnabled({ timeout: 10_000 })

    // Exactly one assistant message; catch-up + live tail ⇒ no gap.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    // Both sides are whitespace-normalized answer text (no name, mark or footer).
    const after = await normalizedAssistantText(assistantMessages(page).first())
    expect(after.length).toBeGreaterThanOrEqual(before.length)
    // The answer seen before the reload is a prefix of the final answer.
    expect(after.startsWith(before.slice(0, 60)), `before: ${before.slice(0, 60)} | after: ${after.slice(0, 80)}`).toBeTruthy()
  })

  test('S-09 second tab attaches mid-turn and converges on the same text', async ({ page, context }) => {
    test.setTimeout(420_000)
    await startLongTurn(page)

    const page2 = await context.newPage()
    await page2.goto('/')
    await expect(chatInput(page2)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page2)

    // Same D4 attach-order guarantee as S-08, asserted on the SECOND tab's
    // own attach: Stop reappears within the 15s bound if the turn is still
    // running, otherwise a completed, non-empty message is already there.
    const stopOrDone = await Promise.race([
      stopButton(page2).waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'stop' as const),
      assistantMessages(page2).first().waitFor({ state: 'visible', timeout: 15_000 }).then(() => 'done' as const),
    ])
    if (stopOrDone === 'stop') {
      await expect(stopButton(page2)).toBeVisible()
      await expect(anyAssistantRow(page2).first()).toBeVisible()
    } else {
      const doneRow = assistantMessages(page2).first()
      await expect(doneRow).toBeVisible()
      expect((await doneRow.innerText()).trim().length).toBeGreaterThan(0)
    }

    await waitTurnDone(page)
    await waitTurnDone(page2)
    await expect(stopButton(page2)).toBeHidden({ timeout: 30_000 })
    await expect(chatInput(page2)).toBeEnabled({ timeout: 10_000 })
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    await expect(assistantMessages(page2)).toHaveCount(1, { timeout: 30_000 })

    // Run 38729142998 had identical answers but an attached-only model footer.
    // Compare all answer prose exactly; optional row metadata is not the answer.
    const t1 = await normalizedAssistantText(assistantMessages(page).first())
    const t2 = await normalizedAssistantText(assistantMessages(page2).first())
    expect(t2).toBe(t1)
    await page2.close()
  })

  test('S-10 offline blip mid-turn: automatic re-attach, no user action', async ({ page, context }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)

    // THE OUTAGE HAS TO ACTUALLY LAST: #823 keeps drops under 15 seconds quiet,
    // then adds a calm continuation line to the interrupted assistant answer.
    //
    // This test used to call context.setOffline(true) and expect the banner
    // within 10s. setOffline emulates network conditions over CDP; it does
    // not fire the DOM `offline` event that ws.ts._onOffline listens for, and
    // it does not reliably stop a loopback reconnect to the gateway running
    // on this same machine. So the socket came straight back — ws.ts's
    // _onOnline path resets backoff and redials after 250ms — and 250ms never
    // clears a 2000ms debounce. No banner, and the product was RIGHT not to
    // draw one.
    //
    // That is why it flip-flopped between the two CI workers on identical
    // commits: on a loaded box the redial slipped past 2s and the banner
    // appeared; on a fast one it did not. Three runs, three different
    // pass/fail splits across ci-omnipus and ci-omnipus-2.
    //
    // So make the outage real: routeWebSocket closes every reconnect attempt,
    // which keeps isConnected false well past the debounce. The route is
    // installed AFTER the turn is already streaming, so the original socket
    // is untouched — only the redials fail, exactly as a real outage behaves.
    //
    // Nothing is softened. Every assertion still runs the real path: a real
    // close, real reconnect scheduling with real failures, the real delayed
    // answer state, and real catch-up of a turn that kept running
    // server-side the whole time.
    await page.routeWebSocket(/\/api\/v1\/chat\/ws/, (ws) => ws.close())
    await context.setOffline(true)
    await page.evaluate(() => window.dispatchEvent(new Event('offline')))
    await expect(page.getByTestId('assistant-connection-status')).toContainText(
      'is still working on this — the rest appears when you\'re connected again.',
      { timeout: 20_000 },
    )

    // Let the network back after the delayed state is genuinely visible.
    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await context.setOffline(false)
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(page.getByTestId('connection-status-line')).toHaveText('Up to date', { timeout: 30_000 })
    await expect(page.getByTestId('connection-status-line')).not.toBeVisible({ timeout: 5_000 })

    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = await normalizedAssistantText(assistantMessages(page).first())
    expect(after.length).toBeGreaterThanOrEqual(before.length)
  })
})

// Harness-only regressions for S-09. Static HTML replaces application rendering,
// not the browser or Locator API. These run in the existing Chromium E2E shard:
// the ordinary Vitest jobs do not provision a Playwright browser.
async function renderAnswerRows(page: Page, body: string, attachedBody = body): Promise<void> {
  // ChatScreen::AssistantMessage wraps MarkdownText in .prose-sm inside the
  // body; VirtualAssistantMessageRow renders HistoricalMessageMarkdown's
  // elements directly. Both have the same body container, outside the label
  // and action/model footer. Deliberately exercise those DIFFERENT layouts.
  await page.setContent(`
    <div id="live"><div><span>Mia</span>
      <div class="text-[length:var(--type-body-compact-size)]"><div class="prose-sm">${body}</div></div>
      <button>Copy</button></div></div>
    <div id="attached"><div><span>Mia</span>
      <div class="text-[length:var(--type-body-compact-size)]">${attachedBody}</div>
      <button>Copy</button><span data-testid="message-model">fixture/model-id</span>
    </div></div>`)
}

test.describe('S-09 rendered-answer oracle', () => {
  test('compares identical prose exactly despite an attached-only model footer', async ({ page }) => {
    await renderAnswerRows(page, '<p>The sea keeps its own clock.</p>')
    const live = await normalizedAssistantText(page.locator('#live'))
    const attached = await normalizedAssistantText(page.locator('#attached'))
    expect(live).toBe('The sea keeps its own clock.')
    expect(attached).toBe(live)
  })

  test('normalizes only whitespace across all paragraphs', async ({ page }) => {
    await renderAnswerRows(page, '<p>The   sea</p><p>keeps\nits own clock.</p>')
    expect(await normalizedAssistantText(page.locator('#live'))).toBe('The sea keeps its own clock.')
  })

  test('retains every Markdown text block in order', async ({ page }) => {
    await renderAnswerRows(page, '<div class="prose-sm">First part.</div><div class="prose-sm">Last part.</div>')
    expect(await normalizedAssistantText(page.locator('#live'))).toBe('First part. Last part.')
  })

  test('retains a model slug when it is part of the answer, not the footer', async ({ page }) => {
    await renderAnswerRows(page, '<p>The model is fixture/model-id.</p>')
    expect(await normalizedAssistantText(page.locator('#attached'))).toBe('The model is fixture/model-id.')
  })

  test('rejects a row with only chrome instead of certifying empty equality', async ({ page }) => {
    await page.setContent('<div id="empty"><span>Mia</span><button>Copy</button><span data-testid="message-model">model</span></div>')
    await expect(normalizedAssistantText(page.locator('#empty'))).rejects.toThrow('Assistant row has no rendered answer text')
  })

  test('rejects an ambiguous row with multiple answer containers', async ({ page }) => {
    await page.setContent(`<div id="ambiguous"><div>
      <div class="text-[length:var(--type-body-compact-size)]">First answer</div>
      <div class="text-[length:var(--type-body-compact-size)]">Second answer</div>
    </div></div>`)
    await expect(normalizedAssistantText(page.locator('#ambiguous'))).rejects.toThrow('Assistant row has multiple answer containers')
  })

  test('rejects a blank Markdown body instead of comparing two empty strings', async ({ page }) => {
    await renderAnswerRows(page, '<p>   </p>')
    await expect(normalizedAssistantText(page.locator('#live'))).rejects.toThrow('Assistant row has no rendered answer text')
  })

  // ADR-087 suffixes are outside the body in ChatScreen's real row layout.
  // Equal partial answers must fail, even if BOTH tabs have the same notice.
  for (const notice of ['(interrupted)', '(cut off at the output limit)']) {
    for (const placement of ['body', 'footer']) {
      test(`rejects matching answers with ${notice} in the ${placement}`, async ({ page }) => {
        const body = '<p>The sea keeps its own clock.</p>'
        await renderAnswerRows(page, placement === 'body' ? `${body}<span>${notice}</span>` : body)
        if (placement === 'footer') {
          await page.locator('#live > div, #attached > div').evaluateAll((containers, text) => {
            for (const container of containers) {
              const suffix = document.createElement('span')
              suffix.textContent = text
              container.append(suffix)
            }
          }, notice)
        }
        for (const id of ['live', 'attached']) {
          await expect(normalizedAssistantText(page.locator(`#${id}`))).rejects.toThrow(
            'Assistant row contains an interruption, cutoff or error notice',
          )
        }
      })
    }
  }

  for (const status of ['error', 'interrupted', 'incomplete']) {
    test(`rejects an answer whose row reports ${status}`, async ({ page }) => {
      await renderAnswerRows(page, '<p>The sea keeps its own clock.</p>')
      await page.locator('#attached').evaluate((row, value) => row.setAttribute('data-status', value), status)
      await expect(normalizedAssistantText(page.locator('#attached'))).rejects.toThrow(
        `Assistant row has failed or incomplete status: ${status}`,
      )
    })
  }

  for (const [label, notice] of [
    ['system-error disclosure', '<details data-testid="error-detail-disclosure"><summary>Technical details</summary><pre>Provider request failed</pre></details>'],
    ['system-error alert', '<div role="alert">The answer could not be saved</div>'],
  ]) {
    test(`rejects an answer with a ${label} outside its body`, async ({ page }) => {
      await renderAnswerRows(page, '<p>The sea keeps its own clock.</p>')
      await page.locator('#attached > div').evaluate((container, html) => container.insertAdjacentHTML('beforeend', html), notice)
      await expect(normalizedAssistantText(page.locator('#attached'))).rejects.toThrow(
        'Assistant row contains an interruption, cutoff or error notice',
      )
    })
  }

  for (const [label, changed] of [
    ['truncated', 'The sea keeps its own'],
    ['duplicated', 'The sea keeps its own clock. The sea keeps its own clock.'],
    ['altered', 'The sky keeps its own clock.'],
  ]) {
    test(`does not certify a ${label} answer as converged`, async ({ page }) => {
      await renderAnswerRows(page, '<p>The sea keeps its own clock.</p>', `<p>${changed}</p>`)
      const live = await normalizedAssistantText(page.locator('#live'))
      const attached = await normalizedAssistantText(page.locator('#attached'))
      expect(live).toBe('The sea keeps its own clock.')
      expect(attached).toBe(changed)
      expect(attached).not.toBe(live)
    })
  }
})

// Harness-only regressions for S-08's "before" text. The running row mirrors
// ChatScreen's live AssistantMessage: agent label, then ONE answer body that
// holds the streamed text AND the trailing reply-status mark, then Copy.
async function renderRunningRow(page: Page, streamed: string): Promise<void> {
  await page.setContent(`
    <div id="running" data-status="running"><div><span>Mia</span>
      <div class="text-[length:var(--type-body-compact-size)]"><div class="prose-sm">${streamed}</div>
        <span data-testid="reply-status-indicator"><span>Thinking</span> Thinking…</span></div>
      <button>Copy</button></div></div>`)
}

test.describe('S-08 streamed-answer oracle', () => {
  test('reads only the streamed answer: no agent name, status mark or Copy', async ({ page }) => {
    // The exact shape CI run 39100617554 captured mid-stream.
    await renderRunningRow(page, '<p>The sea keeps its own calendar. Long before anyone tho</p>')
    expect(await streamedAnswerText(page.locator('#running'))).toBe(
      'The sea keeps its own calendar. Long before anyone tho',
    )
  })

  test('the streamed answer prefixes the finished answer it grows into', async ({ page }) => {
    await renderRunningRow(page, '<p>The sea keeps its own calendar. Long before anyone tho</p>')
    const before = await streamedAnswerText(page.locator('#running'))
    await renderAnswerRows(page, '<p>The sea keeps its own calendar. Long before anyone thought to write dates on paper.</p>')
    const after = await normalizedAssistantText(page.locator('#live'))
    expect(after.startsWith(before.slice(0, 60))).toBe(true)
  })

  test('still catches a real gap: a finished answer that does not continue the streamed one', async ({ page }) => {
    await renderRunningRow(page, '<p>The sea keeps its own calendar. Long before anyone tho</p>')
    const before = await streamedAnswerText(page.locator('#running'))
    await renderAnswerRows(page, '<p>The sky keeps its own calendar. Long before anyone thought to write.</p>')
    const after = await normalizedAssistantText(page.locator('#live'))
    expect(after.startsWith(before.slice(0, 60))).toBe(false)
  })
})
