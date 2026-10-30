/**
 * AgentProfile.presetLine.test.tsx — RED pack for T11 slice B, the Runtime
 * tab's read-only preset line (t11 spec §4 "Agent editor (Runtime tab)";
 * §8 FR-8 first clause; spec §7 reachability U-5).
 *
 * Oracles:
 *  - The editor shows the CLI's SAVED PATH AND PARAMETERS read-only, with a
 *    link to Settings (§4). The program path half (profile-program-path,
 *    profile-program-warning, "Not set") already shipped in the T8
 *    contract-first commit 1243ed887 — those assertions are regression pins,
 *    marked as such; they cannot go red without breaking production. The
 *    preset-parameters display and the Settings link DO NOT exist yet —
 *    those assertions are the RED pair of this file.
 *  - program_warning's text is the contract's exact refusal copy
 *    (ExecutorConfig.program_warning, 1243ed887).
 *  - Per-agent environment values and extra flags stay editable (§4) — pin.
 *
 * Same rendering/mocking pattern as AgentProfile.commandPreview.test.tsx.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, fireEvent, waitFor } from '@testing-library/react'
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

// Codex worker carrying a saved program and the founder's canonical preset
// (§9 U-4: "--dangerously-bypass-approvals-and-sandbox"). effective_cli_path /
// effective_preset_args / program_warning are the read-only fields T8's
// contract defines (ExecutorConfig, 1243ed887).
const NO_PATH_WARNING =
  'This agent cannot run: no program path is set for Codex. A person can set it in Settings -> Third-party agents -> Codex.'

function codexWorker(overrides: Partial<NonNullable<Agent['executor']>> = {}): Agent {
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
      effective_preset_args: '--dangerously-bypass-approvals-and-sandbox --model gpt-5-codex',
      ...overrides,
    },
    memory_enabled: true,
    figure: 'Omnipus',
    role: 'general',
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(agent: Agent) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentProfile agentId={agent.id} />
    </QueryClientProvider>,
  )
}

async function openRuntimeTab(agent: Agent) {
  renderProfile(agent)
  await screen.findByText(agent.name)
  const trigger = screen.getByTestId('tab-runtime')
  trigger.focus()
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
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

describe('AgentProfile Runtime — the preset line is read-only and links to Settings (spec §4; FR-8)', () => {
  it('shows the saved preset parameters read-only inside the Program block', async () => {
    await openRuntimeTab(codexWorker())

    const block = screen.getByTestId('profile-cli-path')
    // The person's saved parameters are VISIBLE in the editor…
    await waitFor(() => {
      expect(block).toHaveTextContent('--dangerously-bypass-approvals-and-sandbox')
      expect(block).toHaveTextContent('--model gpt-5-codex')
    })
    // …and READ-ONLY: no editable control exists anywhere in the block.
    expect(block.querySelectorAll('input, textarea, [contenteditable]')).toHaveLength(0)
  })

  it('links to Settings from the Program block', async () => {
    await openRuntimeTab(codexWorker())

    const block = screen.getByTestId('profile-cli-path')
    const link = within(block).getByRole('link', { name: /settings/i })
    expect(link).toHaveAttribute('href', expect.stringContaining('settings'))
  })
})

describe('AgentProfile Runtime — program path and warning (regression pins, shipped in 1243ed887)', () => {
  // These assertions were already satisfied by the T8 contract-first commit
  // before this RED pack existed; they pin the shipped behaviour the
  // preset-parameters work must not disturb and cannot go red without a
  // production regression.

  it('shows the saved program path while a run would start', async () => {
    await openRuntimeTab(codexWorker())

    expect(await screen.findByTestId('profile-program-path')).toHaveTextContent('/opt/homebrew/bin/codex')
    expect(screen.queryByTestId('profile-program-warning')).not.toBeInTheDocument()
  })

  it('shows Not set and the run warning verbatim when no program is saved', async () => {
    await openRuntimeTab(
      codexWorker({ effective_cli_path: undefined, effective_preset_args: undefined, program_warning: NO_PATH_WARNING }),
    )

    expect(await screen.findByTestId('profile-program-path')).toHaveTextContent('Not set')
    expect(screen.getByTestId('profile-program-warning')).toHaveTextContent(NO_PATH_WARNING)
  })

  it('keeps per-agent environment values and extra flags editable (§4)', async () => {
    await openRuntimeTab(codexWorker())

    const add = await screen.findByTestId('profile-env-add')
    expect(add).toBeEnabled()
    fireEvent.click(add)
    const keyInput = await screen.findByLabelText('Environment variable name')
    expect(keyInput).toBeEnabled()
    const argsInput = screen.getByTestId('profile-cli-args').querySelector('input') as HTMLInputElement
    expect(argsInput).toBeEnabled()
  })
})
