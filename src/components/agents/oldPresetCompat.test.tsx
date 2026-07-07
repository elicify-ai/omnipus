/**
 * oldPresetCompat.test.tsx
 *
 * US-D2 (#333): originally verified that an agent saved under a removed
 * preset (Unrestricted / Standard / Minimal) loaded and re-saved under the
 * newer {default_policy, policies} schema without mutating the policy on
 * mere render.
 *
 * The wire contract has since dropped `default_policy` entirely — every
 * static builtin tool now MUST have an explicit, literal policy entry in a
 * COMPLETE per-tool map (there is no sparse "overrides + fallback default"
 * shape left anywhere, client or server). This file now verifies the
 * equivalent invariant under the new contract: a persisted COMPLETE policy
 * map — whatever its shape (all-allow, mixed, mostly-deny, arbitrary) — loads
 * and round-trips through ToolsAndPermissions without mutation on mere
 * render. There are no more named presets to be "compatible" with; the
 * invariant that matters is round-trip identity of an explicit map.
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
    policies: {},
  })
})

// ── Compatibility tests ────────────────────────────────────────────────────────

describe('oldPresetCompat: a persisted complete policy map round-trips unchanged (#333)', () => {
  /**
   * Equivalent of the retired "Unrestricted" shape: every known tool is
   * explicitly allowed. There is no `default_policy: 'allow'` shortcut for
   * this anymore — the map itself carries an explicit 'allow' per tool.
   */
  it('an all-allow complete map loads unchanged (no mutation on render)', async () => {
    const allAllowCfg: AgentToolsCfg = {
      builtin: { policies: { read_file: 'allow', exec: 'allow', web_search: 'allow' } },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: allAllowCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-all-allow"
        agentType="Main"
        tools={allAllowCfg}
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
   * Equivalent of the retired "Standard" shape: exec requires confirmation,
   * everything else is allowed — expressed as an explicit, complete map
   * rather than a default + one override.
   */
  it('a mixed complete map (exec:ask, others:allow) loads unchanged', async () => {
    const mixedCfg: AgentToolsCfg = {
      builtin: {
        policies: {
          read_file: 'allow',
          exec: 'ask',
          web_search: 'allow',
        },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: mixedCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-mixed"
        agentType="Main"
        tools={mixedCfg}
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
   * Equivalent of the retired "Minimal" shape: mostly denied, with a couple
   * of tools explicitly allowed — again expressed as a complete map, not a
   * deny default plus allow overrides.
   */
  it('a mostly-deny complete map loads unchanged', async () => {
    const mostlyDenyCfg: AgentToolsCfg = {
      builtin: {
        policies: {
          read_file: 'allow',
          exec: 'deny',
          web_search: 'allow',
        },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: mostlyDenyCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-mostly-deny"
        agentType="Main"
        tools={mostlyDenyCfg}
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
   * An arbitrary custom policy (not matching any preset's expanded shape)
   * must round-trip without mutation.
   */
  it('an arbitrary custom complete map round-trips without mutation', async () => {
    const customCfg: AgentToolsCfg = {
      builtin: {
        policies: {
          read_file: 'allow',
          exec: 'deny',
          web_search: 'ask',
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

  /**
   * New regression coverage for the removed-fallback fix: a genuinely
   * INCOMPLETE map (a tool missing from `policies` entirely) must still
   * render without crashing or mutating — the missing tool just shows as
   * "unconfigured" in the editor (ToolPolicyEditor.test.tsx covers the
   * visual state in detail). This should never happen with real server data
   * (coverage is validated at boot and at every write) but the frontend must
   * degrade safely, not silently invent an 'allow'.
   */
  it('an incomplete map (a tool missing entirely) still renders without mutation', async () => {
    const incompleteCfg: AgentToolsCfg = {
      builtin: {
        policies: {
          read_file: 'allow',
          // 'exec' and 'web_search' intentionally absent.
        },
      },
    }
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      config: incompleteCfg,
      tools: [],
    })

    const onChange = vi.fn()

    render(
      <ToolsAndPermissions
        agentId="agent-incomplete"
        agentType="Main"
        tools={incompleteCfg}
        onChange={onChange}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })

    // No mutation on mere render, even with a gap in the map.
    expect(onChange).not.toHaveBeenCalled()
  })
})
