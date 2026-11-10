import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Mailbox } from '@/lib/api/generated/openapi-types'

// Process-edge API responses only. MailboxRow, the screen, Query, Card,
// Badge, closed configuration panels and their controls remain real.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: vi.fn().mockResolvedValue([]),
    fetchMailboxes: vi.fn(),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
  }
})

import { fetchMailboxes } from '@/lib/api'
import { ConnectorsScreen } from './ConnectorsScreen'

// #1277 founder 2026-10-12 and accepted CHECK F3: opening the affected
// screen must show the integration's error and exact missing-secret name.
const missingRef = 'QA1277_UI_MAIL_PASSWORD'
const reason = `Secret reference ${missingRef} is missing from the credential store.`
const bad: Mailbox = {
  agent_id: 'qa-mail-agent',
  workspace_id: 'qa-bad-workspace',
  enabled: true,
  configured: false,
  username: 'bad@example.invalid',
  status: 'degraded',
  status_reason: reason,
}
const healthy: Mailbox = {
  agent_id: 'qa-mail-agent',
  workspace_id: 'qa-healthy-workspace',
  enabled: true,
  configured: true,
  username: 'healthy@example.invalid',
  status: 'ok',
}

let client: QueryClient

function renderScreen(mailboxes: Mailbox[]) {
  vi.mocked(fetchMailboxes).mockResolvedValue(mailboxes)
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(<QueryClientProvider client={client}><ConnectorsScreen /></QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  client?.clear()
})

describe('#1277 mailbox credential-error reachability', () => {
  it('shows degraded status and the exact missing-secret reason on only the affected mailbox', async () => {
    renderScreen([bad, healthy])
    const affected = await screen.findByTestId('mailbox-row-qa-mail-agent-qa-bad-workspace')
    const good = await screen.findByTestId('mailbox-row-qa-mail-agent-qa-healthy-workspace')
    expect(within(good).getByText('Active')).toBeVisible()
    expect(within(good).queryByText(reason)).not.toBeInTheDocument()
    expect(within(good).queryByText(/degraded/i)).not.toBeInTheDocument()
    expect(within(affected).queryByText('Active')).not.toBeInTheDocument()
    expect(within(affected).getByText(reason)).toBeVisible()
    expect(within(affected).getByText(/^degraded$/i)).toBeVisible()
  })

  it('does not report Active when the authoritative mailbox status is degraded even if configured is true', async () => {
    renderScreen([{ ...bad, configured: true }, healthy])
    const affected = await screen.findByTestId('mailbox-row-qa-mail-agent-qa-bad-workspace')
    expect(within(affected).queryByText('Active')).not.toBeInTheDocument()
    expect(within(affected).getByText(reason)).toBeVisible()
    expect(within(affected).getByText(/^degraded$/i)).toBeVisible()
  })

  it('keeps a healthy mailbox Active with no phantom credential error', async () => {
    renderScreen([healthy])
    const row = await screen.findByTestId('mailbox-row-qa-mail-agent-qa-healthy-workspace')
    expect(within(row).getByText('Active')).toBeVisible()
    expect(within(row).getByText(/healthy@example\.invalid/)).toBeVisible()
    expect(screen.queryByText(reason)).not.toBeInTheDocument()
    expect(within(row).queryByText(/degraded/i)).not.toBeInTheDocument()
  })
})
