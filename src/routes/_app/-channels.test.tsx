/**
 * -channels.test.tsx — vitest unit tests for the Channels screen and
 * ChannelConfigPanel Routing section.
 *
 * Covers:
 *   1. ChannelsScreen — feed renders from fetchChannels mock; loading/error/empty states;
 *      clicking Configure opens the panel; Enable/Disable calls the right mutation.
 *   2. ChannelConfigPanel Routing section — renders with correct labels; loads routing
 *      via pre-seeded query data; mutation logic verified structurally.
 *
 * Design: ChannelsScreen lives in a TanStack Router route file. We exercise the same
 * conditional renders via a lightweight stub. ChannelConfigPanel is imported directly.
 *
 * Traces to: sprint/258-jun-2026 — "Channels feed + ChannelConfigPanel Routing section".
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery, useMutation, useQueryClient } from '@tanstack/react-query'

// ── Mocks declared before any SPA imports ─────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: vi.fn(),
    enableChannel: vi.fn(),
    disableChannel: vi.fn(),
    isApiError: vi.fn(() => false),
    fetchChannelConfig: vi.fn(),
    fetchChannelRouting: vi.fn(),
    setChannelRouting: vi.fn(),
    fetchAgents: vi.fn(),
    configureChannel: vi.fn(),
    // ADR-031 Track 2 — ConnectorsScreen resolves each configured instance's
    // workspace→agent binding via these (same helpers ChannelConfigPanel
    // uses); mock them explicitly so Section 3 (real ConnectorsScreen) never
    // falls through to a real fetch() in Node.
    fetchWorkspaces: vi.fn(),
    createChannelInstance: vi.fn(),
    deleteChannelInstance: vi.fn(),
  }
})

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => opts,
    useNavigate: () => vi.fn(),
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

vi.mock('@/store/connection', () => ({
  useConnectionStore: vi.fn((selector: (s: { isConnected: boolean; connection: null }) => unknown) =>
    selector({ isConnected: false, connection: null }),
  ),
}))

vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: vi.fn(
    (selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
      selector({ byChannel: {}, clear: vi.fn() }),
  ),
}))

// Import mocked modules after vi.mock declarations.
import { useUiStore } from '@/store/ui'
import {
  fetchChannels,
  enableChannel,
  disableChannel,
  fetchChannelConfig,
  fetchChannelRouting,
  setChannelRouting,
  fetchAgents,
  fetchWorkspaces,
} from '@/lib/api'
import type { ChannelEntry } from '@/lib/api'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
// The connectors route now lazy-loads its screen; import the screen module
// directly so the test renders real content (not the Suspense fallback).
import { ConnectorsScreen } from '@/components/screens/ConnectorsScreen'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

// Defaults to a CONFIGURED instance (instance_id === id), matching the real
// backend shape for an entry backed by a cfg.Channels[] map key (FR-008 —
// "configured" = has a persisted config entry). Pass `instance_id: undefined`
// explicitly to model the "available but unconfigured" static placeholder row.
function makeChannel(overrides: Partial<ChannelEntry> = {}): ChannelEntry {
  const id = overrides.id ?? 'telegram'
  return {
    id,
    instance_id: id,
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
    // A disabled bare-key entry without an identity is a DefaultConfig
    // template stub (not "configured"); real disabled instances carry their
    // ADR-029 binding, so the factory defaults to one.
    identity: { kind: 'agent', id: 'mia' },
    description: 'Telegram Bot integration',
    ...overrides,
  } as ChannelEntry
}

function mockUiStore() {
  const addToast = vi.fn()
  // useUiStore() is called WITHOUT a selector in ChannelConfigPanel/ChannelsScreen.
  // The mock must return the store state object directly.
  vi.mocked(useUiStore).mockReturnValue({ addToast } as never)
  return { addToast }
}

// ── ChannelsScreen stub ───────────────────────────────────────────────────────

function ChannelsScreenStub({ onConfigure }: { onConfigure?: (ch: ChannelEntry) => void }) {
  const queryClient = useQueryClient()

  const { data: channels = [], isLoading, isError } = useQuery({
    queryKey: ['channels'],
    queryFn: fetchChannels,
  })

  const { mutate: doToggleChannel } = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      enabled ? disableChannel(id) : enableChannel(id),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['channels'] }) },
  })

  if (isError) return <p>Could not load channels.</p>
  if (isLoading) return <p>Loading...</p>
  if ((channels as ChannelEntry[]).length === 0) return <p>No channels configured.</p>

  return (
    <div>
      {(channels as ChannelEntry[]).map((channel) => (
        <div key={channel.id} data-testid={`channel-card-${channel.id}`}>
          <span>{channel.name}</span>
          <button
            type="button"
            onClick={() => onConfigure?.(channel)}
            aria-label={`Configure ${channel.name}`}
          >
            Configure
          </button>
          <button
            type="button"
            onClick={() => doToggleChannel({ id: channel.id, enabled: channel.enabled })}
            data-testid={`toggle-${channel.id}`}
          >
            {channel.enabled ? 'Disable' : 'Enable'}
          </button>
        </div>
      ))}
    </div>
  )
}

function renderChannelsStub(onConfigure?: (ch: ChannelEntry) => void) {
  const client = makeQueryClient()
  return render(
    <QueryClientProvider client={client}>
      <ChannelsScreenStub onConfigure={onConfigure} />
    </QueryClientProvider>,
  )
}

// ── Section 1: ChannelsScreen feed ────────────────────────────────────────────
// Traces to: sprint/258-jun-2026 — Channels screen, task 1.

describe('ChannelsScreen — channel feed rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
  })

  it('renders a card per channel when fetchChannels returns data', async () => {
    // Differentiation test: two different channel entries → two different cards.
    // Traces to: sprint/258-jun-2026 — Channels screen list.
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'telegram', name: 'Telegram' }),
      makeChannel({ id: 'discord', name: 'Discord', transport: 'websocket' }),
    ])

    renderChannelsStub()

    await waitFor(() => {
      expect(screen.getByText('Telegram')).toBeInTheDocument()
      expect(screen.getByText('Discord')).toBeInTheDocument()
    })
  })

  it('renders loading state while fetchChannels is pending', () => {
    // Content test: loading text appears while the API call is in flight.
    vi.mocked(fetchChannels).mockReturnValue(new Promise(() => {}))
    renderChannelsStub()
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  it('renders error state when fetchChannels rejects', async () => {
    // Rejection test: error state appears on API failure.
    vi.mocked(fetchChannels).mockRejectedValue(new Error('network error'))
    renderChannelsStub()
    await waitFor(() => {
      expect(screen.getByText(/could not load channels/i)).toBeInTheDocument()
    })
  })

  it('renders empty state when fetchChannels returns empty array', async () => {
    // Empty state: distinct message when no channels exist.
    vi.mocked(fetchChannels).mockResolvedValue([])
    renderChannelsStub()
    await waitFor(() => {
      expect(screen.getByText(/no channels configured/i)).toBeInTheDocument()
    })
  })

  it('calls the onConfigure callback when Configure button is clicked', async () => {
    // Content test: clicking Configure passes the correct channel to the handler.
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'telegram', name: 'Telegram' }),
    ])
    const onConfigure = vi.fn()
    renderChannelsStub(onConfigure)

    await waitFor(() => screen.getByText('Telegram'))

    fireEvent.click(screen.getByRole('button', { name: /configure telegram/i }))
    expect(onConfigure).toHaveBeenCalledOnce()
    expect(onConfigure.mock.calls[0][0]).toMatchObject({ id: 'telegram', name: 'Telegram' })
  })

  it('calls disableChannel when Disable is clicked on an enabled channel', async () => {
    // Content test: mutation uses the right API function depending on current state.
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'telegram', name: 'Telegram', enabled: true }),
    ])
    vi.mocked(disableChannel).mockResolvedValue({ id: 'telegram', enabled: false } as never)

    renderChannelsStub()
    await waitFor(() => screen.getByText('Telegram'))

    fireEvent.click(screen.getByTestId('toggle-telegram'))
    await waitFor(() => {
      expect(disableChannel).toHaveBeenCalledWith('telegram')
    })
  })

  it('calls enableChannel when Enable is clicked on a disabled channel', async () => {
    // Differentiation test: disabled channel triggers enableChannel (not disableChannel).
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'discord', name: 'Discord', enabled: false }),
    ])
    vi.mocked(enableChannel).mockResolvedValue({ id: 'discord', enabled: true } as never)

    renderChannelsStub()
    await waitFor(() => screen.getByText('Discord'))

    fireEvent.click(screen.getByTestId('toggle-discord'))
    await waitFor(() => {
      expect(enableChannel).toHaveBeenCalledWith('discord')
    })
  })
})

// ── Section 2: ChannelConfigPanel Routing section ─────────────────────────────
// Traces to: sprint/258-jun-2026 — ChannelConfigPanel Routing section.

function renderChannelConfigPanel(channelId: string) {
  const client = makeQueryClient()
  client.setQueryData(['channel-routing', channelId], { default_agent_id: undefined })
  client.setQueryData(['agents'], [
    { id: 'mia', name: 'Mia' },
    { id: 'jim', name: 'Jim' },
  ])
  client.setQueryData(['channel-config', channelId], {})

  const onOpenChange = vi.fn()
  const result = render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId={channelId}
        channelName="Telegram"
        open={true}
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>,
  )
  return { ...result, onOpenChange }
}

describe('ChannelConfigPanel — Routing section', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannelConfig).mockResolvedValue({})
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia' } as never,
      { id: 'jim', name: 'Jim' } as never,
    ])
    vi.mocked(fetchChannelRouting).mockResolvedValue({ default_agent_id: undefined })
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: 'mia' })
  })

  it('renders the Routing section header', async () => {
    // Content test: the panel shows a "Routing" section label.
    // Traces to: sprint/258-jun-2026 — ChannelConfigPanel Routing section.
    renderChannelConfigPanel('telegram')
    await waitFor(() => {
      expect(screen.getByText(/^Routing$/i)).toBeInTheDocument()
    })
  })

  it('renders the "Default agent" label', async () => {
    // Content test: a "Default agent" label for the SmartSelect is present.
    renderChannelConfigPanel('telegram')
    await waitFor(() => {
      // getAllByText: multiple "default agent" matches are valid (label + help text).
      const matches = screen.getAllByText(/default agent/i)
      expect(matches.length).toBeGreaterThan(0)
      // At least one must be a label element.
      expect(matches.some((el) => el.tagName.toLowerCase() === 'label')).toBe(true)
    })
  })

  it('renders help text about global default fallback', async () => {
    // Content test: routing section includes the description copy about (Global default).
    renderChannelConfigPanel('telegram')
    await waitFor(() => {
      const matches = screen.getAllByText(/global.?default/i)
      expect(matches.length).toBeGreaterThan(0)
      // At least one match must be the help text paragraph (not the SmartSelect option).
      expect(
        matches.some((el) => el.tagName.toLowerCase() === 'p'),
      ).toBe(true)
    })
  })

  it('shows the current routing agent name when routing is pre-seeded', async () => {
    // Persistence test: pre-seeded routing with "mia" causes SmartSelect to show "Mia".
    const client = makeQueryClient()
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: 'mia' })
    client.setQueryData(['agents'], [{ id: 'mia', name: 'Mia' }, { id: 'jim', name: 'Jim' }])
    client.setQueryData(['channel-config', 'telegram'], {})

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

    await waitFor(() => {
      expect(screen.getByText('Mia')).toBeInTheDocument()
    })
  })

  // NOTE (ADR-029): the former "selecting an agent ID …" and "selecting (Global
  // default) …" behavioral tests were REMOVED here. They asserted the pre-ADR-029
  // routing UX — a flat agent picker with a "(Global default)" sentinel that PUTs
  // `{ default_agent_id }` with no workspace. That UX is replaced by the
  // workspace-bound flow (select workspace → workspace-filtered agents → mandatory
  // agent → PUT `{ workspace_id, default_agent_id }`; "no workspace" = unbind).
  // The new-flow routing assertions (workspace select, member filtering, mandatory
  // + hint, no global-default option, unbind) now live authoritatively in
  // src/components/skills/ChannelConfigPanel.test.tsx, which mocks fetchWorkspaces/
  // fetchWorkspace + core_team and asserts the setChannelRouting payload.
})

// ── Section 3: ChannelsScreen degraded state ──────────────────────────────────
// Traces to: issue #299 — UI must not show degraded channels as healthy.

// ConnectorsScreen is imported directly from its (eager) module so the test
// renders real content rather than the route's lazy Suspense fallback.
const RealChannelsScreen = ConnectorsScreen

function renderRealChannelsScreen() {
  const client = makeQueryClient()
  return render(
    <QueryClientProvider client={client}>
      <RealChannelsScreen />
    </QueryClientProvider>,
  )
}

describe('ChannelsScreen — degraded channel state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    // ConnectorsScreen resolves each configured row's workspace→agent binding
    // via these queries — give them deterministic empty defaults so the
    // "No workspace bound" fallback renders instead of an unmocked real fetch.
    vi.mocked(fetchChannelRouting).mockResolvedValue({})
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(fetchAgents).mockResolvedValue([])
  })

  it('shows "Failed to start" badge (not "Enabled") for a degraded channel', async () => {
    // Correctness test: a degraded-but-enabled channel must NOT display the green
    // "Enabled" badge. It must display the error "Failed to start" badge.
    // Traces to: issue #299 — degraded channels must not look healthy.
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'whatsapp', name: 'WhatsApp', enabled: true, degraded: true }),
    ])

    renderRealChannelsScreen()

    await waitFor(() => {
      expect(screen.getByText('Failed to start')).toBeInTheDocument()
    })
    expect(screen.queryByText('Enabled')).not.toBeInTheDocument()
  })

  it('shows degraded_reason as helper text under the channel name', async () => {
    // Content test: the degraded reason string must be visible to the operator
    // so they understand why the channel failed.
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({
        id: 'slack',
        name: 'Slack',
        enabled: true,
        degraded: true,
        degraded_reason: 'missing SLACK_BOT_TOKEN credential',
      }),
    ])

    renderRealChannelsScreen()

    await waitFor(() => {
      expect(screen.getByText('missing SLACK_BOT_TOKEN credential')).toBeInTheDocument()
    })
  })

  it('does not show degraded_reason text when degraded_reason is absent', async () => {
    // Edge-case: degraded=true but no reason string — no extra text must appear
    // below the name (no "undefined" or empty paragraph).
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'discord', name: 'Discord', enabled: true, degraded: true }),
    ])

    renderRealChannelsScreen()

    await waitFor(() => {
      expect(screen.getByText('Failed to start')).toBeInTheDocument()
    })
    // No stray "undefined" text.
    expect(screen.queryByText('undefined')).not.toBeInTheDocument()
  })

  it('shows green "Enabled" badge for a healthy (non-degraded) enabled channel', async () => {
    // Differentiation test: a healthy enabled channel still shows green badge.
    vi.mocked(fetchChannels).mockResolvedValue([
      makeChannel({ id: 'telegram', name: 'Telegram', enabled: true, degraded: false }),
    ])

    renderRealChannelsScreen()

    await waitFor(() => {
      expect(screen.getByText('Enabled')).toBeInTheDocument()
    })
    expect(screen.queryByText('Failed to start')).not.toBeInTheDocument()
  })
})
