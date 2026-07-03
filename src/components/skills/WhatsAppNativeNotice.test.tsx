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
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'

// Connection store: expose a spyable `send` + getState (the notice calls
// useConnectionStore.getState() directly in unmount cleanup). vi.hoisted —
// vi.mock factories are hoisted above const initializers.
const { sendSpy, clearSpy, applySpy, disableSpy, enableSpy, pairing } = vi.hoisted(() => {
  const pairing: { byChannel: Record<string, { status: string; qr: string; message: string }> } = { byChannel: {} }
  return {
    sendSpy: vi.fn(),
    clearSpy: vi.fn((id: string) => { delete pairing.byChannel[id] }),
    applySpy: vi.fn(),
    disableSpy: vi.fn(async () => ({})),
    enableSpy: vi.fn(async () => ({})),
    pairing,
  }
})
// Retry restarts the channel via the REST enable/disable endpoints.
vi.mock('@/lib/api', () => ({
  disableChannel: disableSpy,
  enableChannel: enableSpy,
}))
vi.mock('@/store/connection', () => {
  const state = { isConnected: true, connection: { send: sendSpy } }
  const hook = vi.fn((selector: (s: typeof state) => unknown) => selector(state))
  ;(hook as unknown as { getState: () => typeof state }).getState = () => state
  return { useConnectionStore: hook }
})

// Pairing store: real keying semantics (byChannel keyed by frame channel_id).
// getState() is used by handleRetry's error path (apply) and unmount cleanup.
vi.mock('@/store/whatsappPairing', () => {
  const state = () => ({ byChannel: pairing.byChannel, clear: clearSpy, apply: applySpy })
  const hook = vi.fn(
    (selector: (s: ReturnType<typeof state>) => unknown) => selector(state()),
  )
  ;(hook as unknown as { getState: typeof state }).getState = state
  return { useWhatsAppPairingStore: hook }
})

import { WhatsAppNativeNotice } from './WhatsAppNativeNotice'

beforeEach(() => {
  pairing.byChannel = {}
  sendSpy.mockClear()
  clearSpy.mockClear()
  applySpy.mockClear()
  disableSpy.mockClear()
  enableSpy.mockClear()
  disableSpy.mockImplementation(async () => ({}))
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

describe('WhatsAppNativeNotice — Retry restarts the channel (terminal QR loop)', () => {
  // whatsmeow's QR loop is terminal once its codes expire: re-subscribing can
  // only re-emit a CACHED code, never mint a new one. Retry must therefore
  // bounce the channel (disable → enable), which re-arms the QR loop.
  it.each(['whatsapp', 'whatsapp.sales'])(
    'clicking Retry disables then re-enables the instance and re-subscribes (%s)',
    async (channelId) => {
      pairing.byChannel[channelId] = { status: 'timeout', qr: '', message: 'expired' }

      render(<WhatsAppNativeNotice channelId={channelId} />)
      sendSpy.mockClear() // drop the mount-time subscribe

      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: /retry/i }))
      })

      await waitFor(() => {
        expect(disableSpy).toHaveBeenCalledWith(channelId)
        expect(enableSpy).toHaveBeenCalledWith(channelId)
      })
      // disable strictly before enable
      expect(disableSpy.mock.invocationCallOrder[0]).toBeLessThan(
        enableSpy.mock.invocationCallOrder[0],
      )
      // interest re-registered for THIS instance after the restart
      expect(sendSpy).toHaveBeenCalledWith({
        type: 'whatsapp_pairing_subscribe',
        channel_id: channelId,
        active: false,
      })
      expect(sendSpy).toHaveBeenCalledWith({
        type: 'whatsapp_pairing_subscribe',
        channel_id: channelId,
        active: true,
      })
    },
  )

  it('surfaces an error state when the restart round-trip fails', async () => {
    pairing.byChannel['whatsapp'] = { status: 'timeout', qr: '', message: 'expired' }
    disableSpy.mockImplementation(async () => { throw new Error('gateway down') })

    render(<WhatsAppNativeNotice channelId="whatsapp" />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    })

    await waitFor(() => {
      expect(applySpy).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'whatsapp_pairing',
          channel_id: 'whatsapp',
          status: 'error',
        }),
      )
    })
    // No re-subscribe after a failed restart
    expect(enableSpy).not.toHaveBeenCalled()
  })

  it('when disable fails outright, the message does not claim the channel was disabled', async () => {
    pairing.byChannel['whatsapp'] = { status: 'timeout', qr: '', message: 'expired' }
    disableSpy.mockImplementation(async () => { throw new Error('gateway down') })

    render(<WhatsAppNativeNotice channelId="whatsapp" />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    })

    await waitFor(() => {
      expect(applySpy).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'whatsapp_pairing',
          channel_id: 'whatsapp',
          status: 'error',
          message: expect.not.stringMatching(/disabled/i),
        }),
      )
    })
  })

  it('when disable succeeds but re-enable fails, the error message names the channel as now disabled', async () => {
    pairing.byChannel['whatsapp'] = { status: 'timeout', qr: '', message: 'expired' }
    // disableSpy resolves via the default beforeEach mock; enable fails.
    enableSpy.mockImplementation(async () => { throw new Error('reload error') })

    render(<WhatsAppNativeNotice channelId="whatsapp" />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    })

    await waitFor(() => {
      expect(disableSpy).toHaveBeenCalledWith('whatsapp')
      expect(applySpy).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'whatsapp_pairing',
          channel_id: 'whatsapp',
          status: 'error',
          message: expect.stringMatching(/disabled/i),
        }),
      )
    })
  })

  it('ignores a second click fired synchronously while a retry is already in flight (re-entrancy guard)', async () => {
    pairing.byChannel['whatsapp'] = { status: 'timeout', qr: '', message: 'expired' }

    render(<WhatsAppNativeNotice channelId="whatsapp" />)
    const retryBtn = screen.getByRole('button', { name: /retry/i })

    // Two synchronous clicks, no await in between — both invoke handleRetry
    // before React has a chance to re-render and hide the Retry button.
    await act(async () => {
      fireEvent.click(retryBtn)
      fireEvent.click(retryBtn)
    })

    expect(disableSpy).toHaveBeenCalledTimes(1)
    expect(enableSpy).toHaveBeenCalledTimes(1)
  })
})
