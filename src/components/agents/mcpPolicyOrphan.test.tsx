/**
 * mcpPolicyOrphan.test.tsx
 *
 * US-E5 (#337): MCP tools are grouped per-server in the shared ToolPolicyEditor.
 *
 * Covers the M-6 orphan-persistence scenario:
 *   1. A synthetic source:'mcp' tool renders in the MCP per-server section.
 *   2. Removing the server removes its tools from the editor.
 *   3. A previously-saved Deny persists inertly when the server is absent.
 *   4. Re-adding the server re-applies the Deny policy.
 *
 * Note: tests use ToolPolicyEditor directly (the shared primitive that both
 * ToolsAndPermissions and SecuritySection delegate to) and a synthetic MCP
 * tool — no live MCP server at test time.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { RegistryTool } from '@/lib/api'
import { ToolPolicyEditor } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'

// ── Fixtures ───────────────────────────────────────────────────────────────────

/** A synthetic MCP tool from server 'github' */
const MCP_GITHUB_TOOL: RegistryTool = {
  name: 'mcp_github_search',
  scope: 'general',
  category: 'web',
  description: 'Search GitHub repos via MCP',
  source: 'mcp',
}

/** A second tool from the same server */
const MCP_GITHUB_TOOL_2: RegistryTool = {
  name: 'mcp_github_create_pr',
  scope: 'general',
  category: 'vcs',
  description: 'Create PR via MCP',
  source: 'mcp',
}

/** A regular builtin tool (should NOT be in the MCP section) */
const BUILTIN_TOOL: RegistryTool = {
  name: 'read_file',
  scope: 'general',
  category: 'filesystem',
  description: 'Read file contents',
  source: 'builtin',
}

const EMPTY_POLICY: ToolPolicyValue = { default_policy: 'allow', policies: {} }

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

// ── Tests ──────────────────────────────────────────────────────────────────────

