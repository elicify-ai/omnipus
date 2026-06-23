/**
 * ToolPolicyEditor.test.tsx — US-1 / AC1 / AC4 / AC5 / AC6 / FR-103 / Issue #357.
 *
 * Tests use the REAL ToolRegistryEntry payload shape (category / source / scope
 * fields all present) — not a mock that omits category (spec §0 F-01 note).
 *
 * Key invariants verified here:
 *   - Each tool appears EXACTLY ONCE across the whole editor (no duplicates).
 *   - General builtins (exec, web_search) carrying category='core' are present.
 *   - No raw category key 'system' or 'core' is shown as a user-facing heading.
 *   - Allow/ask/deny control is present for sampled tools.
 *   - MCP tools are in their own per-server section, not the builtin grid.
 *   - Per-call-site coverage: global (GlobalToolPoliciesSection) and per-agent
 *     (ToolsAndPermissions) both render the flat list (AC6 — tested in
 *     AgentTools.test.tsx and ToolsAndPermissions.test.tsx).
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

/**
 * General builtins — these carry category='core' in the real backend payload
 * (un-recategorized). The editor must render them under "General" (not drop them
 * and not show the raw key 'core' as a heading).
 *
 * Spec note (F-21): mock uses the REAL category values the backend emits
 * ('core' for un-recategorized) — not 'file'.
 */
const GENERAL_TOOLS: RegistryTool[] = [
  makeTool({ name: 'exec', category: 'core', scope: 'core' }),
  makeTool({ name: 'web_search', category: 'core', scope: 'core' }),
  makeTool({ name: 'web_fetch', category: 'core', scope: 'core' }),
]

/**
 * system.* tools — category='system'; they MUST appear in the main category
 * grid under the "System" label, NOT in a separate disclosure section.
 */
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

/**
 * MCP tools use the format mcp_<server>_<tool> (pkg/tools/mcp_tool.go MCPTool.Name()).
 * Two tools from the same server ('myserver') carry DIFFERENT categories to verify
 * that grouping is by server name derived from the tool name, NOT by category.
 */
const MCP_TOOLS: RegistryTool[] = [
  // Both from server 'myserver' — different categories to exercise Blocker 1 fix
  makeTool({ name: 'mcp_myserver_search', category: 'search', source: 'mcp', scope: 'general' }),
  makeTool({ name: 'mcp_myserver_fetch', category: 'web', source: 'mcp', scope: 'general' }),
]

const MCP_TOOL_NO_CATEGORY: RegistryTool = makeTool({
  // Tool from 'otherserver' — category is 'other' (unset fallback)
  name: 'mcp_otherserver_uncategorized',
  category: 'other',
  source: 'mcp',
  scope: 'general',
})

// Mixed payload: system.*, general (core), file, browser, MCP tools.
const ALL_TOOLS = [...SYSTEM_TOOLS, ...GENERAL_TOOLS, ...FILE_TOOLS, ...BROWSER_TOOLS, ...MCP_TOOLS]

const SAFE_VALUE: ToolPolicyValue = { default_policy: 'allow', policies: {} }

function renderEditor(
  tools: RegistryTool[] = ALL_TOOLS,
  value: ToolPolicyValue = SAFE_VALUE,
  onChange = vi.fn(),
) {
  return render(<ToolPolicyEditor tools={tools} value={value} onChange={onChange} />)
}

// ── US-1 / AC1: each tool appears exactly once — no duplicates ─────────────────

describe('ToolPolicyEditor — no duplicate tool ids (US-1 / AC1)', () => {
  it('each tool appears exactly once across the whole editor (ALL_TOOLS payload)', () => {
    renderEditor(ALL_TOOLS)
    // Collect all tool-row data-testid values in the DOM.
    // Each tool-row has data-testid="tool-row-<name>".
    const toolRows = document.querySelectorAll('[data-testid^="tool-row-"]')
    const ids = Array.from(toolRows).map((el) => el.getAttribute('data-testid'))
    // The category grid is collapsed by default; only the outer editor is checked.
    // But we need to assert uniqueness: if a tool appeared twice it would have
    // two elements with the same data-testid. Check for duplicates.
    const seen = new Set<string>()
    for (const id of ids) {
      expect(seen.has(id!), `tool ${id} appeared more than once`).toBe(false)
      seen.add(id!)
    }
  })

  it('no duplicate tool-row ids after expanding ALL category sections', async () => {
    const user = userEvent.setup()
    renderEditor(ALL_TOOLS)
    // Expand every CategorySection trigger so all builtin tool rows are visible.
    const categoryButtons = document.querySelectorAll('[data-testid="category-grid"] button[aria-expanded]')
    for (const btn of categoryButtons) {
      await user.click(btn as HTMLElement)
    }
    // Now collect all visible tool rows.
    const toolRows = document.querySelectorAll('[data-testid^="tool-row-"]')
    const ids = Array.from(toolRows).map((el) => el.getAttribute('data-testid'))
    const seen = new Set<string>()
    for (const id of ids) {
      expect(seen.has(id!), `tool ${id} appeared more than once`).toBe(false)
      seen.add(id!)
    }
  })
})

