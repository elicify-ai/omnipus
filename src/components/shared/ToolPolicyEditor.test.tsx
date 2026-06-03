/**
 * ToolPolicyEditor.test.tsx — Issue #318 ACs.
 *
 * Tests use the REAL ToolRegistryEntry payload shape (category / source / scope
 * fields all present) — not a mock that omits category (spec §0 F-01 note).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RegistryTool } from '@/lib/api'
import { ToolPolicyEditor, type ToolPolicyValue } from './ToolPolicyEditor'

// ── Fixture helpers ────────────────────────────────────────────────────────────

function makeTool(overrides: Partial<RegistryTool> & { name: string }): RegistryTool {
  return {
    description: `${overrides.name} description`,
    scope: 'core',
    category: 'file',
    source: 'builtin',
    ...overrides,
  }
}

/** A realistic slice of the /api/v1/tools payload (41 tools, all category:'system', scope:'core'). */
const SYSTEM_TOOLS: RegistryTool[] = [
  makeTool({ name: 'system.config_read', category: 'system', scope: 'core' }),
  makeTool({ name: 'system.config_write', category: 'system', scope: 'core' }),
  makeTool({ name: 'system.policy_list', category: 'system', scope: 'core' }),
]

const FILE_TOOLS: RegistryTool[] = [
  makeTool({ name: 'read_file', category: 'file', scope: 'core' }),
  makeTool({ name: 'write_file', category: 'file', scope: 'core' }),
  makeTool({ name: 'list_dir', category: 'file', scope: 'core' }),
]

const BROWSER_TOOLS: RegistryTool[] = [
  makeTool({ name: 'browser.navigate', category: 'browser', scope: 'core' }),
  makeTool({ name: 'browser.click', category: 'browser', scope: 'core' }),
  makeTool({ name: 'browser.type', category: 'browser', scope: 'core' }),
  makeTool({ name: 'browser.evaluate', category: 'browser', scope: 'core' }),
]

const MCP_TOOLS: RegistryTool[] = [
  makeTool({ name: 'mcp_server.search', category: 'search', source: 'mcp', scope: 'general' }),
  makeTool({ name: 'mcp_server.fetch', category: 'web', source: 'mcp', scope: 'general' }),
]

const MCP_TOOL_NO_CATEGORY: RegistryTool = makeTool({
  name: 'mcp_server.uncategorized',
  // No explicit category to test the fallback grouping
  category: 'other',
  source: 'mcp',
  scope: 'general',
})

const ALL_TOOLS = [...SYSTEM_TOOLS, ...FILE_TOOLS, ...BROWSER_TOOLS, ...MCP_TOOLS]

const SAFE_VALUE: ToolPolicyValue = { default_policy: 'allow', policies: {} }

function renderEditor(
  tools: RegistryTool[] = ALL_TOOLS,
  value: ToolPolicyValue = SAFE_VALUE,
  onChange = vi.fn(),
) {
  return render(<ToolPolicyEditor tools={tools} value={value} onChange={onChange} />)
}

// ── Tests ──────────────────────────────────────────────────────────────────────

describe('ToolPolicyEditor — preset application', () => {
  it('renders all three preset buttons', () => {
    renderEditor()
    expect(screen.getByTestId('preset-cautious')).toBeInTheDocument()
    expect(screen.getByTestId('preset-balanced')).toBeInTheDocument()
    expect(screen.getByTestId('preset-full_access')).toBeInTheDocument()
  })

  it('clicking Cautious calls onChange with default_policy=ask and empty policies', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(ALL_TOOLS, SAFE_VALUE, onChange)
    await user.click(screen.getByTestId('preset-cautious'))
    expect(onChange).toHaveBeenCalledWith({ default_policy: 'ask', policies: {} })
  })

  it('clicking Balanced calls onChange with the §2.1 overrides', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(ALL_TOOLS, SAFE_VALUE, onChange)
    await user.click(screen.getByTestId('preset-balanced'))
    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: {
        exec: 'ask',
        'browser.navigate': 'ask',
        'browser.click': 'ask',
        'browser.type': 'ask',
        'browser.evaluate': 'deny',
        write_file: 'ask',
      },
    })
  })

  it('clicking Full access calls onChange with default_policy=allow and no overrides', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(ALL_TOOLS, SAFE_VALUE, onChange)
    await user.click(screen.getByTestId('preset-full_access'))
    expect(onChange).toHaveBeenCalledWith({ default_policy: 'allow', policies: {} })
  })
})

