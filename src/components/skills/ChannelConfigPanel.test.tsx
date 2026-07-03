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
 *   7. Retry on timeout → restarts the channel, spinner shown while waiting; if
 *      no code frame arrives within 90s the UI reverts to timeout + Retry
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
 *
 *   Helper + link render (#322 / US-C1):
 *  18. helpText renders under field
 *  19. helpLink renders as an anchor
 *
 *   Non-whatsapp channels:
 *  20. No pairing notice for non-whatsapp channel
 *
 *   Client-side save validation (channel-Test redesign Stage 1 — the Test
 *   button was removed; required-field validation now blocks Save):
 *  21. Blank required field blocks Save; configureChannel not called
 *  22. Blank required field blocks Save & Enable; configureChannel/enableChannel not called
 *  23. Editing a field with an error clears that field's inline error
 *  24. The "[configured]" sentinel counts as filled — Save proceeds
 *  25. Google Chat: the selected auth-group's field is required even though
 *      neither gchat field carries required:true in the catalog; switching
 *      method switches which field is required
 *  26. WhatsApp (no required fields in the catalog): Save and Save & Enable
 *      are never blocked
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
    fetchWorkspaces: vi.fn(),
    fetchWorkspace: vi.fn(),
    configureChannel: vi.fn(),
    enableChannel: vi.fn(),
    disableChannel: vi.fn(),
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
  fetchWorkspaces,
  fetchWorkspace,
  configureChannel,
  enableChannel,
  disableChannel,
  setChannelRouting,
} from '@/lib/api'
import type { Workspace } from '@/lib/api'
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
  client.setQueryData(['workspaces', { status: 'active' }], [])

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
          // Pairing frames are keyed by INSTANCE id (post multi-instance) — the
          // panel under test renders channelId='whatsapp', so the store keys match.
          whatsapp: { status, qr, message: status === 'error' ? 'Auth failed' : '' },
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
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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

  it('NAMESPACED instance regression: the QR pairing UI renders for "whatsapp.sales" too', async () => {
    // Live-UAT regression (2026-07-03): isWhatsApp/hasPairingFlow used exact
    // `channelId === 'whatsapp'` matches, so every ADR-029 operator-created
    // instance ("whatsapp.<slug>") silently lost the entire pairing flow.
    // Pairing frames are keyed by the INSTANCE id, so the fixture keys match
    // the rendered channelId.
    vi.mocked(useWhatsAppPairingStore).mockImplementation(
      ((selector: (s: { byChannel: Record<string, unknown>; apply: () => void; clear: () => void }) => unknown) =>
        selector({
          byChannel: {
            'whatsapp.sales': { status: 'code', qr: 'https://example.com/ns-qr', message: '' },
          },
          apply: vi.fn(),
          clear: vi.fn(),
        })) as never,
    )
    renderPanel('whatsapp.sales', 'WhatsApp', undefined, true)
    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-qr')).toBeInTheDocument()
    })
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
  // Retry restarts the channel (disable → enable) to re-arm whatsmeow's QR
  // loop, then waits for a fresh `code` frame. If none arrives within 90s
  // (RETRY_TIMEOUT_MS — first QR can take ~60s after connect), the UI must
  // revert to the fallback state with the Retry affordance rather than
  // spinning forever.
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
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
    // Retry bounces the channel before waiting on a fresh code frame.
    vi.mocked(disableChannel).mockResolvedValue({} as never)
    vi.mocked(enableChannel).mockResolvedValue({} as never)
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

  it('Retry on timeout → spinner immediately; after 90s without code frame reverts to timeout + Retry', async () => {
    // Use real timers for initial render + interaction, then fake for the timer advance.
    vi.useRealTimers()

    // Seed timeout state. Pairing frames are keyed by INSTANCE id ('whatsapp').
    pairingByChannel['whatsapp'] = { status: 'timeout', qr: '', message: 'expired' }
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
      vi.advanceTimersByTime(91_000)
    })

    // The UI must revert to timeout + Retry — not an endless spinner.
    expect(screen.getByText(/QR expired/i)).toBeInTheDocument()
    expect(screen.getByTestId('whatsapp-retry')).toBeInTheDocument()
    expect(screen.queryByText(/Generating your QR code/i)).not.toBeInTheDocument()
  })

  it('Retry on error → spinner immediately; after 90s reverts to error + Retry', async () => {
    vi.useRealTimers()

    // Pairing frames are keyed by INSTANCE id ('whatsapp').
    pairingByChannel['whatsapp'] = { status: 'error', qr: '', message: 'auth failed' }
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
      vi.advanceTimersByTime(91_000)
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
      render(<WhatsAppNativeNotice channelId="whatsapp" />)
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

    const { unmount } = render(<WhatsAppNativeNotice channelId="whatsapp" />)

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
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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
    // service_account_file was removed from the catalog: the backend silently
    // strips filesystem-path fields on configure, so the field was a UI dead end.
    expect(document.getElementById('field-service_account_file')).toBeNull()
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

    // Webhook mode requires webhook_url (Stage 1 client-side validation — the
    // newly-selected auth group's field is required) — fill it so Save proceeds;
    // this test is about the DESELECTED SA field's clear payload, not webhook_url.
    fireEvent.change(document.getElementById('field-webhook_url')!, {
      target: { value: 'https://chat.googleapis.com/v1/spaces/new' },
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
    // service_account_file is no longer in the catalog (backend strips path
    // fields on configure anyway, so a '' "clear" for it never did anything).
    expect(Object.prototype.hasOwnProperty.call(payload, 'service_account_file')).toBe(false)
  })

  it('REGRESSION: never echoes routing-owned keys (identity/workspace_id) back through configure', async () => {
    // A create-time-bound instance's config contains its ADR-029 binding. The
    // configure endpoint REJECTS identity/workspace_id with 400 (binding must go
    // through PUT routing, which enforces CoreTeam membership) — so hydrating
    // them into formValues broke Save/Save & Enable for every bound instance
    // (live-UAT: the WhatsApp QR never appeared because enable always 400'd).
    vi.mocked(configureChannel).mockResolvedValue(undefined)

    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram.sales'], {
      type: 'telegram',
      enabled: false,
      // Non-blank: Bot Token is required (Stage 1 client-side validation) — this
      // test is about identity/workspace_id stripping, not required-field checks.
      token: 'existing-bound-token',
      identity: { kind: 'agent', id: 'mia' },
      workspace_id: 'ws-1',
    })
    client.setQueryData(['channel-routing', 'telegram.sales'], { default_agent_id: 'mia', workspace_id: 'ws-1' })
    client.setQueryData(['agents'], [])

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="telegram.sales" channelName="Telegram" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^Save$/ })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))

    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })
    const [, payload] = vi.mocked(configureChannel).mock.calls[0] as [string, Record<string, unknown>]
    expect(Object.prototype.hasOwnProperty.call(payload, 'identity')).toBe(false)
    expect(Object.prototype.hasOwnProperty.call(payload, 'workspace_id')).toBe(false)
  })
})

