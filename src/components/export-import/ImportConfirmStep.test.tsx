// Import Confirm step — spec order 26 (issue #1967 B1).
// Oracles: spec scenarios "Confirm states the mode and pre-chooses the
// default workspace", "No default workspace", "No active workspace",
// "Another import is running", "Try again only after a proven pre-write
// refusal", "Record after a known refusal", "Session refresh on apply is not
// replayed" (real request() over a mocked fetch — the replay rows of 26 per
// spec), "Other apply failures", "Unknown apply outcome, never resent",
// "Expired upload at confirm"; the Import record transitions table.
//
// The step is rendered at its PUBLIC boundary (CHECK F06): a fresh Confirm
// reaches this component through the wizard's in-memory transition, not by
// mounting the route at a confirm address — an initial-URL mount with a
// previewed record IS the reload state and must cancel, which is row 28's
// contract. The record the wizard wrote at Preview time is seeded as the step
// receives it; its transitions around the apply call are asserted.
//
// The packages module stays REAL: the step's calls run through the production
// transport over a mocked fetch (state + apply URLs), and mutations run under
// a production-defaults QueryClient (CHECK F04).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { shouldRetryMutation, mutationRetryDelay } from '@/lib/queryClient'
import {
  PackageImportPreviewSchema,
  PackageImportResultSchema,
  type PackageImportPreview,
  type PackageImportResult,
} from '@/lib/api/packages'

// GREEN CHECK P07 (spec "UI Screens and States" design-system table, line 473:
// "Workspace choice | select"; FR-028 catalogued components): the Confirm's
// workspace choice is the CATALOGUED select, driven through its real
// accessibility contract — a combobox trigger showing the chosen label, a
// choice made by selecting an option — never native <select> change events.
// The shim follows the repo's Radix-select test precedent
// (ConnectorsScreen.test.tsx) and mirrors SelectValue's selected-label display
// so the trigger's shown text stays observable.
//
// D-3 (round 8): the label map is filled by the SELECT itself, from its own
// children, DURING the Select's render — before SelectTrigger/SelectValue
// render in the same pass. The previous shim filled the map in SelectItem's
// effect, which runs after SelectValue has already rendered, so a value
// pre-choosen on the first render showed the placeholder. The walk descends
// through wrappers (fragments, arrays, SelectGroup), so the map is complete
// on the very first render with no extra pass.
vi.mock('@/components/ui/select', () => {
  const labels = new Map<string, string>()
  type SelectProps = { value?: string; onValueChange?: (v: string) => void; children?: React.ReactNode }
  type ItemProps = { value: string; children?: React.ReactNode }
  const SelectCtx = React.createContext<{ value?: string; onValueChange?: (v: string) => void }>({})

  const SelectItem = ({ value, children }: ItemProps) => {
    const { onValueChange } = React.useContext(SelectCtx)
    return React.createElement(
      'div',
      { role: 'option', 'data-value': value, onClick: () => onValueChange?.(value) },
      children,
    )
  }

  function collectItemLabels(node: React.ReactNode, into: Map<string, string>): void {
    React.Children.forEach(node, (child) => {
      if (!React.isValidElement(child)) return
      const props = child.props as { value?: unknown; children?: unknown }
      if (child.type === SelectItem) {
        if (typeof props.value === 'string') {
          into.set(props.value, typeof props.children === 'string' ? props.children : props.value)
        }
        return
      }
      if (props.children != null) collectItemLabels(props.children as React.ReactNode, into)
    })
  }

  const Select = ({ value, onValueChange, children }: SelectProps) => {
    labels.clear()
    collectItemLabels(children, labels)
    return React.createElement(SelectCtx.Provider, { value: { value, onValueChange } }, children)
  }
  const SelectTrigger = ({ children, ...rest }: { children?: React.ReactNode } & Record<string, unknown>) =>
    React.createElement('button', { type: 'button', role: 'combobox', ...rest }, children)
  const SelectValue = ({ placeholder }: { placeholder?: string }) => {
    const { value } = React.useContext(SelectCtx)
    return React.createElement('span', {}, (value && labels.get(value)) || placeholder)
  }
  const SelectContent = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'listbox' }, children)
  const SelectLabel = ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children)
  const SelectGroup = ({ children }: { children?: React.ReactNode }) => React.createElement('div', {}, children)
  const SelectSeparator = () => React.createElement('hr', {})

  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectLabel, SelectGroup, SelectSeparator }
})