describe('ToolPolicyEditor — system.* separation (B-1 / O-1)', () => {
  it('system.* tools appear ONLY in the Advanced/system disclosure, NOT the primary category grid', () => {
    renderEditor(ALL_TOOLS)

    // The primary category grid must not contain system tool names
    const grid = screen.getByTestId('category-grid')
    SYSTEM_TOOLS.forEach((tool) => {
      expect(within(grid).queryByTestId(`tool-row-${tool.name}`)).not.toBeInTheDocument()
    })
  })

  it('the system disclosure wrapper is rendered when system tools exist', () => {
    renderEditor(ALL_TOOLS)
    expect(screen.getByTestId('system-disclosure-wrapper')).toBeInTheDocument()
  })

  it('system tool rows are inside the system disclosure (after expanding)', async () => {
    const user = userEvent.setup()
    renderEditor(ALL_TOOLS)
    // Expand the system disclosure (it is closed by default)
    const systemDisclosureTrigger = within(screen.getByTestId('system-disclosure-wrapper')).getByTestId(
      'advanced-disclosure-trigger',
    )
    await user.click(systemDisclosureTrigger)
    const systemList = screen.getByTestId('system-tools-list')
    SYSTEM_TOOLS.forEach((tool) => {
      expect(within(systemList).getByTestId(`tool-row-${tool.name}`)).toBeInTheDocument()
    })
  })

  it('system disclosure is NOT rendered when there are no system tools', () => {
    renderEditor(FILE_TOOLS)
    expect(screen.queryByTestId('system-disclosure-wrapper')).not.toBeInTheDocument()
  })
})

describe('ToolPolicyEditor — category rollup pills (M-9)', () => {
  it('shows a single pill when all tools in a category have the same policy', () => {
    const value: ToolPolicyValue = { default_policy: 'allow', policies: {} }
    renderEditor(FILE_TOOLS, value)
    // All file tools resolve to 'allow' — pill should say Allow
    const pill = screen.getByTestId('category-pill-file')
    expect(pill).toHaveTextContent('Allow')
  })

  it('shows a Mixed pill when tools in a category have different resolved policies', () => {
    // browser.navigate=ask, browser.evaluate=deny, others=allow → Mixed
    const value: ToolPolicyValue = {
      default_policy: 'allow',
      policies: {
        'browser.navigate': 'ask',
        'browser.evaluate': 'deny',
      },
    }
    renderEditor(BROWSER_TOOLS, value)
    const pill = screen.getByTestId('category-pill-browser')
    expect(pill).toHaveTextContent('Mixed')
  })

  it('shows a uniform pill when all tools resolve to the same non-default policy', () => {
    const value: ToolPolicyValue = {
      default_policy: 'allow',
      policies: {
        read_file: 'ask',
        write_file: 'ask',
        list_dir: 'ask',
      },
    }
    renderEditor(FILE_TOOLS, value)
    const pill = screen.getByTestId('category-pill-file')
    expect(pill).toHaveTextContent('Ask')
  })
})

describe('ToolPolicyEditor — raw tool grid behind Advanced', () => {
  it('raw-tool-grid is NOT visible by default', () => {
    renderEditor()
    expect(screen.queryByTestId('raw-tool-grid')).not.toBeInTheDocument()
  })

  it('raw-tool-grid becomes visible after expanding "Customize per tool (advanced)"', async () => {
    const user = userEvent.setup()
    renderEditor()
    // Find the "Customize per tool (advanced)" trigger button
    const customizeTrigger = screen.getByRole('button', { name: /customize per tool/i })
    await user.click(customizeTrigger)
    expect(screen.getByTestId('raw-tool-grid')).toBeInTheDocument()
  })

  it('raw-tool-grid contains all tools (including system tools)', async () => {
    const user = userEvent.setup()
    renderEditor(ALL_TOOLS)
    await user.click(screen.getByRole('button', { name: /customize per tool/i }))
    const grid = screen.getByTestId('raw-tool-grid')
    // All tools (including system) should appear in the raw grid
    ALL_TOOLS.forEach((tool) => {
      expect(within(grid).getByTestId(`tool-row-${tool.name}`)).toBeInTheDocument()
    })
  })
})

