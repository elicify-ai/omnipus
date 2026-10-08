/**
 * catchup-scenarios.spec.ts — BE-DESIGN.md §8.3, scenarios a–i (the squad
 * brief's "real-browser scenarios (orchestrator)" table).
 *
 * STATUS (pass 2): un-skipped. Lane A's gateway hub
 * (`pkg/gateway/ws_session_hub.go`, `squad/be-lane-a-gateway`) and Lane B's
 * agent/session identity plumbing (`pkg/agent/eventbus.go`'s SetSyncTap,
 * per-message id — `cc41be0a5`/`34786eb16`/`61ab119fa`/`8e17b8687`) are both
 * present on this branch's rebased base as of pass 2 (2026-09-24). Every
 * scenario below drives the REAL embedded binary and asserts the design's
 * common pass criteria (§8.3): the final answer is complete and matches the
 * transcript after a hard reload; exactly one bubble per turn; no duplicated
 * text; tool cards are not stuck on "cancelled"/"running"; the user message
 * sits directly above its answer; no red or orange banner; the phase-1
 * states appear as specified.
 *
 * Per the squad brief's item 6: verified with `npx playwright test --list`
 * only — the harness itself was NOT run for scenarios a-g/i in this pass
 * (they need a real provider key and the operator's data folder, neither
 * available in every environment).
 *
 * STATUS (pass 4): scenario `h` (gateway restart) is un-skipped. It drives
 * its OWN isolated, killable/restartable gateway process via
 * `fixtures/gateway-process.ts`'s `GatewayProcess` (own port, own
 * mkdtemp'd OMNIPUS_HOME, real SIGKILL + re-spawn), entirely separate from
 * the shared gateway the rest of this file's `page` fixture points at —
 * that mechanism was run and confirmed working end-to-end against a real
 * built binary in pass 3: login, turn start, SIGKILL, restart, and WS
 * auto-reconnect/reattach to the SAME session all verified (see `h`'s own
 * comment for the `restart({relogin:false})` harness-bug fix that came out
 * of that pass). Pass 3 still could not reliably catch a turn genuinely
 * mid-answer at kill time — the then-configured model answered fast enough
 * in that environment that three different timing strategies produced three
 * different outcomes. The central e2e model (tests/e2e/e2e-model.json, see
 * fixtures/e2e-model.ts) streams
 * much more slowly (measured full real turns ran 17s-1.8m across scenarios
 * a-f, 2026-09-27 local run), which widens the window, and pass 4's
 * stream-delay knob makes it model-independent anyway. Pass 4 closed that
 * gap with the backend's
 * test-only knob, `OMNIPUS_TEST_ONLY_STREAM_TOKEN_DELAY_MS`
 * (`pkg/gateway/ws_session_hub.go`'s `streamTokenDelayEnvOverrideVar`,
 * read once at gateway start): set to 300ms on `h`'s OWN `GatewayProcess`
 * only (never the shared gateway), it pauses the web streamer that long
 * after each published token, so the turn now stays genuinely mid-stream
 * for many seconds — plenty of margin for kill9()/restart() and a
 * Playwright poll, without touching the assertion itself.
 */

import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, startNewChat, assistantMessages, userMessages } from './fixtures/selectors'
import { installFreezeProxy } from './helpers/freezeProxy'
import { GatewayProcess } from './fixtures/gateway-process'

const stopButton = (page: Page) => page.locator('[data-testid="stop-btn"]')
const assistantConnectionStatus = (page: Page) => page.getByTestId('assistant-connection-status')

const LONG_PROMPT =
  'Do NOT use any tools. Plain prose only. Write eight short paragraphs about the tide, about 600 words total.'

