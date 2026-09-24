/**
 * AUTO_APPROVE_ASK_GROUPS — the plain-language grouping of every tool ADR-092
 * never lets Auto-approve wave through once set to Ask, shown by the
 * Security card's collapsed "Always asks when set to Ask" list and the
 * turn-on confirmation dialog (SecuritySection.tsx::AutoApproveControl).
 *
 * The founder's own report (2026-09-24): the previous single paragraph
 * ("28 always-ask tools, the path rule, the example and the Windows caveat")
 * was "a huge blob of text, not well written". A first redesign pass (seven
 * groups) was reviewed and found to undersell the list: a reader had no line
 * to find `environment_setup`, `run_doctor`, `test_provider`/`test_channel`,
 * or `request_mount` under — "Files outside your workspace" read as the file
 * *content* rule (already covered by the summary sentence above the list),
 * not as "asking to mount a new folder". These EIGHT groups are the
 * corrected, coordinator-specified wording (2026-09-24) — every kind must be
 * findable by a reader scanning the list, even though it stays short. Still
 * deliberately coarser than the 11-row detailed table in docs/security.md
 * (which stays as the full-detail reference for a reader who wants every
 * tool named).
 *
 * Source of truth for MEMBERSHIP (which 28 tools ask) is
 * `pkg/tools/auto_approve.go`'s `AutoApproveClassTable()` — every
 * `AutoAsks`-classified entry except `bash` (its own `AutoShellMode` class,
 * governed by the separate shell-permission mode UI, not this list). This
 * file's own test (`autoApproveAskGroups.test.ts`) pins the count at 28 and
 * asserts every group's tools are disjoint and cover exactly that set, so a
 * future addition to the Go table that isn't mirrored here fails loudly
 * instead of silently under-reporting what Auto-approve still asks about.
 */

export interface AutoApproveAskGroup { // not-wire-format: UI-only label/grouping data built and consumed entirely client-side, never sent to or received from the gateway
  /** Plain-language label shown as one line in the collapsed list. */
  label: string
  /** The raw tool names (pkg/tools/auto_approve.go names) this line covers. */
  tools: string[]
}

export const AUTO_APPROVE_ASK_GROUPS: AutoApproveAskGroup[] = [
  {
    label: 'Sending email',
    tools: ['send_email', 'reply'],
  },
  {
    label: 'Deleting tasks, agents or workspaces',
    tools: ['delete_task', 'delete_task_in_workspace', 'delete_agent', 'delete_workspace'],
  },
  {
    label: 'Installing skills, setting up an environment or publishing a web preview',
    tools: ['install_skill', 'environment_setup', 'serve_web'],
  },
  {
    label: 'Changing or testing settings, providers, channels, agents or skills',
    tools: [
      'set_config',
      'configure_provider',
      'test_provider',
      'enable_channel',
      'disable_channel',
      'configure_channel',
      'test_channel',
      'create_agent',
      'update_agent',
      'update_workspace',
      'create_skill',
      'edit_skill',
      'remove_skill',
    ],
  },
  {
    label: 'Running diagnostics',
    tools: ['run_doctor'],
  },
  {
    label: 'Adding or removing connected (MCP) servers, and MCP tools not marked safe',
    tools: ['add_mcp_server', 'remove_mcp_server'],
  },
  {
    label: 'Browser scripts and uploads',
    tools: ['browser_evaluate', 'browser_upload_file'],
  },
  {
    label: 'Mounting a folder, or files outside your workspace',
    tools: ['request_mount'],
  },
]

/** Flattened tool→group-label lookup, derived once at module load. */
export const AUTO_APPROVE_ASK_GROUP_BY_TOOL: Record<string, string> = Object.fromEntries(
  AUTO_APPROVE_ASK_GROUPS.flatMap((group) => group.tools.map((tool) => [tool, group.label])),
)
