/**
 * ConnectorsScreen.configure-1496.test.tsx
 *
 * T-05 — #1496: Configure opens (channels-spec-r8 T-05, S-71, FR-080; US-9 AS3).
 *
 * Oracle (spec): "Given the Connectors roster with Discord 'Not configured',
 * When the operator clicks Configure, Then the Discord configuration form is
 * visible (component test with the real roster data shape)."
 *
 * The real roster data shape is the backend's static placeholder row (no
 * instance_id) — the same shape the existing ConnectorsScreen tests model as
 * STUB_DISCORD_UNCONFIGURED. The panel is stubbed at this level (its own
 * internals are owned by ChannelConfigPanel.test.tsx); "the form is visible"
 * here is the panel actually opening for the bare discord type key — the
 * FR-080 acceptance.
 *
 * Expected verdict: POSITIVE CONTROL — this is the FR-080 regression pin. The
 * restored roster flow (ConnectorsScreen.tsx "Roster Configure — the restored
 * ORIGINAL flow") already opens the panel; the RED pack carries this test so
 * the cannot-open regression can never return silently. Reported as a
 * positive control in the RED report.
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@tanstack/react-router', () => ({
  createRootRoute: (opts: Record<string, unknown>) => ({ options: opts }),
  createFileRoute: () => (opts: Record<string, unknown>) => ({ options: opts }),
  Outlet: () => null,
  Link: ({ children, to, className }: { children: React.ReactNode; to: string; className?: string }) =>
    React.createElement('a', { href: to, className }, children),
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => vi.fn(),
  useParams: () => ({}),
  useSearch: () => ({}),
}))

const channelConfigPanelOpens: Array<{ channelId: string; channelName: string }> = []
vi.mock('@/components/skills/ChannelConfigPanel', () => ({
  ChannelConfigPanel: (props: { channelId: string; channelName: string; open: boolean }) => {
    if (!props.open) return null
    channelConfigPanelOpens.push({ channelId: props.channelId, channelName: props.channelName })
    return React.createElement(
      'div',
      { 'data-testid': `channel-config-panel-${props.channelId}` },
      `Configuring ${props.channelName}`,
    )
  },
}))

vi.mock('@/components/connectors/EmailMailboxPanel', () => ({
  EmailMailboxPanel: () => null,
}))

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

const mockFetchChannels = vi.fn()
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: mockFetchChannels,
    fetchChannelRouting: vi.fn().mockResolvedValue({}),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchWorkspace: vi.fn().mockResolvedValue({}),
    fetchAgents: vi.fn().mockResolvedValue([]),
    enableChannel: vi.fn(),
    disableChannel: vi.fn(),
    deleteChannelInstance: vi.fn(),
    createChannelInstance: vi.fn(),
    setChannelRouting: vi.fn(),
    isApiError: actual.isApiError,
    EMAIL_CHANNEL_ID: 'email',
  }
})

// The real roster row shape for a not-yet-configured Discord connection: a
// static placeholder entry with no instance_id (backend HandleChannels
// static-rows loop), rendered by the roster with the "Not configured" badge
// family (connectionStatusOf → 'unconfigured').
const DISCORD_NOT_CONFIGURED = {
  id: 'discord',
  name: 'Discord',
  transport: 'websocket' as const,
  enabled: false,
  description: 'Discord Gateway',
}

async function renderScreen() {
  // Dynamic import: the vi.mock factories above close over top-level consts,
  // which must be initialized before the first module graph import.
  const { ConnectorsScreen } = await import('./ConnectorsScreen')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ConnectorsScreen />
    </QueryClientProvider>,
  )
}

describe('ConnectorsScreen — #1496: Configure opens on a "Not configured" Discord row (S-71 / FR-080)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    channelConfigPanelOpens.length = 0
    // Fresh profile: the roster is the Discord placeholder and nothing else.
    mockFetchChannels.mockResolvedValue([DISCORD_NOT_CONFIGURED])
  })

  it('clicking Configure on the Not-configured Discord roster row opens the Discord configuration form', async () => {
    await renderScreen()

    const connectBtn = await screen.findByTestId('channel-roster-connect-discord')
    expect(connectBtn).toBeInTheDocument()
    fireEvent.click(connectBtn)

    const form = await screen.findByTestId('channel-config-panel-discord')
    // FR-080: the Discord configuration form must be visible after the click.
    expect(form).toBeInTheDocument()
    expect(channelConfigPanelOpens).toEqual([
      { channelId: 'discord', channelName: 'Discord' },
    ])
  })
})
