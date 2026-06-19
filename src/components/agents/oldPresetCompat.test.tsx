/**
 * oldPresetCompat.test.tsx
 *
 * US-D2 (#333): An agent saved under a removed preset (Unrestricted / Standard
 * / Minimal) loads and re-saves under the new schema without mutating the
 * policy on mere render.
 *
 * The ToolPolicyEditor (and ToolsAndPermissions wrapping it) MUST round-trip
 * any policy value byte-identically — loading a value and saving unchanged
 * emits a byte-identical payload. No mutation on render.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AgentToolsCfg, RegistryTool } from '@/lib/api'

// ── Mocks ──────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', () => ({
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGlobalToolPolicies: vi.fn(),
  isApiError: vi.fn(() => false),
}))

vi.mock('@/hooks/useAutoSave', () => ({
  useAutoSave: vi.fn(() => ({ status: 'idle', error: null })),
}))

vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))

vi.mock('./MCPServerPicker', () => ({
  MCPServerPicker: () => null,
}))

import * as api from '@/lib/api'
import { ToolsAndPermissions } from './ToolsAndPermissions'

// ── Fixtures ───────────────────────────────────────────────────────────────────

const SOME_TOOLS: RegistryTool[] = [
  { name: 'read_file', scope: 'general', category: 'filesystem', description: 'Read file', source: 'builtin' },
  { name: 'exec', scope: 'general', category: 'exec', description: 'Execute command', source: 'builtin' },
  { name: 'web_search', scope: 'general', category: 'web', description: 'Search web', source: 'builtin' },
]

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0 },
      mutations: { retry: false },
    },
  })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={makeQueryClient()}>
      {children}
    </QueryClientProvider>
  )
}

// ── Setup ──────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchRegistryTools).mockResolvedValue(SOME_TOOLS)
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue(SOME_TOOLS)
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({
    default_policy: 'allow',
    policies: {},
  })
})

// ── Compatibility tests ────────────────────────────────────────────────────────

describe('oldPresetCompat: agents saved under removed presets round-trip unchanged (#333)', () => {
  /**
   * An agent saved under the old "Unrestricted" preset had:
   *   { default_policy: 'allow', policies: {} }
   * The new schema does not have this preset label, but the policy value is
   * identical to 'full_access'. The editor must render and re-save without
   * mutating the policy.
   */
  it('Unrestricted preset (default_policy:allow, no overrides) loads unchanged', async () => {
    const unrestrictedCfg: AgentToolsCfg = {
      builtin: { default_policy: 'allow', policies: {} },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: unrestrictedCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-unrestricted"
        agentType="Main"
        tools={unrestrictedCfg}
        onChange={onChange}
      />,
      { wrapper },
    )

    await waitFor(() => {
      // Editor must render (policy editor present)
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })

    // onChange must NOT have been called just from rendering
    // (no mutation on mere render — round-trip identity)
    expect(onChange).not.toHaveBeenCalled()
  })

  /**
   * An agent saved under the old "Standard" preset had:
   *   { default_policy: 'allow', policies: { exec: 'ask', 'browser.navigate': 'ask', ... } }
   * The new 'balanced' preset has the same structure. The editor must not mutate.
   */
  it('Standard preset (default_policy:allow, exec:ask overrides) loads unchanged', async () => {
    const standardCfg: AgentToolsCfg = {
      builtin: {
        default_policy: 'allow',
        policies: {
          exec: 'ask',
          'browser.navigate': 'ask',
          'browser.click': 'ask',
          'browser.type': 'ask',
          'browser.evaluate': 'deny',
        },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: standardCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-standard"
        agentType="Main"
        tools={standardCfg}
        onChange={onChange}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })

    // No mutation on mere render
    expect(onChange).not.toHaveBeenCalled()
  })

  /**
   * An agent saved under the old "Minimal" preset had:
   *   { default_policy: 'deny', policies: { read_file: 'allow', list_dir: 'allow', ... } }
   * This matches no new preset (closest is 'cautious'). Must still load without mutation.
   */
  it('Minimal preset (default_policy:deny, few allows) loads unchanged', async () => {
    const minimalCfg: AgentToolsCfg = {
      builtin: {
        default_policy: 'deny',
        policies: {
          read_file: 'allow',
          list_dir: 'allow',
          web_search: 'allow',
          web_fetch: 'allow',
          task_list: 'allow',
          agent_list: 'allow',
        },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: minimalCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-minimal"
        agentType="Main"
        tools={minimalCfg}
        onChange={onChange}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })

    // No mutation on mere render
    expect(onChange).not.toHaveBeenCalled()
  })

  /**
   * An arbitrary custom policy (not matching any preset) must round-trip
   * without mutation.
   */
  it('arbitrary custom policy round-trips without mutation', async () => {
    const customCfg: AgentToolsCfg = {
      builtin: {
        default_policy: 'ask',
        policies: {
          read_file: 'allow',
          exec: 'deny',
        },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: customCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-custom"
        agentType="Main"
        tools={customCfg}
        onChange={onChange}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })

    // No mutation on mere render
    expect(onChange).not.toHaveBeenCalled()
  })
})
