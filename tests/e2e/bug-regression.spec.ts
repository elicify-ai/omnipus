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

import { errors, expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { test as canonicalTest } from './fixtures/canonical-history'
import { seedCanonicalLegacyTranscript } from './fixtures/chat-history'
import {
  chatInput,
  assistantMessages,
  waitForConnected,
} from './fixtures/selectors'
import { freshChat } from './fixtures/session-setup'
import { assistantReplyChallenge, assertHansReplyPersisted } from './fixtures/assistant-reply-acceptance'
import { assertHansSessionUnfiled, provisionHansWorkspaceTeam } from './fixtures/hans-workspace'
import { GatewayProcess } from './fixtures/gateway-process'
import http from 'node:http'

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
    //
    // TEST BUG (fixed): this test used to navigate to `/onboarding` with no
    // server-state mock, relying solely on the describe block's storageState
    // override (`{ cookies: [], origins: [] }`) to reach the real wizard. That
    // override only clears the BROWSER's client-side state — SAME root cause
    // already diagnosed and fixed below for Bug-1-b (see its "Root cause of
    // the collision" comment). Whether onboarding is complete is SERVER-side
    // state (`GET /api/v1/state` -> `onboarding_complete`), set to `true` once
    // for the whole worker/run by global-setup.ts's `onboardViaAPI()` call
    // before any test runs. So `/onboarding`'s `beforeLoad`
    // (onboarding.tsx:1748-1768) saw `onboarding_complete: true` and redirected
    // to `/`, whose own `beforeLoad` (_app.tsx) then found no session cookie
    // (storageState cleared it) and redirected again to `/login` — a page with
    // no skip-matching element, so the test usually passed by accident.
    // Under CI's parallel worker load, though, a transient (non-401) failure
    // on the `/auth/validate` call that redirect triggers (checkTokenValidity,
    // src/routes/authValidation.ts) reclassifies as `verdict: 'transient'`,
    // which does NOT redirect to `/login` — it "proceeds into the app"
    // instead, mounting AppShell. AppShell renders a legitimate WCAG 2.4.1
    // "Skip to content" bypass-blocks link (src/components/layout/AppShell.tsx
    // ~line 152-158) which matches `getByRole('link', { name: /skip/i })` —
    // an unrelated accessibility affordance, not a reintroduced onboarding
    // skip button. That is the observed flaky false-positive.
    //
    // Fix: mock `GET /api/v1/state` (same as Bug-1-b) so `beforeLoad` never
    // redirects away from `/onboarding` at all — the redirect chain through
    // `/` / AppShell / `/login` is no longer reachable, so the assertions
    // below run deterministically against the REAL welcome/first step, which
    // never renders AppShell and never contains "Skip to content".
    //
    // `identity` is REQUIRED on AppState (ADR-0010 WP1); this test wants the
    // REAL local-mode wizard (NameStep, "What should I call you?" — the
    // engine's own admin-account first step, WP5), so the stub says
    // `mode: 'local'` explicitly rather than relying on a missing-field
    // parse failure to fall into local mode by accident.
    await page.route('**/api/v1/state', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          onboarding_complete: false,
          identity: { mode: 'local', edition: 'core', signed_in: false },
        }),
      })
    })

    await page.goto('/onboarding')
    await page.waitForLoadState('networkidle')

    // Proves we actually landed on the real onboarding wizard (its first/
    // welcome step), not some redirect target — same anchor Bug-1-b uses.
    await expect(
      page.getByRole('heading', { name: 'What should I call you?' }),
    ).toBeVisible({ timeout: 10_000 })

    const skipButton = page.getByRole('button', { name: /skip/i })
    const skipLink = page.getByRole('link', { name: /skip/i })
    const skipText = page.getByText(/skip.*i know|i know.*skip/i)

    // All of these must NOT be present — declarative assertions that fail loudly on presence.
    await expect(skipButton).toHaveCount(0, { timeout: 5_000 })
    await expect(skipLink).toHaveCount(0)
    await expect(skipText).toHaveCount(0)

    await page.unrouteAll({ behavior: 'ignoreErrors' })
  })

  test('(Bug-1-b) onboarding first step has a Continue button (the only progression path)', async ({ page }) => {
    // BDD: Given the onboarding wizard's first step (username entry)
    //      Then a "Continue" button IS present — the only forward-progress control
    // This is the positive complement to Bug-1-a.
    // Traces to: Bug-1 (only forward path is Continue — there is no skip)
    //
    // TEST BUG (fixed): the previous version gated its assertion behind
    // `page.getByText('Welcome to Omnipus')`, assuming that was the onboarding
    // welcome heading. It is not — that text belongs to ChatScreen's empty-state
    // (WelcomeState(), src/components/chat/ChatScreen.tsx:1611-1633). The real
    // onboarding wizard (src/routes/onboarding.tsx) has no "Welcome to Omnipus"
    // heading and no "Get Started" button anywhere; its first step is `NameStep`
    // ("What should I call you?" heading + a "Continue" button,
    // onboarding.tsx:795-852).
    //
    // Root cause of the collision: this describe block's storageState override
    // (line 32, `{ cookies: [], origins: [] }`) only clears the BROWSER's
    // client-side state. Whether onboarding is complete is SERVER-side state
    // (`GET /api/v1/state` -> `onboarding_complete`), set to `true` once for the
    // whole worker/run by global-setup.ts's `onboardViaAPI()` call before any
    // test runs. So `/onboarding`'s `beforeLoad` (onboarding.tsx:1748-1768) sees
    // `onboarding_complete: true` regardless of the per-test storageState
    // override and redirects to `/` — landing on the ChatScreen empty-state,
    // which happens to share the word "Welcome" with what the old assertion
    // was looking for, so it silently checked the wrong screen and then failed
    // waiting for a "Get Started" button that exists nowhere in the app.
    //
    // Fix: mock `GET /api/v1/state` to report `onboarding_complete: false`
    // so `beforeLoad` does not redirect, letting the test observe the REAL
    // first step and its REAL forward control, scoped by heading so it
    // cannot match ChatScreen's differently-worded empty-state heading
    // ("Welcome to Omnipus"). `identity` is REQUIRED on AppState (ADR-0010
    // WP1, not "the only required field" as this comment used to say); this
    // test wants the REAL local-mode wizard (NameStep), so the stub states
    // `mode: 'local'` explicitly rather than relying on a missing-field
    // parse failure to fall into local mode by accident.
    await page.route('**/api/v1/state', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          onboarding_complete: false,
          identity: { mode: 'local', edition: 'core', signed_in: false },
        }),
      })
    })

    await page.goto('/onboarding')
    await page.waitForLoadState('networkidle')

    // Proves we actually landed on the real onboarding wizard, not the
    // ChatScreen empty-state (different heading text entirely).
    await expect(
      page.getByRole('heading', { name: 'What should I call you?' }),
    ).toBeVisible({ timeout: 10_000 })

    // The only forward-progress control on this step — no "Get Started"
    // button exists anywhere in the real wizard.
    await expect(
      page.getByRole('button', { name: 'Continue' }),
    ).toBeVisible({ timeout: 5_000 })

    await page.unrouteAll({ behavior: 'ignoreErrors' })
  })
})

