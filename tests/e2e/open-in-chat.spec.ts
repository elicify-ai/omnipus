/**
 * open-in-chat.spec.ts — sprint #258 e2e tests for issues #250 and #253.
 *
 * NOTE: These tests drive against the embedded SPA (the Go binary), NOT the
 * Vite dev server. The dev server proxies /api but does not reflect binary-
 * embedded SPA changes. Always test against the binary:
 *
 *   export OMNIPUS_HOME=/tmp/omnipus-e2e-test
 *   rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
 *   npm run build && cp -r dist/spa/* pkg/gateway/spa/
 *   go build -o /tmp/omnipus ./cmd/omnipus/
 *   OMNIPUS_BEARER_TOKEN="" /tmp/omnipus gateway --allow-empty &
 *
 * (a) "Open in Chat" → session route → live streaming (#250)
 *     The Command Center screen this test originally drove through is retired
 *     (CLAUDE.md "Retired surfaces" — /tasks is now a redirect stub into the
 *     workspace Board/Calendar) and `data-testid="task-item"` no longer exists
 *     anywhere in src/, so the click-through UI path is unreachable dead code
 *     and has been removed. TaskDetailPanel's "Open in Chat" button
 *     (src/components/workspaces/OpenInChatButton.tsx) has its own unit test
 *     (OpenInChatButton.test.tsx) covering the click itself. What remains
 *     here, proven end to end against a live gateway:
 *       Given a real session created via POST /api/v1/sessions
 *       And a real task created via POST /api/v1/tasks (workspace-scoped,
 *         proving that endpoint — which TaskDetailPanel's data comes from —
 *         still accepts a real create request)
 *       When the SPA navigates to /sessions/$sessionId
 *       Then the WS attach_session frame is sent (chat loads with existing history)
 *       And a new text message sent from that screen produces a streamed assistant token.
 *
 * (b) Failed send → user bubble shows error + reachable Retry (#253)
 *     Given the WS connection drops mid-send
 *     When the user sends a message (no active session)
 *     Then the user bubble appears with status:error
 *     And a Retry button is visible
 *     After reconnect, clicking Retry resends the message successfully.
 */

import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { assistantMessages, userMessages, waitForConnected, DEFAULT_WORKSPACE_NAME } from './fixtures/selectors'
import { openSessionByDeepLink } from './fixtures/session-setup'
import { Agent, Session, Workspace } from '../../src/lib/api/generated/schemas'
import type { SessionCreateRequest } from '../../src/lib/api/generated/openapi-types'
import { GatewayProcess } from './fixtures/gateway-process'
import { SEND_RETRY_FIRST_REPLY, SEND_RETRY_RESEND_REPLY, startSendRetryProvider } from './fixtures/send-retry-provider'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── (a) Open in Chat → live streaming ─────────────────────────────────────────