const RECORD_KEY = 'omnipus.exportImport.import'

const { navigateSpy, mockWorkspaces, addToastSpy, onAppliedSpy, onBackSpy } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockWorkspaces: vi.fn(),
  addToastSpy: vi.fn(),
  onAppliedSpy: vi.fn(),
  onBackSpy: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => navigateSpy,
  }
})

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchWorkspaces: mockWorkspaces,
}))

import { ImportConfirmStep } from './ImportConfirmStep'
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

function validPreview(): PackageImportPreview {
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

function okResult(activation: 'active' | 'pending' = 'active', workspaceId = 'ws-marketing'): PackageImportResult {
  const result: PackageImportResult = {
    mode: 'new',
    created: [{ type: 'agent', source_id: 'a-writer', new_id: 'a-new', name: 'Writer (imported)', workspace_id: workspaceId }],
    warnings: [],
    needs_secrets: [],
    activation,
  }
  PackageImportResultSchema.parse(result)
  return result
}

const CSRF_403 = () => jsonResponse(403, { error: 'csrf token mismatch' })
const PLANTED_403 = () => jsonResponse(403, { code: 'planted_cookie_cleared', error: 'planted' })

// Installs the fetch double for one row and returns LIVE counters. The record
// value AT THE MOMENT the apply request leaves is captured per call.
function installFetch(answer: () => Response): {
  applyBodies: string[]
  recordAtApply: Array<string | null>
  stateCount: () => number
} {
  const applyBodies: string[] = []
  const recordAtApply: Array<string | null> = []
  let stateReads = 0
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const u = String(url)
    if (u.endsWith('/api/v1/state')) {
      stateReads += 1
      return jsonResponse(200, { onboarding_complete: true, dev_mode_bypass: false })
    }
    if (u.includes('/packages/import/S/apply')) {
      applyBodies.push(String((init as RequestInit | undefined)?.body))
      recordAtApply.push(sessionStorage.getItem(RECORD_KEY))
      return answer()
    }
    throw new Error(`unexpected fetch in test: ${u}`)
  })
  return { applyBodies, recordAtApply, stateCount: () => stateReads }
}

// A production-defaults client (the real exported predicates — CHECK F04);
// the step's queries/mutations ride these defaults.
function makeProductionQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: mutationRetryDelay } } })
}

