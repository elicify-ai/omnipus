/**
 * ChannelConfigPanel.enable-failure.test.tsx
 *
 * Issue #1568 — the Connectors panel after an enable whose start fails.
 *
 * Oracle (issue #1568 "Expected", never current behaviour): "Enable reports a
 * start failure truthfully (for example the same 'not running: <reason>'
 * wording the Configure outcome uses), never a plain success while the
 * adapter is not running." On the panel side this means: when the enable call
 * reports a start failure, the panel must NOT show the success toast
 * "Channel configured and enabled" and must show the failure instead. The
 * failure wording asserted here is the server's own reason text passed
 * through the existing enable error path (the exact server wording is the
 * fix's choice; this test pins the pass-through, not a phrasing).
 *
 * Flow per channels spec T-6c (the issue's stated premise): the save on a
 * disabled/new connection answers `no_reload_needed`, the panel then calls
 * enable, and the enable call reports its own result. Discord is the fixture
 * channel (the issue's connector; the non-pairing toast path —
 * "Channel configured and enabled" — is the wording the issue quotes).
 *
 * Expected GREEN control status: today's panel already shows a rejected
 * enable's error text and suppresses the success toast, so this file is a
 * control that must stay true through the #1568 backend fix — it fails only
 * if a regression ever toasts plain success over a reported start failure.
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/store/connection', () => {
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
    fetchChannelRouting: vi.fn(),
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

import { useUiStore } from '@/store/ui'
import {
  fetchChannelConfig,
  fetchChannelRouting,
  configureChannel,
  enableChannel,
  isApiError,
} from '@/lib/api'
import { ChannelConfigPanel } from './ChannelConfigPanel'

// Synthetic fixture value, not a usable secret.
const SYNTHETIC_TOKEN = 'qa-ta-1568-synthetic-bot-token'
// The enable response's start-failure reason, fed through the error path the
// fixed backend exercises. Wordings are chosen to survive
// mapErrorsToHumanLabels for discord (no field-key collisions) so the
// assertion pins verbatim pass-through.
const START_FAILURE_TEXT =
  'The connection is not running: the platform rejected the credential (401).'
// Issue #1568: the success wording that must never appear over a start failure.
const FORBIDDEN_SUCCESS_TEXT = 'Channel configured and enabled'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

// The routing mock must match the store's entry type.
const EMPTY_ROUTING = { default_agent_id: '', workspace_id: '', bound: false }

function renderDiscordPanel() {
  const addToast = vi.fn()
  const onOpenChange = vi.fn()
  vi.mocked(useUiStore).mockReturnValue({ addToast } as never)
  const client = makeQueryClient()
  client.setQueryData(['channel-config', 'discord'], {} as Record<string, unknown>)
  client.setQueryData(['channel-routing', 'discord'], EMPTY_ROUTING)
  client.setQueryData(['agents'], [])
  client.setQueryData(['workspaces', { status: 'active' }], [])
  render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId="discord"
        channelName="Discord"
        enabled={false}
        open={true}
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>,
  )
  return { addToast, onOpenChange }
}

async function fillTokenAndClickSaveAndEnable() {
  await waitFor(() => {
    expect(screen.getByLabelText(/Bot Token/i)).toBeInTheDocument()
  })
  await act(async () => {
    fireEvent.change(screen.getByLabelText(/Bot Token/i), { target: { value: SYNTHETIC_TOKEN } })
  })
  await waitFor(() => {
    expect(screen.getByRole('button', { name: /Save & Enable/i })).toBeInTheDocument()
  })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /Save & Enable/i }))
  })
}

beforeEach(() => {
  // T-6c premise: the save on a (new/disabled) connection answers
  // no_reload_needed, so the panel proceeds to enable.
  vi.mocked(configureChannel).mockResolvedValue({
    apply_outcome: 'no_reload_needed',
  } as never)
  vi.mocked(fetchChannelConfig).mockResolvedValue({})
  vi.mocked(fetchChannelRouting).mockResolvedValue(EMPTY_ROUTING as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
  vi.clearAllMocks()
})

describe('ChannelConfigPanel — enable reports a start failure (#1568)', () => {
  it('shows the failure and never "Channel configured and enabled" when the enable start fails', async () => {
    // The fixed enable route reports the start failure; the panel receives it
    // as the enable call's rejection carrying the server text (userMessage),
    // the same shape every other enable refusal (404/409/500) rides in on.
    vi.mocked(enableChannel).mockRejectedValue(
      Object.assign(new Error(START_FAILURE_TEXT), { userMessage: START_FAILURE_TEXT }),
    )
    vi.mocked(isApiError).mockReturnValue(true)
    const { addToast, onOpenChange } = renderDiscordPanel()

    await fillTokenAndClickSaveAndEnable()

    // The enable ran (T-6c: no_reload_needed → enable), exactly once, bodyless.
    await waitFor(() => {
      expect(vi.mocked(enableChannel).mock.calls).toEqual([['discord']])
    })
    await act(async () => {})

    const toasts = addToast.mock.calls.map((c) => c[0] as { message: string; variant: string })
    const messages = toasts.map((t) => t.message)
    // ORACLE #1568: never the plain success toast while the adapter is down.
    expect(messages, `the success toast must not appear over a start failure; got: ${JSON.stringify(messages)}`)
      .not.toContain(FORBIDDEN_SUCCESS_TEXT)
    // ORACLE #1568: the failure is shown — the server's reason text passes
    // through, as an error.
    expect(messages, `the start failure must be shown; got: ${JSON.stringify(messages)}`)
      .toContain(START_FAILURE_TEXT)
    const failureToast = toasts.find((t) => t.message === START_FAILURE_TEXT)
    expect(failureToast?.variant).toBe('error')
    // A reported failure is not a success: the panel must not close itself.
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })
})
