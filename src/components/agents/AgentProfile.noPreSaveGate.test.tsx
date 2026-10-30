/**
 * AgentProfile.noPreSaveGate.test.tsx — RED pack for T11 slice B, the
 * pre-save sign-in gate removal (t11 spec §4 "Agent editor (Runtime tab)";
 * §8 FR-8 second clause).
 *
 * Spec oracle: "The editor no longer runs a sign-in test before saving: the
 * current pre-save `testAgentRunner` gate in `AgentProfile.tsx` is removed
 * (Verified that it exists), so a person can always save the key that fixes
 * a broken sign-in."
 *
 * Today the gate exists (AgentProfile.tsx runs testAgentRunner before any
 * save when executor.kind === 'external-cli'), so BOTH tests are RED with a
 * real behavioural failure: the runner test is called (test 1), and a save
 * that would fix the sign-in is blocked (test 2). Same rendering/mocking
 * pattern as AgentProfile.commandPreview.test.tsx — real timers, generous
 * waitFor for the ~400ms auto-save debounce.
 */

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

// A Codex worker whose runner is currently broken (the exact case the spec
// names: the person has the fix — an env key — and must be able to save it).
function codexWorker(): Agent {
  return {
    revision: '0'.repeat(64),
    editable_fields: [
      { name: 'executor', editable: true },
      { name: 'name', editable: true },
    ],
    id: 'codex-worker',
    name: 'Codex Worker',
    type: 'subagent_3p',
    locked: false,
    needs_model: false,
    status: 'active',
    model: 'claude-sonnet-4-6',
    description: 'Delegates to Codex',
    soul: '',
    max_tool_iterations: 20,
    max_tool_iterations_source: 'global',
    max_tool_iterations_override_ignored: false,
    executor: {
      kind: 'external-cli',
      cli: 'codex',
      effective_cli_path: '/opt/homebrew/bin/codex',
      env_overrides: {},
    },
    memory_enabled: true,
    figure: 'Omnipus',
    role: 'general',
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function openRuntimeTab() {
  render(
    <QueryClientProvider client={makeClient()}>
      <AgentProfile agentId="codex-worker" />
    </QueryClientProvider>,
  )
  await screen.findByText('Codex Worker')
  const trigger = screen.getByTestId('tab-runtime')
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
}

/** Adds one env override row (the "key that fixes a broken sign-in") and
 *  commits it — the key commits on blur per EnvironmentOverridesEditor. */
async function addEnvKey(key: string, value: string) {
  fireEvent.click(await screen.findByTestId('profile-env-add'))
  const keyInput = await screen.findByLabelText('Environment variable name')
  const valueInput = screen.getByLabelText('Environment variable value')
  fireEvent.change(keyInput, { target: { value: key } })
  fireEvent.change(valueInput, { target: { value } })
  fireEvent.blur(keyInput)
}

beforeEach(() => {
  mockNavigate.mockClear()
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(codexWorker())
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
  vi.mocked(updateAgent).mockReset().mockResolvedValue(codexWorker())
  vi.mocked(testAgentRunner).mockReset().mockResolvedValue({ ok: true, reason: '', message: 'ready', cli: 'codex' })
  vi.mocked(fetchCliDetect).mockReset().mockResolvedValue({
    claude: { installed: false, path: null, source: null },
    codex: { installed: false, path: null, source: null },
    opencode: { installed: false, path: null, source: null },
  })
  vi.mocked(fetchCliValidate).mockReset().mockResolvedValue({
    ok: true,
    reason: 'ok',
    resolved_path: '/opt/homebrew/bin/codex',
    version: '1.2.3',
    detail: 'OK',
  })
  vi.mocked(fetchExecutorPreview).mockReset().mockResolvedValue({
    binary: 'codex',
    argv: [],
    command_line: 'codex',
    prompt_delivery: 'stdin',
    dropped_args: [],
  })
  vi.mocked(fetchExecutorSmokeTest).mockReset()
  useUiStore.setState({ toasts: [] })
})

describe('AgentProfile Runtime — no pre-save sign-in gate (spec §4; FR-8)', () => {
  it('saving an env override sends no runner test first', async () => {
    await openRuntimeTab()
    await addEnvKey('ANTHROPIC_API_KEY', 'sk-fix-signin-1')

    // The save must go through without any pre-save runner test.
    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
    const payload = vi.mocked(updateAgent).mock.calls[0][1] as {
      executor?: { env_overrides?: Record<string, string> }
    }
    expect(payload.executor?.env_overrides?.ANTHROPIC_API_KEY).toBe('sk-fix-signin-1')
    // THE RED ASSERTION (spec: "the current pre-save testAgentRunner gate …
    // is removed"): today the gate fires before every first save.
    expect(testAgentRunner).not.toHaveBeenCalled()
  })

  it('a failed runner test does not block saving the fix', async () => {
    // The runner is broken exactly as the spec's motivation describes — the
    // saved key IS the repair, so the save must not be blocked on it.
    vi.mocked(testAgentRunner).mockResolvedValue({
      ok: false,
      reason: 'unauthenticated',
      message: 'sign-in required',
      cli: 'codex',
    })
    await openRuntimeTab()
    await addEnvKey('ANTHROPIC_API_KEY', 'sk-fix-signin-2')

    await waitFor(() => expect(updateAgent).toHaveBeenCalled(), { timeout: 5000 })
    const payload = vi.mocked(updateAgent).mock.calls[0][1] as {
      executor?: { env_overrides?: Record<string, string> }
    }
    expect(payload.executor?.env_overrides?.ANTHROPIC_API_KEY).toBe('sk-fix-signin-2')
  })
})
