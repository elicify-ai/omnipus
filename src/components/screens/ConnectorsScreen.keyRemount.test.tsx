/**
 * ConnectorsScreen.keyRemount.test.tsx — round-1 CRITICAL regression, previously
 * UNTESTED: ConnectorsScreen renders ChannelConfigPanel with
 * `key={configuringChannelProps?.id}` so that clicking "Configure" on a
 * different channel row WHILE the panel is already open (configuringChannel
 * jumps straight from A to B — `open` never transitions to false) forces a
 * fresh ChannelConfigPanel instance instead of reusing the dirtied one.
 *
 * ConnectorsScreen.test.tsx stubs ChannelConfigPanel with a stateless mock
 * that just records the props it was opened with, so it cannot exercise (or
 * regress-guard) the actual form-state leak the `key` prop prevents — the
 * leak lives inside ChannelConfigPanel's own state (formValues/isDirtyRef),
 * which the stub doesn't have. This file renders ConnectorsScreen with the
 * REAL ChannelConfigPanel (only its network-facing dependencies are mocked)
 * so the remount behavior is exercised end-to-end.
 *
 * This test MUST fail if the `key={configuringChannelProps?.id}` prop is
 * removed from ConnectorsScreen.tsx's <ChannelConfigPanel> — verified by
 * temporarily removing it locally (see task notes).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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

// Stub EmailMailboxPanel — not exercised by this test.
vi.mock('@/components/connectors/EmailMailboxPanel', () => ({
  EmailMailboxPanel: () => null,
}))

const addToastMock = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: addToastMock }),
}))

// Mock Radix Select with a plain option list — CreateChannelSheet (always
// mounted) uses it, and jsdom lacks hasPointerCapture for the real component.
vi.mock('@/components/ui/select', () => {
  type SelectProps = { value?: string; onValueChange?: (value: string) => void; children?: React.ReactNode }
  type SelectItemProps = { value: string; children?: React.ReactNode }
  const SelectCtx = React.createContext<((v: string) => void) | undefined>(undefined)
  const Select = ({ onValueChange, children }: SelectProps) =>
    React.createElement(SelectCtx.Provider, { value: onValueChange }, children)
  const SelectTrigger = ({ children, ...rest }: { children?: React.ReactNode; [key: string]: unknown }) =>
    React.createElement('div', rest, children)
  const SelectValue = ({ placeholder }: { placeholder?: string }) => React.createElement('span', {}, placeholder)
  const SelectContent = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'listbox' }, children)
  const SelectItem = ({ value, children }: SelectItemProps) => {
    const onValueChange = React.useContext(SelectCtx)
    return React.createElement(
      'div',
      { role: 'option', 'data-value': value, onClick: () => onValueChange?.(value) },
      children,
    )
  }
  const SelectLabel = ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children)
  const SelectGroup = ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children)
  const SelectSeparator = () => React.createElement('hr', {})
  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectLabel, SelectGroup, SelectSeparator }
})

// API mocks. fetchChannelConfig/configureChannel/enableChannel are exercised
// by the REAL ChannelConfigPanel (not stubbed, unlike ConnectorsScreen.test.tsx).
const mockFetchChannels = vi.fn()
const mockFetchChannelConfig = vi.fn()
const mockConfigureChannel = vi.fn()
const mockEnableChannel = vi.fn()
const mockDisableChannel = vi.fn()
const mockCreateChannelInstance = vi.fn()
const mockDeleteChannelInstance = vi.fn()
const mockFetchChannelRouting = vi.fn()
const mockSetChannelRouting = vi.fn()
const mockFetchWorkspaces = vi.fn()
const mockFetchWorkspace = vi.fn()
const mockFetchAgents = vi.fn()
const mockFetchMailboxes = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: mockFetchChannels,
    fetchChannelConfig: mockFetchChannelConfig,
    configureChannel: mockConfigureChannel,
    enableChannel: mockEnableChannel,
    disableChannel: mockDisableChannel,
    createChannelInstance: mockCreateChannelInstance,
    deleteChannelInstance: mockDeleteChannelInstance,
    fetchChannelRouting: mockFetchChannelRouting,
    setChannelRouting: mockSetChannelRouting,
    fetchWorkspaces: mockFetchWorkspaces,
    fetchWorkspace: mockFetchWorkspace,
    fetchAgents: mockFetchAgents,
    fetchMailboxes: mockFetchMailboxes,
    isApiError: actual.isApiError,
    EMAIL_CHANNEL_ID: 'email',
  }
})

// ── Fixtures ──────────────────────────────────────────────────────────────────

// Both carry an `identity` binding so isConfigured() treats them as real
// instances, not DefaultConfig template stubs (see ConnectorsScreen.tsx
// isTemplateStub). Telegram and Discord both expose a generic `token` field
// labeled "Bot Token" — the exact key collision the leak bug hinges on.
const CHANNEL_TELEGRAM = {
  id: 'telegram',
  instance_id: 'telegram',
  name: 'Telegram',
  transport: 'webhook' as const,
  enabled: false,
  identity: { kind: 'agent' as const, id: 'mia' },
  description: 'Telegram Bot API',
}

const CHANNEL_DISCORD = {
  id: 'discord',
  instance_id: 'discord',
  name: 'Discord',
  transport: 'websocket' as const,
  enabled: false,
  identity: { kind: 'agent' as const, id: 'mia' },
  description: 'Discord Gateway',
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

// Dynamic import — a static top-level import would be evaluated by the ESM
// loader before this file's own `const mockFetchChannels = vi.fn()` etc. run
// (module instantiation resolves all static imports first, regardless of
// their lexical position), causing a TDZ ReferenceError inside the mocked
// '@/lib/api' factory. ConnectorsScreen.test.tsx uses the same pattern.
async function renderScreen() {
  const { ConnectorsScreen } = await import('./ConnectorsScreen')
  const client = makeClient()
  return render(
    React.createElement(QueryClientProvider, { client }, React.createElement(ConnectorsScreen)),
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  addToastMock.mockReset()
  mockFetchChannels.mockResolvedValue([CHANNEL_TELEGRAM, CHANNEL_DISCORD])
  mockFetchChannelConfig.mockImplementation((id: string) =>
    Promise.resolve(id === 'telegram' ? { token: 'telegram-stored-token' } : { token: 'discord-stored-token' }),
  )
  mockFetchChannelRouting.mockResolvedValue({})
  mockFetchWorkspaces.mockResolvedValue([])
  mockFetchWorkspace.mockResolvedValue({ id: 'ws1', name: 'Sales', core_team: ['mia'] })
  mockFetchAgents.mockResolvedValue([])
  mockFetchMailboxes.mockResolvedValue([])
  mockSetChannelRouting.mockResolvedValue({})
})

describe('ConnectorsScreen — switching Configure targets while the panel stays open (round-1 CRITICAL)', () => {
  it('B shows B\'s hydrated values with no dirty carryover from A when Configure is clicked on B while A is still open', async () => {
    await renderScreen()

    // Open Configure on Telegram (A).
    const configureTelegramBtn = await screen.findByLabelText('Configure telegram')
    fireEvent.click(configureTelegramBtn)

    // Wait for the real ChannelConfigPanel to hydrate A's stored token.
    await waitFor(() => {
      expect(screen.getByLabelText(/^Bot Token/)).toHaveValue('telegram-stored-token')
    })

    // Dirty the field — type an unsaved edit into A's Bot Token, never saved.
    fireEvent.change(screen.getByLabelText(/^Bot Token/), {
      target: { value: 'A-dirty-unsaved-value' },
    })
    expect(screen.getByLabelText(/^Bot Token/)).toHaveValue('A-dirty-unsaved-value')

    // Click Configure on Discord (B) WHILE the panel is still open — the
    // Configure button for the currently-open channel's OWN row is not
    // rendered inside the (now off-screen) sheet, so this simulates the user
    // clicking a different row's Configure action without ever closing A.
    const configureDiscordBtn = screen.getByLabelText('Configure discord')
    fireEvent.click(configureDiscordBtn)

    // B must show B's real stored value — never A's dirty unsaved edit, and
    // never an empty field (which is what a botched hydration re-run would
    // show if formValues/isDirtyRef leaked across the switch).
    await waitFor(() => {
      expect(screen.getByLabelText(/^Bot Token/)).toHaveValue('discord-stored-token')
    })
    expect(screen.getByLabelText(/^Bot Token/)).not.toHaveValue('A-dirty-unsaved-value')

    // Saving now must submit Discord's real value, never Telegram's leaked one.
    //
    // The click is RETRIED rather than fired once. A single click asserts more
    // than this test means to: that by the instant Bot Token has hydrated, every
    // OTHER field the panel's pre-save validation requires has hydrated too.
    // That holds on an unloaded machine and is not guaranteed on a loaded one —
    // a blocked-by-validation Save returns without calling configureChannel, so
    // the click lands, does nothing, and the wait then expires on a mock that
    // was never going to be called. Observed on CI, not reproducible locally,
    // which is the usual shape of an ordering assumption that only fails under
    // contention.
    //
    // Retrying keeps what the test is actually about — saving submits Discord's
    // value, not Telegram's leaked one — while dropping the incidental claim
    // about hydration order. A save that never becomes possible still fails
    // here, because the wait still expires.
    await waitFor(() => {
      fireEvent.click(screen.getByRole('button', { name: /^Save$/ }))
      expect(mockConfigureChannel).toHaveBeenCalled()
    })
    const [calledId, payload] = mockConfigureChannel.mock.calls[0] as [string, Record<string, unknown>]
    expect(calledId).toBe('discord')
    expect(payload['token']).toBe('discord-stored-token')
  })
})
