/**
 * stop-scope-tree.spec.ts — ADR-20260928 D9 + MAJ-002: web Stop/Esc//cancel
 * surface scoping, asserted on the REAL WebSocket wire in a real browser.
 *
 * STATUS (2026-10-02, RED author): WRITTEN, NOT RUN. The vitest packs carry
 * the frontend RED evidence (ChatScreen.stop-scope.test.tsx,
 * Sidebar.lifecycle-labels.test.tsx); this spec is the browser-level
 * counterpart for the e2e CI shard and has never been executed locally — the
 * local machine has no live gateway/OpenRouter, and common.md forbids bare
 * Playwright runs. First CI run may need frame-shape adjustments if the
 * streaming handshake below drifts from the real gateway.
 * Fixture repair (2026-10-06): exact-session HTTP history and a correlated
 * saved-message acknowledgement added after CI setup failures. All original
 * Stop-scope assertions remain. Browser execution is still delegated to CI.
 *
 * Approach — fully deterministic, no live model:
 *   - page.routeWebSocket() fully mocks the chat WS transport (established
 *     pattern: whatsapp-qr.spec.ts, reconnect-mid-turn.spec.ts).
 *   - The mock answers the client's first `message` frame with
 *     `session_started` + a `token` stream and never sends `done`, so the
 *     turn is mid-stream for the whole test (stop button visible).
 *   - Every client→server frame is captured in Node; the assertions read the
 *     captured CANCEL FRAMES — the process edge, not DOM internals. Scope
 *     values come from the generated CancelFrame schema (scope enum
 *     "session"|"tree", optional).
 *
 * The contract under test (D9 surface table):
 *   - single Stop press            → cancel frame WITHOUT scope "tree"
 *   - first press keeps the SAME Stop button in place for the 3 s window
 *     (no separate Stop-all button — founder 2026-10-06, Q16 = A)
 *   - second Esc within 3 s        → cancel frame with scope "tree"
 *   - after the window expires the next press is session-scoped again
 *   - /cancel command              → cancel frame with scope "tree"
 *
 * Traces to: ADR-20260928 D9, MAJ-002, T21/T26; common.md decided behaviour.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { WebSocketRoute } from '@playwright/test'
import { expect, test } from '@playwright/test'
import { chatInput, waitForConnected } from './fixtures/selectors'

import type { ClientFrame, CancelFrame, SessionStartedFrame, TokenFrame } from '../../src/lib/api/generated/asyncapi-types'
import type { Message as WireMessage } from '../../src/lib/api/generated/openapi-types'

type CapturedFrame = ClientFrame

const E2E_SESSION_ID = 'sess-e2e-stop-scope'
const E2E_TRIGGER_TEXT = 'Trigger a turn for the stop-scope test'
const E2E_STREAMED_TEXT = 'Stop-scope e2e turn running.'

/**
 * Installs the WS mock and starts collecting client→server frames.
 *
 * Server-side script: on the first `message` frame, ack with session_started
 * and stream a few tokens; never send done. auth/ping are ignored (the SPA
 * does not need acks to function — whatsapp-qr.spec.ts precedent).
 */
async function mockChatWebSocket(
  page: import('@playwright/test').Page,
  sentToServer: CapturedFrame[],
): Promise<void> {
  // session_started promises a durably saved first message. Its synthetic
  // session therefore needs matching HTTP history, not a real-gateway 404.
  const savedHistory: WireMessage[] = []
  await page.route(`**/api/v1/sessions/${E2E_SESSION_ID}/messages`, (route) => route.fulfill({ status: 200, json: savedHistory }))
  await page.routeWebSocket(/api\/v1\/chat\/ws/, (ws: WebSocketRoute) => {
    let turnStarted = false
    ws.onMessage((raw: string | Buffer) => {
      let frame: CapturedFrame
      try {
        frame = JSON.parse(typeof raw === 'string' ? raw : raw.toString())
      } catch (error) {
        throw new Error('Stop-scope fixture received malformed client JSON', { cause: error })
      }
      sentToServer.push(frame)

      if (frame.type === 'message' && !turnStarted) {
        expect(frame.content).toBe(E2E_TRIGGER_TEXT)
        if (!frame.client_message_id) throw new Error('Stop-scope first message is missing its delivery ID')
        turnStarted = true
        savedHistory.push({
          id: frame.client_message_id, client_message_id: frame.client_message_id,
          role: 'user', content: E2E_TRIGGER_TEXT, status: 'ok',
          agent_id: frame.agent_id ?? 'jim', timestamp: new Date().toISOString(),
        })
        const ack: SessionStartedFrame = { type: 'session_started', session_id: E2E_SESSION_ID, client_message_id: frame.client_message_id }
        ws.send(JSON.stringify(ack))
        for (const chunk of ['Stop', '-scope ', 'e2e ', 'turn ', 'running.']) {
          const token: TokenFrame = { type: 'token', session_id: E2E_SESSION_ID, content: chunk }
          ws.send(JSON.stringify(token))
        }
        // Deliberately NO done frame — the turn stays mid-stream.
      }
    })
  })
}

