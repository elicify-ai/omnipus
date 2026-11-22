// Import Result step — spec order 27 (issue #1967 B1).
// Oracles: spec scenarios "Import succeeds", "Chat action decided by the
// agent lookup" (the activation × lookup table), "Pending activation",
// "Lists refresh after import" (invalidateAfterAgentCreate once; FR-023).
//
// The step is rendered at its PUBLIC boundary (CHECK F06): the wizard hands it
// the parsed apply result and the workspace name and receives the actions
// through callbacks. Mounting the route at ?step=result was the reload state
// (a stored record must show; without one it must not) — those rows are
// wizard-level and live in ExportImportWizard.reload.test.tsx (D3-9/9a).
//
// Fixtures are valid against the UNCHANGED generated PackageImportResult
// (CHECK F02: activation and activation_message are TOP-LEVEL result fields;
// created[] carries {type, source_id, new_id, name, workspace_id}; no kind
// field is invented) — the PackageImportResultSchema.parse calls at the bottom
// of every fixture builder fail the test loudly if a fixture drifts. The
// lookup answers are validated against the generated Agent schema.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { shouldRetryMutation, mutationRetryDelay } from '@/lib/queryClient'
import { Agent as AgentSchema } from '@/lib/api/generated/schemas'
import type { Agent } from '@/lib/api/generated/openapi-types'

const { mockFetchAgent, mockInvalidate, onOpenTeamSpy, onImportAnotherSpy, onOpenChatSpy } = vi.hoisted(() => ({
  mockFetchAgent: vi.fn(),
  mockInvalidate: vi.fn(),
  onOpenTeamSpy: vi.fn(),
  onImportAnotherSpy: vi.fn(),
  onOpenChatSpy: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgent: mockFetchAgent,
  invalidateAfterAgentCreate: mockInvalidate,
}))

import { ImportResultStep } from './ImportResultStep'
import {
  PackageImportResultSchema,
  type PackageImportResult,
} from '@/lib/api/packages'

function agentFixture(type: Agent['type']): Agent {
  const agent: Agent = {
    revision: 'a'.repeat(64),
    id: 'a-new',
    name: 'Writer (imported)',
    type,
    locked: false,
    figure: 'Robot',
    role: 'writer',
    status: 'active',
    soul: 'You are Writer.',
    max_tool_iterations: 30,
    max_tool_iterations_source: 'global',
    max_tool_iterations_override_ignored: false,
    needs_model: false,
    memory_enabled: true,
  }
  AgentSchema.parse(agent) // a lookup answer that fails its own schema is a harness bug
  return agent
}

function validResult(overrides: Partial<PackageImportResult> = {}): PackageImportResult {
  const result: PackageImportResult = {
    mode: 'new',
    created: [
      { type: 'agent', source_id: 'a-writer', new_id: 'a-new', name: 'Writer (imported)', workspace_id: 'ws-marketing' },
    ],
    warnings: [],
    needs_secrets: [],
    activation: 'active',
    ...overrides,
  }
  PackageImportResultSchema.parse(result)
  return result
}

function makeProductionQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: mutationRetryDelay } } })
}

function renderResult(result: PackageImportResult) {
  render(
    <QueryClientProvider client={makeProductionQueryClient()}>
      <ImportResultStep
        result={result}
        workspaceName="Marketing"
        onOpenTeam={onOpenTeamSpy}
        onImportAnother={onImportAnotherSpy}
        onOpenChat={onOpenChatSpy}
      />
    </QueryClientProvider>,
  )
}

const SUMMARY = 'Writer (imported) was added to Marketing.'

// GREEN CHECK P04: the exact text the BACKEND attaches for an active
// non-chat worker (pkg/gateway/rest_packages_publish.go::
// packageWorkerDelegationMessage — afterCommit sets it when the plan's agent
// is not a chat target and stays active). D4 row 2 (spec line 1511): the
// result, the activation message, and no Open chat.
const ACTIVE_WORKER_MESSAGE =
  'This agent is a worker: it is not a chat partner. It is reached by delegation once you draw a delegation line to it from a Main agent in the Team graph.'

beforeEach(() => {
  mockFetchAgent.mockReset().mockRejectedValue(new Error('lookup not seeded for this row'))
  mockInvalidate.mockReset()
  onOpenTeamSpy.mockReset()
  onImportAnotherSpy.mockReset()
  onOpenChatSpy.mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('Import succeeds (US-4.4)', () => {
  it('shows "<final name> was added to <workspace>." at once with Open Team and Import another', async () => {
    mockFetchAgent.mockResolvedValue(agentFixture('Main'))
    renderResult(validResult())
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Import another' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Open Team' }))
    expect(onOpenTeamSpy).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Import another' }))
    expect(onImportAnotherSpy).toHaveBeenCalledTimes(1)
  })

  it('shows the server warning message and lists API_TOKEN under "Enter again after import"', () => {
    mockFetchAgent.mockResolvedValue(agentFixture('Main'))
    renderResult(
      validResult({
        warnings: [{ code: 'publish_not_durable', message: 'The schedule will not survive a restart.' }],
        needs_secrets: [{ name: 'API_TOKEN', used_by: 'external tool', kind: 'executor_env' }],
      }),
    )
    expect(screen.getByText('The schedule will not survive a restart.')).toBeInTheDocument()
    expect(screen.getByText('Enter again after import')).toBeInTheDocument()
    expect(screen.getByText('API_TOKEN')).toBeInTheDocument()
  })

  it('the lists refresh: invalidateAfterAgentCreate is called exactly once, with the QueryClient', () => {
    mockFetchAgent.mockResolvedValue(agentFixture('Main'))
    renderResult(validResult())
    expect(mockInvalidate).toHaveBeenCalledTimes(1)
    expect(mockInvalidate.mock.calls[0][0]).toBeInstanceOf(QueryClient)
  })
})

