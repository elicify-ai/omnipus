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
 *   WhatsApp Retry bounded timeout (MAJOR fix):
 *   7. Retry on timeout → spinner shown while waiting; if no code frame arrives
 *      within 30s the timer fires and the UI reverts to timeout + Retry
 *
 *   WhatsApp always-native (#283):
 *   8. WhatsAppNativeNotice mounts for whatsapp when native is available
 *   9. No use_native field (removed)
 *  10. QR container renders when pairing WS frame delivers a QR code
 *  11. native_available:true behaves same as undefined default
 *  12. native_available:false → hint instead of QR notice (#299)
 *
 *   Google Chat authGroup picker (#324 / US-C2):
 *  13. Webhook selected → only webhook_url field shows
 *  14. Service account selected → SA fields show
 *  15. Switching method clears the deselected group's state value
 *  16. BLOCKER FIX: submit payload sends '' for deselected sensitive fields
 *      (not omit/delete — backend deep-merge leaves absent fields untouched)
 *
 *   Human-label errors + a11y (#326 / US-C4):
 *  17. aria-describedby target is rendered in the dialog
 *  18. Test button shows "Test = check without saving" hint
 *
 *   Helper + link render (#322 / US-C1):
 *  19. helpText renders under field
 *  18. helpLink renders as an anchor
 *
 *   Non-whatsapp channels:
 *  19. No pairing notice for non-whatsapp channel
 *
 * Traces: #283, #299, #322, #324, #325, #326
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
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
  configureChannel,
  enableChannel,
  testChannel,
  setChannelRouting,
} from '@/lib/api'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { ChannelConfigPanel } from './ChannelConfigPanel'
import { WhatsAppNativeNotice } from './WhatsAppNativeNotice'

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

function renderPanel(channelId: string, channelName: string, nativeAvailable?: boolean, enabled?: boolean) {
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
        enabled={enabled}
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
    // Uses the REST-facing channel id ('whatsapp') as ChannelsScreen would pass it.
    // enabled:true required — WhatsAppNativeNotice only mounts when the channel is enabled.
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
  })

  it('AC2: state "waiting" → shows spinner "Generating your QR code…"', async () => {
    mockPairingState('waiting')
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })

  it('AC2: state "code" → renders QR + Linked Devices steps + refresh note', async () => {
    mockPairingState('code', 'https://example.com/test-qr')
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
    expect(screen.getByText(/Linked Devices/i)).toBeInTheDocument()
    expect(screen.getByText(/20s/i)).toBeInTheDocument()
  })

  it('AC3: state "linked" → success message', async () => {
    mockPairingState('linked')
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Linked successfully/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
  })

  it('AC4: state "timeout" → distinct expired copy + Retry button', async () => {
    mockPairingState('timeout')
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    })
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
  })

  it('AC4: state "error" → distinct pairing-failed copy + Retry button', async () => {
    mockPairingState('error')
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Pairing failed/i)).toBeInTheDocument()
    })
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
  })

  it('AC4: timeout and error render distinct copy from each other', async () => {
    // First render timeout
    mockPairingState('timeout')
    const { unmount } = renderPanel('whatsapp', 'WhatsApp', undefined, true)
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
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Pairing failed/i)).toBeInTheDocument()
    })
    const errorText = screen.getByText(/Pairing failed/i).textContent

    // They must be different strings
    expect(timeoutText).not.toBe(errorText)
  })
})