describe('ChannelConfigPanel — [configured] sentinel never round-trips (secret-overwrite fix)', () => {
  // GET redacts every stored sensitive field to the literal "[configured]"
  // (rest.go redactChannelConfig) and the form hydrates it verbatim — but
  // configureChannel treats ANY non-empty value as a NEW secret. Echoing the
  // sentinel back therefore overwrote the real credential with the literal
  // string "[configured]" on every Save that didn't retype the secret,
  // silently breaking the channel. The submit payload must OMIT untouched
  // sentinel fields (absent = keep stored secret, per channel_secret_ref_test).
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
    vi.mocked(configureChannel).mockResolvedValue(undefined)
  })

  function renderWithConfig(config: Record<string, unknown>) {
    vi.mocked(fetchChannelConfig).mockResolvedValue(config)
    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], config)
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [])
    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="telegram" channelName="Telegram" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )
  }

  it('Save on an unrelated edit OMITS the untouched "[configured]" secret', async () => {
    renderWithConfig({ type: 'telegram', enabled: true, token: '[configured]' })
    await waitFor(() => {
      expect((document.getElementById('field-token') as HTMLInputElement).value).toBe('[configured]')
    })

    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })

    const [, payload] = vi.mocked(configureChannel).mock.calls[0] as [string, Record<string, unknown>]
    // token must be ABSENT — absent means "keep the stored secret".
    expect(Object.prototype.hasOwnProperty.call(payload, 'token')).toBe(false)
    // Non-sensitive fields still travel.
    expect(payload['type']).toBe('telegram')
  })

  it('a newly-typed secret still submits normally', async () => {
    renderWithConfig({ type: 'telegram', enabled: true, token: '[configured]' })
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    fireEvent.change(document.getElementById('field-token')!, { target: { value: 'new-bot-token-123' } })
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })

    const [, payload] = vi.mocked(configureChannel).mock.calls[0] as [string, Record<string, unknown>]
    expect(payload['token']).toBe('new-bot-token-123')
  })

  it('a deliberately cleared NON-required secret ("") still submits so the backend revokes it', async () => {
    // Telegram's Bot Token is a REQUIRED field — Stage 1 client-side validation
    // (added alongside the Test-button removal) now blocks Save on any blank
    // required field, so a required secret can no longer be cleared-to-revoke
    // via Save; that is a deliberate consequence of "prevent saving incomplete
    // configs" and is flagged separately (required-secret revocation may need
    // a dedicated affordance in a later stage). Use Feishu's non-required
    // encrypt_key (advanced, password type) to exercise the underlying
    // buildSubmitPayload sentinel-clear behavior, which is unaffected by
    // required-ness.
    const config = {
      type: 'feishu',
      enabled: true,
      app_id: 'cli_existing',
      app_secret: '[configured]',
      encrypt_key: '[configured]',
    }
    vi.mocked(fetchChannelConfig).mockResolvedValue(config)
    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'feishu'], config)
    client.setQueryData(['channel-routing', 'feishu'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [])
    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="feishu" channelName="Feishu" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(document.getElementById('field-app_id')).not.toBeNull()
    })

    // encrypt_key is an advanced field — expand the disclosure to reach it.
    fireEvent.click(screen.getByRole('button', { name: /Advanced/i }))
    await waitFor(() => {
      expect(document.getElementById('field-encrypt_key')).not.toBeNull()
    })

    fireEvent.change(document.getElementById('field-encrypt_key')!, { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })

    const [, payload] = vi.mocked(configureChannel).mock.calls[0] as [string, Record<string, unknown>]
    // Presence-with-empty-string is the backend's credential-clear signal.
    expect(Object.prototype.hasOwnProperty.call(payload, 'encrypt_key')).toBe(true)
    expect(payload['encrypt_key']).toBe('')
    // The untouched app_secret sentinel is unrelated and must still be OMITTED.
    expect(Object.prototype.hasOwnProperty.call(payload, 'app_secret')).toBe(false)
  })

  it('a "[configured]" value in a plain TEXT field is NOT stripped — only password/textarea fields are ever redacted server-side', async () => {
    // redactChannelConfig (rest.go) only ever redacts password/textarea-typed
    // fields to the sentinel. Telegram's `allow_from` is a plain text field —
    // a user who legitimately types the literal string "[configured]" into it
    // must have that value travel to the backend untouched, not silently
    // dropped by the sentinel-strip logic.
    const config = { type: 'telegram', enabled: true, token: 'realtoken123', allow_from: '[configured]' }
    vi.mocked(fetchChannelConfig).mockResolvedValue(config)
    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], config)
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [])
    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="telegram" channelName="Telegram" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    // allow_from is an advanced TEXT field — expand Advanced to reach it.
    fireEvent.click(screen.getByRole('button', { name: /Advanced/i }))
    await waitFor(() => {
      expect((document.getElementById('field-allow_from') as HTMLInputElement).value).toBe('[configured]')
    })

    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })

    const [, payload] = vi.mocked(configureChannel).mock.calls[0] as [string, Record<string, unknown>]
    // A plain text field's literal "[configured]" must survive untouched.
    expect(payload['allow_from']).toBe('[configured]')
    // The password-typed token is unaffected by this test's assertion.
    expect(payload['token']).toBe('realtoken123')
  })
})

