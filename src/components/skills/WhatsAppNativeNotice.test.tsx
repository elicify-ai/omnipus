/**
 * WhatsAppNativeNotice.test.tsx — QR pairing regression tests.
 *
 * REGRESSION UNDER TEST: after the multi-instance change, the channel manager
 * keys channels — and therefore stamps `whatsapp_pairing` frames — by INSTANCE
 * id ("whatsapp", "whatsapp.sales"), not the old registry name
 * "whatsapp_native". The notice used to hard-subscribe and hard-read
 * "whatsapp_native", so QR frames never matched and the QR never rendered.
 * These tests pin the corrected contract:
 *   1. The QR renders when a frame is stored under the notice's OWN channelId,
 *      for both the bare and namespaced instance forms.
 *   2. Another instance's frame does NOT leak into this notice (isolation).
 *   3. Subscribe interest is registered/cleared with the instance id.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'

// Connection store: expose a spyable `send` + getState (the notice calls
// useConnectionStore.getState() directly in unmount cleanup). vi.hoisted —
// vi.mock factories are hoisted above const initializers.
const { sendSpy, clearSpy, pairing } = vi.hoisted(() => {
  const pairing: { byChannel: Record<string, { status: string; qr: string; message: string }> } = { byChannel: {} }
  return {
    sendSpy: vi.fn(),
    clearSpy: vi.fn((id: string) => { delete pairing.byChannel[id] }),
    pairing,
  }
})
vi.mock('@/store/connection', () => {
  const state = { isConnected: true, connection: { send: sendSpy } }
  const hook = vi.fn((selector: (s: typeof state) => unknown) => selector(state))
  ;(hook as unknown as { getState: () => typeof state }).getState = () => state
  return { useConnectionStore: hook }
})

// Pairing store: real keying semantics (byChannel keyed by frame channel_id).
vi.mock('@/store/whatsappPairing', () => {
  const hook = vi.fn(
    (selector: (s: { byChannel: typeof pairing.byChannel; clear: typeof clearSpy }) => unknown) =>
      selector({ byChannel: pairing.byChannel, clear: clearSpy }),
  )
  return { useWhatsAppPairingStore: hook }
})

import { WhatsAppNativeNotice } from './WhatsAppNativeNotice'

beforeEach(() => {
  pairing.byChannel = {}
  sendSpy.mockClear()
  clearSpy.mockClear()
})

describe('WhatsAppNativeNotice — QR renders from the instance-keyed pairing frame', () => {
  it.each(['whatsapp', 'whatsapp.sales'])(
    'renders the QR for a "code" frame stored under its own channelId (%s)',
    (channelId) => {
      pairing.byChannel[channelId] = { status: 'code', qr: 'wa-pairing-secret', message: '' }

      render(<WhatsAppNativeNotice channelId={channelId} />)

      // The QR block renders (WhatsAppPairingBody → QRCodeSVG on white).
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
      expect(screen.getByTestId('whatsapp-qr').querySelector('svg')).not.toBeNull()
    },
  )

  it("does NOT render another instance's QR (per-instance isolation)", () => {
    // A frame for a DIFFERENT whatsapp instance must not light up this panel.
    pairing.byChannel['whatsapp.other'] = { status: 'code', qr: 'other-secret', message: '' }

    render(<WhatsAppNativeNotice channelId="whatsapp.sales" />)

    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    // Still in the waiting/spinner state for OUR instance.
    expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
  })

  it('does not render a QR for the legacy "whatsapp_native" store key (the old broken contract)', () => {
    // Guards against regressing to the pre-multi-instance hardcoded key: a
    // frame under 'whatsapp_native' belongs to no live instance and must not
    // satisfy an instance-scoped notice.
    pairing.byChannel['whatsapp_native'] = { status: 'code', qr: 'stale-secret', message: '' }

    render(<WhatsAppNativeNotice channelId="whatsapp" />)

    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })
})

describe('WhatsAppNativeNotice — subscribe interest is instance-scoped', () => {
  it.each(['whatsapp', 'whatsapp.sales'])(
    'subscribes with its own channelId on mount and unsubscribes + clears on unmount (%s)',
    (channelId) => {
      const { unmount } = render(<WhatsAppNativeNotice channelId={channelId} />)

      expect(sendSpy).toHaveBeenCalledWith({
        type: 'whatsapp_pairing_subscribe',
        channel_id: channelId,
        active: true,
      })

      unmount()

      expect(sendSpy).toHaveBeenCalledWith({
        type: 'whatsapp_pairing_subscribe',
        channel_id: channelId,
        active: false,
      })
      expect(clearSpy).toHaveBeenCalledWith(channelId)
    },
  )
})
