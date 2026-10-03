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
 *   - first press visibly offers the Stop-all confirmation
 *   - second Esc within 3 s        → cancel frame with scope "tree"
 *   - offer gone after the window expires
 *   - /cancel command              → cancel frame with scope "tree"
 *
 * Traces to: ADR-20260928 D9, MAJ-002, T21/T26; common.md decided behaviour.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)"
 */

import type { WebSocketRoute } from '@playwright/test'
import { expect, test } from '@playwright/test'
import { chatInput, waitForConnected } from './fixtures/selectors'

interface CapturedFrame {
  type?: string
  session_id?: string
  scope?: 'session' | 'tree'
}

const E2E_SESSION_ID = 'sess-e2e-stop-scope'

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
  await page.routeWebSocket(/api\/v1\/chat\/ws/, (ws: WebSocketRoute) => {
    let turnStarted = false
    ws.onMessage((raw: string | Buffer) => {
      let frame: CapturedFrame & { content?: string }
      try {
        frame = JSON.parse(typeof raw === 'string' ? raw : raw.toString())
      } catch {
        return
      }
      sentToServer.push(frame)

      if (frame.type === 'message' && !turnStarted) {
        turnStarted = true
        ws.send(JSON.stringify({ type: 'session_started', session_id: E2E_SESSION_ID }))
        for (const chunk of ['Stop', '-scope ', 'e2e ', 'turn ', 'running.']) {
          ws.send(JSON.stringify({ type: 'token', session_id: E2E_SESSION_ID, content: chunk }))
        }
        // Deliberately NO done frame — the turn stays mid-stream.
      }
    })
  })
}

function cancelFrames(sentToServer: CapturedFrame[]): CapturedFrame[] {
  return sentToServer.filter((f) => f.type === 'cancel')
}

async function startStreamingTurn(page: import('@playwright/test').Page): Promise<void> {
  const input = chatInput(page)
  await expect(input).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page)
  await input.fill('Trigger a turn for the stop-scope test')
  await input.press('Enter')
  // Mid-stream confirmation: the Stop button replaced Send.
  await expect(page.getByTestId('stop-btn')).toBeVisible({ timeout: 15_000 })
  await expect(page.locator('[data-message-id]').first()).toBeVisible({ timeout: 15_000 })
}

test.describe('Stop/Esc//cancel surface scoping (ADR-20260928 D9)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
  })

  test('single Stop press sends one cancel frame without tree scope and shows the Stop-all offer', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    await mockChatWebSocket(page, sentToServer)
    await page.goto('/')
    await startStreamingTurn(page)

    await page.getByTestId('stop-btn').click()

    // D9: the first press VISIBLY offers the Stop-all confirmation.
    await expect(page.getByText(/stop all/i)).toBeVisible({ timeout: 3_000 })

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

  test('Stop-all offer disappears once the confirmation window expires', async ({ page }) => {
    test.setTimeout(90_000)
    const sentToServer: CapturedFrame[] = []
    await mockChatWebSocket(page, sentToServer)
    await page.goto('/')
    await startStreamingTurn(page)

    await page.getByTestId('stop-btn').click()
    await expect(page.getByText(/stop all/i)).toBeVisible({ timeout: 3_000 })

    // Past the 3-second window the offer is gone; a fresh activation would be
    // a new FIRST press (session-scoped), never a silent stop-all.
    await expect(page.getByText(/stop all/i)).toBeHidden({ timeout: 6_000 })
    expect(cancelFrames(sentToServer).some((f) => f.scope === 'tree')).toBe(false)
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