describe('ChannelConfigPanel — human-label errors + a11y (#326 / US-C4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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
})

describe('ChannelConfigPanel — helper + link render (#322 / US-C1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
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

    // Bot Token is required (Stage 1 client-side validation) — fill it so
    // Save & Enable proceeds; this test is about the panel-close behavior.
    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })
    fireEvent.change(document.getElementById('field-token')!, { target: { value: 'a-real-token' } })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    })

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })
})

// Stage 1 of the channel-Test redesign: the Test button + connection-test
// badge were removed entirely. Required-field validation now runs at save
// time and blocks Save/Save & Enable client-side instead.
describe('ChannelConfigPanel — client-side save validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchWorkspace).mockResolvedValue({} as Workspace)
    vi.mocked(configureChannel).mockResolvedValue(undefined as never)
    vi.mocked(enableChannel).mockResolvedValue(undefined as never)
  })

  it('blocks Save when a required field (Telegram token) is blank; configureChannel not called', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    renderPanel('telegram', 'Telegram')

    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))
    })

    await waitFor(() => {
      expect(screen.getByText(/Bot Token is required/i)).toBeInTheDocument()
    })
    expect(vi.mocked(configureChannel)).not.toHaveBeenCalled()
  })

  it('blocks Save & Enable when a required field is blank; configureChannel/enableChannel not called', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    renderPanel('telegram', 'Telegram')

    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    })

    await waitFor(() => {
      expect(screen.getByText(/Bot Token is required/i)).toBeInTheDocument()
    })
    expect(vi.mocked(configureChannel)).not.toHaveBeenCalled()
    expect(vi.mocked(enableChannel)).not.toHaveBeenCalled()
  })

  it('editing the token field clears its inline error', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    renderPanel('telegram', 'Telegram')

    await waitFor(() => {
      expect(document.getElementById('field-token')).not.toBeNull()
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))
    })
    await waitFor(() => {
      expect(screen.getByText(/Bot Token is required/i)).toBeInTheDocument()
    })

    fireEvent.change(document.getElementById('field-token')!, { target: { value: 'a-real-token' } })

    await waitFor(() => {
      expect(screen.queryByText(/Bot Token is required/i)).not.toBeInTheDocument()
    })
  })

  it('the "[configured]" sentinel counts as filled — Save proceeds without a validation error', async () => {
    // renderPanel() unconditionally seeds ['channel-config', id] with {} — use an
    // explicit client seed instead (matches the sentinel describe block's
    // renderWithConfig pattern above) so the pre-filled token value is stable.
    const config = { type: 'telegram', enabled: true, token: '[configured]' }
    vi.mocked(fetchChannelConfig).mockResolvedValue(config)
    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], config)
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [])

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="telegram" channelName="Telegram" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      expect((document.getElementById('field-token') as HTMLInputElement).value).toBe('[configured]')
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))
    })

    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })
    expect(screen.queryByText(/is required/i)).not.toBeInTheDocument()
    // Sentinel-strip behavior is covered by the dedicated describe block above —
    // only assert Save proceeded here, not the payload shape.
  })

  it('Google Chat webhook mode: blank webhook_url blocks Save; switching to service-account mode moves the requirement', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    renderPanel('google-chat', 'Google Chat')

    await waitFor(() => {
      const webhookRadio = screen.getByRole('radio', { name: /Webhook URL/i })
      expect(webhookRadio).toBeChecked()
    })

    // Webhook mode, blank webhook_url → Save is blocked with an inline error,
    // even though webhook_url does not carry required:true in the catalog —
    // the selected auth group's field is required by the pick-one rule.
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    })
    await waitFor(() => {
      expect(screen.getByText(/Webhook URL is required/i)).toBeInTheDocument()
    })
    expect(vi.mocked(configureChannel)).not.toHaveBeenCalled()

    // Switch to service-account mode — the requirement switches to
    // service_account_json; the webhook_url error must no longer be present
    // (the field itself unmounts when switching authGroup).
    fireEvent.click(screen.getByRole('radio', { name: /Service account/i }))
    await waitFor(() => {
      expect(screen.getByRole('radio', { name: /Service account/i })).toBeChecked()
    })
    expect(screen.queryByText(/Webhook URL is required/i)).not.toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
    })
    await waitFor(() => {
      expect(screen.getByText(/Service Account JSON is required/i)).toBeInTheDocument()
    })
    expect(vi.mocked(configureChannel)).not.toHaveBeenCalled()
  })

  it('WhatsApp has no required fields in the catalog — Save is never blocked', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    renderPanel('whatsapp', 'WhatsApp', undefined, false)

    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-enable-prompt')).toBeInTheDocument()
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/i }))
    })
    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
    })
    expect(screen.queryByText(/is required/i)).not.toBeInTheDocument()
  })

  it('WhatsApp has no required fields in the catalog — Save & Enable is never blocked', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    renderPanel('whatsapp', 'WhatsApp', undefined, false)

    await waitFor(() => {
      expect(screen.getByTestId('whatsapp-enable-prompt')).toBeInTheDocument()
    })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
    })
    await waitFor(() => {
      expect(vi.mocked(configureChannel)).toHaveBeenCalled()
      expect(vi.mocked(enableChannel)).toHaveBeenCalled()
    })
    expect(screen.queryByText(/is required/i)).not.toBeInTheDocument()
  })
})