// ── US-1 / AC1: general builtins (core category) are present ──────────────────

describe('ToolPolicyEditor — general builtins present (US-1 / AC1 / AC4)', () => {
  it('exec is present in the category grid', async () => {
    const user = userEvent.setup()
    renderEditor(GENERAL_TOOLS)
    // Expand the "General" (core) category section.
    const categoryGrid = screen.getByTestId('category-grid')
    const coreBtn = within(categoryGrid).getByRole('button', { name: /general/i })
    await user.click(coreBtn)
    expect(screen.getByTestId('tool-row-exec')).toBeInTheDocument()
  })

  it('web_search is present in the category grid', async () => {
    const user = userEvent.setup()
    renderEditor(GENERAL_TOOLS)
    const categoryGrid = screen.getByTestId('category-grid')
    const coreBtn = within(categoryGrid).getByRole('button', { name: /general/i })
    await user.click(coreBtn)
    expect(screen.getByTestId('tool-row-web_search')).toBeInTheDocument()
  })

  it('the category pill for core tools uses the key "core" (maps to "General" label)', () => {
    renderEditor(GENERAL_TOOLS)
    // The category pill data-testid is "category-pill-core"
    expect(screen.getByTestId('category-pill-core')).toBeInTheDocument()
  })
})

// ── US-1 / AC4 / FR-103: no raw 'system' or 'core' shown as a heading ─────────

describe('ToolPolicyEditor — no raw category key as user-facing heading (AC4 / FR-103)', () => {
  it('no heading element has text content equal to the raw key "system"', () => {
    renderEditor(ALL_TOOLS)
    // Check all button text in the category grid — none should equal raw "system" exactly.
    const categoryGrid = screen.getByTestId('category-grid')
    const buttons = within(categoryGrid).getAllByRole('button')
    for (const btn of buttons) {
      // The text may contain the word "system" as part of a label like "System Tools"
      // but must NOT be the bare raw key "system" as the entire button label.
      const text = btn.textContent?.trim() ?? ''
      expect(text).not.toBe('system')
    }
  })

  it('no heading element has text content equal to the raw key "core"', () => {
    renderEditor([...GENERAL_TOOLS, ...FILE_TOOLS])
    const categoryGrid = screen.getByTestId('category-grid')
    const buttons = within(categoryGrid).getAllByRole('button')
    for (const btn of buttons) {
      const text = btn.textContent?.trim() ?? ''
      expect(text).not.toBe('core')
    }
  })

  it('system.* tools appear in the category grid (not in a separate system disclosure)', () => {
    renderEditor(SYSTEM_TOOLS)
    // There must be a category-grid (not absent because system tools exist).
    expect(screen.getByTestId('category-grid')).toBeInTheDocument()
    // There must NOT be a system-disclosure-wrapper (the old §3 section is removed).
    expect(screen.queryByTestId('system-disclosure-wrapper')).not.toBeInTheDocument()
    // The "system" category pill must be present in the category grid.
    expect(screen.getByTestId('category-pill-system')).toBeInTheDocument()
  })

  it('system.* tools can be expanded from the category grid', async () => {
    const user = userEvent.setup()
    renderEditor(SYSTEM_TOOLS)
    const categoryGrid = screen.getByTestId('category-grid')
    const systemBtn = within(categoryGrid).getByRole('button', { name: /system/i })
    await user.click(systemBtn)
    // After expanding, system tool rows must be visible.
    for (const tool of SYSTEM_TOOLS) {
      expect(screen.getByTestId(`tool-row-${tool.name}`)).toBeInTheDocument()
    }
  })
})

// ── US-1 / AC5: allow/ask/deny control present per tool ───────────────────────

