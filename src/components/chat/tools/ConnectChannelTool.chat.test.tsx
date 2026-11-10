/**
 * ConnectChannelTool.chat.test.tsx — the connect_channel tool card in the
 * requesting chat (Train 2, WU-7; LEAN pack).
 *
 * Oracles: omnipus-system-agent-spec BDD-8.4 (QR in the requesting chat),
 * BDD-8.8 (Stop ends pairing) and FR-081; approved frontend plan: a
 * connect_channel tool UI mounts the SAME WhatsAppNativeNotice under a LIVE
 * connect_channel call for a WhatsApp instance; a REPLAYED (non-live) call
 * shows a static pointer to Connectors > WhatsApp and sends no subscribe
 * (founder REUSE rule: no new QR component, no second store).
 *
 * RED at 55d0ddd4d: no connect_channel tool UI exists — the module import is
 * a BLOCKED failure naming the planned surface.
 *
 * Planned public surface (RED's naming decision, recorded in
 * tests/train2-qr-red/ORACLE.md; GREEN must match):
 *   src/components/chat/tools/ConnectChannelTool.tsx::
 *   ConnectChannelTool({ toolCallId, sessionId, channelId, live })
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, waitFor } from '@testing-library/react'
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

import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { WhatsAppPairingBody } from '@/components/skills/WhatsAppPairingBody'

const CHANNEL = 'whatsapp'
const TOOL_CALL = 'toolcall-connect-1'
const SESSION = 'omnipus-main'

// import.meta.glob tolerates a missing file at transform time; the accessor
// below turns the absence into a loud BLOCKED failure (WU-15 harness pattern).
const plannedModules = import.meta.glob<Record<string, unknown>>('/src/components/chat/tools/ConnectChannelTool.tsx')

interface ConnectChannelToolProps {
  toolCallId: string
  sessionId: string
  channelId: string
  live: boolean
}

async function renderConnectChannelTool(props: ConnectChannelToolProps): Promise<void> {
  const load = plannedModules['/src/components/chat/tools/ConnectChannelTool.tsx']
  if (!load) {
    expect.fail(
      'BLOCKED: src/components/chat/tools/ConnectChannelTool.tsx::ConnectChannelTool ' +
        'not implemented — required by BDD-8.4/FR-081 (connect_channel tool UI mounts ' +
        'WhatsAppNativeNotice; approved plan, Train 2 WU-7)',
    )
  }
  const mod = (await load()) as unknown as {
    ConnectChannelTool?: (props: ConnectChannelToolProps) => React.ReactNode
  }
  if (typeof mod.ConnectChannelTool !== 'function') {
    expect.fail(
      'BLOCKED: src/components/chat/tools/ConnectChannelTool.tsx::ConnectChannelTool ' +
        'not exported — required by BDD-8.4/FR-081 (approved plan, Train 2 WU-7)',
    )
  }
  const { ConnectChannelTool } = mod as { ConnectChannelTool: (p: typeof props) => React.ReactNode }
  render(<ConnectChannelTool {...props} />)
}

beforeEach(() => {
  sendSpy.mockClear()
  connectionState.isConnected = true
  act(() => useWhatsAppPairingStore.setState({ byChannel: {} }))
})

describe('Row 1 — a LIVE connect_channel call mounts the same notice under the tool call (BDD-8.4)', () => {
  it('a live WhatsApp call subscribes with request_id = the tool call id, and a code frame renders the QR below it', async () => {
    await renderConnectChannelTool({ toolCallId: TOOL_CALL, sessionId: SESSION, channelId: CHANNEL, live: true })

    // The mounted notice is THE same component — its subscribe carries the
    // tool call's id as the pairing correlation identity.
    await waitFor(() => expect(sendSpy).toHaveBeenCalled())
    const subscribe = sendSpy.mock.calls
      .map(([frame]) => frame as { type?: string; request_id?: string })
      .find((frame) => frame.type === 'whatsapp_pairing_subscribe')
    expect(subscribe, 'the mounted notice subscribes for this pairing').toBeTruthy()
    expect(subscribe?.request_id).toBe(TOOL_CALL)

    act(() =>
      useWhatsAppPairingStore.getState().apply({
        type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'code',
        qr: 'wa-pairing-secret', request_id: TOOL_CALL, session_id: SESSION,
      } as WhatsAppPairingFrame),
    )
    expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
  })
})

describe('Row 7 — a replayed call shows the static pointer and sends no subscribe (BDD-8.4 guard)', () => {
  it('a non-live connect_channel render points at Connectors > WhatsApp and sends nothing', async () => {
    await renderConnectChannelTool({ toolCallId: TOOL_CALL, sessionId: SESSION, channelId: CHANNEL, live: false })

    expect(screen.getByText(/Connectors/i)).toBeInTheDocument()
    expect(screen.getByText(/WhatsApp/i)).toBeInTheDocument()
    expect(sendSpy).not.toHaveBeenCalled()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })
})

describe('Row 4 (Connectors half) — BDD-8.8: a stopped pairing renders a proper stopped state', () => {
  it('WhatsAppPairingBody renders the stopped state instead of falling through to the default spinner', () => {
    act(() =>
      useWhatsAppPairingStore.getState().apply({
        type: 'whatsapp_pairing', channel_id: CHANNEL, status: 'stopped',
        message: 'Pairing stopped.', request_id: TOOL_CALL, session_id: SESSION,
      } as WhatsAppPairingFrame),
    )
    const stopped = useWhatsAppPairingStore.getState().byChannel[CHANNEL]!

    render(<WhatsAppPairingBody pairing={stopped} />)

    expect(screen.getByText(/stopped/i)).toBeInTheDocument()
    // No fallback spinner: a terminal stopped state is never "Generating…".
    expect(screen.queryByText(/Generating your QR code/)).not.toBeInTheDocument()
    expect(document.querySelector('.animate-spin')).toBeNull()
  })
})