// Workers (type:'worker') are delegation-only labour agents and can't be a
// channel routing default (the backend 400s). The routing "Default agent" picker
// must therefore omit them.
describe('ChannelConfigPanel — routing picker excludes workers', () => {
  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('does not offer a worker agent as a channel default; offers base agents (bound flow)', async () => {
    // In the bound flow (workspace selected), the agent select is enabled and filters
    // by core_team. Workers in core_team are still excluded (US-2 / FR-002).
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    // routing has workspace_id set → bound flow; agent select is enabled
    vi.mocked(getChannelRouting).mockResolvedValue({ workspace_id: 'sales', default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core' },
      { id: 'builder', name: 'Builder Worker', type: 'worker' },
    ] as never)
    vi.mocked(fetchWorkspaces).mockResolvedValue([{ id: 'sales', name: 'Sales' } as Workspace])
    // core_team includes mia and builder — builder is a worker so it should be excluded
    vi.mocked(fetchWorkspace).mockResolvedValue({
      id: 'sales',
      name: 'Sales',
      core_team: ['mia', 'builder'],
    } as Workspace)

    // jsdom lacks scrollIntoView; Radix Select calls it when opening the listbox.
    Element.prototype.scrollIntoView = vi.fn()

    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], {})
    client.setQueryData(['channel-routing', 'telegram'], { workspace_id: 'sales' })
    client.setQueryData(['agents'], [
      { id: 'mia', name: 'Mia', type: 'core' },
      { id: 'builder', name: 'Builder Worker', type: 'worker' },
    ])
    client.setQueryData(['workspaces', { status: 'active' }], [{ id: 'sales', name: 'Sales' }])
    client.setQueryData(['workspaces', 'sales'], { id: 'sales', name: 'Sales', core_team: ['mia', 'builder'] })

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel
          channelId="telegram"
          channelName="Telegram"
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>,
    )

    // Wait for agent select to be enabled (workspace loaded, bound flow)
    await waitFor(() => {
      const agentContainer = screen.getByTestId('routing-agent-select')
      const triggers = agentContainer.querySelectorAll('[role="combobox"], button[aria-haspopup="listbox"]')
      const notDisabled = Array.from(triggers).some(
        (el) => !(el as HTMLButtonElement | HTMLSelectElement).disabled,
      )
      expect(notDisabled).toBe(true)
    })

    // Open the select to inspect items
    const agentContainer = screen.getByTestId('routing-agent-select')
    const trigger = agentContainer.querySelector('[role="combobox"]') ??
      agentContainer.querySelector('button[aria-haspopup="listbox"]')
    await act(async () => {
      if (trigger) fireEvent.click(trigger)
    })

    await waitFor(() => {
      const optionEls = Array.from(document.querySelectorAll('[role="option"]'))
        .map((el) => el.textContent?.trim())
      // Mia (in core_team, non-worker) is offered.
      expect(optionEls.some((t) => t === 'Mia')).toBe(true)
      // Builder Worker (worker type) is NOT offered even though in core_team.
      expect(optionEls.some((t) => t === 'Builder Worker')).toBe(false)
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
    vi.mocked(getChannelRouting).mockResolvedValue({ workspace_id: 'ws-1', default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([{ id: 'agent-1', name: 'Agent One', type: 'core' }] as never)
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: 'agent-1' } as never)
    vi.mocked(fetchWorkspaces).mockResolvedValue([{ id: 'ws-1', name: 'WS One' } as Workspace])
    vi.mocked(fetchWorkspace).mockResolvedValue({ id: 'ws-1', name: 'WS One', core_team: ['agent-1'] } as Workspace)

    Element.prototype.scrollIntoView = vi.fn()

    // Build a client with workspace data pre-seeded (so the component immediately
    // gets the bound routing state without async round-trips)
    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], {})
    client.setQueryData(['channel-routing', 'telegram'], { workspace_id: 'ws-1', default_agent_id: undefined })
    client.setQueryData(['agents'], [{ id: 'agent-1', name: 'Agent One', type: 'core' }])
    client.setQueryData(['workspaces', { status: 'active' }], [{ id: 'ws-1', name: 'WS One' }])
    client.setQueryData(['workspaces', 'ws-1'], { id: 'ws-1', name: 'WS One', core_team: ['agent-1'] })

    const { unmount } = render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel channelId="telegram" channelName="Telegram" open={true} onOpenChange={vi.fn()} />
      </QueryClientProvider>,
    )

    // In bound flow with 1 agent in core_team, SmartSelect renders a combobox (Radix)
    // because items.length=1 ≤ SEARCHABLE_THRESHOLD=5. Wait for it to be enabled.
    await waitFor(() => {
      const agentContainer = screen.getByTestId('routing-agent-select')
      const trigger = agentContainer.querySelector('[role="combobox"]')
      expect(trigger).not.toBeNull()
      expect((trigger as HTMLButtonElement).disabled).toBe(false)
    })

    // Switch to fake timers AFTER initial queries settle.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })

    // Open the select and pick the agent to start the debounce timer.
    const agentContainer = screen.getByTestId('routing-agent-select')
    const trigger = agentContainer.querySelector('[role="combobox"]')
    await act(async () => {
      if (trigger) fireEvent.click(trigger)
    })
    await act(async () => {
      const option = document.querySelector('[role="option"]')
      if (option) fireEvent.click(option)
    })

    // Unmount BEFORE the 400ms debounce fires — cleanup effect clears the timer.
    unmount()

    await act(async () => {
      vi.advanceTimersByTime(500)
    })

    // setChannelRouting must NOT have been called — the timer was cancelled.
    expect(vi.mocked(setChannelRouting)).not.toHaveBeenCalled()
  })
})

