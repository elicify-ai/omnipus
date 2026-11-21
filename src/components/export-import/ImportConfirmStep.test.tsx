// Import Confirm step — spec order 26 (issue #1967 B1).
// Oracles: spec scenarios "Confirm states the mode and pre-chooses the
// default workspace", "No default workspace", "No active workspace",
// "Another import is running", "Try again only after a proven pre-write
// refusal", "Record after a known refusal", "Session refresh on apply is not
// replayed" (real request() over a mocked fetch — the replay rows of 26 per
// spec), "Other apply failures", "Unknown apply outcome, never resent",
// "Expired upload at confirm"; the Import record transitions table.
//
// The packages module stays REAL: the wizard runs its calls through the
// production transport, and fetch is mocked per row (state + apply URLs).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

const RECORD_KEY = 'omnipus.exportImport.import'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, addToastSpy } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  addToastSpy: vi.fn(),
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

function readRecord(): Record<string, unknown> | null {
  const raw = sessionStorage.getItem(RECORD_KEY)
  return raw ? (JSON.parse(raw) as Record<string, unknown>) : null
}

function seedRecord(record: Record<string, unknown>) {
  sessionStorage.setItem(RECORD_KEY, JSON.stringify(record))
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const workspaces = [
  { id: 'ws-general', name: 'General', is_default: true, pinned: false },
  { id: 'ws-marketing', name: 'Marketing', is_default: false, pinned: false },
]

const okResult = {
  mode: 'new',
  created: [
    {
      type: 'agent',
      source_id: 'a-writer',
      source_name: 'Writer',
      name: 'Writer (imported)',
      new_id: 'a-new',
      workspace_id: 'ws-marketing',
      activation: 'active',
    },
  ],
  warnings: [],
  needs_secrets: [],
}

interface Row {
  answer: (url: string, init: RequestInit | undefined) => Response
  onApply?: (init: RequestInit | undefined) => void
}

// Installs the fetch double for one row. The apply URL's answer comes from
// `row`; /api/v1/state always answers (the recovery read). The record value
// AT THE MOMENT the apply request leaves is captured.
function installFetch(row: Row): { applyCalls: Array<RequestInit | undefined>; stateCount: () => number; recordAtApply: Array<string | null> } {
  const applyCalls: Array<RequestInit | undefined> = []
  const recordAtApply: Array<string | null> = []
  let stateReads = 0
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const u = String(url)
    if (u.endsWith('/api/v1/state')) {
      stateReads += 1
      return jsonResponse(200, { onboarding_complete: true, dev_mode_bypass: false })
    }
    if (u.includes('/packages/import/S/apply')) {
      applyCalls.push(init)
      recordAtApply.push(sessionStorage.getItem(RECORD_KEY))
      row.onApply?.(init)
      return row.answer(u, init)
    }
    throw new Error(`unexpected fetch in test: ${u}`)
  })
  return { applyCalls, recordAtApply, stateCount: () => stateReads }
}

function openConfirm(record: Record<string, unknown> = {
  staging_id: 'S',
  phase: 'previewed',
  workspace_id: 'ws-general',
  workspace_name: 'General',
  written_at: new Date().toISOString(),
}) {
  seedRecord(record)
  mockSearch = { wizard: 'import', step: 'confirm', staging_id: 'S' }
  const Component = (Route as unknown as { component: React.ComponentType }).component
  render(<Component />)
}

const NO_WORKSPACE_RECORD = {
  staging_id: 'S',
  phase: 'previewed',
  written_at: new Date().toISOString(),
}

beforeEach(() => {
  mockSearch = {}
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([])
  mockWorkspaces.mockReset().mockResolvedValue(workspaces)
  rememberIdentityMode('local')
  useUiStore.setState({ addToast: addToastSpy as unknown as () => void })
  document.cookie = 'csrf=import-confirm-test; path=/'
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
  document.cookie = '__Host-csrf=; max-age=0; path=/'
  sessionStorage.clear()
})

