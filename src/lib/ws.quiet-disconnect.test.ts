/**
 * ws.quiet-disconnect.test.ts — WsConnection close-handling must stay quiet
 * on a recoverable abnormal close (#823 spec item 3).
 *
 * Spec source: issue #823 (elicify-ai/omnipus), founder comment 2026-09-23,
 * "Gap found in a real browser: the old technical banner is still on the
 * error path" — https://github.com/elicify-ai/omnipus/issues/823#issuecomment
 *   "No visible text during a drop contains 'gateway', 'Disconnected from
 *    gateway', or a close code like '1006'."
 * and the parent issue body, section "D. Deliberately invisible" /
 * "Binding rules" (no internal vocabulary in user-facing connection text).
 *
 * `WsConnection.onError` (src/lib/ws.ts) is the ONLY channel the production
 * wiring (`OmnipusRuntimeProvider.tsx`'s `WsLifecycle`, `onError:
 * setConnectionError`) uses to set `useConnectionStore`'s `connectionError` —
 * the field that drives the AppShell alert banner. So the oracle for this
 * file is: whatever `onError` is called with (if it is called at all) during
 * a recoverable abnormal-close → reconnect → give-up cycle must never carry
 * the banned internal vocabulary. This is necessary (not sufficient — the
 * store/AppShell integration is covered separately in
 * src/store/connection.quiet-disconnect.test.ts and
 * src/components/layout/AppShell.quiet-disconnect.test.tsx) for spec item 1
 * (`connectionError` stays null) to hold under the current 1:1 wiring.
 *
 * Close code 1008 (policy violation) is a GENUINE error (forceLogout) and is
 * deliberately untouched — see src/lib/ws.1008.test.ts. This file only
 * covers recoverable/abnormal closes (1006 is the RFC 6455 canonical
 * example and the one named explicitly in the issue).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { WsConnection } from './ws'

// ── Mock WebSocket (mirrors src/lib/ws.1008.test.ts's harness) ──────────────

let lastWsInstance: {
  onopen: (() => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  readyState: number
}

const MockWebSocket = vi.fn(function () {
  const instance = {
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
    send: vi.fn(),
    close: vi.fn(),
    readyState: 1, // OPEN
  }
  lastWsInstance = instance
  return instance
}) as unknown as typeof WebSocket & {
  OPEN: number
  CLOSED: number
  mockClear: () => void
  mock: { calls: unknown[][] }
}

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

beforeEach(() => {
  MockWebSocket.mockClear()
  vi.stubGlobal('WebSocket', MockWebSocket)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function makeCallbacks() {
  return {
    onFrame: vi.fn(),
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
    onError: vi.fn(),
    onReconnectStateChange: vi.fn(),
  }
}

// Banned internal vocabulary, per issue #823's binding rule and the D5
// sanitizer precedent already established for legacy error frames
// (src/store/chat.llm-error.test.ts's "D5: sanitizes a Go-internal-jargon-
// shaped frame.message" tests) — no raw close-code or "gateway" jargon may
// reach the user.
const BANNED_PATTERNS = [/gateway/i, /disconnected from/i, /\b1006\b/, /\bcode\s+\d{3,4}\b/i]

function assertNoBannedText(message: string): void {
  for (const pattern of BANNED_PATTERNS) {
    expect(message, `onError message "${message}" must not match ${pattern}`).not.toMatch(pattern)
  }
}

describe('WsConnection — quiet close handling for a recoverable abnormal close (#823 item 3)', () => {
  it('does not call onError with gateway/close-code wording on the FIRST abnormal close (1006)', () => {
    vi.useFakeTimers()
    const cbs = makeCallbacks()
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })

    for (const call of cbs.onError.mock.calls) {
      assertNoBannedText(String(call[0]))
    }

    conn.disconnect()
    vi.useRealTimers()
  })

  it('never calls onError with gateway/close-code wording across the FULL reconnect schedule through give-up', () => {
    vi.useFakeTimers()
    let gaveUp = false
    const cbs = {
      onFrame: vi.fn(),
      onConnected: vi.fn(),
      onDisconnected: vi.fn(),
      onError: vi.fn(),
      onReconnectStateChange: vi.fn((phase: 'reconnecting' | 'slow' | 'gave_up' | null) => {
        if (phase === 'gave_up') gaveUp = true
      }),
    }
    const conn = new WsConnection(cbs)
    conn.connect()
    lastWsInstance.onopen?.()

    // Keep failing every reconnect attempt (fresh abnormal close on each new
    // socket) until the reconnect state machine reports 'gave_up'. The exact
    // attempt counts (10 fast + 20 slow) are documented in ws.ts's own
    // module comment above _scheduleReconnect but are NOT exported — driving
    // by the observable onReconnectStateChange('gave_up', …) signal (rather
    // than re-typing those counts) keeps this test honest to the public
    // contract. SAFETY_CAP is a generous ceiling so a regression that never
    // gives up fails loudly instead of hanging.
    const SAFETY_CAP = 60
    for (let i = 0; i < SAFETY_CAP && !gaveUp; i++) {
      lastWsInstance.onclose?.({ code: 1006, reason: 'abnormal' })
      vi.advanceTimersByTime(65_000)
    }

    expect(gaveUp, 'the reconnect schedule must actually reach gave_up within the safety cap').toBe(true)
    // No assertion that onError WAS called — spec item 3 only bans specific
    // wording, it does not mandate that onError fire at all for a
    // recoverable close. Zero calls trivially satisfies "never banned text".
    for (const call of cbs.onError.mock.calls) {
      assertNoBannedText(String(call[0]))
    }

    conn.disconnect()
    vi.useRealTimers()
  })
})
