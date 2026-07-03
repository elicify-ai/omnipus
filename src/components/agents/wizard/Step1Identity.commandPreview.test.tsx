// Step1Identity.commandPreview.test.tsx — the create wizard's subagent_3p
// executor block now shows a LIVE preview of the REAL command Omnipus would
// run for the operator's current in-progress settings (`CommandPreview`,
// backed by `POST /agents/executor-preview`), replacing the old generic,
// static "Automatically applied by Omnipus" flag-list block. This proves the
// wizard renders the real `command_line` for the flat `WizardSubmitPayload`
// (no persisted agent id exists yet — the endpoint is stateless/body-driven,
// mirroring `POST /system/cli-validate`), that it live-updates as the
// operator types/switches CLI, that dropped_args/model_dropped_reason
// surface as visible warnings BEFORE Create, and that a fetch failure
// degrades soft with a retry affordance.
//
// Driven through the full `CreateAgentWizard` (the `ExecutorInputs`
// sub-component that owns this block only mounts inside a subagent_3p
// wizard flow), following the same pattern as the file this replaces
// (`Step1Identity.autoAppliedFlags.test.tsx`) and
// `Step1Identity.prefillValidate.test.tsx`. Real timers, generous `waitFor`
// timeouts absorb the 400ms debounce.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { CreateAgentWizard } from '../CreateAgentWizard'
import { useUiStore } from '@/store/ui'
import { act } from 'react'
import { fetchCliDetect, fetchCliValidate, fetchExecutorPreview } from '@/lib/api'
import type { CliDetect, CliValidateResponse, ExecutorCommandPreviewResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchCliDetect: vi.fn(),
    fetchCliValidate: vi.fn(),
    fetchExecutorPreview: vi.fn(),
  }
})

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
    argv: ['-p', '--output-format', 'stream-json'],
    command_line: 'claude -p --output-format stream-json',
    prompt_delivery: 'stdin',
    dropped_args: [],
    ...overrides,
  }
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false, sessionPanelOpen: false, toasts: [] })
  })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue(detect())
  vi.mocked(fetchCliValidate).mockReset().mockResolvedValue(validateResult())
  vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue(previewResponse())
})

function renderWizard(props: Partial<Parameters<typeof CreateAgentWizard>[0]> = {}) {
  const onSubmit = props.onSubmit ?? vi.fn().mockResolvedValue(undefined)
  const onClose = props.onClose ?? vi.fn()
  return render(
    <CreateAgentWizard
      initialType={props.initialType ?? 'subagent_3p'}
      {...(props.initialCli ? { initialCli: props.initialCli } : {})}
      onSubmit={onSubmit}
      onClose={onClose}
    />,
  )
}

