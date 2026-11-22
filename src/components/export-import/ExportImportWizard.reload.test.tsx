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

// GREEN CHECK P07 (spec line 473 "Workspace choice | select", FR-028): the
// Confirm's workspace select is the CATALOGUED select; the same shim as
// ImportConfirmStep.test.tsx (the repo's Radix-select test precedent,
// ConnectorsScreen.test.tsx) keeps its accessibility contract drivable in
// jsdom — combobox trigger, option choice, SelectValue showing the label.
vi.mock('@/components/ui/select', () => {
  const labels = new Map<string, string>()
  type SelectProps = { value?: string; onValueChange?: (v: string) => void; children?: React.ReactNode }
  type ItemProps = { value: string; children?: React.ReactNode }
  const SelectCtx = React.createContext<{ value?: string; onValueChange?: (v: string) => void }>({})

  const Select = ({ value, onValueChange, children }: SelectProps) =>
    React.createElement(SelectCtx.Provider, { value: { value, onValueChange } }, children)
  const SelectTrigger = ({ children, ...rest }: { children?: React.ReactNode } & Record<string, unknown>) =>
    React.createElement('button', { type: 'button', role: 'combobox', ...rest }, children)
  const SelectValue = ({ placeholder }: { placeholder?: string }) => {
    const { value } = React.useContext(SelectCtx)
    return React.createElement('span', {}, (value && labels.get(value)) || placeholder)
  }
  const SelectContent = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'listbox' }, children)
  const SelectItem = ({ value, children }: ItemProps) => {
    const { onValueChange } = React.useContext(SelectCtx)
    React.useEffect(() => {
      labels.set(value, typeof children === 'string' ? children : value)
      return () => {
        labels.delete(value)
      }
    }, [value, children])
    return React.createElement(
      'div',
      { role: 'option', 'data-value': value, onClick: () => onValueChange?.(value) },
      children,
    )
  }
  const SelectLabel = ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children)
  const SelectGroup = ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children)
  const SelectSeparator = () => React.createElement('hr', {})

  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectLabel, SelectGroup, SelectSeparator }
})

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
      // GREEN CHECK P01: a proxy's readable 200 is an unexpected SUCCESSFUL
      // answer — it proves nothing and must end in Uncertain with the record
      // kept, not in the confirmed reload notice.
      label: 'reload + a 200 readable JSON cancel answer: Uncertain, record kept, one cancel',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: previewedRecord(true),
      cancel: { cancel: () => jsonResponse(200, { done: true }) },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        expect(readRecord()?.staging_id, 'an unproven answer keeps the record').toBe('S')
        await settle()
      },
    },
    {
      // GREEN CHECK P01: a 205 reset-content resolves in the transport today
      // but proves nothing either — the same Uncertain outcome.
      label: 'reload + a 205 cancel answer: Uncertain, record kept, one cancel',
      search: { wizard: 'import', step: 'confirm', staging_id: 'S' },
      record: previewedRecord(true),
      cancel: { cancel: () => new Response(null, { status: 205 }) },
      cancelCount: 1,
      assert: async () => {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        expect(readRecord()?.staging_id, 'an unproven answer keeps the record').toBe('S')
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
    // Choose the workspace on the live Confirm, the way a person would —
    // through the catalogued select's option (GREEN CHECK P07, spec line 473).
    await screen.findByRole('combobox')
    await act(async () => {
      fireEvent.click(screen.getByRole('option', { name: 'General' }))
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

// ── GREEN CHECK P01: what a cancel answer proves, for the explicit Cancel ──
// The same evidence rule the reload table pins, driven from a LIVE Preview
// through a fresh upload: only a 204 — or the coded 500
// staging_cleanup_failed, which names the cleanup failure — confirms; every
// unexpected successful answer (a proxy's readable 200, an HTML/truncated 200
// without Content-Length, a 202, a 205) proves nothing and must end on the
// Uncertain screen with the record kept. The live counter holds each row to
// exactly one cancel request, never a second, and never an apply.

// Drives a fresh wizard to a live Preview through the real upload path (the
// explicit Cancel lives on Preview and on Confirm; Preview needs no workspace
// choice, so the drive stops there).
async function driveToPreview(fetchRow: Row): Promise<{ counts: Counters }> {
  const { counts } = installFetch(fetchRow)
  renderAt({ wizard: 'import' })
  // File step: the input's availability is asserted BEFORE the change event,
  // so this row can never fail as "unable to fire a change event".
  const input = document.querySelector('input[type="file"]') as HTMLInputElement | null
  expect(input, 'the File step renders a bundle-file input before the change event fires').not.toBeNull()
  const file = new File([new Uint8Array(64)], 'bundle.omnpkg', { type: 'application/octet-stream' })
  await act(async () => {
    fireEvent.change(input!, { target: { files: [file] } })
  })
  expect(await screen.findByText('Will be added')).toBeInTheDocument()
  return { counts }
}

describe('What a cancel answer proves — explicit Cancel from a live Preview (US-5, FR-025/026, GREEN CHECK P01)', () => {
  const rows: Array<{ label: string; answer: () => Response; outcome: 'landing' | 'later' | 'uncertain' }> = [
    { label: 'a 204 confirms: the landing, the record cleared', answer: () => noContent(), outcome: 'landing' },
    {
      label: 'a coded 500 staging_cleanup_failed confirms with the later-upload notice',
      answer: () => jsonResponse(500, { code: 'staging_cleanup_failed' }),
      outcome: 'later',
    },
    {
      label: 'a 200 with a readable JSON body proves nothing: Uncertain, record kept',
      answer: () => jsonResponse(200, { done: true }),
      outcome: 'uncertain',
    },
    {
      label: 'a 200 HTML body without Content-Length proves nothing: Uncertain, record kept',
      answer: () => new Response('<html>gateway page</html>', { status: 200, headers: { 'content-type': 'text/html' } }),
      outcome: 'uncertain',
    },
    { label: 'a 202 proves nothing: Uncertain, record kept', answer: () => new Response(null, { status: 202 }), outcome: 'uncertain' },
    { label: 'a 205 proves nothing: Uncertain, record kept', answer: () => new Response(null, { status: 205 }), outcome: 'uncertain' },
  ]

  for (const row of rows) {
    it(`${row.label}: exactly one cancel request`, async () => {
      const { counts } = await driveToPreview({
        upload: () => jsonResponse(200, validPreviewBody()),
        cancel: row.answer,
      })
      await act(async () => {
        fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
      })
      if (row.outcome === 'landing') {
        expect(await screen.findByRole('button', { name: 'Start import' })).toBeInTheDocument()
        expect(readRecord(), 'the confirmed cancel cleared the record').toBeNull()
      } else if (row.outcome === 'later') {
        expect(await screen.findByRole('button', { name: 'Start import' })).toBeInTheDocument()
        expect(screen.getByText(LATER_UPLOAD)).toBeInTheDocument()
        expect(readRecord(), 'the coded cleanup failure still ends this upload (never applicable)').toBeNull()
      } else {
        expect(await screen.findByText(UNCERTAIN)).toBeInTheDocument()
        expect(readRecord()?.staging_id, 'an unproven answer keeps the record').toBe('S')
      }
      await settle()
      expect(counts.cancel, 'exactly one cancel request, observed live').toBe(1)
      expect(counts.apply, 'a cancel never applies').toBe(0)
    })
  }
})

// ── GREEN CHECK P02: history navigation after a lost apply answer ───────────
// ONE wizard instance drives the fresh flow to a dispatched apply whose
// answer never arrives (the wizard's own record is apply_sent). In-instance
// history Back to the preview address and Forward to the confirm address must
// show the Outcome-unknown state both times — never the held preview with a
// Cancel that could send the forbidden DELETE, and never a Confirm that could
// offer a second apply for the same upload (FR-024a).
describe('Browser Back after a lost apply answer (US-5, FR-024a, GREEN CHECK P02)', () => {
  it('Back to Preview and Forward to Confirm: Outcome-unknown, zero DELETE, exactly one apply', async () => {
    const { counts, applyDispatched } = installFetch({
      upload: () => jsonResponse(200, validPreviewBody()),
      // The apply answer is genuinely lost: the request never settles.
      apply: () => new Promise<Response>(() => {}),
    })
    // The Confirm screen needs an active default workspace to pre-choose.
    mockWorkspaces.mockResolvedValue([{ id: 'ws-general', name: 'General', is_default: true, pinned: false }])
    // This row holds its own QueryClient so one wizard instance survives
    // rerenders (renderAt builds a fresh client per call).
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: mutationRetryDelay } },
    })
    const Component = (Route as unknown as { component: React.ComponentType }).component
    const wizard = (
      <QueryClientProvider client={client}>
        <Component />
      </QueryClientProvider>
    )
    mockSearch = { wizard: 'import' }
    const view = render(wizard)
    const input = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(input, 'the File step renders a bundle-file input').not.toBeNull()
    const file = new File([new Uint8Array(64)], 'bundle.omnpkg', { type: 'application/octet-stream' })
    await act(async () => {
      fireEvent.change(input!, { target: { files: [file] } })
    })
    expect(await screen.findByText('Will be added')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
    expect(await screen.findByRole('button', { name: 'Import' })).toBeInTheDocument()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await applyDispatched
    await settle()
    expect(readRecord()?.phase, 'the record is apply_sent while the answer is lost').toBe('apply_sent')
    expect(counts.apply, 'the apply was dispatched exactly once').toBe(1)

    // History BACK in the same instance: the address says Preview again.
    mockSearch = { wizard: 'import', step: 'preview', staging_id: 'S' }
    await act(async () => {
      view.rerender(wizard)
    })
    expect(
      await screen.findByText(OUTCOME_UNKNOWN),
      'an upload whose outcome is unknown shows Outcome-unknown — not the held preview with its Cancel',
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open Team' }), 'the record names the workspace, so the link is offered').toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancel' }), 'no Cancel action for an unknown outcome').toBeNull()
    expect(screen.queryByRole('button', { name: 'Import' }), 'no Import action for an unknown outcome').toBeNull()
    expect(counts.cancel, 'zero DELETE for an unknown outcome').toBe(0)
    expect(counts.apply, 'no second apply').toBe(1)

    // History FORWARD: the confirm address must stay Outcome-unknown too.
    mockSearch = { wizard: 'import', step: 'confirm', staging_id: 'S' }
    await act(async () => {
      view.rerender(wizard)
    })
    expect(await screen.findByText(OUTCOME_UNKNOWN)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Import' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(counts.apply, 'still exactly one apply after Back and Forward').toBe(1)
    expect(counts.cancel, 'still zero DELETE after Back and Forward').toBe(0)
  })
})
