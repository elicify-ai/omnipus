// Step1Identity.autoAppliedFlags.test.tsx — Agent System P0 fix: the
// subagent_3p executor's "CLI arguments" field previously showed the real
// auto-applied flags only as vanishing HTML `placeholder` ghost-text (e.g.
// "--verbose --output json") that never matched what Omnipus actually sent.
// This proves the create wizard now shows the REAL flags in a read-only
// "Automatically applied by Omnipus" info block (fetched from
// `GET /agents/executor-defaults` via `fetchExecutorDefaults`), that the
// block updates when the operator switches CLI (client-side selection out of
// the single fetched reference list — the endpoint is static, unfiltered
// reference data per `contracts/components/schemas/ExecutorDefaults.yaml`),
// that the (relabeled) "Additional CLI arguments" field still submits
// exactly what the operator typed, and that the misleading old placeholder
// text is gone.
//
// Driven through the full `CreateAgentWizard` (the `ExecutorInputs`
// sub-component that owns this block only mounts inside a subagent_3p
// wizard flow), following the same pattern as
// `Step1Identity.prefillValidate.test.tsx`.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { CreateAgentWizard } from '../CreateAgentWizard'
import { useUiStore } from '@/store/ui'
import { act } from 'react'
import { fetchCliDetect, fetchCliValidate, fetchExecutorDefaults } from '@/lib/api'
import type { CliDetect, CliValidateResponse, ExecutorDefaults } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchCliDetect: vi.fn(),
    fetchCliValidate: vi.fn(),
    fetchExecutorDefaults: vi.fn(),
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

beforeEach(() => {
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false, sessionPanelOpen: false, toasts: [] })
  })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue(detect())
  vi.mocked(fetchCliValidate).mockReset().mockResolvedValue(validateResult())
  vi.mocked(fetchExecutorDefaults).mockReset().mockResolvedValue(defaultsList())
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

describe('Step1Identity / ExecutorInputs — auto-applied flags block (Agent System P0)', () => {
  it('renders the fetched auto-applied flags for the locked-in CLI', async () => {
    renderWizard({ initialCli: 'claude-code' })

    await waitFor(() => expect(fetchExecutorDefaults).toHaveBeenCalled())
    await waitFor(() => {
      expect(screen.getByTestId('wizard-executor-defaults')).toHaveTextContent('--output-format stream-json')
    })
    const block = screen.getByTestId('wizard-executor-defaults')
    expect(block).toHaveTextContent('Automatically applied by Omnipus')
    expect(block).toHaveTextContent('--permission-mode acceptEdits')
    expect(block).toHaveTextContent(/never receives --dangerously-skip-permissions/i)
    // Only the selected CLI's entry is shown, not the other two.
    expect(block).not.toHaveTextContent('--ask-for-approval never')
  })

  it('is visually and interactively read-only — no input/checkbox inside the block', async () => {
    renderWizard({ initialCli: 'claude-code' })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-executor-defaults')).toHaveTextContent('--output-format stream-json')
    })

    const block = screen.getByTestId('wizard-executor-defaults')
    expect(block.querySelectorAll('input, textarea, button, [contenteditable]')).toHaveLength(0)
  })

  it('updates the shown list when the operator switches CLI via the chooser', async () => {
    renderWizard({}) // no initialCli -> CLI chooser is rendered, nothing locked

    expect(screen.getByTestId('wizard-cli-chooser')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('wizard-cli-claude-code'))
    await waitFor(() => {
      expect(screen.getByTestId('wizard-executor-defaults')).toHaveTextContent('--permission-mode acceptEdits')
    })

    fireEvent.click(screen.getByTestId('wizard-cli-codex'))
    await waitFor(() => {
      const block = screen.getByTestId('wizard-executor-defaults')
      expect(block).toHaveTextContent('--ask-for-approval never')
      expect(block).not.toHaveTextContent('--permission-mode acceptEdits')
    })
  })

  it('the "Additional CLI arguments" field still submits exactly what the operator typed', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    renderWizard({ initialCli: 'claude-code', onSubmit })

    fireEvent.change(screen.getByTestId('wizard-cli-args'), { target: { value: '--add-dir /extra/path' } })
    expect(screen.getByTestId('wizard-cli-args')).toHaveValue('--add-dir /extra/path')

    // Drive the wizard to Create and confirm the exact typed string is what
    // gets submitted — the auto-applied-flags block never mutates this field.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'External Worker' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'delegates to an external CLI' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'claude-sonnet-4-6' } })
    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })
    await waitFor(() => expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'You are focused.' } })
    await waitFor(() => expect(screen.getByTestId('wizard-next-2')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    await screen.findByTestId('wizard-create')
    fireEvent.click(screen.getByTestId('wizard-create'))

    await waitFor(() => expect(onSubmit).toHaveBeenCalled())
    const payload = onSubmit.mock.calls[0][0] as { executor_cli_args?: string }
    expect(payload.executor_cli_args).toBe('--add-dir /extra/path')
  })

  it('the "Additional CLI arguments" field no longer shows the old misleading placeholder examples', () => {
    renderWizard({ initialCli: 'claude-code' })

    const input = screen.getByTestId('wizard-cli-args') as HTMLInputElement
    expect(input.placeholder).not.toContain('--verbose --output json')
    expect(input.placeholder).not.toContain('--no-update-check')
    expect(screen.getByText('Additional CLI arguments')).toBeInTheDocument()
    expect(screen.queryByText('CLI arguments', { exact: true })).not.toBeInTheDocument()
  })

  it('fails soft (omits the block, does not crash the form) when fetchExecutorDefaults rejects', async () => {
    vi.mocked(fetchExecutorDefaults).mockReset().mockRejectedValue(new Error('network down'))
    renderWizard({ initialCli: 'claude-code' })

    await waitFor(() => expect(fetchExecutorDefaults).toHaveBeenCalled())
    await waitFor(() => {
      expect(screen.queryByTestId('wizard-executor-defaults')).not.toBeInTheDocument()
    })
    // The rest of the form remains usable.
    expect(screen.getByTestId('wizard-cli-args')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('wizard-cli-args'), { target: { value: '--foo' } })
    expect(screen.getByTestId('wizard-cli-args')).toHaveValue('--foo')
  })
})