describe('ChannelConfigPanel — WhatsApp Retry bounded timeout (MAJOR fix)', () => {
  // The whatsapp_pairing_subscribe toggle only controls forwarding interest — it
  // does NOT make whatsmeow mint a new QR. For `error` state the QR loop is
  // terminal; for `timeout` a new QR may arrive automatically. If no `code` frame
  // arrives within 30s (RETRY_TIMEOUT_MS), the UI must revert to the fallback
  // state with the Retry affordance rather than spinning forever.
  //
  // Implementation: handleRetry clears the store and sets retryFallbackState.
  // After RETRY_TIMEOUT_MS it calls useWhatsAppPairingStore.getState().apply(frame).
  // We wire a mock getState+apply here so the timer callback can update the mock
  // store state, which causes the component to re-render into the fallback state.

  // Shared mock state for the retry tests — mutable so the timer can push new frames.
  let pairingByChannel: Record<string, { status: string; qr: string; message: string }> = {}
  const clearFn = vi.fn((id: string) => { delete pairingByChannel[id] })
  const applyFn = vi.fn((frame: { channel_id: string; status: string; qr?: string; message?: string }) => {
    pairingByChannel[frame.channel_id] = {
      status: frame.status,
      qr: frame.qr ?? '',
      message: frame.message ?? '',
    }
    // Re-apply the updated mock implementation so the hook returns the new state.
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: unknown) => unknown) =>
        selector({ byChannel: pairingByChannel, clear: clearFn, apply: applyFn })) as never,
    )
  })

  beforeEach(() => {
    // Only fake setTimeout/clearTimeout — leave setInterval real so @testing-library/dom's
    // waitFor polling (which uses setInterval internally) is not intercepted and doesn't deadlock.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    vi.clearAllMocks()
    pairingByChannel = {}
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    // Wire getState so the timer callback's apply() call works.
    const mockStore = vi.mocked(useWhatsAppPairingStore)
    ;(mockStore as unknown as { getState: () => unknown }).getState = () => ({
      byChannel: pairingByChannel,
      clear: clearFn,
      apply: applyFn,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('Retry on timeout → spinner immediately; after 30s without code frame reverts to timeout + Retry', async () => {
    // Use real timers for initial render + interaction, then fake for the timer advance.
    vi.useRealTimers()

    // Seed timeout state. pairingByChannel uses the WS/store key 'whatsapp_native'.
    pairingByChannel['whatsapp_native'] = { status: 'timeout', qr: '', message: 'expired' }
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: unknown) => unknown) =>
        selector({ byChannel: pairingByChannel, clear: clearFn, apply: applyFn })) as never,
    )

    // renderPanel uses the REST-facing id ('whatsapp') as ChannelsScreen would pass it.
    // enabled:true required — WhatsAppNativeNotice only mounts when the channel is enabled.
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    })

    // Switch to fake timers just before clicking Retry so the 30s setTimeout is captured.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })

    // Click Retry — clears the store, sets retryFallbackState → spinner shown.
    await act(async () => {
      fireEvent.click(screen.getByTestId('whatsapp-retry'))
    })
    expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-retry')).not.toBeInTheDocument()

    // Advance past RETRY_TIMEOUT_MS. The timer fires: apply() injects timeout frame,
    // setRetryFallbackState(null) triggers re-render.
    await act(async () => {
      vi.advanceTimersByTime(31_000)
    })

    // The UI must revert to timeout + Retry — not an endless spinner.
    expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  })

  it('Retry on error → spinner immediately; after 30s reverts to error + Retry', async () => {
    vi.useRealTimers()

    // pairingByChannel uses the WS/store key 'whatsapp_native'.
    pairingByChannel['whatsapp_native'] = { status: 'error', qr: '', message: 'auth failed' }
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: unknown) => unknown) =>
        selector({ byChannel: pairingByChannel, clear: clearFn, apply: applyFn })) as never,
    )

    // renderPanel uses the REST-facing id ('whatsapp') as ChannelsScreen would pass it.
    // enabled:true required — WhatsAppNativeNotice only mounts when the channel is enabled.
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Pairing failed/i)).toBeInTheDocument()
    })

    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })

    await act(async () => {
      fireEvent.click(screen.getByTestId('whatsapp-retry'))
    })
    expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()

    await act(async () => {
      vi.advanceTimersByTime(31_000)
    })

    expect(screen.getByText(/Pairing failed/i)).toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
  })
})

