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
 *
 * STATUS (#1661, 2026-11-06): the token-delay knob is retired for b/c/h.
 * The held-stream provider (fixtures/held-stream-provider.ts) scripts the
 * stream itself and holds the mid-turn window open by construction, so the
 * mid-turn gates no longer depend on any model's emission profile (on the
 * CI primary model the whole answer used to arrive only at completion).
 *
 * STATUS (#1766): a/d/e/f now use that same isolated held-stream harness,
 * with exact scripted-answer checks in addition to the original prefix,
 * reload and two-tab equality assertions. e also checks the user message and
 * both clients' resume cursors; f proves both turns are concurrently held and
 * verifies A's incremental recovery as well as B's complete answer on switch.
 * g alone still uses the shared live-model gateway and its real 31-minute wait.
 */

import { expect, type Page } from '@playwright/test'
import type { AttachSessionFrame, CatchUpCompleteFrame, ClientFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { SessionDetail } from '@/lib/api/generated/openapi-types'
import { test } from './fixtures/console-errors'
import { chatInput, waitForConnected, assistantMessages, userMessages } from './fixtures/selectors'
import { freshChat, openSessionByDeepLink, originOf } from './fixtures/session-setup'
import { installFreezeProxy } from './helpers/freezeProxy'
import { GatewayProcess } from './fixtures/gateway-process'
import { E2E_FALLBACK } from './fixtures/e2e-model'
import { normalizedAssistantText } from './fixtures/assistant-text'
import {
  attachHeldStreamEvidence,
  expectTurnHeldMidStream,
  startHeldStreamProvider,
  type HeldStreamProvider,
} from './fixtures/held-stream-provider'

const stopButton = (page: Page) => page.locator('[data-testid="stop-btn"]')
// An assistant reply row in ANY status, including the running one (the shared
// assistantMessages skips running rows, which mid-turn checks need). System
// rows (provider-fallback note, restart notice, clear-context divider) also
// carry data-message-id and are excluded, as in assistantMessages (#1783).
const ANY_STATUS_ASSISTANT_ROW = '[data-message-id]:not(.flex-row-reverse):not([data-message-role="system"])'
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
    // Chrome with its own testid: model footer (MessageItem/ChatScreen
    // "Mirrors MessageItem.tsx" pair), the agent name above assistant
    // messages, participant/reply headers, and the connection-status chip.
    clone
      .querySelectorAll(
        '[data-testid="message-model"], [data-testid="agent-label"], ' +
          '[data-testid="participant-label"], [data-testid="reply-header"], ' +
          '[data-testid="assistant-connection-status"]',
      )
      .forEach((n) => n.remove())
    // Volatile status phrases WITHOUT a testid (AgentStatusIndicator.tsx
    // exports: IDLE_STATUS_TEXT, UNAVAILABLE_STATUS_TEXT; plus the
    // thinking placeholder): they prefix the row text and CHANGE across the
    // exact events this file tests (a network cut flips Idle →
    // Unavailable/reconnecting), so a whole-row read compared status chrome,
    // not content — scenario (a) failed on before="AvaIdleThe tide…" vs
    // after="AvaUnavailable/reconnectingThe tide…" while the essay itself
    // was byte-identical across the cut (measured locally, lane nav-e2e-ff).
    // EXACT-text match only, so essay words are never touched.
    const statusPhrases = new Set(['Idle', 'Unavailable/reconnecting', 'Thinking...'])
    clone.querySelectorAll('span, div').forEach((n) => {
      if (statusPhrases.has((n.textContent ?? '').trim()) && n.children.length === 0) n.remove()
    })
    // The message text itself renders inside MessageBubble's `.prose-sm`
    // div on historical rows (MessageItem.tsx — the only prose-sm in
    // src/components/chat); the live AssistantUI row has none, so read the
    // chrome-stripped remainder there.
    const bubbles = Array.from(clone.querySelectorAll('.prose-sm'))
    if (bubbles.length > 0) return bubbles.map((n) => (n as HTMLElement).innerText).join('\n')
    return (clone as HTMLElement).innerText
  })
}

async function startLongTurn(
  page: Page, prompt: string = LONG_PROMPT, options: { requireRunning?: boolean } = {},
): Promise<string> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await freshChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
  // Bounded (fail fast): an unbounded fill/press rides the locator default
  // (config sets no actionTimeout) out to the whole test timeout when a modal
  // or a dead composer blocks it — the same fail-slow class that burned the
  // llm-agents shard (run 38112714098). The composer was already asserted
  // visible+enabled above, so 15s is generous headroom, not a new budget.
  await input.fill(prompt)
  await input.press('Enter', { timeout: 15_000 })
  await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
  // A completed-only selector waits for done before returning, so it cannot
  // establish the mid-turn precondition b/c require. Other cases retain their
  // existing completed-content baseline until their own coverage is audited.
  const row = options.requireRunning
    ? page.locator(ANY_STATUS_ASSISTANT_ROW).first()
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

