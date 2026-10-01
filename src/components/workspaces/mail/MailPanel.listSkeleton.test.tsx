/**
 * RED — IMPORTANT: MailPanel's loading skeleton reintroduces the rejected
 * `animate-pulse` pattern.
 *
 * Spec source: src/components/ui/skeleton.tsx::Skeleton's own doc comment
 * (lines 23-28): "skeleton-shimmer, not animate-pulse: the pulse throbs the
 * whole block's opacity 1 -> .5 -> 1 over 2s, which reads as blinking and
 * makes a page of placeholders look like it is breathing. The shimmer
 * sweeps a lighter band across a static surface..." — this is the
 * catalogued component's own documented rejection of `animate-pulse`, cited
 * verbatim by the brief. `ListSkeleton` (MailPanel.tsx lines 875-884) hand-
 * rolls three `<div className="... animate-pulse" />` rows instead of using
 * the catalogued `Skeleton` component, so it also misses
 * `useLoadingVisibility`'s delayed-show behavior — `Skeleton` stamps
 * `data-visible={visible}` (skeleton.tsx line 21) on every instance; the
 * hand-rolled rows carry no such attribute at all.
 *
 * Oracle: derived directly from skeleton.tsx's own documented contract, not
 * from what ListSkeleton currently renders — (1) no descendant of the mail
 * list-loading region carries `animate-pulse`, and (2) each skeleton row
 * carries the catalogued component's `data-visible` fingerprint, proving it
 * is actually `Skeleton`, not merely a different animation with the same
 * absent class.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const {
  fetchAgents,
  fetchMailboxes,
  fetchMailFolders,
  fetchMailMessages,
  fetchMailSummary,
} = vi.hoisted(() => ({
  fetchAgents: vi.fn(),
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
  fetchMailMessages: vi.fn(),
  fetchMailSummary: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgents, fetchMailboxes }
})

vi.mock('@/lib/api/mail', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/mail')>()
  return {
    ...actual,
    fetchMailFolders,
    fetchMailMessages,
    fetchMailSummary,
  }
})

async function loadPanel(): Promise<React.ComponentType<{ workspaceId: string }>> {
  const specifier = './MailPanel'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPanel?: React.ComponentType<{ workspaceId: string }> }
    if (typeof mod.MailPanel !== 'function') throw new Error('MailPanel is not a function export')
    return mod.MailPanel
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPanel not implemented — required by spec §16 / US-3. ' + detail, {
      cause: err,
    })
  }
}

function renderPanel(node: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

const folders = {
  folders: [
    { slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: 0 },
    { slug: 'sent', display_name: 'Sent', total: 0, unread_count: null },
    { slug: 'drafts', display_name: 'Drafts', total: 0, unread_count: null },
  ],
}

describe('MailPanel — ListSkeleton uses the catalogued Skeleton, not animate-pulse', () => {
  beforeEach(() => {
    fetchAgents.mockReset()
    fetchMailboxes.mockReset()
    fetchMailFolders.mockReset()
    fetchMailMessages.mockReset()
    fetchMailSummary.mockReset()
    fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia' }])
    fetchMailboxes.mockResolvedValue([
      { agent_id: 'mia', workspace_id: 'ws-1', enabled: true, configured: true, username: 'mia@example.test' },
    ])
    fetchMailFolders.mockResolvedValue(folders)
    // Never resolves — keeps messagesQuery.isPending true so ListSkeleton
    // stays on screen for the assertion, same technique
    // MailPanel.states.test.tsx already uses for a pending detail fetch.
    fetchMailMessages.mockImplementation(() => new Promise(() => {}))
    fetchMailSummary.mockResolvedValue({ items: [] })
  })

  afterEach(() => cleanup())

  it('renders the list-loading skeleton with no animate-pulse class anywhere in it', async () => {
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)

    const loadingRegion = await screen.findByTestId('mail-list-loading')
    const offenders = loadingRegion.querySelectorAll('.animate-pulse, [class*="animate-pulse"]')
    expect(offenders.length).toBe(0)
  })

  it('renders the catalogued Skeleton component (its data-visible fingerprint), not a lookalike animation', async () => {
    const MailPanel = await loadPanel()
    renderPanel(<MailPanel workspaceId="ws-1" />)

    const loadingRegion = await screen.findByTestId('mail-list-loading')
    // skeleton.tsx::Skeleton is the only component in this codebase that
    // stamps data-visible from useLoadingVisibility — a hand-rolled
    // animate-pulse div (or any other lookalike) carries no such attribute.
    const skeletonRows = loadingRegion.querySelectorAll('[data-visible]')
    expect(skeletonRows.length).toBeGreaterThan(0)
  })
})
