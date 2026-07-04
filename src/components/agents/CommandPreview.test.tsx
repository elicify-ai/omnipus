// CommandPreview.test.tsx — the primary executor-transparency UI for a
// subagent_3p worker. Proves the live command-line preview renders the REAL
// computed argv/command_line (not a static description), surfaces
// dropped_args and model_dropped_reason as visible warnings BEFORE saving,
// explains prompt_delivery, and fails soft with a retryable degraded state —
// same resilience contract as the AutoAppliedFlags block it replaces.
//
// Fake timers drive the 400ms debounce (`advanceAndFlush`, mirroring
// `useCliPathValidation.test.ts`). Deliberately does NOT mix `waitFor` with
// fake timers — `waitFor`'s internal polling relies on real timer
// progression, so combining the two hangs until the test's own timeout
// (`useCliPathValidation.test.ts`'s "fake timers + waitFor don't mix well"
// note). `advanceAndFlush` already drains the state update inside `act`, so
// assertions run synchronously right after it.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { CommandPreview } from './CommandPreview'
import { fetchExecutorPreview, fetchExecutorSmokeTest, ApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import type { ExecutorCommandPreviewRequest, ExecutorCommandPreviewResponse, ExecutorSmokeTestResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchExecutorPreview: vi.fn(), fetchExecutorSmokeTest: vi.fn() }
})

function okResponse(overrides: Partial<ExecutorCommandPreviewResponse> = {}): ExecutorCommandPreviewResponse {
  return {
    binary: 'claude',
    argv: ['-p', '--output-format', 'stream-json', '--model', 'sonnet'],
    command_line: 'claude -p --output-format stream-json --model sonnet',
    prompt_delivery: 'stdin',
    dropped_args: [],
    ...overrides,
  }
}

function req(overrides: Partial<ExecutorCommandPreviewRequest> = {}): ExecutorCommandPreviewRequest {
  return { cli: 'claude-code', ...overrides }
}

function okSmokeTestResult(overrides: Partial<ExecutorSmokeTestResponse> = {}): ExecutorSmokeTestResponse {
  return { ok: true, response_text: '2', duration_ms: 2300, used_agent_workspace: false, ...overrides }
}

async function advanceAndFlush(ms: number) {
  await act(async () => {
    vi.advanceTimersByTime(ms)
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

beforeEach(() => {
  vi.mocked(fetchExecutorPreview).mockReset()
  vi.mocked(fetchExecutorSmokeTest).mockReset()
  vi.useFakeTimers()
  act(() => {
    useUiStore.setState({ toasts: [] })
  })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('CommandPreview — no CLI selected', () => {
  it('renders nothing when req is undefined', () => {
    const { container } = render(<CommandPreview req={undefined} testId="cp" />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('CommandPreview — loading', () => {
  it('shows a loading skeleton while the debounced fetch is pending', () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    render(<CommandPreview req={req()} testId="cp" />)

    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'loading')
    expect(screen.getByTestId('cp')).toHaveTextContent(/computing the real command line/i)
  })
})

describe('CommandPreview — success', () => {
  it('renders the REAL command_line string, not a generic description', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      okResponse({ command_line: 'claude -p --output-format stream-json --model sonnet --permission-mode acceptEdits' }),
    )
    render(<CommandPreview req={req({ model: 'sonnet' })} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'ready')
    expect(screen.getByTestId('cp-command-line')).toHaveTextContent(
      'claude -p --output-format stream-json --model sonnet --permission-mode acceptEdits',
    )
  })

  it('explains stdin prompt delivery', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse({ prompt_delivery: 'stdin' }))
    render(<CommandPreview req={req()} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp-prompt-delivery')).toHaveTextContent(/sent via stdin/i)
  })

  it('explains positional-argument prompt delivery (opencode)', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      okResponse({ prompt_delivery: 'positional argument after --', binary: 'opencode' }),
    )
    render(<CommandPreview req={req({ cli: 'opencode' })} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp-prompt-delivery')).toHaveTextContent(/final command-line argument/i)
  })

  it('renders model_dropped_reason as a visible warning near the model output', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      okResponse({ model_dropped_reason: 'opencode requires a "provider/model" shape; "sonnet" was not recognized.' }),
    )
    render(<CommandPreview req={req({ cli: 'opencode', model: 'sonnet' })} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp-model-dropped-reason')).toHaveTextContent(/provider\/model.*shape/i)
  })

  it('does not render a model_dropped_reason line when the field is absent', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    render(<CommandPreview req={req()} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'ready')
    expect(screen.queryByTestId('cp-model-dropped-reason')).not.toBeInTheDocument()
  })

  it('renders each dropped_args entry with its flag and reason as a visible warning', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(
      okResponse({
        dropped_args: [
          { flag: '--dangerously-skip-permissions', reason: 'conflicts with the enforced permission mode' },
          { flag: '--resume', reason: 'every run starts a fresh session' },
        ],
      }),
    )
    render(<CommandPreview req={req({ cli_args: '--dangerously-skip-permissions --resume' })} testId="cp" />)
    await advanceAndFlush(400)

    const block = screen.getByTestId('cp-dropped-args')
    expect(block).toHaveTextContent('--dangerously-skip-permissions')
    expect(block).toHaveTextContent(/conflicts with the enforced permission mode/i)
    expect(block).toHaveTextContent('--resume')
    expect(block).toHaveTextContent(/every run starts a fresh session/i)
  })

  it('renders no dropped-args block when dropped_args is empty', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse({ dropped_args: [] }))
    render(<CommandPreview req={req()} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'ready')
    expect(screen.queryByTestId('cp-dropped-args')).not.toBeInTheDocument()
  })

  it('copies the command line to the clipboard and shows a success toast', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse({ command_line: 'claude -p --verbose' }))
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })

    render(<CommandPreview req={req()} testId="cp" />)
    await advanceAndFlush(400)
    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'ready')

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-copy'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith('claude -p --verbose')
    expect(useUiStore.getState().toasts.some((t) => /copied to clipboard/i.test(t.message))).toBe(true)
  })
})