describe('ToolPolicyEditor — allow/ask/deny controls present (AC5)', () => {
  it('allow/ask/deny badges are present for a file tool after expanding', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(FILE_TOOLS, SAFE_VALUE, onChange)
    const categoryGrid = screen.getByTestId('category-grid')
    const fileBtn = within(categoryGrid).getByRole('button', { name: /file/i })
    await user.click(fileBtn)
    const readFileRow = screen.getByTestId('tool-row-read_file')
    expect(within(readFileRow).getByRole('button', { name: /allow/i })).toBeInTheDocument()
    expect(within(readFileRow).getByRole('button', { name: /ask/i })).toBeInTheDocument()
    expect(within(readFileRow).getByRole('button', { name: /deny/i })).toBeInTheDocument()
  })

  it('clicking deny on a file tool calls onChange with the correct override', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(FILE_TOOLS, SAFE_VALUE, onChange)
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /file/i }))
    const readFileRow = screen.getByTestId('tool-row-read_file')
    await user.click(within(readFileRow).getByRole('button', { name: /deny/i }))
    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: { read_file: 'deny' },
    })
  })
})

// ── Preset application ─────────────────────────────────────────────────────────

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

// ── Category rollup pills ──────────────────────────────────────────────────────

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

// ── Default policy control ─────────────────────────────────────────────────────

describe('ToolPolicyEditor — default policy control (advanced)', () => {
  it('default policy control is accessible inside the Customize defaults section', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(FILE_TOOLS, SAFE_VALUE, onChange)
    // Find the "Customize defaults (advanced)" trigger button
    const customizeTrigger = screen.getByRole('button', { name: /customize defaults/i })
    await user.click(customizeTrigger)
    // Default policy badges must now be visible
    expect(screen.getAllByRole('button', { name: /allow/i }).length).toBeGreaterThan(0)
  })

  it('there is NO raw-tool-grid that re-lists all tools', async () => {
    const user = userEvent.setup()
    renderEditor(ALL_TOOLS)
    // Even after opening Customize defaults, no raw-tool-grid data-testid should exist.
    const customizeTrigger = screen.getByRole('button', { name: /customize defaults/i })
    await user.click(customizeTrigger)
    expect(screen.queryByTestId('raw-tool-grid')).not.toBeInTheDocument()
  })

  it('changing default policy calls onChange dropping overrides that now match', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const valueWithOverride: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { read_file: 'ask' },
    }
    renderEditor(FILE_TOOLS, valueWithOverride, onChange)
    // Open customize defaults
    await user.click(screen.getByRole('button', { name: /customize defaults/i }))
    // Click "Ask" on the default policy — the 'ask' override for read_file should be dropped
    // since it now matches the new default.
    const defaultPolicyBadges = screen.getAllByRole('button', { name: /^ask$/i })
    await user.click(defaultPolicyBadges[0])
    expect(onChange).toHaveBeenCalledWith({ default_policy: 'ask', policies: {} })
  })
})

// ── Policy round-trip ──────────────────────────────────────────────────────────

describe('ToolPolicyEditor — policy round-trip', () => {
  it('loading an existing per-tool override and not touching the UI emits no spurious calls', () => {
    const onChange = vi.fn()
    const existingValue: ToolPolicyValue = {
      default_policy: 'allow',
      policies: {
        'browser.navigate': 'ask',
        'browser.evaluate': 'deny',
        write_file: 'ask',
      },
    }
    renderEditor(FILE_TOOLS, existingValue, onChange)
    // onChange must not be called just from rendering.
    expect(onChange).not.toHaveBeenCalled()
  })

  it('clicking a policy badge that matches default_policy removes the override', async () => {
    const user = userEvent.setup()
    const existingValue: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { read_file: 'ask' },
    }
    const onChange = vi.fn()
    renderEditor(FILE_TOOLS, existingValue, onChange)
    // Expand the 'file' category
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /file/i }))
    const readFileRow = screen.getByTestId('tool-row-read_file')
    // Click Allow (= the default_policy) — should remove the override.
    await user.click(within(readFileRow).getByRole('button', { name: /allow/i }))
    expect(onChange).toHaveBeenCalledWith({ default_policy: 'allow', policies: {} })
  })
})

