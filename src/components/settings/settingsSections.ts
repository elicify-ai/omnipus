// settingsSections.ts — the Settings structure (Settings redesign #1519):
// 11 sections in 3 groups (FR-001), the `?tab=` link rules for new and old
// section names (FR-002 to FR-005, Dataset D-3), and the search index with
// its moved, removed and hidden items (FR-006/FR-007, Dataset D-13).
//
// Sign-in is hidden in this package (signed-in-browsers-spec.md), so it has
// no entry; `signin` and `devices` fall back to Providers like any unknown
// value.

export type SettingsSectionId =
  | 'providers'
  | 'memory'
  | 'system-agents'
  | 'third-party-agents'
  | 'limits'
  | 'protection'
  | 'sandbox'
  | 'keys'
  | 'gateway'
  | 'data'
  | 'about'

export interface SettingsSectionInfo { // not-wire-format: static screen metadata
  id: SettingsSectionId
  label: string
  group: 0 | 1 | 2
  description: string
}

export const SETTINGS_GROUPS = ['AI & agents', 'Security', 'System'] as const

export const SETTINGS_SECTIONS: readonly SettingsSectionInfo[] = [
  { id: 'providers', label: 'Providers', group: 0, description: 'Model and voice services Omnipus can use. Keys are stored on this server, never in the browser.' },
  { id: 'memory', label: 'Memory & recaps', group: 0, description: 'How agents summarise long chats so they remember what happened. Changes apply to the next recap.' },
  { id: 'system-agents', label: 'System agents', group: 0, description: 'The agents the system itself runs on. They are not part of any team and cannot be deleted. Edit opens the same agent editor every agent uses.' },
  { id: 'third-party-agents', label: 'Third-party agents', group: 0, description: 'Coding tools on this computer that your agents can hand work to. Only a person can set these here; agents cannot change them.' },
  { id: 'limits', label: 'Limits', group: 0, description: 'How much work agents may do at once, and how much text a model and its tools may use. Changes apply from the next turn; no restart.' },
  { id: 'protection', label: 'Protection', group: 1, description: 'How much agents may do without asking you. Folder and network rules are in Sandbox & network.' },
  { id: 'sandbox', label: 'Sandbox & network', group: 1, description: 'What agent programs may touch on this server: folders and network addresses.' },
  { id: 'keys', label: 'Keys & secrets', group: 1, description: 'Keys your agents and connectors use, stored encrypted on this server. A value is never shown again after it is saved.' },
  { id: 'gateway', label: 'Gateway', group: 2, description: 'How this server listens for the app and browsers.' },
  { id: 'data', label: 'Data & retention', group: 2, description: 'What Omnipus stores on this server, and how long it keeps chats and memory.' },
  { id: 'about', label: 'About', group: 2, description: '' },
]

const SECTION_IDS = new Set<string>(SETTINGS_SECTIONS.map((s) => s.id))

export function sectionInfo(id: SettingsSectionId): SettingsSectionInfo {
  return SETTINGS_SECTIONS.find((s) => s.id === id)!
}

/** Old tab names and where their settings live now (D-3 rows 3–6, 11). */
const LEGACY_TABS: Record<string, SettingsSectionId> = {
  models: 'limits',
  performance: 'limits',
  integrations: 'providers',
  security: 'protection',
  backup: 'data',
  backups: 'data',
  restore: 'data',
}

export const BACKUP_TABS = new Set(['backup', 'backups', 'restore'])

export interface ResolvedSettingsLink { // not-wire-format: screen-side resolution of the route's search params
  section: SettingsSectionId
  /** `chat` lives in Profile → Chat display now (D-3 row 10). */
  toProfile: boolean
  /** The old backup or restore link was followed (FR-003). */
  backupRemoved: boolean
  focusGodMode: boolean
  prefillOverride?: { provider: string; model: string }
  /** `?row=`: the setting row to scroll to and highlight (FR-075). */
  row?: string
}

/**
 * Resolves `?tab=&provider=&model=&focus=` (FR-003 to FR-005). Ids are exact
 * and lower-case; anything unknown (including the hidden `signin` and
 * `devices`, and a differently cased id) opens Providers without an error.
 */