// ── Workspace-scoped routing UX (US-1/2/3, FR-001/002/003/004/009) ──────────
//
// These tests cover the new workspace selector + filtered agent picker +
// mandatory agent hint, wired on feat/channel-workspace-binding.
//
// Test data
const WS_SALES: Workspace = {
  id: 'sales',
  name: 'Sales',
  status: 'active',
  core_team: ['mia', 'ray'],
  task_count: 0,
  pinned: false,
  pin_order: 0,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
} as unknown as Workspace

const WS_EMPTY: Workspace = {
  id: 'empty-ws',
  name: 'Empty Workspace',
  status: 'active',
  core_team: [],
  task_count: 0,
  pinned: false,
  pin_order: 0,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
} as unknown as Workspace

const AGENT_MIA = { id: 'mia', name: 'Mia', type: 'core', default: true } as unknown as Workspace
const AGENT_RAY = { id: 'ray', name: 'Ray', type: 'core', default: false } as unknown as Workspace
const AGENT_JIM = { id: 'jim', name: 'Jim', type: 'core', default: false } as unknown as Workspace
const AGENT_W1 = { id: 'worker1', name: 'Worker One', type: 'Subagent', default: false } as unknown as Workspace

// Helper to render the panel with workspace routing context
function renderWithWorkspace(opts: {
  routing?: { workspace_id?: string; default_agent_id?: string }
  agents?: unknown[]
  workspaces?: Workspace[]
  workspaceDetail?: Workspace
}) {
  const {
    routing = {},
    agents = [AGENT_MIA, AGENT_RAY, AGENT_JIM, AGENT_W1],
    workspaces = [WS_SALES],
    workspaceDetail = WS_SALES,
  } = opts

  vi.mocked(fetchChannelConfig).mockResolvedValue({})
  vi.mocked(getChannelRouting).mockResolvedValue(routing)
  vi.mocked(fetchAgents).mockResolvedValue(agents as never)
  vi.mocked(fetchWorkspaces).mockResolvedValue(workspaces)
  vi.mocked(fetchWorkspace).mockResolvedValue(workspaceDetail)

  const client = makeQueryClient()
  client.setQueryData(['channel-config', 'telegram'], {})
  client.setQueryData(['channel-routing', 'telegram'], routing)
  client.setQueryData(['agents'], agents)
  client.setQueryData(['workspaces', { status: 'active' }], workspaces)
  // Pre-seed workspace detail so the agent filter is immediately available
  if (workspaceDetail && routing.workspace_id) {
    client.setQueryData(['workspaces', routing.workspace_id], workspaceDetail)
  }
  if (workspaceDetail && workspaceDetail.id) {
    client.setQueryData(['workspaces', workspaceDetail.id], workspaceDetail)
  }

  return render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId="telegram"
        channelName="Telegram"
        open={true}
        onOpenChange={vi.fn()}
      />
    </QueryClientProvider>,
  )
}

describe('ChannelConfigPanel — workspace selector renders (US-1 / FR-001)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchWorkspaces).mockResolvedValue([WS_SALES])
    vi.mocked(fetchWorkspace).mockResolvedValue(WS_SALES)
  })

  it('renders a workspace selector in the Routing section', async () => {
    renderWithWorkspace({})
    await waitFor(() =>
      expect(screen.getByTestId('routing-workspace-select')).toBeInTheDocument(),
    )
  })

  it('renders the agent selector container', async () => {
    renderWithWorkspace({})
    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-select')).toBeInTheDocument(),
    )
  })

  it('agent selector is disabled when no workspace is selected', async () => {
    renderWithWorkspace({ routing: {} })
    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-select')).toBeInTheDocument(),
    )
    const container = screen.getByTestId('routing-agent-select')
    const selects = container.querySelectorAll('select')
    const buttons = container.querySelectorAll('button')
    const allInteractive = [...selects, ...buttons]
    expect(allInteractive.length).toBeGreaterThan(0)
    const allDisabled = allInteractive.every(
      (el) => (el as HTMLSelectElement | HTMLButtonElement).disabled,
    )
    expect(allDisabled).toBe(true)
  })

  it('does not show routing-agent-required-hint when no workspace is selected', async () => {
    renderWithWorkspace({ routing: {} })
    await waitFor(() =>
      expect(screen.getByTestId('routing-workspace-select')).toBeInTheDocument(),
    )
    expect(screen.queryByTestId('routing-agent-required-hint')).not.toBeInTheDocument()
  })

  it('initialises workspace and agent from existing routing on panel open', async () => {
    renderWithWorkspace({
      routing: { workspace_id: 'sales', default_agent_id: 'ray' },
      workspaceDetail: WS_SALES,
    })
    // When both are set, the required-hint must NOT appear
    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-select')).toBeInTheDocument(),
    )
    await waitFor(() =>
      expect(screen.queryByTestId('routing-agent-required-hint')).not.toBeInTheDocument(),
    )
  })
})

