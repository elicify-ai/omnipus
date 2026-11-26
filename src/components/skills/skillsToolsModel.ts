// skillsToolsModel — the pure parts of the Skills & Tools page: rows, groups,
// labels and the filter rules (spec FR-002 to FR-005, C-6).

import type {
  ModelVisibility, Skill, SkillCategory, SkillUpgradeUnfinishedAgent, ToolPolicy, ToolRegistryEntry,
} from '@/lib/api/generated/openapi-types'
import type { PermissionSaveQueue, PermissionSection } from '@/lib/permissionSaveQueue'

export const POLICIES: readonly ToolPolicy[] = ['allow', 'ask', 'deny']

export const POLICY_WORDS: Record<ToolPolicy, string> = { allow: 'Allow', ask: 'Ask', deny: 'Deny' }

export const LEVELS: readonly ModelVisibility[] = ['hidden', 'name_only', 'name_and_description', 'full']

export const LEVEL_WORDS: Record<ModelVisibility, string> = {
  hidden: 'Hidden',
  name_only: 'Name only',
  name_and_description: 'Name and short description',
  full: 'Full',
}

/** One line per level: what the model is sent (spec "Meaning" table). */
export const LEVEL_MEANINGS: Record<ModelVisibility, string> = {
  hidden: 'never sent up front; search can still find it',
  name_only: 'the name, no description',
  name_and_description: 'the name and one line',
  full: 'the whole definition, every turn',
}

/** Category names as the spec (Assumptions, built-in categories) and the approved wireframe write them. */
export const SKILL_CATEGORY_WORDS: Record<SkillCategory, string> = {
  tools: 'Tools',
  business: 'Business',
  development: 'Development',
  testing_security: 'Testing & Security',
  data_ai: 'Data & AI',
  devops: 'DevOps',
  documentation: 'Documentation',
  content_media: 'Content & Media',
  research: 'Research',
  lifestyle: 'Lifestyle',
  databases: 'Databases',
  blockchain: 'Blockchain',
  uncategorised: 'Uncategorised',
}

const TOOL_CATEGORY_WORDS: Record<string, string> = {
  file: 'Files',
  filesystem: 'Files',
  shell: 'Shell',
  web: 'Web & Search',
  communication: 'Communication',
  plans: 'Plans',
  core: 'General',
  browser: 'Browser',
}

/** The group a tool category shows under ("file" and "filesystem" are one). */
export function toolGroup(category: string): { id: string; label: string } {
  const label = TOOL_CATEGORY_WORDS[category]
  if (label) return { id: label.toLowerCase(), label }
  const words = category.replace(/[_-]+/g, ' ').trim() || 'other'
  return { id: words.toLowerCase(), label: words.charAt(0).toUpperCase() + words.slice(1) }
}

/** Tools that ask for every call and so can never be Allow (FR-036). */
export const ALWAYS_ASK_NO_ALLOW: readonly string[] = ['set_channel_outbound']

export const DISCOVERY_SENTENCE = 'Always available for discovery. The tools it finds still follow their own permissions.'
export const SKILL_TOOL_SENTENCE = 'Permissions are set per skill on the Skills tab.'
export const LOCKED_LEVEL_SENTENCE = 'Always loaded: agents need this to work, so its level cannot be changed.'

/** One row of a list: a tool or a skill. // not-wire-format */
export interface CatalogItem {
  /** The permission key (tool name, or the lower-case skill id). */
  key: string
  label: string
  description: string
  group: { id: string; label: string }
  policySection: Extract<PermissionSection, 'policies' | 'skill_policies'>
  levelSection: Extract<PermissionSection, 'visibility' | 'skill_visibility'>
  /**
   * Keys the stored value falls back to, narrowest first: an MCP server's
   * wildcard, then the wildcard of every service (C-7, C-18).
   */
  fallbackKeys?: readonly string[]
  /** Allow / Ask / Deny are not shown: the fixed sentence is. */
  fixedSentence?: string
  /** The level cannot change (C-6). */
  levelLocked: boolean
  /** The level the server reports when nothing is stored. */
  defaultLevel: ModelVisibility
  /** Values this row may not take (always-ask tools never Allow). */
  refused: readonly ToolPolicy[]
  /** Why the server could not read this skill (S-80, FR-063). */
  loadError?: string
  /** A skill row's own facts (S-34, ADR-072 D3.1, C-14). */
  skill?: {
    verified: boolean
    /** When an agent last loaded it; null when never. */
    lastInvoked: string | null
    /** What the skill costs at Full, when the server sent it. */
    fullTokens?: number
  }
}

export function toolItems(tools: readonly ToolRegistryEntry[]): CatalogItem[] {
  return tools.map((tool) => {
    const external = tool.source === 'mcp'
    const server = tool.server_id ?? ''
    return {
      key: tool.name,
      label: tool.name,
      description: tool.description,
      group: external ? { id: `mcp:${server}`, label: server || 'External' } : toolGroup(tool.category),
      policySection: 'policies',
      levelSection: 'visibility',
      ...(external ? { fallbackKeys: server ? [`mcp_${server}_*`, 'mcp_*'] : ['mcp_*'] } : {}),
      ...(tool.permission_fixed
        ? { fixedSentence: tool.name === 'Skill' ? SKILL_TOOL_SENTENCE : DISCOVERY_SENTENCE }
        : {}),
      levelLocked: tool.visibility_locked,
      defaultLevel: tool.visibility,
      refused: ALWAYS_ASK_NO_ALLOW.includes(tool.name) ? ['allow'] : [],
    }
  })
}

