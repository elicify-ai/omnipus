// Export & Import wizard reload table — spec order 28 (issue #1967 B1).
// Oracles: the spec scenario outline "Reload mid-import" (all rows), "Leaving
// the wizard cleans up silently", and FR-025/FR-026 (never an apply request;
// cancel count EXACTLY 0 or 1, read from a LIVE counter at assertion time —
// CHECK F01 — so a forbidden DELETE or apply dispatched during the reload
// fails the row; the answer is read only by "What a cancel answer proves").
// Real transport over a mocked fetch; the per-tab record is seeded in session
// storage under the spec's key.
//
// This file also owns the two ROUTE-LEVEL fresh-flow tables (CHECK F06/N03
// round 2): leaving a live Confirm reached through the real
// upload→Preview→Continue path, and the expired-upload handoff back to the
// File step — the only level where the File step's own notice and action are
// observable. The wizard rides the production QueryClient mutation defaults
// (CHECK F04 round 2).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { shouldRetryMutation, mutationRetryDelay } from '@/lib/queryClient'
import { PackageImportPreviewSchema, type PackageImportPreview } from '@/lib/api/packages'

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
  // Configured only by the fresh-flow rows (leave, expired handoff): the
  // POST /packages/import upload answer and the POST …/apply answer. A reload
  // row never configures them, so its contract stays "must never be sent".
  upload?: () => Response | Promise<Response>
  apply?: () => Response | Promise<Response>
}

// LIVE counters (CHECK F01): the fetch double mutates this object; assertions
// read it AFTER settlement, so any dispatch that happens at any point in the
// row — including during render — is observed.
interface Counters {
  cancel: number
  apply: number
  other: number
  upload: number
}

function installFetch(row: Row): { counts: Counters; applyDispatched: Promise<void> } {
  const counts: Counters = { cancel: 0, apply: 0, other: 0, upload: 0 }
  let resolveApplyDispatch: () => void = () => {}
  const applyDispatched = new Promise<void>((resolve) => {
    resolveApplyDispatch = resolve
  })
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const u = String(url)
    const method = String((init as RequestInit | undefined)?.method ?? 'GET').toUpperCase()
    if (u.endsWith('/api/v1/state') && method === 'GET') {
      return jsonResponse(200, { onboarding_complete: true, dev_mode_bypass: false })
    }
    if (u.includes('/packages/')) {
      if (u.endsWith('/api/v1/packages/import') && method === 'POST') {
        counts.upload += 1
        if (!row.upload) throw new Error('an upload request must never be sent on a reload row')
        return row.upload()
      }
      if (u.endsWith('/api/v1/packages/import/S') && method === 'DELETE') {
        counts.cancel += 1
        if (row.cancelThrows) row.cancelThrows()
        return row.cancel ? row.cancel() : noContent()
      }
      if (u.includes('/apply')) {
        counts.apply += 1
        resolveApplyDispatch()
        if (!row.apply) throw new Error('an apply request must never be sent on a reload')
        return row.apply()
      }
      counts.other += 1
      throw new Error(`unexpected package call in test: ${method} ${u}`)
    }
    throw new Error(`unexpected fetch in test: ${u}`)
  })
  return { counts, applyDispatched }
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

// A schema-valid preview body for the upload answer of the fresh-flow rows
// (validated against the unchanged generated contract, CHECK F02); the expiry
// derives from the real clock (CHECK F07).
function validPreviewBody(): PackageImportPreview {
  const preview: PackageImportPreview = {
    staging_id: 'S',
    expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
    manifest: {
      format: 'omnpkg',
      format_version: 1,
      scope: 'custom',
      elements: [{ type: 'agent', schema: 1, source_id: 'a-writer', name: 'Writer', files: ['agent.json', 'SOUL.md'] }],
      counts: {},
      files: {},
      source_build: 'v0.1.1',
      created_at: new Date(Date.now() - 60_000).toISOString(),
      includes_sessions: false,
      needs_secrets: [],
    },
    will_add: [{ type: 'agent', source_id: 'a-writer', source_name: 'Writer', proposed_name: 'Writer (imported)', renamed: true }],
    skipped_elements: [],
    warnings: [],
    needs_secrets: [],
  }
  PackageImportPreviewSchema.parse(preview)
  return preview
}

// The wizard mounts under the PRODUCTION mutation defaults (CHECK F04 round
// 2): the fresh-flow rows below observe the real mutation configuration of
// the upload, cancel and apply call sites, so a GREEN wizard that omits
// retry:false fails the never-resend oracles (the api-level control proves
// those defaults resend four times).
function renderAt(search: Record<string, string | undefined>) {
  mockSearch = search
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: mutationRetryDelay } },
  })
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(
    <QueryClientProvider client={client}>
      <Component />
    </QueryClientProvider>,
  )
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
      expect(counts.upload, 'a reload never uploads').toBe(0)
      expect(counts.other, 'no other package request').toBe(0)
      if (row.search.step === 'result') {
        expect(counts.cancel + counts.apply + counts.other, 'a result reload sends no package request').toBe(0)
      }
    })
  }
})

