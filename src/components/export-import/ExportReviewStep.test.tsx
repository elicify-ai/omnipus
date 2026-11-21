// Export Review step — spec order 21 (issue #1967 B1).
// Oracles: spec scenarios "Review shows the server's review", "Review
// survives a reload", "Expired review"; the left_out message table; the
// free_text_copied disclosure text and the fixed "Never included" statement.
// The package API module is mocked (spec "Integration Boundaries").
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'
import type { PackageExportPlan } from '@/lib/api/packages'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, mockPlanCreate, mockPlanRead } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  mockPlanCreate: vi.fn(),
  mockPlanRead: vi.fn(),
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

vi.mock('@/lib/api/packages', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api/packages')>()),
  createPackageExportPlan: mockPlanCreate,
  getPackageExportPlan: mockPlanRead,
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'

// The review of Writer (Main) with AGENT.md 500 B, SOUL.md 2,000 B, skills
// research + Summarize, connector github (spec "Review shows the server's
// review"). Messages are the spec's exact left_out table rows, in table order.
const leftOut = [
  { type: 'agent_memory', reason: 'type_not_exportable', message: "This agent's memory is not included in this version." },
  { type: 'session', reason: 'type_not_exportable', message: "This agent's chats are not included." },
  { type: 'work_files', reason: 'type_not_exportable', message: 'Workspace files are not included.' },
  {
    type: 'team',
    reason: 'not_carried',
    message:
      'Team membership and delegation lines are not included. On import the agent joins the workspace you choose, with no delegation lines.',
  },
  {
    type: 'skill',
    reason: 'type_not_exportable',
    message: 'Skills are not included. The agent keeps their names; the other install uses a skill with the same name if it has one.',
    items: ['research', 'Summarize'],
  },
  {
    type: 'connector',
    reason: 'not_restored',
    message:
      'Connector names travel inside the agent\'s settings, but no connector settings or secrets are included, and the assignments are not restored on import. Assign connectors again after import.',
    items: ['github'],
  },
]

export function writerPlan(): PackageExportPlan {
  return {
    plan_id: 'P1',
    created_at: '2026-11-21T09:00:00Z',
    expires_at: '2026-11-21T09:30:00Z',
    scope: 'agents',
    elements: [
      {
        type: 'agent',
        source_id: 'a-writer',
        name: 'Writer',
        kind: 'main',
        files: [
          { path: 'agent.json', size: 1200 },
          { path: 'SOUL.md', size: 2000 },
          { path: 'AGENT.md', size: 500 },
        ],
        bytes: 3700,
      },
    ],
    left_out: leftOut,
    needs_secrets: [],
    disclosures: [
      {
        code: 'free_text_copied',
        message: 'Text you wrote (prompts and instructions) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle.',
      },
    ],
    size_bytes: 3700,
    file_name: 'Writer.omnpkg',
  }
}

beforeEach(() => {
  mockSearch = { wizard: 'export' }
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([{ id: 'a-writer', name: 'Writer', type: 'Main', locked: false, description: 'writes' }])
  mockWorkspaces.mockResolvedValue([])
  mockPlanCreate.mockReset().mockResolvedValue(writerPlan())
  mockPlanRead.mockReset().mockResolvedValue(writerPlan())
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

async function reviewWriter() {
  renderPage()
  fireEvent.click(await screen.findByRole('radio', { name: /Writer/ }))
  fireEvent.click(screen.getByRole('button', { name: 'Review' }))
  await waitFor(() => expect(navigateSpy).toHaveBeenCalled())
  const navArg = navigateSpy.mock.calls[0][0] as { search?: Record<string, string> }
  return navArg
}

describe('Export Review — the server review is shown (US-2.3, US-6.1)', () => {
  it('labels the files with sizes and shows the About size', async () => {
    const nav = await reviewWriter()
    expect(nav.search?.step).toBe('review')
    expect(nav.search?.plan_id).toBe('P1')
    // Re-render at the review address (the wizard is address-driven).
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    expect(await screen.findByText('Settings')).toBeInTheDocument()
    expect(screen.getByText('agent.json')).toBeInTheDocument()
    expect(screen.getByText('Prompt')).toBeInTheDocument()
    expect(screen.getByText('SOUL.md')).toBeInTheDocument()
    expect(screen.getByText('2.0 KB')).toBeInTheDocument()
    expect(screen.getByText('Instructions')).toBeInTheDocument()
    expect(screen.getByText('AGENT.md')).toBeInTheDocument()
    expect(screen.getByText('500 B')).toBeInTheDocument()
    expect(screen.getByText(/About/)).toBeInTheDocument()
  })

  it('lists the left-out rows in the exact table order with their exact messages and items', async () => {
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    await screen.findByText('Left out')
    const bodyText = document.body.textContent ?? ''
    const at = (s: string) => bodyText.indexOf(s)
    expect(at("This agent's memory is not included in this version.")).toBeGreaterThan(-1)
    expect(at("This agent's chats are not included.")).toBeGreaterThan(at("This agent's memory is not included in this version."))
    expect(at('Workspace files are not included.')).toBeGreaterThan(at("This agent's chats are not included."))
    expect(at('Team membership and delegation lines are not included.')).toBeGreaterThan(at('Workspace files are not included.'))
    expect(at('Skills are not included. The agent keeps their names;')).toBeGreaterThan(at('Team membership and delegation lines are not included.'))
    expect(at('Connector names travel inside the agent\'s settings,')).toBeGreaterThan(at('Skills are not included. The agent keeps their names;'))
    expect(screen.getByText('research')).toBeInTheDocument()
    expect(screen.getByText('Summarize')).toBeInTheDocument()
    expect(screen.getByText('github')).toBeInTheDocument()
  })

  it('shows the free-text notice and the fixed "Never included" statement', async () => {
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    expect(
      await screen.findByText(
        'Text you wrote (prompts and instructions) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        "Never included: passwords, API keys and tokens stored in Omnipus, provider keys, channel tokens, this install's settings file, and an external worker's program path, environment values and extra arguments.",
      ),
    ).toBeInTheDocument()
  })

  it('lists secrets under "Enter again after import" when the plan needs them', async () => {
    const plan = writerPlan()
    plan.needs_secrets = [{ name: 'API_TOKEN', used_by: 'external tool', kind: 'executor_env' }]
    mockPlanRead.mockResolvedValue(plan)
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    expect(await screen.findByText('Enter again after import')).toBeInTheDocument()
    expect(screen.getByText('API_TOKEN')).toBeInTheDocument()
  })
})

describe('Export Review — reload and expiry (US-2.7, US-2.8)', () => {
  it('a reload re-reads the plan from the server by plan_id', async () => {
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    await screen.findByText('Settings')
    expect(mockPlanRead).toHaveBeenCalledWith('P1')
  })

  it('an expired or evicted plan (read 404) returns to Choose with the expired notice', async () => {
    mockPlanRead.mockRejectedValue(new ApiError(404, 'gone', { code: 'plan_not_found' }))
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    expect(await screen.findByText('The review expired. Check your choice and review again.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review' })).toBeInTheDocument()
  })

  it('after the expired notice the agent is ticked only when preselect is in the address', async () => {
    mockPlanRead.mockRejectedValue(new ApiError(404, 'gone', { code: 'plan_not_found' }))
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1', preselect: 'agent:a-writer' }
    renderPage()
    await screen.findByText('The review expired. Check your choice and review again.')
    expect(screen.getByRole('radio', { name: /Writer/ })).toBeChecked()

    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    await screen.findByText('The review expired. Check your choice and review again.')
    expect(screen.getByRole('radio', { name: /Writer/ })).not.toBeChecked()
  })

  it('a plan whose expires_at has passed locally returns to Choose with the notice before Download', async () => {
    const plan = writerPlan()
    plan.expires_at = '2026-11-21T09:29:00Z'
    // Simulate a browser clock past expires_at: the plan is loaded (fresh
    // created_at + expires_at 30 min ahead of a NOW the test pins via the
    // plan itself) — instead pin the past directly on the loaded plan.
    plan.created_at = '2026-11-21T08:00:00Z'
    plan.expires_at = '2026-11-21T08:30:00Z'
    mockPlanRead.mockResolvedValue(plan)
    mockSearch = { wizard: 'export', step: 'review', plan_id: 'P1' }
    renderPage()
    await screen.findByText('Settings')
    fireEvent.click(screen.getByRole('button', { name: 'Download' }))
    expect(await screen.findByText('The review expired. Check your choice and review again.')).toBeInTheDocument()
  })
})