describe('ChannelConfigPanel — agent filtering (US-2 / FR-002)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
  })

  it('shows only workspace core_team members (non-workers) in bound flow', async () => {
    // Sales has core_team=[mia, ray]; jim and worker1 are NOT in it.
    // With workspace selected, the agent select is enabled (not disabled).
    // We open the select to verify its option items (Radix Select renders options
    // only when the popover is open or when SelectContent has been mounted).
    Element.prototype.scrollIntoView = vi.fn()
    renderWithWorkspace({
      routing: { workspace_id: 'sales' },
      workspaceDetail: WS_SALES,
      agents: [AGENT_MIA, AGENT_RAY, AGENT_JIM, AGENT_W1],
    })

    // Wait for the agent select to be enabled (workspace data loaded)
    await waitFor(() => {
      const agentContainer = screen.getByTestId('routing-agent-select')
      // In bound flow the agent select must NOT be disabled
      const triggers = agentContainer.querySelectorAll('[role="combobox"], button[aria-haspopup="listbox"]')
      const notDisabled = Array.from(triggers).some(
        (el) => !(el as HTMLButtonElement | HTMLSelectElement).disabled,
      )
      expect(notDisabled).toBe(true)
    })

    // Open the agent select (bound flow → select is enabled)
    const agentContainer = screen.getByTestId('routing-agent-select')
    const trigger = agentContainer.querySelector('[role="combobox"]') ??
      agentContainer.querySelector('button[aria-haspopup="listbox"]')
    await act(async () => {
      if (trigger) fireEvent.click(trigger)
    })

    // Items render into a portal or into the document; check by text
    await waitFor(() => {
      // Mia and Ray should be present as option items
      const optionEls = Array.from(document.querySelectorAll('[role="option"]'))
        .map((el) => el.textContent?.trim())
      expect(optionEls.some((t) => t === 'Mia')).toBe(true)
      expect(optionEls.some((t) => t === 'Ray')).toBe(true)
      // Jim (not in team) and Worker One (worker) must NOT be present
      expect(optionEls.some((t) => t === 'Jim')).toBe(false)
      expect(optionEls.some((t) => t === 'Worker One')).toBe(false)
    })
  })

  it('excludes worker agents even when they appear in core_team', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    const wsWithWorker = { ...WS_SALES, core_team: ['mia', 'worker1'] }
    renderWithWorkspace({
      routing: { workspace_id: 'sales' },
      workspaceDetail: wsWithWorker as Workspace,
      agents: [AGENT_MIA, AGENT_W1],
    })

    // Wait for agent select to be enabled with 1 filtered item (mia only)
    await waitFor(() => {
      const agentContainer = screen.getByTestId('routing-agent-select')
      const triggers = agentContainer.querySelectorAll('[role="combobox"], button[aria-haspopup="listbox"]')
      const notDisabled = Array.from(triggers).some(
        (el) => !(el as HTMLButtonElement | HTMLSelectElement).disabled,
      )
      expect(notDisabled).toBe(true)
    })

    const agentContainer = screen.getByTestId('routing-agent-select')
    const trigger = agentContainer.querySelector('[role="combobox"]') ??
      agentContainer.querySelector('button[aria-haspopup="listbox"]')
    await act(async () => {
      if (trigger) fireEvent.click(trigger)
    })

    await waitFor(() => {
      const optionEls = Array.from(document.querySelectorAll('[role="option"]'))
        .map((el) => el.textContent?.trim())
      expect(optionEls.some((t) => t === 'Mia')).toBe(true)
      expect(optionEls.some((t) => t === 'Worker One')).toBe(false)
    })
  })
})

describe('ChannelConfigPanel — empty core_team (US-2 / FR-009)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
  })

  it('shows "add a member first" hint when workspace has empty core_team', async () => {
    renderWithWorkspace({
      routing: { workspace_id: 'empty-ws' },
      workspaces: [WS_EMPTY],
      workspaceDetail: WS_EMPTY,
    })

    await waitFor(() =>
      expect(screen.getByTestId('routing-empty-core-team-hint')).toBeInTheDocument(),
    )
    expect(screen.getByTestId('routing-empty-core-team-hint')).toHaveTextContent(
      /add a member to this workspace first/i,
    )
  })

  it('does NOT call setChannelRouting when core_team is empty', async () => {
    renderWithWorkspace({
      routing: { workspace_id: 'empty-ws' },
      workspaces: [WS_EMPTY],
      workspaceDetail: WS_EMPTY,
    })

    await waitFor(() =>
      expect(screen.getByTestId('routing-empty-core-team-hint')).toBeInTheDocument(),
    )
    expect(vi.mocked(setChannelRouting)).not.toHaveBeenCalled()
  })
})

describe('ChannelConfigPanel — agent required hint (US-3 / FR-004)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
  })

  it('shows routing-agent-required-hint when workspace is selected but no agent chosen', async () => {
    renderWithWorkspace({
      routing: { workspace_id: 'sales' },
      workspaceDetail: WS_SALES,
    })

    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-required-hint')).toBeInTheDocument(),
    )
    expect(screen.getByTestId('routing-agent-required-hint')).toHaveTextContent(
      /select an agent from this workspace to enable routing/i,
    )
  })

  it('does NOT call setChannelRouting when workspace is selected but agent is empty', async () => {
    renderWithWorkspace({
      routing: { workspace_id: 'sales' },
      workspaceDetail: WS_SALES,
    })

    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-required-hint')).toBeInTheDocument(),
    )
    await waitFor(() => expect(vi.mocked(fetchWorkspace)).toHaveBeenCalledWith('sales'))
    expect(vi.mocked(setChannelRouting)).not.toHaveBeenCalled()
  })

  it('does NOT show hint when both workspace and agent are set (valid state)', async () => {
    renderWithWorkspace({
      routing: { workspace_id: 'sales', default_agent_id: 'ray' },
      workspaceDetail: WS_SALES,
    })

    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-select')).toBeInTheDocument(),
    )
    expect(screen.queryByTestId('routing-agent-required-hint')).not.toBeInTheDocument()
  })
})