// Capture which chat is on screen so it can be reopened later (see below).
async function activeChatSessionId(page: Page): Promise<string> {
  // The sidebar no longer lists per-session rows, so a chat has no sidebar
  // title to capture and re-find (the old read - the last
  // `button[aria-current="page"]` - now only ever matches the workspace header
  // "My Workspace"). The stable handle for "this chat" is the session id the
  // chat surface itself is bound to (ChatScreen stamps data-active-session-id).
  const bound = page.locator('[data-active-session-id]').first()
  await expect(bound).toBeAttached({ timeout: 15_000 })
  const id = ((await bound.getAttribute('data-active-session-id')) ?? '').trim()
  expect(id, 'the chat surface must be bound to a session').not.toBe('')
  return id
}

// Go (back) to a chat through the SPA's own /#/sessions/<id> route. Not
// page.goto of a different document: the router is a hash router
// (createHashHistory, src/main.tsx), so a same-origin hash goto is a
// same-document navigation - the in-memory cursor survives, which the
// "no reload" marker check in scenario c proves. Escape is never used - it
// cancels the running turn (ADR-057).
async function switchToSession(page: Page, sessionId: string) {
  await openSessionByDeepLink(page, sessionId, originOf(page))
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
  expect(
    norm(after).startsWith(norm(before).slice(0, 60)),
    // Named, with both readings printed: a bare toBeTruthy() made this
    // failure undiagnosable from the report alone (the two texts are the
    // whole question — live-streamed prefix vs reloaded final text).
    `the reloaded final text must begin with the pre-cut streamed prefix — ` +
      `before=${JSON.stringify(norm(before).slice(0, 160))} after=${JSON.stringify(norm(after).slice(0, 160))}`,
  ).toBeTruthy()
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
    await switchToSession(page, title)
  }
  await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
  const reloaded = (await bubbleText(assistantMessages(page).first())).trim()
  expect(norm(reloaded)).toBe(norm(after))
}

/** Catch-up scenarios need a real turn that remains in flight while the UI is moved away —
 * for as long as the test wants, independent of any model's emission profile
 * (#1661): their gateway is pointed at the held-stream provider, which keeps
 * the scripted stream open until the test calls release(). Turn engine,
 * persistence, reattach and catch-up stay real. Fails safe: a failed boot or
 * navigation stops the provider along with the gateway. */
async function openHeldCatchupChat(page: Page, provider: HeldStreamProvider): Promise<GatewayProcess> {
  const gw = await GatewayProcess.start({ apiBase: provider.url, model: E2E_FALLBACK.model })
  try {
    await page.context().addCookies((await gw.browserStorageState()).cookies)
    await page.goto(gw.baseURL)
    return gw
  } catch (error) {
    await gw.stop()
    await provider.close()
    throw error
  }
}

const normalizeText = (text: string) => text.replace(/\s+/g, ' ').trim()

// Observe real wire traffic without replacing the socket, frames or store.
function observeCatchup(page: Page) {
  const received: ServerFrame[] = []
  const sent: ClientFrame[] = []
  page.on('websocket', (socket) => {
    socket.on('framereceived', ({ payload }) => received.push(JSON.parse(payload.toString()) as ServerFrame))
    socket.on('framesent', ({ payload }) => sent.push(JSON.parse(payload.toString()) as ClientFrame))
  })
  return { received, sent }
}

function latestCatchup(frames: ServerFrame[], sessionId: string) {
  return frames.filter((frame): frame is CatchUpCompleteFrame => frame.type === 'catch_up_complete' && frame.session_id === sessionId).at(-1)
}

// #1766: no live-essay length wait. The fixture proves one open, unreleased
// provider call with at least two deltas; the running DOM must show its sent prefix.
async function startHeldTurn(page: Page, provider: HeldStreamProvider, label: string, prompt = LONG_PROMPT, script?: string) {
  await expect(chatInput(page)).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  const chat = await freshChat(page)
  await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
  await chatInput(page).fill(prompt)
  await chatInput(page).press('Enter', { timeout: 15_000 })
  await expectTurnHeldMidStream(provider, label, script)
  const row = page.locator(ANY_STATUS_ASSISTANT_ROW).first()
  await expect(row).toHaveAttribute('data-status', 'running')
  await expect(row.locator('.prose-sm')).toContainText(/\S/)
  await expect(stopButton(page)).toBeVisible()
  // Read the visible markdown body, not the adjacent live Thinking status.
  // DOM first, then fixture: the UI may lag, never lead.
  const visible = normalizeText(await row.locator('.prose-sm').innerText())
  expect(visible, `${label}: the running answer has visible text`).not.toBe('')
  const alreadySent = provider.expectedHeldAnswer(script)
  expect(visible, `${label}: visible text is a prefix of the provider's already-sent deltas`).toBe(alreadySent.slice(0, visible.length))
  return { sessionId: chat.sessionId, before: (await bubbleText(row)).trim() }
}