// ─── Deterministic mock provider (issue #1661) ────────────────────────────────
//
// Bug-3-a and Bug-5-a/5-b no longer call a paid model (chief decision on
// #1661, 2026-11-06 13:55Z): the live 90 s `expect.poll` assertions flaked on
// real-model latency (traced run 42202300235: tab 1's model call silent for
// ~82 s, the test's own teardown Stop then rewrote the artifact into
// "turn_canceled", and the no-flaky gate failed the job on the passing retry).
//
// The mock is a node:http OpenAI-compatible SSE provider (the proven
// provider-retry.spec.ts::startMockProvider pattern), wired through
// GatewayProcess.start({ apiBase }) so the REAL gateway, agent loop, WS hub,
// session store and replay path stay under test — only the paid-model edge is
// replaced. Two deliberate differences from startMockProvider:
//
//   1. The script branches on the USER MESSAGE TEXT in the request body —
//      never on a call counter. startMockProvider's counter is global across
//      the mock, and the same agent (mia) issues one streaming call per tool
//      iteration across ALL sessions, so a counter cannot tell tab A from tab
//      B (hunt report, "Q4 fixes" #3).
//   2. A scripted entry can HOLD a request open — SSE headers are written,
//      the body is never written — so a turn can be made deterministically
//      "still in flight". The mock records every streaming request with its
//      marker and open/answered state, which gives the tests a deterministic
//      signal ("B's request is still open at the mock") instead of a
//      wall-clock budget.

/** The model every mock-provider gateway onboards with (any string works — the mock speaks the protocol, not a catalog). */
const MOCK_MODEL = 'mock-model-a'

// Per-test markers. They ride INSIDE the user message text, so the mock can
// tell the two tabs'/turns' requests apart by content alone.
const BUG3A_MARKER_TAB_A = 'bug3a-mock-tab-a'
const BUG3A_MARKER_TAB_B = 'bug3a-mock-tab-b'
const BUG5A_MARKER = 'bug5a-mock-turn-one'
const BUG5B_MARKER_ONE = 'bug5b-mock-turn-one'
const BUG5B_MARKER_TWO = 'bug5b-mock-turn-two'

// Scripted answers — the exact expected reply text each oracle asserts. Plain
// ASCII so markdown rendering cannot transform them.
const BUG3A_ANSWER_TAB_A = 'MOCK-REPLY-BUG3A-TAB-A: tab A settled while tab B was still in flight.'
const BUG5A_ANSWER = 'MOCK-REPLY-BUG5A: single-turn scripted answer for the replay-order test.'
const BUG5B_ANSWER_ONE = 'MOCK-REPLY-BUG5B-ONE: first turn scripted answer.'
const BUG5B_ANSWER_TWO = 'MOCK-REPLY-BUG5B-TWO: second turn scripted answer.'
const MOCK_FALLBACK_ANSWER = 'MOCK-REPLY: scripted answer (no marker matched).'

/** Exact suffix the product renders for a cancelled/interrupted turn (src/lib/truncation.ts::INTERRUPTED_SUFFIX_TEXT). */
const INTERRUPTED_SUFFIX_TEXT = '(interrupted)'

type MockScriptEntry = { kind: 'answer'; text: string } | { kind: 'hold' }

/** Marker (script key) → what the mock does with a streaming request carrying it. */
type MockScript = Record<string, MockScriptEntry>

/**
 * One streaming /chat/completions request the mock received, with the state
 * the tests poll on. `open` is true from request arrival until the mock
 * finished the response OR the client (the gateway's provider transport)
 * closed the connection — which is exactly the deterministic "still in
 * flight" / "was cancelled" signal.
 */
interface MockRequestRecord {
  seq: number
  marker: string | null
  /** True while the request is in flight at the mock (not answered, not closed by the client). */
  open: boolean
  /** True when the script HELD this request (headers sent, body never written). */
  heldOpen: boolean
  /** True when the mock completed the scripted answer. */
  answered: boolean
  /** True when the client closed the connection before the mock answered — the wire-level cancel signal. */
  clientClosed: boolean
}

interface ScriptedMockProvider {
  url: string
  /** Snapshot of every streaming request seen so far (records are copied — mutations do not leak). */
  requests: () => MockRequestRecord[]
  /** End every held request server-side (teardown safety; the tests never rely on it). */
  releaseAllHolds: () => void
  close: () => Promise<void>
}

function sseChunk(delta: Record<string, unknown>, finish: string | null): string {
  return (
    'data: ' +
    JSON.stringify({
      id: 'chatcmpl-bug-mock',
      object: 'chat.completion.chunk',
      created: Math.floor(Date.now() / 1000),
      model: MOCK_MODEL,
      choices: [{ index: 0, delta, finish_reason: finish }],
    }) +
    '\n\n'
  )
}

/** The text of a chat message's content — a plain string or an OpenAI-style content-parts array. */
function userTextOf(content: unknown): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((part) =>
        part && typeof part === 'object' && typeof (part as { text?: unknown }).text === 'string'
          ? (part as { text: string }).text
          : '',
      )
      .join('\n')
  }
  return ''
}

/**
 * Which script marker (if any) the request's USER messages carry. Scanned
 * from the last user message backwards so later turns win; tool results are
 * role "tool" and never match. This — not a call counter — is what tells the
 * two tabs' and two turns' requests apart.
 */
function markerFromUserMessages(messages: unknown, markers: string[]): string | null {
  if (!Array.isArray(messages)) return null
  for (let i = messages.length - 1; i >= 0; i--) {
    const entry = messages[i] as { role?: unknown; content?: unknown } | null
    if (!entry || entry.role !== 'user') continue
    const text = userTextOf(entry.content)
    const hit = markers.find((marker) => text.includes(marker))
    if (hit) return hit
  }
  return null
}

