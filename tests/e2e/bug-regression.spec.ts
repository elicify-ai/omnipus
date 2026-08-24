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

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, assistantMessages, newChatButton, waitForConnected } from './fixtures/selectors'

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
    await page.route('**/api/v1/state', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ onboarding_complete: false }),
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
    // (`onboarding_complete` is AppState's only required field —
    // src/lib/api/generated/schemas.ts:1930-1937) so `beforeLoad` does not
    // redirect, letting the test observe the REAL first step and its REAL
    // forward control, scoped by heading so it cannot match ChatScreen's
    // differently-worded empty-state heading ("Welcome to Omnipus").
    await page.route('**/api/v1/state', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ onboarding_complete: false }),
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

// ─── Bug-3: Concurrent sessions both respond ──────────────────────────────────

test.describe('Bug-3: Concurrent sessions both respond', () => {
  test(
    '(Bug-3-a) two chats opened in parallel both receive replies',
    async ({ page, context }) => {
      // Real LLM turns in two tabs; glm-5.2 (the standard e2e model) is reliable
      // but slower than the old gemini pick, so budget the full slow ceiling.
      test.slow()
      // BDD: Given two browser tabs open to different chat sessions
      //      When a message is sent in each tab within a short window
      //      Then both tabs receive at least one assistant message within 30s
      //
      // Traces to: Bug-3 (concurrent sessions both respond)

      // Navigate to first chat.
      await page.goto('/')
      const input1 = chatInput(page)
      await expect(input1).toBeVisible({ timeout: 15_000 })

      // Open a second tab.
      const page2 = await context.newPage()
      await page2.goto('/')
      const input2 = chatInput(page2)
      await expect(input2).toBeVisible({ timeout: 15_000 })

      // Click New Chat on both to get fresh sessions.
      const newChat1 = newChatButton(page)
      const newChat2 = newChatButton(page2)
      if (await newChat1.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat1.click()
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      }
      if (await newChat2.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat2.click()
        await expect(assistantMessages(page2)).toHaveCount(0, { timeout: 10_000 })
      }

      // Send messages on both tabs nearly simultaneously.
      await input1.fill('Bug-3 concurrent test hello tab1')
      await input2.fill('Bug-3 concurrent test hello tab2')
      await input1.press('Enter')
      await input2.press('Enter')

      // Both must get a settled reply. glm-5.2 (the standard e2e model) is
      // reliable but slower than the old gemini pick, and concurrent turns share
      // the model, so allow generous headroom.
      const replyTimeout = 90_000

      // The contract under test is "both sessions are serviced concurrently"
      // (no session starvation). The assertion MUST wait for a SETTLED reply, not
      // the optimistic empty assistant placeholder the SPA renders synchronously
      // on send (ChatScreen.tsx ~860 emits data-message-id + data-status="running"
      // before any token arrives). Without the `:not([data-status="running"])`
      // exclusion the test would go green the instant Enter is pressed — even under
      // total session starvation, the exact bug it guards. So we reuse the
      // `assistantMessages` helper (selectors.ts), which excludes running
      // placeholders. This STILL tolerates a provider-interrupted reply: an
      // interrupted/incomplete message has data-status "incomplete"/"interrupted",
      // NOT "running", so it is still admitted as a serviced reply.
      const anyAssistantReply = (p: typeof page) => assistantMessages(p)

      // Wait for at least one settled assistant reply in tab 1.
      await expect(anyAssistantReply(page).first()).toBeVisible({ timeout: replyTimeout })
        .catch((e) => {
          throw new Error(
            `BUG-3: Tab 1 did not receive a reply within ${replyTimeout / 1000}s. ` +
            `This indicates session starvation — while tab 2 was processing, ` +
            `tab 1 was blocked. Original error: ${e.message}`,
          )
        })

      // Wait for at least one assistant reply in tab 2.
      await expect(anyAssistantReply(page2).first()).toBeVisible({ timeout: replyTimeout })
        .catch((e) => {
          throw new Error(
            `BUG-3: Tab 2 did not receive a reply within ${replyTimeout / 1000}s. ` +
            `This indicates session starvation — while tab 1 was processing, ` +
            `tab 2 was blocked. Original error: ${e.message}`,
          )
        })

      await page2.close()
    },
  )
})

// ─── Bug-5: Replay frame ordering preserved on reconnect ─────────────────────