// Real-browser follow-up (orchestrator): assertNoDuplicateOrGapText compared
// the whole bubble's innerText, which includes bubble CHROME (the model
// footer, `[data-testid="message-model"]`, rendered via ModelFooter.tsx —
// only when `message.model` is a non-empty string). That field is populated
// once the turn is persisted, so a live-streaming bubble legitimately has no
// model label yet while the SAME bubble read after a reload does — that
// difference exists in the release build too (ModelFooter's own doc
// comment: it's deliberately rendered identically by both the live
// MessageItem.tsx path and the replay VirtualAssistantMessageRow path, from
// the same `message.model` field, which is what differs, not the rendering
// logic) and is not a product regression to fix. The design's own pass
// criteria (§8.3) is about message CONTENT ("the final answer is complete
// and matches the transcript"), not bubble chrome — so every content
// comparison in this file reads through this helper, which excludes any
// `[data-testid="message-model"]` subtree from the extracted text.
async function bubbleText(row: import('@playwright/test').Locator): Promise<string> {
  return row.evaluate((el) => {
    const clone = el.cloneNode(true) as HTMLElement
    clone.querySelectorAll('[data-testid="message-model"]').forEach((n) => n.remove())
    return (clone as HTMLElement).innerText
  })
}

async function startLongTurn(
  page: Page, prompt: string = LONG_PROMPT, options: { requireRunning?: boolean } = {},
): Promise<string> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await startNewChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
  await input.fill(prompt)
  await input.press('Enter')
  await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
  // A completed-only selector waits for done before returning, so it cannot
  // establish the mid-turn precondition b/c require. Other cases retain their
  // existing completed-content baseline until their own coverage is audited.
  const row = options.requireRunning
    ? page.locator('[data-message-id]:not(.flex-row-reverse)').first()
    : assistantMessages(page).first()
  await expect.poll(async () => (await bubbleText(row).catch(() => '')).trim().length, { timeout: 60_000 }).toBeGreaterThan(80)
  if (options.requireRunning) {
    await expect(row, 'real content has arrived while the assistant is still streaming').toHaveAttribute('data-status', 'running')
    await expect(stopButton(page)).toBeVisible()
  }
  return (await bubbleText(row)).trim()
}

async function waitTurnDone(page: Page) {
  await expect(stopButton(page)).toBeHidden({ timeout: 240_000 })
}

// Real-browser follow-up (orchestrator, scenario b): `waitTurnDone`'s "stop
// button hidden" check is only a genuine "the turn finished" signal once
// something has established a baseline of the button actually having been
// VISIBLE for THIS render tree. Right after a fresh page.reload() (or a
// switch to a different session, scenario c's own fix above), the button
// has never appeared at all in the new DOM — "hidden" is trivially,
// immediately true regardless of whether the turn is still genuinely
// streaming server-side, which is exactly what produced scenario b's
// `toHaveCount(1)` / received 0: the count check fired before the
// reconnect's catch-up rebuild had time to reconstruct anything at all.
// Give the rebuild a real chance to show the turn as still running first
// (if it genuinely still is) before treating "hidden" as meaningful.
async function waitTurnDoneAfterReload(page: Page) {
  const appeared = await stopButton(page)
    .waitFor({ state: 'visible', timeout: 20_000 })
    .then(() => true)
    .catch(() => false)
  if (!appeared) {
    // The turn may have genuinely finished during the freeze/reload gap —
    // wait for the reconstructed answer to actually exist before deciding
    // there's nothing left to wait for.
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 60_000 })
    return
  }
  await waitTurnDone(page)
}

// Fresh browser contexts start with the sidebar closed, even on wide windows.
// Showing it is a real user action; never seed the sidebar store or press Escape
// to dismiss it while a turn is running (Escape can cancel the turn).

// A session's sidebar row (Sidebar.tsx's SidebarSessionRow) is a ghost
// Button whose accessible name is the session's title — there is no
// dedicated testid per row, the title text IS the selector.
// Real-browser follow-up (orchestrator, scenarios b and c — traced from the
// actual trace.zip execution log, not guessed): scoping the row lookup to
// the WHOLE page matched the WRONG element. The header's "My Workspace"
// workspace tab apparently ALSO carries aria-current="page" (the tablist's
// own "currently selected view" semantics) and, being earlier in the DOM
// than the sidebar drawer, `.first()` picked IT — so a captured "title"
// was literally the string "My Workspace", and clicking it again later hit
// the SAME header tab, never a real session row. That explains both bugs
// from one root cause: the drawer's onClose (wired only to an actual
// session row's own click handler) never fired, so its backdrop stayed
// open indefinitely — not a transient fade, exactly what was observed.
// Scoped to the drawer panel specifically (`#sidebar-overlay-panel`,
// Sidebar.tsx) so nothing outside it can ever match.
const sidebarPanel = (page: Page) => page.getByRole('navigation', { name: 'Main navigation' })
const sessionRowByTitle = (page: Page, title: string) => sidebarPanel(page).getByRole('button')
  .filter({ has: page.getByText(title, { exact: true }) })