function startScriptedMockProvider(script: MockScript): Promise<ScriptedMockProvider> {
  return new Promise((resolve, reject) => {
    const markers = Object.keys(script)
    const records: MockRequestRecord[] = []
    const heldResponses = new Map<number, http.ServerResponse>()
    const heldRecords = new Set<MockRequestRecord>()
    let seq = 0

    const server = http.createServer((req, res) => {
      const chunks: Buffer[] = []
      req.on('data', (c: Buffer) => chunks.push(c))
      req.on('end', () => {
        const bodyRaw = Buffer.concat(chunks).toString('utf8')

        if (req.method === 'GET' && req.url?.startsWith('/models')) {
          res.writeHead(200, { 'Content-Type': 'application/json' })
          res.end(JSON.stringify({ object: 'list', data: [{ id: MOCK_MODEL, object: 'model' }] }))
          return
        }

        if (req.method === 'POST' && req.url?.includes('/chat/completions')) {
          let parsed: { stream?: unknown; messages?: unknown } = {}
          try {
            parsed = JSON.parse(bodyRaw) as typeof parsed
          } catch {
            parsed = {}
          }

          if (!parsed.stream) {
            // Onboarding / provider-validation probe (never streaming): answer
            // honestly so the install comes up healthy — never part of the script.
            res.writeHead(200, { 'Content-Type': 'application/json' })
            res.end(
              JSON.stringify({
                id: 'chatcmpl-bug-mock-probe',
                object: 'chat.completion',
                created: Math.floor(Date.now() / 1000),
                model: MOCK_MODEL,
                choices: [
                  {
                    index: 0,
                    message: { role: 'assistant', content: 'probe ok' },
                    finish_reason: 'stop',
                  },
                ],
              }),
            )
            return
          }

          // Scripted streaming call — branch on the user message TEXT.
          const marker = markerFromUserMessages(parsed.messages, markers)
          const entry = marker !== null ? script[marker] : undefined
          const record: MockRequestRecord = {
            seq: (seq += 1),
            marker,
            open: true,
            heldOpen: false,
            answered: false,
            clientClosed: false,
          }
          records.push(record)

          res.on('close', () => {
            // Fires on normal completion too; only an unanswered, still-held
            // close is a client-driven cancellation (the gateway canceling the
            // provider call when the turn is stopped).
            if (!record.answered && heldResponses.has(record.seq)) {
              record.clientClosed = true
              record.open = false
              heldResponses.delete(record.seq)
              heldRecords.delete(record)
            }
          })

          if (!entry || entry.kind === 'answer') {
            const text = entry && entry.kind === 'answer' ? entry.text : MOCK_FALLBACK_ANSWER
            res.writeHead(200, { 'Content-Type': 'text/event-stream' })
            res.end(
              sseChunk({ role: 'assistant', content: '' }, null) +
                sseChunk({ content: text }, null) +
                sseChunk({}, 'stop') +
                'data: [DONE]\n\n',
            )
            record.answered = true
            record.open = false
            return
          }

          // HOLD: SSE headers only, the body is never written — the request
          // stays open at the mock until the gateway cancels it (turn Stop)
          // or the test/teardown releases it. Deliberately no res.end() here.
          record.heldOpen = true
          heldRecords.add(record)
          heldResponses.set(record.seq, res)
          res.writeHead(200, {
            'Content-Type': 'text/event-stream',
            'Cache-Control': 'no-cache',
            Connection: 'keep-alive',
          })
          return
        }

        res.writeHead(404, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ error: { message: 'scripted mock provider: no such route' } }))
      })
    })

    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address()
      if (!addr || typeof addr === 'string') {
        reject(new Error('scripted mock provider: no ephemeral port'))
        return
      }
      resolve({
        url: `http://127.0.0.1:${addr.port}`,
        requests: () => records.map((r) => ({ ...r })),
        releaseAllHolds: () => {
          for (const record of heldRecords) record.open = false
          for (const res of heldResponses.values()) res.destroy()
          heldResponses.clear()
          heldRecords.clear()
        },
        close: () =>
          new Promise<void>((resolveClose) => {
            for (const res of heldResponses.values()) res.destroy()
            heldResponses.clear()
            heldRecords.clear()
            // close() alone would wait for held sockets to finish on their
            // own — they never would. Destroy them, then close the listener.
            server.closeAllConnections()
            server.close(() => resolveClose())
          }),
      })
    })
  })
}

/**
 * Authenticate the browser against an isolated GatewayProcess gateway by
 * injecting the cookies of the fixture's own REST login into the browser
 * context — the repo's proven isolated-gateway browser-auth path
 * (catchup-scenarios.spec.ts::addCookies(gw.browserStorageState()), also
 * agent-grep.spec.ts / mail-panel.spec.ts / cancel-cross-channel.spec.ts via
 * newContext({ storageState })). A UI form login was tried first and is NOT
 * viable here: against a fresh isolated gateway it rendered the shell but
 * every subsequent authenticated fetch failed (workspace-load error state,
 * receipts in logs/bug3a/ta/bug3a-run.log), while the same flow over curl and
 * via cookie injection passes. The REST-issued omnipus-session cookie is what
 * the SPA's fetches authenticate with (FR-009 cutover; pkg/gateway/auth.go
 * ResolveUserFromCookie fallback), so the injected cookie pair is the
 * product's own real session, not a test stub.
 */