describe('Step1Identity / ExecutorInputs — live command preview (executor-command-preview)', () => {
  it('renders the REAL computed command_line for the locked-in CLI', async () => {
    renderWizard({ initialCli: 'claude-code' })

    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })
    expect(screen.getByTestId('wizard-command-preview-command-line')).toHaveTextContent(
      'claude -p --output-format stream-json',
    )
    expect(vi.mocked(fetchExecutorPreview).mock.calls[0][0].cli).toBe('claude-code')
  })

  it('is visually and interactively read-only — no input/textarea inside the block', async () => {
    renderWizard({ initialCli: 'claude-code' })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })

    const block = screen.getByTestId('wizard-command-preview')
    expect(block.querySelectorAll('input, textarea, [contenteditable]')).toHaveLength(0)
  })

  it('renders nothing until a CLI is chosen, then live-updates when the operator switches CLI', async () => {
    renderWizard({}) // no initialCli -> CLI chooser is rendered, nothing locked

    expect(screen.getByTestId('wizard-cli-chooser')).toBeInTheDocument()
    expect(screen.queryByTestId('wizard-command-preview')).not.toBeInTheDocument()

    vi.mocked(fetchExecutorPreview).mockResolvedValue(previewResponse({ command_line: 'claude -p --output-format stream-json' }))
    fireEvent.click(screen.getByTestId('wizard-cli-claude-code'))
    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview-command-line')).toHaveTextContent('claude -p --output-format stream-json')
    }, { timeout: 2000 })

    vi.mocked(fetchExecutorPreview).mockResolvedValue(previewResponse({ command_line: 'codex exec --sandbox workspace-write' }))
    fireEvent.click(screen.getByTestId('wizard-cli-codex'))
    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview-command-line')).toHaveTextContent('codex exec --sandbox workspace-write')
    }, { timeout: 2000 })
  })

  it('live-updates the preview as the operator types Additional CLI arguments', async () => {
    renderWizard({ initialCli: 'claude-code' })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview')).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })

    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      previewResponse({ command_line: 'claude -p --output-format stream-json --add-dir /extra/path' }),
    )
    fireEvent.change(screen.getByTestId('wizard-cli-args'), { target: { value: '--add-dir /extra/path' } })

    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview-command-line')).toHaveTextContent('--add-dir /extra/path')
    }, { timeout: 2000 })
    expect(screen.getByTestId('wizard-cli-args')).toHaveValue('--add-dir /extra/path')
  })

  it('renders dropped_args as a visible warning before the operator can Create', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      previewResponse({
        dropped_args: [{ flag: '--resume', reason: 'every run starts a fresh session' }],
      }),
    )
    renderWizard({ initialCli: 'claude-code' })

    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview-dropped-args')).toHaveTextContent('--resume')
      expect(screen.getByTestId('wizard-command-preview-dropped-args')).toHaveTextContent(/every run starts a fresh session/i)
    }, { timeout: 2000 })
  })

  it('renders model_dropped_reason as a visible warning', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      previewResponse({ model_dropped_reason: 'opencode requires a "provider/model" shape.' }),
    )
    renderWizard({ initialCli: 'opencode' })

    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview-model-dropped-reason')).toHaveTextContent(/provider\/model.*shape/i)
    }, { timeout: 2000 })
  })

  it('the "Additional CLI arguments" field still submits exactly what the operator typed', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    renderWizard({ initialCli: 'claude-code', onSubmit })

    fireEvent.change(screen.getByTestId('wizard-cli-args'), { target: { value: '--add-dir /extra/path' } })
    expect(screen.getByTestId('wizard-cli-args')).toHaveValue('--add-dir /extra/path')

    // Drive the wizard to Create and confirm the exact typed string is what
    // gets submitted — the command-preview block never mutates this field.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'External Worker' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'delegates to an external CLI' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'claude-sonnet-4-6' } })
    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })
    await waitFor(() => expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled(), { timeout: 2000 })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'You are focused.' } })
    // subagent_3p wizards are TWO steps (Identity, Personality — tools/
    // skills/fallback policy never apply to an external runner), so
    // Personality is the LAST step: the footer button here is "Create",
    // not a "Next" to a 3rd step.
    await waitFor(() => expect(screen.getByTestId('wizard-create')).not.toBeDisabled(), { timeout: 2000 })
    fireEvent.click(screen.getByTestId('wizard-create'))

    await waitFor(() => expect(onSubmit).toHaveBeenCalled(), { timeout: 2000 })
    const payload = onSubmit.mock.calls[0][0] as { executor_cli_args?: string }
    expect(payload.executor_cli_args).toBe('--add-dir /extra/path')
  })

  it('the "Additional CLI arguments" field no longer shows the old misleading placeholder examples', () => {
    renderWizard({ initialCli: 'claude-code' })

    const input = screen.getByTestId('wizard-cli-args') as HTMLInputElement
    expect(input.placeholder).not.toContain('--verbose --output json')
    expect(input.placeholder).not.toContain('--no-update-check')
    expect(input.placeholder).not.toContain('flags shown above')
    expect(screen.getByText('Additional CLI arguments')).toBeInTheDocument()
    expect(screen.queryByText('CLI arguments', { exact: true })).not.toBeInTheDocument()
  })

  it('fails soft with a visible, retryable degraded state (never silently omits the block) when fetchExecutorPreview rejects', async () => {
    vi.mocked(fetchExecutorPreview).mockReset().mockRejectedValue(new Error('network down'))
    renderWizard({ initialCli: 'claude-code' })

    await waitFor(() => expect(fetchExecutorPreview).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      const block = screen.getByTestId('wizard-command-preview')
      expect(block).toHaveAttribute('data-preview-status', 'error')
      expect(block).toHaveTextContent(/couldn't compute a live command preview/i)
    }, { timeout: 2000 })
    // The rest of the form remains usable.
    expect(screen.getByTestId('wizard-cli-args')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('wizard-cli-args'), { target: { value: '--foo' } })
    expect(screen.getByTestId('wizard-cli-args')).toHaveValue('--foo')

    // The edit itself reactively re-triggers the (still-rejecting) preview
    // fetch — let that settle back to 'error' before exercising the manual
    // Retry affordance, so it's actually present in the DOM to click.
    await waitFor(() => {
      expect(screen.getByTestId('wizard-command-preview')).toHaveAttribute('data-preview-status', 'error')
    }, { timeout: 2000 })

    // Retry affordance re-triggers the fetch.
    vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue(previewResponse())
    fireEvent.click(screen.getByTestId('wizard-command-preview-retry'))
    await waitFor(() => {
      const block = screen.getByTestId('wizard-command-preview')
      expect(block).toHaveAttribute('data-preview-status', 'ready')
    }, { timeout: 2000 })
  })
})