// Renders the step at its public boundary (fresh Confirm, NOT the reload
// state — CHECK F06), with the record the wizard wrote at Preview time.
function renderConfirm(workspaceInRecord = true) {
  seedRecord({
    staging_id: 'S',
    phase: 'previewed',
    ...(workspaceInRecord ? { workspace_id: 'ws-general', workspace_name: 'General' } : {}),
    written_at: new Date().toISOString(),
  })
  render(
    <QueryClientProvider client={makeProductionQueryClient()}>
      <ImportConfirmStep stagingId="S" preview={validPreview()} onApplied={onAppliedSpy} onBack={onBackSpy} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  navigateSpy.mockReset()
  mockWorkspaces.mockReset().mockResolvedValue(workspaces)
  onAppliedSpy.mockReset()
  onBackSpy.mockReset()
  addToastSpy.mockClear()
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
    installFetch(() => jsonResponse(200, okResult()))
    renderConfirm()
    expect(await screen.findByText('Added as new. Existing agents are never changed.')).toBeInTheDocument()
    // GREEN CHECK P07: the catalogued select shows the CHOSEN WORKSPACE'S
    // LABEL in its trigger (SelectValue) — the observable form of the
    // pre-choice; the combobox is the trigger button, not a native select.
    expect(await screen.findByRole('combobox')).toHaveTextContent('General')
  })

  it('without a default workspace nothing is pre-chosen and Import is disabled until one is chosen', async () => {
    mockWorkspaces.mockResolvedValue([
      { id: 'ws-a', name: 'A', is_default: false, pinned: false },
      { id: 'ws-b', name: 'B', is_default: false, pinned: false },
    ])
    installFetch(() => jsonResponse(200, okResult()))
    renderConfirm(false)
    // GREEN CHECK P07: with no choice the trigger shows the placeholder, and
    // the choice is made by selecting the option — the catalogued select's
    // contract (spec line 473), not a native change event.
    expect(await screen.findByRole('combobox')).toHaveTextContent('Choose a workspace')
    expect(screen.getByRole('button', { name: 'Import' })).toBeDisabled()
    await act(async () => {
      fireEvent.click(screen.getByRole('option', { name: 'B' }))
    })
    expect(screen.getByRole('button', { name: 'Import' })).toBeEnabled()
  })

  it('no active workspace: "Create a workspace first." and no Import action', async () => {
    mockWorkspaces.mockResolvedValue([])
    installFetch(() => jsonResponse(200, okResult()))
    renderConfirm(false)
    expect(await screen.findByText('Create a workspace first.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Import' })).toBeNull()
  })

  it('Import sends the exact {mode, workspace_id} body with the record at apply_sent before the request', async () => {
    const { applyBodies, recordAtApply } = installFetch(() => jsonResponse(200, okResult()))
    renderConfirm()
    // GREEN CHECK P07: the choice goes through the catalogued select's option
    // (trigger → option), and the exact body assertion below is unchanged.
    await screen.findByRole('combobox')
    await act(async () => {
      fireEvent.click(screen.getByRole('option', { name: 'Marketing' }))
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await act(async () => {})
    expect(onAppliedSpy).toHaveBeenCalledTimes(1)
    expect(onAppliedSpy.mock.calls[0][0]).toMatchObject({ created: [{ new_id: 'a-new' }], activation: 'active' })
    expect(applyBodies).toHaveLength(1)
    expect(JSON.parse(applyBodies[0])).toEqual({ mode: 'new', workspace_id: 'ws-marketing' })
    const atRequest = JSON.parse(recordAtApply[0] ?? 'null') as Record<string, unknown> | null
    expect(atRequest?.phase, 'phase apply_sent is written before the request leaves').toBe('apply_sent')
    expect(atRequest?.staging_id).toBe('S')
  })
})

describe('Session refresh on apply is not replayed (US-5, D4-11a/11b)', () => {
  const SESSION_TEXT = 'Your session needed refreshing. Nothing was imported. Select Import again.'

  it('a server CSRF 403: one apply, one recovery read, no toast, record previewed', async () => {
    const { applyBodies, stateCount } = installFetch(CSRF_403)
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await screen.findByText(SESSION_TEXT)
    expect(applyBodies).toHaveLength(1)
    expect(stateCount(), 'exactly one recovery read GET /api/v1/state').toBe(1)
    expect(addToastSpy).not.toHaveBeenCalled()
    expect(readRecord()?.phase).toBe('previewed')
    expect(screen.getByRole('button', { name: 'Import' })).toBeEnabled()
  })

  it('a planted_cookie_cleared 403: one apply, one recovery read, no toast', async () => {
    const { applyBodies, stateCount } = installFetch(PLANTED_403)
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await screen.findByText(SESSION_TEXT)
    expect(applyBodies).toHaveLength(1)
    expect(stateCount()).toBe(1)
    expect(addToastSpy).not.toHaveBeenCalled()
    expect(readRecord()?.phase).toBe('previewed')
  })

  it('client csrf_missing: ZERO apply requests, one recovery read; the next click sends the first', async () => {
    document.cookie = 'csrf=; max-age=0; path=/'
    const { applyBodies, stateCount } = installFetch(() => jsonResponse(200, okResult()))
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await screen.findByText(SESSION_TEXT)
    expect(applyBodies, 'the real cookie gate refuses before any request').toHaveLength(0)
    expect(stateCount(), 'exactly one recovery read').toBe(1)
    expect(readRecord()?.phase).toBe('previewed')

    // The session is valid again; the person clicks Import once more.
    document.cookie = 'csrf=import-confirm-test; path=/'
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await act(async () => {})
    expect(onAppliedSpy).toHaveBeenCalledTimes(1)
    expect(applyBodies, 'this click sends the FIRST apply').toHaveLength(1)
  })
})

