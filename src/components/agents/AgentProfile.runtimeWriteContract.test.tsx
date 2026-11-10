/**
 * C04 oracle: T11 FR-8 / U-5 and ExecutorConfigUpdate.yaml, not the save
 * implementation. READ executor fields and masked values are never a WRITE
 * payload. The real form, editors, hooks and autosave remain under test;
 * routing context and API transport are the only mocked boundaries.
 */
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Agent, AgentUpdateRequest, ExecutorConfigUpdate } from '@/lib/api/generated/openapi-types'
import { AgentProfile } from './AgentProfile'

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...rest}>{children}</a>
    ),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgent: vi.fn(),
    fetchWorkspace: vi.fn(),
    updateAgent: vi.fn(),
    updateWorkspace: vi.fn(),
    deleteAgent: vi.fn(),
    fetchSkills: vi.fn(),
    fetchProviders: vi.fn(),
    testAgentRunner: vi.fn(),
    fetchCliDetect: vi.fn(),
    fetchCliValidate: vi.fn(),
    fetchExecutorPreview: vi.fn(),
    fetchExecutorSmokeTest: vi.fn(),
  }
})

import {
  fetchAgent, fetchSkills, fetchProviders, updateAgent, testAgentRunner,
  fetchCliDetect, fetchExecutorPreview,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'

// ExecutorConfig READ contract: EVERY stored environment value is masked.
const MASK = '••••••'
const SAVED_PROGRAM = '/opt/t11/codex'
const PERSON_PRESET = '--dangerously-bypass-approvals-and-sandbox'
const ORIGINAL_ARGS = '--max-turns 5'

function externalWorker(cli: NonNullable<Agent['executor']>['cli'] = 'codex'): Agent {
  return {
    revision: '0'.repeat(64),
    id: 'c04-worker',
    name: 'C04 worker',
    type: 'subagent_3p',
    locked: false,
    needs_model: false,
    status: 'active',
    model: 'fixture-model',
    description: 'C04 Runtime write contract',
    soul: '',
    memory_enabled: true,
    max_tool_iterations: 20,
    max_tool_iterations_source: 'global',
    max_tool_iterations_override_ignored: false,
    figure: 'Omnipus',
    role: 'general',
    editable_fields: [{ name: 'executor', editable: true }, { name: 'name', editable: true }],
    executor: {
      kind: 'external-cli',
      cli,
      effective_cli_path: SAVED_PROGRAM,
      effective_preset_args: PERSON_PRESET,
      env_overrides: { T11_EDIT: MASK, T11_KEEP: MASK, T11_REMOVE: MASK },
      cli_args: ORIGINAL_ARGS,
    },
  }
}

async function openRuntime(agent = externalWorker()) {
  vi.mocked(fetchAgent).mockResolvedValue(agent)
  vi.mocked(updateAgent).mockResolvedValue(agent)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(<QueryClientProvider client={client}><AgentProfile agentId={agent.id} /></QueryClientProvider>)
  await screen.findByText(agent.name)
  const tab = screen.getByTestId('tab-runtime')
  act(() => { tab.focus() })
  fireEvent.keyDown(tab, { key: 'Enter' })
  fireEvent.click(tab)
}

function argumentsInput() {
  return within(screen.getByTestId('profile-cli-args')).getByRole('textbox')
}

function environmentRow(name: string) {
  const key = screen.getByDisplayValue(name)
  const row = key.closest('[data-testid^="profile-env-row-"]')
  expect(row, `SETUP: the real environment row for ${name} must exist`).not.toBeNull()
  return within(row as HTMLElement)
}

async function savedExecutor(): Promise<NonNullable<AgentUpdateRequest['executor']>> {
  // Existing AgentProfile tests use this observation window for autosave;
  // it is not a new timeout or a substitute for observing the actual call.
  await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
  const [id, payload] = vi.mocked(updateAgent).mock.calls[0]
  expect(id).toBe('c04-worker')
  expect(payload.executor, 'the edit must produce an executor update').toBeDefined()
  return payload.executor!
}

function expectWriteDelta(actual: ExecutorConfigUpdate, expected: Required<ExecutorConfigUpdate>) {
  // The contract has no required keys: omitted map/list are documented no-ops,
  // and omitted arguments preserve the old string. Do not require redundant
  // optional keys, but reject EVERY key outside the closed WRITE schema.
  expect.soft(Object.keys(actual).filter(key => !['env_overrides', 'env_overrides_remove', 'cli_args'].includes(key)),
    'ExecutorConfigUpdate rejects kind, cli, cli_path, effective_* and all other READ fields').toEqual([])
  expect.soft({
    env_overrides: actual.env_overrides ?? {},
    env_overrides_remove: actual.env_overrides_remove ?? [],
    cli_args: actual.cli_args ?? ORIGINAL_ARGS,
  }, 'send only the changed environment values, explicit removals and intended arguments').toEqual(expected)
  expect.soft(JSON.stringify(actual), 'an untouched READ mask must never be sent back').not.toContain(MASK)
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgent).mockReset()
  vi.mocked(updateAgent).mockReset()
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(testAgentRunner).mockReset().mockResolvedValue({ ok: true, reason: '', message: 'ready', cli: 'codex' })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue({
    claude: { installed: false, path: null, source: null },
    codex: { installed: false, path: null, source: null },
    opencode: { installed: false, path: null, source: null },
  })
  vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue({
    binary: SAVED_PROGRAM, argv: [], command_line: 'fixture preview', prompt_delivery: 'stdin', dropped_args: [],
  })
  useUiStore.setState({ toasts: [] })
})

