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
  const React = require('react') as typeof import('react')
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
const mockFetchWorkspaces = vi.fn()
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
    fetchWorkspaces: mockFetchWorkspaces,
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

// A configured instance (has instance_id — FR-008's "configured" test).
const STUB_TELEGRAM = {
  id: 'telegram',
  instance_id: 'telegram',
  name: 'Telegram',
  transport: 'webhook' as const,
  enabled: false,
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
  mockFetchAgents.mockResolvedValue([])
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

  it('shows "No workspace bound" when the instance has no routing binding', async () => {
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
    mockGetChannelRouting.mockResolvedValue({})

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-telegram')).toHaveTextContent('No workspace bound')
    })
  })

  it('shows "No workspace bound" (never crashes) when the routing fetch rejects', async () => {
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
    mockGetChannelRouting.mockRejectedValue(new Error('network error'))

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-binding-telegram')).toHaveTextContent('No workspace bound')
    })
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
    expect(within(roster).getAllByText('Connect').length).toBeGreaterThan(0)

    // No grouped list must render at the same time (mutually exclusive, FR-010).
    expect(screen.queryByTestId('channel-type-group-discord')).not.toBeInTheDocument()

    // FR-014 disclaimer present in the roster too.
    expect(screen.getByText(/trademarks of their respective owners/i)).toBeInTheDocument()
  })

  it('opens the create Sheet pre-filled with the clicked type when Connect is clicked', async () => {
    mockFetchChannels.mockResolvedValue([{ ...STUB_TELEGRAM, instance_id: undefined }])
    const user = userEvent.setup()

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    const connectBtn = await screen.findByTestId('channel-roster-connect-telegram')
    await user.click(connectBtn)

    const sheet = await screen.findByTestId('create-channel-sheet')
    expect(sheet).toBeInTheDocument()
    // Type is locked (pre-filled), not an open picker.
    expect(within(sheet).getByTestId('create-channel-type-locked')).toHaveTextContent('Telegram')
    expect(within(sheet).queryByTestId('create-channel-type-select')).not.toBeInTheDocument()
  })
})

// ── Create flow (US-10) ────────────────────────────────────────────────────────

describe('ConnectorsScreen — create channel via Sheet, never a modal Dialog (US-10 AS-1)', () => {
  beforeEach(() => {
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
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

  it('shows invalid slug hint when slug fails [a-z0-9-]{1,32} pattern', async () => {
    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await selectChannelType('Telegram')

    const slugInput = screen.getByTestId('create-channel-slug-input')
    await user.type(slugInput, 'EU')

    const errorHint = await screen.findByTestId('create-channel-slug-error')
    expect(errorHint).toBeInTheDocument()
    expect(errorHint.textContent).toMatch(/lowercase/i)

    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    expect(submitBtn).toBeDisabled()
    expect(mockCreateChannelInstance).not.toHaveBeenCalled()
  })

  it('fires POST with {type, slug} and then opens ChannelConfigPanel for the new instance', async () => {
    // US-10 AS-2: completing the Sheet creates the instance via the ADR-029 path
    // and immediately opens Configure so the operator sets its binding.
    mockCreateChannelInstance.mockResolvedValueOnce({
      id: 'telegram.eu-1',
      type: 'telegram',
      enabled: false,
    })

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await selectChannelType('Telegram')

    const slugInput = screen.getByTestId('create-channel-slug-input')
    await user.type(slugInput, 'eu-1')

    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    expect(submitBtn).not.toBeDisabled()
    await user.click(submitBtn)

    await waitFor(() => {
      expect(mockCreateChannelInstance).toHaveBeenCalledTimes(1)
    })
    expect(mockCreateChannelInstance).toHaveBeenCalledWith({ type: 'telegram', slug: 'eu-1' })

    // The create Sheet closes...
    await waitFor(() => {
      expect(screen.queryByTestId('create-channel-sheet')).not.toBeInTheDocument()
    })
    // ...and ChannelConfigPanel opens for the new instance.
    await waitFor(() => {
      expect(screen.getByTestId('channel-config-panel-telegram.eu-1')).toBeInTheDocument()
    })
    expect(channelConfigPanelOpens.at(-1)).toMatchObject({ channelId: 'telegram.eu-1', enabled: false })
  })

  it('shows 409 conflict error inline when the instance key already exists', async () => {
    const { ApiError } = await import('@/lib/api-error')
    const conflictError = new ApiError(409, 'An instance with that id already exists: "telegram.eu"')
    mockCreateChannelInstance.mockRejectedValueOnce(conflictError)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openCreateSheetGlobal(user)
    await selectChannelType('Telegram')

    const slugInput = screen.getByTestId('create-channel-slug-input')
    await user.type(slugInput, 'eu')

    const submitBtn = screen.getByTestId('create-channel-submit-btn')
    await user.click(submitBtn)

    const serverError = await screen.findByTestId('create-channel-server-error')
    expect(serverError).toBeInTheDocument()
    expect(serverError.textContent).toMatch(/already exists/i)

    // Sheet stays open on failure — the create did not silently succeed.
    expect(screen.getByTestId('create-channel-sheet')).toBeInTheDocument()
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

// ── Web Chat + Email stay outside the type-grouped/roster model ──────────────

describe('ConnectorsScreen — built-in Web Chat is excluded from grouping/roster', () => {
  it('renders Web Chat as an always-on built-in row, not inside the roster or a type group', async () => {
    mockFetchChannels.mockResolvedValue([STUB_WEBCHAT])

    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await waitFor(() => {
      expect(screen.getByTestId('channel-card-webchat')).toBeInTheDocument()
    })
    expect(screen.getByText('Always on')).toBeInTheDocument()
    // Zero conversational instances configured -> Channels section shows its
    // own (possibly empty) roster, not webchat.
    expect(screen.queryByTestId('channel-roster-connect-webchat')).not.toBeInTheDocument()
  })
})
