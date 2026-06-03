/**
 * ChannelConfigPanel.test.tsx — tests for nativeAvailable prop behaviour.
 *
 * Covers:
 *   1. With channelId="whatsapp" and nativeAvailable={false}:
 *      - the use_native Switch is disabled
 *      - the "native unavailable" hint renders
 *      - WhatsAppNativeNotice (QR block) does NOT render
 *   2. With channelId="whatsapp" and nativeAvailable={true}:
 *      - the use_native Switch is interactive (not disabled)
 *      - no hint rendered when use_native is off
 *   3. With nativeAvailable={undefined} (omitted):
 *      - treated as "no restriction" — same as true
 *
 * Traces to: issue #299 — SPA must only offer what the binary can actually do.
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

interface RenderOptions {
  nativeAvailable?: boolean
  /** Pre-seed use_native in the form so the QR flow would render when available. */
  useNativeOn?: boolean
}

function renderWhatsAppPanel(opts: RenderOptions = {}) {
  const { nativeAvailable, useNativeOn = false } = opts
  const client = makeQueryClient()
  // Pre-seed query data so the component doesn't need to fetch.
  client.setQueryData(['channel-config', 'whatsapp'], { use_native: useNativeOn })
  client.setQueryData(['channel-routing', 'whatsapp'], { default_agent_id: undefined })
  client.setQueryData(['agents'], [])

  const onOpenChange = vi.fn()
  const result = render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId="whatsapp"
        channelName="WhatsApp"
        open={true}
        onOpenChange={onOpenChange}
        nativeAvailable={nativeAvailable}
      />
    </QueryClientProvider>,
  )
  return { ...result, onOpenChange }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('ChannelConfigPanel — nativeAvailable=false (build lacks whatsmeow)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({ use_native: false })
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('renders the native-unavailable hint when nativeAvailable is false', async () => {
    // Content test: the operator must see an explanation why native mode is greyed out.
    // Traces to: issue #299 — SPA must not offer what the binary cannot do.
    renderWhatsAppPanel({ nativeAvailable: false })

    await waitFor(() => {
      expect(screen.getByTestId('native-unavailable-hint')).toBeInTheDocument()
    })
    expect(screen.getByText(/This build doesn't include native WhatsApp/i)).toBeInTheDocument()
  })

  it('the use_native Switch is disabled when nativeAvailable is false', async () => {
    // Correctness test: the toggle must be non-interactive so the operator cannot
    // accidentally enable a feature the binary cannot serve.
    // WhatsApp has multiple toggles; target the specific use_native switch by id.
    renderWhatsAppPanel({ nativeAvailable: false })

    await waitFor(() => {
      const toggle = document.getElementById('field-use_native')
      expect(toggle).not.toBeNull()
      expect(toggle).toBeDisabled()
    })
  })

  it('does NOT render WhatsAppNativeNotice (QR block) when nativeAvailable is false', async () => {
    // Safety test: no QR code element must appear — attempting to scan a QR when
    // native is unavailable would silently fail.
    // The native-unavailable-hint being present is the positive signal; absence of
    // the QR testid confirms the block is suppressed.
    renderWhatsAppPanel({ nativeAvailable: false, useNativeOn: true })

    await waitFor(() => {
      expect(screen.getByTestId('native-unavailable-hint')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })
})

describe('ChannelConfigPanel — nativeAvailable=true (native build)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({ use_native: false })
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('the use_native Switch is interactive (not disabled) when nativeAvailable is true', async () => {
    // Differentiation test: explicitly available native → switch must be enabled.
    renderWhatsAppPanel({ nativeAvailable: true })

    await waitFor(() => {
      const toggle = document.getElementById('field-use_native')
      expect(toggle).not.toBeNull()
      expect(toggle).not.toBeDisabled()
    })
  })

  it('does NOT render the native-unavailable hint when nativeAvailable is true', async () => {
    // Differentiation test: hint must not pollute the UI when native is available.
    renderWhatsAppPanel({ nativeAvailable: true })

    await waitFor(() => {
      // Wait for the form to render (the use_native field must be present).
      expect(document.getElementById('field-use_native')).not.toBeNull()
    })
    expect(screen.queryByTestId('native-unavailable-hint')).not.toBeInTheDocument()
  })

  it('renders the QR container when nativeAvailable=true and use_native is on and pairing has a QR code', async () => {
    // Positive-path test: the QR block must appear when the binary supports native
    // mode, the user has toggled use_native on, and the WS delivers a QR code.
    // Traces to: issue #299 — native-available build must surface the QR flow.
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; apply: () => void; clear: () => void }) => unknown) =>
        selector({
          byChannel: { whatsapp_native: { status: 'code', qr: 'https://example.com/test-qr', message: '' } },
          apply: vi.fn(),
          clear: vi.fn(),
        })) as never,
    )

    renderWhatsAppPanel({ nativeAvailable: true, useNativeOn: true })

    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('native-unavailable-hint')).not.toBeInTheDocument()
  })
})

describe('ChannelConfigPanel — nativeAvailable=undefined (omitted / older backend)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({ use_native: false })
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('the use_native Switch is interactive when nativeAvailable is undefined', async () => {
    // Backward-compat test: absence of the field (older backend response) must not
    // disable the toggle — "undefined" is treated as "no restriction".
    renderWhatsAppPanel({ nativeAvailable: undefined })

    await waitFor(() => {
      const toggle = document.getElementById('field-use_native')
      expect(toggle).not.toBeNull()
      expect(toggle).not.toBeDisabled()
    })
  })

  it('does NOT render the native-unavailable hint when nativeAvailable is undefined', async () => {
    // Backward-compat: no hint when field is absent.
    renderWhatsAppPanel({ nativeAvailable: undefined })

    await waitFor(() => {
      expect(document.getElementById('field-use_native')).not.toBeNull()
    })
    expect(screen.queryByTestId('native-unavailable-hint')).not.toBeInTheDocument()
  })
})