async function ensureSidebarOpen(page: Page) {
  if (!(await sidebarPanel(page).isVisible())) {
    await page.getByRole('button', { name: 'Show sidebar', exact: true }).click()
  }
  await expect(sidebarPanel(page)).toBeVisible()
}

// The currently-active row carries aria-current="page" (SidebarSessionRow).
// Used to capture chat A's real, server-assigned title (which is a model-
// generated summary of the first message, not the prompt text verbatim —
// reading it back from the DOM, rather than guessing what the title will
// be, is what makes re-selecting the row later reliable).
async function activeSidebarTitle(page: Page): Promise<string> {
  // Real-browser follow-up (orchestrator, traced from the actual trace.zip
  // execution log): even scoped to the sidebar panel, `aria-current="page"`
  // ALSO matches the workspace accordion header (Sidebar.tsx's own
  // per-project button — "you're currently in this workspace"), which
  // renders BEFORE its session tree in DOM order. `.first()` picked that
  // header, not the actual active session row (SidebarSessionRow, line
  // ~987 of that file) — captured title was literally "My Workspace",
  // which later matched the SAME header again, never a real session,
  // leaving the drawer's onClose never triggered (explains scenario c's
  // "not a 150ms fade, the layer stays" too — same root cause). The
  // active SESSION row renders AFTER the workspace header within its
  // expanded accordion section, so `.last()` is the real one.
  await ensureSidebarOpen(page)
  const row = sidebarPanel(page).locator('button[aria-current="page"]').last()
  await expect(row).toBeVisible({ timeout: 10_000 })
  // The sibling lifecycle label changes from Working to Done; it is not part
  // of the stable session title and cannot be used to locate the row later.
  const title = (await row.locator('span.truncate').innerText()).trim()
  await row.focus()
  await page.keyboard.press('ControlOrMeta+b')
  await expect(sidebarPanel(page)).toBeHidden()
  return title
}

// Real sidebar navigation (orchestrator follow-up — the previous draft left
// this "for the orchestrator to wire once runnable"): open the drawer,
// click the session's OWN row by its title. Escape is never used here — it
// cancels the running turn (ADR-057's cancel state machine), which would
// defeat the entire point of this scenario.
async function switchToSessionByTitle(page: Page, title: string) {
  // Real-browser follow-up (orchestrator, traced locally against a real
  // gateway): the sidebar title is a plain 60-char truncation of the first
  // message (confirmed: a captured title's own .length was exactly 60,
  // ending in a literal "..." baked into the text, not a CSS-only
  // truncation). Every scenario in this file that calls startLongTurn uses
  // the SAME hardcoded LONG_PROMPT, so once more than one of them has run
  // against the SAME gateway process (true for CI too — the whole spec
  // file runs sequentially against one worker's gateway, not just this
  // isolated local run) their titles collide exactly, and getByRole's
  // {exact: true} — needing a single match to click — hangs against the
  // resulting ambiguity instead of erroring cleanly. `.first()` is the most
  // recently created matching session (the sidebar lists newest-first),
  // which is always the one THIS test just made.
  await ensureSidebarOpen(page)
  await sessionRowByTitle(page, title).first().click()
  await expect(sidebarPanel(page)).toBeHidden()
}