async function loginToGateway(page: Page, gw: GatewayProcess): Promise<void> {
  await page.context().addCookies((await gw.browserStorageState()).cookies)
  await page.goto(gw.baseURL)
  await expect(page).not.toHaveURL(/\/#\/login/, { timeout: 15_000 })
}

/**
 * Wait for the workspace chrome to be laid out before driving it. On a
 * JUST-ONBOARDED isolated gateway the first workspace render trails the login
 * redirect by tens of seconds (first-boot entity bootstrap and catalog
 * refresh happen between "SPA served" and "banner rendered"), so freshChat's
 * built-in 15 s actionability window can expire before the hamburger exists —
 * the click then races the render instead of testing anything. Waiting for the
 * actual chrome is a readiness signal, not a widened timeout.
 */
async function awaitWorkspaceChrome(page: Page): Promise<void> {
  await page
    .getByRole('button', { name: 'Show sidebar', exact: true })
    .waitFor({ state: 'visible', timeout: 45_000 })
}

/**
 * A SETTLED assistant reply and nothing else: the assistantMessages shape
 * (non-user, non-system) further narrowed to data-status="complete". The
 * product renders data-status="incomplete" for BOTH error and interrupted
 * store statuses (src/lib/omnipus-runtime.ts::buildMessageStatus) and
 * "running" for the streaming placeholder (ChatScreen.tsx assistant root:
 * data-status={message.status?.type ?? 'complete'}) — none of those is ever
 * a reply here. This is the replacement for the removed Bug-3 rule that
 * admitted an interrupted/incomplete message as a "serviced reply" (issue
 * #1661: an interrupted turn must NEVER count as a reply).
 */
const SETTLED_REPLY_SELECTOR =
  '[data-message-id]:not(.flex-row-reverse):not([data-message-role="system"])[data-status="complete"]'
const settledReplies = (page: Page) => page.locator(SETTLED_REPLY_SELECTOR)
const settledReplyWithText = (page: Page, text: string) =>
  page.locator(SETTLED_REPLY_SELECTOR).filter({ hasText: text })

/** The assistant row the product renders for a cancelled/interrupted turn. */
const interruptedReplyRow = (page: Page) =>
  page
    .locator(
      '[data-message-id]:not(.flex-row-reverse):not([data-message-role="system"])[data-status="incomplete"]',
    )
    .filter({ hasText: INTERRUPTED_SUFFIX_TEXT })

// ─── Bug-3: Concurrent sessions both respond ──────────────────────────────────

test.describe('Bug-3: Concurrent sessions both respond', () => {
  test(
    '(Bug-3-a) two chats opened in parallel both receive replies',
    async ({ page, context }) => {
      // Deterministic mock-provider replacement (issue #1661, chief decision
      // 2026-11-06 13:55Z). The previous version drove two REAL model turns and
      // polled 90 s for both replies; every checked CI failure was that fixed
      // wall-clock budget losing to real-model latency (traced run 42202300235:
      // tab 1's model call silent ~82 s, then the test's own teardown Stop
      // rewrote the artifact into "turn_canceled" and the no-flaky gate fired
      // on the passing retry). The contract under test — "both sessions are
      // serviced concurrently" — is now asserted on deterministic signals:
      //   1. the mock's own record: tab A's streaming request ANSWERED while
      //      tab B's is HELD OPEN (the concurrency proof — A settled while B
      //      was still in flight, with no session starvation in between);
      //   2. tab A's settled reply visible in that exact window;
      //   3. Stop on tab B cancels B (its provider request CLOSES at the mock;
      //      the UI shows the "(interrupted)" state) without touching A;
      //   4. an interrupted/incomplete message NEVER counts as a reply
      //      (settledReplies only admits data-status="complete").
      // No paid model, no wall-clock budget: every wait polls the mock's
      // request record or a settled DOM status.
      test.setTimeout(240_000) // bounds gateway boot + onboarding + UI setup, not model latency
      // BDD: Given two browser tabs open to two distinct chat sessions of the
      //      same agent, with a mock provider that answers tab A's message and
      //      holds tab B's open
      //      When a message is sent in each tab
      //      Then tab A's settled reply is visible while tab B's request is
      //           still open at the mock, and stopping tab B ends it as
      //           cancelled — never as a reply — without affecting tab A
      //
      // Traces to: Bug-3 (concurrent sessions both respond); issue #1661.

      const mock = await startScriptedMockProvider({
        [BUG3A_MARKER_TAB_A]: { kind: 'answer', text: BUG3A_ANSWER_TAB_A },
        [BUG3A_MARKER_TAB_B]: { kind: 'hold' },
      })
      let gw: GatewayProcess | null = null
      try {
        gw = await GatewayProcess.start({
          apiBase: mock.url,
          model: MOCK_MODEL,
          adminPassword: 'admin1234',
        })

        // Tab 1 logs in through the UI; tab 2 shares the context's cookies, so
        // no second login (and the session-token slot stays single).
        await loginToGateway(page, gw)
        await awaitWorkspaceChrome(page)
        const page2 = await context.newPage()
        await page2.goto(gw.baseURL)
        await awaitWorkspaceChrome(page2)

        // Fresh chats on both tabs — two genuinely distinct, empty sessions of
        // the same agent (freshChat, fixtures/session-setup.ts: public REST +
        // the SPA's own deep-link route; the agent-row "New chat" button is
        // gone by founder decision D7).
        const tabA = await freshChat(page)
        const tabB = await freshChat(page2)
        expect(
          tabA.sessionId,
          'the two tabs must chat in two DISTINCT sessions for the concurrency property to mean anything',
        ).not.toBe(tabB.sessionId)
        await expect(settledReplies(page)).toHaveCount(0, { timeout: 10_000 })
        await expect(settledReplies(page2)).toHaveCount(0, { timeout: 10_000 })

        // Send on both tabs. The marker inside each message is what the MOCK
        // branches on (answer A, hold B) — no call counter, which could never
        // tell the two tabs' streaming calls apart on a shared agent.
        const input1 = chatInput(page)
        const input2 = chatInput(page2)
        await input1.fill(`${BUG3A_MARKER_TAB_A} — Bug-3 mock concurrent test, tab A`)
        await input2.fill(`${BUG3A_MARKER_TAB_B} — Bug-3 mock concurrent test, tab B`)
        await input1.press('Enter')
        await input2.press('Enter')

        // 1. Deterministic concurrency proof at the mock: A's request answered,
        //    B's request STILL OPEN. (The mock answered A with a final
        //    "stop"-finished answer carrying no tool calls, so A's turn ends on
        //    its first model call; B's body is never written, so B's turn is
        //    still in flight.)
        await expect
          .poll(
            () => {
              const reqs = mock.requests()
              return {
                tabAAnswered: reqs.some(
                  (r) => r.marker === BUG3A_MARKER_TAB_A && r.answered && !r.open,
                ),
                tabBStillOpen: reqs.some(
                  (r) => r.marker === BUG3A_MARKER_TAB_B && r.heldOpen && r.open && !r.answered,
                ),
              }
            },
            { timeout: 30_000, intervals: [250, 500, 1_000] },
          )
          .toEqual({ tabAAnswered: true, tabBStillOpen: true })

        // 2. Tab A's settled reply is visible NOW — while tab B's request is
        //    still open at the mock — and it is A's ONLY settled reply, with
        //    the exact scripted text.
        await expect(settledReplyWithText(page, BUG3A_ANSWER_TAB_A)).toBeVisible({
          timeout: 15_000,
        })
        await expect(settledReplies(page)).toHaveCount(1)

        // 3. Tab B has NO reply yet: no settled row (a running placeholder
        //    wouldn't pass this locator, and neither would an interrupted one),
        //    and its turn is still streaming (the Stop button is up).
        await expect(settledReplies(page2)).toHaveCount(0)
        await expect(page2.getByTestId('stop-btn')).toBeVisible({ timeout: 15_000 })

        // 4. Stop on tab B — the user action under test.
        await page2.getByTestId('stop-btn').click({ timeout: 5_000 })

        // 5. Deterministic cancel proof at the wire: the gateway canceled B's
        //    provider call, so B's held request is no longer open at the mock
        //    (the client closed it — the same cancel that the gateway logs as
        //    turn_canceled).
        await expect
          .poll(
            () => mock.requests().some((r) => r.marker === BUG3A_MARKER_TAB_B && r.open),
            { timeout: 15_000, intervals: [250, 500, 1_000] },
          )
          .toBe(false)

        // 6. Tab B ends CANCELLED: an interrupted-status row carrying the
        //    product's "(interrupted)" suffix — and still no settled reply.
        //    An interrupted/incomplete message is data-status="incomplete"
        //    (buildMessageStatus maps the store's interrupted status there),
        //    which the settledReplies oracle never admits.
        await expect(interruptedReplyRow(page2)).toBeVisible({ timeout: 30_000 })
        await expect(settledReplies(page2)).toHaveCount(0)

        // 7. Tab A unaffected by B's cancel: its settled reply is still there,
        //    still its only one, and the mock answered A exactly once (no
        //    re-send, no second streaming call for A's turn).
        await expect(settledReplyWithText(page, BUG3A_ANSWER_TAB_A)).toBeVisible({
          timeout: 15_000,
        })
        await expect(settledReplies(page)).toHaveCount(1)
        expect(
          mock
            .requests()
            .filter((r) => r.marker === BUG3A_MARKER_TAB_A && r.answered).length,
          'tab A must have been answered exactly once at the mock',
        ).toBe(1)
      } finally {
        if (gw) await gw.stop()
        await mock.close()
      }
    },
  )
})

/** Deny unexpected inline tool requests; annotate only after that ID resolves. */
async function dismissAndAnnotate(page: Page): Promise<void> {
  const pending = page.getByTestId('tool-approval-panel')
  if (!(await pending.isVisible())) return
  const identity = await pending.getAttribute('aria-labelledby')
  if (!identity) throw new Error('approval drain: inline request has no identity')
  // A queue replacement must not receive a decision made for the prior card.
  const request = page.locator(
    `[data-testid="tool-approval-panel"][aria-labelledby=${JSON.stringify(identity)}]`,
  )
  await request.getByRole('button', { name: 'Deny', exact: true }).click({ timeout: 5_000 })
  await expect(request).toHaveCount(0, { timeout: 5_000 })
  test.info().annotations.push({
    type: 'approval-drained',
    description: `Deny selected for inline approval ${identity}; the request is no longer pending`,
  })
}

/** One poll tick: deny the inline request, then count; action errors propagate. */
async function drainApprovalDialogsAndCount(page: Page): Promise<number> {
  await dismissAndAnnotate(page)
  return assistantMessages(page).count()
}

/**
 * Keep the existing exact reply-count predicate while denying unexpected tools.
 * CI39656158622 reached a network-preflight card on turn 2 (and turn 1 on
 * retry), then waited until teardown: the former helper queried only the old
 * modal. WU-8/#1263 cards are inline, keep the composer unlocked, and do not
 * resolve on Escape. Click Deny on the inspected card instead. The existing
 * count, replay-content/order checks and budgets remain unchanged; every
 * completed denial is annotated and failed decisions fail the test loudly.
 */
async function expectAssistantMessagesWithApprovalDrain(
  page: Page,
  count: number,
  timeoutMs: number,
): Promise<void> {
  await expect
    .poll(async () => drainApprovalDialogsAndCount(page), {
      timeout: timeoutMs,
      intervals: [500, 1_000, 2_000, 4_000],
    })
    .toBe(count)
}

// ─── Bug-5: Replay frame ordering preserved on reconnect ─────────────────────

test.describe('Bug-5: Replay frame ordering preserved after navigation', () => {
  test(
    '(Bug-5-a) navigating away and back to a session preserves message order',
    async ({ page }) => {
      // Deterministic mock-provider replacement (issue #1661): the reply text
      // is scripted by the mock, so the replay assertion no longer rides a
      // live-model 90 s predicate (the same fixed-budget-vs-latency flake
      // class as Bug-3-a — hunt report, "Bug-5 — the report's 'passed in all
      // checked logs' is false").
      test.setTimeout(180_000)
      // BDD: Given a chat session with at least 1 assistant message
      //      When the user navigates to another page and back
      //      Then the messages appear in the same order as before
      //
      // Traces to: Bug-5 (replay frame ordering preserved when leaving +
      // returning to chat); issue #1661.

      const mock = await startScriptedMockProvider({
        [BUG5A_MARKER]: { kind: 'answer', text: BUG5A_ANSWER },
      })
      let gw: GatewayProcess | null = null
      try {
        gw = await GatewayProcess.start({
          apiBase: mock.url,
          model: MOCK_MODEL,
          adminPassword: 'admin1234',
        })
        await loginToGateway(page, gw)
        await awaitWorkspaceChrome(page)

        // Fresh session (freshChat — public REST + deep-link; the
        // agent-row "New chat" button is removed by D7).
        await freshChat(page)
        await expect(settledReplies(page)).toHaveCount(0, { timeout: 10_000 })

        // Send a message; the mock answers it with the exact scripted text.
        const input = chatInput(page)
        await input.fill(`${BUG5A_MARKER} — Bug-5 mock replay order test, turn one`)
        await input.press('Enter')

        // The settled reply (complete status, exact scripted text) must be the
        // session's only assistant reply before navigating away.
        await expect(settledReplyWithText(page, BUG5A_ANSWER)).toBeVisible({ timeout: 30_000 })
        await expect(settledReplies(page)).toHaveCount(1)

        // Capture the URL of the current session to return to it.
        const sessionURL = page.url()

        // Navigate away. (WU-11 FR-108: away-surface is a surviving library
        // route — the retired /#/agents no longer navigates.)
        await page.goto('/#/skills')
        await page.waitForLoadState('networkidle')

        // Navigate back to the session.
        await page.goto(sessionURL)
        await page.waitForLoadState('networkidle')

        // Wait for messages to restore via replay.
        await expect(settledReplies(page)).toHaveCount(1, { timeout: 45_000 })

        // The replayed message must carry the SAME scripted content — asserted
        // against the mock's script constant (the spec-derived expected value),
        // not against whatever the DOM happened to show before navigation.
        await expect(settledReplyWithText(page, BUG5A_ANSWER)).toBeVisible({ timeout: 15_000 })
      } finally {
        if (gw) await gw.stop()
        await mock.close()
      }
    },
  )

  test(
    '(Bug-5-b) two-turn session: turns appear in chronological order after replay',
    async ({ page }) => {
      // Deterministic mock-provider replacement (issue #1661): both replies are
      // scripted by the mock, so the chronological-order assertion runs without
      // a live model and asserts EXACT scripted content in EXACT positions.
      test.setTimeout(180_000)
      // BDD: Given a session with 2 turns (user→assistant, user→assistant)
      //      When the user navigates away and back
      //      Then turn 1 appears before turn 2 (causal order preserved)
      //
      // Traces to: Bug-5 multi-turn replay ordering; issue #1661.

      const mock = await startScriptedMockProvider({
        [BUG5B_MARKER_ONE]: { kind: 'answer', text: BUG5B_ANSWER_ONE },
        [BUG5B_MARKER_TWO]: { kind: 'answer', text: BUG5B_ANSWER_TWO },
      })
      let gw: GatewayProcess | null = null
      try {
        gw = await GatewayProcess.start({
          apiBase: mock.url,
          model: MOCK_MODEL,
          adminPassword: 'admin1234',
        })
        await loginToGateway(page, gw)
        await awaitWorkspaceChrome(page)

        // Fresh session (freshChat — public REST + deep-link; the agent-row
        // "New chat" button is removed by D7).
        await freshChat(page)
        await expect(settledReplies(page)).toHaveCount(0, { timeout: 10_000 })

        const input = chatInput(page)

        // Turn 1 — the mock answers with the scripted ONE text.
        await input.fill(`${BUG5B_MARKER_ONE} — Bug-5 mock turn 1, first message`)
        await input.press('Enter')
        await expect(settledReplyWithText(page, BUG5B_ANSWER_ONE)).toBeVisible({ timeout: 30_000 })

        // Turn 2 — the mock answers with the scripted TWO text.
        await input.fill(`${BUG5B_MARKER_TWO} — Bug-5 mock turn 2, second message`)
        await input.press('Enter')
        await expect(settledReplyWithText(page, BUG5B_ANSWER_TWO)).toBeVisible({ timeout: 30_000 })

        // Exactly two settled replies, in causal order: turn 1's reply first,
        // turn 2's second — each with its exact scripted content.
        await expect(settledReplies(page)).toHaveCount(2)
        await expect(settledReplies(page).nth(0)).toContainText(BUG5B_ANSWER_ONE)
        await expect(settledReplies(page).nth(1)).toContainText(BUG5B_ANSWER_TWO)

        // Navigate away and back. (WU-11 FR-108: surviving away-surface.)
        const sessionURL = page.url()
        await page.goto('/#/skills')
        await page.waitForLoadState('networkidle')
        await page.goto(sessionURL)
        await page.waitForLoadState('networkidle')

        // Wait for 2 messages to restore, then require the SAME chronological
        // order with the SAME exact scripted content (order preserved = turn
        // 1's reply is still first, turn 2's still second).
        await expect(settledReplies(page)).toHaveCount(2, { timeout: 45_000 })
        await expect(settledReplies(page).nth(0)).toContainText(BUG5B_ANSWER_ONE)
        await expect(settledReplies(page).nth(1)).toContainText(BUG5B_ANSWER_TWO)
      } finally {
        if (gw) await gw.stop()
        await mock.close()
      }
    },
  )
})

// Deterministic controls for Bug-5's test-operator helper. Synthetic card DOM,
// real locators; these supplement, never replace, the live replay assertions.
async function showBug5Approval(page: Page, keepPending = false): Promise<void> {
  await page.setContent('<body data-approval-decisions="[]"></body>')
  await page.evaluate(({ keepPending }) => {
    const card = document.createElement('section')
    card.dataset.testid = 'tool-approval-panel'
    card.setAttribute('aria-labelledby', 'bug5-approval-a')
    const title = document.createElement('h3')
    title.id = 'bug5-approval-a'
    title.textContent = 'Tool Approval Required'
    const command = document.createElement('pre')
    command.textContent = 'git status --short'
    card.append(title, command)
    for (const [label, action] of [['Approve Once', 'allow_once'], ['Deny', 'deny']]) {
      const button = document.createElement('button')
      button.textContent = label
      button.onclick = () => {
        const decisions = JSON.parse(document.body.dataset.approvalDecisions ?? '[]') as string[]
        decisions.push(action)
        document.body.dataset.approvalDecisions = JSON.stringify(decisions)
        if (!keepPending) card.remove()
      }
      card.append(button)
    }
    document.body.append(card)
  }, { keepPending })
}

test.describe('Bug-5 inline approval helper controls', () => {
  test('denies the inline request once and annotates its resolved identity', async ({ page }) => {
    await showBug5Approval(page)
    const before = test.info().annotations.length
    await dismissAndAnnotate(page)
    expect(await page.locator('body').getAttribute('data-approval-decisions')).toBe('["deny"]')
    const added = test.info().annotations.slice(before)
    expect(added).toHaveLength(1)
    expect(added[0]).toEqual({
      type: 'approval-drained',
      description: 'Deny selected for inline approval bug5-approval-a; the request is no longer pending',
    })
  })

  test('does nothing without an inline request', async ({ page }) => {
    await page.setContent('<body data-approval-decisions="[]"></body>')
    const before = test.info().annotations.length
    await dismissAndAnnotate(page)
    expect(await page.locator('body').getAttribute('data-approval-decisions')).toBe('[]')
    expect(test.info().annotations).toHaveLength(before)
  })

  test('fails rather than recording a denial when the request stays pending', async ({ page }) => {
    await showBug5Approval(page, true)
    const before = test.info().annotations.length
    await expect(dismissAndAnnotate(page)).rejects.toThrow(/toHaveCount/)
    expect(await page.locator('body').getAttribute('data-approval-decisions')).toBe('["deny"]')
    expect(test.info().annotations).toHaveLength(before)
  })

  test('never transfers a stale request decision to its queued replacement', async ({ page }) => {
    await showBug5Approval(page)
    const before = test.info().annotations.length
    await page.addLocatorHandler(page.locator('[aria-labelledby="bug5-approval-a"]'), async () => {
      await page.locator('[aria-labelledby="bug5-approval-a"]').evaluate(original => {
        const replacement = original.cloneNode(true) as HTMLElement
        replacement.setAttribute('aria-labelledby', 'bug5-approval-b')
        const title = replacement.querySelector('h3')
        if (!title) throw new Error('replacement control has no title')
        title.id = 'bug5-approval-b'
        for (const button of replacement.querySelectorAll('button')) {
          button.onclick = () => {
            document.body.dataset.approvalDecisions = JSON.stringify([button.textContent])
            replacement.remove()
          }
        }
        original.replaceWith(replacement)
        document.body.dataset.controlReplacement = '1'
      })
    }, { times: 1 })
    const decision = dismissAndAnnotate(page)
    await expect(decision).rejects.toThrow(errors.TimeoutError)
    await expect(decision).rejects.toThrow(/locator\.click: Timeout 5000ms exceeded/)
    expect(await page.locator('body').getAttribute('data-control-replacement')).toBe('1')
    expect(await page.locator('body').getAttribute('data-approval-decisions')).toBe('[]')
    await expect(page.locator('[aria-labelledby="bug5-approval-b"]')).toHaveCount(1)
    expect(test.info().annotations).toHaveLength(before)
  })

  test('propagates a stale-request error through the exact-count polling wrapper', async ({ page }) => {
    await showBug5Approval(page)
    const before = test.info().annotations.length
    await page.addLocatorHandler(page.locator('[aria-labelledby="bug5-approval-a"]'), async () => {
      await page.locator('[aria-labelledby="bug5-approval-a"]').evaluate(original => {
        const replacement = original.cloneNode(true) as HTMLElement
        replacement.setAttribute('aria-labelledby', 'bug5-approval-b')
        const title = replacement.querySelector('h3')
        if (!title) throw new Error('replacement control has no title')
        title.id = 'bug5-approval-b'
        for (const button of replacement.querySelectorAll('button')) {
          button.onclick = () => {
            document.body.dataset.approvalDecisions = JSON.stringify([button.textContent])
            replacement.remove()
          }
        }
        original.replaceWith(replacement)
        document.body.dataset.controlReplacement = '1'
      })
    }, { times: 1 })
    // No assistant rows exist: count zero would pass if a stale-click timeout
    // were swallowed. Require the action error, not a later count mismatch.
    const decision = expectAssistantMessagesWithApprovalDrain(page, 0, 90_000)
    await expect(decision).rejects.toThrow(errors.TimeoutError)
    await expect(decision).rejects.toThrow(/locator\.click: Timeout 5000ms exceeded/)
    expect(await page.locator('body').getAttribute('data-control-replacement')).toBe('1')
    expect(await page.locator('body').getAttribute('data-approval-decisions')).toBe('[]')
    await expect(page.locator('[aria-labelledby="bug5-approval-b"]')).toHaveCount(1)
    expect(test.info().annotations).toHaveLength(before)
  })
})

// ─── Bug-Hans: Per-agent session resume returns "session not found" ───────────

// Reproduce the bug a user reported (2026-05-30): Ava created a custom agent
// "Hans" via the agent-builder flow; the task scheduler ran two tasks against
// Hans which wrote sessions into ~/.omnipus/agents/<hans-id>/sessions/. The
// user opened one of those sessions from history and sent a follow-up message.
// The gateway responded with an `error` frame `{message:"session not found"}`
// even though the transcript had already rendered in the UI.
//
// Root cause: WSHandler.handleChatMessage unconditionally picked the shared
// session store via GetSessionStore(), which only knows about chats minted
// through the modern shared-sessions layout. Sessions created in per-agent
// stores (task scheduler, per-agent tools, custom agents) were invisible to
// the WS message path even though REST GET /api/v1/sessions/{id} could load
// them through ResolveSessionStore.
//
// Fix: handleChatMessage now calls ResolveSessionStore(sessionID) for
// existing sessions and only falls back to GetSessionStore when minting a
// new session. See pkg/gateway/websocket.go (handleChatMessage).
//
// NOTE (2026-10-12): the per-agent stores were deleted (DEL-10; one shared
// session store, pkg/agent/shared_session_store.go), so the store-split root
// cause above can no longer occur. The test keeps its user-facing outcome and
// seeds the session the way the shared store holds it (see step 3).
//
// This E2E asserts the user-facing outcome: a per-agent session can be
// resumed and a follow-up message lands without the "session not found"
// error toast, with the message appearing in the chat.
import * as fs from 'fs'
import * as path from 'path'
import { randomUUID } from 'crypto'
let BUG_BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
let BUG_OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '')

