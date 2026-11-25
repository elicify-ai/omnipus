import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const mockRunGated = vi.fn(async (fn: (token: string) => unknown) => fn(''))

// The agent-tools edge of the API module is doubled. The global value is not:
// the real client reads it from GET /security/permissions through the fetch
// double below (C-22 row 6 retires GET /security/tool-policies, this tab's
// last reader), so a held or failed read of it is a held or failed fetch.
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchRegistryTools: vi.fn(),
  fetchBuiltinTools: vi.fn(),
  fetchAgentTools: vi.fn(),
  fetchMcpServersForAgent: vi.fn(),
  updateAgentTools: vi.fn(),
  fetchGodMode: vi.fn(),
  reAuth: vi.fn(),
  isApiError: vi.fn(() => false),
}))

vi.mock('@/components/settings/useReAuthGate', () => ({
  useReAuthGate: () => ({ runGated: mockRunGated, dialog: null, open: false }),
  isReAuthCancelled: vi.fn(() => false),
}))

vi.mock('@/components/ui/AutoSaveIndicator', () => ({
  AutoSaveIndicator: () => null,
}))

vi.mock('@/store/ui', () => ({
  useUiStore: (selector: (s: { addToast: () => void }) => unknown) => selector({ addToast: vi.fn() }),
}))

import * as api from '@/lib/api'
import type { RegistryTool, AgentToolsCfg, McpServer } from '@/lib/api'
import { ToolsAndPermissions } from './ToolsAndPermissions'
import { useSkillPermissionNetwork } from '../../../tests/helpers/skill-permission-network'

// The global value the badges are floored by (Skills & Tools). These are the
// two values this file's earlier global-policies double returned; a test that
// needs another says so in its own Given. Only the fetch edge is doubled.
const permissions = useSkillPermissionNetwork(() => ({
  policies: { read_file: 'allow', ToolSearch: 'allow' },
  skill_policies: {}, visibility: {}, skill_visibility: {}, skill_trust: 'ask',
}))

const READ_FILE: RegistryTool = {
  name: 'read_file',
  scope: 'general',
  category: 'filesystem',
  description: 'Read file contents',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}
const TOOL_SEARCH: RegistryTool = {
  name: 'ToolSearch',
  scope: 'core',
  category: 'system',
  description: 'Discover deferred tools',
  source: 'builtin',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}
const REVISION = '2'.repeat(64)
const SERVER: McpServer = {
  id: 'docs',
  name: 'Docs',
  enabled: true,
  transport: 'stdio',
  status: 'connected',
  tool_count: 2,
}

function renderWithQuery(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

beforeEach(() => {
  vi.clearAllMocks()
  mockRunGated.mockImplementation(async (fn: (token: string) => unknown) => fn(''))
  vi.mocked(api.fetchRegistryTools).mockResolvedValue([READ_FILE, TOOL_SEARCH])
  vi.mocked(api.fetchAgentTools).mockResolvedValue({
    revision: REVISION,
    override_names: ['read_file'],
    config: { builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } }, mcp: { servers: [] } },
    tools: [],
    skills: [],
  })
  vi.mocked(api.fetchGodMode).mockResolvedValue({ enabled: false, persisted: false, available: true, supported: true })
  vi.mocked(api.fetchMcpServersForAgent).mockResolvedValue([SERVER])
  vi.mocked(api.updateAgentTools).mockImplementation(async (_id, cfg) => ({
    revision: REVISION,
    override_names: cfg.override_names,
    config: cfg.config ?? { builtin: { policies: {} } },
    tools: [],
    skills: [],
  }))
})

afterEach(() => { vi.useRealTimers() })

describe('ToolsAndPermissions — sparse inherit and discovery', () => {
  it('removes a local override instead of rewriting the catalog', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        tools={{ builtin: { policies: { read_file: 'deny' } } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    // The editor is on screen when its category grid is (this wait used to be
    // the role-preset button, retired by US-3 AS1).
    await screen.findByTestId('category-grid')
    fireEvent.click(screen.getByRole('button', { name: /Files/ }))
    const inherit = await screen.findByTestId('inherit-read_file')
    // The Inherit button renders before the tools draft hydrates; await
    // enablement so this click is never a pre-hydration no-op.
    await waitFor(() => expect(inherit).toBeEnabled())
    fireEvent.click(inherit)
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'mia',
        expect.objectContaining({ override_names: [] }),
        '',
      )
    })
  })

  it('does not offer a deny control for ToolSearch discovery', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    // The system category's pill is the control this test needs; it renders
    // with the editor (this wait used to be the retired role-preset button).
    const systemPill = await screen.findByTestId('category-pill-system')
    fireEvent.click(systemPill.closest('button')!)
    expect(await screen.findByTestId('toolsearch-always-available')).toBeInTheDocument()
    expect(screen.queryByTestId('inherit-ToolSearch')).not.toBeInTheDocument()
    const discoveryRow = screen.getByTestId('tool-row-ToolSearch')
    expect(discoveryRow.querySelector('[data-testid="toolsearch-always-available"]')).not.toBeNull()
    expect(discoveryRow.textContent ?? '').not.toMatch(/\bDeny\b/)
  })

  it('assigns an installed connector through the mounted picker', async () => {
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('mcp-assignment-section')).toBeInTheDocument())
    // Controls stay disabled until the agent-tools GET hydrates the draft;
    // await enablement so this click is never a pre-hydration edit.
    await waitFor(() => expect(screen.getByLabelText('Assign Docs')).toBeEnabled())
    fireEvent.click(screen.getByLabelText('Assign Docs'))
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalledWith(
        'mia',
        expect.objectContaining({
          config: expect.objectContaining({
            mcp: { servers: [{ id: 'docs' }] },
          }),
        }),
        '',
      )
    })
  })
})

