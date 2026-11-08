import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react'
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
  revision: '0'.repeat(64),
  editable_fields: [{ name: 'executor', editable: true }, { name: 'model', editable: true }, { name: 'name', editable: true }],
  id: 'external-worker',
  name: 'External Worker',
  type: 'subagent_3p',
  locked: false,
  needs_model: false,
  status: 'active',
  model: 'claude-sonnet-4-6',
  description: 'Delegates to an external CLI',
  // The pinned REST Agent contract requires this legacy slot. Empty here;
  // this UI fixture does not assert get_agent's omission of external soul.
  soul: '',
  max_tool_iterations: 20,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  executor: { kind: 'external-cli', cli: 'claude-code', effective_cli_path: '/opt/t8/cli-v2' },
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  memory_enabled: true,
  figure: 'Omnipus',
  role: 'general',
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
  act(() => {
    trigger.focus()
    fireEvent.keyDown(trigger, { key: 'Enter' })
    fireEvent.click(trigger)
  })
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

// Oracle: tool-parity r17 17.6. Contract-first consumers may already satisfy
// this; an observed baseline pass is not claimed as a new red-first test.
describe('T8 Runtime program is read-only', () => {
  it('17.6 renders the Settings path as text and links to Settings, with no path input', async () => {
    renderProfile('external-worker')
    await screen.findByText('External Worker')
    switchTab('tab-runtime')
    await waitFor(() => expect(screen.getByTestId('profile-program-path')).toHaveTextContent('/opt/t8/cli-v2'))
    const path = screen.getByTestId('profile-program-path')
    expect(path.matches('input, textarea, [contenteditable]')).toBe(false)
    expect(path.querySelectorAll('input, textarea, [contenteditable]')).toHaveLength(0)
    const legacyContainer = screen.queryByTestId('profile-cli-path')
    expect(legacyContainer?.matches('input, textarea, [contenteditable]') ?? false).toBe(false)
    expect(legacyContainer?.querySelectorAll('input, textarea, [contenteditable]').length ?? 0).toBe(0)
    const links = screen.getAllByRole('link')
    expect(links.some(link => link.textContent?.includes('Settings') && link.getAttribute('href')?.includes('settings'))).toBe(true)
  })
})