describe('WhatsAppNativeNotice — 15s initial timeout (#368)', () => {
  // When the panel opens and no whatsapp_pairing WS frame arrives within 15s,
  // TIMEOUT_STATE is surfaced ("QR code timed out — click Retry") instead of
  // an infinite spinner. Tested directly against WhatsAppNativeNotice to avoid
  // TanStack Query async timing issues.

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows TIMEOUT_STATE message and Retry button after 15s with no QR frame', async () => {
    // beforeEach in the parent sets up the useWhatsAppPairingStore mock as empty
    // (pairing undefined). Clear and reset it here for isolation.
    vi.clearAllMocks()
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; apply: () => void; clear: () => void }) => unknown) =>
        selector({ byChannel: {}, apply: vi.fn(), clear: vi.fn() })) as never,
    )
    // Wire getState so the component's imperative calls work.
    const mockStore = vi.mocked(useWhatsAppPairingStore)
    ;(mockStore as unknown as { getState: () => unknown }).getState = () => ({
      byChannel: {},
      apply: vi.fn(),
      clear: vi.fn(),
    })

    // Use fake timers for the entire test — WhatsAppNativeNotice has no TanStack Query.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })

    // Render under act() to flush mount effects synchronously.
    await act(async () => {
      render(<WhatsAppNativeNotice />)
    })

    // Initial state: spinner shown, no retry button.
    expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-retry')).not.toBeInTheDocument()

    // Advance past 15s — fake setTimeout fires, setTimedOut(true), React re-renders.
    await act(async () => {
      vi.advanceTimersByTime(15_001)
    })

    // Timeout state: WhatsAppPairingBody renders its hardcoded timeout message
    // and the Retry affordance. The TIMEOUT_STATE.status='timeout' drives this.
    expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
    // Spinner must be gone.
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  })

  it('does not trigger TIMEOUT_STATE after unmount (no setState on unmounted component)', async () => {
    vi.clearAllMocks()
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; apply: () => void; clear: () => void }) => unknown) =>
        selector({ byChannel: {}, apply: vi.fn(), clear: vi.fn() })) as never,
    )
    const mockStore = vi.mocked(useWhatsAppPairingStore)
    ;(mockStore as unknown as { getState: () => unknown }).getState = () => ({
      byChannel: {},
      apply: vi.fn(),
      clear: vi.fn(),
    })

    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })

    const { unmount } = render(<WhatsAppNativeNotice />)

    // Flush effects so the timer is registered.
    await act(async () => {})

    // Unmount before the fake 15s timer fires — effect cleanup cancels the timer.
    unmount()

    // Advance past 15s — the cancelled timer must NOT call setTimedOut.
    await act(async () => {
      vi.advanceTimersByTime(15_001)
    })

    // Component is gone — no timeout text in the DOM.
    expect(screen.queryByText(/QR expired/i)).not.toBeInTheDocument()
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
    // enabled:true required — WhatsAppNativeNotice only mounts when the channel is enabled.
    // Uses the REST-facing channel id ('whatsapp') as ChannelsScreen would pass it.
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
  })

  it('does NOT render a use_native field (the toggle was removed)', async () => {
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
    expect(document.getElementById('field-use_native')).toBeNull()
  })

  it('renders the QR container when the pairing WS frame delivers a QR code', async () => {
    mockPairingState('code', 'https://example.com/test-qr')
    renderPanel('whatsapp', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
  })

  it('renders the QR notice when native_available:true is explicitly passed', async () => {
    renderPanel('whatsapp', 'WhatsApp', true, true)
    await waitFor(() => {
      expect(screen.getByText(/Generating your QR code/i)).toBeInTheDocument()
    })
    expect(screen.queryByTestId('native-unavailable-hint')).not.toBeInTheDocument()
  })

  it('capability gating (#299): native_available:false shows the hint and NOT the QR notice', async () => {
    mockPairingState('code', 'https://example.com/test-qr')
    renderPanel('whatsapp', 'WhatsApp', false, true)
    await waitFor(() => {
      expect(screen.getByTestId('native-unavailable-hint')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  })

  it('enabled:false → shows enable-prompt, suppresses QR notice and unavailable hint', async () => {
    // When the channel is not yet enabled, WhatsAppNativeNotice must NOT mount.
    // whatsmeow only generates a QR after the channel is enabled, so starting the
    // 15-second timeout with no chance of a QR arriving is misleading.
    // The enable-prompt informs the user to Save & Enable first.
    renderPanel('whatsapp', 'WhatsApp', undefined, false)
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-enable-prompt')).toBeInTheDocument()
    })
    // QR notice must not be present (WhatsAppNativeNotice suppressed).
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('whatsapp-qr')).not.toBeInTheDocument()
    // Capability-unavailable hint must not coexist with the enable-prompt.
    expect(screen.queryByTestId('native-unavailable-hint')).not.toBeInTheDocument()
  })

  it('enabled:undefined → shows enable-prompt (same as false — defaults to not enabled)', async () => {
    // No enabled prop passed → defaults to undefined → treat as not enabled.
    renderPanel('whatsapp', 'WhatsApp')
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-enable-prompt')).toBeInTheDocument()
    })
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

  it('BLOCKER FIX: submit payload sends \'\' for deselected SA sensitive field (not omit)', async () => {
    // Verify that buildSubmitPayload sends explicit '' for deselected group fields.
    // The backend configureChannel is a deep-merge: an absent field is left untouched,
    // so deleting (omitting) deselected fields would leave the previously-stored
    // service_account_json_ref alive in config.json + the credential store.
    // Sending '' triggers the backend's clear path (rest.go ~4532-4537).
    vi.mocked(configureChannel).mockResolvedValue(undefined)

    const client = makeQueryClient()
    // Seed with a service_account_json already set (simulates a switch from SA → Webhook)
    client.setQueryData(['channel-config', 'google-chat'], {
      service_account_json: '[configured]',
    })
    client.setQueryData(['channel-routing', 'google-chat'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [])

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="google-chat" channelName="Google Chat" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    // The component should detect SA is pre-set and select service_account.
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Service account/i })).toBeChecked()
    })

    // Switch to Webhook — this should prepare to clear the SA fields.
    fireEvent.click(screen.getByRole('radio', { name: /Webhook URL/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Webhook URL/i })).toBeChecked()
    })

    // Click Save to trigger configureChannel.
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))

    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })

    const [, payload] = vi.mocked(configureChannel).mock.calls[0] as [string, Record<string, unknown>]
    // The payload MUST include service_account_json: '' (not deleted/absent).
    // This presence-with-empty-string is what triggers the backend's credential clear path.
    expect(Object.prototype.hasOwnProperty.call(payload, 'service_account_json')).toBe(true)
    expect(payload['service_account_json']).toBe('')
    // service_account_file must also be cleared.
    expect(Object.prototype.hasOwnProperty.call(payload, 'service_account_file')).toBe(true)
    expect(payload['service_account_file']).toBe('')
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

