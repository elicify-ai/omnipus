// The /export-import route — spec order 17 (issue #1967 B1).
// Oracles: spec "Address (URL) state" table + Dataset D3 (search validation),
// US-1.4 (platform edition notice), the UI-states landing row, and the
// Machine-Verifiable row "platform mode → unavailable notice and zero
// /api/v1/packages/ requests".
//
// Mirrors -library.test.tsx's mocking approach: createFileRoute is stubbed
// with a fixed useSearch, and Route.options.validateSearch stays REAL so the
// URL table is exercised against the actual validator.
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'

let mockSearch: Record<string, string | undefined> = {}

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType; validateSearch?: (s: Record<string, unknown>) => unknown }) => ({
      ...opts,
      useSearch: () => mockSearch,
    }),
    useNavigate: () => vi.fn(),
  }
})

const { mockAgents, mockWorkspaces } = vi.hoisted(() => ({ mockAgents: vi.fn(), mockWorkspaces: vi.fn() }))
// The REAL '@/lib/api' module stays loaded under the spread: workspaceQueries
// (the shared activeWorkspacesQueryOptions the Confirm step now uses) reads
// workspacesQueryKeys from this module at import time, so a replacement
// factory here breaks the import before any test runs (CI PR #1971,
// Vitest - lib-store, head d5c7a0427).
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: mockAgents,
  fetchWorkspaces: mockWorkspaces,
}))

import { Route } from './export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'

// The passthrough createFileRoute mock spreads the route opts, so the REAL
// validateSearch and component sit on the mocked Route object itself.
function realValidateSearch(): (s: Record<string, unknown>) => Record<string, unknown> {
  const validate = (Route as unknown as {
    validateSearch?: (s: Record<string, unknown>) => Record<string, unknown>
  }).validateSearch
  expect(validate, 'the route must define validateSearch (the URL table)').toBeDefined()
  return validate as (s: Record<string, unknown>) => Record<string, unknown>
}

const ownAgents = [
  { id: 'a-coder', name: 'Coder', type: 'worker', description: 'codes', locked: false },
  { id: 'a-helper', name: 'Helper', type: 'worker', description: 'helps', locked: false },
  { id: 'a-writer', name: 'Writer', type: 'custom', description: 'writes', locked: false },
  { id: 'mia', name: 'Mia', type: 'core', description: '', locked: false },
]

beforeEach(() => {
  mockSearch = {}
  mockAgents.mockResolvedValue(ownAgents)
  mockWorkspaces.mockReset().mockResolvedValue([])
  rememberIdentityMode('local')
})

function renderPage() {
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
}

describe('/export-import — URL validation (Dataset D3)', () => {
  it('D3-1 no parameters validates to the landing (no wizard)', () => {
    const search = realValidateSearch()({})
    expect(search.wizard).toBeUndefined()
  })

  it('D3-2 wizard=export validates through and lands on Choose', () => {
    const search = realValidateSearch()({ wizard: 'export' })
    expect(search.wizard).toBe('export')
    expect(search.step).toBeUndefined()
  })

  it('D3-3 an invalid step drops to the first step of the wizard', () => {
    const search = realValidateSearch()({ wizard: 'export', step: 'bogus' })
    expect(search.wizard).toBe('export')
    expect(search.step).toBe('choose')
  })

  it('D3-4 an unknown wizard drops to the landing', () => {
    const search = realValidateSearch()({ wizard: 'nope' })
    expect(search.wizard).toBeUndefined()
  })

  it('D3-11 scope=everything is treated as agents', () => {
    const search = realValidateSearch()({ scope: 'everything' })
    expect(search.scope).toBe('agents')
  })

  it('D3-8 plan_id=expired survives validation; the page decides with the server', () => {
    const search = realValidateSearch()({ wizard: 'export', step: 'review', plan_id: 'expired' })
    expect(search.plan_id).toBe('expired')
  })
})

describe('/export-import — the page (US-1, landing)', () => {
  it('the landing shows the heading, Start export and Start import with their texts', () => {
    renderPage()
    expect(screen.getByRole('heading', { name: 'Export & Import' })).toBeInTheDocument()
    expect(screen.getByText('Save one of your agents as a bundle file.')).toBeInTheDocument()
    expect(screen.getByText('Add an agent from a bundle file.')).toBeInTheDocument()
  })

  it('an export wizard address opens the export Choose step', () => {
    mockSearch = { wizard: 'export' }
    renderPage()
    expect(screen.getByText('A bundle holds one agent in this version.')).toBeInTheDocument()
  })

  it('a forged ?wizard=import&step=result with no tab record claims nothing (D3-9a)', () => {
    mockSearch = { wizard: 'import', step: 'result' }
    renderPage()
    // Landing, no completion claim: the result summary is absent.
    expect(screen.queryByText('Import finished')).toBeNull()
    expect(screen.getByRole('heading', { name: 'Export & Import' })).toBeInTheDocument()
  })
})

describe('/export-import — preselect (Dataset D3 rows 5–7, US-3.3)', () => {
  it('D3-5 preselect=agent:a-writer opens the choose step with Writer ticked', async () => {
    mockSearch = { wizard: 'export', preselect: 'agent:a-writer' }
    renderPage()
    const chosen = await screen.findByRole('radio', { name: /Writer/ })
    expect(chosen).toBeChecked()
  })

  it('D3-6 a malformed preselect ticks nothing and explains why', async () => {
    mockSearch = { wizard: 'export', preselect: 'agent:' }
    renderPage()
    await screen.findByText(/cannot be exported yet|is not an agent that can be exported/)
    const radios = screen.queryAllByRole('radio')
    expect(radios.every((r) => !(r as HTMLInputElement).checked)).toBe(true)
  })

  it('D3-7 a non-agent preselect type ticks nothing and explains why', async () => {
    mockSearch = { wizard: 'export', preselect: 'skill:x' }
    renderPage()
    await screen.findByText(/cannot be exported yet|is not an agent that can be exported/)
    const radios = screen.queryAllByRole('radio')
    expect(radios.every((r) => !(r as HTMLInputElement).checked)).toBe(true)
  })
})

describe('/export-import — edition gating (US-1.4, fail closed)', () => {
  it('platform edition: the unavailable notice and no wizard', () => {
    rememberIdentityMode('platform')
    renderPage()
    expect(screen.getByText('Export & Import is not available in this edition.')).toBeInTheDocument()
    expect(screen.queryByText('Save one of your agents as a bundle file.')).toBeNull()
  })

  it('edition unknown (null): the same unavailable notice — fail closed', () => {
    rememberIdentityMode(undefined)
    renderPage()
    expect(screen.getByText('Export & Import is not available in this edition.')).toBeInTheDocument()
  })

  it('platform edition: zero /api/v1/packages/ requests', async () => {
    rememberIdentityMode('platform')
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    renderPage()
    await new Promise((r) => setTimeout(r, 20))
    const packageCalls = fetchSpy.mock.calls.filter(([url]) => String(url).includes('/api/v1/packages/'))
    expect(packageCalls, 'the unavailable page sends no package request').toEqual([])
  })
})