describe('C04 Runtime uses the executor WRITE contract (T11 FR-8 / U-5)', () => {
  it.each(['claude-code', 'codex', 'opencode'] as const)('enables env and arguments for an editable %s worker', async cli => {
    await openRuntime(externalWorker(cli))
    expect(argumentsInput()).toBeEnabled()
    expect(screen.getByTestId('profile-env-add')).toBeEnabled()
    for (const name of ['T11_EDIT', 'T11_KEEP', 'T11_REMOVE']) {
      const row = environmentRow(name)
      expect(row.getByLabelText('Environment variable name')).toBeEnabled()
      expect(row.getByLabelText('Environment variable value')).toBeEnabled()
    }
  })

  it('saves one changed env value without resending the other masked values or READ fields', async () => {
    await openRuntime()
    const value = environmentRow('T11_EDIT').getByLabelText('Environment variable value')
    expect(value).toBeEnabled()
    fireEvent.change(value, { target: { value: 'new-c04-value' } })
    fireEvent.blur(value)
    expect(value).toHaveValue('new-c04-value')
    expectWriteDelta(await savedExecutor(), {
      env_overrides: { T11_EDIT: 'new-c04-value' }, env_overrides_remove: [], cli_args: ORIGINAL_ARGS,
    })
  })

  it('saves exact additional arguments with no untouched masked environment values', async () => {
    await openRuntime()
    const input = argumentsInput()
    expect(input).toBeEnabled()
    fireEvent.change(input, { target: { value: '--max-turns 7' } })
    fireEvent.blur(input)
    expect(input).toHaveValue('--max-turns 7')
    expectWriteDelta(await savedExecutor(), {
      env_overrides: {}, env_overrides_remove: [], cli_args: '--max-turns 7',
    })
  })

  it('sends a removed stored name in env_overrides_remove, never an empty merge pretending to delete it', async () => {
    await openRuntime()
    const remove = environmentRow('T11_REMOVE').getByRole('button', { name: /remove/i })
    expect(remove).toBeEnabled()
    fireEvent.click(remove)
    expect(screen.queryByDisplayValue('T11_REMOVE')).not.toBeInTheDocument()
    expectWriteDelta(await savedExecutor(), {
      env_overrides: {}, env_overrides_remove: ['T11_REMOVE'], cli_args: ORIGINAL_ARGS,
    })
  })

  it('adds only a newly entered env value without overwriting stored masked values', async () => {
    await openRuntime()
    const add = screen.getByTestId('profile-env-add')
    expect(add).toBeEnabled()
    fireEvent.click(add)
    const row = within(screen.getByTestId('profile-env-row-3'))
    const key = row.getByLabelText('Environment variable name')
    const value = row.getByLabelText('Environment variable value')
    expect(key).toBeEnabled()
    expect(value).toBeEnabled()
    fireEvent.change(key, { target: { value: 'T11_ADDED' } })
    fireEvent.blur(key)
    fireEvent.change(value, { target: { value: 'added-c04-value' } })
    fireEvent.blur(value)
    expect(key).toHaveValue('T11_ADDED')
    expect(value).toHaveValue('added-c04-value')
    expectWriteDelta(await savedExecutor(), {
      env_overrides: { T11_ADDED: 'added-c04-value' }, env_overrides_remove: [], cli_args: ORIGINAL_ARGS,
    })
  })

  it('keeps the saved program path and person parameters visible and read-only', async () => {
    await openRuntime()
    const program = screen.getByTestId('profile-cli-path')
    expect(screen.getByTestId('profile-program-path')).toHaveTextContent(SAVED_PROGRAM)
    expect(program).toHaveTextContent(PERSON_PRESET)
    expect(program.querySelectorAll('input, textarea, [contenteditable]')).toHaveLength(0)
  })

  it('never exposes enabled environment or argument controls for a native worker', async () => {
    const agent: Agent = {
      ...externalWorker(), type: 'Subagent', needs_model: true,
      executor: { kind: 'native' },
      editable_fields: [{ name: 'executor', editable: false, reason: 'Only external-CLI workers have a runtime environment.' }],
    }
    vi.mocked(fetchAgent).mockResolvedValue(agent)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    render(<QueryClientProvider client={client}><AgentProfile agentId={agent.id} /></QueryClientProvider>)
    await screen.findByText(agent.name)
    const runtime = screen.queryByTestId('tab-runtime')
    // The native field matrix has no Runtime tab. If a layout does render
    // one, the dispatch still requires its executor controls to be disabled.
    if (runtime) {
      act(() => { runtime.focus() })
      fireEvent.keyDown(runtime, { key: 'Enter' })
      fireEvent.click(runtime)
      expect(screen.getByTestId('profile-env-add')).toBeDisabled()
      expect(argumentsInput()).toBeDisabled()
    } else {
      expect(screen.queryByTestId('profile-env-overrides')).not.toBeInTheDocument()
      expect(screen.queryByTestId('profile-env-add')).not.toBeInTheDocument()
      expect(screen.queryByTestId('profile-cli-args')).not.toBeInTheDocument()
    }
    expect(updateAgent).not.toHaveBeenCalled()
  })
})