describe('CommandPreview — degraded / error state', () => {
  it('fails soft with a visible, retryable degraded state (never silently omits the block) for a transport failure', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(new Error('network down'))
    render(<CommandPreview req={req()} testId="cp" />)
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'error')
    expect(screen.getByTestId('cp')).toHaveTextContent(/couldn't compute a live command preview/i)
    // Reassuring copy is only correct for a transport-shaped failure — it
    // must NOT claim the settings are unvalidated-but-fine when the server
    // actually rejected the request body (covered below).
    expect(screen.getByTestId('cp')).not.toHaveTextContent(/couldn't be validated/i)

    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse({ command_line: 'claude -p --verbose' }))
    act(() => {
      fireEvent.click(screen.getByTestId('cp-retry'))
    })
    await advanceAndFlush(400)

    expect(screen.getByTestId('cp')).toHaveAttribute('data-preview-status', 'ready')
    expect(screen.getByTestId('cp-command-line')).toHaveTextContent('claude -p --verbose')
  })

  it('shows the real server message for a 400 schema-validation failure, and does NOT claim the settings still apply', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(
      new ApiError(400, 'request body does not match schema ExecutorCommandPreviewRequest: model is too long'),
    )
    render(<CommandPreview req={req({ model: 'x'.repeat(300) })} testId="cp" />)
    await advanceAndFlush(400)

    const block = screen.getByTestId('cp')
    expect(block).toHaveAttribute('data-preview-status', 'error')
    expect(block).toHaveTextContent(/couldn't be validated/i)
    expect(block).toHaveTextContent(/model is too long/i)
    expect(block).toHaveTextContent(/check it before saving/i)
    // The old reassuring copy would be actively misleading here — a value
    // this endpoint rejects as schema-invalid is likely to misbehave at real
    // save/dispatch time too (same shared schema family).
    expect(block).not.toHaveTextContent(/the settings below still apply when this agent runs/i)
  })
})

// ── "Send a test message" smoke test ────────────────────────────────────
//
// Unlike the reactive, debounced command-line preview above, the smoke test
// never fires on its own — these tests never advance the fake timers past
// the preview's own debounce, so `fetchExecutorPreview` (mocked with no
// implementation, i.e. returns `undefined`) never actually gets called; the
// preview block simply stays in its loading state throughout, which is
// irrelevant here since the smoke test section renders as an independent
// sibling regardless of preview status.

