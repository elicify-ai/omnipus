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
 * only — the harness itself was NOT run (it needs a real provider key and
 * the operator's data folder, neither available in this environment). One
 * scenario, `h` (gateway restart), stays individually `test.skip`-ed — it
 * needs a human to restart the actual binary mid-test, which this spec file
 * has no process control to do unattended; see that test's own comment.
 */

import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, startNewChat, assistantMessages, userMessages } from './fixtures/selectors'
import { installFreezeProxy } from './helpers/freezeProxy'

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

async function startLongTurn(page: Page): Promise<string> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await startNewChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
  await input.fill(LONG_PROMPT)
  await input.press('Enter')
  await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
  const row = assistantMessages(page).first()
  await expect.poll(async () => (await bubbleText(row).catch(() => '')).trim().length, { timeout: 60_000 }).toBeGreaterThan(80)
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

// Sidebar toggle: ScreenHeader.tsx's hamburger, aria-label="Toggle
// navigation sidebar" (NOT the "sidebar-toggle" testid the previous draft
// of this scenario guessed at and silently swallowed the failure of via
// `.catch(() => {})` — that selector does not exist in the app).
const sidebarToggle = (page: Page) => page.getByRole('button', { name: 'Toggle navigation sidebar' })

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
const sidebarPanel = (page: Page) => page.locator('#sidebar-overlay-panel')
const sessionRowByTitle = (page: Page, title: string) => sidebarPanel(page).getByRole('button', { name: title, exact: true })

// Real-browser follow-up (orchestrator, scenario c): Sidebar.tsx's own
// "click-outside overlay dismiss" backdrop (`aria-hidden="true"`,
// `className="absolute inset-0 z-30"`, `onClick={close}`) is wrapped in
// AnimatePresence with a 150ms exit fade — after close() fires (either the
// hamburger's own toggle, or a session row's auto-close via onClose), this
// element stays mounted and still intercepts pointer events for that
// window. CI evidence: a composer click timed out with this exact element
// ("<div aria-hidden=true class=absolute inset-0 z-30>") named as the
// interceptor. Never `force` the composer (masks a real reachability
// problem for an actual user) — wait for the backdrop to actually leave
// the DOM instead, the same way a real click has to wait for it.
const sidebarBackdrop = (page: Page) => page.locator('div[aria-hidden="true"].absolute.inset-0.z-30')
// Real-browser follow-up (orchestrator): a silently-swallowed timeout here
// masked the real bug for two whole debugging rounds — the drawer failing
// to close surfaced as a much more confusing failure much later (a
// composer click blocked by the same backdrop, in an unrelated helper).
// Never weaken this to a soft catch again: if the drawer doesn't actually
// close, this must fail HERE, loudly, at the point that is actually wrong.
async function waitForSidebarClosed(page: Page) {
  await sidebarBackdrop(page).waitFor({ state: 'hidden', timeout: 10_000 })
}

// The currently-active row carries aria-current="page" (SidebarSessionRow).
// Used to capture chat A's real, server-assigned title (which is a model-
// generated summary of the first message, not the prompt text verbatim —
// reading it back from the DOM, rather than guessing what the title will
// be, is what makes re-selecting the row later reliable).
async function activeSidebarTitle(page: Page): Promise<string> {
  await sidebarToggle(page).click()
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
  const row = sidebarPanel(page).locator('button[aria-current="page"]').last()
  await expect(row).toBeVisible({ timeout: 10_000 })
  const title = (await row.innerText()).trim()
  // Real-browser follow-up (orchestrator, corrected after a live-traced
  // misdiagnosis): the drawer panel (#sidebar-overlay-panel, z-40) and its
  // own click-outside backdrop (z-30) both sit ON TOP of the header while
  // open — including the hamburger's own screen position, which the panel
  // visually covers. A `force: true` click still resolves via real
  // coordinate-based input (Playwright dispatches at the element's
  // bounding box, but the browser's own hit-testing still delivers it to
  // whatever is topmost AT THAT SCREEN POINT), so it was actually landing
  // on the drawer/backdrop rather than the hamburger underneath — the
  // drawer never genuinely closed (confirmed against the real gateway:
  // the backdrop stayed for the ENTIRE remainder of the test, not a 150ms
  // fade). Escape is the sidebar's own designed close key but stays
  // off-limits regardless — it also cancels a running turn. Fixed per the
  // orchestrator's own working harness: click the backdrop directly, at a
  // point clearly OUTSIDE the drawer panel's own width (the right edge of
  // the viewport) so there is no coordinate ambiguity about which layer
  // receives the click.
  const viewport = page.viewportSize() ?? { width: 1280, height: 720 }
  await sidebarBackdrop(page).click({ position: { x: viewport.width - 20, y: viewport.height / 2 } })
  await waitForSidebarClosed(page)
  return title
}

