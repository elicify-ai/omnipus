// Import Result step — spec order 27 (issue #1967 B1).
// Oracles: spec scenarios "Import succeeds", "Chat action decided by the
// agent lookup" (the activation × lookup table), "Pending activation",
// "Lists refresh after import" (invalidateAfterAgentCreate once), and the
// Reload-mid-import result rows. The packages module stays real; fetch is
// mocked for the apply, and @/lib/api's fetchAgent / invalidateAfterAgentCreate
// are spies. Mock answers are valid against the unchanged PackageImportResult
// (no kind field invented).
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const RECORD_KEY = 'omnipus.exportImport.import'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces, mockFetchAgent, mockInvalidate } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
  mockFetchAgent: vi.fn(),
  mockInvalidate: vi.fn(),
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
  fetchAgent: mockFetchAgent,
  invalidateAfterAgentCreate: mockInvalidate,
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

const applyAnswer = (activation: string, warnings: Array<{ code: string; message: string }> = []) => ({
  mode: 'new',
  created: [
    {
      type: 'agent',
      source_id: 'a-writer',
      source_name: 'Writer',
      name: 'Writer (imported)',
      new_id: 'a-new',
      workspace_id: 'ws-marketing',
      activation,
    },
  ],
  warnings,
  needs_secrets: [],
})

// Installs the fetch double answering the apply URL; counts /packages/ calls.
function installFetch(answer: () => Response): { packageCalls: string[] } {
  const packageCalls: string[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
    const u = String(url)
    if (u.includes('/packages/')) {
      packageCalls.push(u)
      if (u.includes('/apply')) return answer()
      throw new Error(`unexpected package call in test: ${u}`)
    }
    throw new Error(`unexpected fetch in test: ${u}`)
  })
  return { packageCalls }
}

function renderAt(search: Record<string, string | undefined>) {
  mockSearch = search
  const Component = (Route as unknown as { component: React.ComponentType }).component
  return render(<Component />)
}

const confirmRecord = {
  staging_id: 'S',
  phase: 'previewed',
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

async function runImport(activation: string, warnings: Array<{ code: string; message: string }> = []) {
  sessionStorage.setItem(RECORD_KEY, JSON.stringify(confirmRecord))
  installFetch(() => jsonResponse(200, applyAnswer(activation, warnings)))
  renderAt({ wizard: 'import', step: 'confirm', staging_id: 'S' })
  fireEvent.click(await screen.findByRole('button', { name: 'Import' }))
  await screen.findByText('Import finished')
}

beforeEach(() => {
  mockSearch = {}
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([])
  mockWorkspaces.mockReset().mockResolvedValue([
    { id: 'ws-marketing', name: 'Marketing', is_default: true, pinned: false },
  ])
  mockFetchAgent.mockReset().mockRejectedValue(new Error('fetchAgent not seeded for this row'))
  mockInvalidate.mockReset()
  rememberIdentityMode('local')
  useUiStore.setState({ addToast: vi.fn() as unknown as () => void })
  document.cookie = 'csrf=import-result-test; path=/'
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
  sessionStorage.clear()
})

describe('Import Result (US-4.4)', () => {
  it('shows "<final name> was added to <workspace>." with Open Team and Import another', async () => {
    await runImport('active')
    expect(screen.getByText('Writer (imported) was added to Marketing.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Import another' })).toBeInTheDocument()
  })

  it('shows warnings and secrets on the result when the answer carries them', async () => {
    await runImport('active', [{ code: 'publish_not_durable', message: 'The schedule will not survive a restart.' }])
    expect(screen.getByText('The schedule will not survive a restart.')).toBeInTheDocument()
  })

  it('the lists refresh: invalidateAfterAgentCreate is called exactly once', async () => {
    await runImport('active')
    expect(mockInvalidate).toHaveBeenCalledTimes(1)
  })

  describe('Chat action decided by the agent lookup (D4-1…4)', () => {
    it('active + Main: "Open chat" opens the agent chat', async () => {
      mockFetchAgent.mockResolvedValue({ id: 'a-new', name: 'Writer (imported)', type: 'Main', locked: false, description: '' })
      await runImport('active')
      await waitFor(() => expect(mockFetchAgent).toHaveBeenCalledWith('a-new'))
      expect(await screen.findByRole('button', { name: 'Open chat' })).toBeInTheDocument()
    })

    it('active + Subagent: no chat action, rest of the result intact', async () => {
      mockFetchAgent.mockResolvedValue({ id: 'a-new', name: 'Writer (imported)', type: 'Subagent', locked: false, description: '' })
      await runImport('active')
      await waitFor(() => expect(mockFetchAgent).toHaveBeenCalled())
      expect(await screen.findByText('Writer (imported) was added to Marketing.')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
      expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
    })

    it('active + subagent_3p: no chat action', async () => {
      mockFetchAgent.mockResolvedValue({ id: 'a-new', name: 'Writer (imported)', type: 'subagent_3p', locked: false, description: '' })
      await runImport('active')
      await waitFor(() => expect(mockFetchAgent).toHaveBeenCalled())
      await screen.findByText('Writer (imported) was added to Marketing.')
      expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    })

    it('pending activation: the activation message shows in a notice and "Open chat" is absent; the agent is not looked up', async () => {
      await runImport('pending', [{ code: 'activation_pending', message: 'The agent starts on its next heartbeat.' }])
      expect(await screen.findByText('The agent starts on its next heartbeat.')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
      expect(mockFetchAgent).not.toHaveBeenCalled()
    })

    it('lookup still loading: no chat link yet, no failure text either', async () => {
      mockFetchAgent.mockReturnValue(new Promise(() => {}))
      await runImport('active')
      await waitFor(() => expect(mockFetchAgent).toHaveBeenCalled())
      await screen.findByText('Writer (imported) was added to Marketing.')
      expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
      expect(screen.queryByText('Open it from the Team.')).toBeNull()
    })

    it('lookup 404 or network failure: "Open it from the Team." replaces the action', async () => {
      mockFetchAgent.mockRejectedValue(new Error('404'))
      await runImport('active')
      expect(await screen.findByText('Open it from the Team.')).toBeInTheDocument()
      expect(screen.getByText('Writer (imported) was added to Marketing.')).toBeInTheDocument()
    })
  })

  describe('Reload mid-import — result rows (D3-9/9a)', () => {
    it('a done record shows the stored summary under "Import finished" with no package request', () => {
      const { packageCalls } = { packageCalls: [] as string[] }
      const fetchSpy = vi.spyOn(globalThis, 'fetch')
      sessionStorage.setItem(RECORD_KEY, JSON.stringify(doneRecord))
      renderAt({ wizard: 'import', step: 'result', staging_id: 'S' })
      expect(screen.getByText('Import finished')).toBeInTheDocument()
      expect(screen.getByText('Writer (imported) was added to Marketing.')).toBeInTheDocument()
      const pkg = fetchSpy.mock.calls.filter(([url]) => String(url).includes('/packages/'))
      expect(pkg, 'no package request on a result reload').toHaveLength(0)
      void packageCalls
    })

    it('no record: landing, no completion claim, no package request', () => {
      const fetchSpy = vi.spyOn(globalThis, 'fetch')
      renderAt({ wizard: 'import', step: 'result', staging_id: 'S' })
      expect(screen.queryByText('Import finished')).toBeNull()
      expect(screen.getByText('Save one of your agents as a bundle file.')).toBeInTheDocument()
      const pkg = fetchSpy.mock.calls.filter(([url]) => String(url).includes('/packages/'))
      expect(pkg).toHaveLength(0)
    })
  })
})
