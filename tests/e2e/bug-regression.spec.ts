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
import {
  chatInput,
  assistantMessages,
  waitForConnected,
} from './fixtures/selectors'
import { freshChat } from './fixtures/session-setup'

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

// ─── Bug-3: Concurrent sessions both respond ──────────────────────────────────

test.describe('Bug-3: Concurrent sessions both respond', () => {
  test(
    '(Bug-3-a) two chats opened in parallel both receive replies',
    async ({ page, context }) => {
      // Real LLM turns in two tabs; the central e2e model (tests/e2e/e2e-model.json) is reliable
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

      // Fresh chats on both tabs. The agent row's "New chat with <Agent>"
      // action this used to click is REMOVED (founder decision D7 — capability
      // stays, UI access point gone; FR-029/FR-031 still govern). freshChat
      // (fixtures/session-setup.ts) creates each tab's session via the public
      // REST API and deep-links it, restoring the intended fresh-session
      // setup: two tabs on two genuinely distinct, empty sessions.
      await freshChat(page)
      await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })
      await freshChat(page2)
      await expect(assistantMessages(page2)).toHaveCount(0, { timeout: 10_000 })

      // Send messages on both tabs nearly simultaneously. Dismiss first
      // (BOTH tabs): a retry attempt's fresh page rehydrates any approval the
      // failed attempt left pending (session_state frame on every WS connect),
      // and that Radix dialog's focus trap hijacks the composer's Enter press
      // (activates the focused Deny button), so the message is never sent at
      // all. Same guard as Bug-5-a/5-b's turns.
      await dismissAndAnnotate(page)
      await dismissAndAnnotate(page2)
      await input1.fill('Bug-3 concurrent test hello tab1')
      await input2.fill('Bug-3 concurrent test hello tab2')
      await input1.press('Enter')
      await input2.press('Enter')

      // Both must get a settled reply. The central e2e model (tests/e2e/e2e-model.json) is
      // reliable but slower than the old gemini pick, and concurrent turns share
      // the model, so allow generous headroom.
      const replyTimeout = 90_000

      // The contract under test is "both sessions are serviced concurrently"
      // (no session starvation). The assertion MUST wait for a SETTLED reply, not
      // the optimistic empty assistant placeholder the SPA renders synchronously
      // on send (ChatScreen.tsx ~860 emits data-message-id + data-status="running"
      // before any token arrives). Without the `:not([data-status="running"])`
      // exclusion the test would go green the instant Enter is pressed — even under
      // total session starvation, the exact bug it guards. So the drain waits reuse
      // the `assistantMessages` helper (selectors.ts), which excludes running
      // placeholders. This STILL tolerates a provider-interrupted reply: an
      // interrupted/incomplete message has data-status "incomplete"/"interrupted",
      // NOT "running", so it is still admitted as a serviced reply.
      await expectAnyAssistantMessageWithApprovalDrain(page, replyTimeout).catch((e) => {
        throw new Error(
          `BUG-3: Tab 1 did not receive a reply within ${replyTimeout / 1000}s. ` +
          `This indicates session starvation — while tab 2 was processing, ` +
          `tab 1 was blocked. Original error: ${e.message}`,
        )
      })

      // Wait for at least one assistant reply in tab 2.
      await expectAnyAssistantMessageWithApprovalDrain(page2, replyTimeout).catch((e) => {
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

/**
 * Bug-3 variant: waits for AT LEAST ONE settled assistant message to be
 * visible — the exact predicate of the plain wait it replaces
 * (expect(assistantMessages(p).first()).toBeVisible()), poll-shaped so the
 * approval drain fits inside, with the SAME budget the plain waits used. No
 * weakening: the settled-reply requirement is unchanged (assistantMessages
 * excludes the data-status="running" placeholder ChatScreen.tsx ~860 renders
 * synchronously on send, so the test cannot go green the instant Enter is
 * pressed — the session-starvation signal it guards); only the drain ticks
 * are added.
 */
async function expectAnyAssistantMessageWithApprovalDrain(
  page: Page,
  timeoutMs: number,
): Promise<void> {
  await expect
    .poll(
      async () => {
        await drainApprovalDialogsAndCount(page)
        return assistantMessages(page).first().isVisible()
      },
      { timeout: timeoutMs, intervals: [500, 1_000, 2_000, 4_000] },
    )
    .toBe(true)
}

// ─── Bug-5: Replay frame ordering preserved on reconnect ─────────────────────

test.describe('Bug-5: Replay frame ordering preserved after navigation', () => {
  test(
    '(Bug-5-a) navigating away and back to a session preserves message order',
    async ({ page }) => {
      // Real LLM turn + replay; the central e2e model (tests/e2e/e2e-model.json) is reliable but
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

      // Get a fresh session (freshChat — public REST + deep-link; the
      // agent-row "New chat" button is removed by D7).
      await freshChat(page)
      await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })

      // Send a message and wait for reply. Dismiss first: a retry attempt's
      // fresh page rehydrates any approval the failed attempt left pending
      // (session_state frame on every WS connect), and that dialog's focus
      // trap hijacks the composer's Enter press (activates the focused Deny
      // button), so the message is never sent. Same guard as Bug-5-b's turns.
      await dismissAndAnnotate(page)
      await input.fill('Bug-5 replay order test message one')
      await input.press('Enter')
      await expectAssistantMessagesWithApprovalDrain(page, 1, 90_000)

      // Capture the text of the first assistant message.
      const firstMessageText = await assistantMessages(page).first().textContent()

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
      if (firstMessageText !== replayedText) {
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
      // Two real LLM turns + replay; the central e2e model (tests/e2e/e2e-model.json) is reliable
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

      // Fresh session (freshChat — public REST + deep-link; the agent-row
      // "New chat" button is removed by D7).
      await freshChat(page)
      await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 })

      // Turn 1. Dismiss first: a retry attempt's fresh page rehydrates any
      // approval the failed attempt left pending (session_state frame on
      // every WS connect), and that dialog's focus trap hijacks the
      // composer's Enter press (activates the focused Deny button), so the
      // message is never sent.
      await dismissAndAnnotate(page)
      await input.fill('Bug-5 turn 1 — first message')
      await input.press('Enter')
      await expectAssistantMessagesWithApprovalDrain(page, 1, 90_000)

      // Turn 2.
      await dismissAndAnnotate(page)
      await input.fill('Bug-5 turn 2 — second message')
      await input.press('Enter')
      await expectAssistantMessagesWithApprovalDrain(page, 2, 90_000)

      // Capture original ordering.
      const originalTexts = await assistantMessages(page).allTextContents()
      const sessionURL = page.url()

      // Navigate away and back. (WU-11 FR-108: surviving away-surface.)
      await page.goto('/#/skills')
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
const BUG_BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
const BUG_OMNIPUS_HOME =
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
test.describe('Bug-Hans: per-agent session resume must not produce "session not found"', () => {

  test('(Bug-Hans-a) follow-up message on a per-agent session succeeds', async ({ page }) => {
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
    fs.writeFileSync(
      path.join(sessionDir, 'transcript.jsonl'),
      JSON.stringify({ view_membership: 'chat', ...priorEntry }) + '\n',
    )

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
  })
})