// ── Fresh-flow rows (CHECK F06 round 2): a LEAVE is a fresh wizard driven to
//    a live Confirm through the real in-wizard path — landing → file choice →
//    upload answer → Preview → Continue → Confirm. Round 1 mounted the route
//    at a confirm address with a seeded record instead, which is exactly the
//    reload table's state (a conforming route cancels there), not a leave.
//    The record under test is the wizard's own write, and the same drive
//    gives the expired-upload handoff its route-level proof (CHECK N03).

// Drives a fresh wizard to a live Confirm through the real transitions and
// returns the live counters plus the promise resolved when the apply leaves.
async function driveToConfirm(fetchRow: Row): Promise<{
  counts: Counters
  applyDispatched: Promise<void>
  view: ReturnType<typeof render>
}> {
  const { counts, applyDispatched } = installFetch(fetchRow)
  // The Confirm screen needs an active default workspace to pre-choose.
  mockWorkspaces.mockResolvedValue([{ id: 'ws-general', name: 'General', is_default: true, pinned: false }])
  const view = renderAt({ wizard: 'import' })
  // File step: the input's availability is asserted BEFORE the change event,
  // so this row can never fail as "unable to fire a change event" — its only
  // sanctioned failure is the missing surface itself.
  const input = document.querySelector('input[type="file"]') as HTMLInputElement | null
  expect(input, 'the File step renders a bundle-file input before the change event fires').not.toBeNull()
  const file = new File([new Uint8Array(64)], 'bundle.omnpkg', { type: 'application/octet-stream' })
  await act(async () => {
    fireEvent.change(input!, { target: { files: [file] } })
  })
  // Preview: the wizard wrote its own previewed record on the way.
  expect(await screen.findByText('Will be added')).toBeInTheDocument()
  expect(readRecord()?.phase, 'the wizard wrote the previewed record at Preview time').toBe('previewed')
  expect(readRecord()?.staging_id).toBe('S')
  // Continue → the live Confirm.
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByRole('button', { name: 'Import' })).toBeInTheDocument()
  return { counts, applyDispatched, view }
}

describe('Leaving the wizard cleans up silently (US-7, FR-026)', () => {
  it('leaving a live Confirm (phase previewed): unmounting sends exactly one silent cancel, no message', async () => {
    const { counts, view } = await driveToConfirm({
      upload: () => jsonResponse(200, validPreviewBody()),
      cancel: () => noContent(),
    })
    // Choose the workspace on the live Confirm, the way a person would.
    const select = await screen.findByRole('combobox')
    await act(async () => {
      fireEvent.change(select, { target: { value: 'ws-general' } })
    })
    expect(readRecord()?.phase, 'the record stays previewed until the apply leaves').toBe('previewed')
    act(() => {
      view.unmount()
    })
    await settle()
    expect(counts.cancel, 'one silent best-effort cancel from a live Confirm, observed live').toBe(1)
    expect(counts.apply, 'no apply was sent by leaving').toBe(0)
    expect(counts.other, 'no other package request').toBe(0)
    expect(document.body.textContent).not.toContain(UNCERTAIN)
    expect(document.body.textContent).not.toContain(LATER_UPLOAD)
  })

  it('leaving after Import (phase apply_sent): no cancel is sent when leaving (live counter, CHECK F01)', async () => {
    // The apply answer never arrives: the request is genuinely in flight when
    // the person navigates away — the spec's lost-answer leave state.
    const { counts, applyDispatched, view } = await driveToConfirm({
      upload: () => jsonResponse(200, validPreviewBody()),
      apply: () => new Promise<Response>(() => {}),
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await applyDispatched
    expect(readRecord()?.phase, 'phase apply_sent is written before the request leaves').toBe('apply_sent')
    await settle()
    act(() => {
      view.unmount()
    })
    await settle()
    expect(counts.cancel, 'no cancel for an upload whose apply may have run').toBe(0)
    expect(counts.apply, 'the in-flight apply was dispatched exactly once').toBe(1)
  })
})

describe('Expired upload at confirm returns to File (US-5.4/D4-5 — route-level handoff, CHECK N03)', () => {
  it('apply 404 staging_not_found: the File step shows the expired-or-used notice and the bundle-file action; the record is cleared', async () => {
    const { counts } = await driveToConfirm({
      upload: () => jsonResponse(200, validPreviewBody()),
      apply: () => jsonResponse(404, { code: 'staging_not_found' }),
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    // The handoff proof at the route level: the FILE step (not only a
    // navigation) explains itself, with the exact spec notice and the file
    // action — the oracles the component-level boundary cannot observe.
    expect(await screen.findByText('This upload expired or was already used. Choose the file again.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
    expect(readRecord(), 'the record is cleared when the staging id is gone').toBeNull()
    expect(counts.apply, 'exactly one apply was sent').toBe(1)
    await settle()
    expect(counts.cancel, 'no cancel rides on an expired upload').toBe(0)
  })

  it('apply 500 import_rolled_back: Start over lands on File with the bundle-file action; no second apply', async () => {
    const { counts } = await driveToConfirm({
      upload: () => jsonResponse(200, validPreviewBody()),
      apply: () => jsonResponse(500, { code: 'import_rolled_back' }),
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('The import failed and was undone. Nothing was added.')).toBeInTheDocument()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Start over' }))
    })
    expect(await screen.findByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
    expect(counts.apply, 'no second apply').toBe(1)
    expect(readRecord(), 'a rolled-back import clears the record').toBeNull()
  })
})
