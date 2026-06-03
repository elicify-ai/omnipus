/**
 * ChannelConfigPanel.test.tsx — tests for the ChannelConfigPanel component.
 *
 * Covers:
 *   WhatsApp QR 5-state machine (#325 / US-C3):
 *   1. No frame (waiting) → spinner + "Generating your QR code…"
 *   2. state 'code' → QR container renders
 *   3. state 'linked' → success copy
 *   4. state 'timeout' → distinct copy + Retry button
 *   5. state 'error' → distinct copy + Retry button (different from timeout)
 *   6. timeout vs error copy are distinct from each other
 *
 *   WhatsApp always-native (#283):
 *   7. WhatsAppNativeNotice mounts for whatsapp when native is available
 *   8. No use_native field (removed)
 *   9. QR container renders when pairing WS frame delivers a QR code
 *  10. native_available:true behaves same as undefined default
 *  11. native_available:false → hint instead of QR notice (#299)
 *
 *   Google Chat authGroup picker (#324 / US-C2):
 *  12. Webhook selected → only webhook_url field shows
 *  13. Service account selected → SA fields show
 *  14. Switching method clears the deselected group's state value
 *
 *   Human-label errors + a11y (#326 / US-C4):
 *  15. aria-describedby target is rendered in the dialog
 *  16. Test button shows "Test = check without saving" hint
 *
 *   Helper + link render (#322 / US-C1):
 *  17. helpText renders under field
 *  18. helpLink renders as an anchor
 *
 *   Non-whatsapp channels:
 *  19. No pairing notice for non-whatsapp channel
 *
 * Traces: #283, #299, #322, #324, #325, #326
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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

function renderPanel(channelId: string, channelName: string, nativeAvailable?: boolean) {
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
        nativeAvailable={nativeAvailable}
        open={true}
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>,
  )
  return { ...result, onOpenChange }
}

type PairingStatus = 'waiting' | 'code' | 'linked' | 'timeout' | 'error'

function mockPairingState(status: PairingStatus, qr = '') {
  vi.mocked(useWhatsAppPairingStore).mockImplementation(
    ((selector: (s: { byChannel: Record<string, unknown>; apply: () => void; clear: () => void }) => unknown) =>
      selector({
        byChannel: {
          whatsapp_native: { status, qr, message: status === 'error' ? 'Auth failed' : '' },
        },
        apply: vi.fn(),
        clear: vi.fn(),
      })) as never,
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('ChannelConfigPanel — WhatsApp QR 5-state machine (#325 / US-C3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    // Reset pairing store to empty (no frame → waiting state).
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
        selector({ byChannel: {}, clear: vi.fn() })) as never,
    )
  })

  it('AC1: no frame → shows spinner with "Generating your QR code…"', async () => {
    // No pairing frame in store → waiting state (the default when the notice mounts).
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
  })

  it('AC2: state "waiting" → shows spinner "Generating your QR code…"', async () => {
    mockPairingState('waiting')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })

  it('AC2: state "code" → renders QR + Linked Devices steps + refresh note', async () => {
    mockPairingState('code', 'https://example.com/test-qr')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
    expect(screen.getByText(/Linked Devices/i)).toBeInTheDocument()
    expect(screen.getByText(/20s/i)).toBeInTheDocument()
  })

  it('AC3: state "linked" → success message', async () => {
    mockPairingState('linked')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Linked successfully/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })

  it('AC4: state "timeout" → distinct expired copy + Retry button', async () => {
    mockPairingState('timeout')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    })
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
  })

  it('AC4: state "error" → distinct pairing-failed copy + Retry button', async () => {
    mockPairingState('error')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Pairing failed/i)).toBeInTheDocument()
    })
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
  })

  it('AC4: timeout and error render distinct copy from each other', async () => {
    // First render timeout
    mockPairingState('timeout')
    const { unmount } = renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    })
    const timeoutText = screen.getByText(/QR expired/i).textContent
    unmount()

    // Then render error
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    mockPairingState('error')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Pairing failed/i)).toBeInTheDocument()
    })
    const errorText = screen.getByText(/Pairing failed/i).textContent

    // They must be different strings
    expect(timeoutText).not.toBe(errorText)
  })
})

describe('ChannelConfigPanel — WhatsApp is always native (#283)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
        selector({ byChannel: {}, clear: vi.fn() })) as never,
    )
  })

  it('renders the live linked-device pairing notice for whatsapp (no use_native gate)', async () => {
    // nativeAvailable is omitted → undefined → default-available.
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
  })

  it('does NOT render a use_native field (the toggle was removed)', async () => {
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
    expect(document.getElementById('field-use_native')).toBeNull()
  })

  it('renders the QR container when the pairing WS frame delivers a QR code', async () => {
    mockPairingState('code', 'https://example.com/test-qr')
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
  })

  it('renders the QR notice when native_available:true is explicitly passed', async () => {
    renderPanel('whatsapp', 'WhatsApp', true)
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('native-unavailable-hint')).not.toBeInTheDocument()
  })

  it('capability gating (#299): native_available:false shows the hint and NOT the QR notice', async () => {
    mockPairingState('code', 'https://example.com/test-qr')
    renderPanel('whatsapp', 'WhatsApp', false)
    await waitFor(() => {
      expect(screen.getByTestId('native-unavailable-hint')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  })
})

describe('ChannelConfigPanel — Google Chat authGroup picker (#324 / US-C2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('AC1: webhook selected → only webhook_url field shows; SA fields absent', async () => {
    renderPanel('google-chat', 'Google Chat')
    await waitFor(() => {
      // Webhook radio should be pre-selected (default)
      const webhookRadio = screen.getByRole('radio', { name: /Webhook URL/i })
      expect(webhookRadio).toBeChecked()
    })
    // webhook_url field must be present
    expect(document.getElementById('field-webhook_url')).not.toBeNull()
    // Service account fields must NOT be present
    expect(document.getElementById('field-service_account_json')).toBeNull()
    expect(document.getElementById('field-service_account_file')).toBeNull()
  })

  it('AC1: service account selected → SA fields show; webhook_url absent', async () => {
    renderPanel('google-chat', 'Google Chat')
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Service account/i })).toBeInTheDocument()
    })
    // Switch to service account
    fireEvent.click(screen.getByRole('radio', { name: /Service account/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Service account/i })).toBeChecked()
    })
    expect(document.getElementById('field-service_account_json')).not.toBeNull()
    expect(document.getElementById('field-service_account_file')).not.toBeNull()
    expect(document.getElementById('field-webhook_url')).toBeNull()
  })

  it('AC2: switching method clears the deselected group state (no stale secret)', async () => {
    // Seed with a webhook_url value
    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'google-chat'], { webhook_url: 'https://chat.googleapis.com/v1/spaces/test' })
    client.setQueryData(['channel-routing', 'google-chat'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [])

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="google-chat" channelName="Google Chat" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    // Wait for webhook radio to be ready
    await waitFor(() => {
      const webhookRadio = screen.getByRole('radio', { name: /Webhook URL/i })
      expect(webhookRadio).toBeChecked()
    })

    // Switch to service account — this should clear webhook_url in state
    fireEvent.click(screen.getByRole('radio', { name: /Service account/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Service account/i })).toBeChecked()
    })

    // Switch back to webhook — the webhook_url field should be empty (state was cleared)
    fireEvent.click(screen.getByRole('radio', { name: /Webhook URL/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Webhook URL/i })).toBeChecked()
    })
    const webhookInput = document.getElementById('field-webhook_url') as HTMLInputElement | null
    expect(webhookInput).not.toBeNull()
    // The value must be empty — cleared when switching away, not resurrected on switch-back
    expect(webhookInput!.value).toBe('')
  })
})

describe('ChannelConfigPanel — human-label errors + a11y (#326 / US-C4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('AC2: Configure dialog has an aria-describedby target', async () => {
    renderPanel('telegram', 'Telegram')
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })
    // The SheetContent must have aria-describedby pointing at the description element
    const sheetContent = document.querySelector('[aria-describedby]')
    expect(sheetContent).not.toBeNull()
    const descId = sheetContent!.getAttribute('aria-describedby')
    expect(descId).toBeTruthy()
    const descEl = document.getElementById(descId!)
    expect(descEl).not.toBeNull()
    // The description element must have non-empty text
    expect(descEl!.textContent?.trim()).not.toBe('')
  })

  it('Test button includes a "check without saving" hint', async () => {
    renderPanel('telegram', 'Telegram')
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })
    expect(
      screen.getByText(/Test checks your connection without saving/i),
    ).toBeInTheDocument()
  })
})

describe('ChannelConfigPanel — helper + link render (#322 / US-C1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('helpText renders under the Bot Token field for Telegram', async () => {
    renderPanel('telegram', 'Telegram')
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })
    expect(screen.getByText(/Get from @BotFather/i)).toBeInTheDocument()
  })

  it('helpLink renders as an https anchor for Telegram Bot Token', async () => {
    renderPanel('telegram', 'Telegram')
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })
    const link = screen.getByRole('link', { name: /Open BotFather/i })
    expect(link).toBeInTheDocument()
    expect(link.getAttribute('href')).toMatch(/^https:\/\//)
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
  })

  it('a field with no helpLink renders helpText only (no broken link)', async () => {
    // Matrix join_on_invite has helpText but no helpLink.
    renderPanel('matrix', 'Matrix')
    await waitFor(() => {
      expect(document.getElementById('field-homeserver')).not.toBeNull()
    })
    // Expand Advanced to see join_on_invite
    const advButton = screen.getByRole('button', { name: /Advanced/i })
    fireEvent.click(advButton)
    await waitFor(() => {
      expect(screen.getByText(/Automatically join rooms when invited/i)).toBeInTheDocument()
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
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  })
})
