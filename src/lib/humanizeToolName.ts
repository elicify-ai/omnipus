// Humanized, Title-Case labels for tool-call chips.
//
// Tool IDs mix conventions across the system: bare verbs (`remember`),
// snake_case (`recall_memory`), dotted namespaces (`browser.navigate`,
// `system.task.update`), and special verbs (`handoff`). The collapsed chip
// should show a readable label; the expanded chip still shows the raw ID so
// power users see the real tool name.

/**
 * Explicit overrides for tools whose generic transform would read awkwardly.
 * Keep this small and data-driven — only add entries the fallback gets wrong.
 */
const EXPLICIT_LABELS: Record<string, string> = {
  remember: 'Remember',
  recall_memory: 'Recall memory',
  recall: 'Recall memory',
  retrospective: 'Retrospective',
  handoff: 'Hand off',
  spawn: 'Spawn subagent',
  exec: 'Run command',
  'browser.navigate': 'Navigate browser',
  browser_navigate: 'Navigate browser',
  'browser.screenshot': 'Take screenshot',
  browser_screenshot: 'Take screenshot',
  'browser.click': 'Click element',
  'browser.type': 'Type text',
  'browser.get_text': 'Read page text',
  'browser.evaluate': 'Run script',
  'browser.wait': 'Wait for element',
  web_search: 'Search the web',
  web_fetch: 'Fetch URL',
  web_serve: 'Serve site',
  read_file: 'Read file',
  write_file: 'Write file',
  edit_file: 'Edit file',
  append_file: 'Append to file',
  list_dir: 'List directory',
  'workspace.shell': 'Run shell command',
  'workspace.shell_bg': 'Run shell (background)',
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
