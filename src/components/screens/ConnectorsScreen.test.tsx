/**
 * ConnectorsScreen.test.tsx
 *
 * Component tests for ConnectorsScreen — ADR-031 Track 2 (US-9/US-10):
 * type-grouped binding-first rows, empty-state roster, Sheet create flow.
 *
 * Tested behaviors:
 *   1. Type-grouped rows: configured instances group under one header per
 *      channel type, with an in-group "Add another…" action.
 *   2. Binding-first row title: `<Workspace> → <Agent>` resolved via the
 *      routing/workspaces/agents queries (same helpers ChannelConfigPanel uses).
 *   3. Unbound row falls back to "No workspace bound" (never crashes).
 *   4. Empty-state roster renders all connectable channel types + the brand
 *      disclaimer when zero instances are configured.
 *   5. "Add another…" / roster "Connect" / the global "Add channel" button all
 *      open the create Sheet (never the old modal Dialog) with the right type
 *      pre-filled/locked or open for picking.
 *   6. Slug validation (client-side) and 409-conflict server error handling
 *      are preserved from the old AddInstanceDialog.
 *   7. A successful create closes the Sheet and immediately opens
 *      ChannelConfigPanel for the new instance.
 *   8. Delete confirm/cancel behavior is preserved unchanged.
 *
 * Traces to: connectors-providers-redesign-spec.md US-9, US-10, FR-008/009/010/014.
 *
 * Strategy: mock the api module so no real network calls are made; render the
 * ConnectorsScreen in a QueryClientProvider; interact via @testing-library/user-event.
 * Radix Select is mocked with a plain native <select>-like option list to avoid
 * the jsdom hasPointerCapture limitation. ChannelConfigPanel is mocked to a
 * lightweight stub that records the props it was opened with, so we can assert
 * *which* instance the Configure sheet was opened for without exercising its
 * full internals (covered separately in ChannelConfigPanel.test.tsx).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'

// ── Module mocks ──────────────────────────────────────────────────────────────

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

// Stub ChannelConfigPanel — records the props it was opened with (channelId in
// particular) so tests can assert Configure/Connect opens the right instance
// without exercising the panel's full internals (owned by its own test file).
const channelConfigPanelOpens: Array<{ channelId: string; channelName: string; enabled?: boolean; nativeAvailable?: boolean }> = []
vi.mock('@/components/skills/ChannelConfigPanel', () => ({
  ChannelConfigPanel: (props: { channelId: string; channelName: string; enabled?: boolean; nativeAvailable?: boolean; open: boolean }) => {
    if (!props.open) return null
    channelConfigPanelOpens.push({
      channelId: props.channelId,
      channelName: props.channelName,
      enabled: props.enabled,
      nativeAvailable: props.nativeAvailable,
    })
    return React.createElement('div', { 'data-testid': `channel-config-panel-${props.channelId}` }, `Configuring ${props.channelName}`)
  },
}))

// Stub EmailMailboxPanel
vi.mock('@/components/connectors/EmailMailboxPanel', () => ({
  EmailMailboxPanel: () => null,
}))

// Stub the ui store to capture toast calls
const addToastMock = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: addToastMock }),
}))

// Mock Radix Select with a plain option list to avoid jsdom's missing
// hasPointerCapture. The Select passes value/onValueChange so we wire them
// through role=option elements using onChange semantics.
vi.mock('@/components/ui/select', () => {
  type SelectProps = {
    value?: string
    onValueChange?: (value: string) => void
    children?: React.ReactNode
  }
  type SelectItemProps = {
    value: string
    children?: React.ReactNode
  }
  const SelectCtx = React.createContext<((v: string) => void) | undefined>(undefined)

  const Select = ({ onValueChange, children }: SelectProps) =>
    React.createElement(SelectCtx.Provider, { value: onValueChange }, children)
  const SelectTrigger = ({ children, ...rest }: { children?: React.ReactNode; [key: string]: unknown }) =>
    React.createElement('div', rest, children)
  const SelectValue = ({ placeholder }: { placeholder?: string }) =>
    React.createElement('span', {}, placeholder)
  const SelectContent = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'listbox' }, children)
  const SelectItem = ({ value, children }: SelectItemProps) => {
    const onValueChange = React.useContext(SelectCtx)
    return React.createElement(
      'div',
      {
        role: 'option',
        'data-value': value,
        onClick: () => onValueChange?.(value),
      },
      children,
    )
  }
  const SelectLabel = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', {}, children)
  const SelectGroup = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', {}, children)
  const SelectSeparator = () => React.createElement('hr', {})

  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectLabel, SelectGroup, SelectSeparator }
})

// API mocks — defined as module-level spies so individual tests can override
const mockFetchChannels = vi.fn()
const mockCreateChannelInstance = vi.fn()
const mockDeleteChannelInstance = vi.fn()
const mockEnableChannel = vi.fn()
const mockDisableChannel = vi.fn()
const mockGetChannelRouting = vi.fn()
const mockSetChannelRouting = vi.fn()
const mockFetchWorkspaces = vi.fn()
const mockFetchWorkspace = vi.fn()
const mockFetchAgents = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: mockFetchChannels,
    createChannelInstance: mockCreateChannelInstance,
    deleteChannelInstance: mockDeleteChannelInstance,
    enableChannel: mockEnableChannel,
    disableChannel: mockDisableChannel,
    getChannelRouting: mockGetChannelRouting,
    setChannelRouting: mockSetChannelRouting,
    fetchWorkspaces: mockFetchWorkspaces,
    fetchWorkspace: mockFetchWorkspace,
    fetchAgents: mockFetchAgents,
    isApiError: actual.isApiError,
    EMAIL_CHANNEL_ID: 'email',
  }
})

// ── Test setup ─────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return React.createElement(QueryClientProvider, { client: makeClient() }, children)
}

// A configured instance (FR-008). NB: a bare-type key that is disabled AND
// unbound is a DefaultConfig template stub (isTemplateStub in the component) —
// a REAL disabled bare-key instance is modeled by carrying an ADR-029 identity
// binding, which is what distinguishes it from the seeded template on the wire.
const STUB_TELEGRAM = {
  id: 'telegram',
  instance_id: 'telegram',
  name: 'Telegram',
  transport: 'webhook' as const,
  enabled: false,
  identity: { kind: 'agent' as const, id: 'mia' },
  description: 'Telegram Bot API',
}

const STUB_WHATSAPP_SALES = {
  id: 'whatsapp.sales',
  instance_id: 'whatsapp.sales',
  name: 'WhatsApp',
  transport: 'native' as const,
  enabled: true,
  description: 'WhatsApp (native, whatsmeow)',
}

const STUB_WHATSAPP_SUPPORT = {
  id: 'whatsapp.support',
  instance_id: 'whatsapp.support',
  name: 'WhatsApp',
  transport: 'native' as const,
  enabled: false,
  description: 'WhatsApp (native, whatsmeow)',
}

// An "available but unconfigured" placeholder row — no instance_id, per the
// backend HandleChannels contract (pkg/gateway/rest.go static-rows loop).
const STUB_DISCORD_UNCONFIGURED = {
  id: 'discord',
  name: 'Discord',
  transport: 'websocket' as const,
  enabled: false,
  description: 'Discord Gateway',
}

const STUB_WEBCHAT = {
  id: 'webchat',
  name: 'Web Chat',
  transport: 'websocket' as const,
  enabled: true,
  description: 'Built-in browser chat',
}

beforeEach(() => {
  vi.clearAllMocks()
  addToastMock.mockReset()
  channelConfigPanelOpens.length = 0
  // Sensible defaults so any per-instance routing/workspace/agent query that
  // fires never hangs or rejects unexpectedly.
  mockGetChannelRouting.mockResolvedValue({})
  mockFetchWorkspaces.mockResolvedValue([])
  mockFetchWorkspace.mockResolvedValue({ id: 'ws1', name: 'Sales', core_team: ['mia'] })
  mockFetchAgents.mockResolvedValue([])
  mockSetChannelRouting.mockResolvedValue({})
})

afterEach(() => {
  vi.clearAllTimers()
})

// ── Helpers ───────────────────────────────────────────────────────────────────

async function openCreateSheetGlobal(user: ReturnType<typeof userEvent.setup>) {
  const addBtn = await screen.findByTestId('add-channel-instance-btn')
  await user.click(addBtn)
  return screen.findByTestId('create-channel-sheet')
}

async function selectChannelType(typeName: string) {
  const option = await screen.findByRole('option', { name: typeName })
  fireEvent.click(option)
}

// Fill the mom-friendly create form: Channel + Workspace + Agent (no slug —
// the instance key is auto-generated from the workspace name).
async function fillCreateSheet(opts: { type?: string; workspace?: string; agent?: string } = {}) {
  if (opts.type) await selectChannelType(opts.type)
  if (opts.workspace) {
    fireEvent.click(await screen.findByRole('option', { name: opts.workspace }))
  }
  if (opts.agent) {
    fireEvent.click(await screen.findByRole('option', { name: opts.agent }))
  }
}

// ── Grouping + roster ─────────────────────────────────────────────────────────

describe('ConnectorsScreen — type-grouped rows (US-9 AS-1)', () => {
  it('groups multiple instances of the same type under one header with "Add another…"', async () => {
    // BDD: Given three whatsapp.* instances, When the list renders, Then one
    // WhatsApp group header shows both rows with an in-group "Add another…".
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM, STUB_WHATSAPP_SALES, STUB_WHATSAPP_SUPPORT])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const group = await screen.findByTestId('channel-type-group-whatsapp')
    expect(within(group).getByTestId('channel-card-whatsapp.sales')).toBeInTheDocument()
    expect(within(group).getByTestId('channel-card-whatsapp.support')).toBeInTheDocument()
    expect(within(group).getByTestId('channel-type-add-another-whatsapp')).toBeInTheDocument()

    // Telegram is a separate group.
    const telegramGroup = await screen.findByTestId('channel-type-group-telegram')
    expect(within(telegramGroup).getByTestId('channel-card-telegram')).toBeInTheDocument()
  })

  it('renders the brand disclaimer on the populated (grouped) list', async () => {
    // FR-014: the disclaimer must be present on every screen showing a BrandIcon.
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await screen.findByTestId('channel-type-group-telegram')
    expect(screen.getByText(/trademarks of their respective owners/i)).toBeInTheDocument()
  })

  it('does not render an "unconfigured" placeholder row inside a type group', async () => {
    // FR-008: only configured instances (instance_id set) render as rows; the
    // static unconfigured Discord placeholder must not appear as a group.
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM, STUB_DISCORD_UNCONFIGURED])
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await screen.findByTestId('channel-type-group-telegram')
    expect(screen.queryByTestId('channel-type-group-discord')).not.toBeInTheDocument()
  })

  it('does not render the empty-state roster when at least one type is configured (B2 — mutual exclusivity)', async () => {
    // Mixed state: one configured type (telegram) + one unconfigured type
    // (discord). FR-010's grouped-list/roster split is mutually exclusive —
    // the grouped list wins as soon as ANY instance is configured, so the
    // roster must be completely absent, not just missing a discord entry.
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM, STUB_DISCORD_UNCONFIGURED])
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await screen.findByTestId('channel-type-group-telegram')
    expect(screen.queryByTestId('channel-roster')).not.toBeInTheDocument()
  })

  it('resolves each row\'s workspace→agent binding independently via its own instance id (B1)', async () => {
    // BDD: Given two same-type instances, When the list renders, Then each
    // row's binding is resolved from ITS OWN routing query result, keyed by
    // its own instance id — never leaking one row's binding into another's.
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES, STUB_WHATSAPP_SUPPORT])
    mockGetChannelRouting.mockImplementation((id: string) => {
      if (id === 'whatsapp.sales') return Promise.resolve({ workspace_id: 'ws-1', default_agent_id: 'agent-1' })
      if (id === 'whatsapp.support') return Promise.resolve({ workspace_id: 'ws-2', default_agent_id: 'agent-2' })
      return Promise.resolve({})
    })
    mockFetchWorkspaces.mockResolvedValue([
      { id: 'ws-1', name: 'Sales' },
      { id: 'ws-2', name: 'Support' },
    ])
    mockFetchAgents.mockResolvedValue([
      { id: 'agent-1', name: 'Mia' },
      { id: 'agent-2', name: 'Jim' },
    ])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-whatsapp.sales')).toHaveTextContent('Sales → Mia')
      expect(screen.getByTestId('channel-binding-whatsapp.support')).toHaveTextContent('Support → Jim')
    })
    expect(mockGetChannelRouting).toHaveBeenCalledWith('whatsapp.sales')
    expect(mockGetChannelRouting).toHaveBeenCalledWith('whatsapp.support')
  })
})

describe('ConnectorsScreen — binding-first row title (US-9 AS-2)', () => {
  it('shows "<Workspace> → <Agent>" resolved from routing + workspace + agent queries', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    mockGetChannelRouting.mockResolvedValue({ workspace_id: 'ws-1', default_agent_id: 'agent-1' })
    mockFetchWorkspaces.mockResolvedValue([{ id: 'ws-1', name: 'Sales' }])
    mockFetchAgents.mockResolvedValue([{ id: 'agent-1', name: 'Mia' }])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-whatsapp.sales')).toHaveTextContent('Sales → Mia')
    })
    // No redundant "WhatsApp" prefix in the row title itself.
    expect(screen.getByTestId('channel-binding-whatsapp.sales').textContent).not.toMatch(/whatsapp/i)
  })

  it('falls back to the raw workspace id when the name cannot be resolved', async () => {
    // "Never crash on missing routing data — fall back to raw ids."
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    mockGetChannelRouting.mockResolvedValue({ workspace_id: 'ws-unknown', default_agent_id: 'agent-unknown' })
    mockFetchWorkspaces.mockResolvedValue([]) // ws-unknown not present
    mockFetchAgents.mockResolvedValue([]) // agent-unknown not present

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-whatsapp.sales')).toHaveTextContent('ws-unknown → agent-unknown')
    })
  })

  it('resolves the workspace name but falls back to the raw agent id when only the agent is unknown (B7)', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    mockGetChannelRouting.mockResolvedValue({ workspace_id: 'ws-1', default_agent_id: 'agent-ghost' })
    mockFetchWorkspaces.mockResolvedValue([{ id: 'ws-1', name: 'Sales' }])
    mockFetchAgents.mockResolvedValue([]) // agent-ghost not present

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-whatsapp.sales')).toHaveTextContent('Sales → agent-ghost')
    })
  })

  it('falls back to the raw workspace id but resolves the agent name when only the workspace is unknown (B7)', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    mockGetChannelRouting.mockResolvedValue({ workspace_id: 'ws-ghost', default_agent_id: 'agent-1' })
    mockFetchWorkspaces.mockResolvedValue([]) // ws-ghost not present
    mockFetchAgents.mockResolvedValue([{ id: 'agent-1', name: 'Mia' }])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-whatsapp.sales')).toHaveTextContent('ws-ghost → Mia')
    })
  })

  it('shows "<Workspace> → unassigned agent" when routing has a workspace but no default agent (B5)', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    mockGetChannelRouting.mockResolvedValue({ workspace_id: 'ws-1' })
    mockFetchWorkspaces.mockResolvedValue([{ id: 'ws-1', name: 'Sales' }])
    mockFetchAgents.mockResolvedValue([])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-whatsapp.sales')).toHaveTextContent('Sales → unassigned agent')
    })
  })

  it('shows "No workspace bound" when the instance has no routing binding', async () => {
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
    mockGetChannelRouting.mockResolvedValue({})

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-telegram')).toHaveTextContent('No workspace bound')
    })
  })

  it('shows a distinct error state — never "No workspace bound" — when the routing fetch rejects (A1/B8)', async () => {
    // "No workspace bound" asserts a known fact (we asked, and there's
    // nothing). A rejected fetch means we DON'T KNOW — conflating the two
    // would mislead an operator into thinking the channel is genuinely unbound.
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
    mockGetChannelRouting.mockRejectedValue(new Error('network error'))

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-telegram')).toHaveTextContent("Couldn't load binding")
    })
    expect(screen.getByTestId('channel-binding-telegram')).not.toHaveTextContent('No workspace bound')
    // Component tree must not have thrown — the row and its Configure action still render.
    expect(screen.getByLabelText('Configure telegram')).toBeInTheDocument()
  })
})

describe('ConnectorsScreen — empty-state roster (US-9 AS-3)', () => {
  it('renders a Connect roster of every known channel type when 0 instances are configured', async () => {
    mockFetchChannels.mockResolvedValue([STUB_DISCORD_UNCONFIGURED, { ...STUB_TELEGRAM, instance_id: undefined }])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const roster = await screen.findByTestId('channel-roster')
    expect(within(roster).getByTestId('channel-roster-connect-discord')).toBeInTheDocument()
    expect(within(roster).getByTestId('channel-roster-connect-telegram')).toBeInTheDocument()
    expect(within(roster).getAllByText('Configure').length).toBeGreaterThan(0)

    // No grouped list must render at the same time (mutually exclusive, FR-010).
    expect(screen.queryByTestId('channel-type-group-discord')).not.toBeInTheDocument()

    // FR-014 disclaimer present in the roster too.
    expect(screen.getByText(/trademarks of their respective owners/i)).toBeInTheDocument()
  })

  it('treats DefaultConfig bare-type template stubs as unconfigured — a fresh install shows the roster, not 12 groups (live-UAT regression)', async () => {
    // The REAL fresh-install shape: DefaultConfig seeds a disabled, unbound
    // stub under the bare-type key for 12 of the 13 types. instance_id IS set
    // for all of them — only the template heuristic keeps the roster visible.
    const templates = [
      'telegram', 'discord', 'slack', 'whatsapp', 'matrix', 'irc',
      'line', 'qq', 'weixin', 'wecom', 'feishu', 'dingtalk',
    ].map((id) => ({
      id,
      instance_id: id,
      name: id,
      transport: 'webhook' as const,
      enabled: false,
      description: '',
    }))
    mockFetchChannels.mockResolvedValue(templates)

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const roster = await screen.findByTestId('channel-roster')
    expect(within(roster).getByTestId('channel-roster-connect-telegram')).toBeInTheDocument()
    expect(within(roster).getByTestId('channel-roster-connect-dingtalk')).toBeInTheDocument()
    // Not a single template stub may render as a configured group.
    expect(screen.queryAllByTestId(/^channel-type-group-/)).toHaveLength(0)
  })

  it('a bare-key stub that is bound (or enabled) is configured, not a template', async () => {
    const stub = {
      id: 'telegram',
      instance_id: 'telegram',
      name: 'Telegram',
      transport: 'webhook' as const,
      enabled: false,
      description: '',
    }
    mockFetchChannels.mockResolvedValue([
      { ...stub, identity: { kind: 'agent' as const, id: 'mia' } },
    ])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    // Renders as a configured group row, and the roster stays hidden.
    expect(await screen.findByTestId('channel-type-group-telegram')).toBeInTheDocument()
    expect(screen.queryByTestId('channel-roster')).not.toBeInTheDocument()
  })

  it('roster Configure opens ChannelConfigPanel DIRECTLY on the bare type — the original one-click flow, no slug/create step (operator-mandated)', async () => {
    mockFetchChannels.mockResolvedValue([{ ...STUB_TELEGRAM, instance_id: undefined, identity: undefined }])
    const user = userEvent.setup()

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await user.click(await screen.findByTestId('channel-roster-connect-telegram'))

    // ChannelConfigPanel (stubbed above) opens with the BARE type id — the
    // token+routing slide-over, not the slug-create Sheet.
    expect(channelConfigPanelOpens.at(-1)).toMatchObject({ channelId: 'telegram', channelName: 'Telegram' })
    expect(screen.queryByTestId('create-channel-sheet')).not.toBeInTheDocument()
  })

  it('the global "+ Add channel" button still opens the slug-create Sheet with an open type picker (multi-instance tool)', async () => {
    mockFetchChannels.mockResolvedValue([{ ...STUB_TELEGRAM, instance_id: undefined }])
    const user = userEvent.setup()

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await user.click(await screen.findByTestId('add-channel-instance-btn'))

    const sheet = await screen.findByTestId('create-channel-sheet')
    expect(sheet).toBeInTheDocument()
    // Global entry point → type is pickable, not locked.
    expect(within(sheet).getByTestId('create-channel-type-select')).toBeInTheDocument()
    expect(within(sheet).queryByTestId('create-channel-type-locked')).not.toBeInTheDocument()
  })
})

// ── Create flow (US-10) ────────────────────────────────────────────────────────

describe('ConnectorsScreen — create channel via Sheet, never a modal Dialog (US-10 AS-1)', () => {
  beforeEach(() => {
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
    mockFetchWorkspaces.mockResolvedValue([{ id: 'ws1', name: 'Sales' }])
    mockFetchWorkspace.mockResolvedValue({ id: 'ws1', name: 'Sales', core_team: ['mia'] })
    mockFetchAgents.mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true },
      { id: 'worker-1', name: 'Worker', type: 'Subagent' },
    ])
  })

  it('the global "Add channel" button opens a Sheet with an open type picker (no modal Dialog)', async () => {
    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const sheet = await openCreateSheetGlobal(user)
    expect(sheet).toBeInTheDocument()
    expect(within(sheet).getByTestId('create-channel-type-select')).toBeInTheDocument()
    expect(within(sheet).queryByTestId('create-channel-type-locked')).not.toBeInTheDocument()

    // No legacy modal Dialog anywhere in the tree.
    expect(screen.queryByTestId('add-instance-dialog')).not.toBeInTheDocument()
  })

  it('"Add another…" on a group opens the Sheet locked to that group\'s type', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const addAnother = await screen.findByTestId('channel-type-add-another-whatsapp')
    await user.click(addAnother)

    const sheet = await screen.findByTestId('create-channel-sheet')
    expect(within(sheet).getByTestId('create-channel-type-locked')).toHaveTextContent('WhatsApp')
  })

  it('clears the type lock when reopened via the global "Add channel" after a locked open (B6)', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    // Open locked via the group's "Add another…".
    const addAnother = await screen.findByTestId('channel-type-add-another-whatsapp')
    await user.click(addAnother)
    let sheet = await screen.findByTestId('create-channel-sheet')
    expect(within(sheet).getByTestId('create-channel-type-locked')).toBeInTheDocument()

    // Close it via Cancel.
    await user.click(within(sheet).getByRole('button', { name: /cancel/i }))
    await waitFor(() => {
      expect(screen.queryByTestId('create-channel-sheet')).not.toBeInTheDocument()
    })

    // Reopen via the global "Add channel" entry point — must be pickable,
    // with no residual lock from the previous locked open.
    sheet = await openCreateSheetGlobal(user)
    expect(within(sheet).getByTestId('create-channel-type-select')).toBeInTheDocument()
    expect(within(sheet).queryByTestId('create-channel-type-locked')).not.toBeInTheDocument()
  })

  it('submit stays disabled until channel, workspace AND agent are all chosen — no slug field exists', async () => {
    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const sheet = await openCreateSheetGlobal(user)
    // The instance key is auto-generated; the operator never types one.
    expect(within(sheet).queryByTestId('create-channel-slug-input')).not.toBeInTheDocument()

    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    expect(submitBtn).toBeDisabled()
    await fillCreateSheet({ type: 'Telegram' })
    expect(submitBtn).toBeDisabled()
    await fillCreateSheet({ workspace: 'Sales' })
    expect(submitBtn).toBeDisabled()
    await fillCreateSheet({ agent: 'Mia' })
    await waitFor(() => expect(submitBtn).not.toBeDisabled())
    expect(mockCreateChannelInstance).not.toHaveBeenCalled()
  })

  it('creates with an auto-generated key from the workspace name, persists the binding, and opens Configure', async () => {
    mockCreateChannelInstance.mockResolvedValueOnce({
      id: 'telegram.sales',
      type: 'telegram',
      enabled: false,
    })

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await fillCreateSheet({ type: 'Telegram', workspace: 'Sales', agent: 'Mia' })
    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    await waitFor(() => expect(submitBtn).not.toBeDisabled())
    await user.click(submitBtn)

    // Key auto-derived from the workspace name — the operator typed nothing.
    await waitFor(() => {
      expect(mockCreateChannelInstance).toHaveBeenCalledWith({ type: 'telegram', slug: 'sales' })
    })
    // The ADR-029 binding chosen in the dialog is persisted immediately.
    await waitFor(() => {
      expect(mockSetChannelRouting).toHaveBeenCalledWith('telegram.sales', {
        workspace_id: 'ws1',
        default_agent_id: 'mia',
      })
    })
    await waitFor(() => {
      expect(screen.queryByTestId('create-channel-sheet')).not.toBeInTheDocument()
    })
    await waitFor(() => {
      expect(screen.getByTestId('channel-config-panel-telegram.sales')).toBeInTheDocument()
    })
  })

  it('de-duplicates the auto key when the workspace name is already taken for that type', async () => {
    mockFetchChannels.mockResolvedValue([
      { ...STUB_TELEGRAM, id: 'telegram.sales', instance_id: 'telegram.sales' },
    ])
    mockCreateChannelInstance.mockResolvedValueOnce({
      id: 'telegram.sales-2',
      type: 'telegram',
      enabled: false,
    })

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await fillCreateSheet({ type: 'Telegram', workspace: 'Sales', agent: 'Mia' })
    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    await waitFor(() => expect(submitBtn).not.toBeDisabled())
    await user.click(submitBtn)

    await waitFor(() => {
      expect(mockCreateChannelInstance).toHaveBeenCalledWith({ type: 'telegram', slug: 'sales-2' })
    })
  })

  it('shows 409 conflict error inline when the instance key already exists', async () => {
    const { ApiError } = await import('@/lib/api-error')
    const conflictError = new ApiError(409, 'An instance with that id already exists: "telegram.eu"')
    mockCreateChannelInstance.mockRejectedValueOnce(conflictError)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await fillCreateSheet({ type: 'Telegram', workspace: 'Sales', agent: 'Mia' })
    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    await waitFor(() => expect(submitBtn).not.toBeDisabled())
    await user.click(submitBtn)

    const serverError = await screen.findByTestId('create-channel-server-error')
    expect(serverError).toBeInTheDocument()
    expect(serverError.textContent).toMatch(/just added elsewhere/i)

    // Sheet stays open on failure — the create did not silently succeed.
    expect(screen.getByTestId('create-channel-sheet')).toBeInTheDocument()
  })

  it('shows the ApiError userMessage for a non-409 error status (B4)', async () => {
    const { ApiError } = await import('@/lib/api-error')
    const serverErr = new ApiError(500, 'The server is unavailable. Please try again in a moment.')
    mockCreateChannelInstance.mockRejectedValueOnce(serverErr)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await fillCreateSheet({ type: 'Telegram', workspace: 'Sales', agent: 'Mia' })
    await waitFor(() => expect(screen.getByTestId('create-channel-submit-btn')).not.toBeDisabled())
    await user.click(screen.getByTestId('create-channel-submit-btn'))

    const serverError = await screen.findByTestId('create-channel-server-error')
    expect(serverError.textContent).toMatch(/server is unavailable/i)
  })

  it('shows a generic actionable message — never the raw Error.message — for a non-ApiError failure (A5/B4)', async () => {
    mockCreateChannelInstance.mockRejectedValueOnce(new Error('Unexpected token < in JSON at position 0'))

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await fillCreateSheet({ type: 'Telegram', workspace: 'Sales', agent: 'Mia' })
    await waitFor(() => expect(screen.getByTestId('create-channel-submit-btn')).not.toBeDisabled())
    await user.click(screen.getByTestId('create-channel-submit-btn'))

    const serverError = await screen.findByTestId('create-channel-server-error')
    expect(serverError.textContent).toMatch(/unexpected response from the server/i)
    expect(serverError.textContent).not.toMatch(/unexpected token/i)
  })

  it('shows the generic message for an ApiSchemaError without leaking zod/response internals (A5/B4)', async () => {
    const { ApiSchemaError } = await import('@/lib/api')
    const schemaErr = new ApiSchemaError('/channels', [{ path: ['id'], message: 'Required' }], { foo: 'bar' })
    mockCreateChannelInstance.mockRejectedValueOnce(schemaErr)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await fillCreateSheet({ type: 'Telegram', workspace: 'Sales', agent: 'Mia' })
    await waitFor(() => expect(screen.getByTestId('create-channel-submit-btn')).not.toBeDisabled())
    await user.click(screen.getByTestId('create-channel-submit-btn'))

    const serverError = await screen.findByTestId('create-channel-server-error')
    expect(serverError.textContent).toMatch(/unexpected response from the server/i)
    expect(serverError.textContent).not.toMatch(/zod|schema mismatch|Required/i)
  })
})

// ── Delete instance (unchanged UX, still an AlertDialog confirmation) ─────────

describe('ConnectorsScreen — delete instance', () => {
  it('fires DELETE for the instance id when confirm is clicked', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])
    mockDeleteChannelInstance.mockResolvedValueOnce(undefined)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const deleteBtn = await screen.findByTestId('channel-delete-btn-whatsapp.sales')
    await user.click(deleteBtn)

    const dialog = await screen.findByTestId('delete-instance-dialog')
    expect(dialog).toBeInTheDocument()
    expect(dialog.textContent).toMatch(/whatsapp\.sales/i)

    const confirmBtn = screen.getByTestId('delete-instance-confirm-btn')
    await user.click(confirmBtn)

    await waitFor(() => {
      expect(mockDeleteChannelInstance).toHaveBeenCalledTimes(1)
    })
    expect(mockDeleteChannelInstance).toHaveBeenCalledWith('whatsapp.sales')
  })

  it('does NOT fire DELETE when cancel is clicked', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES])

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const deleteBtn = await screen.findByTestId('channel-delete-btn-whatsapp.sales')
    await user.click(deleteBtn)

    const dialog = await screen.findByTestId('delete-instance-dialog')
    expect(dialog).toBeInTheDocument()

    const cancelBtn = screen.getByTestId('delete-instance-cancel-btn')
    await user.click(cancelBtn)

    expect(mockDeleteChannelInstance).not.toHaveBeenCalled()
    await waitFor(() => {
      expect(screen.queryByTestId('delete-instance-dialog')).not.toBeInTheDocument()
    })
  })
})

// ── Enable/Disable toggle (B3 — real ConnectorsScreen, not the -channels.test.tsx stub) ──

describe('ConnectorsScreen — Enable/Disable toggle fires the real mutation', () => {
  it('calls disableChannel for an enabled instance and enableChannel for a disabled one', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_SALES, STUB_TELEGRAM])
    mockDisableChannel.mockResolvedValueOnce({ id: 'whatsapp.sales', enabled: false })
    mockEnableChannel.mockResolvedValueOnce({ id: 'telegram', enabled: true })

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    // STUB_WHATSAPP_SALES.enabled === true -> the toggle reads "Disable".
    const salesToggle = await screen.findByTestId('channel-toggle-whatsapp.sales')
    expect(salesToggle).toHaveTextContent('Disable')
    await user.click(salesToggle)
    await waitFor(() => {
      expect(mockDisableChannel).toHaveBeenCalledWith('whatsapp.sales')
    })
    expect(mockEnableChannel).not.toHaveBeenCalled()

    // STUB_TELEGRAM.enabled === false -> the toggle reads "Enable".
    const telegramToggle = await screen.findByTestId('channel-toggle-telegram')
    expect(telegramToggle).toHaveTextContent('Enable')
    await user.click(telegramToggle)
    await waitFor(() => {
      expect(mockEnableChannel).toHaveBeenCalledWith('telegram')
    })
  })
})

// ── Web Chat + Email stay outside the type-grouped/roster model ──────────────

describe('ConnectorsScreen — built-in Web Chat is excluded from grouping/roster', () => {
  it('renders Web Chat as an always-on built-in row, separate from the (empty) Channels roster (B9)', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WEBCHAT])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-card-webchat')).toBeInTheDocument()
    })
    expect(screen.getByText('Always on')).toBeInTheDocument()

    // Zero conversational (non-webchat, non-email) instances are configured,
    // so the Channels section renders its own empty-state roster — webchat is
    // excluded from `channels` entirely (not merely absent from a testid that
    // could never exist either way; see the retired tautological assertion).
    const roster = await screen.findByTestId('channel-roster')
    expect(within(roster).queryByText('Web Chat')).not.toBeInTheDocument()
    expect(within(roster).queryAllByTestId(/^channel-roster-connect-/)).toHaveLength(0)
  })
})
