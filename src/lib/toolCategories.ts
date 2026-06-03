import type { BuiltinTool } from '@/lib/api'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'

export const CATEGORY_LABELS: Record<string, string> = {
  file: 'File & Code',
  code: 'Code Execution',
  web: 'Web & Search',
  browser: 'Browser Automation',
  communication: 'Communication',
  task: 'Task Management',
  automation: 'Automation',
  search: 'Search & Discovery',
  skills: 'Skills',
  hardware: 'Hardware (IoT)',
  system: 'System',
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
 *  3. Default policy fallback.
 *
 * The backend stores glob keys like `system.*` to seed the privilege rail
 * (custom agents default to system.*=deny). Without glob resolution the
 * per-agent consumer with that value renders system tools as if no override
 * exists, which is incorrect.
 */
export function resolvePolicy(
  toolName: string,
  policies: Record<string, ToolPolicy> | undefined,
  defaultPolicy: ToolPolicy,
): ToolPolicy {
  if (!policies) return defaultPolicy

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

  // 3. Default.
  return defaultPolicy
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