const MCP_SEARCH: RegistryTool = {
  name: 'search',
  scope: 'general',
  category: 'web',
  description: 'Search docs',
  source: 'mcp',
  server_id: 'docs',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}
const MCP_FETCH: RegistryTool = {
  name: 'fetch',
  scope: 'general',
  category: 'web',
  description: 'Fetch a document',
  source: 'mcp',
  server_id: 'docs',
  visibility: 'full',
  visibility_locked: false,
  permission_fixed: false,
}

function lastToolsConfig() {
  const calls = vi.mocked(api.updateAgentTools).mock.calls
  expect(calls.length).toBeGreaterThan(0)
  return calls[calls.length - 1]![1] as {
    override_names?: string[]
    config?: AgentToolsCfg
  }
}

// The residual-GET, global-value and MCP-binding cases are split across three
// describe blocks so each callback stays under the function-size budget
// (scripts/check-function-budget.sh); they keep their original order.
describe('ToolsAndPermissions — MCP bindings: policy-only saves and unassign', () => {
  it('keeps assigned connectors when a policy-only inherit is saved', async () => {
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: {
        builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } },
        mcp: { servers: [{ id: 'docs' }] },
      },
      tools: [],
      skills: [],
    })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: { read_file: 'deny' } } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('mcp-assignment-section')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /Files/ }))
    const inherit = await screen.findByTestId('inherit-read_file')
    await waitFor(() => expect(inherit).toBeEnabled())
    fireEvent.click(inherit)
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalled()
    })
    expect(lastToolsConfig().config?.mcp).toEqual({ servers: [{ id: 'docs' }] })
  })

  it('does not send an empty connector list on a policy-only save when GET omitted mcp', async () => {
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: { builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } } },
      tools: [],
      skills: [],
    })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: { read_file: 'deny' } } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('mcp-assignment-section')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /Files/ }))
    const inherit = await screen.findByTestId('inherit-read_file')
    await waitFor(() => expect(inherit).toBeEnabled())
    fireEvent.click(inherit)
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalled()
    })
    expect(lastToolsConfig().config?.mcp).toBeUndefined()
  })

  it('unassigns the last connector as an explicit empty list', async () => {
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: {
        builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } },
        mcp: { servers: [{ id: 'docs' }] },
      },
      tools: [],
      skills: [],
    })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await waitFor(() => expect(screen.getByLabelText('Unassign Docs')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Unassign Docs'))
    await waitFor(() => {
      expect(lastToolsConfig().config?.mcp).toEqual({ servers: [] })
    })
  })
})

describe('ToolsAndPermissions — MCP bindings: connector tool subsets', () => {
  it('round-trips a selected connector tool subset', async () => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([READ_FILE, TOOL_SEARCH, MCP_SEARCH, MCP_FETCH])
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: {
        builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } },
        mcp: { servers: [{ id: 'docs', tools: ['search', 'fetch'] }] },
      },
      tools: [],
      skills: [],
    })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('mcp-mode-docs-selected')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('mcp-tool-docs-fetch'))
    await waitFor(() => {
      expect(lastToolsConfig().config?.mcp).toEqual({
        servers: [{ id: 'docs', tools: ['search'] }],
      })
    })
  })

  it('round-trips explicit no-tools on an assigned connector', async () => {
    vi.mocked(api.fetchRegistryTools).mockResolvedValue([READ_FILE, TOOL_SEARCH, MCP_SEARCH, MCP_FETCH])
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: {
        builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } },
        mcp: { servers: [{ id: 'docs' }] },
      },
      tools: [],
      skills: [],
    })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('mcp-mode-docs-all')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('mcp-mode-docs-none'))
    await waitFor(() => {
      expect(lastToolsConfig().config?.mcp).toEqual({
        servers: [{ id: 'docs', tools: [] }],
      })
    })
  })
})