export function skillItems(skills: readonly Skill[]): CatalogItem[] {
  return skills.map((skill) => {
    const category = (skill.category ?? 'uncategorised') as SkillCategory
    return {
      key: skill.id.toLowerCase(),
      label: skill.name,
      description: skill.description ?? '',
      group: { id: category, label: SKILL_CATEGORY_WORDS[category] ?? category },
      policySection: 'skill_policies',
      levelSection: 'skill_visibility',
      levelLocked: false,
      defaultLevel: 'name_and_description',
      refused: [],
      ...(skill.load_error ? { loadError: skill.load_error } : {}),
      skill: {
        verified: skill.verified,
        lastInvoked: skill.last_invoked ?? null,
        ...(typeof skill.full_tokens === 'number' ? { fullTokens: skill.full_tokens } : {}),
      },
    }
  })
}

/** The row's own stored value, else the first wildcard that has one. */
function valueWithFallback(queue: PermissionSaveQueue, section: PermissionSection, item: CatalogItem) {
  for (const key of [item.key, ...(item.fallbackKeys ?? [])]) {
    const value = queue.valueOf(section, key)
    if (value !== undefined) return value
  }
  return undefined
}

export function policyOf(queue: PermissionSaveQueue, item: CatalogItem): ToolPolicy | undefined {
  return valueWithFallback(queue, item.policySection, item) as ToolPolicy | undefined
}

export function levelOf(queue: PermissionSaveQueue, item: CatalogItem): ModelVisibility {
  if (item.levelLocked) return 'full'
  return (valueWithFallback(queue, item.levelSection, item) ?? item.defaultLevel) as ModelVisibility
}

/**
 * A Full skill's size on its row (C-14, D18 rows 1-5): rounded to the
 * nearest 100, half up; below 50 it reads "under 100 tokens".
 */
export function fullTokensText(tokens: number): string {
  if (tokens < 50) return 'under 100 tokens'
  return `about ${(Math.round(tokens / 100) * 100).toLocaleString('en-US')} tokens`
}

/** The figures of one agent the Full-size warning reads (C-14). // not-wire-format */
export interface AgentContextFigures {
  name: string
  full_skills_tokens?: number
  context_window_effective?: number
}

/**
 * The agents whose Full skills take more than half of their model's context
 * (C-14 "Page warning", D18 rows 6, 7, 11, 12). Exactly half is not over;
 * an agent without both figures, or with a window of 0, is never named.
 */
export function agentsOverHalf(agents: readonly AgentContextFigures[]): string[] {
  return agents
    .filter(({ full_skills_tokens: tokens, context_window_effective: window }) =>
      typeof tokens === 'number' && typeof window === 'number' && window > 0 && tokens * 2 > window)
    .map((agent) => agent.name)
}

export function fullSizeWarning(names: readonly string[]): string {
  return `The skills set to Full take more than half of the model's context for ${names.join(', ')}. ` +
    'Set some of them to Name and short description, or choose a model with a larger context.'
}

const UNFINISHED_REASON_WORDS: Record<SkillUpgradeUnfinishedAgent['reason'], string> = {
  write_failed: 'its settings file could not be saved',
  unreadable: 'its settings file could not be read',
}

/**
 * The unfinished-upgrade notice (C-19 "The notice", S-131): each agent by
 * its name from GET /agents, else its id, with its reason in words.
 */
export function upgradeNotice(
  entries: readonly SkillUpgradeUnfinishedAgent[],
  nameOf: (agentId: string) => string | undefined,
): string {
  const listed = entries.map((entry) => `${nameOf(entry.agent_id) ?? entry.agent_id} (${UNFINISHED_REASON_WORDS[entry.reason]})`)
  const count = entries.length === 1 ? '1 agent' : `${entries.length} agents`
  return `The upgrade has not finished for ${count}: ${listed.join(', ')}. Their old skill limits still apply.`
}

/** The list's search and two flat filters (FR-002). // not-wire-format */
export interface CatalogFilters {
  search: string
  policy: ToolPolicy | null
  level: ModelVisibility | null
}

export function filtersActive(filters: CatalogFilters): boolean {
  return filters.search.trim() !== '' || filters.policy !== null || filters.level !== null
}

/**
 * Whether a row shows under the filters. A row whose change is still being
 * saved stays until the save settles, then leaves if it no longer matches
 * (FR-005). Denied items are left out of a level filter: they are not loaded.
 */
export function rowMatches(queue: PermissionSaveQueue, item: CatalogItem, filters: CatalogFilters): boolean {
  const text = filters.search.trim().toLowerCase()
  if (text && !`${item.label} ${item.key} ${item.description}`.toLowerCase().includes(text)) return false
  if (filters.policy === null && filters.level === null) return true
  if (queue.isPending(item.policySection, item.key) || queue.isPending(item.levelSection, item.key)) return true
  const policy = item.fixedSentence ? 'allow' : policyOf(queue, item)
  if (filters.policy !== null && policy !== filters.policy) return false
  if (filters.level !== null && (policy === 'deny' || levelOf(queue, item) !== filters.level)) return false
  return true
}

/** Groups in first-seen order, each with its rows. */
export function groupItems(items: readonly CatalogItem[]): Array<{ id: string; label: string; items: CatalogItem[] }> {
  const groups = new Map<string, { id: string; label: string; items: CatalogItem[] }>()
  for (const item of items) {
    const group = groups.get(item.group.id) ?? { ...item.group, items: [] }
    group.items.push(item)
    groups.set(item.group.id, group)
  }
  return [...groups.values()].sort((a, b) => a.label.localeCompare(b.label))
}
