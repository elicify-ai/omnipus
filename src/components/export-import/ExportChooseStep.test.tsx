// Export Choose step — spec order 20 (issue #1967 B1).
// Oracles: spec scenarios "Choose lists only the person's own agents",
// "Review needs a choice", "No own agents", "Export refusals at review time"
// and "Unusable preselect" (D3-6/7), rendered through the /export-import page
// with the package API module mocked (per spec "Integration Boundaries":
// wizard-step tests mock the package API module).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, mockPlanCreate } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  mockPlanCreate: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: Record<string, unknown>) => ({
      ...opts,
      useSearch: () => mockSearch,
    }),
    useNavigate: () => navigateSpy,
  }
})

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: mockAgents,
  fetchWorkspaces: mockWorkspaces,
}))

vi.mock('@/lib/api/packages', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/packages')>()),
  createPackageExportPlan: mockPlanCreate,
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'

// The scenario's roster: two locked built-ins, the Judge (system), and the
// person's own three agents. Sorted by name: Coder, Helper, Writer.
const roster = [
  { id: 'a-coder', name: 'Coder', type: 'subagent_3p', locked: false, description: 'codes things' },
  { id: 'a-helper', name: 'Helper', type: 'Subagent', locked: false, description: 'helps out' },
  { id: 'a-judge', name: 'Judge', type: 'system', locked: false, description: 'judges' },
  { id: 'a-planner', name: 'Planner', type: 'Subagent', locked: true, description: 'plans' },
  { id: 'mia', name: 'Mia', type: 'core', locked: true, description: '' },
  { id: 'a-writer', name: 'Writer', type: 'Main', locked: false, description: 'writes things' },
]

beforeEach(() => {
  mockSearch = { wizard: 'export' }
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue(roster)
  mockWorkspaces.mockResolvedValue([])
  mockPlanCreate.mockReset()
  rememberIdentityMode('local')
  useUiStore.setState({ addToast: vi.fn() as unknown as () => void })
})

afterEach(() => {
  sessionStorage.clear()
})

function renderPage() {
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
}

// The radio's ACCESSIBLE name (label text or aria-label), not raw DOM
// textContent — the ordering assertions survive markup changes (CHECK W02).
function accessibleName(el: Element): string {
  const labelled = el.getAttribute('aria-label')
  if (labelled) return labelled
  const label = el.closest('label')
  return (label?.textContent ?? '').replace(/\s+/g, ' ').trim()
}

describe('Export Choose — the own-agent list (US-2.1)', () => {
  it('lists only own agents sorted by name, with kind labels, and the one-agent note', async () => {
    renderPage()
    expect(await screen.findByRole('radio', { name: /Writer/ })).toBeInTheDocument()
    const radios = screen.getAllByRole('radio')
    const names = radios.map(accessibleName)
    expect(names.some((l) => l.includes('Mia')), 'built-in Mia is not listed').toBe(false)
    expect(names.some((l) => l.includes('Planner')), 'locked Planner is not listed').toBe(false)
    expect(names.some((l) => l.includes('Judge')), 'system Judge is not listed').toBe(false)
    // Sorted by name (spec: Coder, Helper, Writer): the exact expected order,
    // each agent exactly once, in accessible-name order.
    const positions = ['Coder', 'Helper', 'Writer'].map((n) => {
      const matches = radios.filter((r) => accessibleName(r).includes(n))
      expect(matches, `${n} is listed exactly once`).toHaveLength(1)
      return radios.indexOf(matches[0])
    })
    expect(positions, 'rows are sorted by name').toEqual([0, 1, 2])
    expect(screen.getByText('External worker')).toBeInTheDocument()
    expect(screen.getByText('Worker')).toBeInTheDocument()
    expect(screen.getByText('Main')).toBeInTheDocument()
    expect(screen.getByText('A bundle holds one agent in this version.')).toBeInTheDocument()
  })

  it('two own agents with the same name are both listed and disambiguated (ID prefix, FR-016)', async () => {
    // The spec's exact disambiguation (rev 5, Ambiguity Warnings item 4):
    // when two names are equal, also show the FIRST 8 CHARACTERS of the
    // agent ID. The ids differ within those eight characters ('writer-a' vs
    // 'writer-9'), so a conforming display is exactly what this row asserts
    // — no longer prefix may be required of the product (CHECK 3 N04).
    mockAgents.mockResolvedValue([
      { id: 'writer-a1b2c3d4', name: 'Writer', type: 'Main', locked: false, description: 'writes' },
      { id: 'writer-9e8f7a6b', name: 'Writer', type: 'Main', locked: false, description: 'also writes' },
    ])
    renderPage()
    const radios = await screen.findAllByRole('radio')
    expect(radios, 'both same-name agents are listed').toHaveLength(2)
    const names = radios.map((r) => accessibleName(r))
    expect(names.every((n) => n.includes('Writer')), 'both rows carry the agent name').toBe(true)
    expect(names.filter((n) => n.includes('writer-a')), 'the first 8 characters of the first id are shown').toHaveLength(1)
    expect(names.filter((n) => n.includes('writer-9')), 'the first 8 characters of the second id are shown').toHaveLength(1)
    expect(new Set(names).size, 'duplicate names are disambiguated so each row is addressable').toBe(2)
  })

  it('choosing one agent un-chooses any other', async () => {
    renderPage()
    const coder = await screen.findByRole('radio', { name: /Coder/ })
    const writer = screen.getByRole('radio', { name: /Writer/ })
    fireEvent.click(coder)
    expect(coder).toBeChecked()
    fireEvent.click(writer)
    expect(writer).toBeChecked()
    expect(coder).not.toBeChecked()
  })

  it('without a choice Review is disabled with the hint "Choose an agent to continue."', async () => {
    renderPage()
    await screen.findByRole('radio', { name: /Writer/ })
    const review = screen.getByRole('button', { name: 'Review' })
    expect(review).toBeDisabled()
    expect(screen.getByText('Choose an agent to continue.')).toBeInTheDocument()
  })

  it('with only built-in agents: the empty text and no Review action', async () => {
    mockAgents.mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', locked: true, description: '' },
      { id: 'judge', name: 'Judge', type: 'system', locked: false, description: '' },
    ])
    renderPage()
    expect(
      await screen.findByText(
        'You have no agents of your own yet. Built-in agents are on every install and are not exported.',
      ),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Review' })).toBeNull()
  })
})