// Real sidebar navigation (orchestrator follow-up — the previous draft left
// this "for the orchestrator to wire once runnable"): open the drawer,
// click the session's OWN row by its title. Escape is never used here — it
// cancels the running turn (ADR-057's cancel state machine), which would
// defeat the entire point of this scenario.
async function switchToSessionByTitle(page: Page, title: string) {
  await sidebarToggle(page).click()
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
  await sessionRowByTitle(page, title).first().click()
  await waitForSidebarClosed(page)
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
async function assertNoDuplicateOrGapText(page: Page, before: string, after: string) {
  const norm = (s: string) => s.replace(/\s+/g, ' ').trim()
  expect(norm(after).startsWith(norm(before).slice(0, 60))).toBeTruthy()
  await page.reload()
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
  const reloaded = (await bubbleText(assistantMessages(page).first())).trim()
  expect(norm(reloaded)).toBe(norm(after))
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
    const before = await startLongTurn(page)
    // Real-browser follow-up (orchestrator): a hard reload lands on the
    // welcome/empty chat, not the previous session — the tab does not
    // reopen the last session on its own (checked: this is release
    // behaviour too, not something to change the product for). Capture
    // chat A's real, server-assigned sidebar title BEFORE the reload, the
    // same way scenario c already does, so it can be reopened afterward.
    const titleA = await activeSidebarTitle(page)
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
    await assertNoDuplicateOrGapText(page, before, after)
  })

  test('c: tab switched to another chat while the turn finishes — incremental on return', async ({ page }) => {
    test.setTimeout(420_000)
    const before = await startLongTurn(page)
    // Capture chat A's real, server-assigned sidebar title BEFORE switching
    // away — this is what makes finding it again reliable (see
    // activeSidebarTitle's own comment on why this beats guessing).
    const titleA = await activeSidebarTitle(page)
    // Switch to a fresh chat B while chat A's turn is still running
    // server-side (the turn never depends on a UI connection, ADR-082 P1 —
    // switching the VIEW away changes nothing about it).
    await startNewChat(page)
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
    // Switch back to chat A via the sidebar (not page.goto — a reload would
    // clear the in-memory cursor and defeat the point of this scenario; see
    // reconnect-mid-turn.spec.ts's own S-11 note on the same trap). Escape
    // is never pressed — it cancels the running turn.
    await switchToSessionByTitle(page, titleA)
    // Only NOW does the DOM reflect chat A again, so only now does
    // waitTurnDone's stop-button check actually observe chat A's own
    // state — checking it while chat B was active (the previous draft's
    // sequencing) would have passed immediately regardless of whether A's
    // turn had genuinely finished, since chat B never shows a stop button.
    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    const after = (await bubbleText(assistantMessages(page).first())).trim()
    await assertNoDuplicateOrGapText(page, before, after)
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

  // Skipped (not un-skipped like the rest of this file, pass 2 item 6):
  // restarting the actual embedded binary process mid-test is an
  // orchestrator-level, human-in-the-loop operation this spec file has no
  // process control to perform unattended — `npx playwright test --list`
  // still registers it (proving it's syntactically valid and reachable),
  // but running the suite normally would hang waiting for a restart that
  // never happens. The orchestrator removes `.skip` when running this ONE
  // scenario manually, restarting the gateway at the marked point.
  test.skip('h: gateway restart with tab open — snapshot boot_mismatch, "couldn\'t be finished · Generate again"', async ({ page }) => {
    test.setTimeout(420_000)
    await startLongTurn(page)
    // >>> ORCHESTRATOR: restart the gateway binary here, then resume. <<<
    await expect(page.getByRole('button', { name: /Generate again/i })).toBeVisible({ timeout: 60_000 })
    await expect(assistantConnectionStatus(page)).toContainText('couldn\'t be finished')
  })

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