async function assertScriptedAnswer(page: Page, provider: HeldStreamProvider, before: string, sessionId: string, label: string, script?: string) {
  await expect(assistantMessages(page), `${label}: exactly one complete answer`).toHaveCount(1, { timeout: 30_000 })
  await expect(assistantMessages(page)).toHaveAttribute('data-status', 'complete')
  const after = (await bubbleText(assistantMessages(page).first())).trim()
  expect(await normalizedAssistantText(assistantMessages(page).first()), `${label}: exact scripted answer, no missing or duplicate deltas`).toBe(provider.expectedHeldAnswer(script))
  // Retain both original checks: pre-cut prefix and full hard-reload identity.
  await assertNoDuplicateOrGapText(page, before, after, sessionId)
  return normalizeText(after)
}

async function expectPersistedAnswer(gw: GatewayProcess, sessionId: string, provider: HeldStreamProvider, script?: string) {
  await expect.poll(async () => {
    const result = await gw.apiFetch<SessionDetail>('GET', `/api/v1/sessions/${sessionId}`)
    expect(result.ok, 'completion receipt: session detail is readable').toBe(true)
    return result.body.messages?.filter((message) => message.role === 'assistant').map((message) => normalizeText(message.content ?? ''))
  }, { timeout: 30_000, message: 'the released turn is durably complete even with no tab attached' }).toEqual([provider.expectedHeldAnswer(script)])
}

function expectReleased(provider: HeldStreamProvider, label: string, count = 1) {
  expect(provider.evidence.streamRequests, `${label}: exactly the scripted calls`).toBe(count)
  expect(provider.evidence.released, `${label}: only release finished the turns`).toBe(count)
  expect(provider.evidence.streamsClosed, `${label}: every released stream closed`).toBe(count)
  expect(provider.evidence.errors, `${label}: no unexpected provider traffic`).toEqual([])
}