test(
  '(a) Open in Chat navigates to session route and enables chat',
  async ({ page }) => {
    test.setTimeout(120_000)

    await page.goto(`${BASE_URL}/`)
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Helper: make an authenticated fetch in-browser
    type FetchResult = { ok: boolean; status: number; body: unknown }
    const apiFetch = async (method: string, path: string, body?: object): Promise<FetchResult> => {
      return page.evaluate(
        async ({ method, path, body }: { method: string; path: string; body?: object }) => {
          const token =
            sessionStorage.getItem('omnipus_auth_token') ??
            localStorage.getItem('omnipus_auth_token') ??
            ''
          const csrfCookie = document.cookie
            .split(';')
            .find((c) => c.trim().startsWith('csrf=') || c.trim().startsWith('__Host-csrf='))
          const csrfToken = csrfCookie ? csrfCookie.split('=').slice(1).join('=').trim() : ''
          const res = await fetch(path, {
            method,
            headers: {
              Authorization: `Bearer ${token}`,
              'Content-Type': 'application/json',
              ...(method !== 'GET' ? { 'X-CSRF-Token': csrfToken } : {}),
            },
            ...(body ? { body: JSON.stringify(body) } : {}),
          })
          return { ok: res.ok, status: res.status, body: await res.json().catch(() => null) }
        },
        { method, path, body },
      )
    }

    // Create a real session via the API — this is the session route the test
    // navigates to below (the reachable end of the "Open in Chat" flow).
    // NOTE: agent_id is REQUIRED — omitting it defaults to the non-existent "main"
    // agent, which makes the session read-only ("Agent removed") and disables the
    // chat input. Use a seeded core agent ('mia', the guide) so the session is live.
    const sessionResp = await apiFetch('POST', '/api/v1/sessions', { agent_id: 'mia' })
    expect(sessionResp.ok).toBeTruthy()
    const sessionId = (sessionResp.body as { id: string }).id
    expect(typeof sessionId).toBe('string')

    // Create a workspace to hang the task off of. POST /api/v1/tasks requires
    // workspace_id (contracts/components/schemas/TaskCreateRequest.yaml: required
    // [title, action, workspace_id]) — every task is workspace-scoped, there is
    // no bare/global task creation.
    const wsResp = await apiFetch('POST', '/api/v1/workspaces', {
      name: `E2E Open-in-Chat Workspace ${Date.now()}`,
    })
    expect(wsResp.ok, `POST /api/v1/workspaces failed: ${JSON.stringify(wsResp.body)}`).toBe(true)
    const workspaceId = (wsResp.body as { id: string }).id
    expect(typeof workspaceId).toBe('string')

    // Create a task in that workspace. NOTE: TaskCreateRequest has no session_id
    // field at all (additionalProperties: false on the schema) — a task's
    // session_id is populated server-side once the task actually runs (see
    // task.Task.SessionID's doc comment / pkg/gateway/rest_tasks.go), never
    // supplied by the client at creation. This is a real assertion, not a
    // soft-skip: a regression in task creation must fail the suite, not
    // silently vanish (see docs/internal/false-green-patterns.md).
    //
    // `criteria` and `dod` are BOTH mandatory at creation (operator decision
    // D-C, 2026-09-11; GOAL-FR-021/FR-047/FR-048): the server answers 400
    // naming whichever list is missing, and the DoD must be DISTINCT from the
    // acceptance criteria. This task is inert scaffolding for the deep-link
    // assertion below — it is never dispatched or judged — so both lists are
    // the smallest honest pair that satisfies the gate.
    const taskResp = await apiFetch('POST', '/api/v1/tasks', {
      title: 'e2e-open-in-chat-test',
      prompt: 'Test task for open-in-chat e2e',
      workspace_id: workspaceId,
      criteria: [
        {
          kind: 'prose',
          text: 'the open-in-chat deep link reaches a live, working chat session',
          author: { kind: 'user', id: 'admin' },
          status: 'pending',
        },
      ],
      dod: [
        {
          kind: 'prose',
          text: 'this task exists in the workspace and is reachable by id',
          author: { kind: 'user', id: 'admin' },
          status: 'pending',
        },
      ],
    })
    expect(taskResp.ok, `POST /api/v1/tasks failed: ${JSON.stringify(taskResp.body)}`).toBe(true)

    // The Command Center screen and its task-item click-through are retired
    // (CLAUDE.md "Retired surfaces" — /tasks is now a redirect stub into the
    // workspace Board/Calendar) and `data-testid="task-item"` does not exist
    // anywhere in src/ — that UI path can never execute. TaskDetailPanel's
    // "Open in Chat" (src/components/workspaces/OpenInChatButton.tsx) is
    // covered by its own unit test (OpenInChatButton.test.tsx); this e2e test
    // proves the reachable end of that flow instead: navigating straight to a
    // session route enables a live, working chat.
    //
    // Use the suite's own canonical deep-link helper (fixtures/session-setup.ts)
    // rather than a bare `page.goto` + composer-enabled check: a bare
    // visible/enabled wait on the composer is satisfied by the PREVIOUS
    // route's composer during the swap (the page is still on `/` from the
    // top of this test) — typing at that instant sends against a stale/null
    // session binding and no send frame ever reaches the wire. This is the
    // exact, previously-diagnosed root cause documented in that helper
    // ("root cause of the replay-fidelity (c) mid-turn attach flake") and
    // reproduced live here: a bare-goto version of this test hung the full
    // 60s timeout with zero send frame on the WS trace roughly 1 in 3 runs.
    // `openSessionByDeepLink` adds the missing route-swap guard
    // (`[data-active-session-id="$id"]`) before checking the composer at all.
    await openSessionByDeepLink(page, sessionId)

    // Assert: URL must now be /sessions/$sessionId (not /)
    await expect(page).toHaveURL(new RegExp(`sessions/${sessionId}`), { timeout: 10_000 })

    const chatInput = page.locator('[data-testid="chat-input"]').first()

    // Send a message and assert a streamed assistant token appears
    await chatInput.fill('Say exactly: "ping"')
    await chatInput.press('Enter')

    // Wait for at least one assistant message to appear with a streamed token
    const assistantMsg = page.locator('[data-message-role="assistant"]').first()
    await expect(assistantMsg).toBeVisible({ timeout: 60_000 })
  },
)

