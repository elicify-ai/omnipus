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
    getChannelRouting: vi.fn(),
    setChannelRouting: vi.fn(),
    fetchAgents: vi.fn(),
    configureChannel: vi.fn(),
    testChannel: vi.fn(),
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
  getChannelRouting,
  setChannelRouting,
  fetchAgents,
} from '@/lib/api'
import type { ChannelEntry } from '@/lib/api'
import { ChannelConfigPanel } from '@/components/skills/ChannelConfigPanel'
// The channels route now lazy-loads its screen; import the screen module
// directly so the test renders real content (not the Suspense fallback).
import { ChannelsScreen } from '@/components/screens/ChannelsScreen'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function makeChannel(overrides: Partial<ChannelEntry> = {}): ChannelEntry {
  return {
    id: 'telegram',
    name: 'Telegram',
    transport: 'webhook',
    enabled: false,
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
    vi.mocked(getChannelRouting).mockResolvedValue({ default_agent_id: undefined })
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

  it('selecting an agent ID calls setChannelRouting with { default_agent_id: "<id>" }', async () => {
    // Behavioral test: rendering ChannelConfigPanel with an agent list and firing
    // SmartSelect onValueChange("mia") must call setChannelRouting with the correct
    // payload.  This catches hardcoded or no-op mutations.
    // Traces to: sprint/258-jun-2026 — ChannelConfigPanel, Routing section, set agent.
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: 'mia' })

    const client = makeQueryClient()
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: undefined })
    client.setQueryData(['agents'], [
      { id: 'mia', name: 'Mia' },
      { id: 'jim', name: 'Jim' },
    ])
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

    // Wait for the SmartSelect to be rendered with the "(Global default)" placeholder.
    await waitFor(() => {
      expect(screen.getByText(/^Routing$/i)).toBeInTheDocument()
    })

    // SmartSelect with 3 items (<= SEARCHABLE_THRESHOLD=5) renders a native <select>.
    // Find the select inside the routing section.
    const routingSection = screen.getByText(/^Routing$/i).closest('div')!
    const select = routingSection.querySelector('select') as HTMLSelectElement | null
    if (select) {
      // Native select: fire change event with agent id value.
      fireEvent.change(select, { target: { value: 'mia' } })
    } else {
      // Searchable combobox fallback: click the trigger then the option.
      const trigger = routingSection.querySelector('[role="combobox"]') as HTMLElement | null
      if (trigger) {
        fireEvent.click(trigger)
        const option = screen.getByRole('option', { name: /mia/i })
        fireEvent.click(option)
      }
    }

    // setChannelRouting must be called with the agent id (not null, not hardcoded).
    await waitFor(() => {
      expect(setChannelRouting).toHaveBeenCalledWith('telegram', { default_agent_id: 'mia' })
    })
  })

  it('selecting "(Global default)" calls setChannelRouting with { default_agent_id: undefined }', async () => {
    // Behavioral test: selecting the sentinel "__none__" value must call
    // setChannelRouting with default_agent_id omitted, not "__none__".  Two different inputs → two
    // different outputs — catches hardcoded or identity-pass-through implementations.
    // Traces to: sprint/258-jun-2026 — ChannelConfigPanel, Routing section, clear agent.
    vi.mocked(setChannelRouting).mockResolvedValue({ default_agent_id: undefined })

    const client = makeQueryClient()
    // Pre-seed with "mia" so the initial value is not already empty.
    client.setQueryData(['channel-routing', 'telegram'], { default_agent_id: 'mia' })
    client.setQueryData(['agents'], [
      { id: 'mia', name: 'Mia' },
      { id: 'jim', name: 'Jim' },
    ])
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
      expect(screen.getByText(/^Routing$/i)).toBeInTheDocument()
    })

    const routingSection = screen.getByText(/^Routing$/i).closest('div')!
    const select = routingSection.querySelector('select') as HTMLSelectElement | null
    if (select) {
      // Select the sentinel value which represents "Global default" → omitted.
      fireEvent.change(select, { target: { value: '__none__' } })
    } else {
      const trigger = routingSection.querySelector('[role="combobox"]') as HTMLElement | null
      if (trigger) {
        fireEvent.click(trigger)
        const option = screen.getByRole('option', { name: /(global default)/i })
        fireEvent.click(option)
      }
    }

    // setChannelRouting must be called with default_agent_id omitted (sentinel "__none__" → undefined mapping).
    await waitFor(() => {
      expect(setChannelRouting).toHaveBeenCalledWith('telegram', { default_agent_id: undefined })
    })
  })
})

// ── Section 3: ChannelsScreen degraded state ──────────────────────────────────
// Traces to: issue #299 — UI must not show degraded channels as healthy.

// ChannelsScreen is imported directly from its (eager) module so the test
// renders real content rather than the route's lazy Suspense fallback.
const RealChannelsScreen = ChannelsScreen

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
