// Export & Import wizard reload table — spec order 28 (issue #1967 B1).
// Oracles: the spec scenario outline "Reload mid-import" (all rows), "Leaving
// the wizard cleans up silently", and FR-025/FR-026 (never an apply request;
// cancel count EXACTLY 0 or 1, read from a LIVE counter at assertion time —
// CHECK F01 — so a forbidden DELETE or apply dispatched during the reload
// fails the row; the answer is read only by "What a cancel answer proves").
// Real transport over a mocked fetch; the per-tab record is seeded in session
// storage under the spec's key.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'

const RECORD_KEY = 'omnipus.exportImport.import'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: Record<string, unknown>) => ({ ...opts, useSearch: () => mockSearch }),
    useNavigate: () => navigateSpy,
  }
})

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: mockAgents,
  fetchWorkspaces: mockWorkspaces,
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function noContent(): Response {
  // 204 is bodyless by definition — a Response with a body is a fixture
  // exception, not a 204 answer (CHECK F05).
  return new Response(null, { status: 204 })
}

interface Row {
  cancel?: () => Response // the DELETE /packages/import/S answer
  cancelThrows?: () => never
}

// LIVE counters (CHECK F01): the fetch double mutates this object; assertions
// read it AFTER settlement, so any dispatch that happens at any point in the
// row — including during render — is observed.
interface Counters {
  cancel: number
  apply: number
  other: number
}

function installFetch(row: Row): { counts: Counters } {
  const counts: Counters = { cancel: 0, apply: 0, other: 0 }
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const u = String(url)
    const method = String((init as RequestInit | undefined)?.method ?? 'GET').toUpperCase()
    if (u.includes('/packages/')) {
      if (u.endsWith('/api/v1/packages/import/S') && method === 'DELETE') {
        counts.cancel += 1
        if (row.cancelThrows) row.cancelThrows()
        return row.cancel ? row.cancel() : noContent()
      }
      if (u.includes('/apply')) {
        counts.apply += 1
        throw new Error('an apply request must never be sent on a reload')
      }
      counts.other += 1
      throw new Error(`unexpected package call in test: ${method} ${u}`)
    }
    throw new Error(`unexpected fetch in test: ${u}`)
  })
  return { counts }
}

function seedRecord(record: Record<string, unknown> | null) {
  if (record === null) sessionStorage.removeItem(RECORD_KEY)
  else sessionStorage.setItem(RECORD_KEY, JSON.stringify(record))
}

function readRecord(): Record<string, unknown> | null {
  const raw = sessionStorage.getItem(RECORD_KEY)
  return raw ? (JSON.parse(raw) as Record<string, unknown>) : null
}

const previewedRecord = (workspace = false) => ({
  staging_id: 'S',
  phase: 'previewed',
  ...(workspace ? { workspace_id: 'ws-marketing', workspace_name: 'Marketing' } : {}),
  written_at: new Date().toISOString(),
})

const applySentRecord = {
  staging_id: 'S',
  phase: 'apply_sent',
  workspace_id: 'ws-marketing',
  workspace_name: 'Marketing',
  written_at: new Date().toISOString(),
}

const doneRecord = {
  staging_id: 'S',
  phase: 'done',
  workspace_id: 'ws-marketing',
  workspace_name: 'Marketing',
  written_at: new Date().toISOString(),
  result: { name: 'Writer (imported)', new_id: 'a-new', workspace_name: 'Marketing', activation: 'active' },
}

interface ReloadRow {
  label: string
  search: Record<string, string | undefined>
  record: Record<string, unknown> | null
  cancel: Row
  cancelCount: number // the EXACT expected count: 0 or 1 (CHECK F01)
  assert: () => Promise<void> | void
}

const RELOAD_NOTICE = 'The page was reloaded. Choose the file again.'
const LATER_UPLOAD = 'The upload was cancelled. Omnipus will try to remove its temporary files at a later upload.'
const UNCERTAIN =
  'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.'
const OUTCOME_UNKNOWN =
  'The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again.'

function renderAt(search: Record<string, string | undefined>) {
  mockSearch = search
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
}

function settle(ms = 30): Promise<void> {
  return act(async () => {
    await new Promise((r) => setTimeout(r, ms))
  })
}

beforeEach(() => {
  mockSearch = {}
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([])
  mockWorkspaces.mockResolvedValue([])
  rememberIdentityMode('local')
  useUiStore.setState({ addToast: vi.fn() as unknown as () => void })
  document.cookie = 'csrf=reload-table-test; path=/'
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
  sessionStorage.clear()
})