// Round-3/round-4 open item (orchestrator, both rounds): the previous version
// of this check only verified `after` didn't SHRINK relative to `before` and
// shared its first 60 characters — a truncated answer that happened to start
// right, or one that silently dropped a middle section, still passed. That
// is not "no duplicate or gap text", it's "no obviously wrong text".
//
// Fixed to assert the FULL expected text by using the design's own stated
// ground truth for "complete and correct" (BE-DESIGN.md §8.3's common pass
// criteria: "the final answer is complete and matches the transcript after a
// hard reload"): hard-reload the page, re-read the SAME message from the
// freshly reconstructed transcript, and require it to be BYTE-IDENTICAL
// (after whitespace normalization) to `after`. A hard reload discards every
// piece of in-memory/live-streaming state this whole test suite exists to
// stress — what remains is exactly what the server actually persisted, which
// is the only true "full expected text" available to an e2e test whose
// prompt output is a live, non-deterministic LLM response (there is no fixed
// string to assert against up front). `before` is kept as a secondary,
// cheap sanity check (the reload's answer must still contain the same
// opening, catching a wholesale content swap early with a clearer failure).
async function assertNoDuplicateOrGapText(page: Page, before: string, after: string, title?: string) {
  const norm = (s: string) => s.replace(/\s+/g, ' ').trim()
  expect(norm(after).startsWith(norm(before).slice(0, 60))).toBeTruthy()
  await page.reload()
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  // Real-browser follow-up (CI run 36059392570, scenario b): a reload always
  // lands on the welcome/empty chat, not the previous session — confirmed
  // release behaviour, not a product bug. Every OTHER reload in this file
  // reopens the chat via the sidebar afterward; this one, being the shared
  // final-verification helper, didn't — so once a test's own mid-test reload
  // already moved off session A's own deep link, this bare reload had
  // nothing to restore it. Reopen the same way when a title is given.
  if (title !== undefined && (await assistantMessages(page).count()) === 0) {
    await switchToSessionByTitle(page, title)
  }
  await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
  const reloaded = (await bubbleText(assistantMessages(page).first())).trim()
  expect(norm(reloaded)).toBe(norm(after))
}

/** b/c need a real turn that remains in flight while the UI is moved away.
 * Pace only their own gateway's real outbound tokens using the existing h
 * fixture knob. Model output, turn engine, persistence, and reattach stay real.
 */
async function openPacedCatchupChat(page: Page): Promise<GatewayProcess> {
  const gw = await GatewayProcess.start({ env: { OMNIPUS_TEST_ONLY_STREAM_TOKEN_DELAY_MS: '100' } })
  try {
    await page.context().addCookies((await gw.browserStorageState()).cookies)
    await page.goto(gw.baseURL)
    return gw
  } catch (error) {
    await gw.stop()
    throw error
  }
}

