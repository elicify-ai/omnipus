/**
 * ChannelConfigPanel.test.tsx — tests for the WhatsApp native QR pairing UI.
 *
 * WhatsApp is now UNCONDITIONALLY native (whatsmeow): the legacy bridge channel
 * and the `use_native` toggle were removed. The linked-device QR pairing UI
 * (#283) must therefore render for the whatsapp channel without any `use_native`
 * field gating it.
 *
 * Covers:
 *   1. The WhatsAppNativeNotice (live QR / status block) mounts for whatsapp.
 *   2. When the pairing WS frame carries a QR code, the QR container renders.
 *   3. No `use_native` field is rendered (the field was removed).
 *   4. Non-whatsapp channels do NOT render the pairing notice.
 *
 * Traces to: #283 (live QR pairing in the SPA) + the channels-cleanup branch
 * (always-native WhatsApp; `use_native` removed from channel-fields).
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/store/connection', () => {
  // WhatsAppNativeNotice calls useConnectionStore.getState() directly on unmount
  // (not via the hook), so the mock function must also expose getState.
  const state = { isConnected: false, connection: null }
  const hook = vi.fn((selector: (s: typeof state) => unknown) => selector(state))
  ;(hook as unknown as { getState: () => typeof state }).getState = () => state
  return { useConnectionStore: hook }
})

vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: vi.fn(
    (selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
      selector({ byChannel: {}, clear: vi.fn() }),
  ),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannelConfig: vi.fn(),
    getChannelRouting: vi.fn(),
    fetchAgents: vi.fn(),
    configureChannel: vi.fn(),
    testChannel: vi.fn(),
    enableChannel: vi.fn(),
    setChannelRouting: vi.fn(),
    isApiError: vi.fn(() => false),
  }
})

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, prop: string) =>
      ({ children, ...props }: Record<string, unknown>) =>
        React.createElement(prop as string, props, children as React.ReactNode),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

// ── Imports after mocks ────────────────────────────────────────────────────────

import { useUiStore } from '@/store/ui'
import {
  fetchChannelConfig,
  getChannelRouting,
  fetchAgents,
} from '@/lib/api'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { ChannelConfigPanel } from './ChannelConfigPanel'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function mockUiStore() {
  const addToast = vi.fn()
  vi.mocked(useUiStore).mockReturnValue({ addToast } as never)
  return { addToast }
}

function renderPanel(channelId: string, channelName: string) {
  const client = makeQueryClient()
  // Pre-seed query data so the component doesn't need to fetch.
  client.setQueryData(['channel-config', channelId], {})
  client.setQueryData(['channel-routing', channelId], { default_agent_id: undefined })
  client.setQueryData(['agents'], [])

  const onOpenChange = vi.fn()
  const result = render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId={channelId}
        channelName={channelName}
        open={true}
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>,
  )
  return { ...result, onOpenChange }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('ChannelConfigPanel — WhatsApp is always native', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('renders the live linked-device pairing notice for whatsapp (no use_native gate)', async () => {
    // The native QR pairing UX (#283) must mount unconditionally for whatsapp.
    renderPanel('whatsapp', 'WhatsApp')

    await waitFor(() => {
      expect(
        screen.getByText(/Enable and save, then a QR code will appear here/i),
      ).toBeInTheDocument()
    })
  })

  it('does NOT render a use_native field (the toggle was removed)', async () => {
    // The `use_native` channel field was deleted; the panel must not surface it.
    renderPanel('whatsapp', 'WhatsApp')

    await waitFor(() => {
      // The pairing notice having mounted is the positive signal the panel rendered.
      expect(
        screen.getByText(/Enable and save, then a QR code will appear here/i),
      ).toBeInTheDocument()
    })
    expect(document.getElementById('field-use_native')).toBeNull()
  })

  it('renders the QR container when the pairing WS frame delivers a QR code', async () => {
    // Positive-path: a delivered QR must surface the scannable code block.
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; apply: () => void; clear: () => void }) => unknown) =>
        selector({
          byChannel: { whatsapp_native: { status: 'code', qr: 'https://example.com/test-qr', message: '' } },
          apply: vi.fn(),
          clear: vi.fn(),
        })) as never,
    )

    renderPanel('whatsapp', 'WhatsApp')

    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
  })
})

describe('ChannelConfigPanel — non-whatsapp channels', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('does NOT render the WhatsApp pairing notice for a non-whatsapp channel', async () => {
    renderPanel('telegram', 'Telegram')

    await waitFor(() => {
      // Telegram's required Bot Token field confirms the panel rendered.
      expect(document.getElementById('field-token')).not.toBeNull()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(
      screen.queryByText(/Enable and save, then a QR code will appear here/i),
    ).not.toBeInTheDocument()
  })
})