describe('ChannelConfigPanel — no global default in bound flow (US-3 / FR-003)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
  })

  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('does not show (Global default) option when workspace is selected', async () => {
    // jsdom lacks scrollIntoView; Radix Select calls it when opening the listbox.
    Element.prototype.scrollIntoView = vi.fn()

    renderWithWorkspace({
      routing: { workspace_id: 'sales' },
      workspaceDetail: WS_SALES,
    })

    // Wait for the agent select to be enabled (workspace data loaded → bound flow)
    await waitFor(() => {
      const agentContainer = screen.getByTestId('routing-agent-select')
      const triggers = agentContainer.querySelectorAll('[role="combobox"], button[aria-haspopup="listbox"]')
      const notDisabled = Array.from(triggers).some(
        (el) => !(el as HTMLButtonElement | HTMLSelectElement).disabled,
      )
      expect(notDisabled).toBe(true)
    })

    // Open the Radix Select — options only render when the popover is open.
    const agentContainer = screen.getByTestId('routing-agent-select')
    const trigger =
      agentContainer.querySelector('[role="combobox"]') ??
      agentContainer.querySelector('button[aria-haspopup="listbox"]')
    await act(async () => {
      if (trigger) fireEvent.click(trigger)
    })

    // Assert on [role="option"] items rendered into the portal — (Global default)
    // must NOT appear as an option in the bound flow.
    await waitFor(() => {
      const optionEls = Array.from(document.querySelectorAll('[role="option"]'))
        .map((el) => el.textContent?.trim())
      expect(optionEls.some((t) => t?.includes('Global default'))).toBe(false)
      // The bound flow should offer at least the workspace members (Mia, Ray).
      expect(optionEls.some((t) => t === 'Mia' || t === 'Ray')).toBe(true)
    })
  })

  it('shows (Global default) placeholder in the unbound flow', async () => {
    // In the unbound flow (no workspace selected), the agent select is disabled.
    // Radix Select's content (options) only renders when the popover is open,
    // and a disabled select cannot be opened. Instead, verify the placeholder
    // "(Global default)" is shown in the trigger text, confirming the unbound
    // path is active (not the bound flow with mandatory selection).
    renderWithWorkspace({ routing: {} })

    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-select')).toBeInTheDocument(),
    )

    // The unbound flow agent select should NOT show routing-agent-required-hint
    // (that only appears in the bound flow when no agent is chosen)
    expect(screen.queryByTestId('routing-agent-required-hint')).not.toBeInTheDocument()
    // The placeholder "(Global default)" should be visible in the disabled trigger
    const agentContainer = screen.getByTestId('routing-agent-select')
    expect(agentContainer.textContent).toContain('(Global default)')
  })
})

// ── Finding #1: workspace fetch error surface ──────────────────────────────────
//
// When fetchWorkspace rejects (network/500/transient) for a bound channel the
// component must render a DISTINCT error state (routing-workspace-load-error)
// rather than silently leaving the agent picker empty.  The agent picker must
// NOT render in this state (it would appear empty with no indication of why).

describe('ChannelConfigPanel — workspace fetch error surface (Finding #1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: undefined } as never)
  })

  it('shows routing-workspace-load-error when fetchWorkspace rejects in bound flow', async () => {
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ workspace_id: 'sales', default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([AGENT_MIA, AGENT_RAY] as never)
    vi.mocked(fetchWorkspaces).mockResolvedValue([WS_SALES])
    // fetchWorkspace rejects — simulates network error or 500
    vi.mocked(fetchWorkspace).mockRejectedValue(new Error('Network error'))

    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], {})
    client.setQueryData(['channel-routing', 'telegram'], { workspace_id: 'sales', default_agent_id: undefined })
    client.setQueryData(['agents'], [AGENT_MIA, AGENT_RAY])
    client.setQueryData(['workspaces', { status: 'active' }], [WS_SALES])
    // Do NOT pre-seed workspace detail — let the query run and fail

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel
          channelId="telegram"
          channelName="Telegram"
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>,
    )

    // The distinct error state must appear
    await waitFor(() =>
      expect(screen.getByTestId('routing-workspace-load-error')).toBeInTheDocument(),
      { timeout: 3000 },
    )
    expect(screen.getByTestId('routing-workspace-load-error')).toHaveTextContent(
      /Couldn't load workspace members/i,
    )
    // The Retry button must be in the error message
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    // The agent picker must NOT render silently empty
    expect(screen.queryByTestId('routing-agent-select')).not.toBeInTheDocument()
    // The required-agent hint must NOT also appear (separate error state covers this)
    expect(screen.queryByTestId('routing-agent-required-hint')).not.toBeInTheDocument()
  })

  it('does not show routing-workspace-load-error in unbound flow (fetchWorkspace not called)', async () => {
    // When no workspace is selected, fetchWorkspace is disabled — error state must not appear.
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(fetchAgents).mockResolvedValue([AGENT_MIA] as never)
    vi.mocked(fetchWorkspaces).mockResolvedValue([WS_SALES])
    vi.mocked(fetchWorkspace).mockRejectedValue(new Error('Should not be called'))

    renderWithWorkspace({ routing: {} })

    await waitFor(() =>
      expect(screen.getByTestId('routing-agent-select')).toBeInTheDocument(),
    )
    expect(screen.queryByTestId('routing-workspace-load-error')).not.toBeInTheDocument()
  })
})

// ── Finding #2: unbind flow (bound → "No workspace" → PUT fires) ───────────────
//
// Selecting "No workspace" on a currently-bound channel must fire a PUT with no
// workspace_id and no default_agent_id so the backend clears the binding.
// Conversely, selecting "No workspace" when already unbound must NOT fire a PUT.