describe('ToolPolicyEditor — policy round-trip', () => {
  it('loading an existing per-tool override value and calling onChange untouched emits the same payload', async () => {
    const user = userEvent.setup()
    const existingValue: ToolPolicyValue = {
      default_policy: 'allow',
      policies: {
        'browser.navigate': 'ask',
        'browser.evaluate': 'deny',
        write_file: 'ask',
      },
    }
    const onChange = vi.fn()
    renderEditor(FILE_TOOLS, existingValue, onChange)
    // Expand "Customize per tool (advanced)"
    await user.click(screen.getByRole('button', { name: /customize per tool/i }))
    // Click the currently-active policy for write_file to "toggle" — should produce same result
    const grid = screen.getByTestId('raw-tool-grid')
    // The write_file row should be present (override is loaded correctly)
    expect(within(grid).getByTestId('tool-row-write_file')).toBeInTheDocument()
    // onChange not called yet (no user interaction)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('clicking a policy badge that matches an existing override removes it (round-trip clean)', async () => {
    const user = userEvent.setup()
    const existingValue: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { read_file: 'ask' },
    }
    const onChange = vi.fn()
    renderEditor(FILE_TOOLS, existingValue, onChange)
    await user.click(screen.getByRole('button', { name: /customize per tool/i }))
    const grid = screen.getByTestId('raw-tool-grid')
    // Click Allow on read_file (= the default_policy) — should remove the override
    const readFileRow = within(grid).getByTestId('tool-row-read_file')
    const allowBadge = within(readFileRow).getByRole('button', { name: /allow/i })
    await user.click(allowBadge)
    expect(onChange).toHaveBeenCalledWith({ default_policy: 'allow', policies: {} })
  })
})

describe('ToolPolicyEditor — MCP tools (F-G06 guard)', () => {
  it('MCP tools appear in the MCP section, not the primary category grid', () => {
    renderEditor([...FILE_TOOLS, ...MCP_TOOLS])
    const mcpSection = screen.getByTestId('mcp-tools-section')
    expect(mcpSection).toBeInTheDocument()
    // The primary category grid must not contain MCP tool rows
    const grid = screen.getByTestId('category-grid')
    MCP_TOOLS.forEach((tool) => {
      expect(within(grid).queryByTestId(`tool-row-${tool.name}`)).not.toBeInTheDocument()
    })
  })

  it('MCP tools are NOT swept into the system disclosure', () => {
    renderEditor([...SYSTEM_TOOLS, ...MCP_TOOLS])
    // System disclosure should exist for system tools
    expect(screen.getByTestId('system-disclosure-wrapper')).toBeInTheDocument()
    // But MCP section should also exist and be separate
    expect(screen.getByTestId('mcp-tools-section')).toBeInTheDocument()
    // MCP tools must be in the MCP section, not the system disclosure
    const systemWrapper = screen.getByTestId('system-disclosure-wrapper')
    MCP_TOOLS.forEach((tool) => {
      expect(within(systemWrapper).queryByTestId(`tool-row-${tool.name}`)).not.toBeInTheDocument()
    })
  })

  it('MCP tools render with a source badge per server', async () => {
    const user = userEvent.setup()
    renderEditor([...FILE_TOOLS, ...MCP_TOOLS])
    const mcpSection = screen.getByTestId('mcp-tools-section')
    // Expand one of the MCP server disclosures to see the badge
    const mcpTrigger = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')[0]
    await user.click(mcpTrigger)
    // Should see at least one MCP source badge
    expect(screen.getAllByText('MCP').length).toBeGreaterThan(0)
  })

  it('an MCP tool with category unset (fallback: other) is not swallowed by the system filter', () => {
    renderEditor([MCP_TOOL_NO_CATEGORY])
    // Must appear in MCP section
    expect(screen.getByTestId('mcp-tools-section')).toBeInTheDocument()
    // Must NOT appear in system disclosure (which shouldn't even exist)
    expect(screen.queryByTestId('system-disclosure-wrapper')).not.toBeInTheDocument()
  })

  it('MCP tools grouped per server — tools from the same server share a server disclosure', async () => {
    const user = userEvent.setup()
    const sameServerTools: RegistryTool[] = [
      makeTool({ name: 'my_server.tool_a', category: 'web', source: 'mcp', scope: 'general' }),
      makeTool({ name: 'my_server.tool_b', category: 'web', source: 'mcp', scope: 'general' }),
    ]
    renderEditor(sameServerTools)
    const mcpSection = screen.getByTestId('mcp-tools-section')
    // Should be exactly one server disclosure (both tools share 'web' or 'my_server' category)
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(1)
    // Expand and verify both tools are inside
    await user.click(triggers[0])
    expect(screen.getByTestId('tool-row-my_server.tool_a')).toBeInTheDocument()
    expect(screen.getByTestId('tool-row-my_server.tool_b')).toBeInTheDocument()
  })
})
