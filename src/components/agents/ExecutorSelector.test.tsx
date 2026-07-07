/**
 * Tests for ExecutorSelector (Spec-4 FR-4.1 / FR-4.2)
 *
 * Coverage:
 *  - kind select renders native default and persists kind+cli changes via onChange
 *  - switching to external-cli reveals the cli select (defaults to claude-code)
 *  - switching to remote-a2a shows the reserved note and hides the cli select
 *  - Test Connection button calls POST /agents/{id}/runner/test and renders each
 *    RunnerTestResponse reason as a distinct result state
 *  - loading state while the test is in flight
 *  - request-level error state (mutation rejects)
 *  - the test button is hidden when no agentId is provided (create flow)
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ExecutorSelector } from './ExecutorSelector'
import { ApiError } from '@/lib/api'
import type { ExecutorConfig, RunnerTestResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    testAgentRunner: vi.fn(),
  }
})

import { testAgentRunner } from '@/lib/api'

const mockedTest = vi.mocked(testAgentRunner)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSelector(
  overrides: Partial<Parameters<typeof ExecutorSelector>[0]> = {},
) {
  const onChange = vi.fn()
  const props = {
    value: undefined as ExecutorConfig | undefined,
    onChange,
    ...overrides,
  }
  render(
    <QueryClientProvider client={makeClient()}>
      <ExecutorSelector {...props} />
    </QueryClientProvider>,
  )
  return { onChange }
}

beforeEach(() => {
  mockedTest.mockReset()
})

describe('ExecutorSelector — kind + cli persistence', () => {
  it('defaults the runtime select to native when value is undefined', () => {
    renderSelector()
    const kind = screen.getByTestId('executor-kind-select') as HTMLSelectElement
    expect(kind.value).toBe('native')
    // No cli block, no remote note in the native default.
    expect(screen.queryByTestId('executor-cli-block')).toBeNull()
    expect(screen.queryByTestId('executor-remote-a2a-note')).toBeNull()
  })

  it('reflects an existing external-cli executor value', () => {
    renderSelector({ value: { kind: 'external-cli', cli: 'codex' } })
    const kind = screen.getByTestId('executor-kind-select') as HTMLSelectElement
    expect(kind.value).toBe('external-cli')
    const cli = screen.getByTestId('executor-cli-select') as HTMLSelectElement
    expect(cli.value).toBe('codex')
  })

  it('switching to external-cli emits kind+cli with claude-code default', () => {
    const { onChange } = renderSelector()
    fireEvent.change(screen.getByTestId('executor-kind-select'), {
      target: { value: 'external-cli' },
    })
    expect(onChange).toHaveBeenCalledWith({ kind: 'external-cli', cli: 'claude-code' })
  })

  it('changing the cli select persists the chosen cli', () => {
    const { onChange } = renderSelector({ value: { kind: 'external-cli', cli: 'claude-code' } })
    fireEvent.change(screen.getByTestId('executor-cli-select'), {
      target: { value: 'opencode' },
    })
    expect(onChange).toHaveBeenCalledWith({ kind: 'external-cli', cli: 'opencode' })
  })

  it('switching back to native emits a cli-less native executor', () => {
    const { onChange } = renderSelector({ value: { kind: 'external-cli', cli: 'codex' } })
    fireEvent.change(screen.getByTestId('executor-kind-select'), {
      target: { value: 'native' },
    })
    expect(onChange).toHaveBeenCalledWith({ kind: 'native' })
  })
})

describe('ExecutorSelector — remote-a2a reserved note', () => {
  it('shows the reserved note and hides the cli select for remote-a2a', () => {
    renderSelector({ value: { kind: 'remote-a2a' } })
    const note = screen.getByTestId('executor-remote-a2a-note')
    expect(note).toBeInTheDocument()
    expect(note).toHaveTextContent(/reserved — not available in v0\.1\.0/i)
    expect(screen.queryByTestId('executor-cli-block')).toBeNull()
  })

  it('emits a cli-less remote-a2a executor when selected', () => {
    const { onChange } = renderSelector()
    fireEvent.change(screen.getByTestId('executor-kind-select'), {
      target: { value: 'remote-a2a' },
    })
    expect(onChange).toHaveBeenCalledWith({ kind: 'remote-a2a' })
  })

  // Traces to: wave2 findings-fix cycle — the note used to (incorrectly)
  // claim the agent "will fall back to the native runtime until A2A
  // resolution ships." That was verified false against the backend
  // (pkg/agent/external_dispatch.go / pkg/agent/subturn.go), which reject
  // remote-a2a sub-turns with an error rather than silently falling back to
  // native. The copy was corrected to state the sub-turn fails with an
  // error. The generic "reserved — not available in v0.1.0" substring
  // (asserted above) would still match the OLD, misleading copy, so it does
  // NOT catch a regression back to the false claim — only pinning the
  // specific corrected sentence does.
  it('pins the corrected remote-a2a copy (sub-turns fail with an error, do NOT fall back to native)', () => {
    renderSelector({ value: { kind: 'remote-a2a' } })
    const note = screen.getByTestId('executor-remote-a2a-note')
    expect(note).toHaveTextContent(
      /selecting this will cause delegated sub-turns to fail with an error until a2a resolution ships/i,
    )
    // The retired, inaccurate claim must never reappear.
    expect(note).not.toHaveTextContent(/fall back to the native runtime/i)
  })
})

describe('ExecutorSelector — core-agent gate (G9)', () => {
  it('renders external-cli option disabled with the workers-only tooltip when isCoreAgent', () => {
    renderSelector({ isCoreAgent: true })
    const opt = screen.getByTestId('executor-kind-option-external-cli') as HTMLOptionElement
    expect(opt).toBeInTheDocument()
    expect(opt.disabled).toBe(true)
    expect(opt.title).toMatch(/core agents run native only/i)
    expect(opt.textContent).toMatch(/workers only/i)
  })

  it('keeps native + remote-a2a options enabled for core agents', () => {
    renderSelector({ isCoreAgent: true })
    const native = screen.getByTestId('executor-kind-option-native') as HTMLOptionElement
    const a2a = screen.getByTestId('executor-kind-option-remote-a2a') as HTMLOptionElement
    expect(native.disabled).toBe(false)
    expect(a2a.disabled).toBe(false)
  })

  it('leaves external-cli option enabled for non-core (worker) agents', () => {
    renderSelector()
    const opt = screen.getByTestId('executor-kind-option-external-cli') as HTMLOptionElement
    expect(opt.disabled).toBe(false)
    expect(opt.title).toBe('')
  })

  it('clamps the kind back to native when isCoreAgent is true (defence-in-depth)', () => {
    const { onChange } = renderSelector({ isCoreAgent: true })
    fireEvent.change(screen.getByTestId('executor-kind-select'), {
      target: { value: 'external-cli' },
    })
    expect(onChange).toHaveBeenCalledWith({ kind: 'native' })
  })

  it('surfaces the core-agent tooltip on the kind select itself', () => {
    renderSelector({ isCoreAgent: true })
    const select = screen.getByTestId('executor-kind-select')
    expect(select.getAttribute('title')).toMatch(/core agents run native only/i)
  })
})

describe('ExecutorSelector — Test Connection button visibility', () => {
  it('hides the test button when no agentId is given (create flow)', () => {
    renderSelector({ value: { kind: 'external-cli', cli: 'claude-code' } })
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
  })

  it('shows the test button only for external-cli with an agentId', () => {
    renderSelector({ value: { kind: 'native' }, agentId: 'agent-1' })
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
    // Re-render with external-cli.
  })

  it('renders the test button for external-cli + agentId', () => {
    renderSelector({ value: { kind: 'external-cli', cli: 'claude-code' }, agentId: 'agent-1' })
    expect(screen.getByTestId('runner-test-button')).toBeInTheDocument()
  })

  it('hides the test button when disabled (locked agent)', () => {
    renderSelector({
      value: { kind: 'external-cli', cli: 'claude-code' },
      agentId: 'agent-1',
      disabled: true,
    })
    expect(screen.queryByTestId('runner-test-button')).toBeNull()
  })
})

describe('ExecutorSelector — Test Connection result states', () => {
  function renderWithTest() {
    return renderSelector({
      value: { kind: 'external-cli', cli: 'claude-code' },
      agentId: 'agent-1',
    })
  }

  it('calls testAgentRunner with the agentId on click', async () => {
    mockedTest.mockResolvedValue({
      ok: true,
      reason: '',
      message: 'ready',
      cli: 'claude-code',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    await waitFor(() => expect(mockedTest).toHaveBeenCalledWith('agent-1'))
  })

  it('healthy → CLI found · authenticated', async () => {
    mockedTest.mockResolvedValue({
      ok: true,
      reason: '',
      message: 'binary present, v1.2.3, authenticated',
      cli: 'claude-code',
      cli_version: '1.2.3',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const result = await screen.findByTestId('runner-test-result')
    expect(result).toHaveAttribute('data-reason', 'ok')
    expect(result).toHaveTextContent(/CLI found · authenticated/i)
    expect(result).toHaveTextContent(/v1\.2\.3/)
  })

  it('missing-binary → CLI not installed', async () => {
    mockedTest.mockResolvedValue({
      ok: false,
      reason: 'missing-binary',
      message: 'claude not found on PATH',
      cli: 'claude-code',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const result = await screen.findByTestId('runner-test-result')
    expect(result).toHaveAttribute('data-reason', 'missing-binary')
    expect(result).toHaveTextContent(/CLI not installed/i)
  })

  it('unauthenticated → CLI found but not logged in', async () => {
    mockedTest.mockResolvedValue({
      ok: false,
      reason: 'unauthenticated',
      message: 'present but no credentials',
      cli: 'claude-code',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const result = await screen.findByTestId('runner-test-result')
    expect(result).toHaveAttribute('data-reason', 'unauthenticated')
    expect(result).toHaveTextContent(/CLI found but not logged in/i)
  })

  it('handshake-failed → CLI did not respond', async () => {
    mockedTest.mockResolvedValue({
      ok: false,
      reason: 'handshake-failed',
      message: 'binary present but no version',
      cli: 'claude-code',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const result = await screen.findByTestId('runner-test-result')
    expect(result).toHaveAttribute('data-reason', 'handshake-failed')
    expect(result).toHaveTextContent(/CLI did not respond/i)
  })

  it('unknown-cli → Unknown CLI', async () => {
    mockedTest.mockResolvedValue({
      ok: false,
      reason: 'unknown-cli',
      message: 'executor.cli is empty',
      cli: '',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const result = await screen.findByTestId('runner-test-result')
    expect(result).toHaveAttribute('data-reason', 'unknown-cli')
    expect(result).toHaveTextContent(/Unknown CLI/i)
  })

  it('not-external-cli → No external runner to test', async () => {
    mockedTest.mockResolvedValue({
      ok: false,
      reason: 'not-external-cli',
      message: 'executor is not external-cli',
    } satisfies RunnerTestResponse)
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const result = await screen.findByTestId('runner-test-result')
    expect(result).toHaveAttribute('data-reason', 'not-external-cli')
    expect(result).toHaveTextContent(/No external runner to test/i)
  })

  it('shows a loading state while the test is in flight', async () => {
    let resolve!: (v: RunnerTestResponse) => void
    mockedTest.mockReturnValue(new Promise<RunnerTestResponse>((r) => { resolve = r }))
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    await waitFor(() =>
      expect(screen.getByTestId('runner-test-button')).toHaveTextContent(/Testing/i),
    )
    expect(screen.getByTestId('runner-test-button')).toBeDisabled()
    resolve({ ok: true, reason: '', message: 'ok', cli: 'claude-code' })
    await screen.findByTestId('runner-test-result')
  })

  it('surfaces a request-level error when the mutation rejects', async () => {
    mockedTest.mockRejectedValue(new Error('network down'))
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const err = await screen.findByTestId('runner-test-request-error')
    expect(err).toHaveTextContent(/network down/i)
    // No success result rendered on error.
    expect(screen.queryByTestId('runner-test-result')).toBeNull()
  })

  // Traces to: wave2 findings-fix cycle — getErrorMessage(error, 'Test
  // request failed') (ExecutorSelector.tsx ~L235) branches on ApiError vs
  // plain Error vs fallback. The existing "network down" test above only
  // exercises the plain-Error branch (`err instanceof Error → err.message`).
  // This test exercises the actual point of the helper: when the mutation
  // rejects with a real ApiError, the rendered message must be the ApiError's
  // `userMessage` — a safe, human-facing string — not the legacy
  // "${status}: ${body}" Error.message shape and not the raw server body.
  it('surfaces the ApiError userMessage (not the status-prefixed Error.message or raw body) when the mutation rejects with an ApiError', async () => {
    mockedTest.mockRejectedValue(
      new ApiError(503, 'The server is unavailable. Please try again in a moment.', {
        body: '{"error":"executor subsystem unreachable: dial tcp refused"}',
      }),
    )
    renderWithTest()
    fireEvent.click(screen.getByTestId('runner-test-button'))
    const err = await screen.findByTestId('runner-test-request-error')
    expect(err).toHaveTextContent(
      'Test request failed: The server is unavailable. Please try again in a moment.',
    )
    // Must not leak the legacy "503: ..." Error.message shape or the raw
    // server-internal body text — userMessage is the only thing rendered.
    expect(err).not.toHaveTextContent(/503:/)
    expect(err).not.toHaveTextContent(/executor subsystem unreachable/i)
  })
})