test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  // #1766: same own-gateway, scripted-provider, zero-retry scope as b/c.
  // Only the paid model edge is fake; no catch-up frame or product state is.
  test.describe.configure({ retries: 0 })

  test('a: clean network cut mid-turn, 30s, then back — incremental catch-up', async ({ page, context }, info) => {
    test.setTimeout(420_000)
    const provider = await startHeldStreamProvider({ promptIncludes: LONG_PROMPT })
    const wire = observeCatchup(page)
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const { before, sessionId } = await startHeldTurn(page, provider, 'catchup a')
      wire.received.length = 0
      await context.setOffline(true)
      await page.waitForTimeout(30_000)
      provider.release()
      await expectPersistedAnswer(gw, sessionId, provider)
      await context.setOffline(false)
      await expect.poll(() => latestCatchup(wire.received, sessionId)?.mode, {
        timeout: 30_000, message: 'catchup a: reconnect resumes incrementally without a reload',
      }).toBe('incremental')
      await waitTurnDone(page)
      await assertScriptedAnswer(page, provider, before, sessionId, 'catchup a')
      expectReleased(provider, 'catchup a')
    } finally {
      try { await attachHeldStreamEvidence(info, 'catchup-a', provider) }
      finally { await gw.stop(); await provider.close() }
    }
  })

  test('d: no tab at all while the turn finishes (laptop sleep) — reopen shows the complete answer', async ({ page, context }, info) => {
    test.setTimeout(420_000)
    const provider = await startHeldStreamProvider({ promptIncludes: LONG_PROMPT })
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const { before, sessionId } = await startHeldTurn(page, provider, 'catchup d')
      const chunksAtClose = provider.evidence.chunksSent
      const streamsClosedAtClose = provider.evidence.streamsClosed
      await page.close()
      expect(context.pages(), 'catchup d: no tab exists when the turn finishes').toHaveLength(0)
      // F1 / ADR-082: a real zero-tab window must not abort the turn. Count 20
      // further provider deltas (about 3s at its 150ms cadence), not a sleep.
      await expect.poll(() => provider.evidence.chunksSent, {
        timeout: 15_000, message: 'catchup d: at least 20 more deltas flow with no tab attached',
      }).toBeGreaterThanOrEqual(chunksAtClose + 20)
      await expectTurnHeldMidStream(provider, 'catchup d without tabs')
      expect(provider.evidence.streamsClosed, 'catchup d: no stream closed during the zero-tab window').toBe(streamsClosedAtClose)
      expect(provider.evidence.released, 'catchup d: zero-tab survival precedes any scripted release').toBe(0)
      await info.attach('catchup-d-zero-tab-window', {
        body: JSON.stringify({ chunksAtClose, chunksBeforeRelease: provider.evidence.chunksSent, streamsClosedAtClose, streamsClosedBeforeRelease: provider.evidence.streamsClosed, released: provider.evidence.released }),
        contentType: 'application/json',
      })
      provider.release()
      await expectPersistedAnswer(gw, sessionId, provider)
      const page2 = await context.newPage()
      await page2.goto(gw.baseURL)
      await expect(chatInput(page2)).toBeVisible({ timeout: 15_000 })
      await waitForConnected(page2)
      await waitTurnDoneAfterReload(page2)
      await assertScriptedAnswer(page2, provider, before, sessionId, 'catchup d')
      expectReleased(provider, 'catchup d')
    } finally {
      try { await attachHeldStreamEvidence(info, 'catchup-d', provider) }
      finally { await gw.stop(); await provider.close() }
    }
  })

  test('e: two tabs on one chat — second tab shows the user message and the identical answer, cursors equal', async ({ page, context }, info) => {
    test.setTimeout(420_000)
    const provider = await startHeldStreamProvider({ promptIncludes: LONG_PROMPT })
    const wire1 = observeCatchup(page)
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const { before, sessionId } = await startHeldTurn(page, provider, 'catchup e')
      const page2 = await context.newPage()
      const wire2 = observeCatchup(page2)
      await page2.goto(gw.baseURL)
      await expect(chatInput(page2)).toBeVisible({ timeout: 15_000 })
      await waitForConnected(page2)
      await expect(stopButton(page2), 'second tab attaches to the same still-running turn').toBeVisible()
      await expectTurnHeldMidStream(provider, 'catchup e with both tabs')
      for (const tab of [page, page2]) {
        await expect(userMessages(tab), 'the user message is present exactly once in each tab').toHaveCount(1)
        await expect(userMessages(tab)).toContainText(LONG_PROMPT)
      }
      provider.release()
      await waitTurnDone(page)
      await waitTurnDone(page2)
      const t1 = await assertScriptedAnswer(page, provider, before, sessionId, 'catchup e first tab')
      const t2 = await assertScriptedAnswer(page2, provider, before, sessionId, 'catchup e second tab')
      expect(t2, 'both tabs show the identical complete answer').toBe(t1)
      // Hard reload above commits a catch-up cursor in each real client.
      await expect.poll(() => latestCatchup(wire1.received, sessionId)?.seq).toBeGreaterThan(0)
      await expect.poll(() => latestCatchup(wire2.received, sessionId)?.seq).toBe(latestCatchup(wire1.received, sessionId)?.seq)
      const cursor = latestCatchup(wire1.received, sessionId)!
      expect(latestCatchup(wire2.received, sessionId)?.boot_id, 'same gateway boot for both cursors').toBe(cursor.boot_id)
      wire1.sent.length = 0
      wire2.sent.length = 0
      // Verify the cursor each CLIENT actually sends, not just a server receipt.
      for (const tab of [page, page2]) await tab.evaluate(() => {
        for (const ws of (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances ?? []) ws.close()
      })
      for (const wire of [wire1, wire2]) {
        await expect.poll(() => wire.sent.filter((frame): frame is AttachSessionFrame => frame.type === 'attach_session' && frame.session_id === sessionId).at(-1)?.since_seq, {
          timeout: 30_000, message: 'both tabs resume from their equal committed cursor',
        }).toBe(cursor.seq)
      }
      expectReleased(provider, 'catchup e')
      await page2.close()
    } finally {
      try { await attachHeldStreamEvidence(info, 'catchup-e', provider) }
      finally { await gw.stop(); await provider.close() }
    }
  })
})

