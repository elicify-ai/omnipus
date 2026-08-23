// AgentProfile.commandPreview.test.tsx — the Runtime tab's executor block
// now shows a LIVE preview of the REAL command Omnipus would run for the
// agent's actual current settings (`CommandPreview`, backed by
// `POST /agents/executor-preview`), replacing the old generic, static
// "Automatically applied by Omnipus" flag-list block. This proves the edit
// form renders the real `command_line` for the agent's current
// cli/model/cli_path/cli_args, that it live-updates when the operator edits
// those fields, that dropped_args/model_dropped_reason surface as visible
// warnings BEFORE saving, and that a fetch failure degrades soft with a
// retry affordance rather than disappearing.
//
// Follows the same rendering/mocking pattern as the file this replaces
// (`AgentProfile.autoAppliedFlags.test.tsx` — Agent System P0 fix) and
// `AgentProfile.cliPathValidate.test.tsx`. Real timers (not fake) — matches
// `Step1Identity.prefillValidate.test.tsx`'s approach to the same 400ms
// debounce shape, generous `waitFor` timeouts absorb it.

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
    fetchExecutorPreview: vi.fn(),
    fetchExecutorSmokeTest: vi.fn(),
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
  fetchExecutorPreview,
  fetchExecutorSmokeTest,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import type { CliDetect, CliValidateResponse, ExecutorCommandPreviewResponse } from '@/lib/api'

const mockExternalAgent: Agent = {
  id: 'external-worker',
  name: 'External Worker',
  type: 'subagent_3p',
  locked: false,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'Delegates to an external CLI',
  soul: 'You are a focused delegate.',
  timeout_seconds: 60,
  max_tool_iterations: 20,
  rate_limits: { use_global_defaults: true },
  executor: { kind: 'external-cli', cli: 'claude-code' },
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
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

function previewResponse(overrides: Partial<ExecutorCommandPreviewResponse> = {}): ExecutorCommandPreviewResponse {
  return {
    binary: 'claude',
    argv: ['-p', '--output-format', 'stream-json', '--model', 'claude-sonnet-4-6'],
    command_line: 'claude -p --output-format stream-json --model claude-sonnet-4-6',
    prompt_delivery: 'stdin',
    dropped_args: [],
    ...overrides,
  }
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
  vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue(previewResponse())
  vi.mocked(fetchExecutorSmokeTest).mockReset()
  useUiStore.setState({ toasts: [] })
})

describe('AgentProfile — Runtime tab live command preview (executor-command-preview)', () => {
  it('renders the REAL computed command_line for the agent\'s current executor settings', async () => {
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })
    expect(screen.getByTestId('profile-command-preview-command-line')).toHaveTextContent(
      'claude -p --output-format stream-json --model claude-sonnet-4-6',
    )
    // Sent with the agent's real settings, not a hand-maintained description.
    const call = vi.mocked(fetchExecutorPreview).mock.calls[0][0]
    expect(call.cli).toBe('claude-code')
    expect(call.model).toBe('claude-sonnet-4-6')
    // subagent_3p excludes max_tool_iterations from the wire payload
    // entirely (agent-types-field-matrix.md Decisions #1) — the preview
    // must mirror that, not send the agent's unrelated max_tool_iterations.
    expect(call.max_tool_iterations).toBeUndefined()
  })

  it('is visually and interactively read-only — no input/checkbox inside the block', async () => {
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })
    const block = screen.getByTestId('profile-command-preview')
    expect(block.querySelectorAll('input, textarea, [contenteditable]')).toHaveLength(0)
  })

  it('live-updates the preview when the operator edits Additional CLI arguments', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/local/bin/claude' },
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')
    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })

    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      previewResponse({ command_line: 'claude -p --output-format stream-json --model claude-sonnet-4-6 --add-dir /extra/path' }),
    )
    const argsInput = screen.getByTestId('profile-cli-args').querySelector('input') as HTMLInputElement
    fireEvent.change(argsInput, { target: { value: '--add-dir /extra/path' } })

    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview-command-line')).toHaveTextContent('--add-dir /extra/path')
    }, { timeout: 2000 })
    // Editing the preview never mutates the field itself — it still reflects
    // exactly what the operator typed.
    expect(argsInput.value).toBe('--add-dir /extra/path')
  })

  it('renders dropped_args as a visible warning before the operator saves', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      previewResponse({
        dropped_args: [{ flag: '--dangerously-skip-permissions', reason: 'conflicts with the enforced permission mode' }],
      }),
    )
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview-dropped-args')).toHaveTextContent('--dangerously-skip-permissions')
      expect(screen.getByTestId('profile-command-preview-dropped-args')).toHaveTextContent(/conflicts with the enforced permission mode/i)
    }, { timeout: 2000 })
  })

  it('renders model_dropped_reason as a visible warning', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      previewResponse({ model_dropped_reason: 'opencode requires a "provider/model" shape.' }),
    )
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview-model-dropped-reason')).toHaveTextContent(/provider\/model.*shape/i)
    }, { timeout: 2000 })
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
    expect(argsInput.placeholder).not.toContain('flags shown above')
    expect(screen.getByText('Additional CLI arguments')).toBeInTheDocument()
  })

  it('fails soft with a visible, retryable degraded state (never silently omits the block) when fetchExecutorPreview rejects', async () => {
    vi.mocked(fetchExecutorPreview).mockReset().mockRejectedValue(new Error('network down'))
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      const block = screen.getByTestId('profile-command-preview')
      expect(block).toHaveAttribute('data-preview-status', 'error')
      expect(block).toHaveTextContent(/couldn't compute a live command preview/i)
    }, { timeout: 2000 })
    const argsInput = screen.getByTestId('profile-cli-args').querySelector('input') as HTMLInputElement
    expect(argsInput).toBeInTheDocument()
    fireEvent.change(argsInput, { target: { value: '--foo' } })
    expect(argsInput.value).toBe('--foo')

    // The edit itself reactively re-triggers the (still-rejecting) preview
    // fetch — let that settle back to 'error' before exercising the manual
    // Retry affordance, so it's actually present in the DOM to click.
    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'error')
    }, { timeout: 2000 })

    vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue(previewResponse())
    fireEvent.click(screen.getByTestId('profile-command-preview-retry'))
    await waitFor(() => {
      const block = screen.getByTestId('profile-command-preview')
      expect(block).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })
  })
})

describe('AgentProfile — smoke test runs in this real, saved agent\'s own workspace', () => {
  it('sends the real, saved agent_id in the smoke-test request (not the ephemeral-scratch-dir path)', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue({
      ok: true,
      response_text: '2',
      duration_ms: 1800,
      used_agent_workspace: true,
    })
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })

    fireEvent.click(screen.getByTestId('profile-command-preview-smoke-test-button'))

    await waitFor(() => expect(fetchExecutorSmokeTest).toHaveBeenCalled(), { timeout: 2000 })
    const call = vi.mocked(fetchExecutorSmokeTest).mock.calls[0][0]
    // 'external-worker' is mockExternalAgent's real id (renderProfile's arg) —
    // proves AgentProfile threads its OWN saved agentId through, not a
    // hand-typed or missing value.
    expect(call.agent_id).toBe('external-worker')

    await waitFor(() => {
      expect(screen.getByTestId('profile-command-preview-smoke-test-success-workspace-note')).toHaveTextContent(
        /this agent's own folder/i,
      )
    }, { timeout: 2000 })
  })
})