describe('ChannelConfigPanel — unbind flow (Finding #2 / W1-6)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(fetchAgents).mockResolvedValue([AGENT_MIA, AGENT_RAY] as never)
    vi.mocked(fetchWorkspaces).mockResolvedValue([WS_SALES])
    vi.mocked(fetchWorkspace).mockResolvedValue(WS_SALES)
  })

  afterEach(() => {
    vi.useRealTimers()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('fires PUT with no workspace_id/default_agent_id when unselecting workspace from bound state', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    vi.mocked(getChannelRouting).mockResolvedValue({ workspace_id: 'sales', default_agent_id: 'ray' })
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: undefined } as never)

    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], {})
    client.setQueryData(['channel-routing', 'telegram'], { workspace_id: 'sales', default_agent_id: 'ray' })
    client.setQueryData(['agents'], [AGENT_MIA, AGENT_RAY])
    client.setQueryData(['workspaces', { status: 'active' }], [WS_SALES])
    client.setQueryData(['workspaces', 'sales'], WS_SALES)

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel
          channelId="telegram"
          channelName="Telegram"
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>,
    )

    // Wait for the workspace selector to be present
    await waitFor(() =>
      expect(screen.getByTestId('routing-workspace-select')).toBeInTheDocument(),
    )

    // The channel starts bound (workspace_id='sales') — select "No workspace" to unbind.
    // SmartSelect renders a native <select> for small item counts.
    const wsContainer = screen.getByTestId('routing-workspace-select')
    const nativeSelect = wsContainer.querySelector('select')
    const combobox = wsContainer.querySelector('[role="combobox"]')

    if (nativeSelect) {
      fireEvent.change(nativeSelect, { target: { value: '__none__' } })
    } else if (combobox) {
      fireEvent.click(combobox)
      await waitFor(() => {
        const option = Array.from(document.querySelectorAll('[role="option"]')).find(
          (el) => el.textContent?.includes('No workspace'),
        )
        expect(option).toBeTruthy()
      })
      const option = Array.from(document.querySelectorAll('[role="option"]')).find(
        (el) => el.textContent?.includes('No workspace'),
      )
      if (option) fireEvent.click(option)
    }

    // After the debounce fires, setChannelRouting must have been called with no
    // workspace_id and no default_agent_id (unbind PUT shape).
    await waitFor(() =>
      expect(vi.mocked(setChannelRouting)).toHaveBeenCalled(),
      { timeout: 2000 },
    )

    const [chanId, payload] = vi.mocked(setChannelRouting).mock.calls[0]
    expect(chanId).toBe('telegram')
    // The unbind PUT must have no workspace_id (undefined or absent)
    expect(payload.workspace_id).toBeUndefined()
    // The unbind PUT must have no default_agent_id (undefined or absent)
    expect(payload.default_agent_id).toBeUndefined()
  })

  it('does NOT fire PUT when selecting "No workspace" when already unbound', async () => {
    // Already unbound (no workspace_id in routing) → selecting __none__ again is a no-op.
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: undefined } as never)

    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], {})
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [AGENT_MIA, AGENT_RAY])
    client.setQueryData(['workspaces', { status: 'active' }], [WS_SALES])

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel
          channelId="telegram"
          channelName="Telegram"
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>,
    )

    await waitFor(() =>
      expect(screen.getByTestId('routing-workspace-select')).toBeInTheDocument(),
    )

    // Select __none__ on an already-unbound channel — should be a no-op
    const wsContainer = screen.getByTestId('routing-workspace-select')
    const nativeSelect = wsContainer.querySelector('select')
    if (nativeSelect) {
      fireEvent.change(nativeSelect, { target: { value: '__none__' } })
    }

    // Wait to confirm no PUT fires (beyond the 400ms debounce)
    await new Promise((r) => setTimeout(r, 500))
    expect(vi.mocked(setChannelRouting)).not.toHaveBeenCalled()
  })

  it('UI reflects unbound state after unbind (agent select disabled, required hint gone)', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    vi.mocked(getChannelRouting).mockResolvedValue({ workspace_id: 'sales', default_agent_id: 'ray' })
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: undefined } as never)

    const client = makeQueryClient()
    client.setQueryData(['channel-config', 'telegram'], {})
    client.setQueryData(['channel-routing', 'telegram'], { workspace_id: 'sales', default_agent_id: 'ray' })
    client.setQueryData(['agents'], [AGENT_MIA, AGENT_RAY])
    client.setQueryData(['workspaces', { status: 'active' }], [WS_SALES])
    client.setQueryData(['workspaces', 'sales'], WS_SALES)

    render(
      <QueryClientProvider client={client}>
        <ChannelConfigPanel
          channelId="telegram"
          channelName="Telegram"
          open={true}
          onOpenChange={vi.fn()}
        />
      </QueryClientProvider>,
    )

    await waitFor(() =>
      expect(screen.getByTestId('routing-workspace-select')).toBeInTheDocument(),
    )

    // Unbind: select "No workspace"
    const wsContainer = screen.getByTestId('routing-workspace-select')
    const nativeSelect = wsContainer.querySelector('select')
    const combobox = wsContainer.querySelector('[role="combobox"]')

    if (nativeSelect) {
      fireEvent.change(nativeSelect, { target: { value: '__none__' } })
    } else if (combobox) {
      fireEvent.click(combobox)
      await waitFor(() => {
        const option = Array.from(document.querySelectorAll('[role="option"]')).find(
          (el) => el.textContent?.includes('No workspace'),
        )
        expect(option).toBeTruthy()
      })
      const option = Array.from(document.querySelectorAll('[role="option"]')).find(
        (el) => el.textContent?.includes('No workspace'),
      )
      if (option) fireEvent.click(option)
    }

    // After unbind: agent select must be DISABLED (unbound flow)
    await waitFor(() => {
      const agentContainer = screen.getByTestId('routing-agent-select')
      const allInteractive = [
        ...Array.from(agentContainer.querySelectorAll('select')),
        ...Array.from(agentContainer.querySelectorAll('button')),
      ]
      expect(allInteractive.length).toBeGreaterThan(0)
      const allDisabled = allInteractive.every(
        (el) => (el as HTMLSelectElement | HTMLButtonElement).disabled,
      )
      expect(allDisabled).toBe(true)
    })

    // The required-agent hint must NOT be shown (we're back in unbound flow)
    expect(screen.queryByTestId('routing-agent-required-hint')).not.toBeInTheDocument()
  })
})