test.describe('Bug-5: Replay frame ordering preserved after navigation', () => {
  test(
    '(Bug-5-a) navigating away and back to a session preserves message order',
    async ({ page }) => {
      // Real LLM turn + replay; glm-5.2 (the standard e2e model) is reliable but
      // slower than the old gemini pick, so budget the full slow ceiling.
      test.slow()
      // BDD: Given a chat session with at least 1 assistant message
      //      When the user navigates to another page and back
      //      Then the messages appear in the same order as before
      //
      // Traces to: Bug-5 (replay frame ordering preserved when leaving + returning to chat)

      await page.goto('/')
      const input = chatInput(page)
      await expect(input).toBeVisible({ timeout: 15_000 })

      // Get a fresh session.
      const newChat = newChatButton(page)
      if (await newChat.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat.click()
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      }

      // Send a message and wait for reply.
      await input.fill('Bug-5 replay order test message one')
      await input.press('Enter')
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 })

      // Capture the text of the first assistant message.
      const firstMessageText = await assistantMessages(page).first().textContent()

      // Capture the URL of the current session to return to it.
      const sessionURL = page.url()

      // Navigate away.
      await page.goto('/#/agents')
      await page.waitForLoadState('networkidle')

      // Navigate back to the session.
      await page.goto(sessionURL)
      await page.waitForLoadState('networkidle')

      // Wait for messages to restore via replay.
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 45_000 })
        .catch((e) => {
          throw new Error(
            `BUG-5: After navigating back to the session, the assistant message ` +
            `did not appear. This indicates the replay path failed to restore ` +
            `the session state. Original error: ${e.message}`,
          )
        })

      // The replayed message must have the same content as before navigation.
      const replayedText = await assistantMessages(page).first().textContent()
      if (firstMessageText && replayedText && firstMessageText !== replayedText) {
        throw new Error(
          `BUG-5: Replayed message content differs from original. ` +
          `Before: "${firstMessageText}" | After replay: "${replayedText}". ` +
          `This indicates the replay stream produced frames in the wrong order ` +
          `or overwrote the message content.`,
        )
      }
    },
  )

  test(
    '(Bug-5-b) two-turn session: turns appear in chronological order after replay',
    async ({ page }) => {
      // Two real LLM turns + replay; glm-5.2 (the standard e2e model) is reliable
      // but slower than the old gemini pick, so budget the full slow ceiling.
      test.slow()
      // BDD: Given a session with 2 turns (user→assistant, user→assistant)
      //      When the user navigates away and back
      //      Then turn 1 appears before turn 2 (causal order preserved)
      //
      // Traces to: Bug-5 multi-turn replay ordering

      await page.goto('/')
      const input = chatInput(page)
      await expect(input).toBeVisible({ timeout: 15_000 })

      const newChat = newChatButton(page)
      if (await newChat.isVisible({ timeout: 3_000 }).catch(() => false)) {
        await newChat.click()
        await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      }

      // Turn 1.
      await input.fill('Bug-5 turn 1 — first message')
      await input.press('Enter')
      await expect(assistantMessages(page)).toHaveCount(1, { timeout: 90_000 })

      // Turn 2.
      await input.fill('Bug-5 turn 2 — second message')
      await input.press('Enter')
      await expect(assistantMessages(page)).toHaveCount(2, { timeout: 90_000 })

      // Capture original ordering.
      const originalTexts = await assistantMessages(page).allTextContents()
      const sessionURL = page.url()

      // Navigate away and back.
      await page.goto('/#/agents')
      await page.waitForLoadState('networkidle')
      await page.goto(sessionURL)
      await page.waitForLoadState('networkidle')

      // Wait for 2 messages to restore.
      await expect(assistantMessages(page)).toHaveCount(2, { timeout: 45_000 })
        .catch((e) => {
          throw new Error(
            `BUG-5: After navigating back, expected 2 assistant messages but got fewer. ` +
            `Original error: ${e.message}`,
          )
        })

      // Messages must be in the same order.
      const replayedTexts = await assistantMessages(page).allTextContents()
      if (originalTexts.length === replayedTexts.length) {
        for (let i = 0; i < originalTexts.length; i++) {
          if (originalTexts[i] !== replayedTexts[i]) {
            throw new Error(
              `BUG-5: Replayed message[${i}] content changed. ` +
              `Original: "${originalTexts[i]}" | Replayed: "${replayedTexts[i]}".`,
            )
          }
        }
      }
    },
  )
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
// This E2E asserts the user-facing outcome: a per-agent session can be
// resumed and a follow-up message lands without the "session not found"
// error toast, with the message appearing in the chat.
import * as fs from 'fs'
import * as path from 'path'
import { randomUUID } from 'crypto'
const BUG_BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
const BUG_OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '')