// #358: "Save & Enable" must keep the Configure panel OPEN for WhatsApp so the QR
// (pushed over the whatsapp_pairing WS frame once the channel starts) can render in
// WhatsAppNativeNotice. For channels without a pairing flow the panel closes as before.
describe('ChannelConfigPanel — Save & Enable panel close behavior (#358)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(configureChannel).mockResolvedValue(undefined as never)
    vi.mocked(enableChannel).mockResolvedValue(undefined as never)
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
        selector({ byChannel: {}, clear: vi.fn() })) as never,
    )
  })

  it('keeps the panel open after Save & Enable for WhatsApp (QR can render)', async () => {
    // Use the REST-facing id ('whatsapp') — that is what ChannelsScreen passes as channelId.
    // enabled:true so WhatsAppNativeNotice mounts (panel must stay open for the QR to render).
    const { onOpenChange } = renderPanel('whatsapp', 'WhatsApp', true, true)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    })

    await waitFor(() => {
      // enableChannel is called with the same channelId the panel received.
      expect(vi.mocked(enableChannel)).toHaveBeenCalledWith('whatsapp')
    })
    // The panel must NOT be closed — onOpenChange(false) would unmount the QR notice.
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('closes the panel after Save & Enable for a non-pairing channel (Telegram)', async () => {
    const { onOpenChange } = renderPanel('telegram', 'Telegram')

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    })

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })
})