// ── MCP tools (F-G06 guard) ────────────────────────────────────────────────────

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

  it('MCP tools are NOT swept into the category grid alongside system tools', () => {
    renderEditor([...SYSTEM_TOOLS, ...MCP_TOOLS])
    // Category grid should exist (for system tools)
    expect(screen.getByTestId('category-grid')).toBeInTheDocument()
    // MCP section should also exist and be separate
    expect(screen.getByTestId('mcp-tools-section')).toBeInTheDocument()
    // MCP tools must NOT be in the category grid
    const grid = screen.getByTestId('category-grid')
    MCP_TOOLS.forEach((tool) => {
      expect(within(grid).queryByTestId(`tool-row-${tool.name}`)).not.toBeInTheDocument()
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

  it('an MCP tool with category unset (fallback: other) is not swallowed by the category filter', () => {
    renderEditor([MCP_TOOL_NO_CATEGORY])
    // Must appear in MCP section
    expect(screen.getByTestId('mcp-tools-section')).toBeInTheDocument()
    // Must NOT appear in the builtin category grid
    expect(screen.queryByTestId('category-grid')).not.toBeInTheDocument()
  })

  /**
   * Blocker 1 — MCP grouping must key on server name derived from the tool
   * NAME (format: mcp_<server>_<tool>), NOT on category.
   *
   * (a) Same server, different categories → ONE disclosure.
   * (b) Different servers, same category → TWO disclosures.
   */
  it('(Blocker 1a) same-server tools with different categories → ONE server disclosure', async () => {
    const user = userEvent.setup()
    // mcp_myserver_search has category:'search'; mcp_myserver_fetch has category:'web'
    // Both share server 'myserver' → must produce exactly ONE disclosure in the MCP section.
    renderEditor(MCP_TOOLS)
    const mcpSection = screen.getByTestId('mcp-tools-section')
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(1)
    // Expand and verify both tools are inside
    await user.click(triggers[0])
    expect(screen.getByTestId('tool-row-mcp_myserver_search')).toBeInTheDocument()
    expect(screen.getByTestId('tool-row-mcp_myserver_fetch')).toBeInTheDocument()
  })

  it('(Blocker 1b) different-server tools with the same category → TWO server disclosures', async () => {
    // Two tools, same category 'web', but different servers (server1, server2)
    const differentServerSameCategory: RegistryTool[] = [
      makeTool({ name: 'mcp_server1_tool', category: 'web', source: 'mcp', scope: 'general' }),
      makeTool({ name: 'mcp_server2_tool', category: 'web', source: 'mcp', scope: 'general' }),
    ]
    renderEditor(differentServerSameCategory)
    const mcpSection = screen.getByTestId('mcp-tools-section')
    // Must produce TWO disclosures (one per server), NOT one
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(2)
  })

  it('MCP tools grouped per server — mcp_<server>_<tool> naming convention', async () => {
    const user = userEvent.setup()
    // Explicitly named per the real MCPTool.Name() format
    const sameServerTools: RegistryTool[] = [
      makeTool({ name: 'mcp_mysvr_tool_a', category: 'web', source: 'mcp', scope: 'general' }),
      makeTool({ name: 'mcp_mysvr_tool_b', category: 'search', source: 'mcp', scope: 'general' }),
    ]
    renderEditor(sameServerTools)
    const mcpSection = screen.getByTestId('mcp-tools-section')
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(1)
    await user.click(triggers[0])
    expect(screen.getByTestId('tool-row-mcp_mysvr_tool_a')).toBeInTheDocument()
    expect(screen.getByTestId('tool-row-mcp_mysvr_tool_b')).toBeInTheDocument()
  })
})

// ── Glob-keyed policies (Blocker 4) ───────────────────────────────────────────

describe('ToolPolicyEditor — glob-keyed policies (Blocker 4)', () => {
  it('system.* glob key resolves correctly for system tools in the category grid', async () => {
    const user = userEvent.setup()
    // Seeded privilege rail: default_policy='allow', system.*='deny'
    const valueWithGlob: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { 'system.*': 'deny' },
    }
    renderEditor(SYSTEM_TOOLS, valueWithGlob)
    // The category grid shows the 'system' category pill
    expect(screen.getByTestId('category-pill-system')).toBeInTheDocument()
    // Expand the system category
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /system/i }))
    // All three system tools should now be visible and resolved to 'deny'
    for (const tool of SYSTEM_TOOLS) {
      const row = screen.getByTestId(`tool-row-${tool.name}`)
      // The 'Deny' badge must be active (aria-pressed="true")
      const denyBadge = within(row).getByRole('button', { name: /deny/i })
      expect(denyBadge).toHaveAttribute('aria-pressed', 'true')
    }
  })

  it('system.* glob does not affect non-system tools', () => {
    const valueWithGlob: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { 'system.*': 'deny' },
    }
    renderEditor(FILE_TOOLS, valueWithGlob)
    // The category pill for 'file' should show 'Allow' (default), not 'Deny'
    const pill = screen.getByTestId('category-pill-file')
    expect(pill).toHaveTextContent('Allow')
  })

  it('exact key takes precedence over a glob key for the same tool', async () => {
    const user = userEvent.setup()
    const valueWithBoth: ToolPolicyValue = {
      default_policy: 'allow',
      // Glob denies all system.*, but system.config_read is explicitly allowed
      policies: { 'system.*': 'deny', 'system.config_read': 'allow' },
    }
    renderEditor(SYSTEM_TOOLS, valueWithBoth)
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /system/i }))
    // system.config_read → allow (exact match wins)
    const readRow = screen.getByTestId('tool-row-system.config_read')
    expect(within(readRow).getByRole('button', { name: /allow/i })).toHaveAttribute('aria-pressed', 'true')
    // system.config_write → deny (glob applies)
    const writeRow = screen.getByTestId('tool-row-system.config_write')
    expect(within(writeRow).getByRole('button', { name: /deny/i })).toHaveAttribute('aria-pressed', 'true')
  })
})