// Preflight (2026-09-16): an unresolvable OMNIPUS_HOME used to soft-skip the
// Bug-Hans test below (green with nothing executed — forbidden by the skip
// policy); it now fails fast instead.
function requireBugHarnessHome(): void {
  if (!BUG_OMNIPUS_HOME) {
    throw new Error(
      '[E2E preflight] OMNIPUS_HOME is not set (and $HOME is unavailable, so the\n' +
      '~/.omnipus fallback cannot resolve either).\n' +
      'The Bug-Hans test writes its seeded per-agent session under\n' +
      '$OMNIPUS_HOME/agents/… and cannot run without it. Its absence previously\n' +
      'soft-skipped the test — a green run with nothing executed, which the skip\n' +
      'policy forbids (tests/e2e/README.md).\n\n' +
      'To fix:\n' +
      '  export OMNIPUS_HOME=/tmp/omnipus-e2e  (CI sets this in .github/workflows/pr.yml).',
    )
  }
}

// NOTE (fixed 2026-08-24, was `.fixme` since 2026-05-30): the test sets
// up a per-agent session by writing meta.json + transcript.jsonl into
//   OMNIPUS_HOME/agents/<id>/sessions/<sid>/
// then drives a real browser to it and sends a follow-up message.
//
// The original note guessed the resulting transcript stayed empty
// because an empty SEEDED transcript reads to SPA hydration as "fresh
// chat". Live reproduction (instrumenting the real WS traffic) disproved
// that: the actual cause was a RACE between the composer becoming
// enabled/"connected" (which only proves the WS socket is open) and the
// SessionRoute mount effect's `attach_session` call actually completing
// (which is what points the chat store's `activeSessionId` at this
// seeded session). The test used to send its follow-up as soon as the
// composer looked usable — often before that effect had run — so the
// outbound `message` frame carried no `session_id`, and the gateway
// minted a brand-new session in the shared store (attributed to the
// default agent) instead of resuming the seeded one. Fixed by waiting
// for the WS `catch_up_complete` frame that closes out `attach_session`'s catch-up
// before sending anything — see the `expect.poll` below, right before
// the follow-up is sent. Two harness-only bugs (unrelated to the
// product) had to be fixed to even reach that race: the agent-creation
// POST needed the CSRF header this suite's double-submit pattern
// requires, and it needed `type`/`soul`, both required by the
// ADR-034 discriminated-union create contract added since this test
// was written.
//
// The underlying PRODUCT bug (not this test harness) already has a
// rock-solid Go regression test — TestWS_Message_FindsSession_InPerAgentStore
// in pkg/gateway/websocket_session_test.go — verified to fail without
// the fix and pass with it.
canonicalTest.describe('Bug-Hans: per-agent session resume must not produce "session not found"', () => {
  canonicalTest.beforeEach(async ({ canonicalGateway }) => {
    BUG_BASE_URL = canonicalGateway.baseURL
    BUG_OMNIPUS_HOME = canonicalGateway.homeDir
  })

  canonicalTest('(Bug-Hans-a) follow-up message on a per-agent session succeeds', async ({ page }) => {
    test.slow() // real LLM call once the message lands; budget accordingly
    requireBugHarnessHome() // unresolvable home is a fail-fast preflight, not a skip (helper above)
    // Collect every WS frame the SPA receives for the life of the test. The
    // hash-router navigation in step 4 below does NOT open a new
    // WebSocket — the connection opened by step 1's page.goto('/') persists
    // — so this listener must be registered before that connection opens to
    // see every frame, including the `attach_session` round-trip step 4
    // triggers. Used below to wait for the real "session attached" signal
    // before sending the follow-up message (see the comment at that wait).
    const receivedFrames: string[] = []
    page.on('websocket', (ws) => {
      ws.on('framereceived', (frame) => {
        if (typeof frame.payload === 'string') receivedFrames.push(frame.payload)
      })
    })

    // 1. Navigate to the SPA first so the page is on the right origin —
    //    localStorage is inaccessible from about:blank, which is the default
    //    page state before any goto().
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // 2. Create a custom agent — its store lives at
    //    OMNIPUS_HOME/agents/<id>/sessions/, NOT in the shared layout.
    //    Auth rides the storageState `omnipus-session` cookie automatically
    //    (page.request shares the browser context's cookie jar — ADR-044
    //    removed the JS-readable bearer token this used to read from
    //    localStorage). The double-submit CSRF pattern additionally
    //    requires the SAME csrf cookie value echoed back as an explicit
    //    `X-CSRF-Token` header (src/lib/api.ts's withCsrfHeaders does this
    //    for real browser-driven fetches; see agents.spec.ts's identical
    //    csrfHeaders() helper) — without it the gateway's CSRF middleware
    //    403s with "csrf header missing" before this test ever reaches the
    //    per-agent-session behaviour it's meant to exercise.
    const cookies = await page.context().cookies()
    const csrfCookie = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf')
    const authHeaders: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(csrfCookie ? { 'X-CSRF-Token': csrfCookie.value } : {}),
    }
    const agentResp = await page.request.post(`${BUG_BASE_URL}/api/v1/agents`, {
      headers: authHeaders,
      data: {
        // `type` and `soul` are required by the v0.1.1 discriminated-union
        // create contract (ADR-034, AgentCreateRequestMain.yaml) — 'Main' is
        // the wire enum for a user-defined chat colleague, matching the
        // custom-agent-via-builder scenario this test reproduces (see
        // agents.spec.ts's identical create-agent payload shape).
        name: `Hans-${Date.now()}`,
        type: 'Main',
        soul: 'You are Hans, a regression fixture agent for per-agent session resume.',
        description: 'regression fixture for per-agent session resume',
      },
    })
    expect(agentResp.ok(), `POST /agents failed: ${agentResp.status()} ${await agentResp.text()}`).toBe(true)
    const agent = (await agentResp.json()) as { id: string; name: string }
    await provisionHansWorkspaceTeam(page, BUG_BASE_URL, agent.id, authHeaders)

    // 3. Create a session owned by that custom agent and seed its transcript -
    //    what the task scheduler leaves behind when it schedules work to a
    //    custom agent: a session with a prior user entry. There is ONE session
    //    store now: the per-agent stores this test used to seed
    //    (OMNIPUS_HOME/agents/<id>/sessions/) were deleted by DEL-10
    //    (pkg/agent/shared_session_store.go: "There are no per-agent stores"),
    //    so a session written there is invisible to REST ("404 session not
    //    found") and the route can never attach it - every run failed at the
    //    attach wait for that reason. Create it through the public REST API
    //    (it lands in OMNIPUS_HOME/sessions/<id>) and seed the transcript there.
    const sessionResp = await page.request.post(`${BUG_BASE_URL}/api/v1/sessions`, {
      headers: authHeaders,
      data: { agent_id: agent.id, type: 'chat' },
    })
    expect(sessionResp.ok(), `POST /sessions failed: ${sessionResp.status()} ${await sessionResp.text()}`).toBe(true)
    const sessionID = ((await sessionResp.json()) as { id: string }).id
    await assertHansSessionUnfiled(page, BUG_BASE_URL, sessionID)
    const sessionDir = path.join(BUG_OMNIPUS_HOME, 'sessions', sessionID)
    expect(fs.existsSync(sessionDir), `session dir ${sessionDir} must exist after POST /sessions`).toBe(true)
    const nowISO = new Date().toISOString()
    const priorEntry = {
      id: randomUUID(),
      role: 'user',
      agent_id: agent.id,
      content: 'task done — open me to resume',
      timestamp: nowISO,
    }
    await seedCanonicalLegacyTranscript(sessionID, [priorEntry])

    // 4. Navigate directly to the seeded session (TanStack Router hash form).
    await page.goto(`/#/sessions/${sessionID}`)
    // Wait for the chat surface to be bound to THIS session - not for the
    // "banner" landmark. The only banner (WorkspaceTabContainer's <header>)
    // belongs to the workspace route; a session with no workspace_id (this one:
    // created for a custom agent with no workspace) renders the inline
    // ChatScreen, which has none. A banner wait here is therefore a race with
    // the route swap: it passed only if the first poll beat the swap (the old
    // route's header still on screen) and failed 15 s later otherwise - failed
    // the first attempt, passed on retry, in 3 of 4 runs.
    await expect(page.locator(`[data-active-session-id="${sessionID}"]`)).toBeVisible({ timeout: 15_000 })

    const input = chatInput(page)
    await expect(input).toBeEnabled({ timeout: 15_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts).
    await waitForConnected(page, { timeout: 15_000 })

    // 4b. Wait for the SPA's session-attach round-trip to actually finish
    // before sending anything. `toBeEnabled()` + `waitForConnected()` above
    // only prove the WS is open — they say nothing about whether THIS
    // route's mount effect (SessionRoute, src/routes/_app/sessions.$sessionId.tsx)
    // has yet sent `attach_session` for our seeded session and flipped
    // `activeSessionId` in the store. Confirmed by instrumenting the real WS
    // traffic: without this wait, the composer sends the follow-up BEFORE
    // that effect runs, so the outbound `message` frame carries no
    // `session_id` at all — the gateway mints a brand-new session (attributed
    // to the default agent) instead of resuming this one, and
    // `attach_session` for the real seeded session only goes out afterward.
    // That race, not an empty seeded transcript, is the actual, confirmed
    // cause of this test's original failure. Since the #823 catch-up redesign
    // the terminal frame of an attach_session response (incremental AND
    // snapshot path) is `catch_up_complete` (contracts/asyncapi.yaml,
    // receiveCatchUpComplete) - NOT `done`, which now only ends a TURN. This
    // wait used to key on `done` and timed out on every run once the
    // redesign landed: the same "attach finished" signal, renamed.
    const frameDigest = () =>
      receivedFrames
        .map((raw) => {
          try {
            const f = JSON.parse(raw) as { type?: string; session_id?: string }
            return `${f.type ?? '?'}:${f.session_id ?? '-'}`
          } catch {
            return 'unparseable'
          }
        })
        .filter((d) => !d.startsWith('ping') && !d.startsWith('pong'))
        .slice(-40)
        .join(' ')
    try {
      await expect
      .poll(
        () =>
          receivedFrames.some((raw) => {
            try {
              const frame = JSON.parse(raw) as { type?: string; session_id?: string }
              return frame.type === 'catch_up_complete' && frame.session_id === sessionID
            } catch {
              return false
            }
          }),
        {
          timeout: 15_000,
          message: `expected a WS 'catch_up_complete' frame for session ${sessionID} confirming attach_session's catch-up completed before sending a follow-up`,
        },
      )
      .toBe(true)
    } catch (err) {
      // Name what DID arrive: a missing terminal frame and a terminal frame
      // carrying a different session id are different defects.
      const detail = await page.request
        .get(`${BUG_BASE_URL}/api/v1/sessions/${sessionID}`, { headers: authHeaders, failOnStatusCode: false })
        .then(async (r) => `${r.status()} ${(await r.text()).slice(0, 300)}`)
        .catch((e: unknown) => `request failed: ${String(e)}`)
      throw new Error(
        `${(err as Error).message}\nlast WS frames received (type:session_id): ${frameDigest()}\n` +
          `GET /api/v1/sessions/${sessionID} -> ${detail}\npage url: ${page.url()}`,
        { cause: err },
      )
    }

    // 5. Listen for the "session not found" error frame. If it arrives,
    //    the bug is back. We watch the page's connection store for a
    //    toast or error banner with that text.
    const errorBanner = page.locator('text=/session not found/i')

    // 6. Send a follow-up message. With the fix this lands normally; without
    //    it the SPA would surface the WS error.
    const challenge = assistantReplyChallenge('hans-follow-up')
    const followup = `Hello from the regression test.\nReply only with the requested token. ${challenge.instruction}`
    expect(followup, 'only the assistant can supply the concatenated reply nonce').not.toContain(challenge.nonce)
    await input.fill(followup)
    await input.press('Enter')

    // 7. The user message bubble must appear (echoed by the WS). `.last()`,
    //    not `.first()` — the thread now also renders the seeded prior
    //    entry's replay ("task done — open me to resume") ahead of it.
    const userMsg = page.locator('[data-message-id].flex-row-reverse').last()
    await expect(userMsg).toBeVisible({ timeout: 10_000 })
    await expect(userMsg).toContainText('Hello from the regression test.')

    // 8. The "session not found" surface must NOT appear at any point.
    await expect(errorBanner).toBeHidden({ timeout: 2_000 })

    // 9. Belt-and-suspenders: the transcript on disk now contains the user
    //    message — proving the WS handler accepted the frame and wrote it
    //    through the per-agent store.
    //
    //    POLL the file; never a fixed sleep. This assertion used to be a
    //    `page.waitForTimeout(1_000)` followed by a single readFileSync, and
    //    on 2026-08-28 that lost the race on all three attempts of the
    //    llm-light shard while the PRODUCT WAS WORKING CORRECTLY — the
    //    follow-up entry was on disk in every one of the three failed runs,
    //    just 0.1–1.8s later than the read. The gateway does persist this
    //    entry synchronously inside handleChatMessage (pkg/gateway/websocket.go,
    //    `store.AppendTranscript(sessionID, entry)`), but the handler first
    //    performs the M4 workspace-bind `SetMeta` — a flock + fsync + rename +
    //    directory-fsync write that measured ~1.2s on the CI worker with
    //    twelve e2e shards sharing one disk. A one-second sleep can never be
    //    the right budget for an fsync on a loaded box.
    //
    //    Note also that step 7's message bubble is the SPA's optimistic local
    //    echo, so it is no evidence at all that the server write landed —
    //    this poll is the only assertion in the test that proves it.
    const transcriptPath = path.join(sessionDir, 'transcript.jsonl')
    await expect
      .poll(
        () => {
          try {
            return fs.readFileSync(transcriptPath, 'utf-8')
          } catch {
            return '' // file not created yet — keep polling, don't throw
          }
        },
        {
          timeout: 15_000,
          message: 'transcript.jsonl must contain the follow-up user message',
        },
      )
      .toContain('Hello from the regression test.')
    // Addendum 5: the case's "follow-up succeeds" intent requires a completed saved answer, not only accepted input.
    await assertHansReplyPersisted(page, receivedFrames, sessionID, agent.id, challenge.nonce, transcriptPath)
    await assertHansSessionUnfiled(page, BUG_BASE_URL, sessionID)
  })
})
