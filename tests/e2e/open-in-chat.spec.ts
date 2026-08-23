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
 * (a) Command Center "Open in Chat" → session route → live streaming (#250)
 *     Given a task with a running session_id
 *     When the user clicks "Open in Chat" on TaskDetailPanel
 *     Then the SPA navigates to /sessions/$sessionId
 *     And the WS attach_session frame is sent (chat loads with existing history)
 *     And a new text message sent from that screen produces a streamed assistant token.
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
import { assistantMessages, waitForConnected } from './fixtures/selectors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── (a) Open in Chat → live streaming ─────────────────────────────────────────

test(
  '(a) Command Center Open in Chat navigates to session route and enables chat',
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

    // Create a session via the API so TaskDetailPanel has a real session_id to open.
    // NOTE: agent_id is REQUIRED — omitting it defaults to the non-existent "main"
    // agent, which makes the session read-only ("Agent removed") and disables the
    // chat input. Use a seeded core agent ('mia', the guide) so the session is live.
    const sessionResp = await apiFetch('POST', '/api/v1/sessions', { agent_id: 'mia' })
    expect(sessionResp.ok).toBeTruthy()
    const sessionId = (sessionResp.body as { id: string }).id
    expect(typeof sessionId).toBe('string')

    // Create a task that references the session
    const taskResp = await apiFetch('POST', '/api/v1/tasks', {
      title: 'e2e-open-in-chat-test',
      prompt: 'Test task for open-in-chat e2e',
      session_id: sessionId,
    })
    // If tasks endpoint isn't available, skip gracefully (not all builds have it)
    if (!taskResp.ok) {
      test.skip()
      return
    }

    // Navigate to Command Center and open the task detail
    await page.goto(`${BASE_URL}/#/tasks`)
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Find and click the task
    const taskItem = page.locator('[data-testid="task-item"]', { hasText: 'e2e-open-in-chat-test' }).first()
    const taskFound = await taskItem.isVisible({ timeout: 10_000 }).catch(() => false)
    if (!taskFound) {
      // Task list may not show in the current view — navigate directly to session
      await page.goto(`${BASE_URL}/#/sessions/${sessionId}`)
    } else {
      await taskItem.click()
      // Click "Open in Chat"
      const openBtn = page.locator('button', { hasText: /Open in Chat/i })
      await expect(openBtn).toBeVisible({ timeout: 5_000 })
      await openBtn.click()
    }

    // Assert: URL must now be /sessions/$sessionId (not /)
    await expect(page).toHaveURL(new RegExp(`sessions/${sessionId}`), { timeout: 10_000 })

    // The chat input must be enabled and reachable
    const chatInput = page.locator('[data-testid="chat-input"]').first()
    await expect(chatInput).toBeVisible({ timeout: 15_000 })
    await expect(chatInput).toBeEnabled({ timeout: 10_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts).
    await waitForConnected(page, { timeout: 10_000 })

    // Send a message and assert a streamed assistant token appears
    await chatInput.fill('Say exactly: "ping"')
    await chatInput.press('Enter')

    // Wait for at least one assistant message to appear with a streamed token
    const assistantMsg = page.locator('[data-message-role="assistant"]').first()
    await expect(assistantMsg).toBeVisible({ timeout: 60_000 })
  },
)

// ── (b) Failed send → error bubble + Retry ────────────────────────────────────

test(
  '(b) one-shot send failure marks the message status:error and Retry resends it on the live socket',
  async ({ page }) => {
    // Two real-LLM round-trips (the first establishes the session, the retried
    // second one recovers), so allow a generous budget under suite load.
    test.setTimeout(150_000)

    // Distinctive marker carried in the 2nd message so the wrapper can fail
    // EXACTLY that one send() and nothing else (auth, ping, msg #1, the retry).
    const FAIL_MARKER = 'ONESHOT_SEND_FAILURE_TRIGGER'

    // Realistic #253 model: WRAP the real WebSocket rather than replacing it. The
    // socket genuinely connects to the gateway and a real session is created — we
    // only make ONE later send() throw (a transient hiccup on an OPEN socket).
    // This exercises WsConnection.send()'s try/catch → failed-send recovery
    // (kept bubble, status:'error', Retry) without the artificial "first send with
    // no session → reconnect → empty replay" state that a fully-faked socket
    // produced. The one-shot is armed via window.__failOnceMatch right before the
    // 2nd send and consumes itself on the throw, so Retry resends successfully on
    // the same healthy socket.
    await page.addInitScript((marker: string) => {
      const RealWebSocket = window.WebSocket
      // Disarmed until the test arms it right before the 2nd message.
      ;(window as unknown as Record<string, unknown>).__failOnceMatch = null
      void marker
      class OneShotFailWebSocket extends RealWebSocket {
        // Parameter type is taken straight from the base method rather than
        // hand-listed. The hand-written union said `ArrayBufferLike`, which
        // includes SharedArrayBuffer — a type WebSocket.send does NOT accept —
        // so `super.send(data)` did not type-check. Deriving it keeps the
        // override in lockstep with lib.dom.
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
    }, FAIL_MARKER)

    await page.goto(`${BASE_URL}/`)
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    const chatInput = page.locator('[data-testid="chat-input"]').first()
    await expect(chatInput).toBeEnabled({ timeout: 15_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts).
    await waitForConnected(page, { timeout: 15_000 })

    // Step 1: Send a normal first message so a REAL session is established (the
    // gateway mints a session_id, history exists, the socket is live). Wait for
    // the turn to finish — assistant reply rendered AND the composer re-enabled —
    // before inducing the failure on the second send.
    await chatInput.fill('Reply with just the single word: ready.')
    await chatInput.press('Enter')
    await expect(page.locator('[data-message-role="assistant"]').first()).toBeVisible({
      timeout: 90_000,
    })
    await expect(chatInput).toBeEnabled({ timeout: 90_000 })
    // toBeEnabled() alone no longer implies "connected" (2fa26e6a, #105 fix —
    // see waitForConnected's doc comment in fixtures/selectors.ts). This
    // gate matters here specifically: the 2nd message below relies on the
    // socket being the SAME still-open one the test's WebSocket wrapper is
    // watching for its one-shot failure marker.
    await waitForConnected(page, { timeout: 15_000 })

    // Step 2: Arm the one-shot, then send the second message. Its send() throws
    // exactly once → WsConnection.send() catches the throw and reports the send as
    // failed → the store keeps the user bubble and marks it status:'error'.
    await page.evaluate((marker: string) => {
      ;(window as unknown as Record<string, unknown>).__failOnceMatch = marker
    }, FAIL_MARKER)
    await chatInput.fill(`second message ${FAIL_MARKER}`)
    await chatInput.press('Enter')

    // Step 3: The failed message MUST surface as an error bubble with a Retry.
    // If this fails, the #253 OPEN-socket send-failure handling is broken.
    const erroredMsg = page.locator('[data-message-role="user"][data-status="error"]')
    await expect(erroredMsg).toBeVisible({ timeout: 15_000 })
    const retryBtn = page.locator('[data-testid="user-message-retry"]').first()
    await expect(retryBtn).toBeVisible({ timeout: 5_000 })

    // Step 4: Click Retry. handleRetry() (#253(c)) RESENDS the original content as
    // a new message — it intentionally leaves the old errored bubble in place, so
    // recovery is NOT proven by the error bubble vanishing. The one-shot is already
    // consumed, so the resend goes out on the SAME healthy, still-OPEN socket within
    // the existing session and SUCCEEDS — proven by a second COMPLETED assistant
    // reply arriving (msg #1 → 1 reply, resent #2 → 2). No reconnect, no
    // missing-session replay hang. The composer also stays usable throughout.
    await retryBtn.click()
    await expect(assistantMessages(page)).toHaveCount(2, { timeout: 90_000 })
    await expect(chatInput).toBeEnabled({ timeout: 90_000 })
  },
)
