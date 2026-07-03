/**
 * EmailMailboxPanel.test.tsx
 *
 * Tests for the email mailbox account config panel and its multi-mailbox
 * integration in the Connectors screen. A mailbox belongs to exactly one
 * (agent, workspace) PAIR — the same agent can hold a different mailbox in
 * each workspace it belongs to.
 *
 * Covers:
 * 1. ConnectorsScreen's Email section renders an "Add mailbox" action plus one
 *    row per configured (agent, workspace) pair; each row's Configure opens
 *    the panel seeded with that row's mailbox, and Add mailbox opens it in
 *    create mode.
 * 2. The password field is write-only: it is never pre-filled from the
 *    server config (the backend never returns the password value; only
 *    password_ref is returned when a credential is stored).
 * 3. Save calls saveAgentMailbox (PUT /agents/{id}/mailboxes/{workspaceId} —
 *    M11 pair endpoints; the legacy /channels/email path is dead) with the
 *    correct payload — the request body carries no workspace_id.
 * 4. An empty password on Save does NOT include the password field in the
 *    payload (leave-existing-credential-intact semantics).
 * 5. Validation: required fields (username, imap_host, smtp_host) show errors
 *    when blank on submit.
 * 6. When password_ref is present in the GET response, the password placeholder
 *    says "(stored — enter a new value to rotate)".
 * 7. Email channel entry is excluded from the conversational channels list.
 * 8. Remove Mailbox (edit mode only) calls deleteAgentMailbox with the seeded
 *    mailbox's exact (agent_id, workspace_id) pair.
 * 9. The same agent with mailboxes in two different workspaces renders two
 *    distinct rows; Configure on each seeds the panel with that pair's data.
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act, within } from '@testing-library/react'
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
    fetchMailboxes: vi.fn(),
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
  fetchMailboxes,
  saveAgentMailbox,
  deleteAgentMailbox,
  fetchAgents,
  fetchWorkspaces,
  fetchChannels,
  getChannelRouting,
} from '@/lib/api'
import type { Mailbox } from '@/lib/api'
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

// mailbox: the edit-target Mailbox wire object passed as a prop directly
// (the panel no longer self-fetches — its parent, ConnectorsScreen, owns the
// ['agent-mailboxes'] list query). undefined/null = create mode.
// workspaces: optional override for the seeded ['workspaces'] cache — tests
// that need to pick a *different* workspace in the select (the move
// scenario) pass a multi-workspace list up front instead of waiting on a
// background refetch to replace the single-workspace default.
function renderPanel(mailbox?: Record<string, unknown> | null, workspaces?: Record<string, unknown>[]) {
  const client = makeQueryClient()
  client.setQueryData(['agents'], [
    { id: 'mia', name: 'Mia', type: 'core', locked: true },
  ])
  client.setQueryData(
    ['workspaces'],
    workspaces ?? [{ id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 }],
  )

  const onOpenChange = vi.fn()
  const result = render(
    <QueryClientProvider client={client}>
      <EmailMailboxPanel open={true} onOpenChange={onOpenChange} mailbox={(mailbox ?? null) as Mailbox | null} />
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
    vi.mocked(fetchMailboxes).mockResolvedValue([])
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])
    vi.mocked(getChannelRouting).mockResolvedValue({})
  })

  it('renders the email mailbox account card (empty state)', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      expect(screen.getByTestId('email-mailbox-card')).toBeInTheDocument()
      expect(screen.getByText('Email Mailbox')).toBeInTheDocument()
    })
  })

  it('shows an Add mailbox button in the Email section', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      expect(screen.getByTestId('email-mailbox-add-btn')).toBeInTheDocument()
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

  it('clicking Add mailbox opens the EmailMailboxPanel slide-over in create mode', async () => {
    renderConnectorsScreen()
    await waitFor(() => {
      expect(screen.getByTestId('email-mailbox-add-btn')).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByTestId('email-mailbox-add-btn'))
    })
    await waitFor(() => {
      expect(screen.getByText('Email Mailbox Account')).toBeInTheDocument()
    })
    // Create mode: no mailbox to remove yet.
    expect(screen.queryByTestId('mailbox-delete-btn')).not.toBeInTheDocument()
  })
})

describe('ConnectorsScreen — multi-mailbox roster (one row per (agent, workspace) pair)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchChannels).mockResolvedValue([
      { id: 'telegram', instance_id: 'telegram', name: 'Telegram', transport: 'webhook', enabled: false, identity: { kind: 'agent', id: 'mia' } } as never,
      { id: 'email', name: 'Email', transport: 'email', enabled: false } as never,
    ])
    vi.mocked(getChannelRouting).mockResolvedValue({})
  })

  it('renders one row per mailbox, titled with the owning agent\'s display name', async () => {
    vi.mocked(fetchMailboxes).mockResolvedValue([
      { agent_id: 'mia', enabled: true, configured: true, username: 'mia@example.com', workspace_id: 'ws-1' } as Mailbox,
      { agent_id: 'jim', enabled: false, configured: false, username: 'jim@example.com', workspace_id: 'ws-1' } as Mailbox,
    ])
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true } as never,
      { id: 'jim', name: 'Jim', type: 'core', locked: true } as never,
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([{ id: 'ws-1', name: 'My Workspace' } as never])

    renderConnectorsScreen()

    await waitFor(() => {
      expect(screen.getByTestId('mailbox-row-mia-ws-1')).toBeInTheDocument()
      expect(screen.getByTestId('mailbox-row-jim-ws-1')).toBeInTheDocument()
    })
    expect(within(screen.getByTestId('mailbox-row-mia-ws-1')).getByText('Mia')).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-jim-ws-1')).getByText('Jim')).toBeInTheDocument()
    // One is Active (enabled+configured), the other Not configured.
    expect(within(screen.getByTestId('mailbox-row-mia-ws-1')).getByText('Active')).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-jim-ws-1')).getByText('Not configured')).toBeInTheDocument()
  })

  it('falls back to the raw agent id when the agent name cannot be resolved', async () => {
    vi.mocked(fetchMailboxes).mockResolvedValue([
      { agent_id: 'ghost-agent', enabled: true, configured: true, username: 'ghost@example.com', workspace_id: 'ws-ghost' } as Mailbox,
    ])
    vi.mocked(fetchAgents).mockResolvedValue([])
    vi.mocked(fetchWorkspaces).mockResolvedValue([])

    renderConnectorsScreen()

    await waitFor(() => {
      expect(within(screen.getByTestId('mailbox-row-ghost-agent-ws-ghost')).getByText('ghost-agent')).toBeInTheDocument()
    })
  })

  it('Configure on a specific row opens the panel seeded with that mailbox', async () => {
    vi.mocked(fetchMailboxes).mockResolvedValue([
      {
        agent_id: 'mia',
        enabled: true,
        configured: true,
        username: 'mia@example.com',
        imap_host: 'imap.example.com',
        smtp_host: 'smtp.example.com',
        workspace_id: 'ws-1',
      } as Mailbox,
    ])
    vi.mocked(fetchAgents).mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', locked: true } as never])
    vi.mocked(fetchWorkspaces).mockResolvedValue([{ id: 'ws-1', name: 'My Workspace' } as never])

    renderConnectorsScreen()

    await waitFor(() => {
      expect(screen.getByTestId('mailbox-configure-btn-mia-ws-1')).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByTestId('mailbox-configure-btn-mia-ws-1'))
    })

    await waitFor(() => {
      expect((screen.getByTestId('mailbox-username') as HTMLInputElement).value).toBe('mia@example.com')
    })
    // Edit mode (an existing target): the Remove Mailbox action is available.
    expect(screen.getByTestId('mailbox-delete-btn')).toBeInTheDocument()
  })

  it('Remove Mailbox calls deleteAgentMailbox with the seeded row\'s exact (agent_id, workspace_id) pair', async () => {
    vi.mocked(fetchMailboxes).mockResolvedValue([
      { agent_id: 'mia', enabled: true, configured: true, username: 'mia@example.com', workspace_id: 'ws-1' } as Mailbox,
    ])
    vi.mocked(fetchAgents).mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', locked: true } as never])
    vi.mocked(fetchWorkspaces).mockResolvedValue([{ id: 'ws-1', name: 'My Workspace' } as never])
    vi.mocked(deleteAgentMailbox).mockResolvedValue({ success: true } as never)

    renderConnectorsScreen()

    await waitFor(() => {
      expect(screen.getByTestId('mailbox-configure-btn-mia-ws-1')).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByTestId('mailbox-configure-btn-mia-ws-1'))
    })

    await waitFor(() => {
      expect(screen.getByTestId('mailbox-delete-btn')).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByTestId('mailbox-delete-btn'))
    })

    await waitFor(() => {
      expect(vi.mocked(deleteAgentMailbox)).toHaveBeenCalledWith('mia', 'ws-1')
    })
  })

  it('the same agent with mailboxes in two workspaces renders two distinct rows', async () => {
    vi.mocked(fetchMailboxes).mockResolvedValue([
      { agent_id: 'mia', enabled: true, configured: true, username: 'mia-eu@example.com', workspace_id: 'ws-eu' } as Mailbox,
      { agent_id: 'mia', enabled: true, configured: true, username: 'mia-us@example.com', workspace_id: 'ws-us' } as Mailbox,
    ])
    vi.mocked(fetchAgents).mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', locked: true } as never])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-eu', name: 'EU Workspace' } as never,
      { id: 'ws-us', name: 'US Workspace' } as never,
    ])

    renderConnectorsScreen()

    await waitFor(() => {
      expect(screen.getByTestId('mailbox-row-mia-ws-eu')).toBeInTheDocument()
      expect(screen.getByTestId('mailbox-row-mia-ws-us')).toBeInTheDocument()
    })
    // Both rows are titled "Mia" but carry distinct workspace subtitles.
    expect(within(screen.getByTestId('mailbox-row-mia-ws-eu')).getByText('Mia')).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-mia-ws-us')).getByText('Mia')).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-mia-ws-eu')).getByText(/mia-eu@example\.com/)).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-mia-ws-eu')).getByText(/EU Workspace/)).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-mia-ws-us')).getByText(/mia-us@example\.com/)).toBeInTheDocument()
    expect(within(screen.getByTestId('mailbox-row-mia-ws-us')).getByText(/US Workspace/)).toBeInTheDocument()
  })

  it('Configure on each of the same agent\'s two pair-rows seeds the panel with that pair\'s username', async () => {
    vi.mocked(fetchMailboxes).mockResolvedValue([
      { agent_id: 'mia', enabled: true, configured: true, username: 'mia-eu@example.com', workspace_id: 'ws-eu' } as Mailbox,
      { agent_id: 'mia', enabled: true, configured: true, username: 'mia-us@example.com', workspace_id: 'ws-us' } as Mailbox,
    ])
    vi.mocked(fetchAgents).mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core', locked: true } as never])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-eu', name: 'EU Workspace' } as never,
      { id: 'ws-us', name: 'US Workspace' } as never,
    ])

    renderConnectorsScreen()

    await waitFor(() => {
      expect(screen.getByTestId('mailbox-configure-btn-mia-ws-us')).toBeInTheDocument()
    })
    await act(async () => {
      fireEvent.click(screen.getByTestId('mailbox-configure-btn-mia-ws-us'))
    })
    await waitFor(() => {
      expect((screen.getByTestId('mailbox-username') as HTMLInputElement).value).toBe('mia-us@example.com')
    })
  })
})

describe('EmailMailboxPanel — write-only password field', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
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

describe('EmailMailboxPanel — Save calls the (agent, workspace) pair mailbox endpoint', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true } as never,
    ])
    vi.mocked(fetchWorkspaces).mockResolvedValue([
      { id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 } as never,
    ])
    vi.mocked(saveAgentMailbox).mockResolvedValue({ agent_id: 'mia', enabled: true, configured: true } as never)
  })

  it('Save calls saveAgentMailbox(agentId, workspaceId, req) with the entered values and NO workspace_id in the body', async () => {
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

    const [agentId, workspaceId, payload] = vi.mocked(saveAgentMailbox).mock.calls[0] as [string, string, Record<string, unknown>]
    expect(agentId).toBe('mia')
    expect(workspaceId).toBe('ws-1')
    expect(payload['enabled']).toBe(true)
    expect(payload['username']).toBe('agent@example.com')
    expect(payload['imap_host']).toBe('imap.example.com')
    expect(payload['smtp_host']).toBe('smtp.example.com')
    expect(payload['password']).toBe('supersecret')
    // MailboxConfigureRequest no longer carries workspace_id — both ids ride in the path.
    expect(Object.prototype.hasOwnProperty.call(payload, 'workspace_id')).toBe(false)
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

    const [, , payload] = vi.mocked(saveAgentMailbox).mock.calls[0] as [string, string, Record<string, unknown>]
    // password must NOT be in the request — leave the stored credential untouched
    expect(Object.prototype.hasOwnProperty.call(payload, 'password')).toBe(false)
  })

  it('changing the workspace on an existing mailbox is a MOVE — deletes the old pair, then saves the new one', async () => {
    vi.mocked(deleteAgentMailbox).mockResolvedValue({ success: true } as never)
    // jsdom lacks scrollIntoView; Radix Select calls it when opening the listbox.
    Element.prototype.scrollIntoView = vi.fn()

    const twoWorkspaces = [
      { id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 },
      { id: 'ws-2', name: 'Other Workspace', status: 'active', pinned: false, pin_order: 0 },
    ]
    // The panel's ['workspaces'] query has staleTime 0, so it refetches on
    // mount via fetchWorkspaces — override the block's single-workspace
    // default so the background refetch doesn't shrink the list back down
    // to one item out from under the open listbox.
    vi.mocked(fetchWorkspaces).mockResolvedValue(twoWorkspaces as never)

    renderPanel(
      {
        agent_id: 'mia',
        enabled: true,
        workspace_id: 'ws-1',
        username: 'mia@example.com',
        imap_host: 'imap.example.com',
        smtp_host: 'smtp.example.com',
        configured: true,
      },
      twoWorkspaces,
    )

    await waitFor(() => {
      expect((screen.getByTestId('mailbox-username') as HTMLInputElement).value).toBe('mia@example.com')
    })

    // Two comboboxes render in DOM order: owning agent, then workspace.
    const workspaceTrigger = document.body.querySelectorAll('[role="combobox"]')[1] as HTMLElement
    fireEvent.click(workspaceTrigger)
    const option = await screen.findByRole('option', { name: 'Other Workspace' })
    fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
    fireEvent.click(option)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Save Mailbox/i }))
    })

    await waitFor(() => {
      expect(vi.mocked(saveAgentMailbox)).toHaveBeenCalled()
    })

    // The OLD pair is deleted first...
    expect(vi.mocked(deleteAgentMailbox)).toHaveBeenCalledWith('mia', 'ws-1')
    // ...then the mailbox is saved under the NEW pair.
    const [agentId, workspaceId] = vi.mocked(saveAgentMailbox).mock.calls[0] as [string, string, Record<string, unknown>]
    expect(agentId).toBe('mia')
    expect(workspaceId).toBe('ws-2')

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })
})

describe('EmailMailboxPanel — client-side validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
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
