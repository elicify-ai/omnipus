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

/** A parsed wildcard policy key (e.g. 'system.*' or 'mcp_github_*'). */
interface WildcardEntry { // not-wire-format: internal policy-resolution index entry, never crosses the gateway boundary
  prefix: string
  /** Dot-segment count for '.*' wildcards; always 1 for '_*' wildcards. */
  segments: number
  delimiter: '.' | '_'
  policy: ToolPolicy
}

/**
 * Parse a policy map's wildcard keys and sort them by specificity, exactly
 * mirroring the backend's buildWildcardIndex (pkg/tools/compositor.go):
 * primary key is segment count descending (a '.*' wildcard's segments is its
 * dot-count + 1; a '_*' wildcard's segments is always 1, since underscore
 * names are single-segment identifiers), secondary key is prefix length
 * descending, tertiary key is prefix lexicographic ascending. Both wildcard
 * forms compete in this ONE ranking, not two separate longest-prefix
 * contests keyed by delimiter.
 */
function buildWildcardIndex(policies: Record<string, ToolPolicy>): WildcardEntry[] {
  const entries: WildcardEntry[] = []
  for (const key of Object.keys(policies)) {
    let prefix: string
    let delimiter: '.' | '_'
    let segments: number
    if (key.endsWith('.*')) {
      prefix = key.slice(0, -2) // e.g. 'system.*' → 'system'
      delimiter = '.'
      segments = prefix.split('.').length // dot-count + 1
    } else if (key.endsWith('_*')) {
      prefix = key.slice(0, -2) // e.g. 'mcp_github_*' → 'mcp_github'
      delimiter = '_'
      segments = 1
    } else {
      continue
    }
    if (prefix === '') continue // a bare '.*'/'_*' would match everything — ignore it
    entries.push({ prefix, segments, delimiter, policy: policies[key] })
  }
  entries.sort((a, b) => {
    if (a.segments !== b.segments) return b.segments - a.segments
    if (a.prefix.length !== b.prefix.length) return b.prefix.length - a.prefix.length
    return a.prefix < b.prefix ? -1 : a.prefix > b.prefix ? 1 : 0
  })
  return entries
}

/**
 * Resolve the effective policy for a tool.
 *
 * Resolution order, an exact mirror of the backend's resolveFromMap
 * (pkg/tools/compositor.go — exact > most-specific wildcard, no default
 * fallback), for every input, not just single-segment keys:
 *  1. Exact match: policies['browser.evaluate'] → direct hit.
 *  2. Wildcard match — two trailing forms, both competing in ONE
 *     specificity-ranked contest (see buildWildcardIndex):
 *       - '.*' (dot-delimited): policies['system.*'] matches any tool name
 *         that starts with 'system.' or equals 'system'. This is the form
 *         the backend seeds for dot-namespaced builtin tools.
 *       - '_*' (underscore-delimited): policies['mcp_github_*'] matches any
 *         tool name that starts with 'mcp_github_' or equals 'mcp_github'.
 *         This is the form MCP tool names use (mcp_<server>_<tool>) —
 *         ToolPolicyEditor's per-server bulk control (McpServerSection)
 *         writes exactly this kind of key.
 *     Segment count wins first (e.g. 'a.b.*' — 2 segments — beats
 *     'a.b.c_*' — 1 segment — for a tool named 'a.b.c_d', even though
 *     'a.b.c_*' has the longer literal prefix); prefix length is only the
 *     tie-breaker when segment counts are equal (e.g. 'mcp_github_mcp_*'
 *     beats 'mcp_github_*' for 'mcp_github_mcp_search' — both are
 *     single-segment '_*' wildcards).
 *
 * There is no third "default policy" fallback step. The backend guarantees
 * (via boot-time + write-time hard validation, Constraint #6) that every
 * static builtin tool has an explicit, literal policy entry, so a tool that
 * resolves to neither an exact nor a wildcard match is genuinely anomalous —
 * stale or incomplete local state, not a legitimate "use the default" case.
 * Callers MUST treat the `undefined` return as a distinct "unconfigured /
 * needs attention" state — never silently coerce it to 'allow'.
 */
export function resolvePolicy(
  toolName: string,
  policies: Record<string, ToolPolicy> | undefined,
): ToolPolicy | undefined {
  if (!policies) return undefined

  // 1. Exact match (most specific — takes precedence over any wildcard).
  if (Object.prototype.hasOwnProperty.call(policies, toolName)) {
    return policies[toolName]
  }

  // 2. Most-specific wildcard match (already sorted by specificity).
  for (const w of buildWildcardIndex(policies)) {
    const matches =
      w.delimiter === '.'
        ? toolName.startsWith(w.prefix + '.') || toolName === w.prefix
        : toolName.startsWith(w.prefix + '_') || toolName === w.prefix
    if (matches) return w.policy
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