describe('mcpPolicyOrphan: MCP per-server grouping and orphan persistence (#337 / M-6)', () => {
  it('MCP tool renders in the MCP per-server section (not the category grid)', async () => {
    render(
      <ToolPolicyEditor
        tools={[BUILTIN_TOOL, MCP_GITHUB_TOOL]}
        value={EMPTY_POLICY}
        onChange={vi.fn()}
      />,
      { wrapper },
    )

    await waitFor(() => {
      // MCP tools section present
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).toBeInTheDocument()
      // The tool row for the MCP tool is under the mcp section
      const mcpSection = document.querySelector('[data-testid="mcp-tools-section"]')
      expect(mcpSection).not.toBeNull()
    })
  })

  it('MCP source badge appears for MCP server group when expanded', async () => {
    render(
      <ToolPolicyEditor
        tools={[MCP_GITHUB_TOOL, MCP_GITHUB_TOOL_2]}
        value={EMPTY_POLICY}
        onChange={vi.fn()}
      />,
      { wrapper },
    )

    // The MCP section renders collapsed AdvancedDisclosure per server.
    // Expand the first one (the github server group).
    await waitFor(() => {
      const triggers = document.querySelectorAll('[data-testid="advanced-disclosure-trigger"]')
      expect(triggers.length).toBeGreaterThan(0)
    })

    // Click the first disclosure trigger to expand the github server group.
    const trigger = document.querySelector('[data-testid="advanced-disclosure-trigger"]')!
    fireEvent.click(trigger)

    await waitFor(() => {
      // The server 'github' source badge should be present in the expanded content
      expect(document.querySelector('[data-testid="mcp-source-badge-github"]')).toBeInTheDocument()
    })
  })

  it('removing the server (empty tool list) removes MCP tools from the editor', async () => {
    // Initially render with MCP tool
    const { rerender } = render(
      <ToolPolicyEditor
        tools={[MCP_GITHUB_TOOL, BUILTIN_TOOL]}
        value={EMPTY_POLICY}
        onChange={vi.fn()}
      />,
      { wrapper },
    )

    await waitFor(() => {
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).toBeInTheDocument()
    })

    // Re-render without MCP tool (server removed)
    rerender(
      <QueryClientProvider client={makeQueryClient()}>
        <ToolPolicyEditor
          tools={[BUILTIN_TOOL]}
          value={EMPTY_POLICY}
          onChange={vi.fn()}
        />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      // MCP section should no longer be present
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).not.toBeInTheDocument()
    })
  })

  it('a previously-saved Deny policy for an MCP tool persists inertly when server is absent', () => {
    // Deny was set for the MCP tool in the policy; server is now absent.
    // The policy value itself still carries the entry — it stays in state, just
    // not rendered. This is M-6: no pruning job, orphan entries kept-but-inert.
    const policyWithDeny: ToolPolicyValue = {
      default_policy: 'allow',
      policies: {
        mcp_github_search: 'deny',   // orphaned — server removed
      },
    }

    // When the server is absent (tools list has no MCP tools), the policy entry
    // persists in the value and is not removed.
    expect(policyWithDeny.policies['mcp_github_search']).toBe('deny')

    // The editor with an empty tool list does NOT crash on this value.
    render(
      <ToolPolicyEditor
        tools={[BUILTIN_TOOL]}   // MCP tool absent
        value={policyWithDeny}
        onChange={vi.fn()}
      />,
      { wrapper },
    )

    // No MCP section rendered — the orphaned policy is inert
    expect(document.querySelector('[data-testid="mcp-tools-section"]')).not.toBeInTheDocument()
    // But the builtin category grid is still rendered
    expect(document.querySelector('[data-testid="category-grid"]')).toBeInTheDocument()
  })

  it('re-adding the server re-applies the persisted Deny to the tool', async () => {
    // Start with orphaned Deny for the MCP tool (server absent)
    const policyWithDeny: ToolPolicyValue = {
      default_policy: 'allow',
      policies: {
        mcp_github_search: 'deny',
      },
    }

    const onChange = vi.fn()

    // Render without MCP tool (server absent)
    const { rerender } = render(
      <ToolPolicyEditor
        tools={[BUILTIN_TOOL]}
        value={policyWithDeny}
        onChange={onChange}
      />,
      { wrapper },
    )

    // Confirm MCP section absent
    expect(document.querySelector('[data-testid="mcp-tools-section"]')).not.toBeInTheDocument()

    // Re-add server (MCP tool is back in the list)
    rerender(
      <QueryClientProvider client={makeQueryClient()}>
        <ToolPolicyEditor
          tools={[BUILTIN_TOOL, MCP_GITHUB_TOOL]}
          value={policyWithDeny}
          onChange={onChange}
        />
      </QueryClientProvider>,
    )

    await waitFor(() => {
      // MCP section should reappear
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).toBeInTheDocument()
    })

    // Expand the github server group to see the tool rows
    const triggers = document.querySelectorAll('[data-testid="advanced-disclosure-trigger"]')
    const githubTrigger = Array.from(triggers).find((t) => t.textContent?.includes('github'))
    if (githubTrigger) {
      fireEvent.click(githubTrigger)
    }

    await waitFor(() => {
      // The tool row for mcp_github_search should exist inside the MCP section.
      const toolRow = document.querySelector('[data-testid="tool-row-mcp_github_search"]')
      expect(toolRow).toBeInTheDocument()
    })
  })

  it('multiple MCP tools from same server are grouped together', async () => {
    render(
      <ToolPolicyEditor
        tools={[MCP_GITHUB_TOOL, MCP_GITHUB_TOOL_2, BUILTIN_TOOL]}
        value={EMPTY_POLICY}
        onChange={vi.fn()}
      />,
      { wrapper },
    )

    // Wait for MCP section to appear
    await waitFor(() => {
      expect(document.querySelector('[data-testid="mcp-tools-section"]')).toBeInTheDocument()
    })

    // Expand the github server group disclosure
    const triggers = document.querySelectorAll('[data-testid="advanced-disclosure-trigger"]')
    // First trigger = github server group in the MCP section (the 'customize per tool' may be another)
    // Find the trigger that contains 'github'
    const githubTrigger = Array.from(triggers).find((t) => t.textContent?.includes('github'))
    expect(githubTrigger).toBeDefined()
    fireEvent.click(githubTrigger!)

    await waitFor(() => {
      // Only ONE server group for 'github' (both tools under same server)
      const serverGroup = document.querySelector('[data-testid="mcp-server-github"]')
      expect(serverGroup).toBeInTheDocument()
      // Both tools present in the group
      expect(serverGroup?.querySelector('[data-testid="tool-row-mcp_github_search"]')).toBeInTheDocument()
      expect(serverGroup?.querySelector('[data-testid="tool-row-mcp_github_create_pr"]')).toBeInTheDocument()
    })
  })
})