describe('CommandPreview — smoke test button', () => {
  it('renders the button once a CLI is chosen', () => {
    render(<CommandPreview req={req()} testId="cp" />)

    const button = screen.getByTestId('cp-smoke-test-button')
    expect(button).toHaveTextContent(/send a test message/i)
    expect(button).not.toBeDisabled()
    expect(screen.getByTestId('cp-smoke-test')).toHaveTextContent(/spends a small amount of usage/i)
  })

  it('never calls fetchExecutorSmokeTest until the button is clicked', () => {
    render(<CommandPreview req={req()} testId="cp" />)
    expect(fetchExecutorSmokeTest).not.toHaveBeenCalled()
  })

  it('shows a reassuring pending state while the real subprocess runs', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockReturnValue(new Promise(() => {}))
    render(<CommandPreview req={req()} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
    })

    expect(fetchExecutorSmokeTest).toHaveBeenCalledWith(
      { cli: 'claude-code', model: undefined, cli_path: undefined, cli_args: undefined },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    const button = screen.getByTestId('cp-smoke-test-button')
    expect(button).toBeDisabled()
    expect(screen.getByTestId('cp-smoke-test-pending')).toHaveTextContent(/up to 30 seconds/i)
  })

  it('shows the real response text and formatted duration on success', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult({ response_text: '2', duration_ms: 2300 }))
    render(<CommandPreview req={req({ model: 'claude-sonnet-4-6' })} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-success')).toHaveTextContent('2.3s')
    expect(screen.getByTestId('cp-smoke-test-response-text')).toHaveTextContent('2')
    expect(screen.getByTestId('cp-smoke-test-button')).not.toBeDisabled()
  })

  it('shows the plain error text for a domain-level ok:false result, with the button re-enabled to retry', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue({
      ok: false,
      error: 'Authentication failed.',
      duration_ms: 900,
      used_agent_workspace: false,
    })
    render(<CommandPreview req={req()} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-error')).toHaveTextContent('Authentication failed.')
    expect(screen.getByTestId('cp-smoke-test-button')).not.toBeDisabled()
  })

  it('shows a plain error for a transport failure, and re-clicking the same button retries', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockRejectedValueOnce(new ApiError(0, 'Network unavailable. Check your connection.'))
    render(<CommandPreview req={req()} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-error')).toHaveTextContent('Network unavailable. Check your connection.')
    const button = screen.getByTestId('cp-smoke-test-button')
    expect(button).not.toBeDisabled()

    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult())
    await act(async () => {
      fireEvent.click(button)
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-success')).toBeInTheDocument()
    expect(fetchExecutorSmokeTest).toHaveBeenCalledTimes(2)
  })

  it('sends only cli/model/cli_path/cli_args — no max_tool_iterations field on the wire', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult())
    render(
      <CommandPreview
        req={{ cli: 'codex', model: 'gpt-5', cli_path: '/usr/local/bin/codex', cli_args: '--foo', max_tool_iterations: 30 }}
        testId="cp"
      />,
    )

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
    })

    const call = vi.mocked(fetchExecutorSmokeTest).mock.calls[0][0]
    expect(call).toEqual({ cli: 'codex', model: 'gpt-5', cli_path: '/usr/local/bin/codex', cli_args: '--foo' })
    expect(call).not.toHaveProperty('max_tool_iterations')
  })

  it('resets a stale success result to idle when the underlying settings change, without auto-refiring', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult({ response_text: '2', duration_ms: 2300 }))
    const { rerender } = render(<CommandPreview req={req({ cli: 'claude-code', model: 'claude-sonnet-4-6' })} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(screen.getByTestId('cp-smoke-test-success')).toBeInTheDocument()

    // Operator switches CLI without re-clicking "Send a test message" — the
    // previous claude-code result must not keep rendering as if it still
    // describes the now-selected codex configuration.
    rerender(<CommandPreview req={req({ cli: 'codex', model: 'gpt-5' })} testId="cp" />)

    expect(screen.queryByTestId('cp-smoke-test-success')).not.toBeInTheDocument()
    expect(screen.queryByTestId('cp-smoke-test-error')).not.toBeInTheDocument()
    const button = screen.getByTestId('cp-smoke-test-button')
    expect(button).not.toBeDisabled()
    expect(button).toHaveTextContent(/send a test message/i)

    // Never auto-refires — only a deliberate click may spend real tokens.
    expect(fetchExecutorSmokeTest).toHaveBeenCalledTimes(1)
  })

  it('resets a stale error result to idle when the underlying settings change', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue({
      ok: false,
      error: 'Authentication failed.',
      duration_ms: 900,
      used_agent_workspace: false,
    })
    const { rerender } = render(<CommandPreview req={req({ cli_args: '--foo' })} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(screen.getByTestId('cp-smoke-test-error')).toBeInTheDocument()

    rerender(<CommandPreview req={req({ cli_args: '--bar' })} testId="cp" />)

    expect(screen.queryByTestId('cp-smoke-test-error')).not.toBeInTheDocument()
    expect(screen.getByTestId('cp-smoke-test-button')).not.toBeDisabled()
  })

  it('aborts an in-flight smoke test and resets to idle when settings change mid-flight', async () => {
    let capturedSignal: AbortSignal | undefined
    vi.mocked(fetchExecutorSmokeTest).mockImplementation((_r, opts) => {
      capturedSignal = opts?.signal
      return new Promise<ExecutorSmokeTestResponse>(() => {
        // never resolves — simulates a slow in-flight request
      })
    })
    const { rerender } = render(<CommandPreview req={req({ model: 'one' })} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
    })
    expect(screen.getByTestId('cp-smoke-test-button')).toBeDisabled()
    expect(capturedSignal?.aborted).toBe(false)

    rerender(<CommandPreview req={req({ model: 'two' })} testId="cp" />)

    expect(capturedSignal?.aborted).toBe(true)
    expect(screen.getByTestId('cp-smoke-test-button')).not.toBeDisabled()
  })

  it('does NOT reset the result when re-rendered with the same settings VALUES (no phantom staleness)', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult())
    const { rerender } = render(<CommandPreview req={req({ model: 'claude-sonnet-4-6' })} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(screen.getByTestId('cp-smoke-test-success')).toBeInTheDocument()

    // Fresh object literal, identical field values — must NOT clear the
    // just-produced result.
    rerender(<CommandPreview req={req({ model: 'claude-sonnet-4-6' })} testId="cp" />)

    expect(screen.getByTestId('cp-smoke-test-success')).toBeInTheDocument()
  })
})