describe('Import Confirm (US-4.3)', () => {
  it('states the mode and pre-chooses the default workspace', async () => {
    installFetch({ answer: () => jsonResponse(200, okResult) })
    openConfirm()
    expect(await screen.findByText('Added as new. Existing agents are never changed.')).toBeInTheDocument()
    const select = await screen.findByRole('combobox')
    expect((select as HTMLSelectElement).value).toBe('ws-general')
  })

  it('without a default workspace nothing is pre-chosen and Import is disabled until one is chosen', async () => {
    mockWorkspaces.mockResolvedValue([
      { id: 'ws-a', name: 'A', is_default: false, pinned: false },
      { id: 'ws-b', name: 'B', is_default: false, pinned: false },
    ])
    installFetch({ answer: () => jsonResponse(200, okResult) })
    openConfirm(NO_WORKSPACE_RECORD)
    const select = await screen.findByRole('combobox')
    expect((select as HTMLSelectElement).value).toBe('')
    expect(screen.getByRole('button', { name: 'Import' })).toBeDisabled()
    await act(async () => {
      fireEvent.change(select, { target: { value: 'ws-b' } })
    })
    expect(screen.getByRole('button', { name: 'Import' })).toBeEnabled()
  })

  it('no active workspace: "Create a workspace first." and no Import action', async () => {
    mockWorkspaces.mockResolvedValue([])
    installFetch({ answer: () => jsonResponse(200, okResult) })
    openConfirm(NO_WORKSPACE_RECORD)
    expect(await screen.findByText('Create a workspace first.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Import' })).toBeNull()
  })

  it('Import sends the exact {mode, workspace_id} body with the record at apply_sent before the request', async () => {
    const row: Row = {
      answer: () => jsonResponse(200, okResult),
      onApply: () => {
        /* record read already captured */
      },
    }
    const { applyCalls, recordAtApply } = installFetch(row)
    openConfirm({
      staging_id: 'S',
      phase: 'previewed',
      workspace_id: 'ws-general',
      workspace_name: 'General',
      written_at: new Date().toISOString(),
    })
    const select = await screen.findByRole('combobox')
    await act(async () => {
      fireEvent.change(select, { target: { value: 'ws-marketing' } })
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await screen.findByText('Writer (imported) was added to Marketing.')
    expect(applyCalls).toHaveLength(1)
    const body = JSON.parse(String(applyCalls[0]?.body))
    expect(body).toEqual({ mode: 'new', workspace_id: 'ws-marketing' })
    const atRequest = JSON.parse(recordAtApply[0] ?? 'null') as Record<string, unknown> | null
    expect(atRequest?.phase, 'phase apply_sent is written before the request leaves').toBe('apply_sent')
    expect(atRequest?.staging_id).toBe('S')
  })
})

describe('Session refresh on apply is not replayed (US-5, D4-11a/11b)', () => {
  const SESSION_TEXT = 'Your session needed refreshing. Nothing was imported. Select Import again.'

  it('a server CSRF 403: one apply, one recovery read, no toast, record previewed', async () => {
    const { applyCalls, stateCount } = installFetch({ answer: () => jsonResponse(403, { error: 'csrf token mismatch' }) })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await screen.findByText(SESSION_TEXT)
    expect(applyCalls).toHaveLength(1)
    expect(stateCount(), 'exactly one recovery read GET /api/v1/state').toBe(1)
    expect(addToastSpy).not.toHaveBeenCalled()
    expect(readRecord()?.phase).toBe('previewed')
  })

  it('a planted_cookie_cleared 403: one apply, one recovery read, no toast', async () => {
    const { applyCalls, stateCount } = installFetch({
      answer: () => jsonResponse(403, { code: 'planted_cookie_cleared', error: 'planted' }),
    })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await screen.findByText(SESSION_TEXT)
    expect(applyCalls).toHaveLength(1)
    expect(stateCount()).toBe(1)
    expect(addToastSpy).not.toHaveBeenCalled()
    expect(readRecord()?.phase).toBe('previewed')
  })

  it('client csrf_missing: ZERO apply requests, one recovery read; the next click sends the first', async () => {
    document.cookie = 'csrf=; max-age=0; path=/'
    const { applyCalls, stateCount } = installFetch({ answer: () => jsonResponse(200, okResult) })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await screen.findByText(SESSION_TEXT)
    expect(applyCalls, 'the real cookie gate refuses before any request').toHaveLength(0)
    expect(stateCount(), 'exactly one recovery read').toBe(1)
    expect(readRecord()?.phase).toBe('previewed')

    // The session is valid again; the person clicks Import once more.
    document.cookie = 'csrf=import-confirm-test; path=/'
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await screen.findByText('Writer (imported) was added to Marketing.')
    expect(applyCalls, 'this click sends the FIRST apply').toHaveLength(1)
  })
})

describe('Another import is running + Try again (US-5, D4-6)', () => {
  it('409 import_in_progress: message and Try again; the retry sends the same body once more', async () => {
    const { applyCalls, recordAtApply } = installFetch({ answer: () => jsonResponse(409, { code: 'import_in_progress' }) })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('Another import is running. Try again in a moment.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
    expect(readRecord()?.phase).toBe('previewed')
    expect(applyCalls).toHaveLength(1)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    })
    await screen.findByText('Another import is running. Try again in a moment.')
    expect(applyCalls, 'exactly one second apply with the same body').toHaveLength(2)
    expect(JSON.parse(String(applyCalls[1]?.body))).toEqual(JSON.parse(String(applyCalls[0]?.body)))
    expect(JSON.parse(recordAtApply[1] ?? '{}').phase).toBe('apply_sent')
  })
})

