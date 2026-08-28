// Humanized, sentence-case labels for tool-call chips.
//
// Tool IDs use snake_case (`recall_memory`, `browser_navigate`,
// `create_task`) and special verbs (`switch_agent`). The collapsed chip
// should show a readable label; the expanded chip still shows the raw ID so
// power users see the real tool name.
//
// Legacy dotted-namespace aliases (browser.navigate etc.) are kept for
// backward compatibility with old session transcripts.

/**
 * Explicit overrides for tools whose generic transform would read awkwardly.
 * Keep this small and data-driven — only add entries the fallback gets wrong.
 */
const EXPLICIT_LABELS: Record<string, string> = {
  remember: 'Remember',
  recall_memory: 'Recall memory',
  recall: 'Recall memory',
  // New canonical names
  run_retrospective: 'Retrospective',
  // ADR-071 D4: `hand_off` and `return_to_default` are merged into one
  // tool, `switch_agent(target, note?)` — see §5.1 of the ADR.
  switch_agent: 'Switch agent',
  // ADR-036: `delegate` replaces spawn / run_subagent / check_spawn_status
  // (one unified delegation tool — see §3.2).
  delegate: 'Delegate task',
  // Legacy names (backward compat with old session transcripts). `hand_off`
  // and `handoff` were the pre-ADR-071 names (`return_to_default`'s literal
  // is not mapped here — the generic fallback already renders it as "Return
  // to default", so no explicit entry is needed for it to stay readable).
  retrospective: 'Retrospective',
  hand_off: 'Hand off',
  handoff: 'Hand off',
  run_subagent: 'Run subagent',
  check_spawn_status: 'Check spawn status',
  spawn_status: 'Check spawn status',
  subagent: 'Run subagent',
  spawn: 'Spawn subagent',
  // ADR-036: `bash` replaces exec / workspace_shell / workspace_shell_bg
  // (one unified shell tool — see §3.1).
  bash: 'Run command',
  // Legacy names (backward compat with old session transcripts)
  exec: 'Run command',
  // New canonical browser tool names
  browser_navigate: 'Navigate browser',
  browser_screenshot: 'Take screenshot',
  browser_click: 'Click element',
  browser_type: 'Type text',
  browser_get_text: 'Read page text',
  browser_evaluate: 'Run script',
  browser_wait: 'Wait for element',
  // Legacy dotted names (backward compat with old transcripts)
  'browser.navigate': 'Navigate browser',
  'browser.screenshot': 'Take screenshot',
  'browser.click': 'Click element',
  'browser.type': 'Type text',
  'browser.get_text': 'Read page text',
  'browser.evaluate': 'Run script',
  'browser.wait': 'Wait for element',
  // Web tools
  search_web: 'Search the web',
  fetch_url: 'Fetch URL',
  serve_web: 'Serve site',
  // Legacy web tool names (backward compat)
  web_search: 'Search the web',
  web_fetch: 'Fetch URL',
  web_serve: 'Serve site',
  // Filesystem
  read_file: 'Read file',
  write_file: 'Write file',
  edit_file: 'Edit file',
  append_file: 'Append to file',
  list_directory: 'List directory',
  // Legacy filesystem names (backward compat)
  list_dir: 'List directory',
  // ADR-063 FR-7.2: an agent asking for read/write access to a folder on the
  // operator's machine. User-facing concept is "Add folder" — never "mount" —
  // see ToolApprovalModal.tsx's approvalPreviews/RequestMountApprovalPreview.
  request_mount: 'Add folder',
  // Shell — ADR-036 retires these three as distinct tools in favor of `bash`
  // (see the `bash` entry above); kept here only so old session transcripts
  // that still literally contain these tool_names render a readable label.
  workspace_shell: 'Run shell command',
  workspace_shell_bg: 'Run shell (background)',
  'workspace.shell': 'Run shell command',
  'workspace.shell_bg': 'Run shell (background)',
  // Communication
  send_message: 'Send message',
  // Task tools
  create_task: 'Create task',
  update_task: 'Update task',
  list_tasks: 'List tasks',
  delete_task: 'Delete task',
  create_task_in_workspace: 'Create task in workspace',
  update_task_in_workspace: 'Update task in workspace',
  list_tasks_in_workspace: 'List tasks in workspace',
  delete_task_in_workspace: 'Delete task in workspace',
  // Agent tools
  list_agents: 'List agents',
  create_agent: 'Create agent',
  update_agent: 'Update agent',
  delete_agent: 'Delete agent',
  read_agent_metadata: 'Read agent metadata',
  write_agent_metadata: 'Write agent metadata',
  // Token usage
  get_usage: 'Get token usage',
  // Tool discovery — canonical loader tool (renamed tools → load_tool,
  // 2026-06-26; renamed load_tool → ToolSearch, ADR-071 D1).
  // The canonical name is `ToolSearch`; it finds and loads tool schemas on demand.
  ToolSearch: 'Find & load tools',
  // Legacy names kept for backward compat with old session transcripts only.
  // Do NOT use these names for new tool calls — the canonical name is
  // `ToolSearch`. `load_tool` is retained ONLY so a conversation transcript
  // recorded before the ADR-071 D1 rename still renders a readable label
  // instead of falling through to the raw identifier (FR-015).
  load_tool: 'Find & load tools',
  /* retired: search_tools_bm25 → 'Search tools (BM25)' */
  /* retired: search_tools_regex → 'Search tools (regex)' */
  /* retired: tools → 'Tools (search & load)' (§-consolidation intermediate name) */
}

/**
 * Convert a raw tool ID into a humanized, readable label.
 *
 * - Exact match against {@link EXPLICIT_LABELS} wins.
 * - Generic fallback: drop a leading namespace segment (e.g. `system.`),
 *   split the remainder on `.`, `_`, and `-`, then sentence-case the result
 *   (capitalize only the first word; the rest stay lowercase).
 *   Example: `system.task.update` → "Task update".
 *
 * Always returns a non-empty string; falls back to the raw id when it cannot
 * derive anything meaningful.
 */
export function humanizeToolName(id: string): string {
  if (!id) return ''

  const explicit = EXPLICIT_LABELS[id]
  if (explicit) return explicit

  // Drop a single leading namespace segment so `system.task.update` reads as
  // "Task update" rather than "System task update". Only strip when there is
  // more than one dotted segment so a bare `system` (unlikely) survives.
  const dotParts = id.split('.')
  const withoutNamespace = dotParts.length > 1 ? dotParts.slice(1).join('.') : id

  // Split on the remaining separators into lowercase words.
  const words = withoutNamespace
    .split(/[._-]+/)
    .map((w) => w.trim().toLowerCase())
    .filter(Boolean)

  if (words.length === 0) return id

  // Sentence case: capitalize only the first word.
  const label = [words[0].charAt(0).toUpperCase() + words[0].slice(1), ...words.slice(1)].join(' ')
  return label || id
}