function cancelFrames(sentToServer: CapturedFrame[]): CancelFrame[] {
  return sentToServer.filter((f) => f.type === 'cancel')
}

async function startStreamingTurn(page: import('@playwright/test').Page): Promise<void> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await input.fill(E2E_TRIGGER_TEXT)
  await input.press('Enter')
  // Mid-stream confirmation: the Stop button replaced Send.
  await expect(page.getByTestId('stop-btn')).toBeVisible({ timeout: 15_000 })
  await expect(page.locator('[data-message-id]').first()).toBeVisible({ timeout: 15_000 })
  // A visible optimistic Stop alone is insufficient: the real stream must
  // have arrived, and the matching history route must not replace the thread.
  await expect(page.getByText(E2E_STREAMED_TEXT, { exact: true })).toBeVisible({ timeout: 15_000 })
}

test.describe('Stop/Esc//cancel surface scoping (ADR-20260928 D9)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('single Stop press sends one session-scoped cancel frame and the same Stop button stays for the window', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    await mockChatWebSocket(page, sentToServer)
    await page.goto('/')
    await startStreamingTurn(page)

    await page.getByTestId('stop-btn').click()

    // Founder 2026-10-06: no separate Stop-all button. The SAME Stop button
    // stays in place so a second click inside the 3 s window is possible.
    await expect(page.getByTestId('stop-btn')).toBeVisible()
    await expect(page.getByText(/stop all/i)).toHaveCount(0)

    // And the wire saw exactly one cancel frame, session-scoped (no tree).
    const cancels = cancelFrames(sentToServer)
    expect(cancels).toHaveLength(1)
    expect(cancels[0].session_id).toBeTruthy()
    expect(cancels[0].scope).not.toBe('tree')
  })

  test('second Esc within 3s confirms stop-all — cancel frame carries scope tree', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    await mockChatWebSocket(page, sentToServer)
    await page.goto('/')
    await startStreamingTurn(page)

    const input = chatInput(page)
    await input.press('Escape')
    // Second Esc inside the confirmation window (well under 3 s).
    await input.press('Escape')

    await expect
      .poll(() => cancelFrames(sentToServer).some((f) => f.scope === 'tree'), { timeout: 5_000 })
      .toBe(true)
  })

  test('after the confirmation window expires the next Stop press is session-scoped again', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    await mockChatWebSocket(page, sentToServer)
    await page.goto('/')
    await startStreamingTurn(page)

    await page.getByTestId('stop-btn').click()
    await expect(page.getByTestId('stop-btn')).toBeVisible()
    expect(cancelFrames(sentToServer)).toHaveLength(1)

    // Past the 3-second window (real time) a fresh activation is a new FIRST
    // press: session-scoped, never a silent tree stop.
    await page.waitForTimeout(3_500)
    await chatInput(page).press('Escape')

    await expect
      .poll(() => cancelFrames(sentToServer).length, { timeout: 5_000 })
      .toBe(2)
    const cancels = cancelFrames(sentToServer)
    expect(cancels[0].scope).not.toBe('tree')
    expect(cancels[1].scope, 'a press after the window closed must not be a tree stop').not.toBe('tree')
    expect(cancels[1].session_id).toBe(E2E_SESSION_ID)
  })

  test('/cancel sends one cancel frame with scope tree', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    await mockChatWebSocket(page, sentToServer)
    await page.goto('/')
    await startStreamingTurn(page)

    // /cancel is available while streaming; select it from the slash menu.
    const input = chatInput(page)
    await input.fill('/')
    await page.getByText('/cancel').click()

    await expect
      .poll(() => cancelFrames(sentToServer), { timeout: 5_000 })
      .toHaveLength(1)
    expect(cancelFrames(sentToServer)[0].scope).toBe('tree')
    expect(cancelFrames(sentToServer)[0].session_id).toBe(E2E_SESSION_ID)
  })
})