// ── Global override locking (no contradicting configs) ──────────────────────────

describe('ToolPolicyEditor — global override locking', () => {
  it('locks less-restrictive per-agent controls when a global glob denies the tool', async () => {
    const user = userEvent.setup()
    render(
      <ToolPolicyEditor
        tools={BROWSER_TOOLS}
        value={SAFE_VALUE}
        onChange={vi.fn()}
        globalPolicies={{ default_policy: 'allow', policies: { 'browser.*': 'deny' } }}
      />,
    )
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /browser/i }))

    const row = screen.getByTestId('tool-row-browser.navigate')
    // A "Global: Deny" indicator links to Settings → Security.
    const link = within(row).getByTestId('global-override-browser.navigate')
    expect(link).toHaveTextContent(/global:\s*deny/i)
    expect(link).toHaveAttribute('href', '/#/settings')
    // allow + ask are locked (less restrictive than deny); deny stays enabled.
    expect(within(row).getByRole('button', { name: /allow/i })).toBeDisabled()
    expect(within(row).getByRole('button', { name: /ask/i })).toBeDisabled()
    expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })

  it('a global "ask" floor locks only the allow control (ask/deny stay settable)', async () => {
    const user = userEvent.setup()
    render(
      <ToolPolicyEditor
        tools={FILE_TOOLS}
        value={SAFE_VALUE}
        onChange={vi.fn()}
        globalPolicies={{ default_policy: 'allow', policies: { read_file: 'ask' } }}
      />,
    )
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /file/i }))

    const row = screen.getByTestId('tool-row-read_file')
    expect(within(row).getByTestId('global-override-read_file')).toHaveTextContent(/global:\s*ask/i)
    expect(within(row).getByRole('button', { name: /allow/i })).toBeDisabled()
    expect(within(row).getByRole('button', { name: /ask/i })).not.toBeDisabled()
    expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })

  it('no global override (allow) leaves every control enabled and shows no lock', async () => {
    const user = userEvent.setup()
    render(
      <ToolPolicyEditor
        tools={FILE_TOOLS}
        value={SAFE_VALUE}
        onChange={vi.fn()}
        globalPolicies={{ default_policy: 'allow', policies: {} }}
      />,
    )
    const categoryGrid = screen.getByTestId('category-grid')
    await user.click(within(categoryGrid).getByRole('button', { name: /file/i }))

    const row = screen.getByTestId('tool-row-write_file')
    expect(within(row).queryByTestId('global-override-write_file')).toBeNull()
    expect(within(row).getByRole('button', { name: /allow/i })).not.toBeDisabled()
    expect(within(row).getByRole('button', { name: /ask/i })).not.toBeDisabled()
    expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })
})

// ── MCP tool policy controls (interaction + global-lock) ──────────────────────