describe('Record after a known refusal (US-5, D4-5…11c)', () => {
  const rows: Array<{ label: string; answer: () => Response; text: string }> = [
    {
      label: '409 name_suffix_exhausted',
      answer: () => jsonResponse(409, { code: 'name_suffix_exhausted' }),
      text: 'Every "(imported N)" name is taken. Rename one of the existing agents, then try again.',
    },
    {
      label: '422 element_not_creatable (field model.primary)',
      answer: () =>
        jsonResponse(422, { code: 'element_not_creatable', field: 'model.primary', error: 'not creatable' }),
      text: 'The agent in this bundle breaks a rule for new agents: model.primary',
    },
    {
      label: '413 (no code)',
      answer: () => new Response(JSON.stringify({ error: 'request body too large' }), { status: 413 }),
      text: 'Omnipus refused the request as too large. Nothing was imported. Select Import again.',
    },
  ]

  for (const row of rows) {
    it(`${row.label}: the mapped text shows and the record is back to previewed`, async () => {
      installFetch({ answer: row.answer })
      openConfirm()
      await act(async () => {
        fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
      })
      expect(await screen.findByText(row.text)).toBeInTheDocument()
      expect(readRecord()?.phase).toBe('previewed')
      expect(screen.getByRole('button', { name: 'Import' })).toBeEnabled()
    })
  }

  it('404 workspace_not_found: text, list refreshed, record previewed; a new choice applies once with W2', async () => {
    const { applyCalls } = installFetch({
      answer: (() => {
        let n = 0
        return () => (n++ === 0 ? jsonResponse(404, { code: 'workspace_not_found' }) : jsonResponse(200, okResult))
      })(),
    })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('That workspace no longer exists. Choose another.')).toBeInTheDocument()
    expect(readRecord()?.phase).toBe('previewed')
    const listCallsAfterRefusal = mockWorkspaces.mock.calls.length
    expect(listCallsAfterRefusal, 'the workspace list is refreshed').toBeGreaterThan(1)

    const select = screen.getByRole('combobox')
    await act(async () => {
      fireEvent.change(select, { target: { value: 'ws-marketing' } })
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await screen.findByText('Writer (imported) was added to Marketing.')
    expect(applyCalls, 'exactly one new apply').toHaveLength(2)
    expect(JSON.parse(String(applyCalls[1]?.body)).workspace_id).toBe('ws-marketing')
  })

  it('404 staging_not_found: record cleared; the File step opens with the expired-or-used text', async () => {
    installFetch({ answer: () => jsonResponse(404, { code: 'staging_not_found' }) })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('This upload expired or was already used. Choose the file again.')).toBeInTheDocument()
    expect(readRecord(), 'the record is cleared').toBeNull()
    expect(screen.getByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
  })

  it('500 import_rolled_back: nothing-added text; record cleared; Start over goes to File with no cancel', async () => {
    const { applyCalls } = installFetch({ answer: () => jsonResponse(500, { code: 'import_rolled_back' }) })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('The import failed and was undone. Nothing was added.')).toBeInTheDocument()
    expect(readRecord()).toBeNull()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Start over' }))
    })
    expect(screen.getByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
    expect(applyCalls, 'no second apply').toHaveLength(1)
  })

  it('500 import_rollback_incomplete: the restart text and a cleared record', async () => {
    installFetch({ answer: () => jsonResponse(500, { code: 'import_rollback_incomplete' }) })
    openConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(
      await screen.findByText(
        'The import failed and could not be fully undone. Restart Omnipus to finish the clean-up.',
      ),
    ).toBeInTheDocument()
    expect(readRecord()).toBeNull()
  })
})

describe('Unknown apply outcome, never resent (US-5, D4-12…15)', () => {
  const UNKNOWN_TEXT =
    'The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again.'

  const rows: Array<{ label: string; answer: () => Response }> = [
    { label: 'a network error', answer: () => { throw new TypeError('network down') } },
    { label: 'an uncoded 500', answer: () => jsonResponse(500, { error: 'internal server error' }) },
    { label: 'a 502 from a proxy', answer: () => new Response('<html>bad gateway</html>', { status: 502 }) },
    { label: 'a 200 with a schema-invalid body', answer: () => jsonResponse(200, { unexpected: true }) },
  ]

  for (const row of rows) {
    it(`${row.label}: the Outcome-unknown screen, one apply, no Import, no cancel`, async () => {
      const { applyCalls } = installFetch({ answer: row.answer })
      openConfirm({
        staging_id: 'S',
        phase: 'previewed',
        workspace_id: 'ws-general',
        workspace_name: 'General',
        written_at: new Date().toISOString(),
      })
      await act(async () => {
        fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
      })
      expect(await screen.findByText(UNKNOWN_TEXT)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Import' })).toBeNull()
      expect(applyCalls, 'never resent').toHaveLength(1)
      expect(readRecord()?.phase, 'the record stays apply_sent').toBe('apply_sent')
    })
  }
})