describe('Reload mid-import (US-5, D3 rows 9–10c)', () => {
  const rows: ReloadRow[] = [
    {
      label: 'preview + previewed + S ready: one cancel 204, File with the reload notice, record cleared',
      search: { wizard: 'import', step: 'preview', staging_id: 'S' },
      record: previewedRecord(),
      cancel: { cancel: () => noContent() },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(RELOAD_NOTICE)).toBeInTheDocument()
        expect(screen.getByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
        expect(readRecord()).toBeNull()
      },
    },
    {
      label: 'confirm + previewed + S ready: same as the preview row',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: previewedRecord(),
      cancel: { cancel: () => noContent() },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(RELOAD_NOTICE)).toBeInTheDocument()
        expect(readRecord()).toBeNull()
      },
    },
    {
      label: 'confirm + apply_sent + S applying: no cancel, Outcome-unknown with Open Team',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: applySentRecord,
      cancel: { cancel: () => noContent() },
      cancelCount: 0,
      assert: async () => {
        expect(await screen.findByText(OUTCOME_UNKNOWN)).toBeInTheDocument()
        expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
        expect(readRecord()?.phase).toBe('apply_sent')
      },
    },
    {
      label: 'confirm + apply_sent + S done (answer lost): no cancel, Outcome-unknown',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: applySentRecord,
      cancel: { cancel: () => jsonResponse(404, { code: 'staging_not_found' }) },
      cancelCount: 0,
      assert: async () => {
        expect(await screen.findByText(OUTCOME_UNKNOWN)).toBeInTheDocument()
      },
    },
    {
      label: 'confirm + no record + S ready: one cancel 204, File with the reload notice',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => noContent() },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(RELOAD_NOTICE)).toBeInTheDocument()
      },
    },
    {
      label: 'confirm + no record + folder removal fails: cancel 500, File with the reload notice and the later-upload text',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => jsonResponse(500, { code: 'staging_cleanup_failed' }) },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(RELOAD_NOTICE)).toBeInTheDocument()
        expect(screen.getByText(LATER_UPLOAD)).toBeInTheDocument()
        expect(readRecord()).toBeNull()
      },
    },
    {
      label: 'confirm + no record + S applying: cancel 409, Uncertain without a workspace link',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => jsonResponse(409, { code: 'staging_busy' }) },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        expect(screen.getByText('check the Team of the workspace you chose')).toBeInTheDocument()
        expect(screen.queryByRole('button', { name: 'Open Team' })).toBeNull()
        expect(screen.getByRole('button', { name: 'Start over' })).toBeInTheDocument()
      },
    },
    {
      label: 'confirm + no record + S done (another tab): cancel 404, Uncertain without a link',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => jsonResponse(404, { code: 'staging_not_found' }) },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        expect(screen.queryByRole('button', { name: 'Open Team' })).toBeNull()
      },
    },
    {
      label: 'confirm + previewed + S done (another tab imported it): cancel 404, Uncertain WITH Open Team',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: previewedRecord(true),
      cancel: { cancel: () => jsonResponse(404, { code: 'staging_not_found' }) },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
      },
    },
    {
      label: 'cancel answer lost: Uncertain and no second cancel',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: previewedRecord(true),
      cancel: { cancelThrows: () => { throw new TypeError('connection cut') } },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        await settle()
      },
    },
    {
      label: 'result + done record: the stored summary under Import finished, no package request',
      search: { wizard: 'import', step: 'result', staging_id: 'S' },
      record: doneRecord,
      cancel: { cancel: () => noContent() },
      cancelCount: 0,
      assert: async () => {
        expect(await screen.findByText('Import finished')).toBeInTheDocument()
        expect(screen.getByText('Writer (imported) was added to Marketing.')).toBeInTheDocument()
        expect(readRecord()).not.toBeNull()
      },
    },
    {
      label: 'result + no record: landing, no completion claim',
      search: { wizard: 'import', step: 'result', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => noContent() },
      cancelCount: 0,
      assert: async () => {
        expect(await screen.findByText('Save one of your agents as a bundle file.')).toBeInTheDocument()
        expect(screen.queryByText('Import finished')).toBeNull()
      },
    },
  ]

  for (const row of rows) {
    it(row.label, async () => {
      const { counts } = installFetch(row.cancel)
      seedRecord(row.record)
      renderAt(row.search)
      await row.assert()
      await settle()
      // Live observations, read after the row settled (CHECK F01).
      expect(counts.cancel, `exactly ${row.cancelCount} cancel(s) for this reload row`).toBe(row.cancelCount)
      expect(counts.apply, 'never an apply request').toBe(0)
      expect(counts.other, 'no other package request').toBe(0)
      if (row.search.step === 'result') {
        expect(counts.cancel + counts.apply + counts.other, 'a result reload sends no package request').toBe(0)
      }
    })
  }
})

describe('Leaving the wizard cleans up silently (US-7, FR-026)', () => {
  it('phase previewed: unmounting (navigation away) sends exactly one silent cancel, no message', async () => {
    const { counts } = installFetch({ cancel: () => noContent() })
    seedRecord(previewedRecord(true))
    const { unmount } = renderAt({ wizard: 'import', step: 'confirm', staging_id: 'S' })
    await screen.findByRole('button', { name: 'Import' })
    act(() => {
      unmount()
    })
    await settle()
    expect(counts.cancel, 'one silent best-effort cancel, observed live').toBe(1)
    expect(counts.apply).toBe(0)
    expect(document.body.textContent).not.toContain(UNCERTAIN)
    expect(document.body.textContent).not.toContain(LATER_UPLOAD)
  })

  it('phase apply_sent: no cancel is sent when leaving (live counter, CHECK F01)', async () => {
    const { counts } = installFetch({})
    seedRecord(applySentRecord)
    const { unmount } = renderAt({ wizard: 'import', step: 'confirm', staging_id: 'S' })
    await settle()
    act(() => {
      unmount()
    })
    await settle()
    expect(counts.cancel, 'no cancel for an upload whose apply may have run').toBe(0)
    expect(counts.apply).toBe(0)
  })
})