export function resolveSettingsLink(search: {
  tab?: string
  provider?: string
  model?: string
  focus?: string
  row?: string
}): ResolvedSettingsLink {
  const tab = search.tab
  // FR-075: `?row=` names one setting row of the search index; the row also
  // names its section. An unknown row is ignored.
  const rowEntry = search.row ? SETTINGS_SEARCH_INDEX.find((e) => e.row === search.row && e.section) : undefined
  const prefillOverride = search.provider && search.model ? { provider: search.provider, model: search.model } : undefined
  const focusGodMode = search.focus === 'god-mode'
  let section: SettingsSectionId = 'providers'
  if (tab && SECTION_IDS.has(tab)) section = tab as SettingsSectionId
  else if (tab && tab in LEGACY_TABS) section = LEGACY_TABS[tab]
  if (rowEntry?.section) section = rowEntry.section
  if (prefillOverride) section = 'limits'
  if (focusGodMode) section = 'protection'
  return {
    section,
    toProfile: tab === 'chat',
    backupRemoved: !!tab && BACKUP_TABS.has(tab),
    focusGodMode,
    prefillOverride,
    row: rowEntry && rowEntry.section === section ? rowEntry.row : undefined,
  }
}

// ── Search (FR-006/FR-007) ───────────────────────────────────────────────────

export interface SettingsSearchEntry { // not-wire-format: static search index row
  label: string
  description: string
  /** Section that holds it; absent for moved, removed or hidden items. */
  section?: SettingsSectionId
  /** For items that are not in Settings: where they went, or why they are gone. */
  note?: string
  /** In-app destination of a moved item. */
  href?: string
  /**
   * The setting row this entry jumps to (FR-075): the value of `?row=` and of
   * the row's `data-settings-row` attribute. Absent for a whole section.
   */
  row?: string
}

/** A row identity from its visible name: "Max tool calls per turn" → "max-tool-calls-per-turn". */
export function settingsRowId(label: string): string {
  return label.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
}

/** Spread on the element that holds one setting row, so search and deep links can find it (FR-075). */
export function settingsRow(label: string): { 'data-settings-row': string } {
  return { 'data-settings-row': settingsRowId(label) }
}

/** A setting row entry; `row` overrides the identity when the row's name differs from the entry's. */
const S = (section: SettingsSectionId, label: string, description = '', row = settingsRowId(label)): SettingsSearchEntry => ({
  section,
  label,
  description,
  row,
})