describe('ToolPolicyEditor — MCP tool policy controls', () => {
  /**
   * Helper: render with MCP_TOOLS only and expand the 'myserver' server disclosure.
   * Returns the expanded tool container so callers can query within it.
   */
  async function renderAndExpandMcpServer(
    value: ToolPolicyValue = SAFE_VALUE,
    onChange = vi.fn(),
    globalPolicies?: import('./ToolPolicyEditor').ToolPolicyEditorProps['globalPolicies'],
  ) {
    const user = userEvent.setup()
    render(
      <ToolPolicyEditor
        tools={MCP_TOOLS}
        value={value}
        onChange={onChange}
        globalPolicies={globalPolicies}
      />,
    )
    const mcpSection = screen.getByTestId('mcp-tools-section')
    // There is exactly one server disclosure for MCP_TOOLS (both are from 'myserver').
    const trigger = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')[0]
    await user.click(trigger)
    // After expansion the mcp-server-myserver container holds the tool rows.
    const serverContainer = screen.getByTestId('mcp-server-myserver')
    return { user, serverContainer, mcpSection }
  }

  // ── 1. allow/ask/deny clicks on MCP tool row call onChange with correct payload ──

  it('clicking deny on an MCP tool row calls onChange with the MCP tool name + deny', async () => {
    const onChange = vi.fn()
    const { user, serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, onChange)

    const row = within(serverContainer).getByTestId('tool-row-mcp_myserver_search')
    await user.click(within(row).getByRole('button', { name: /deny/i }))

    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: { mcp_myserver_search: 'deny' },
    })
  })

  it('clicking ask on an MCP tool row calls onChange with the MCP tool name + ask', async () => {
    const onChange = vi.fn()
    const { user, serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, onChange)

    const row = within(serverContainer).getByTestId('tool-row-mcp_myserver_fetch')
    await user.click(within(row).getByRole('button', { name: /ask/i }))

    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: { mcp_myserver_fetch: 'ask' },
    })
  })

  it('clicking allow on an already-overridden MCP tool removes the override (round-trip clean)', async () => {
    // Start with mcp_myserver_search overridden to 'deny'
    const startValue: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { mcp_myserver_search: 'deny' },
    }
    const onChange = vi.fn()
    const { user, serverContainer } = await renderAndExpandMcpServer(startValue, onChange)

    const row = within(serverContainer).getByTestId('tool-row-mcp_myserver_search')
    // Click Allow (= default_policy) — override should be removed
    await user.click(within(row).getByRole('button', { name: /allow/i }))

    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: {},
    })
  })

  // ── 2. Global exact-key deny locks less-restrictive controls on the MCP row ──

  it('a global exact-key deny locks allow+ask and keeps deny enabled on the MCP row', async () => {
    // globalPolicies uses an exact key matching the MCP tool name.
    // (The glob pattern mcp_myserver.* would NOT work for MCP tool names because
    // resolvePolicy's glob check tests prefix + '.', but MCP names use underscores.)
    const globalPolicies: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { mcp_myserver_search: 'deny' },
    }
    const { serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, vi.fn(), globalPolicies)

    const row = within(serverContainer).getByTestId('tool-row-mcp_myserver_search')

    // Global lock indicator must be present
    const lockBadge = within(row).getByTestId('global-override-mcp_myserver_search')
    expect(lockBadge).toHaveTextContent(/global:\s*deny/i)
    expect(lockBadge).toHaveAttribute('href', '/#/settings')

    // allow and ask are locked (less restrictive than deny); deny is still clickable
    expect(within(row).getByRole('button', { name: /allow/i })).toBeDisabled()
    expect(within(row).getByRole('button', { name: /ask/i })).toBeDisabled()
    expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })

  it('a global deny lock does not bleed into a sibling MCP tool from the same server', async () => {
    // Only mcp_myserver_search is locked; mcp_myserver_fetch must remain unlocked
    const globalPolicies: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { mcp_myserver_search: 'deny' },
    }
    const { serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, vi.fn(), globalPolicies)

    // mcp_myserver_fetch — no global override → all controls enabled, no lock badge
    const fetchRow = within(serverContainer).getByTestId('tool-row-mcp_myserver_fetch')
    expect(within(fetchRow).queryByTestId('global-override-mcp_myserver_fetch')).toBeNull()
    expect(within(fetchRow).getByRole('button', { name: /allow/i })).not.toBeDisabled()
    expect(within(fetchRow).getByRole('button', { name: /ask/i })).not.toBeDisabled()
    expect(within(fetchRow).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })

  // ── 3. Global exact-key ask locks only the allow control ──────────────────────

  it('a global exact-key ask locks only allow (ask + deny remain enabled)', async () => {
    const globalPolicies: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { mcp_myserver_search: 'ask' },
    }
    const { serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, vi.fn(), globalPolicies)

    const row = within(serverContainer).getByTestId('tool-row-mcp_myserver_search')

    const lockBadge = within(row).getByTestId('global-override-mcp_myserver_search')
    expect(lockBadge).toHaveTextContent(/global:\s*ask/i)

    // allow is locked (less restrictive than ask); ask and deny are NOT locked
    expect(within(row).getByRole('button', { name: /allow/i })).toBeDisabled()
    expect(within(row).getByRole('button', { name: /ask/i })).not.toBeDisabled()
    expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })

  // ── 4. Global default_policy deny (no per-tool overrides) locks all MCP rows ──

  it('a global default_policy=deny (no exact overrides) locks allow+ask on all MCP rows', async () => {
    // When the global default is 'deny', globalOverrideFor returns 'deny' for every
    // tool that has no more-specific override, including MCP tools.
    const globalPolicies: ToolPolicyValue = {
      default_policy: 'deny',
      policies: {},
    }
    const { serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, vi.fn(), globalPolicies)

    // Both MCP tools from 'myserver' should be locked at deny
    for (const toolName of ['mcp_myserver_search', 'mcp_myserver_fetch']) {
      const row = within(serverContainer).getByTestId(`tool-row-${toolName}`)
      expect(within(row).getByRole('button', { name: /allow/i })).toBeDisabled()
      expect(within(row).getByRole('button', { name: /ask/i })).toBeDisabled()
      expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
    }
  })

  // ── 5. No global policies prop → no locks on MCP rows ──────────────────────

  it('no globalPolicies prop leaves all MCP row controls enabled and shows no lock badge', async () => {
    const { serverContainer } = await renderAndExpandMcpServer(SAFE_VALUE, vi.fn(), undefined)

    const row = within(serverContainer).getByTestId('tool-row-mcp_myserver_search')
    expect(within(row).queryByTestId('global-override-mcp_myserver_search')).toBeNull()
    expect(within(row).getByRole('button', { name: /allow/i })).not.toBeDisabled()
    expect(within(row).getByRole('button', { name: /ask/i })).not.toBeDisabled()
    expect(within(row).getByRole('button', { name: /deny/i })).not.toBeDisabled()
  })
})