test.describe('Bug-Hans: per-agent session resume must not produce "session not found"', () => {
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
  // for the WS `done` frame that closes out `attach_session`'s replay
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
  test('(Bug-Hans-a) follow-up message on a per-agent session succeeds', async ({ page }) => {
    test.slow() // real LLM call once the message lands; budget accordingly
    if (!BUG_OMNIPUS_HOME) {
      test.skip(true, 'OMNIPUS_HOME unavailable')
      return
    }
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

    // 3. Seed a session directly under the agent's per-agent store —
    //    mimicking what the task scheduler does when scheduling work to a
    //    custom agent. The session_id ULID is from the gateway's normal
    //    minting alphabet so validation.EntityID accepts it.
    const sessionID = `session_${Date.now().toString(36).toUpperCase().padStart(26, 'A')}`
    const sessionDir = path.join(
      BUG_OMNIPUS_HOME,
      'agents',
      agent.id,
      'sessions',
      sessionID,
    )
    fs.mkdirSync(sessionDir, { recursive: true })
    const nowISO = new Date().toISOString()
    const meta = {
      id: sessionID,
      agent_id: agent.id,
      title: `task done — open me to resume`,
      status: 'active',
      created_at: nowISO,
      updated_at: nowISO,
      stats: {
        tokens_in: 0, tokens_out: 0, tokens_total: 0,
        cost: 0, tool_calls: 0, message_count: 1,
      },
      channel: 'webchat',
      partitions: null,
      agent_ids: [agent.id],
      active_agent_id: agent.id,
      type: 'chat',
    }
    fs.writeFileSync(path.join(sessionDir, 'meta.json'), JSON.stringify(meta, null, 2))
    // Seed a real one-line transcript entry, not an empty file — this is
    // what a session the task scheduler actually created looks like (it
    // always has at least the kickoff message). Shape matches
    // session.TranscriptEntry (pkg/session/daypartition.go) exactly as
    // production writes it for a real user message on the interactive WS
    // path (uuid id, role, agent_id, content, RFC3339 timestamp —
    // pkg/gateway/websocket.go's handleChatMessage, `entry :=
    // session.TranscriptEntry{...}`).
    //
    // NOTE ON THE ORIGINAL 2026-05-30 DIAGNOSIS: this test was `.fixme`'d on
    // the theory that an EMPTY seeded transcript reads to SPA hydration as
    // "fresh chat", causing the follow-up to go out with no session_id.
    // Reproducing this test live (instrumenting the real WS traffic) proved
    // that theory wrong: an empty transcript.jsonl round-trips through
    // attach_session/replay/done exactly as well as a seeded one. The real
    // cause was a RACE, unrelated to transcript content — see the comment
    // on the `expect.poll` wait below, right before the follow-up is sent.
    const priorEntry = {
      id: randomUUID(),
      role: 'user',
      agent_id: agent.id,
      content: 'task done — open me to resume',
      timestamp: nowISO,
    }
    fs.writeFileSync(path.join(sessionDir, 'transcript.jsonl'), JSON.stringify(priorEntry) + '\n')

    // 4. Navigate directly to the seeded session (TanStack Router hash form).
    await page.goto(`/#/sessions/${sessionID}`)
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

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
    // cause of this test's original failure. The server's `done` frame
    // closing out `attach_session`'s replay (of the seeded transcript entry
    // above) is the one genuine "session attached" signal in the WS
    // vocabulary — there is no separate ack frame type for it.
    await expect
      .poll(
        () =>
          receivedFrames.some((raw) => {
            try {
              const frame = JSON.parse(raw) as { type?: string; session_id?: string }
              return frame.type === 'done' && frame.session_id === sessionID
            } catch {
              return false
            }
          }),
        {
          timeout: 15_000,
          message: `expected a WS 'done' frame for session ${sessionID} confirming attach_session's replay completed before sending a follow-up`,
        },
      )
      .toBe(true)

    // 5. Listen for the "session not found" error frame. If it arrives,
    //    the bug is back. We watch the page's connection store for a
    //    toast or error banner with that text.
    const errorBanner = page.locator('text=/session not found/i')

    // 6. Send a follow-up message. With the fix this lands normally; without
    //    it the SPA would surface the WS error.
    await input.fill('Hello from the regression test.')
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
    await page.waitForTimeout(1_000) // let the append flush
    const transcript = fs.readFileSync(
      path.join(sessionDir, 'transcript.jsonl'),
      'utf-8',
    )
    expect(
      transcript.includes('Hello from the regression test.'),
      `transcript.jsonl must contain the user message; got: ${transcript.slice(0, 300)}`,
    ).toBe(true)
  })
})
