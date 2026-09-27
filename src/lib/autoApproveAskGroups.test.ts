import { describe, it, expect } from 'vitest'
import { AUTO_APPROVE_ASK_GROUPS, AUTO_APPROVE_ASK_GROUP_BY_TOOL } from './autoApproveAskGroups'

// Oracle independence: this list is HAND-TRANSCRIBED from
// pkg/tools/auto_approve.go's `autoApproveClasses` table (read directly,
// 2026-09-24) — every entry whose class is `AutoAsks`, i.e. every tool that
// always asks under Auto-approve. `bash` is deliberately excluded: its own
// class is `AutoShellMode`, a distinct mechanism (the shell-permission mode
// UI), not a member of this 28-tool ask-list. This is NOT derived from
// autoApproveAskGroups.ts — a mistake in that file's grouping (a dropped
// tool, a duplicate, an extra invented one) must fail against this
// independent transcription, not just agree with itself.
const EXPECTED_ASK_LIST_TOOLS = [
  // Files
  'request_mount',
  // Web
  'install_skill',
  'environment_setup',
  'serve_web',
  // Sending out
  'send_email',
  'reply',
  // Agents & tasks
  'delete_task',
  // System (settings)
  'set_config',
  'run_doctor',
  // System (providers)
  'configure_provider',
  'test_provider',
  // System (channels)
  'enable_channel',
  'disable_channel',
  'configure_channel',
  'test_channel',
  // System (MCP)
  'add_mcp_server',
  'remove_mcp_server',
  // System (agents)
  'create_agent',
  'update_agent',
  'delete_agent',
  // System (workspaces)
  'update_workspace',
  'delete_workspace',
  'delete_task_in_workspace',
  // System (skills)
  'create_skill',
  'edit_skill',
  'remove_skill',
  // Browser
  'browser_evaluate',
  'browser_upload_file',
].sort()

describe('AUTO_APPROVE_ASK_GROUPS — covers every one of the 28 ADR-092 always-ask tools', () => {
  it('the hand-transcribed reference list has exactly 28 tools', () => {
    expect(EXPECTED_ASK_LIST_TOOLS).toHaveLength(28)
  })

  it('every group tool is unique to that group — no tool appears in two groups', () => {
    const allTools = AUTO_APPROVE_ASK_GROUPS.flatMap((g) => g.tools)
    expect(new Set(allTools).size).toBe(allTools.length)
  })

  it('no group is empty', () => {
    for (const group of AUTO_APPROVE_ASK_GROUPS) {
      expect(group.tools.length, `group "${group.label}" has no tools`).toBeGreaterThan(0)
    }
  })

  it('every group label is non-empty, plain text (no raw tool names leaking into the label)', () => {
    for (const group of AUTO_APPROVE_ASK_GROUPS) {
      expect(group.label.trim().length).toBeGreaterThan(0)
      // A raw snake_case tool identifier leaking into the user-facing label
      // would be exactly the "jargon" the founder's fix is supposed to remove.
      expect(group.label).not.toMatch(/_/)
    }
  })

  it('the grouped tools, flattened and sorted, exactly equal the hand-transcribed 28-tool reference list', () => {
    const grouped = AUTO_APPROVE_ASK_GROUPS.flatMap((g) => g.tools).sort()
    expect(grouped).toEqual(EXPECTED_ASK_LIST_TOOLS)
  })

  it('every reference-list tool resolves to exactly one group via AUTO_APPROVE_ASK_GROUP_BY_TOOL', () => {
    for (const tool of EXPECTED_ASK_LIST_TOOLS) {
      expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL[tool], `no group found for "${tool}"`).toBeTruthy()
    }
  })

  it('carries no tool that is NOT in the reference list (no invented/stale entries)', () => {
    const grouped = new Set(AUTO_APPROVE_ASK_GROUPS.flatMap((g) => g.tools))
    for (const tool of grouped) {
      expect(EXPECTED_ASK_LIST_TOOLS, `"${tool}" is grouped but not in the reference list`).toContain(tool)
    }
  })
})
