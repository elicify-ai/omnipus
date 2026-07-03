// AgentProfile.autoAppliedFlags.test.tsx — Agent System P0 fix: the Runtime
// tab's "CLI arguments" field previously showed the real auto-applied flags
// only as vanishing HTML `placeholder` ghost-text (e.g. "--no-update-check")
// that never matched what Omnipus actually sent. This proves the edit form
// now shows the REAL flags in a read-only "Automatically applied by Omnipus"
// info block (fetched from `fetchExecutorDefaults`, keyed on the agent's
// locked `executor.cli`), that the (relabeled) "Additional CLI arguments"
// field still submits exactly what the operator typed, and that the
// misleading old placeholder text is gone.
//
// Follows the same rendering/mocking pattern as
// `AgentProfile.cliPathValidate.test.tsx`.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import type { Agent } from '@/lib/api'

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
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
    fetchExecutorDefaults: vi.fn(),
  }
})

import {
  fetchAgent,
  fetchSkills,
  fetchProviders,
  updateAgent,
  testAgentRunner,
  fetchCliDetect,
  fetchCliValidate,
  fetchExecutorDefaults,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import type { CliDetect, CliValidateResponse, ExecutorDefaults } from '@/lib/api'

const mockExternalAgent: Agent = {
  id: 'external-worker',
  name: 'External Worker',
  type: 'subagent_3p',
  locked: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'Delegates to an external CLI',
  soul: 'You are a focused delegate.',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  steering_mode: 'one-at-a-time',
  rate_limits: { use_global_defaults: true },
  executor: { kind: 'external-cli', cli: 'claude-code' },
}

function detect(overrides: Partial<CliDetect> = {}): CliDetect {
  return {
    claude: { installed: true, path: '/usr/local/bin/claude', source: 'path' },
    codex: { installed: false, path: null, source: null },
    opencode: { installed: true, path: '/home/dev/.local/bin/opencode', source: 'well-known' },
    ...overrides,
  }
}

function validateResult(overrides: Partial<CliValidateResponse> = {}): CliValidateResponse {
  return { ok: true, reason: 'ok', resolved_path: '/usr/local/bin/claude', version: '1.2.3', detail: 'OK', ...overrides }
}

// GET /agents/executor-defaults is static, unfiltered reference data — one
// entry per supported CLI, always returned together (never queried per-CLI).
function defaultsList(): ExecutorDefaults[] {
  return [
    {
      cli: 'claude-code',
      auto_applied_flags: ['--output-format stream-json', '--permission-mode acceptEdits'],
      notes: 'claude never receives --dangerously-skip-permissions.',
    },
    {
      cli: 'codex',
      auto_applied_flags: ['--sandbox workspace-write', '--ask-for-approval never'],
      notes: 'codex runs fully headless — approval prompts are never issued.',
    },
    {
      cli: 'opencode',
      auto_applied_flags: ['--dangerously-skip-permissions'],
      notes: 'opencode auto-approves edits; there is no middle-ground flag.',
    },
  ]
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(agentId: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentProfile agentId={agentId} />
    </QueryClientProvider>,
  )
}

function switchTab(testId: string) {
  const trigger = screen.getByTestId(testId)
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockExternalAgent)
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(updateAgent).mockReset().mockResolvedValue(mockExternalAgent)
  vi.mocked(testAgentRunner).mockReset().mockResolvedValue({ ok: true, reason: '', message: 'ready', cli: 'claude-code' })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue(detect())
  vi.mocked(fetchCliValidate).mockReset().mockResolvedValue(validateResult())
  vi.mocked(fetchExecutorDefaults).mockReset().mockResolvedValue(defaultsList())
  useUiStore.setState({ toasts: [] })
})

describe('AgentProfile — Runtime tab auto-applied flags block (Agent System P0)', () => {
  it('renders the fetched auto-applied flags for the agent\'s locked executor CLI', async () => {
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => expect(fetchExecutorDefaults).toHaveBeenCalled())
    await waitFor(() => {
      expect(screen.getByTestId('profile-executor-defaults')).toHaveTextContent('--output-format stream-json')
    })
    const block = screen.getByTestId('profile-executor-defaults')
    expect(block).toHaveTextContent('Automatically applied by Omnipus')
    expect(block).toHaveTextContent('--permission-mode acceptEdits')
    expect(block).toHaveTextContent(/never receives --dangerously-skip-permissions/i)
    // Only the agent's locked executor CLI entry is shown, not the others.
    expect(block).not.toHaveTextContent('--ask-for-approval never')
  })

  it('is visually and interactively read-only — no input/checkbox inside the block', async () => {
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => {
      expect(screen.getByTestId('profile-executor-defaults')).toHaveTextContent('--output-format stream-json')
    })
    const block = screen.getByTestId('profile-executor-defaults')
    expect(block.querySelectorAll('input, textarea, button, [contenteditable]')).toHaveLength(0)
  })

  it('updates the shown list when the agent\'s executor CLI is codex', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'codex' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => expect(fetchExecutorDefaults).toHaveBeenCalled())
    await waitFor(() => {
      const block = screen.getByTestId('profile-executor-defaults')
      expect(block).toHaveTextContent('--ask-for-approval never')
      expect(block).not.toHaveTextContent('--permission-mode acceptEdits')
    })
  })

  it('the "Additional CLI arguments" field still submits exactly what the operator typed', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/local/bin/claude' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const argsInput = screen.getByTestId('profile-cli-args').querySelector('input') as HTMLInputElement
    fireEvent.change(argsInput, { target: { value: '--add-dir /extra/path' } })
    expect(argsInput.value).toBe('--add-dir /extra/path')
    fireEvent.blur(argsInput)

    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
    const payload = vi.mocked(updateAgent).mock.calls[0][1] as { executor?: { cli_args?: string } }
    expect(payload.executor?.cli_args).toBe('--add-dir /extra/path')
  })

  it('the "Additional CLI arguments" field no longer shows the old misleading placeholder', async () => {
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const argsInput = screen.getByTestId('profile-cli-args').querySelector('input') as HTMLInputElement
    expect(argsInput.placeholder).not.toContain('--no-update-check')
    expect(argsInput.placeholder).not.toContain('--verbose --output json')
    expect(screen.getByText('Additional CLI arguments')).toBeInTheDocument()
  })

  it('fails soft (omits the block, does not block the form) when fetchExecutorDefaults rejects', async () => {
    vi.mocked(fetchExecutorDefaults).mockReset().mockRejectedValue(new Error('network down'))
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => expect(fetchExecutorDefaults).toHaveBeenCalled())
    await waitFor(() => {
      expect(screen.queryByTestId('profile-executor-defaults')).not.toBeInTheDocument()
    })
    const argsInput = screen.getByTestId('profile-cli-args').querySelector('input') as HTMLInputElement
    expect(argsInput).toBeInTheDocument()
    fireEvent.change(argsInput, { target: { value: '--foo' } })
    expect(argsInput.value).toBe('--foo')
  })
})
