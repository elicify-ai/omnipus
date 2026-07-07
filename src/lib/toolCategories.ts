import type { BuiltinTool } from '@/lib/api'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'

export const CATEGORY_LABELS: Record<string, string> = {
  // Domain categories (new, post-rename)
  filesystem: 'Files',
  shell: 'Shell',
  web: 'Web & Search',
  browser: 'Browser',
  communication: 'Communication',
  delegation: 'Delegation',
  memory: 'Memory',
  tasks: 'Tasks',
  skills: 'Skills',
  tool_discovery: 'Tool Discovery',
  agents: 'Agents',
  workspaces: 'Workspaces',
  channels: 'Channels',
  providers: 'Providers',
  platform: 'Platform',
  mcp: 'MCP',
  // Legacy category values kept for backward compatibility with older tool registries.
  file: 'Files',
  code: 'Code',
  task: 'Tasks',
  automation: 'Automation',
  search: 'Search & Discovery',
  hardware: 'Hardware',
  // 'system' is the legacy category for un-recategorized system.* tools.
  system: 'System',
  // 'core' is the legacy category emitted by un-recategorized general builtins;
  // render as "General" so no raw internal key leaks to users (AC4 / FR-103).
  core: 'General',
  // Fallback for tools with no recognised category.
  other: 'Other',
}

/**
 * Resolve the effective policy for a tool.
 *
 * Resolution order (most specific first):
 *  1. Exact match: policies['browser.evaluate'] → direct hit.
 *  2. Glob match:  policies['system.*'] applies to any tool name that starts
 *     with 'system.' — the only glob pattern the backend seeds is `<prefix>.*`.
 *     We support the general `<prefix>.*` form: strip the trailing `.*` from
 *     the glob key, then check that the tool name starts with that prefix
 *     followed by '.'.
 *
 * There is no third "default policy" fallback step. The backend guarantees
 * (via boot-time + write-time hard validation, Constraint #6) that every
 * static builtin tool has an explicit, literal policy entry, so a tool that
 * resolves to neither an exact nor a glob match is genuinely anomalous — stale
 * or incomplete local state, not a legitimate "use the default" case. Callers
 * MUST treat the `undefined` return as a distinct "unconfigured / needs
 * attention" state — never silently coerce it to 'allow'.
 */
export function resolvePolicy(
  toolName: string,
  policies: Record<string, ToolPolicy> | undefined,
): ToolPolicy | undefined {
  if (!policies) return undefined

  // 1. Exact match (most specific — takes precedence over any glob).
  if (Object.prototype.hasOwnProperty.call(policies, toolName)) {
    return policies[toolName]
  }

  // 2. Glob match — iterate keys ending in '.*' and test prefix.
  for (const key of Object.keys(policies)) {
    if (!key.endsWith('.*')) continue
    const prefix = key.slice(0, -2) // e.g. 'system.*' → 'system'
    if (toolName.startsWith(prefix + '.')) {
      return policies[key]
    }
  }

  // 3. Genuinely unconfigured — no default to fall back to.
  return undefined
}

export function groupByCategory(tools: BuiltinTool[]): Record<string, BuiltinTool[]> {
  const groups: Record<string, BuiltinTool[]> = {}
  for (const t of tools) {
    const cat = t.category || 'other'
    if (!groups[cat]) groups[cat] = []
    groups[cat].push(t)
  }
  return groups
}