test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  test.describe.configure({ retries: 0 })

  test('f: two chats active during an outage — A incremental, B complete on switch', async ({ page, context }, info) => {
    test.setTimeout(420_000)
    const promptA = `${LONG_PROMPT} Catchup-f-chat-A.`
    const promptB = `${LONG_PROMPT} Catchup-f-chat-B.`
    const provider = await startHeldStreamProvider({
      promptIncludes: promptA,
      scripts: [{ promptIncludes: promptA, behavior: 'hold' }, { promptIncludes: promptB, behavior: 'hold' }],
    })
    const wire = observeCatchup(page)
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const a = await startHeldTurn(page, provider, 'catchup f A', promptA, promptA)
      const b = await startHeldTurn(page, provider, 'catchup f B', promptB, promptB)
      expect(a.sessionId, 'two distinct chats, not two views of the same turn').not.toBe(b.sessionId)
      await switchToSession(page, a.sessionId)
      await expect(stopButton(page)).toBeVisible()
      await expectTurnHeldMidStream(provider, 'catchup f A still open', promptA)
      await expectTurnHeldMidStream(provider, 'catchup f B still open', promptB)
      await page.evaluate(() => { (window as { navCatchupMark?: string }).navCatchupMark = 'scenario-f' })
      wire.received.length = 0
      await context.setOffline(true)
      await page.waitForTimeout(45_000)
      // Both real turns finish during the outage, while only A has a view.
      provider.release()
      await expectPersistedAnswer(gw, a.sessionId, provider, promptA)
      await expectPersistedAnswer(gw, b.sessionId, provider, promptB)
      await context.setOffline(false)
      await expect.poll(() => latestCatchup(wire.received, a.sessionId)?.mode, {
        timeout: 30_000, message: 'catchup f A resumes incrementally after the outage',
      }).toBe('incremental')
      await waitTurnDone(page)
      // Verify A before switching; its convergence is no longer left unchecked.
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
      expect(await normalizedAssistantText(assistantMessages(page).first()), 'catchup f A: complete incremental answer').toBe(provider.expectedHeldAnswer(promptA))
      await switchToSession(page, b.sessionId)
      expect(await page.evaluate(() => (window as { navCatchupMark?: string }).navCatchupMark), 'switch to B keeps the same document').toBe('scenario-f')
      await expect(stopButton(page), 'B is already complete on switch').toBeHidden()
      await assertScriptedAnswer(page, provider, b.before, b.sessionId, 'catchup f B', promptB)
      await switchToSession(page, a.sessionId)
      await assertScriptedAnswer(page, provider, a.before, a.sessionId, 'catchup f A', promptA)
      expectReleased(provider, 'catchup f', 2)
    } finally {
      try { await attachHeldStreamEvidence(info, 'catchup-f', provider) }
      finally { await gw.stop(); await provider.close() }
    }
  })
})

test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('g: idle 31+ minutes, then send — the answer is shown (attempt 1 dropped it)', async ({ page }) => {
    test.setTimeout(35 * 60_000)
    const input = chatInput(page)
    await expect(input).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page)
    await freshChat(page)
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
    await input.press('Enter', { timeout: 15_000 })
    await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
    await waitTurnDone(page)
    await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
    expect((await bubbleText(assistantMessages(page).first())).trim().length).toBeGreaterThan(0)
  })
})