describe('ToolsAndPermissions — residual GET failures, global-value gating, and overrides', () => {
  it('surfaces an agent-tools GET failure with retry', async () => {
    vi.mocked(api.fetchAgentTools)
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({
        revision: REVISION,
        override_names: ['read_file'],
        config: { builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } }, mcp: { servers: [] } },
        tools: [],
        skills: [],
      })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    expect(await screen.findByTestId('agent-tools-load-error')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-policy-editor')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('agent-tools-retry'))
    expect(await screen.findByTestId('tool-policy-editor')).toBeInTheDocument()
  })

  // The role-preset half of this case ("disables presets and individual
  // policies ...") is retired with the presets (US-3 AS1, T-Q6 A; CHECK round 4,
  // candidate 9). The individual-policy half is kept, on the same oracle: a tab
  // whose global value has not loaded edits nothing (state table, "Agent Tools
  // tab": loading "as today"; global value missing "read-only").
  it('keeps individual policies disabled until the global ceiling has loaded', async () => {
    // The global value's read stays unanswered; every other read lands.
    permissions().holdReads()
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await screen.findByTestId('category-grid')
    fireEvent.click(screen.getByRole('button', { name: /Files/ }))
    const row = await screen.findByTestId('tool-row-read_file')
    // The agent's own draft HAS hydrated: the connector control waits for that
    // draft alone and is enabled. So the disabled badge below is the pending
    // global value, not a tab that has not finished loading.
    await waitFor(() => expect(screen.getByLabelText('Assign Docs')).toBeEnabled())
    const allow = within(row).getByRole('button', { name: 'Allow' })
    expect(allow).toBeDisabled()
    fireEvent.click(allow)
    // Past the 500 ms autosave debounce, so an accepted edit would have been
    // sent by now (the earlier check ran before the debounce could fire).
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 700)) })
    expect(api.updateAgentTools).not.toHaveBeenCalled()
  })

  it('surfaces a global ceiling GET failure and enables policy editing only after retry', async () => {
    // The first read of the global value fails; the read after Retry answers.
    permissions().failNextRead(500, 'offline')
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )

    expect(await screen.findByTestId('global-policies-load-error')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Files/ }))
    const row = await screen.findByTestId('tool-row-read_file')
    expect(within(row).getByRole('button', { name: 'Allow' })).toBeDisabled()
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('global-policies-retry'))
    await waitFor(() => {
      expect(screen.queryByTestId('global-policies-load-error')).not.toBeInTheDocument()
      expect(within(row).getByRole('button', { name: 'Allow' })).not.toBeDisabled()
    })
  })

  it('surfaces an MCP server GET failure and restores the hydrated assignment after retry', async () => {
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: {
        builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } },
        mcp: { servers: [{ id: 'docs' }] },
      },
      tools: [],
      skills: [],
    })
    vi.mocked(api.fetchMcpServersForAgent)
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue([SERVER])
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        isMcpEditable
        tools={{ builtin: { policies: {} } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )

    expect(await screen.findByTestId('mcp-servers-load-error')).toBeInTheDocument()
    expect(screen.queryByText('No MCP servers configured. Add servers on the Skills & Tools screen.')).not.toBeInTheDocument()
    expect(screen.getByTestId('mcp-servers-load-error')).toHaveTextContent('Existing connector assignments are preserved.')
    expect(api.updateAgentTools).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('mcp-servers-retry'))
    expect(await screen.findByLabelText('Unassign Docs')).toBeInTheDocument()
    expect(api.updateAgentTools).not.toHaveBeenCalled()
  })

  it('keeps a same-ceiling single-tool click as an explicit override', async () => {
    vi.mocked(api.fetchAgentTools).mockResolvedValue({
      revision: REVISION,
      override_names: ['read_file'],
      config: { builtin: { policies: { read_file: 'deny', ToolSearch: 'allow' } }, mcp: { servers: [] } },
      tools: [],
      skills: [],
    })
    renderWithQuery(
      <ToolsAndPermissions
        agentId="mia"
        agentType="core"
        isEditable
        tools={{ builtin: { policies: { read_file: 'deny' } } } as AgentToolsCfg}
        onChange={() => {}}
      />,
    )
    await screen.findByTestId('category-grid')
    fireEvent.click(screen.getByRole('button', { name: /Files/ }))
    const row = await screen.findByTestId('tool-row-read_file')
    // The badge renders before the tools draft hydrates; await enablement
    // so this click is never a pre-hydration no-op.
    const allow = within(row).getByRole('button', { name: 'Allow' })
    await waitFor(() => expect(allow).toBeEnabled())
    fireEvent.click(allow)
    await waitFor(() => {
      expect(api.updateAgentTools).toHaveBeenCalled()
    })
    const payload = lastToolsConfig()
    expect(payload.override_names).toEqual(['read_file'])
    expect(payload.config?.builtin?.policies?.read_file).toBe('allow')
  })
})