// ── G11: MCP grouping by server_id (not name-parse) ───────────────────────────

describe('ToolPolicyEditor — G11: MCP grouping by server_id', () => {
  /**
   * (a) server_id "github_mcp" + tool name "mcp_github_mcp_search".
   *
   * The name-parse fallback would extract "github" (first segment after mcp_),
   * producing group "github". With server_id present, grouping must use the
   * server_id value "github_mcp" and produce ONE group, not "github".
   */
  it('(a) groups by server_id even when server name contains an underscore', async () => {
    const user = userEvent.setup()
    const tools: RegistryTool[] = [
      makeTool({
        name: 'mcp_github_mcp_search',
        source: 'mcp',
        scope: 'general',
        category: 'search',
        server_id: 'github_mcp',
      }),
      makeTool({
        name: 'mcp_github_mcp_list',
        source: 'mcp',
        scope: 'general',
        category: 'search',
        server_id: 'github_mcp',
      }),
    ]
    renderEditor(tools)
    const mcpSection = screen.getByTestId('mcp-tools-section')
    // Must produce exactly ONE disclosure for server "github_mcp", not "github".
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(1)
    // The trigger text must include the full server name "github_mcp".
    expect(triggers[0]).toHaveTextContent('github_mcp')
    // Expand and verify both tools are inside.
    await user.click(triggers[0])
    expect(screen.getByTestId('tool-row-mcp_github_mcp_search')).toBeInTheDocument()
    expect(screen.getByTestId('tool-row-mcp_github_mcp_list')).toBeInTheDocument()
  })

  /**
   * (b) Fallback: no server_id → name-parse still works for older payloads.
   *
   * Two tools from "mysvr" without a server_id field → name-parse extracts
   * "mysvr" → ONE group.
   */
  it('(b) fallback name-parse grouping works when server_id is absent', async () => {
    const user = userEvent.setup()
    const tools: RegistryTool[] = [
      makeTool({ name: 'mcp_mysvr_tool_a', source: 'mcp', scope: 'general', category: 'web' }),
      makeTool({ name: 'mcp_mysvr_tool_b', source: 'mcp', scope: 'general', category: 'web' }),
    ]
    renderEditor(tools)
    const mcpSection = screen.getByTestId('mcp-tools-section')
    const triggers = within(mcpSection).getAllByTestId('advanced-disclosure-trigger')
    expect(triggers.length).toBe(1)
    await user.click(triggers[0])
    expect(screen.getByTestId('tool-row-mcp_mysvr_tool_a')).toBeInTheDocument()
    expect(screen.getByTestId('tool-row-mcp_mysvr_tool_b')).toBeInTheDocument()
  })
})

