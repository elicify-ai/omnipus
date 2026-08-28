// AgentProfile.cliPathValidate.test.tsx — external-executor-cli-path-
// detection spec (ADR-030), US-5: the edit form gets the same detect→
// prefill + validate-on-blur as the create wizard, because `cli_path` is
// mutable on edit (only `cli` itself stays locked).
//
// Covers: prefill-on-empty (US-5 AC-1), no-clobber of an existing value
// (US-4), validate-on-blur blocking autosave on missing-binary/
// handshake-failed (FR-008/FR-018), unauthenticated as a non-blocking
// warning that still autosaves (US-3 AC-3), and FR-019 (an edit after a
// block lifts it — no permanent trap).

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
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import type { CliDetect, CliValidateResponse } from '@/lib/api'

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
  vi.mocked(fetchCliDetect).mockReset()
  vi.mocked(fetchCliValidate).mockReset()
  useUiStore.setState({ toasts: [] })
})

describe('AgentProfile — Runtime tab CLI path prefill (US-5 AC-1)', () => {
  it('prefills the detected absolute path when cli_path is empty', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => {
      const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
      expect(input.value).toBe('/usr/local/bin/claude')
    })
    expect(screen.getByTestId('profile-cli-path-status')).toHaveTextContent(/Detected \(PATH\)/i)
  })

  it('never overwrites an existing non-empty cli_path (US-4)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/opt/custom/claude' },
    })
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    await waitFor(() => expect(fetchCliDetect).toHaveBeenCalled())
    const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
    expect(input.value).toBe('/opt/custom/claude')
  })
})

describe('AgentProfile — Runtime tab CLI path prefill does not autosave itself (code-review M2a)', () => {
  it('merely opening the profile never persists the offered path — cli_path stays absent from any save until confirmed', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    // The offered (detected) path is shown immediately — same UX as before.
    await waitFor(() => {
      const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
      expect(input.value).toBe('/usr/local/bin/claude')
    })

    // Give the autosave debounce (and then some) a chance to fire. The form
    // hydrating from the server is itself a `formData` change (unrelated
    // pre-existing behavior — the initial render snapshot is taken before
    // the agent query resolves), so an idempotent round-trip save MAY fire
    // here. What must NOT happen (code-review M2a) is that save carrying a
    // `cli_path` the operator never typed or confirmed — before the fix, the
    // old prefill effect called `setExecutor` + `markDirty()` the moment
    // detection resolved, so this exact round-trip save would have shipped
    // the auto-detected path with zero operator action.
    await new Promise((resolve) => setTimeout(resolve, 700))
    for (const call of vi.mocked(updateAgent).mock.calls) {
      const payload = call[1] as { executor?: { cli_path?: string } }
      expect(payload.executor?.cli_path).toBeUndefined()
    }
  })

  it('leaving the field with the offered value still showing (blur) DOES commit + autosave it — the "confirm" gesture', async () => {
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ reason: 'ok' }))
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
    await waitFor(() => expect(input.value).toBe('/usr/local/bin/claude'))

    fireEvent.blur(input)

    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
    const payload = vi.mocked(updateAgent).mock.calls[0][1] as { executor?: { cli_path?: string } }
    expect(payload.executor?.cli_path).toBe('/usr/local/bin/claude')
  })
})

describe('AgentProfile — Runtime tab: a cli_path-only edit re-triggers the runner test (code-review m2)', () => {
  it('re-runs testAgentRunner on a second save when only cli_path changed (kind/cli unchanged)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/local/bin/claude' },
    })
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ reason: 'ok' }))
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
    fireEvent.change(input, { target: { value: '/usr/local/bin/claude-v2' } })
    fireEvent.blur(input)

    await waitFor(() => expect(testAgentRunner).toHaveBeenCalledTimes(1), { timeout: 5000 })
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(1), { timeout: 5000 })

    // A second, independent cli_path-only edit — the executor kind/cli
    // signature is unchanged, but the path itself differs, so the runner
    // test MUST re-fire (code-review m2) rather than reusing the cached
    // "already tested" signature from the first save, which would let a
    // definitively-bad new path silently persist unverified.
    fireEvent.change(input, { target: { value: '/usr/local/bin/claude-v3' } })
    fireEvent.blur(input)

    await waitFor(() => expect(testAgentRunner).toHaveBeenCalledTimes(2), { timeout: 5000 })
    await waitFor(() => expect(updateAgent).toHaveBeenCalledTimes(2), { timeout: 5000 })
  })
})

describe('AgentProfile — Runtime tab validate-on-blur blocking rule (FR-008/FR-018)', () => {
  it('missing-binary blocks the autosave (no updateAgent call) and toasts the reason', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/nope/claude' },
    })
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(
      validateResult({ ok: false, reason: 'missing-binary', resolved_path: null, version: null, detail: 'not found' }),
    )
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
    fireEvent.blur(input)

    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalledWith('claude-code', '/nope/claude', expect.anything()), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('profile-cli-path-status')).toHaveTextContent(/no cli found/i)
    })

    // Trigger the autosave debounce via an UNRELATED field (the Basics tab's
    // name input) — the cli_path itself is left untouched so the blocking
    // verdict from the blur above survives, and the block must fire BEFORE
    // any PUT is sent.
    switchTab('tab-basics')
    const nameInputs = screen.getAllByDisplayValue('External Worker')
    fireEvent.change(nameInputs[0], { target: { value: 'External Worker Renamed' } })

    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      expect(toasts.some((t) => /failed validation/i.test(t.message))).toBe(true)
    }, { timeout: 3000 })

    expect(updateAgent).not.toHaveBeenCalled()
  })

  it('unauthenticated is a non-blocking warning — autosave still fires', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/usr/local/bin/claude' },
    })
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ ok: true, reason: 'unauthenticated' }))
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
    fireEvent.change(input, { target: { value: '/usr/local/bin/claude2' } })
    fireEvent.blur(input)

    await waitFor(() => expect(fetchCliValidate).toHaveBeenCalled(), { timeout: 2000 })
    await waitFor(() => {
      expect(screen.getByTestId('profile-cli-path-status')).toHaveTextContent(/not logged in/i)
    })

    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
    const payload = vi.mocked(updateAgent).mock.calls[0][1] as { executor?: { cli_path?: string } }
    expect(payload.executor?.cli_path).toBe('/usr/local/bin/claude2')
  })

  it('editing the path after a block clears it and a subsequent ok result allows autosave (FR-019)', async () => {
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockExternalAgent,
      executor: { kind: 'external-cli', cli: 'claude-code', cli_path: '/nope/claude' },
    })
    vi.mocked(fetchCliDetect).mockResolvedValue(detect())
    vi.mocked(fetchCliValidate).mockResolvedValueOnce(
      validateResult({ ok: false, reason: 'missing-binary', resolved_path: null, version: null, detail: 'not found' }),
    )
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')

    const input = screen.getByTestId('profile-cli-path').querySelector('input') as HTMLInputElement
    fireEvent.blur(input)
    await waitFor(() => {
      expect(screen.getByTestId('profile-cli-path-status')).toHaveTextContent(/no cli found/i)
    })

    vi.mocked(fetchCliValidate).mockResolvedValue(validateResult({ reason: 'ok' }))
    fireEvent.change(input, { target: { value: '/usr/local/bin/claude' } })
    fireEvent.blur(input)

    await waitFor(() => {
      expect(screen.getByTestId('profile-cli-path-status')).toHaveTextContent(/cli runs/i)
    })
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
  })
})
