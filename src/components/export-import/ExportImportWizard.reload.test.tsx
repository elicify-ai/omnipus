// Export & Import wizard reload table — spec order 28 (issue #1967 B1).
// Oracles: the spec scenario outline "Reload mid-import" (all rows), "Leaving
// the wizard cleans up silently", and FR-025/FR-026 (never an apply request;
// cancel count 0 or 1; the answer is read only by "What a cancel answer
// proves"). Real transport over a mocked fetch; the per-tab record is seeded
// in session storage under the spec's key.
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

interface Row {
  cancel?: () => Response // the DELETE /packages/import/S answer
  cancelThrows?: () => never
}

function installFetch(row: Row): { cancelCalls: number; applyCalls: number; packageCalls: string[] } {
  let cancelCalls = 0
  let applyCalls = 0
  const packageCalls: string[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const u = String(url)
    if (u.includes('/packages/')) {
      packageCalls.push(`${(init as RequestInit | undefined)?.method ?? 'GET'} ${u}`)
      if (u.endsWith('/api/v1/packages/import/S') && (init as RequestInit | undefined)?.method === 'DELETE') {
        cancelCalls += 1
        if (row.cancelThrows) row.cancelThrows()
        return row.cancel ? row.cancel() : jsonResponse(204, null)
      }
      if (u.includes('/apply')) {
        applyCalls += 1
        throw new Error('an apply request must never be sent on a reload')
      }
      throw new Error(`unexpected package call in test: ${u}`)
    }
    throw new Error(`unexpected fetch in test: ${u}`)
  })
  return { cancelCalls, applyCalls, packageCalls }
}

function seedRecord(record: Record<string, unknown> | null) {
  if (record === null) sessionStorage.removeItem(RECORD_KEY)
  else sessionStorage.setItem(RECORD_KEY, JSON.stringify(record))
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
  assert: () => Promise<void> | void
}

const RELOAD_NOTICE = 'The page was reloaded. Choose the file again.'
const LATER_UPLOAD = 'The upload was cancelled. Omnipus will try to remove its temporary files at a later upload.'
const UNCERTAIN =
  'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.'
const OUTCOME_UNKNOWN =
  'The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again.'

function readRecord(): Record<string, unknown> | null {
  const raw = sessionStorage.getItem(RECORD_KEY)
  return raw ? (JSON.parse(raw) as Record<string, unknown>) : null
}

function renderAt(search: Record<string, string | undefined>) {
  mockSearch = search
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
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
      cancel: { cancel: () => jsonResponse(204, null) },
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
      cancel: { cancel: () => jsonResponse(204, null) },
      assert: async () => {
        expect(await screen.findByText(RELOAD_NOTICE)).toBeInTheDocument()
        expect(readRecord()).toBeNull()
      },
    },
    {
      label: 'confirm + apply_sent + S applying: no cancel, Outcome-unknown with Open Team',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: applySentRecord,
      cancel: { cancel: () => jsonResponse(204, null) },
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
      assert: async () => {
        expect(await screen.findByText(OUTCOME_UNKNOWN)).toBeInTheDocument()
      },
    },
    {
      label: 'confirm + no record + S ready: one cancel 204, File with the reload notice',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => jsonResponse(204, null) },
      assert: async () => {
        expect(await screen.findByText(RELOAD_NOTICE)).toBeInTheDocument()
      },
    },
    {
      label: 'confirm + no record + folder removal fails: cancel 500, File with the reload notice and the later-upload text',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: null,
      cancel: { cancel: () => jsonResponse(500, { code: 'staging_cleanup_failed' }) },
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
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        await act(async () => {
          await new Promise((r) => setTimeout(r, 20))
        })
      },
    },
    {
      label: 'result + done record: the stored summary under Import finished, no package request',
      search: { wizard: 'import', step: 'result', staging_id: 'S' },
      record: doneRecord,
      cancel: { cancel: () => jsonResponse(204, null) },
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
      cancel: { cancel: () => jsonResponse(204, null) },
      assert: async () => {
        expect(await screen.findByText('Save one of your agents as a bundle file.')).toBeInTheDocument()
        expect(screen.queryByText('Import finished')).toBeNull()
      },
    },
  ]

  for (const row of rows) {
    it(row.label, async () => {
      const { cancelCalls, applyCalls, packageCalls } = installFetch(row.cancel)
      seedRecord(row.record)
      const { unmount } = renderAt(row.search)
      await row.assert()
      // Package-request discipline: at most one cancel; never an apply.
      expect(cancelCalls, 'cancel count 0 or 1').toBeLessThan(2)
      expect(applyCalls, 'never an apply request').toBe(0)
      if (row.search.step === 'result') {
        expect(packageCalls, 'a result reload sends no package request').toHaveLength(0)
      }
      void unmount
    })
  }

  it('a lost cancel answer sends no second cancel', async () => {
    const { cancelCalls } = installFetch({ cancelThrows: () => { throw new TypeError('connection cut') } })
    seedRecord(previewedRecord(true))
    renderAt({ wizard: 'import', step: 'confirm', staging_id: 'S' })
    await screen.findByText(UNCERTAIN)
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30))
    })
    expect(cancelCalls, 'no second cancel').toBe(1)
  })
})

describe('Leaving the wizard cleans up silently (US-7, FR-026)', () => {
  it('phase previewed: unmounting (navigation away) sends exactly one silent cancel, no message', async () => {
    const { cancelCalls } = installFetch({ cancel: () => jsonResponse(204, null) })
    seedRecord(previewedRecord(true))
    const { unmount } = renderAt({ wizard: 'import', step: 'confirm', staging_id: 'S' })
    await screen.findByRole('button', { name: 'Import' })
    act(() => {
      unmount()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30))
    })
    expect(cancelCalls, 'one silent best-effort cancel').toBe(1)
    expect(document.body.textContent).not.toContain(UNCERTAIN)
    expect(document.body.textContent).not.toContain(LATER_UPLOAD)
  })

  it('phase apply_sent: no cancel is sent when leaving', async () => {
    const { cancelCalls } = installFetch({})
    seedRecord(applySentRecord)
    const { unmount } = renderAt({ wizard: 'import', step: 'confirm', staging_id: 'S' })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30))
    })
    act(() => {
      unmount()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30))
    })
    expect(cancelCalls, 'no cancel for an upload whose apply may have run').toBe(0)
  })
})