// ── G10: per-server bulk allow/ask/deny control ────────────────────────────────

describe('ToolPolicyEditor — G10: per-server bulk policy control', () => {
  /**
   * The wildcard key for the "myserver" group is "mcp_myserver_*".
   * MCP_TOOLS = [mcp_myserver_search, mcp_myserver_fetch] → common prefix = "mcp_myserver_".
   */

  it('per-server bulk control is present in the MCP server group header', () => {
    renderEditor(MCP_TOOLS)
    const bulkControls = screen.getByTestId('mcp-server-bulk-myserver')
    expect(bulkControls).toBeInTheDocument()
  })

  it('clicking Deny all writes the mcp_<server>_* wildcard key via onChange', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(MCP_TOOLS, SAFE_VALUE, onChange)
    const denyBtn = screen.getByTestId('mcp-server-bulk-myserver-deny')
    await user.click(denyBtn)
    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: { 'mcp_myserver_*': 'deny' },
    })
  })

  it('clicking Ask all writes the mcp_<server>_* wildcard key with ask', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderEditor(MCP_TOOLS, SAFE_VALUE, onChange)
    const askBtn = screen.getByTestId('mcp-server-bulk-myserver-ask')
    await user.click(askBtn)
    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: { 'mcp_myserver_*': 'ask' },
    })
  })

  it('clicking Allow all removes the wildcard key from policies', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const startValue: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { 'mcp_myserver_*': 'deny' },
    }
    renderEditor(MCP_TOOLS, startValue, onChange)
    const allowBtn = screen.getByTestId('mcp-server-bulk-myserver-allow')
    await user.click(allowBtn)
    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: {},
    })
  })

  it('Deny bulk button is aria-pressed when the wildcard key is already set to deny', () => {
    const valueWithWildcard: ToolPolicyValue = {
      default_policy: 'allow',
      policies: { 'mcp_myserver_*': 'deny' },
    }
    renderEditor(MCP_TOOLS, valueWithWildcard)
    const denyBtn = screen.getByTestId('mcp-server-bulk-myserver-deny')
    expect(denyBtn).toHaveAttribute('aria-pressed', 'true')
    // Allow should NOT be active
    expect(screen.getByTestId('mcp-server-bulk-myserver-allow')).toHaveAttribute('aria-pressed', 'false')
  })

  it('Allow bulk button is aria-pressed when no wildcard key exists (default state)', () => {
    renderEditor(MCP_TOOLS, SAFE_VALUE)
    const allowBtn = screen.getByTestId('mcp-server-bulk-myserver-allow')
    expect(allowBtn).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('mcp-server-bulk-myserver-deny')).toHaveAttribute('aria-pressed', 'false')
  })

  it('bulk Deny for server with underscore in server_id writes correct wildcard key', async () => {
    // server_id = "github_mcp", tools: mcp_github_mcp_search, mcp_github_mcp_list
    // common prefix = "mcp_github_mcp_" → wildcard key = "mcp_github_mcp_*"
    const user = userEvent.setup()
    const onChange = vi.fn()
    const tools: RegistryTool[] = [
      makeTool({
        name: 'mcp_github_mcp_search',
        source: 'mcp',
        scope: 'general',
        category: 'search',
        server_id: 'github_mcp',
      }),
      makeTool({
        name: 'mcp_github_mcp_list',
        source: 'mcp',
        scope: 'general',
        category: 'search',
        server_id: 'github_mcp',
      }),
    ]
    renderEditor(tools, SAFE_VALUE, onChange)
    const denyBtn = screen.getByTestId('mcp-server-bulk-github_mcp-deny')
    await user.click(denyBtn)
    expect(onChange).toHaveBeenCalledWith({
      default_policy: 'allow',
      policies: { 'mcp_github_mcp_*': 'deny' },
    })
  })
})
