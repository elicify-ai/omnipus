/**
 * AgentTools.test.tsx
 *
 * Tests for:
 *   - B-2 (#332 / US-D5): locked agent renders read-only editor, no write fires
 *   - B-1 (#331 / US-D4): system.* tools (category==='system') appear in the
 *     Advanced disclosure, NOT the primary category grid
 *   - D1 (#334): new agent in CreateAgentModal defaults to Balanced preset
 *
 * These tests use ToolsAndPermissions and CreateAgentModal with mocked API.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool, AgentToolsCfg } from '@/lib/api'

// ── Mocks ──────────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', () => ({
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGlobalToolPolicies: vi.fn(),
  fetchProviders: vi.fn(),
  fetchSkills: vi.fn().mockResolvedValue([]),
  createAgent: vi.fn(),
  isApiError: vi.fn(() => false),
}))

vi.mock('@/hooks/useAutoSave', () => ({
  useAutoSave: vi.fn(() => ({ status: 'idle', error: undefined, lastSavedAt: undefined, saveNow: vi.fn() })),
}))

vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))

vi.mock('./MCPServerPicker', () => ({
  MCPServerPicker: () => null,
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({
    createAgentModalOpen: true,
    closeCreateAgentModal: vi.fn(),
    addToast: vi.fn(),
  }),
}))

import * as api from '@/lib/api'
import * as autoSaveModule from '@/hooks/useAutoSave'
import { ToolsAndPermissions } from './ToolsAndPermissions'

// ── Fixtures ───────────────────────────────────────────────────────────────────

const SYSTEM_TOOL: RegistryTool = {
  name: 'system.config.set',
  scope: 'core',         // scope is 'core', NOT 'system' — B-1: must filter by category
  category: 'system',   // category==='system' → goes in Advanced disclosure
  description: 'Set system config',
  source: 'builtin',
}

const FILESYSTEM_TOOL: RegistryTool = {
  name: 'read_file',
  scope: 'general',
  category: 'filesystem',
  description: 'Read file contents',
  source: 'builtin',
}

const MCP_TOOL: RegistryTool = {
  name: 'mcp_github_search',
  scope: 'general',
  category: 'web' as string,
  description: 'Search GitHub via MCP',
  source: 'mcp',
}

const DEFAULT_TOOLS_CFG: AgentToolsCfg = {
  builtin: { default_policy: 'allow', policies: {} },
}

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
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([SYSTEM_TOOL, FILESYSTEM_TOOL, MCP_TOOL])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([SYSTEM_TOOL, FILESYSTEM_TOOL, MCP_TOOL])
  vi.mocked(api.fetchAgentTools).mockResolvedValue({
    config: DEFAULT_TOOLS_CFG,
    tools: [],
  })
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue({
    default_policy: 'allow',
    policies: {},
  })
  vi.mocked(api.fetchProviders).mockResolvedValue([])
})

// ── US-1 / AC5 / FR-103: system.* tools appear in the flat category grid ──────
// (The old system-disclosure-wrapper §3 is removed per Issue #357.)

describe('US-1: system.* tools appear in the flat category grid, not a separate disclosure (#357)', () => {
  it('renders system.* tool inside the category grid (no separate system-disclosure-wrapper)', async () => {
    render(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    await waitFor(() => {
      // The system-disclosure-wrapper must NOT be present (removed in #357).
      expect(document.querySelector('[data-testid="system-disclosure-wrapper"]')).not.toBeInTheDocument()
      // The category grid must be present with the system category pill.
      expect(document.querySelector('[data-testid="category-grid"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="category-pill-system"]')).toBeInTheDocument()
    })
  })

  it('a payload with both system.* and regular tools: both appear in the category grid', async () => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([
      SYSTEM_TOOL,
      FILESYSTEM_TOOL,
      { name: 'system.exec.run', scope: 'core', category: 'system', description: 'Run exec', source: 'builtin' as const },
    ])
    vi.mocked(api.fetchAgentTools).mockResolvedValue({ config: DEFAULT_TOOLS_CFG, tools: [] })

    render(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    await waitFor(() => {
      // No separate system disclosure (removed in #357).
      expect(document.querySelector('[data-testid="system-disclosure-wrapper"]')).not.toBeInTheDocument()
      // The category grid must include both the system category and the filesystem category.
      const categoryGrid = document.querySelector('[data-testid="category-grid"]')
      expect(categoryGrid).toBeInTheDocument()
      expect(document.querySelector('[data-testid="category-pill-system"]')).toBeInTheDocument()
      expect(document.querySelector('[data-testid="category-pill-filesystem"]')).toBeInTheDocument()
    })
  })

  it('MCP tool with unset category goes to the MCP section, not the category grid', async () => {
    // An MCP tool with no category must go to the MCP section.
    const mcpToolNoCategory: RegistryTool = {
      name: 'mcp_server_action',
      scope: 'general',
      category: undefined as unknown as string, // unset category — real-world edge case
      description: 'Some MCP action',
      source: 'mcp',
    }
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([mcpToolNoCategory])

    render(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    await waitFor(() => {
      // No system-disclosure-wrapper (removed in #357).
      expect(document.querySelector('[data-testid="system-disclosure-wrapper"]')).not.toBeInTheDocument()
      // MCP tools section should be present.
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).toBeInTheDocument()
    })
  })
})

// ── B-2 (#332 / US-D5): locked agent → read-only, no write ────────────────────

describe('B-2: locked agent renders read-only, no write fires (#332)', () => {
  it('shows read-only notice when isLocked=true', async () => {
    render(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isLocked={true}
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(screen.getByTestId('locked-agent-readonly-notice')).toBeInTheDocument()
    })
  })

  it('does NOT show read-only notice when isLocked=false', async () => {
    render(
      <ToolsAndPermissions
        agentId="agent-1"
        agentType="Main"
        isLocked={false}
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(screen.queryByTestId('locked-agent-readonly-notice')).not.toBeInTheDocument()
    })
  })

  it('disables auto-save when isLocked=true — useAutoSave receives disabled:true', async () => {
    const useAutoSaveSpy = vi.spyOn(autoSaveModule, 'useAutoSave')
    useAutoSaveSpy.mockReturnValue({ status: 'idle', error: undefined, lastSavedAt: undefined, saveNow: vi.fn() })

    render(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isLocked={true}
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    await waitFor(() => {
      // The auto-save hook must have been called with disabled:true
      const calls = useAutoSaveSpy.mock.calls
      const lastCall = calls[calls.length - 1]
      expect(lastCall[2]).toMatchObject({ disabled: true })
    })

    useAutoSaveSpy.mockRestore()
  })

  it('does NOT call updateAgentTools for a locked agent', async () => {
    render(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isLocked={true}
        tools={DEFAULT_TOOLS_CFG}
        onChange={() => {}}
      />,
      { wrapper },
    )

    // Wait for render to settle
    await waitFor(() => {
      expect(document.querySelector('[data-testid="tool-policy-editor"]')).toBeInTheDocument()
    })

    // No update should have been called
    expect(api.updateAgentTools).not.toHaveBeenCalled()
  })
})

// ── D1 (#334): Create-Agent defaults to Balanced ───────────────────────────────
// The legacy 2-tab modal's Tools & Permissions tab + Balanced default are
// folded into the new wizard's Advanced step, which is deferred per
// CreateAgentWizard.tsx's file header. These tests are kept as `.skip`
// so a follow-up PR can light them up when the Advanced step lands —
// the Tools editor + Balanced preset are part of #334 follow-up work.

describe.skip('D1: Create-Agent defaults to Balanced preset (#334) — deferred to Advanced step', () => {
  it('the Tools tab is present and the Balanced preset is the default state', () => {})
  it('submits with Balanced default policy when no changes made', () => {})
})
