/**
 * EmailMailboxPanel.test.tsx
 *
 * Tests for the email mailbox account config panel and its integration in
 * the Connectors screen.
 *
 * Covers:
 * 1. The email mailbox account card renders in ConnectorsScreen with a
 *    "Configure" button that opens the EmailMailboxPanel.
 * 2. The password field is write-only: it is never pre-filled from the
 *    server config (the backend never returns the password value; only
 *    password_ref is returned when a credential is stored).
 * 3. Save calls saveAgentMailbox (PUT /agents/{id}/mailbox — M11 per-agent
 *    endpoints; the legacy /channels/email path is dead) with the correct payload.
 * 4. An empty password on Save does NOT include the password field in the
 *    payload (leave-existing-credential-intact semantics).
 * 5. Validation: required fields (username, imap_host, smtp_host) show errors
 *    when blank on submit.
 * 6. When password_ref is present in the GET response, the password placeholder
 *    says "(stored — enter a new value to rotate)".
 * 7. Email channel entry is excluded from the conversational channels list.
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// ── Mocks ─────────────────────────────────────────────────────────────────────

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: vi.fn(),
    findConfiguredMailbox: vi.fn(),
    saveAgentMailbox: vi.fn(),
    deleteAgentMailbox: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspaces: vi.fn(),
    // ADR-031 Track 2 — ConnectorsScreen resolves each configured row's
    // workspace→agent binding via getChannelRouting; mock it so the real
    // (unmocked) implementation never fires a network call in Node.
    getChannelRouting: vi.fn(),
    enableChannel: vi.fn(),
    disableChannel: vi.fn(),
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
  findConfiguredMailbox,
  saveAgentMailbox,
  fetchAgents,
  fetchWorkspaces,
  fetchChannels,
  getChannelRouting,
} from '@/lib/api'
import { EmailMailboxPanel } from './EmailMailboxPanel'
import { ConnectorsScreen } from '@/components/screens/ConnectorsScreen'

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

// mailbox: the seeded per-agent Mailbox wire object (null = none configured).
function renderPanel(mailbox?: Record<string, unknown> | null) {
  const client = makeQueryClient()
  client.setQueryData(['agent-mailboxes'], mailbox ?? null)
  vi.mocked(findConfiguredMailbox).mockResolvedValue((mailbox ?? null) as never)
  client.setQueryData(['agents'], [
    { id: 'mia', name: 'Mia', type: 'core', locked: true },
  ])
  client.setQueryData(['workspaces'], [
    { id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 },
  ])

  const onOpenChange = vi.fn()
  const result = render(
    <QueryClientProvider client={client}>
      <EmailMailboxPanel open={true} onOpenChange={onOpenChange} />
    </QueryClientProvider>,
  )
  return { ...result, onOpenChange }
}

function renderConnectorsScreen() {
  const client = makeQueryClient()
  client.setQueryData(['channels'], [
    // instance_id set — a real configured instance (FR-008), not the static
    // "available but unconfigured" placeholder row.
    { id: 'telegram', instance_id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false, identity: { kind: 'agent', id: 'mia' } },
    { id: 'email', name: 'Email', transport: 'email', enabled: false },
  ])

  return render(
    <QueryClientProvider client={client}>
      <ConnectorsScreen />
    </QueryClientProvider>,
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('ConnectorsScreen — email mailbox account section', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannels).mockResolvedValue([
      { id: 'telegram', instance_id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false, identity: { kind: 'agent', id: 'mia' } } as never,
      { id: 'email', name: 'Email', transport: 'email', enabled: false } as never,
    ])
    vi.mocked(findConfiguredMailbox).mockResolvedValue(null)
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(getChannelRouting).mockResolvedValue({})
  })

  it('renders the email mailbox account card', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      expect(screen.getByTestId('email-mailbox-card')).toBeInTheDocument()
    })
    expect(screen.getByText('Email Mailbox')).toBeInTheDocument()
  })

  it('shows a Configure button for the email mailbox', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      expect(screen.getByTestId('email-mailbox-configure-btn')).toBeInTheDocument()
    })
  })

  it('excludes the email channel from the conversational channels list', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      // Telegram should appear in the channels list
      expect(screen.getByTestId('channel-card-telegram')).toBeInTheDocument()
    })
    // Email channel must NOT appear as a channel card (it's in the mailbox section)
    expect(screen.queryByTestId('channel-card-email')).not.toBeInTheDocument()
  })

  it('clicking Configure opens the EmailMailboxPanel slide-over', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      expect(screen.getByTestId('email-mailbox-configure-btn')).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByTestId('email-mailbox-configure-btn'))
    })
    await waitFor(() => {
      expect(screen.getByText('Email Mailbox Account')).toBeInTheDocument()
    })
  })
})

describe('EmailMailboxPanel — write-only password field', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(findConfiguredMailbox).mockResolvedValue(null)
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
  })

  it('renders the password field with type=password (hidden by default)', async () => {
    renderPanel()
    await waitFor(() => {
      const pwField = screen.getByTestId('mailbox-password')
      expect(pwField).toBeInTheDocument()
      expect((pwField as HTMLInputElement).type).toBe('password')
    })
  })

  it('password field is empty even when the server returns other fields', async () => {
    // Backend never returns the actual password — the Mailbox wire type only
    // carries `configured` (whether a stored credential resolves).
    renderPanel({
      agent_id: 'mia',
      enabled: true,
      workspace_id: 'ws-1',
      username: 'bot@example.com',
      imap_host: 'imap.gmail.com',
      smtp_host: 'smtp.gmail.com',
      configured: true,
    })
    await waitFor(() => {
      const usernameField = screen.getByTestId('mailbox-username') as HTMLInputElement
      expect(usernameField.value).toBe('bot@example.com')
    })
    // Password field must remain empty — never pre-filled
    const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
    expect(pwField.value).toBe('')
  })

  it('shows "(stored)" placeholder hint when a stored credential resolves (configured)', async () => {
    renderPanel({ agent_id: 'mia', enabled: true, configured: true })
    await waitFor(() => {
      const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
      expect(pwField.placeholder).toMatch(/stored/)
    })
  })

  it('shows generic placeholder when no credential is stored', async () => {
    renderPanel(null)
    await waitFor(() => {
      const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
      expect(pwField.placeholder).not.toMatch(/stored/)
    })
  })
})

describe('EmailMailboxPanel — Save calls the per-agent mailbox endpoint', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(findConfiguredMailbox).mockResolvedValue(null)
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true } as never,
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 } as never,
    ])
    vi.mocked(saveAgentMailbox).mockResolvedValue({ agent_id: 'mia', enabled: true, configured: true } as never)
  })

  it('Save calls saveAgentMailbox(agentId, req) with the entered values', async () => {
    // Agent + workspace are pre-bound (seeded mailbox) — the user edits the
    // credential fields and saves.
    renderPanel({ agent_id: 'mia', enabled: true, workspace_id: 'ws-1', configured: false })
    await waitFor(() => {
      expect(screen.getByTestId('mailbox-username')).toBeInTheDocument()
    })

    fireEvent.change(screen.getByTestId('mailbox-username'), { target: { value: 'agent@example.com' } })
    fireEvent.change(screen.getByTestId('mailbox-imap-host'), { target: { value: 'imap.example.com' } })
    fireEvent.change(screen.getByTestId('mailbox-smtp-host'), { target: { value: 'smtp.example.com' } })
    fireEvent.change(screen.getByTestId('mailbox-password'), { target: { value: 'supersecret' } })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save Mailbox/i }))
    })

    await waitFor(() => {
      expect(vi.mocked(saveAgentMailbox)).toHaveBeenCalled()
    })

    const [agentId, payload] = vi.mocked(saveAgentMailbox).mock.calls[0] as [string, Record<string, unknown>]
    expect(agentId).toBe('mia')
    expect(payload['enabled']).toBe(true)
    expect(payload['workspace_id']).toBe('ws-1')
    expect(payload['username']).toBe('agent@example.com')
    expect(payload['imap_host']).toBe('imap.example.com')
    expect(payload['smtp_host']).toBe('smtp.example.com')
    expect(payload['password']).toBe('supersecret')
  })

  it('Save omits password from the request when the password field is empty', async () => {
    // Simulates rotating: user opens the panel, leaves password blank, saves
    // (should leave the stored credential intact, not overwrite with empty)
    renderPanel({ agent_id: 'mia', enabled: true, workspace_id: 'ws-1', username: 'agent@example.com', imap_host: 'imap.example.com', smtp_host: 'smtp.example.com', configured: true })
    await waitFor(() => {
      const usernameField = screen.getByTestId('mailbox-username') as HTMLInputElement
      expect(usernameField.value).toBe('agent@example.com')
    })

    // Password field is blank (not pre-filled)
    const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
    expect(pwField.value).toBe('')

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save Mailbox/i }))
    })

    await waitFor(() => {
      expect(vi.mocked(saveAgentMailbox)).toHaveBeenCalled()
    })

    const [, payload] = vi.mocked(saveAgentMailbox).mock.calls[0] as [string, Record<string, unknown>]
    // password must NOT be in the request — leave the stored credential untouched
    expect(Object.prototype.hasOwnProperty.call(payload, 'password')).toBe(false)
  })
})

describe('EmailMailboxPanel — client-side validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(findConfiguredMailbox).mockResolvedValue(null)
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(saveAgentMailbox).mockResolvedValue({ agent_id: 'mia', enabled: true, configured: true } as never)
  })

  it('shows an error when username is blank and Save is clicked', async () => {
    renderPanel({ agent_id: 'mia', enabled: true, workspace_id: 'ws-1', configured: false })
    await waitFor(() => {
      expect(screen.getByTestId('mailbox-imap-host')).toBeInTheDocument()
    })

    // Fill imap and smtp but leave username blank
    fireEvent.change(screen.getByTestId('mailbox-imap-host'), { target: { value: 'imap.example.com' } })
    fireEvent.change(screen.getByTestId('mailbox-smtp-host'), { target: { value: 'smtp.example.com' } })

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save Mailbox/i }))
    })

    await waitFor(() => {
      expect(screen.getByText(/Email address is required/i)).toBeInTheDocument()
    })
    // Must NOT call saveAgentMailbox when validation fails
    expect(vi.mocked(saveAgentMailbox)).not.toHaveBeenCalled()
  })

  it('shows an error when imap_host is blank and Save is clicked', async () => {
    renderPanel({ agent_id: 'mia', enabled: true, workspace_id: 'ws-1', configured: false })
    await waitFor(() => {
      expect(screen.getByTestId('mailbox-username')).toBeInTheDocument()
    })

    fireEvent.change(screen.getByTestId('mailbox-username'), { target: { value: 'bot@example.com' } })
    fireEvent.change(screen.getByTestId('mailbox-smtp-host'), { target: { value: 'smtp.example.com' } })
    // imap_host intentionally left blank

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save Mailbox/i }))
    })

    await waitFor(() => {
      expect(screen.getByText(/IMAP server hostname is required/i)).toBeInTheDocument()
    })
    expect(vi.mocked(saveAgentMailbox)).not.toHaveBeenCalled()
  })
})
