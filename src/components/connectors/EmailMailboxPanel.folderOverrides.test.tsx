// W3 RED pack — C9 (spec §8.4; file name per spec §8.4's table; lives in the
// connectors tree beside the panel it tests — CI group components-misc).
//
// Oracle source: spec §4 US-4 (AS-1…AS-5), §7 scenarios 4.1/4.4, FR-W3-10.
// Expected values derived from the spec BEFORE the panel was read
// (receipts/w3-red-derivation.md). The panel runs REAL; mocks sit at the
// network edge (@/lib/api) and the ui-store edge.
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

const {
  fetchChannels,
  fetchMailboxes,
  saveAgentMailbox,
  deleteAgentMailbox,
  fetchAgents,
  fetchWorkspace,
  fetchWorkspaces,
  fetchChannelRouting,
  enableChannel,
  disableChannel,
  fetchMailFolders,
} = vi.hoisted(() => ({
  fetchChannels: vi.fn(),
  fetchMailboxes: vi.fn(),
  saveAgentMailbox: vi.fn(),
  deleteAgentMailbox: vi.fn(),
  fetchAgents: vi.fn(),
  fetchWorkspace: vi.fn(),
  fetchWorkspaces: vi.fn(),
  fetchChannelRouting: vi.fn(),
  enableChannel: vi.fn(),
  disableChannel: vi.fn(),
  fetchMailFolders: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels,
    fetchMailboxes,
    saveAgentMailbox,
    deleteAgentMailbox,
    fetchAgents,
    fetchWorkspace,
    fetchWorkspaces,
    fetchChannelRouting,
    enableChannel,
    disableChannel,
  }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return { ...actual, fetchMailFolders }
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
import type { Mailbox } from '@/lib/api'
import { EmailMailboxPanel } from './EmailMailboxPanel'

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

function mailboxFixture(over: Record<string, unknown> = {}): Mailbox {
  return {
    agent_id: 'mia',
    workspace_id: 'ws-1',
    enabled: true,
    configured: true,
    username: 'mia@example.test',
    imap_host: 'imap.example.test',
    smtp_host: 'smtp.example.test',
    ...over,
  } as Mailbox
}

function renderPanel(mailbox: Mailbox) {
  vi.mocked(fetchWorkspace).mockResolvedValue({
    id: 'ws-1', name: 'My Workspace', status: 'active', core_team: ['mia'],
  } as never)
  const client = makeQueryClient()
  client.setQueryData(['agents'], [{ id: 'mia', name: 'Mia', type: 'core', locked: true }])
  client.setQueryData(['workspaces'], [{ id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 }])
  return render(
    <QueryClientProvider client={client}>
      <EmailMailboxPanel open={true} onOpenChange={vi.fn()} mailbox={mailbox} mailboxes={[mailbox]} />
    </QueryClientProvider>,
  )
}

const FOLDERS_PRESENT = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 1, unread_count: 0, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null, availability: 'present', uidvalidity: 777, mapping_source: 'special_use' },
  ],
}

describe('EmailMailboxPanel folder-name overrides (C9: US-4, scenarios 4.1/4.4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockUiStore()
    fetchChannels.mockResolvedValue([])
    fetchMailboxes.mockResolvedValue([])
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchChannelRouting.mockResolvedValue([])
    saveAgentMailbox.mockResolvedValue({ ok: true })
    deleteAgentMailbox.mockResolvedValue(undefined)
    enableChannel.mockResolvedValue(undefined)
    disableChannel.mockResolvedValue(undefined)
    fetchMailFolders.mockResolvedValue(FOLDERS_PRESENT)
  })

  it('scenario 4.1 — the fields show the saved value or the "Automatic" placeholder; the helpers spell out both semantics', async () => {
    renderPanel(mailboxFixture({ sent_folder_name: '', drafts_folder_name: 'Odchozí' }))
    // "Sent folder name" and "Drafts folder name" are present and optional
    // (US-4 AS-1) — empty shows the placeholder "Automatic".
    expect(await screen.findByTestId('mailbox-sent-folder')).toHaveValue('')
    expect(screen.getByTestId('mailbox-sent-folder')).toHaveAttribute('placeholder', 'Automatic')
    expect(screen.getByTestId('mailbox-drafts-folder')).toHaveValue('Odchozí')
    // The helper text explains the empty field's automatic semantics (AS-2)…
    expect(screen.getByText('Leave empty to find the folder automatically.')).toBeInTheDocument()
    // …and the non-empty field's override semantics (AS-3).
    expect(screen.getByText('Uses this exact folder name on your mail server.')).toBeInTheDocument()
  })

  it('scenario 4.1 — saving with a field empty clears that override through the existing save path; no extra checkbox exists', async () => {
    renderPanel(mailboxFixture({ sent_folder_name: '', drafts_folder_name: 'Odchozí' }))
    await screen.findByTestId('mailbox-sent-folder')
    // US-4 AS-2: no checkbox mediates the override — empty IS the automatic.
    expect(screen.queryByRole('checkbox', { name: /sent|drafts|automatic/i })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save mailbox/i }))
    await waitFor(() => expect(saveAgentMailbox).toHaveBeenCalledTimes(1))
    // saveAgentMailbox(agentId, workspaceId, request) — the generated request
    // body is the third argument.
    const req = saveAgentMailbox.mock.calls[0][2] as Record<string, unknown>
    // The generated request carries BOTH fields verbatim: the empty Sent
    // clears the override (the backend's clear-to-automatic behaviour), and
    // the Drafts name round-trips.
    expect(req.sent_folder_name).toBe('')
    expect(req.drafts_folder_name).toBe('Odchozí')
  })

  it('scenario 4.4 — a legacy stored name survives as a deliberate override until the user clears it', async () => {
    renderPanel(mailboxFixture({ sent_folder_name: 'Sent', drafts_folder_name: '' }))
    // The stored name shows as the FIELD VALUE (an override) — the panel
    // never discards or rewrites it on the user's behalf (US-4 AS-5).
    expect(await screen.findByTestId('mailbox-sent-folder')).toHaveValue('Sent')
    // Its helper announces the override semantics, not automatic discovery.
    expect(screen.getByText('Uses this exact folder name on your mail server.')).toBeInTheDocument()
    // Rendering alone saves nothing — no migration, no clearing.
    await waitFor(() => { expect(saveAgentMailbox).not.toHaveBeenCalled() })
  })

  it('US-4 AS-4 — an override the server cannot confirm warns on the exact field, naming it and the way out', async () => {
    fetchMailFolders.mockResolvedValue({
      folders: [
        FOLDERS_PRESENT.folders[0],
        { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null, availability: 'unknown', uidvalidity: 777, mapping_source: 'override' },
        FOLDERS_PRESENT.folders[2],
      ],
    })
    renderPanel(mailboxFixture({ sent_folder_name: 'Odchozí', drafts_folder_name: '' }))
    await screen.findByTestId('mailbox-sent-folder')
    // The warning is bound to the Sent field's row: it names the field and is
    // actionable (check the name, or clear it). It is never silently ignored.
    const warning = await screen.findByTestId('mailbox-sent-folder-unresolved')
    expect(warning).toHaveTextContent(/Sent/)
    expect(warning.textContent).toMatch(/couldn't confirm/i)
    expect(warning.textContent).toMatch(/clear the field|check the name/i)
  })
})
