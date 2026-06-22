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
 * 3. Save calls saveMailboxConfig (the email channel config endpoint seam)
 *    with the correct payload.
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
    fetchMailboxConfig: vi.fn(),
    saveMailboxConfig: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspaces: vi.fn(),
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
  fetchMailboxConfig,
  saveMailboxConfig,
  fetchAgents,
  fetchWorkspaces,
  fetchChannels,
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

function renderPanel(configOverrides?: Record<string, unknown>) {
  const client = makeQueryClient()
  client.setQueryData(['mailbox-config'], configOverrides ?? {})
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
    { id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false },
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
      { id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false } as never,
      { id: 'email', name: 'Email', transport: 'email', enabled: false } as never,
    ])
    vi.mocked(fetchMailboxConfig).mockResolvedValue({})
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
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
    vi.mocked(fetchMailboxConfig).mockResolvedValue({})
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

  it('password field is empty even when server config returns other fields', async () => {
    // Backend never returns the actual password — simulate a config with all fields
    // except password (only password_ref is returned when a credential is stored)
    renderPanel({
      username: 'bot@example.com',
      imap_host: 'imap.gmail.com',
      smtp_host: 'smtp.gmail.com',
      // no password field — only password_ref
      password_ref: 'cred:email_password',
    })
    await waitFor(() => {
      const usernameField = screen.getByTestId('mailbox-username') as HTMLInputElement
      expect(usernameField.value).toBe('bot@example.com')
    })
    // Password field must remain empty — never pre-filled
    const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
    expect(pwField.value).toBe('')
  })

  it('shows "(stored)" placeholder hint when password_ref is present', async () => {
    renderPanel({ password_ref: 'cred:email_password' })
    await waitFor(() => {
      const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
      expect(pwField.placeholder).toMatch(/stored/)
    })
  })

  it('shows generic placeholder when no credential is stored', async () => {
    renderPanel({})
    await waitFor(() => {
      const pwField = screen.getByTestId('mailbox-password') as HTMLInputElement
      expect(pwField.placeholder).not.toMatch(/stored/)
    })
  })
})

describe('EmailMailboxPanel — Save calls the correct API seam', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchMailboxConfig).mockResolvedValue({})
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true } as never,
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 } as never,
    ])
    vi.mocked(saveMailboxConfig).mockResolvedValue(undefined)
  })

  it('Save calls saveMailboxConfig with the entered values', async () => {
    renderPanel()
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
      expect(vi.mocked(saveMailboxConfig)).toHaveBeenCalled()
    })

    const [payload] = vi.mocked(saveMailboxConfig).mock.calls[0] as [Record<string, unknown>]
    expect(payload['username']).toBe('agent@example.com')
    expect(payload['imap_host']).toBe('imap.example.com')
    expect(payload['smtp_host']).toBe('smtp.example.com')
    expect(payload['password']).toBe('supersecret')
  })

  it('Save omits password from payload when the password field is empty', async () => {
    // Simulates rotating: user opens the panel, leaves password blank, saves
    // (should leave the stored credential intact, not overwrite with empty)
    renderPanel({ username: 'agent@example.com', imap_host: 'imap.example.com', smtp_host: 'smtp.example.com', password_ref: 'cred:x' })
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
      expect(vi.mocked(saveMailboxConfig)).toHaveBeenCalled()
    })

    const [payload] = vi.mocked(saveMailboxConfig).mock.calls[0] as [Record<string, unknown>]
    // password must NOT be in the payload — leave the stored credential untouched
    expect(Object.prototype.hasOwnProperty.call(payload, 'password')).toBe(false)
  })
})

describe('EmailMailboxPanel — client-side validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchMailboxConfig).mockResolvedValue({})
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(saveMailboxConfig).mockResolvedValue(undefined)
  })

  it('shows an error when username is blank and Save is clicked', async () => {
    renderPanel()
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
    // Must NOT call saveMailboxConfig when validation fails
    expect(vi.mocked(saveMailboxConfig)).not.toHaveBeenCalled()
  })

  it('shows an error when imap_host is blank and Save is clicked', async () => {
    renderPanel()
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
    expect(vi.mocked(saveMailboxConfig)).not.toHaveBeenCalled()
  })
})