test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('a: clean network cut mid-turn, 30s, then back — incremental catch-up', async ({ page, context }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)
    await context.setOffline(true)
    await page.waitForTimeout(30_000)
    await context.setOffline(false)
    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = (await bubbleText(assistantMessages(page).first())).trim()
    await assertNoDuplicateOrGapText(page, before, after)
    // Server log/diagnostic surfaced client-side would confirm
    // "catch_up mode=incremental" — the design leaves the exact
    // surfacing mechanism to Lane A/C's own diagnostics; this scenario
    // asserts the user-visible outcome, which is the contract.
  })

  test('b: dead connection ~60s, server still thinks attached — new connection catches up, no duplicate', async ({ page }) => {
    test.setTimeout(420_000)
    const freeze = await installFreezeProxy(page)
    const gw = await openPacedCatchupChat(page)
    try {
      const before = await startLongTurn(page, LONG_PROMPT, { requireRunning: true })
      await expect(stopButton(page), 'the turn must still be running before the outage or switch').toBeVisible()
      // Real-browser follow-up (orchestrator): a hard reload lands on the
      // welcome/empty chat, not the previous session — the tab does not
      // reopen the last session on its own (checked: this is release
      // behaviour too, not something to change the product for). Capture
      // chat A's real, server-assigned sidebar title BEFORE the reload, the
      // same way scenario c already does, so it can be reopened afterward.
      const titleA = await activeSidebarTitle(page)
      await expect(stopButton(page), 'freeze begins during the real turn, not after done').toBeVisible()
      await freeze.freeze()
      await page.waitForTimeout(60_000)
      // The new connection (opened by a reload) attaches independently while
      // the frozen one is still nominally bound server-side.
      await page.reload()
      await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
      await waitForConnected(page)
      // Reopen chat A via the sidebar — the same real navigation scenario c
      // uses, not page.goto (a second reload would just repeat the same
      // welcome-screen landing).
      await switchToSessionByTitle(page, titleA)
      await waitTurnDoneAfterReload(page)
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
      const after = (await bubbleText(assistantMessages(page).first())).trim()
      await assertNoDuplicateOrGapText(page, before, after, titleA)
    } finally {
      await gw.stop()
    }
  })

  test('c: tab switched to another chat while the turn finishes — incremental on return', async ({ page }) => {
    test.setTimeout(420_000)
    const gw = await openPacedCatchupChat(page)
    try {
      const before = await startLongTurn(page, LONG_PROMPT, { requireRunning: true })
      await expect(stopButton(page), 'the turn must still be running before the outage or switch').toBeVisible()
      // Capture chat A's real, server-assigned sidebar title BEFORE switching
      // away — this is what makes finding it again reliable (see
      // activeSidebarTitle's own comment on why this beats guessing).
      const titleA = await activeSidebarTitle(page)
      // Switch to a fresh chat B while chat A's turn is still running
      // server-side (the turn never depends on a UI connection, ADR-082 P1 —
      // switching the VIEW away changes nothing about it).
      // The slash palette filters out /new while streaming. Use the real
      // sidebar action, which changes the view without stopping chat A.
      await ensureSidebarOpen(page)
      await expect(stopButton(page), 'the view switches away while chat A is still running').toBeVisible()
      await sidebarPanel(page).getByRole('button', { name: 'New chat', exact: true }).click()
      await expect(sidebarPanel(page)).toBeHidden()
      await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      // Switch back to chat A via the sidebar (not page.goto — a reload would
      // clear the in-memory cursor and defeat the point of this scenario; see
      // reconnect-mid-turn.spec.ts's own S-11 note on the same trap). Escape
      // is never pressed — it cancels the running turn.
      await switchToSessionByTitle(page, titleA)
      await expect(stopButton(page), 'reattach reconstructs the still-running turn').toBeVisible()
      // Only NOW does the DOM reflect chat A again, so only now does
      // waitTurnDone's stop-button check actually observe chat A's own
      // state — checking it while chat B was active (the previous draft's
      // sequencing) would have passed immediately regardless of whether A's
      // turn had genuinely finished, since chat B never shows a stop button.
      await waitTurnDone(page)
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
      const after = (await bubbleText(assistantMessages(page).first())).trim()
      await assertNoDuplicateOrGapText(page, before, after, titleA)
    } finally {
      await gw.stop()
    }
  })

  test('d: no tab at all while the turn finishes (laptop sleep) — reopen shows the complete answer', async ({ page, context }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)
    await page.close()
    const page2 = await context.newPage()
    await page2.goto('/')
    await expect(chatInput(page2)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page2)
    // page2 has never shown its own stop button (it is a brand-new page,
    // never having rendered chat A live) — the same reload-baseline issue
    // as scenario b's fix above (waitTurnDone's "hidden" check would be
    // trivially, immediately true here regardless of whether the turn had
    // actually finished server-side during the tab close).
    await waitTurnDoneAfterReload(page2)
    await expect(assistantMessages(page2)).toHaveCount(1, { timeout: 30_000 })
    const after = (await bubbleText(assistantMessages(page2).first())).trim()
    await assertNoDuplicateOrGapText(page2, before, after)
  })

  test('e: two tabs on one chat — second tab shows the user message and the identical answer, cursors equal', async ({ page, context }) => {
    test.setTimeout(420_000)
    await startLongTurn(page)
    const page2 = await context.newPage()
    await page2.goto('/')
    await expect(chatInput(page2)).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page2)
    await waitTurnDone(page)
    await waitTurnDone(page2)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    await expect(assistantMessages(page2)).toHaveCount(1, { timeout: 30_000 })
    const t1 = (await bubbleText(assistantMessages(page).first())).replace(/\s+/g, ' ').trim()
    const t2 = (await bubbleText(assistantMessages(page2).first())).replace(/\s+/g, ' ').trim()
    expect(t2).toBe(t1)
    await page2.close()
  })

  test('f: two chats active during an outage — A incremental, B complete on switch', async ({ page, context }) => {
    test.setTimeout(420_000)
    const beforeA = await startLongTurn(page)
    await startNewChat(page)
    const beforeB = await startLongTurn(page)
    await context.setOffline(true)
    await page.waitForTimeout(45_000)
    await context.setOffline(false)
    await waitTurnDone(page) // chat B, currently foreground
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const afterB = (await bubbleText(assistantMessages(page).first())).trim()
    await assertNoDuplicateOrGapText(page, beforeB, afterB)
    void beforeA // chat A's own convergence is scenario c's concern; this
    // scenario's unique assertion is B's incremental catch-up under outage.
  })

  test('g: idle 31+ minutes, then send — the answer is shown (attempt 1 dropped it)', async ({ page }) => {
    test.setTimeout(35 * 60_000)
    const input = chatInput(page)
    await expect(input).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)
    await startNewChat(page)
    // Founder decision Q6: real wait. Lane A's test-only idle-eviction
    // shortcut (`b588bbb04`) is `OMNIPUS_TEST_ONLY_HUB_IDLE_EVICT_SECONDS`
    // — an env var read once at gateway startup
    // (`newHubRegistry`/`hubIdleEvictAfterEnvOverrideVar`,
    // pkg/gateway/ws_session_hub.go), never a config file or REST call, so
    // it must be set on the gateway PROCESS before this spec runs (the
    // orchestrator's job, not this file's — a Playwright test cannot set an
    // env var retroactively on an already-running binary). When set (e.g.
    // to 5-10s for this scenario), the wait below only needs to exceed that
    // shortened window, not the real 31 minutes; left at the real 31
    // minutes here as the safe default when the knob is NOT set.
    await page.waitForTimeout(31 * 60_000)
    await input.fill('are you still there?')
    await input.press('Enter')
    await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    expect((await bubbleText(assistantMessages(page).first())).trim().length).toBeGreaterThan(0)
  })

  // F1: the held-open tab must choose the same single status regardless of
  // whether the optional lifecycle list or the boot-mismatch attach arrives first.
  for (const ordering of ['catch-up first', 'lifecycle first'] as const) {
    test(`h: gateway restart with tab open — Interrupted alone, Generate again completes (${ordering})`, async ({ page }) => {
      test.setTimeout(420_000)
      const gw = await GatewayProcess.start({ env: { OMNIPUS_TEST_ONLY_STREAM_TOKEN_DELAY_MS: '300' } })
      let crashed = false
      let releaseList!: () => void
      let listReachedPage = false
      const listHeld = new Promise<void>((resolve) => { releaseList = resolve })
      const waitForLifecycleList = () => expect.poll(() => listReachedPage, {
        timeout: 60_000, message: 'The SPA must refetch its lifecycle list after reconnecting; the ordering gate cannot wait forever.',
      }).toBe(true)
      let regenerating = false
      let targetSessionId: string | null = null
      const retryDone: import('@/lib/api/generated/asyncapi-types').DoneFrame[] = []
      const retryErrors: import('@/lib/api/generated/asyncapi-types').ErrorFrame[] = []
      page.on('websocket', (socket) => socket.on('framereceived', ({ payload }) => {
        const frame = JSON.parse(payload.toString()) as import('@/lib/api/generated/asyncapi-types').ServerFrame
        if (!regenerating || !('session_id' in frame) || frame.session_id !== targetSessionId) return
        if (frame.type === 'done') retryDone.push(frame)
        if (frame.type === 'error') retryErrors.push(frame)
      }))
      try {
        // Delay a real response, never fabricate lifecycle state or mock the UI.
        await page.route(/\/api\/v1\/sessions(?:\?.*)?$/, async (route) => {
          if (!crashed) { await route.continue(); return }
          let response
          try { response = await route.fetch() }
          catch (error) {
            // A request hitting the intentional SIGKILL window must still fail
            // on the wire, just as it would without this timing interceptor.
            if (!/ECONNREFUSED/.test(String(error))) throw error
            await route.abort('connectionrefused')
            return
          }
          if (ordering === 'catch-up first') await listHeld
          await route.fulfill({ response })
          listReachedPage = true
        })
        if (ordering === 'lifecycle first') {
          await page.routeWebSocket(/\/api\/v1\/chat\/ws/, (socket) => {
            const server = socket.connectToServer()
            socket.onMessage((message) => server.send(message))
            server.onMessage(async (message) => {
              const frame = JSON.parse(message.toString()) as import('@/lib/api/generated/asyncapi-types').ServerFrame
              if (crashed && frame.type === 'catch_up_complete') await waitForLifecycleList()
              socket.send(message)
            })
            socket.onClose((code, reason) => server.close({ code, reason }))
            server.onClose((code, reason) => socket.close({ code, reason }))
          })
        }

        // The isolated process owns its own login; never replace its cookie
        // with the shared gateway's, or re-login behind this open tab on restart.
        await page.goto(gw.baseURL)
        await expect(page.locator('#login-username')).toBeVisible({ timeout: 15_000 })
        await page.locator('#login-username').pressSequentially(gw.adminUsername)
        await page.locator('#login-password').pressSequentially(gw.adminPassword)
        await page.getByRole('button', { name: 'Sign in' }).click()
        await expect(page).not.toHaveURL(/\/#\/login/, { timeout: 15_000 })
        const input = chatInput(page)
        await expect(input).toBeVisible({ timeout: 15_000 })
        await waitForConnected(page)
        await startNewChat(page)
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
        // Token delay plus the explicit running precondition make a finite
        // answer long enough to interrupt, and still let its manual retry finish.
        await input.fill(LONG_PROMPT)
        await input.press('Enter')
        await expect(stopButton(page)).toBeVisible({ timeout: 15_000 })
        const liveAnswer = page.locator('[data-message-id]:not(.flex-row-reverse)')
        await expect(liveAnswer).toHaveCount(1, { timeout: 15_000 })
        // Entire bubble text includes the agent name and Thinking… before any
        // token. Only rendered markdown proves real answer content arrived.
        await expect(liveAnswer.locator('.prose-sm')).toContainText(/\S/, { timeout: 60_000 })
        const chat = page.locator('[data-active-session-id]')
        const sessionId = await chat.getAttribute('data-active-session-id')
        expect(sessionId).not.toBeNull()
        expect(sessionId).not.toBe('')
        expect(sessionId).not.toBe('__pending')
        targetSessionId = sessionId
        await expect(liveAnswer).toHaveAttribute('data-status', 'running')
        await expect(stopButton(page)).toBeVisible()
        crashed = true
        await gw.kill9()
        await gw.restart({ relogin: false })

        const notice = page.getByTestId('restart-interrupted-notice')
        const generateAgain = page.getByRole('button', { name: /Generate again/i })
        const assertSingleStatus = async () => {
          await expect(notice).toHaveCount(1, { timeout: 60_000 })
          await expect(notice).toContainText('Interrupted · The restart cut this answer off. Send a message to continue.')
          await expect(assistantConnectionStatus(page)).toHaveCount(0)
          await expect(page.getByText(/couldn't be finished/i)).toHaveCount(0)
          await expect(generateAgain).toHaveCount(1)
          await expect(notice.getByRole('button', { name: 'Generate again', exact: true })).toBeVisible()
          await expect(stopButton(page)).toBeHidden()
          await expect(chat).toHaveAttribute('data-active-session-id', sessionId!)
        }
        await assertSingleStatus()
        // The late-list case has already asserted the status while REST is
        // held. Releasing it must not replace or duplicate that same status.
        releaseList()
        await waitForLifecycleList()
        await assertSingleStatus()
        await test.info().attach(`interrupted-${ordering}`, { body: await page.screenshot(), contentType: 'image/png' })

        regenerating = true
        await generateAgain.click()
        await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
        await expect(notice).toHaveCount(0)
        await expect(generateAgain).toHaveCount(0)
        await waitTurnDone(page)
        const answer = assistantMessages(page)
        await expect(answer).toHaveCount(1, { timeout: 30_000 })
        await expect(answer).toHaveAttribute('data-status', 'complete')
        await expect(answer.locator('p').first()).toContainText(/\S/)
        expect(retryErrors, 'Generate again must not end in a provider/turn error').toEqual([])
        expect(retryDone, 'Generate again must complete exactly one new answer').toHaveLength(1)
        expect(retryDone[0].stats?.turn_failed).not.toBe(true)
        expect(retryDone[0].stats?.truncated).not.toBe(true)
        expect(retryDone[0].stats?.replay_error).not.toBe(true)
        await expect(userMessages(page)).toHaveCount(1)
        await expect(chat).toHaveAttribute('data-active-session-id', sessionId!)
        await expect(notice).toHaveCount(0)
        await expect(generateAgain).toHaveCount(0)
        await expect(assistantConnectionStatus(page)).toHaveCount(0)
        await expect(page.getByText(/couldn't be finished/i)).toHaveCount(0)
        await test.info().attach(`completed-${ordering}`, { body: await page.screenshot(), contentType: 'image/png' })
      } finally {
        releaseList()
        await gw.stop()
      }
    })
  }

  test('i: message typed while offline — appears at the end, then its answer; no duplicate; ticks received → working', async ({ page, context }) => {
    test.setTimeout(180_000)
    const input = chatInput(page)
    await expect(input).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)
    await startNewChat(page)
    await context.setOffline(true)
    await page.evaluate(() => window.dispatchEvent(new Event('offline')))
    await input.fill('typed while offline')
    await input.press('Enter')
    // Real-browser follow-up (orchestrator): the previous version of this
    // scenario asserted the CONNECTION banner (`connection-status-line`)
    // within 20s of going offline. That contradicts the quiet-disconnect
    // design on purpose (ConnectionStatus.tsx's own CHAT_NOTICE_MS = 120s —
    // no chat-connection indicator is SUPPOSED to appear for a drop this
    // short, to avoid flickering a banner for a brief blip; #833/ADR-082).
    // Not a product regression — a test asserting behaviour the design
    // deliberately prevents. The scenario's OWN title names the real
    // signal: the per-message delivery ticks
    // (`user-message-delivery-status`, ConnectionStatus.tsx's
    // UserMessageDeliveryStatus) — 'queued' while offline ("Not sent yet,
    // will be sent automatically"), transitioning to 'working' once the
    // agent picks it up after the reconnect delivers it.
    //
    // Real-browser follow-up (orchestrator): UserMessageDeliveryStatus's
    // own text is not a visible text node at all — StatusTooltip renders
    // only an icon as the visible child and puts the whole description on
    // the inner IconButton's aria-label (ConnectionStatus.tsx's
    // StatusTooltip). toHaveText() on the container therefore always saw
    // an empty string (CI: "Received string: \"\""). Read the accessible
    // name instead, via the role query that actually matches how this
    // renders.
    const deliveryStatus = page.getByTestId('user-message-delivery-status')
    await expect(deliveryStatus.getByRole('button', { name: /not sent yet/i })).toBeVisible({ timeout: 15_000 })
    await context.setOffline(false)
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(deliveryStatus.getByRole('button', { name: /is working on it/i })).toBeVisible({ timeout: 30_000 })
    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    // The user's own message is the pending tail (§4.7) — it must render at
    // the end of history, never duplicated, once user_message reconciles it.
    //
    // Real-browser follow-up (orchestrator): an unscoped page.getByText
    // resolved to 2 elements — investigated (Sidebar.tsx's isPinned
    // defaults false, so the drawer isn't rendered at all without an
    // explicit open, ruling out a stale sidebar row; the two sr-only
    // aria-live announcers in ChatScreen.tsx don't echo user message text
    // either) without being able to pin down the second match from CI
    // evidence alone (the 382MB trace artifact did not finish downloading
    // in time — noted honestly, not guessed past). Scoped to actual
    // message bubbles specifically (userMessages — every real chat row
    // carries data-message-id, which nothing else on the page does) rather
    // than broad page text — this directly answers what this assertion is
    // actually for ("is the message duplicated IN THE CHAT"), and does NOT
    // mask a real duplicate: if both of CI's two matches turn out to be
    // actual message bubbles, this scoped locator still finds both and
    // still fails.
    await expect(userMessages(page).filter({ hasText: 'typed while offline' })).toHaveCount(1)
  })
})