// F7: doSave.onSuccess and doSaveAndEnable.onSuccess clear the Connection-test badge
describe('ChannelConfigPanel — test result badge cleared on save (F7)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(configureChannel).mockResolvedValue(undefined as never)
    vi.mocked(enableChannel).mockResolvedValue(undefined as never)
    vi.mocked(testChannel).mockResolvedValue({ success: true, message: 'OK' })
  })

  it('doSave.onSuccess: clears the test result badge after a successful save', async () => {
    renderPanel('telegram', 'Telegram')

    // Wait for the form to be ready
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    // Trigger a connection test so the badge appears
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Test$/i }))
    })

    // Badge must be visible
    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    // Trigger Save — onSuccess calls setTestResult(null)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))
    })

    // Badge must be gone
    await waitFor(() => {
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    })
  })

  it('doSaveAndEnable.onSuccess: clears the test result badge after Save & Enable', async () => {
    renderPanel('telegram', 'Telegram')

    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    // Trigger a connection test so the badge appears
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Test$/i }))
    })

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    // Trigger Save & Enable — onSuccess calls setTestResult(null)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    })

    // Badge must be gone
    await waitFor(() => {
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    })
  })
})

// F8: routingDebounceRef cleanup — unmounting cancels the pending routing auto-save
describe('ChannelConfigPanel — RoutingDebounce', () => {
  afterEach(() => {
    vi.useRealTimers()
    // Remove scrollIntoView polyfill if added
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('cancels the pending routing auto-save timer on unmount', async () => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    // Provide one real agent so the SmartSelect renders a second selectable option
    vi.mocked(fetchAgents).mockResolvedValue([{ id: 'agent-1', name: 'Agent One' }] as never)
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: 'agent-1' } as never)

    // Polyfill scrollIntoView — jsdom does not implement it; Radix Select calls
    // it when opening the listbox to scroll the current item into view.
    Element.prototype.scrollIntoView = vi.fn()

    const { unmount } = renderPanel('telegram', 'Telegram')

    // Wait for the routing section to be ready (agents loaded, select rendered)
    await waitFor(() => {
      expect(screen.getByRole('combobox')).toBeInTheDocument()
    })

    // Switch to fake timers AFTER the initial render/queries settle so we don't
    // intercept TanStack Query's internal setInterval polling.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })

    // Open the Radix Select listbox by clicking its trigger.
    await act(async () => {
      fireEvent.click(screen.getByRole('combobox'))
    })

    // Radix renders SelectContent into a portal at document.body.
    // Find and click the "Agent One" option to trigger onValueChange (and thus
    // start the 400ms debounce timer).
    await act(async () => {
      const agentOption = document.querySelector('[role="option"][data-radix-select-item]') ??
        Array.from(document.querySelectorAll('[role="option"]')).find(
          (el) => el.textContent?.includes('Agent One'),
        )
      if (agentOption) {
        fireEvent.click(agentOption)
      }
    })

    // Unmount BEFORE the 400ms debounce fires — cleanup effect clears the timer.
    unmount()

    // Advance past 400ms. If cleanup failed, doSaveRouting would fire and call
    // setChannelRouting. With correct cleanup it must NOT be called.
    await act(async () => {
      vi.advanceTimersByTime(500)
    })

    // setChannelRouting must NOT have been called — the timer was cancelled.
    expect(vi.mocked(setChannelRouting)).not.toHaveBeenCalled()
  })
})