describe('Chat action decided by the agent lookup (FR-023, D4-1…4)', () => {
  it('active + Main: "Open chat" appears and opens that agent chat (onOpenChat with new_id)', async () => {
    mockFetchAgent.mockResolvedValue(agentFixture('Main'))
    renderResult(validResult())
    expect(mockFetchAgent).toHaveBeenCalledWith('a-new')
    const chat = await screen.findByRole('button', { name: 'Open chat' })
    fireEvent.click(chat)
    expect(onOpenChatSpy).toHaveBeenCalledTimes(1)
    expect(onOpenChatSpy).toHaveBeenCalledWith('a-new')
  })

  it('active + Subagent: no chat action, no failure text, rest of the result intact', async () => {
    mockFetchAgent.mockResolvedValue(agentFixture('Subagent'))
    renderResult(validResult())
    await waitFor(() => expect(mockFetchAgent).toHaveBeenCalled())
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    expect(screen.queryByText('Open it from the Team.')).toBeNull()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
  })

  it('active + subagent_3p: no chat action', async () => {
    mockFetchAgent.mockResolvedValue(agentFixture('subagent_3p'))
    renderResult(validResult())
    await waitFor(() => expect(mockFetchAgent).toHaveBeenCalled())
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    expect(screen.queryByText('Open it from the Team.')).toBeNull()
  })

  it('active + subagent_3p: the server\'s worker message shows and "Open chat" stays absent (GREEN CHECK P04, D4 row 2)', async () => {
    mockFetchAgent.mockResolvedValue(agentFixture('subagent_3p'))
    // The backend attaches this top-level message for an active worker
    // (rest_packages_publish.go::packageWorkerDelegationMessage); the Result
    // must show it — not swallow it behind `!active` — and never offer Open
    // chat for a delegation-only agent.
    renderResult(validResult({ activation: 'active', activation_message: ACTIVE_WORKER_MESSAGE }))
    await waitFor(() => expect(mockFetchAgent).toHaveBeenCalled())
    expect(screen.getByText(ACTIVE_WORKER_MESSAGE), 'the server\'s explanation of how to use the worker').toBeInTheDocument()
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Import another' })).toBeInTheDocument()
  })

  it('pending: the top-level activation_message shows in a notice, "Open chat" absent, agent never looked up', () => {
    renderResult(validResult({ activation: 'pending', activation_message: 'The agent starts on its next heartbeat.' }))
    expect(screen.getByText('The agent starts on its next heartbeat.')).toBeInTheDocument()
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    expect(mockFetchAgent).not.toHaveBeenCalled()
    expect(mockInvalidate).toHaveBeenCalledTimes(1)
  })

  it('lookup still loading: the result is shown at once and a busy placeholder stands in for the chat action', async () => {
    mockFetchAgent.mockReturnValue(new Promise(() => {}))
    renderResult(validResult())
    expect(mockFetchAgent).toHaveBeenCalledWith('a-new')
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    // Scoped to the named busy placeholder (dispute D-1, verified): the
    // spec shows the REST of the result at once, and every catalogued
    // Button portals its own empty role=status announcement — so an
    // unscoped getByRole('status') throws on the placeholder's own
    // announcement regions' siblings. The NAME keeps the row's teeth: no
    // placeholder, no match, and this row still dies.
    expect(
      screen.getByRole('status', { name: 'Checking the new agent' }),
      'a busy placeholder while the type is looked up',
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    expect(screen.queryByText('Open it from the Team.')).toBeNull()
  })

  it('lookup 404: the result is kept and "Open it from the Team." replaces the action', async () => {
    mockFetchAgent.mockRejectedValue(Object.assign(new Error('not found'), { status: 404 }))
    renderResult(validResult())
    expect(await screen.findByText('Open it from the Team.')).toBeInTheDocument()
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Open Team' })).toBeInTheDocument()
  })

  it('lookup network failure: the same replacement', async () => {
    mockFetchAgent.mockRejectedValue(new TypeError('network down'))
    renderResult(validResult())
    expect(await screen.findByText('Open it from the Team.')).toBeInTheDocument()
    expect(screen.getByText(SUMMARY)).toBeInTheDocument()
  })
})