describe('Export Choose — the filter field (FR-016, D3)', () => {
  // GREEN CHECK P07 (spec "UI Screens and States" design-system table, line
  // 470: "Agent choice | radio-group (+ search-field for the filter)";
  // FR-028 catalogued components): the filter is the CATALOGUED search-field,
  // not a bare input — role searchbox (type=search), a "Clear search" button
  // while text is present, and Escape-to-clear. The narrowing, no-match and
  // restore oracles are unchanged.
  async function openList() {
    renderPage()
    await screen.findByRole('radio', { name: /Writer/ })
  }

  function filterField(): HTMLInputElement {
    const fields = screen.getAllByRole('searchbox')
    expect(fields, 'the Choose step has exactly one filter field').toHaveLength(1)
    return fields[0] as HTMLInputElement
  }

  async function filterTo(query: string) {
    await act(async () => {
      fireEvent.change(filterField(), { target: { value: query } })
    })
  }

  it('a matching query narrows the list to the matching agent', async () => {
    await openList()
    await filterTo('wri')
    expect(screen.getByRole('radio', { name: /Writer/ })).toBeInTheDocument()
    expect(screen.queryByRole('radio', { name: /Coder/ })).toBeNull()
    expect(screen.queryByRole('radio', { name: /Helper/ })).toBeNull()
  })

  it('a query with no match shows "No agent matches." and no rows', async () => {
    await openList()
    await filterTo('zzz')
    expect(screen.getByText('No agent matches.')).toBeInTheDocument()
    expect(screen.queryByRole('radio')).toBeNull()
  })

  it('the search-field offers its Clear button while text is present, and clicking it restores the full list', async () => {
    await openList()
    await filterTo('zzz')
    expect(screen.getByText('No agent matches.')).toBeInTheDocument()
    // The kit contract: the clear action exists exactly while text is present.
    expect(screen.getByRole('button', { name: 'Clear search' })).toBeInTheDocument()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Clear search' }))
    })
    expect(screen.queryByText('No agent matches.')).toBeNull()
    expect(screen.getByRole('radio', { name: /Coder/ })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /Helper/ })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /Writer/ })).toBeInTheDocument()
    // And the clear action goes away again once the field is empty.
    expect(screen.queryByRole('button', { name: 'Clear search' })).toBeNull()
  })

  it('Escape clears the filter and restores the full list', async () => {
    await openList()
    await filterTo('zzz')
    expect(screen.getByText('No agent matches.')).toBeInTheDocument()
    await act(async () => {
      fireEvent.keyDown(filterField(), { key: 'Escape' })
    })
    expect(screen.queryByText('No agent matches.')).toBeNull()
    expect(screen.getByRole('radio', { name: /Coder/ })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /Helper/ })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: /Writer/ })).toBeInTheDocument()
  })
})