export const SETTINGS_SEARCH_INDEX: readonly SettingsSearchEntry[] = [
  ...SETTINGS_SECTIONS.map((s): SettingsSearchEntry => ({ section: s.id, label: s.label, description: s.description })),
  S('providers', 'Default model', 'Used by every agent that has no model of its own.'),
  S('providers', 'Model providers', 'Connect a provider, API keys, sign-in.'),
  S('providers', 'Active transcriber', 'Voice input: turns your voice messages into text.'),
  S('providers', 'Voice input', 'Voice services and their keys.'),
  S('memory', 'Auto recap', 'Summarise a chat after it has been quiet for a while.'),
  S('memory', 'Bootstrap recap', 'Catch up on chats that changed while the server was off.'),
  S('memory', 'Summarization model', 'Primary and fallback recap models.'),
  S('memory', 'Recap limits', 'What the recap reads and writes, timing and retries.'),
  S('memory', 'How long memory is kept', 'Chat history and memory retention are in Data & retention.'),
  S('system-agents', 'Judge', 'Checks whether goals and tasks meet their acceptance criteria.'),
  S('system-agents', 'Plan Supervisor', 'Corrects a running plan when it stalls.'),
  S('system-agents', 'Omnipus', 'Sets up and changes your agents, teams, skills, connectors, providers and channels.'),
  S('third-party-agents', 'Claude Code program path', 'Third-party coding tool.'),
  S('third-party-agents', 'Codex program path', 'Third-party coding tool.'),
  S('third-party-agents', 'OpenCode program path', 'Third-party coding tool.'),
  S('limits', 'Max parallel agents', 'How many agents may run at once.'),
  S('limits', 'Tries per goal', 'How many rounds an agent gets to finish one goal.'),
  S('limits', 'Idle reminder quiet window', 'Seconds before an idle reminder.'),
  S('limits', 'Stalled-work check-ins', 'Ask a stuck agent how it is doing.'),
  S('limits', 'Max tool calls per turn', 'Lowering it first shows which agents are affected.'),
  S('limits', 'Tool result caps', 'MCP, built-in and failed tool result caps.', settingsRowId('MCP tool result cap')),
  S('limits', 'Default context window', 'Context length used when a model reports none.'),
  S('limits', 'Model overrides', 'Context length per provider and model.'),
  S('protection', 'Security health', 'Score and issues to fix.'),
  S('protection', 'Auto-approve', 'Agents run tools set to Ask without stopping for you.'),
  S('protection', 'God Mode', 'Lets every agent do anything without asking. For test installs only.'),
  S('protection', 'Prompt injection defense', 'Defense level for text from web pages, files and email.'),
  S('protection', 'Rate limits', 'Model calls per hour and tool calls per minute for each agent.'),
  S('protection', 'Audit log', 'Every tool call and permission decision.'),
  S('sandbox', 'Sandbox mode', 'Enforce, permissive or off.'),
  S('sandbox', 'Filesystem model', 'Confined or open.'),
  S('sandbox', 'Shell workspace limit', 'Where shell commands may run.'),
  S('sandbox', 'Allowed folders', 'Folders agents may use outside their working folder.'),
  S('sandbox', 'Internal network addresses', 'Private addresses agents may reach (SSRF protection).'),
  S('sandbox', 'Network proxy for shell commands', 'Routes network calls from shell commands through the address rules.'),
  S('keys', 'Stored keys', 'Keys and secrets for agents and connectors.'),
  S('keys', 'Master key', 'Encrypts every stored key.'),
  S('gateway', 'Bind address', 'Which network interfaces the gateway listens on.'),
  S('gateway', 'Port', 'The port the gateway listens on.'),
  S('gateway', 'Gateway token', 'Apps and programs use it to sign in.'),
  S('gateway', 'Preview server', 'Serves pages your agents build.'),
  S('gateway', 'Log level', 'How much detail the gateway writes to its log.'),
  S('gateway', 'Restart gateway', 'Applies saved changes that need a restart.'),
  S('gateway', 'Remote access', 'Tailscale and SSH tunnel.'),
  S('data', 'Storage', 'Workspace files, sessions and memory entries.'),
  S('data', 'Keep chat history for', 'Chat history retention in days.'),
  S('data', 'Keep memory for', 'Memory retention in days, or keep forever.'),
  S('data', 'Clear all chat history', 'Deletes all chat transcripts.'),
  S('about', 'Version', 'Version, build and system information.'),
  // Moved, removed and hidden items (FR-007).
  { label: 'Web search', description: 'Search providers and keys.', note: 'Moved to Skills & Tools.', href: '/skills' },
  { label: 'Tool permissions', description: 'Allow, Ask or Deny for each tool.', note: 'Moved to Skills & Tools.', href: '/skills' },
  { label: 'Skill trust', description: 'Whether unverified skills may load.', note: 'Moved to Skills & Tools.', href: '/skills' },
  { label: 'Verbose chat', description: 'Show every tool call in the transcript.', note: 'Moved to Profile → Chat display.', href: '/profile' },
  { label: 'Backup', description: 'Create or restore a backup.', note: 'Backup and restore were removed.' },
  { label: 'Restore', description: 'Restore a backup.', note: 'Backup and restore were removed.' },
  { label: 'Device pairing', description: 'Pair a device.', note: 'Device pairing is not available in Settings at the moment.' },
  { label: 'Signed-in browsers', description: 'Browsers signed in to your account.', note: 'Signed-in browsers are not in Settings yet.' },
]

/** Plain case-insensitive substring match over label and description (FR-006). */
export function searchSettings(query: string): SettingsSearchEntry[] {
  const q = query.trim().toLowerCase()
  if (!q) return []
  return SETTINGS_SEARCH_INDEX.filter((e) => `${e.label} ${e.description}`.toLowerCase().includes(q))
}