test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  // #1661: these are the deterministic held-stream scenarios — b, c and h
  // (both orderings, below) now script ONLY the external provider
  // (fixtures/held-stream-provider.ts) behind their own GatewayProcess,
  // because on the CI primary model (see tests/e2e/e2e-model.json) the
  // ">80 chars while running" mid-turn window never opens: answer text
  // becomes visible only at/near completion, so the old gates failed every
  // attempt even though the gateway streams correctly. Scenario titles,
  // preconditions and assertions are unchanged; the mid-turn proof is now
  // the provider evidence (expectTurnHeldMidStream) on top of the unchanged
  // UI gates, and is model-independent. Zero retries: a broken catch-up is
  // a property no retry establishes.
  test.describe.configure({ retries: 0 })

  test('b: dead connection ~60s, server still thinks attached — new connection catches up, no duplicate', async ({ page }, info) => {
    test.setTimeout(420_000)
    const provider = await startHeldStreamProvider({ promptIncludes: LONG_PROMPT })
    const freeze = await installFreezeProxy(page)
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const before = await startLongTurn(page, LONG_PROMPT, { requireRunning: true })
      await expect(stopButton(page), 'the turn must still be running before the outage or switch').toBeVisible()
      // MID-TURN SIGNAL (#1661): the held-stream provider proves the turn is
      // genuinely still streaming (stream open, deltas flowing). The old
      // ">80 chars while running" gate could coincide with completion on a
      // burst-emitting model; this evidence cannot.
      await expectTurnHeldMidStream(provider, 'catchup b')
      // Real-browser follow-up (orchestrator): a hard reload lands on the
      // welcome/empty chat, not the previous session — the tab does not
      // reopen the last session on its own (checked: this is release
      // behaviour too, not something to change the product for). Capture
      // chat A's real, server-assigned sidebar title BEFORE the reload, the
      // same way scenario c already does, so it can be reopened afterward.
      const titleA = await activeChatSessionId(page)
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
      await switchToSession(page, titleA)
      // Chat A is reattached and its turn is STILL live — the freeze only cut
      // the browser side; the provider stream never ended. Release it now so
      // the remaining deltas and the completion must arrive over the
      // reattached connection to satisfy the assertions below. release() ends
      // the SAME stream, so anything the turn loses here is a real catch-up
      // defect, not a fixture artifact.
      provider.release()
      await waitTurnDoneAfterReload(page)
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
      const after = (await bubbleText(assistantMessages(page).first())).trim()
      await assertNoDuplicateOrGapText(page, before, after, titleA)
      // The turn ran to its scripted end and was delivered complete: exactly
      // one held stream, finished by the scripted release, nothing unexpected.
      expect(provider.evidence.streamRequests, 'catchup b: exactly the scripted turn reached the provider').toBe(1)
      expect(provider.evidence.released, 'catchup b: the finish came from the scripted release').toBe(1)
      expect(provider.evidence.errors, 'catchup b: no unexpected scripted-provider request').toEqual([])
    } finally {
      try {
        await attachHeldStreamEvidence(info, 'catchup-b', provider)
      } finally {
        await gw.stop()
        await provider.close()
      }
    }
  })

  test('c: tab switched to another chat while the turn finishes — incremental on return', async ({ page }, info) => {
    test.setTimeout(420_000)
    const provider = await startHeldStreamProvider({ promptIncludes: LONG_PROMPT })
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const before = await startLongTurn(page, LONG_PROMPT, { requireRunning: true })
      await expect(stopButton(page), 'the turn must still be running before the outage or switch').toBeVisible()
      // MID-TURN SIGNAL (#1661) — same as scenario b: the provider evidence
      // proves the turn is genuinely still streaming before the UI moves.
      await expectTurnHeldMidStream(provider, 'catchup c')
      // Capture chat A's real, server-assigned sidebar title BEFORE switching
      // away — this is what makes finding it again reliable (see
      // activeChatSessionId's own comment on why this beats guessing).
      const titleA = await activeChatSessionId(page)
      // Switch to a fresh chat B while chat A's turn is still running
      // server-side (the turn never depends on a UI connection, ADR-082 P1 —
      // switching the VIEW away changes nothing about it).
      // The sidebar "New chat" button this used to click is REMOVED (founder
      // decision D7 — no New-chat UI entry anywhere; the /new palette item
      // filters itself out while streaming anyway), so chat B comes from
      // freshChat: a public-REST create opened through the SPA's own
      // /#/sessions/<id> deep-link route. That goto is hash-only on the
      // origin the page is already on, the router is a hash router
      // (createHashHistory, src/main.tsx), and Playwright defines a
      // same-URL-different-hash goto as a same-document navigation — so the
      // view switches WITHOUT the document reload that would clear the
      // in-memory cursor and reduce the return below to the full-replay
      // path (the trap the switch-back comment names). The surviving
      // marker assertion after the return makes that precondition
      // machine-checked, not argued.
      await expect(stopButton(page), 'the view switches away while chat A is still running').toBeVisible()
      await page.evaluate(() => { (window as { navCatchupMark?: string }).navCatchupMark = 'scenario-c' })
      const chatB = await freshChat(page)
      await expect(
        page.locator(`[data-active-session-id="${chatB.sessionId}"]`),
        'the view switched to fresh chat B (bound) without a page reload',
      ).toBeVisible({ timeout: 15_000 })
      await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      // Switch back to chat A via the sidebar (not page.goto — a reload would
      // clear the in-memory cursor and defeat the point of this scenario; see
      // reconnect-mid-turn.spec.ts's own S-11 note on the same trap). Escape
      // is never pressed — it cancels the running turn.
      await switchToSession(page, titleA)
      await expect(stopButton(page), 'reattach reconstructs the still-running turn').toBeVisible()
      // The window marker set before the switch survived: no document reload
      // happened across the away-and-back round trip, so the catch-up below
      // really exercises the in-memory cursor. A reload would silently turn
      // this scenario into the post-reload replay path — the exact weaker
      // behaviour this scenario exists to distinguish — and pass vacuously.
      // awaited: expect(<Promise>).toBe(...) compares the Promise object itself
      // ("Received: Promise {}"), so this check could never pass - the scenario
      // never got far enough before to expose it.
      expect(
        await page.evaluate(() => (window as { navCatchupMark?: string }).navCatchupMark),
        'no reload across the away-and-back switch',
      ).toBe('scenario-c')
      // Only NOW does the DOM reflect chat A again, so only now does
      // waitTurnDone's stop-button check actually observe chat A's own
      // state — checking it while chat B was active (the previous draft's
      // sequencing) would have passed immediately regardless of whether A's
      // turn had genuinely finished, since chat B never shows a stop button.
      // Chat A's turn is still live (the provider stream never ended);
      // release it so the remaining deltas and the completion must arrive
      // over the reattached in-memory-cursor connection below.
      provider.release()
      await waitTurnDone(page)
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 30_000 })
      const after = (await bubbleText(assistantMessages(page).first())).trim()
      await assertNoDuplicateOrGapText(page, before, after, titleA)
      // The turn ran to its scripted end and was delivered complete: exactly
      // one held stream, finished by the scripted release, nothing unexpected.
      expect(provider.evidence.streamRequests, 'catchup c: exactly the scripted turn reached the provider').toBe(1)
      expect(provider.evidence.released, 'catchup c: the finish came from the scripted release').toBe(1)
      expect(provider.evidence.errors, 'catchup c: no unexpected scripted-provider request').toEqual([])
    } finally {
      try {
        await attachHeldStreamEvidence(info, 'catchup-c', provider)
      } finally {
        await gw.stop()
        await provider.close()
      }
    }
  })
})