// ── (b) Failed send → error bubble + Retry ────────────────────────────────────

// Own gateway, own cookies, a FRESH Jim chat and a scripted model edge (#1792).
// The old version drove the shared gateway's persistent Mia main session with a
// live model: the resent text sometimes started a long tool-using turn that had
// not finished in 90 s, and every retry attempt then saw the earlier attempts'
// transcript, so an exact reply count could never match. The product path under
// test (the failed-send recovery of #253 and Retry's resend on the live socket)
// is unchanged and real; only the paid model is scripted. Whether a live model
// answers a resent message is NOT covered here (a known limit, see #1792).
test.describe(() => {
  test.describe.configure({ retries: 0 })
  test.use({ storageState: { cookies: [], origins: [] } })

  test(
    '(b) one-shot send failure marks the message status:error and Retry resends it on the live socket',
    async ({ page }, info) => {
      test.setTimeout(120_000)

      // Distinctive marker carried in the 2nd message so the wrapper can fail
      // EXACTLY that one send() and nothing else (auth, ping, msg #1, the retry).
      const FAIL_MARKER = 'ONESHOT_SEND_FAILURE_TRIGGER'
      const FIRST = 'Reply with just the single word: ready.'
      const SECOND = `second message ${FAIL_MARKER}`

      // Realistic #253 model: WRAP the real WebSocket rather than replacing it. The
      // socket genuinely connects to the gateway and a real session is created — we
      // only make ONE later send() throw (a transient hiccup on an OPEN socket).
      // The one-shot is armed via window.__failOnceMatch right before the 2nd send
      // and consumes itself on the throw, so Retry resends successfully on the
      // same healthy socket.
      await page.addInitScript(() => {
        const RealWebSocket = window.WebSocket
        // Disarmed until the test arms it right before the 2nd message.
        ;(window as unknown as Record<string, unknown>).__failOnceMatch = null
        class OneShotFailWebSocket extends RealWebSocket {
          // Parameter type is taken straight from the base method rather than
          // hand-listed (the hand-written union included SharedArrayBuffer, which
          // WebSocket.send does NOT accept).
          send(data: Parameters<WebSocket['send']>[0]): void {
            const w = window as unknown as { __failOnceMatch?: string | null }
            if (w.__failOnceMatch && typeof data === 'string' && data.includes(w.__failOnceMatch)) {
              w.__failOnceMatch = null // one-shot — consume so the Retry resend passes
              throw new Error('stubbed one-shot OPEN-socket send failure (#253)')
            }
            super.send(data)
          }
        }
        window.WebSocket = OneShotFailWebSocket as unknown as typeof WebSocket
      })

      const provider = await startSendRetryProvider({ first: FIRST, second: SECOND })
      let gw: GatewayProcess | undefined
      try {
        gw = await GatewayProcess.start({ apiBase: provider.url })
        const agentsResult = await gw.apiFetch('GET', '/api/v1/agents')
        expect(agentsResult.status, 'the isolated gateway must expose its real roster').toBe(200)
        const jim = Agent.array().parse(agentsResult.body).find((agent) => agent.name === 'Jim')
        expect(jim, 'the isolated roster must contain Jim').toBeDefined()
        if (!jim) throw new Error('BLOCKED: Jim is required by open-in-chat (b)')
        const workspacesResult = await gw.apiFetch('GET', '/api/v1/workspaces')
        expect(workspacesResult.status, 'the isolated workspace must be readable').toBe(200)
        const workspace = Workspace.array().parse(workspacesResult.body).find((ws) => ws.name === DEFAULT_WORKSPACE_NAME)
        expect(workspace, 'the isolated gateway must seed its default workspace').toBeDefined()
        if (!workspace) throw new Error('BLOCKED: a workspace is required by open-in-chat (b)')
        const created = await gw.apiFetch('POST', '/api/v1/sessions', {
          type: 'chat', agent_id: jim.id, workspace_id: workspace.id,
        } satisfies SessionCreateRequest)
        expect(created.status, 'create a new Jim chat, not a shared main session').toBe(201)
        const session = Session.parse(created.body)
        await page.context().addCookies((await gw.browserStorageState()).cookies)
        await openSessionByDeepLink(page, session.id, gw.baseURL)
        await waitForConnected(page, { timeout: 15_000 })

        // A fresh chat: nothing inherited from any earlier attempt or test.
        await expect(userMessages(page)).toHaveCount(0)
        await expect(assistantMessages(page)).toHaveCount(0)

        const chatInput = page.locator('[data-testid="chat-input"]').first()
        await expect(chatInput).toBeEnabled({ timeout: 15_000 })

        // Step 1: a normal first message; the scripted reply must complete.
        await chatInput.fill(FIRST)
        await chatInput.press('Enter')
        await expect(assistantMessages(page)).toHaveCount(1, { timeout: 15_000 })
        await expect(assistantMessages(page).getByText(SEND_RETRY_FIRST_REPLY, { exact: true })).toBeVisible({ timeout: 15_000 })
        await expect(chatInput).toBeEnabled({ timeout: 15_000 })
        await waitForConnected(page, { timeout: 15_000 })

        // Step 2: arm the one-shot, then send the second message. Its send()
        // throws exactly once, so the message never reaches the gateway.
        await page.evaluate((marker: string) => {
          ;(window as unknown as Record<string, unknown>).__failOnceMatch = marker
        }, FAIL_MARKER)
        await chatInput.fill(SECOND)
        await chatInput.press('Enter')

        // Step 3: the failed message MUST surface as an error bubble with a Retry.
        const erroredMsg = page.locator('[data-message-role="user"][data-status="error"]')
        await expect(erroredMsg).toBeVisible({ timeout: 15_000 })
        const retryBtn = page.locator('[data-testid="user-message-retry"]').first()
        await expect(retryBtn).toBeVisible({ timeout: 5_000 })
        expect(provider.evidence.streamRequests, 'the failed send must not have reached the model').toBe(1)

        // Step 4: Retry resends the same text on the SAME open socket (the old
        // errored bubble intentionally stays). Recovery is proven by the second
        // scripted reply: exactly two completed assistant messages, the last one
        // the resend's.
        await retryBtn.click()
        await expect(assistantMessages(page)).toHaveCount(2, { timeout: 15_000 })
        await expect(assistantMessages(page).getByText(SEND_RETRY_RESEND_REPLY, { exact: true })).toBeVisible({ timeout: 15_000 })
        await expect(chatInput).toBeEnabled({ timeout: 15_000 })
        expect(provider.evidence.streamRequests, 'one request for msg #1, one for the single resend').toBe(2)
        expect(provider.evidence.lastUserTexts[1]).toContain(FAIL_MARKER)
        expect(provider.evidence.errors, 'no unexpected scripted-provider traffic').toEqual([])
      } finally {
        try {
          await info.attach('send-retry-provider-evidence', {
            body: JSON.stringify(provider.evidence, null, 2), contentType: 'application/json',
          })
          if (gw) await info.attach('send-retry-gateway', { body: gw.outputTail(), contentType: 'text/plain' })
        } finally {
          try { await gw?.stop() }
          finally { await provider.close() }
        }
      }
    },
  )
})