describe('Another import is running + Try again (US-5, D4-6)', () => {
  it('409 import_in_progress: message and Try again; the retry sends the same body once more', async () => {
    const { applyBodies, recordAtApply } = installFetch(() => jsonResponse(409, { code: 'import_in_progress' }))
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('Another import is running. Try again in a moment.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
    expect(readRecord()?.phase).toBe('previewed')
    expect(applyBodies).toHaveLength(1)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    })
    await screen.findByText('Another import is running. Try again in a moment.')
    expect(applyBodies, 'exactly one second apply with the same body').toHaveLength(2)
    expect(JSON.parse(applyBodies[1])).toEqual(JSON.parse(applyBodies[0]))
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
      installFetch(row.answer)
      renderConfirm()
      await act(async () => {
        fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
      })
      expect(await screen.findByText(row.text)).toBeInTheDocument()
      expect(readRecord()?.phase).toBe('previewed')
      expect(screen.getByRole('button', { name: 'Import' })).toBeEnabled()
    })
  }

  it('404 workspace_not_found: text, list refreshed, record previewed; a new choice applies once with W2', async () => {
    const { applyBodies } = installFetch(
      (() => {
        let n = 0
        return () => (n++ === 0 ? jsonResponse(404, { code: 'workspace_not_found' }) : jsonResponse(200, okResult()))
      })(),
    )
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('That workspace no longer exists. Choose another.')).toBeInTheDocument()
    expect(readRecord()?.phase).toBe('previewed')
    const listCallsAfterRefusal = mockWorkspaces.mock.calls.length
    expect(listCallsAfterRefusal, 'the workspace list is refreshed').toBeGreaterThan(1)

    // GREEN CHECK P07: the new choice after the refusal is an option
    // selection on the catalogued select; the apply-once assertion below is
    // unchanged.
    await act(async () => {
      fireEvent.click(screen.getByRole('option', { name: 'Marketing' }))
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Import' }))
    })
    await act(async () => {})
    expect(onAppliedSpy).toHaveBeenCalledTimes(1)
    expect(applyBodies, 'exactly one new apply').toHaveLength(2)
    expect(JSON.parse(applyBodies[1]).workspace_id).toBe('ws-marketing')
  })

  it('404 staging_not_found: record cleared, exactly one apply; the step hands control back to the File step', async () => {
    const { applyBodies } = installFetch(() => jsonResponse(404, { code: 'staging_not_found' }))
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    await act(async () => {})
    expect(readRecord(), 'the record is cleared').toBeNull()
    expect(applyBodies, 'exactly one apply was sent for the expired staging id').toHaveLength(1)
    // The step hands control back to the File step (the navigation is the
    // observable boundary here). The File step's own notice and action are
    // asserted at the route level in ExportImportWizard.reload.test.tsx
    // ("Expired upload at confirm returns to File") — the only level where
    // the File step renders (CHECK N03 round 2).
    expect(navigateSpy).toHaveBeenCalledTimes(1)
    const arg = JSON.stringify(navigateSpy.mock.calls[0])
    expect(arg).toContain('file')
    expect(arg).toContain('import')
  })

  it('500 import_rolled_back: nothing-added text; record cleared; Start over goes to File with no second apply', async () => {
    const { applyBodies } = installFetch(() => jsonResponse(500, { code: 'import_rolled_back' }))
    renderConfirm()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
    })
    expect(await screen.findByText('The import failed and was undone. Nothing was added.')).toBeInTheDocument()
    expect(readRecord()).toBeNull()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Start over' }))
    })
    expect(navigateSpy).toHaveBeenCalledTimes(1)
    expect(JSON.stringify(navigateSpy.mock.calls[0])).toContain('file')
    expect(applyBodies, 'no second apply').toHaveLength(1)
  })

  it('500 import_rollback_incomplete: the restart text and a cleared record', async () => {
    installFetch(() => jsonResponse(500, { code: 'import_rollback_incomplete' }))
    renderConfirm()
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
      const { applyBodies } = installFetch(row.answer)
      renderConfirm()
      await act(async () => {
        fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
      })
      expect(await screen.findByText(UNKNOWN_TEXT)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Import' })).toBeNull()
      expect(applyBodies, 'never resent').toHaveLength(1)
      expect(readRecord()?.phase, 'the record stays apply_sent').toBe('apply_sent')
    })
  }
})