// ── agent_id threading (AgentProfile edit view vs. create wizard) ──────────

describe('CommandPreview — agentId prop threading into the smoke-test request', () => {
  it('includes agent_id in the smoke-test request when the agentId prop is provided (AgentProfile edit view)', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult())
    render(<CommandPreview req={req()} agentId="agent-123" testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
    })

    expect(fetchExecutorSmokeTest).toHaveBeenCalledWith(
      { cli: 'claude-code', model: undefined, cli_path: undefined, cli_args: undefined, agent_id: 'agent-123' },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })

  it('omits agent_id from the smoke-test request when no agentId prop is provided (create wizard)', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult())
    render(<CommandPreview req={req()} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
    })

    const call = vi.mocked(fetchExecutorSmokeTest).mock.calls[0][0]
    expect(call.agent_id).toBeUndefined()
  })
})

// ── used_agent_workspace transparency copy ──────────────────────────────

describe('CommandPreview — used_agent_workspace transparency copy', () => {
  it('says the test ran in the agent\'s own workspace on success when used_agent_workspace is true', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult({ used_agent_workspace: true }))
    render(<CommandPreview req={req()} agentId="agent-123" testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-success-workspace-note')).toHaveTextContent(
      /this agent's own saved workspace/i,
    )
  })

  it('says the test ran in a temporary workspace on success when used_agent_workspace is false', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okSmokeTestResult({ used_agent_workspace: false }))
    render(<CommandPreview req={req()} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-success-workspace-note')).toHaveTextContent(
      /temporary workspace.*save the agent first/i,
    )
  })

  it('says the test ran in the agent\'s own workspace on a domain-level FAILURE when used_agent_workspace is true', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue({
      ok: false,
      error: 'Authentication failed.',
      duration_ms: 900,
      used_agent_workspace: true,
    })
    render(<CommandPreview req={req()} agentId="agent-123" testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-failure-workspace-note')).toHaveTextContent(
      /this agent's own saved workspace/i,
    )
  })

  it('says the test ran in a temporary workspace on a domain-level FAILURE when used_agent_workspace is false', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue({
      ok: false,
      error: 'Authentication failed.',
      duration_ms: 900,
      used_agent_workspace: false,
    })
    render(<CommandPreview req={req()} testId="cp" />)

    await act(async () => {
      fireEvent.click(screen.getByTestId('cp-smoke-test-button'))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(screen.getByTestId('cp-smoke-test-failure-workspace-note')).toHaveTextContent(
      /temporary workspace.*save the agent first/i,
    )
  })
})