describe('Export Choose — refusals at review time (US-2.6, D1)', () => {
  async function chooseWriterAndReview() {
    renderPage()
    fireEvent.click(await screen.findByRole('radio', { name: /Writer/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Review' }))
    await waitFor(() => expect(mockPlanCreate).toHaveBeenCalled())
  }

  const rows: Array<[string, () => ApiError, string]> = [
    [
      'blank SOUL.md',
      () => new ApiError(422, 'refused', { code: 'agent_prompt_missing' }),
      'This agent has no prompt, so it cannot be exported.',
    ],
    [
      'SOUL.md of 1,048,577 bytes',
      () =>
        new ApiError(422, 'refused', {
          code: 'agent_file_unreadable',
          body: JSON.stringify({ code: 'agent_file_unreadable', error: 'file unreadable', details: { check: 'too_large', file: 'SOUL.md' } }),
        }),
      'A file of this agent could not be read safely (SOUL.md). It is over 1 MB.',
    ],
    [
      'SOUL.md is a link',
      () =>
        new ApiError(422, 'refused', {
          code: 'agent_file_unreadable',
          body: JSON.stringify({ code: 'agent_file_unreadable', error: 'file unreadable', details: { check: 'link', file: 'SOUL.md' } }),
        }),
      'A file of this agent could not be read safely (SOUL.md).',
    ],
  ]

  for (const [condition, makeError, text] of rows) {
    it(`${condition}: the Choose step shows the mapped text and stays`, async () => {
      mockPlanCreate.mockRejectedValue(makeError())
      await chooseWriterAndReview()
      expect(await screen.findByText(text)).toBeInTheDocument()
      // Still on Choose: the radio list and Review action are present.
      expect(screen.getByRole('radio', { name: /Writer/ })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument()
    })
  }

  it('a worker with an empty description: the export rule text with the field', async () => {
    mockAgents.mockResolvedValue([
      { id: 'a-worker', name: 'Helper', type: 'Subagent', locked: false, description: '' },
    ])
    mockPlanCreate.mockRejectedValue(
      new ApiError(422, 'refused', {
        code: 'element_not_creatable',
        body: JSON.stringify({ code: 'element_not_creatable', field: 'description' }),
      }),
    )
    renderPage()
    fireEvent.click(await screen.findByRole('radio', { name: /Helper/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Review' }))
    expect(
      await screen.findByText('This agent breaks a rule for agents and cannot be exported: description'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument()
  })
})

describe('Export Choose — unusable preselect (D3-5…7, US-3.3)', () => {
  const rows: Array<[string, string]> = [
    ['agent:does-not-exist', 'The agent chosen from its page no longer exists.'],
    ['agent:mia', 'Built-in agents are on every install and cannot be exported.'],
    ['skill:research', 'This kind of item cannot be exported yet.'],
    ['agent:', 'This kind of item cannot be exported yet.'],
  ]

  for (const [value, notice] of rows) {
    it(`preselect=${value}: nothing chosen, notice "${notice}"`, async () => {
      mockSearch = { wizard: 'export', preselect: value }
      renderPage()
      expect(await screen.findByText(notice)).toBeInTheDocument()
      const radios = screen.queryAllByRole('radio')
      expect(radios.every((r) => !(r as HTMLInputElement).checked)).toBe(true)
    })
  }
})