// Same deterministic describe, split at the h scenarios only for the 240-line
// function budget (scripts/check-function-budget.sh): the identical title
// keeps every full test name byte-identical, and the zero-retry scope carries
// over — a broken catch-up is a property no retry establishes.
test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  test.describe.configure({ retries: 0 })

  // F1: the held-open tab must choose the same single status regardless of
  // whether the optional lifecycle list or the boot-mismatch attach arrives first.
  for (const ordering of ['catch-up first', 'lifecycle first'] as const) {
    test(`h: gateway restart with tab open — Interrupted alone, Generate again completes (${ordering})`, async ({ page }, info) => {
      test.setTimeout(420_000)
      // #1661: the held-stream provider replaces the pass-4 token-delay knob —
      // the interrupted turn stays mid-stream because the provider holds the
      // SSE stream open, not because the gateway paces a live model.
      const provider = await startHeldStreamProvider({ promptIncludes: LONG_PROMPT })
      const gw = await GatewayProcess.start({ apiBase: provider.url, model: E2E_FALLBACK.model })
      let crashed = false
      let releaseList!: () => void
      let listReachedPage = false
      const listHeld = new Promise<void>((resolve) => { releaseList = resolve })
      const waitForLifecycleList = () => expect.poll(() => listReachedPage, {
        timeout: 60_000, message: 'The SPA must refetch its lifecycle list after reconnecting; the ordering gate cannot wait forever.',
      }).toBe(true)
      let regenerating = false
      let retryHasContent = false
      let targetSessionId: string | null = null
      const retryDone: import('@/lib/api/generated/asyncapi-types').DoneFrame[] = []
      const retryErrors: import('@/lib/api/generated/asyncapi-types').ErrorFrame[] = []
      page.on('websocket', (socket) => socket.on('framereceived', ({ payload }) => {
        const frame = JSON.parse(payload.toString()) as import('@/lib/api/generated/asyncapi-types').ServerFrame
        if (!regenerating || !('session_id' in frame) || frame.session_id !== targetSessionId) return
        if (frame.type === 'token' && /\S/.test(frame.content)) retryHasContent = true
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
        await freshChat(page)
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
        // The held-stream provider keeps the answer open until the SIGKILL,
        // so the explicit running precondition below cannot race completion.
        await input.fill(LONG_PROMPT)
        await input.press('Enter', { timeout: 15_000 })
        await expect(stopButton(page)).toBeVisible({ timeout: 15_000 })
        const liveAnswer = page.locator(ANY_STATUS_ASSISTANT_ROW)
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
        // MID-TURN SIGNAL (#1661): the stream is provably still open right
        // before the SIGKILL — the Interrupted state below is then a real
        // mid-answer cut, never a post-completion fake.
        await expectTurnHeldMidStream(provider, `catchup h (${ordering})`)
        crashed = true
        await gw.kill9()
        // The old 25ms/frame pacing existed to keep a live-model retry
        // finishable within the wait budgets; the scripted retry is answered
        // complete in one deterministic response, so the restart needs no
        // env knob (#1661).
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
        await expect(notice).toHaveCount(0)
        await expect(generateAgain).toHaveCount(0)
        // Resend only shows Stop after the first token; its working acknowledgement
        // changes delivery ticks, not streaming state. Do not turn model startup
        // latency into a 30s UI deadline. Use waitTurnDone's existing turn budget,
        // but fail immediately on an error or an empty terminal response.
        await expect.poll(() => retryHasContent || retryErrors.length > 0 || retryDone.length > 0, {
          timeout: 240_000, message: 'Generate again must produce real answer content within the turn budget.',
        }).toBe(true)
        expect(retryErrors, 'Generate again must start without a provider/turn error').toEqual([])
        expect(retryHasContent, 'Generate again must not finish without real answer content').toBe(true)
        const retryAnswer = page.locator(`${ANY_STATUS_ASSISTANT_ROW}[data-status="running"]`)
        await expect(retryAnswer.locator('.prose-sm')).toContainText(/\S/)
        await expect(stopButton(page)).toBeVisible({ timeout: 30_000 })
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
        // End-of-test provider evidence: the SIGKILL tore down the held
        // stream (streamsClosed, without any release), and "Generate again"
        // reached the provider exactly once more and was answered complete —
        // the retry completed deterministically.
        expect(provider.evidence.streamsClosed, `catchup h (${ordering}): the crash closed the held stream`).toBe(1)
        expect(provider.evidence.released, `catchup h (${ordering}): no scripted release was involved`).toBe(0)
        expect(provider.evidence.retried, `catchup h (${ordering}): Generate again was answered complete`).toBe(1)
        expect(provider.evidence.streamRequests, `catchup h (${ordering}): exactly the held turn and the retry reached the provider`).toBe(2)
        expect(provider.evidence.errors, `catchup h (${ordering}): no unexpected scripted-provider request`).toEqual([])
      } finally {
        releaseList()
        // done refreshes the lifecycle list. Drain its active route.fetch
        // before stopping the server or letting Playwright close the context;
        // wait preserves handler failures, unlike ignoreErrors.
        try {
          try { await page.unrouteAll({ behavior: 'wait' }) }
          finally { await gw.stop() }
        } finally {
          try {
            await attachHeldStreamEvidence(info, `catchup-h-${ordering}`, provider)
          } finally {
            await provider.close()
          }
        }
      }
    })
  }
})

