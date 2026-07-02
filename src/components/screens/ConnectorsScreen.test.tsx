/**
 * ConnectorsScreen.test.tsx
 *
 * Component tests for the channel-instance add/delete UI in ConnectorsScreen.
 *
 * Tested behaviors:
 *   1. Invalid slug → POST not fired, slug error message shown
 *   2. Valid slug + type → POST fires with {type, slug}
 *   3. 409 conflict response → inline error shown in dialog
 *   4. Delete confirm → DELETE fires for the correct instance id
 *   5. Delete cancel → no DELETE call made
 *
 * Traces to: channel-instance-workspace-binding-spec.md US-6 / US-10 / US-11
 *
 * Strategy: mock the api module so no real network calls are made; render the
 * ConnectorsScreen in a QueryClientProvider; interact via @testing-library/user-event.
 * Radix Select is mocked with a plain native <select> to avoid the jsdom
 * hasPointerCapture limitation.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'

// ── Module mocks ──────────────────────────────────────────────────────────────

// Stub TanStack Router — ConnectorsScreen doesn't use routing directly but some
// deps might. Provide minimal stubs.
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

// Stub ChannelConfigPanel — we don't test the routing UX here (owned by another agent)
vi.mock('@/components/skills/ChannelConfigPanel', () => ({
  ChannelConfigPanel: () => null,
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

// Mock Radix Select with a plain native <select> to avoid jsdom's missing
// hasPointerCapture. The Select passes value/onValueChange so we wire them
// through a native select using onChange.
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
  // Context to pass onValueChange down to items
  const SelectCtx = React.createContext<((v: string) => void) | undefined>(undefined)

  const Select = ({ onValueChange, children }: SelectProps) =>
    React.createElement(SelectCtx.Provider, { value: onValueChange }, children)
  const SelectTrigger = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', {}, children)
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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: mockFetchChannels,
    createChannelInstance: mockCreateChannelInstance,
    deleteChannelInstance: mockDeleteChannelInstance,
    enableChannel: mockEnableChannel,
    disableChannel: mockDisableChannel,
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

// Minimal channel entries used across tests
const STUB_TELEGRAM = {
  id: 'telegram',
  instance_id: 'telegram',
  name: 'Telegram',
  transport: 'webhook' as const,
  enabled: false,
  description: 'Telegram Bot API',
}

const STUB_WHATSAPP_EU = {
  id: 'whatsapp',
  instance_id: 'whatsapp.eu',
  name: 'WhatsApp',
  transport: 'native' as const,
  enabled: false,
  description: 'WhatsApp',
}

beforeEach(() => {
  vi.clearAllMocks()
  addToastMock.mockReset()
  // Default: return a simple channel list with telegram
  mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])
})

afterEach(() => {
  vi.clearAllTimers()
})

// ── Helper: open the add-instance dialog and select a type ───────────────────

async function openAddDialog(user: ReturnType<typeof userEvent.setup>) {
  const addBtn = await screen.findByTestId('add-channel-instance-btn')
  await user.click(addBtn)
  return screen.findByTestId('add-instance-dialog')
}

async function selectChannelType(typeName: string) {
  // Our mocked Select renders items as role=option; click the matching one.
  const option = await screen.findByRole('option', { name: typeName })
  fireEvent.click(option)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('ConnectorsScreen — add-instance dialog', () => {
  it('shows invalid slug hint when slug fails [a-z0-9-]{1,32} pattern', async () => {
    // BDD: Given the add-instance dialog is open and an invalid slug is typed,
    // When the slug contains uppercase characters (e.g. "EU"),
    // Then an inline slug error hint appears and the submit button remains disabled.
    // Traces to: US-6 / FR-017 — slug validation client-side
    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openAddDialog(user)

    // Select a channel type from the mocked select
    await selectChannelType('telegram')

    // Enter an invalid slug (uppercase)
    const slugInput = screen.getByTestId('add-instance-slug-input')
    await user.type(slugInput, 'EU')

    // Slug error hint should appear
    const errorHint = await screen.findByTestId('add-instance-slug-error')
    expect(errorHint).toBeInTheDocument()
    expect(errorHint.textContent).toMatch(/lowercase/i)

    // Submit button should be disabled
    const submitBtn = screen.getByTestId('add-instance-submit-btn')
    expect(submitBtn).toBeDisabled()

    // POST must NOT be fired
    expect(mockCreateChannelInstance).not.toHaveBeenCalled()
  })

  it('fires POST with {type, slug} when a valid slug is submitted', async () => {
    // BDD: Given a valid type "telegram" and valid slug "eu-1",
    // When the form is submitted,
    // Then POST fires exactly once with {type: 'telegram', slug: 'eu-1'}.
    // Traces to: US-6 / AC-1 — create N instances per type; DS-2 row 1
    mockCreateChannelInstance.mockResolvedValueOnce({
      id: 'telegram.eu-1',
      type: 'telegram',
      enabled: false,
    })
    mockFetchChannels.mockResolvedValue([STUB_TELEGRAM])

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openAddDialog(user)

    // Select channel type via mocked Select
    await selectChannelType('telegram')

    // Enter a valid slug
    const slugInput = screen.getByTestId('add-instance-slug-input')
    await user.type(slugInput, 'eu-1')

    // Submit the form
    const submitBtn = screen.getByTestId('add-instance-submit-btn')
    expect(submitBtn).not.toBeDisabled()
    await user.click(submitBtn)

    // POST must have been called exactly once with the right body
    await waitFor(() => {
      expect(mockCreateChannelInstance).toHaveBeenCalledTimes(1)
    })
    expect(mockCreateChannelInstance).toHaveBeenCalledWith({ type: 'telegram', slug: 'eu-1' })
  })

  it('shows 409 conflict error inline when the instance key already exists', async () => {
    // BDD: Given an instance "telegram.eu" already exists,
    // When the form is submitted with type=telegram slug=eu,
    // Then the 409 error message is shown inline in the dialog.
    // Traces to: US-6 — 409 error surface

    // Use the real ApiError class so isApiError() recognises it (instanceof check).
    const { ApiError } = await import('@/lib/api-error')
    const conflictError = new ApiError(409, 'An instance with that id already exists: "telegram.eu"')
    mockCreateChannelInstance.mockRejectedValueOnce(conflictError)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    await openAddDialog(user)

    // Select type
    await selectChannelType('telegram')

    // Enter a valid slug
    const slugInput = screen.getByTestId('add-instance-slug-input')
    await user.type(slugInput, 'eu')

    // Submit
    const submitBtn = screen.getByTestId('add-instance-submit-btn')
    await user.click(submitBtn)

    // Wait for the server error to appear inline
    const serverError = await screen.findByTestId('add-instance-server-error')
    expect(serverError).toBeInTheDocument()
    expect(serverError.textContent).toMatch(/already exists/i)
  })
})

describe('ConnectorsScreen — delete instance', () => {
  it('fires DELETE for the instance id when confirm is clicked', async () => {
    // BDD: Given a channel instance "whatsapp.eu" is in the list,
    // When the delete button is clicked and the confirmation is confirmed,
    // Then DELETE is called with the correct instance id.
    // Traces to: US-10 / AC-2 — instance delete removes config+creds+store
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_EU])
    mockDeleteChannelInstance.mockResolvedValueOnce(undefined)

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    // Wait for the channel list to render
    const deleteBtn = await screen.findByTestId('channel-delete-btn-whatsapp.eu')
    await user.click(deleteBtn)

    // Confirmation dialog should appear
    const dialog = await screen.findByTestId('delete-instance-dialog')
    expect(dialog).toBeInTheDocument()
    expect(dialog.textContent).toMatch(/whatsapp\.eu/i)

    // Click the confirm button
    const confirmBtn = screen.getByTestId('delete-instance-confirm-btn')
    await user.click(confirmBtn)

    await waitFor(() => {
      expect(mockDeleteChannelInstance).toHaveBeenCalledTimes(1)
    })
    expect(mockDeleteChannelInstance).toHaveBeenCalledWith('whatsapp.eu')
  })

  it('does NOT fire DELETE when cancel is clicked', async () => {
    // BDD: Given a channel instance "whatsapp.eu" is in the list,
    // When the delete button is clicked but cancel is pressed,
    // Then DELETE is NOT called.
    // Traces to: US-10 — delete is a destructive action; cancel aborts
    mockFetchChannels.mockResolvedValue([STUB_WHATSAPP_EU])

    const user = userEvent.setup()
    const { ConnectorsScreen } = await import('./ConnectorsScreen')
    render(React.createElement(ConnectorsScreen), { wrapper })

    // Open delete dialog
    const deleteBtn = await screen.findByTestId('channel-delete-btn-whatsapp.eu')
    await user.click(deleteBtn)

    const dialog = await screen.findByTestId('delete-instance-dialog')
    expect(dialog).toBeInTheDocument()

    // Click cancel
    const cancelBtn = screen.getByTestId('delete-instance-cancel-btn')
    await user.click(cancelBtn)

    // DELETE must NOT have been called
    expect(mockDeleteChannelInstance).not.toHaveBeenCalled()

    // Dialog should be closed
    await waitFor(() => {
      expect(screen.queryByTestId('delete-instance-dialog')).not.toBeInTheDocument()
    })
  })
})
