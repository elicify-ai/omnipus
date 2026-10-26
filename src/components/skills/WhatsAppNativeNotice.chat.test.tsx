/**
 * WhatsAppNativeNotice.chat.test.tsx — the pairing QR in the REQUESTING chat
 * (Train 2, WU-7). LEAN pack: exactly the dispatched rows.
 *
 * Oracles (spec docs/internal/specs/omnipus-system-agent-spec.md):
 *   BDD-8.4  — the code and progress render in Omnipus's chat, correlated by
 *              request id.
 *   BDD-8.5  — timeout: "saved but not working: pairing timed out" state.
 *   BDD-8.8  — Stop ends pairing with "stopped"; a reconnect re-subscribes to
 *              the same request and sees current progress.
 *   FR-081   — QR pairing renders in the requesting chat, correlated by
 *              request and session id, with timeout, Stop and reconnect
 *              handling.
 *   K10      — WhatsAppPairingFrame/SubscribeFrame gain optional request_id
 *              and session_id (status now includes "stopped").
 *   Train 2 round 2 — arch-retry-contract.md::Decision moves the explicit
 *              Retry ownership claim to REST enable. Subscriptions only
 *              observe; a refused enable leaves the channel disabled.
 * Approved plan (frontend dev): WhatsAppNativeNotice gains optional
 * requestId/sessionId props sent on the subscribe frame; the store keeps
 * request_id and session_id from frames; subscription lifetime is
 * ref-counted per channel.
 *
 * REUSE rule (founder): the chat mounts the SAME WhatsAppNativeNotice and
 * the SAME whatsappPairing store Connectors uses — these tests drive that
 * one real component and the one real store (only the connection send edge
 * is doubled).
 *
 * RED at 55d0ddd4d: the notice has no requestId/sessionId props (the
 * subscribe frame cannot carry them), the store keeps no request identity,
 * and no stopped state exists.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, waitFor, fireEvent } from '@testing-library/react'
import { disableChannel, enableChannel } from '@/lib/api'
import type { WhatsAppPairingFrame } from '@/lib/api/generated/asyncapi-types'

const { sendSpy, connectionState } = vi.hoisted(() => {
  const connectionState = {
    isConnected: true,
    connection: { send: vi.fn() },
  }
  return { sendSpy: connectionState.connection.send, connectionState }
})
vi.mock('@/lib/api', () => ({
  disableChannel: vi.fn(async () => ({})),
  enableChannel: vi.fn(async () => ({})),
}))
vi.mock('@/store/connection', () => {
  const hook = vi.fn((selector: (s: typeof connectionState) => unknown) => selector(connectionState))
  ;(hook as unknown as { getState: () => typeof connectionState }).getState = () => connectionState
  return { useConnectionStore: hook }
})

import { WhatsAppNativeNotice } from './WhatsAppNativeNotice'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'

const CHANNEL = 'whatsapp'
const REQ_A = 'toolcall-aaa'
const SESSION = 'omnipus-main'

function pairingFrame(status: WhatsAppPairingFrame['status'], requestId: string, over: Partial<WhatsAppPairingFrame> = {}): WhatsAppPairingFrame {
  return {
    type: 'whatsapp_pairing', channel_id: CHANNEL, status,
    request_id: requestId, session_id: SESSION,
    ...(status === 'code' ? { qr: 'wa-pairing-secret' } : {}),
    ...over,
  }
}

function apply(frame: WhatsAppPairingFrame): void {
  act(() => useWhatsAppPairingStore.getState().apply(frame))
}

function subscribeCalls(): Array<{ channel_id: string; active: boolean; request_id?: string; session_id?: string }> {
  return sendSpy.mock.calls
    .map(([frame]) => frame)
    .filter((frame: { type?: string }) => frame?.type === 'whatsapp_pairing_subscribe')
}

function setConnected(connected: boolean): void {
  act(() => {
    connectionState.isConnected = connected
    connectionState.connection = { send: sendSpy }
  })
}

function mountNotice(props: { channelId: string; requestId?: string; sessionId?: string }) {
  // The planned requestId/sessionId props do not exist at RED; the cast lets
  // this compile while the component ignores them (the failures below are
  // the missing behaviour, not a type error).
  return render(<WhatsAppNativeNotice {...(props as React.ComponentProps<typeof WhatsAppNativeNotice>)} />)
}

beforeEach(() => {
  sendSpy.mockClear()
  connectionState.isConnected = true
  act(() => useWhatsAppPairingStore.setState({ byChannel: {} }))
})

describe('Row 1 — BDD-8.4: the QR renders in the requesting chat, correlated by request id', () => {
  it('subscribes with channel_id, active, request_id and session_id; a code frame with THAT request_id renders the QR', async () => {
    mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })

    // The subscribe frame carries the correlation identity (K10).
    await waitFor(() => expect(subscribeCalls()).toHaveLength(1))
    expect(subscribeCalls()[0]).toMatchObject({
      channel_id: CHANNEL, active: true, request_id: REQ_A, session_id: SESSION,
    })

    apply(pairingFrame('code', REQ_A))
    expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
  })

  it('Row 2 — correlation: a code frame for a DIFFERENT request_id does not render in this card', () => {
    mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })
    apply(pairingFrame('code', 'toolcall-other'))

    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })
})

describe('Row 3 — BDD-8.5: the timeout state, correlated', () => {
  it('a timeout frame with this request_id shows the timeout state with Retry; a different request_id does not', () => {
    mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })

    // Different request_id: no timeout state leaks into this card.
    apply(pairingFrame('timeout', 'toolcall-other', { message: 'saved but not working: pairing timed out' }))
    expect(screen.queryByText(/pairing timed out/)).not.toBeInTheDocument()

    // Matching request_id: the timeout state with Retry renders.
    apply(pairingFrame('timeout', REQ_A, { message: 'saved but not working: pairing timed out' }))
    expect(screen.getByText(/pairing timed out/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})

describe('Row 4 (notice half) — BDD-8.8: a stopped frame shows a stopped state in the chat', () => {
  it('a stopped frame with this request_id renders a stopped state — no spinner, no QR', () => {
    mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })
    apply(pairingFrame('stopped', REQ_A, { message: 'Pairing stopped.' }))

    expect(screen.getByText(/stopped/i)).toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    // No waiting spinner for a terminal stopped state.
    expect(document.querySelector('.animate-spin')).toBeNull()
  })
})

describe('Row 5 — BDD-8.8: reconnect re-subscribes with the SAME request id', () => {
  it('after a socket drop and reconnect the subscribe is re-sent with the same request_id and session_id', async () => {
    const view = mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })
    await waitFor(() => expect(subscribeCalls()).toHaveLength(1))

    // Socket drop, then reconnect. The mocked connection hook is not
    // reactive, so the rerender carries the new isConnected to the effect —
    // the same edge the real store's notify triggers.
    setConnected(false)
    view.rerender(<WhatsAppNativeNotice {...({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION } as React.ComponentProps<typeof WhatsAppNativeNotice>)} />)
    sendSpy.mockClear()
    setConnected(true)
    view.rerender(<WhatsAppNativeNotice {...({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION } as React.ComponentProps<typeof WhatsAppNativeNotice>)} />)

    await waitFor(() => expect(subscribeCalls().length).toBeGreaterThanOrEqual(1))
    const last = subscribeCalls()[subscribeCalls().length - 1]
    expect(last).toMatchObject({ active: true, request_id: REQ_A, session_id: SESSION })
  })
})

describe('Row 6 — two mount sites: ref-counted subscription per channel', () => {
  it('closing ONE of two mounted notices keeps the other subscribed; only the last close unsubscribes', () => {
    const chat = mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })
    const connectors = mountNotice({ channelId: CHANNEL })

    sendSpy.mockClear()
    chat.unmount()

    expect(
      subscribeCalls().filter((frame) => frame.active === false),
      'closing one site must NOT unsubscribe while the other is open',
    ).toEqual([])

    connectors.unmount()
    const unsubscribe = subscribeCalls().filter((frame) => frame.active === false)
    expect(unsubscribe).toHaveLength(1)
    expect(unsubscribe[0]).toMatchObject({ channel_id: CHANNEL, active: false })
  })
})

// Chief-authorized replacements: arch-retry-contract.md::Decision makes
// REST enable the explicit ownership claim; subscriptions only observe.
describe('Retry ordering — explicit REST claim, observation-only subscription (Train 2 round 2)', () => {
  it('a Retry click disables then sends the exact claim on REST enable without a subscribe toggle', async () => {
    mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })
    apply(pairingFrame('timeout', REQ_A, { message: 'saved but not working: pairing timed out' }))

    // Only post-click traffic counts: the mount-time subscribe is cleared away.
    sendSpy.mockClear()
    vi.mocked(enableChannel).mockClear()
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(enableChannel).toHaveBeenCalledTimes(1))

    expect(enableChannel).toHaveBeenCalledExactlyOnceWith(CHANNEL, {
      pairing_retry: { request_id: REQ_A, session_id: SESSION },
    })
    expect(vi.mocked(disableChannel).mock.invocationCallOrder.at(-1)).toBeLessThan(
      vi.mocked(enableChannel).mock.invocationCallOrder[0],
    )
    expect(sendSpy.mock.calls).toEqual([])
  })

  it('a failed enable shows the exact disabled-state alert without toggling observation interest', async () => {
    mountNotice({ channelId: CHANNEL, requestId: REQ_A, sessionId: SESSION })
    apply(pairingFrame('timeout', REQ_A, { message: 'saved but not working: pairing timed out' }))
    vi.mocked(enableChannel).mockRejectedValueOnce(new Error('reload failed'))

    sendSpy.mockClear()
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))

    // A refusal does not roll back the confirmed disable. Keep observing;
    // ownership is never granted by a subscription (architect ruling).
    expect((await screen.findByRole('alert')).textContent).toBe(
      'The channel was disabled before the enable request, but its current state could not be confirmed. Could not restart pairing. Open the WhatsApp connector and try again.Retry',
    )
    expect(screen.queryByText('Generating your QR code…')).not.toBeInTheDocument()
    expect(sendSpy.mock.calls).toEqual([])
  })
})