test.describe('BE-DESIGN.md §8.3 real-browser catch-up scenarios', () => {
  // #1661: scenario i joins the deterministic held-stream scenarios — it
  // scripts ONLY the external provider (fixtures/held-stream-provider.ts)
  // behind its own GatewayProcess, via the same openHeldCatchupChat harness
  // b and c use. Reason: on the CI primary model (see tests/e2e/e2e-model.json)
  // the offline-typed turn completed within roughly 430 ms of delivery, so the
  // transient "working" delivery tick was already gone (turn done) before its
  // 30 s check ran (CI llm-agents: flaky on #1668). With the held stream the
  // delivered message's turn stays open until the scripted release(), so the
  // tick, once shown, stays observable — completion cannot race the check.
  // Scenario title, preconditions and every post-event assertion are
  // unchanged; the added proof is provider evidence (expectTurnHeldMidStream
  // + the end-of-test counters), which is model-independent. Zero retries: a
  // lost or duplicated offline-typed message is a property no retry establishes.
  test.describe.configure({ retries: 0 })

  test('i: message typed while offline — appears at the end, then its answer; no duplicate; ticks received → working', async ({ page, context }, info) => {
    test.setTimeout(180_000)
    const provider = await startHeldStreamProvider({ promptIncludes: 'typed while offline' })
    const gw = await openHeldCatchupChat(page, provider)
    try {
      const input = chatInput(page)
      await expect(input).toBeVisible({ timeout: 15_000 })
      await waitForConnected(page)
      await freshChat(page)
      await context.setOffline(true)
      await page.evaluate(() => window.dispatchEvent(new Event('offline')))
      await input.fill('typed while offline')
      await input.press('Enter', { timeout: 15_000 })
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
      // MID-TURN SIGNAL (#1661): the reconnect delivers the queued message
      // and the held stream keeps that turn open until release() below — so
      // the "working" tick below cannot be racing completion, and the gate
      // right after proves the tick meant something real: exactly one
      // provider stream, deltas flowing, still open. A turn that never
      // started cannot show the tick at all; a turn that already finished
      // cannot pass the gate (its stream would be closed before the check).
      await expect(deliveryStatus.getByRole('button', { name: /is working on it/i })).toBeVisible({ timeout: 30_000 })
      await expectTurnHeldMidStream(provider, 'catchup i')
      // The tick is observed and the turn is provably live mid-stream.
      // release() ends the SAME stream, so the remaining deltas and the
      // completion must arrive over the reconnected connection to satisfy
      // the assertions below; anything lost here is a real catch-up defect,
      // not a fixture artifact.
      provider.release()
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
      // End-of-test provider evidence: the offline-typed message really
      // started the turn (exactly one request reached the provider) and the
      // answer was finished by the scripted release — never by an early
      // model answer racing the tick check.
      expect(provider.evidence.streamRequests, 'catchup i: exactly the scripted turn reached the provider').toBe(1)
      expect(provider.evidence.released, 'catchup i: the finish came from the scripted release').toBe(1)
      expect(provider.evidence.errors, 'catchup i: no unexpected scripted-provider request').toEqual([])
    } finally {
      try {
        await attachHeldStreamEvidence(info, 'catchup-i', provider)
      } finally {
        await gw.stop()
        await provider.close()
      }
    }
  })
})
