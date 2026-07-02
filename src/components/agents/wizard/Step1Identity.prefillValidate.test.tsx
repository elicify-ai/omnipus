// Step1Identity.prefillValidate.test.tsx — external-executor-cli-path-
// detection spec (ADR-030), driven through the CreateAgentWizard (the
// `ExecutorInputs` sub-component that owns the cli_path prefill + validate-
// on-blur logic only mounts inside a subagent_3p wizard flow).
//
// Covers US-1 (prefill), US-2 (well-known source), US-3 (validate-on-blur
// blocking rule), US-4 (manual override / no-clobber), FR-005/FR-008/FR-018/
// FR-019 (gate on `reason`, never trap Create on an unchecked/errored
// result).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { CreateAgentWizard } from '../CreateAgentWizard'
import { useUiStore } from '@/store/ui'
import { act } from 'react'
import { fetchCliDetect, fetchCliValidate } from '@/lib/api'
import type { CliDetect, CliValidateResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchCliDetect: vi.fn(), fetchCliValidate: vi.fn() }
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

beforeEach(() => {
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false, sessionPanelOpen: false, toasts: [] })
  })
  vi.mocked(fetchCliDetect).mockReset()
  vi.mocked(fetchCliValidate).mockReset()
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

/** Drives the wizard from step 1 through to the Create button, filling the
 *  minimum required fields for a subagent_3p agent. Does NOT touch the CLI
 *  path field beyond what the caller already set — used to reach step 3
 *  for the Create-button gating assertions. */
async function advanceToStep3() {
  fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'External Worker' } })
  fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'delegates to an external CLI' } })
  fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'claude-sonnet-4-6' } })
  await waitFor(() => expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled())
  fireEvent.click(screen.getByTestId('wizard-next-1'))
  fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'You are focused.' } })
  await waitFor(() => expect(screen.getByTestId('wizard-next-2')).not.toBeDisabled())
  fireEvent.click(screen.getByTestId('wizard-next-2'))
  await screen.findByTestId('wizard-create')
}

describe('Step1Identity / ExecutorInputs — prefill (US-1/US-2)', () => {
  it('prefills the detected absolute path when the CLI is selected and the field is empty (US-1 AC-1)', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderWizard({ initialCli: 'claude-code' })

    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path')).toHaveValue('/usr/local/bin/claude')
    })
    expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/Detected \(PATH\)/i)
  })

  it('prefills from a well-known-dir hit with the "well-known" source label (US-2 AC-1)', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderWizard({ initialCli: 'opencode' })

    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path')).toHaveValue('/home/dev/.local/bin/opencode')
    })
    expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/Detected \(well-known\)/i)
  })

  it('shows a "not found" hint and leaves the field empty when the CLI is not detected anywhere (US-1 AC-3)', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderWizard({ initialCli: 'codex' })

    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/not found/i)
    })
    expect(screen.getByTestId('wizard-cli-path')).toHaveValue('')
  })

  it('never overwrites an existing non-empty path on (re)selection (US-1 AC-2 / US-4)', async () => {
    // Hold the detect promise open so we can type into the field BEFORE
    // detection resolves, proving the prefill effect checks emptiness at
    // fire-time and a subsequent resolution never clobbers a typed value.
    let resolveDetect: (d: CliDetect) => void = () => {}
    vi.mocked(fetchCliDetect).mockReturnValue(new Promise((resolve) => { resolveDetect = resolve }))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/opt/custom/claude' } })
    expect(screen.getByTestId('wizard-cli-path')).toHaveValue('/opt/custom/claude')

    await act(async () => {
      resolveDetect(detect())
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('wizard-cli-path')).toHaveValue('/opt/custom/claude')
  })

  it('prefills per CLI chooser selection when no CLI is locked from the roster', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderWizard({})

    expect(screen.getByTestId('wizard-cli-chooser')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('wizard-cli-opencode'))

    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path')).toHaveValue('/home/dev/.local/bin/opencode')
    })
  })
})

describe('Step1Identity / ExecutorInputs — validate-on-blur blocking rule (US-3, FR-008/FR-018)', () => {
  it('missing-binary blocks the Create button with the reason shown', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect({ claude: { installed: false, path: null, source: null } }))
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ ok: false, reason: 'missing-binary', resolved_path: null, version: null, detail: 'not found' }))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/nope/claude' } })
    fireEvent.blur(screen.getByTestId('wizard-cli-path'))

    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalledWith('claude-code', '/nope/claude', expect.anything()), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/no cli found/i)
    })

    await advanceToStep3()
    expect(screen.getByTestId('wizard-create')).toBeDisabled()
    expect(screen.getByTestId('wizard-cli-validation-blocked')).toHaveTextContent(/missing-binary/)
  })

  it('handshake-failed blocks the Create button', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ ok: false, reason: 'handshake-failed', version: null, detail: 'did not run' }))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/opt/not-a-cli' } })
    fireEvent.blur(screen.getByTestId('wizard-cli-path'))
    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalled(), { timeout: 2000 })

    await advanceToStep3()
    await waitFor(() => expect(screen.getByTestId('wizard-create')).toBeDisabled())
  })

  it('unauthenticated is a non-blocking warning — Create stays enabled', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ ok: true, reason: 'unauthenticated' }))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })
    fireEvent.blur(screen.getByTestId('wizard-cli-path'))
    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/not logged in/i)
    })

    await advanceToStep3()
    expect(screen.getByTestId('wizard-create')).not.toBeDisabled()
    expect(screen.queryByTestId('wizard-cli-validation-blocked')).toBeNull()
  })

  it('reason "ok" allows Create', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ reason: 'ok' }))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })
    fireEvent.blur(screen.getByTestId('wizard-cli-path'))
    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalled(), { timeout: 2000 })

    await advanceToStep3()
    expect(screen.getByTestId('wizard-create')).not.toBeDisabled()
  })
})

describe('Step1Identity / ExecutorInputs — FR-019 (never trap Create)', () => {
  it('Create is allowed by default before any validate-on-blur has run', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })
    // No blur — fetchCliValidate is never called.
    await advanceToStep3()
    expect(fetchCliValidate).not.toHaveBeenCalled()
    expect(screen.getByTestId('wizard-create')).not.toBeDisabled()
  })

  it('a rejected validate (network error / rate-limited) shows a soft notice and allows Create', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockRejectedValue(new Error('429 rate limited'))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })
    fireEvent.blur(screen.getByTestId('wizard-cli-path'))
    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/couldn't verify/i)
    })

    await advanceToStep3()
    expect(screen.getByTestId('wizard-create')).not.toBeDisabled()
  })

  it('editing the path after a block clears the block (no permanent trap)', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ ok: false, reason: 'missing-binary', resolved_path: null, version: null, detail: 'not found' }))
    renderWizard({ initialCli: 'claude-code' })

    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/nope/claude' } })
    fireEvent.blur(screen.getByTestId('wizard-cli-path'))
    await waitFor(() => {
      expect(screen.getByTestId('wizard-cli-path-status')).toHaveTextContent(/no cli found/i)
    })

    // Editing resets the verdict to idle — the stale block must not persist.
    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude' } })

    await advanceToStep3()
    expect(screen.getByTestId('wizard-create')).not.toBeDisabled()
    expect(screen.queryByTestId('wizard-cli-validation-blocked')).toBeNull()
  })
})
