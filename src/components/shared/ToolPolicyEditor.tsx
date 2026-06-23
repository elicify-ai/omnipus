/**
 * ToolPolicyEditor — THE shared role-preset + collapsed-category tool editor.
 *
 * Replaces the duplicated logic in:
 *   - ToolsAndPermissions.tsx (per-agent)
 *   - GlobalToolPoliciesSection in SecuritySection.tsx (global)
 *
 * Layout (top to bottom):
 * 1. Role preset selector (Cautious / Balanced / Full access) via toolPolicyPresets.
 * 2. Single de-duplicated category grid — ALL builtin tools appear exactly once,
 *    grouped by their human-readable category label. MCP tools are NOT here.
 * 3. MCP tools — grouped per-server with a source badge.
 * 4. Default policy control (inside "Customize defaults (advanced)" — no tool re-list).
 *
 * Design decisions (US-1 / AC5 / FR-103):
 * - There is NO separate "system" disclosure — system.* tools appear in the
 *   category grid under the "System" label alongside all other builtins.
 * - There is NO raw per-tool re-listing — each tool appears exactly once.
 * - The `core` category (un-recategorized general builtins) renders as "General"
 *   via CATEGORY_LABELS (AC4). The raw key "core" or "system" is never shown
 *   as a user-facing heading.
 *
 * Props:
 *   tools    — full RegistryTool[] from GET /api/v1/tools
 *   value    — {default_policy, policies} — the current (persisted or local) state
 *   onChange — called synchronously with the new value on every user interaction
 *
 * Policy round-trip: loading a value and saving unchanged emits a byte-identical
 * payload. The component never mutates `value` in place.
 *
 * Spec §2.1 / US-1 / AC5 / FR-103 / Issue #357.
 */

import { useMemo, useState } from 'react'
import { CaretDown, CaretUp, Database, LockSimple } from '@phosphor-icons/react'
import type { RegistryTool } from '@/lib/api'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'
import { PolicyBadge } from '@/components/shared/PolicyBadge'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { CATEGORY_LABELS, groupByCategory, resolvePolicy } from '@/lib/toolCategories'
import {
  POLICY_PRESETS,
  applyRolePreset,
  type RolePreset,
  type ToolPolicyValue,
} from '@/lib/toolPolicyPresets'

// ── Types ──────────────────────────────────────────────────────────────────────

// ToolPolicyValue is the single canonical type (HC#8) — re-exported so callers
// that only import from this component get the same type.
export type { ToolPolicyValue }

export interface ToolPolicyEditorProps {
  /** Full tool list from GET /api/v1/tools. */
  tools: RegistryTool[]
  /** Current value (controlled). */
  value: ToolPolicyValue
  /** Called with the new value on every user interaction. */
  onChange: (next: ToolPolicyValue) => void
  /** Whether the editor is read-only (e.g. during a save in flight). */
  disabled?: boolean
  /**
   * Global (Settings → Security) tool policy. When set, a per-tool control that
   * would be LESS restrictive than the global floor is locked (the runtime merge
   * is most-restrictive-wins: deny > ask > allow, so a global deny/ask cannot be
   * relaxed at the agent level). Omit in the global editor itself — there is no
   * self-override there. When the global value resolves to "allow" for a tool,
   * nothing is locked.
   */
  globalPolicies?: ToolPolicyValue
}

// ── Helpers ────────────────────────────────────────────────────────────────────

const ALL_POLICIES: ToolPolicy[] = ['allow', 'ask', 'deny']

// Restrictiveness rank — the runtime merge keeps the MORE restrictive of
// global×agent (deny > ask > allow). A per-agent policy strictly LESS restrictive
// than the global floor is unreachable, so we lock it.
const POLICY_RANK: Record<ToolPolicy, number> = { allow: 0, ask: 1, deny: 2 }

/**
 * Resolve the global override floor for a tool, or null when the global policy
 * imposes no restriction (resolves to "allow", or no global policy provided).
 */
function globalOverrideFor(toolName: string, global?: ToolPolicyValue): ToolPolicy | null {
  if (!global) return null
  const g = resolvePolicy(toolName, global.policies, (global.default_policy || 'allow') as ToolPolicy)
  return g === 'allow' ? null : g
}

/** A per-agent policy is locked when it is strictly less restrictive than the floor. */
function isPolicyLocked(p: ToolPolicy, floor: ToolPolicy | null): boolean {
  if (!floor) return false
  return POLICY_RANK[p] < POLICY_RANK[floor]
}

/** Resolve the single summary policy for a category, or 'mixed' if heterogeneous. */
function categorySummaryPolicy(
  toolsInCategory: RegistryTool[],
  policies: Record<string, ToolPolicy>,
  defaultPolicy: ToolPolicy,
): ToolPolicy | 'mixed' {
  if (toolsInCategory.length === 0) return defaultPolicy
  const first = resolvePolicy(toolsInCategory[0].name, policies, defaultPolicy)
  for (let i = 1; i < toolsInCategory.length; i++) {
    if (resolvePolicy(toolsInCategory[i].name, policies, defaultPolicy) !== first) {
      return 'mixed'
    }
  }
  return first
}

/** Detect which preset (if any) the current value matches. */
function detectActivePreset(value: ToolPolicyValue): RolePreset | null {
  for (const [key, preset] of Object.entries(POLICY_PRESETS) as [RolePreset, typeof POLICY_PRESETS[RolePreset]][]) {
    if (preset.defaultPolicy !== value.default_policy) continue
    const presetOverrideKeys = Object.keys(preset.overrides)
    const currentOverrideKeys = Object.keys(value.policies)
    if (presetOverrideKeys.length !== currentOverrideKeys.length) continue
    const matches = presetOverrideKeys.every((k) => value.policies[k] === preset.overrides[k])
    if (matches) return key
  }
  return null
}

/**
 * Extract the MCP server name from a registered MCP tool name.
 *
 * Fallback only — used when a tool has no server_id field (older payloads).
 * Prefer grouping by tool.server_id (see groupMcpByServer).
 *
 * The backend (pkg/tools/mcp_tool.go MCPTool.Name()) formats MCP tool names as:
 *   mcp_<sanitizedServer>_<sanitizedTool>
 * with an optional hash suffix when sanitization is lossy or the name exceeds
 * 64 characters.
 *
 * The server name is the segment immediately after the 'mcp_' prefix —
 * i.e. the characters up to the first underscore after 'mcp_'. Note: this
 * is ambiguous when the sanitized server name itself contains an underscore
 * (e.g. mcp_github_mcp_<tool> would extract 'github', not 'github_mcp'),
 * because the backend sanitizer permits underscores in server names.
 *
 * We NEVER key on tool.category (two tools from the same server can carry
 * different categories; two servers can share a category value).
 *
 * Falls back to the whole name when the 'mcp_' prefix is absent (should not
 * happen in practice since source:'mcp' tools always go through MCPTool.Name()).
 */
function mcpServerFromToolName(toolName: string): string {
  // Expected format: mcp_<server>_<tool>[_<hash>]
  const MCP_PREFIX = 'mcp_'
  if (!toolName.startsWith(MCP_PREFIX)) {
    // Unexpected format — fall back gracefully.
    return toolName.split('_')[0] || 'mcp'
  }
  const withoutPrefix = toolName.slice(MCP_PREFIX.length) // '<server>_<tool>...'
  const firstUnderscore = withoutPrefix.indexOf('_')
  if (firstUnderscore === -1) {
    // Only server segment, no tool suffix — treat the whole thing as server.
    return withoutPrefix || 'mcp'
  }
  return withoutPrefix.slice(0, firstUnderscore)
}

/**
 * Compute the longest common prefix of a list of tool names that ends with '_'.
 *
 * For a group of MCP tools like ['mcp_github_mcp_search', 'mcp_github_mcp_list'],
 * this returns 'mcp_github_mcp_'. Appending '*' gives the wildcard policy key.
 *
 * Returns '' when no common underscore-terminated prefix exists (safe fallback —
 * the caller should not write a wildcard in that case, but in practice every MCP
 * group shares at least the 'mcp_<server>_' prefix).
 */
function longestCommonUnderscorePrefix(toolNames: string[]): string {
  if (toolNames.length === 0) return ''
  // Find the longest common character prefix of all names.
  let prefix = toolNames[0]
  for (let i = 1; i < toolNames.length; i++) {
    const name = toolNames[i]
    let j = 0
    while (j < prefix.length && j < name.length && prefix[j] === name[j]) j++
    prefix = prefix.slice(0, j)
    if (prefix === '') return ''
  }
  // Trim to the last '_' (inclusive) so the prefix ends with '_'.
  const lastUnderscore = prefix.lastIndexOf('_')
  if (lastUnderscore === -1) return ''
  return prefix.slice(0, lastUnderscore + 1)
}

/**
 * Group MCP tools by server.
 *
 * Uses tool.server_id when present (unambiguous, even when the server name
 * contains underscores). Falls back to mcpServerFromToolName for older
 * payloads that pre-date the server_id field.
 */
function groupMcpByServer(mcpTools: RegistryTool[]): Record<string, RegistryTool[]> {
  const servers: Record<string, RegistryTool[]> = {}
  for (const tool of mcpTools) {
    const serverName = tool.server_id != null ? tool.server_id : mcpServerFromToolName(tool.name)
    if (!servers[serverName]) servers[serverName] = []
    servers[serverName].push(tool)
  }
  return servers
}

// ── Sub-components ─────────────────────────────────────────────────────────────

/** A single expanded category row showing per-tool policy badges. */
function CategoryToolRow({
  tool,
  policies,
  defaultPolicy,
  onChange,
  disabled,
  globalPolicies,
}: {
  tool: RegistryTool
  policies: Record<string, ToolPolicy>
  defaultPolicy: ToolPolicy
  onChange: (toolId: string, p: ToolPolicy) => void
  disabled?: boolean
  globalPolicies?: ToolPolicyValue
}) {
  const effective = resolvePolicy(tool.name, policies, defaultPolicy)
  const floor = globalOverrideFor(tool.name, globalPolicies)
  const floorLabel = floor === 'deny' ? 'Deny' : floor === 'ask' ? 'Ask' : ''
  const lockTitle = floor
    ? `Locked by a global "${floorLabel}" policy in Settings → Security. ` +
      `The effective policy is the most restrictive of the global and per-agent values (deny > ask > allow), ` +
      `so it cannot be relaxed here.`
    : undefined
  return (
    <div className="flex items-center justify-between py-1 gap-2" data-testid={`tool-row-${tool.name}`}>
      <span className="text-[11px] text-[var(--color-muted)] font-mono truncate flex-1 flex items-center gap-1.5" title={tool.name}>
        <span className="truncate">{tool.name}</span>
        {floor && (
          // Hash link (no full reload) to Settings → Security where the global
          // policy is managed. Plain anchor (not router Link) so the editor
          // renders without a RouterProvider in any context/test.
          <a
            href="/#/settings"
            title={lockTitle}
            data-testid={`global-override-${tool.name}`}
            className="inline-flex items-center gap-0.5 shrink-0 px-1 py-0.5 rounded text-[9px] font-semibold border border-[var(--color-border)] text-[var(--color-warning)] hover:bg-[var(--color-surface-2)]"
          >
            <LockSimple size={9} weight="bold" />
            Global: {floorLabel}
          </a>
        )}
      </span>
      <div className="flex gap-1 shrink-0">
        {ALL_POLICIES.map((p) => {
          const locked = isPolicyLocked(p, floor)
          return (
            <PolicyBadge
              key={p}
              policy={p}
              active={effective === p}
              disabled={disabled || locked}
              title={locked ? lockTitle : undefined}
              onClick={() => onChange(tool.name, p)}
            />
          )
        })}
      </div>
    </div>
  )
}

/**
 * CategorySection — collapsible row for a single tool category.
 *
 * The summary pill is ALWAYS visible in the trigger header (not gated on
 * expand state) so tests and users can see the category's policy at a glance
 * without expanding. The pill uses data-testid="category-pill-{categoryKey}".
 */
function CategorySection({
  categoryKey,
  tools,
  policies,
  defaultPolicy,
  onToolChange,
  disabled,
  globalPolicies,
}: {
  categoryKey: string
  tools: RegistryTool[]
  policies: Record<string, ToolPolicy>
  defaultPolicy: ToolPolicy
  onToolChange: (toolId: string, p: ToolPolicy) => void
  disabled?: boolean
  globalPolicies?: ToolPolicyValue
}) {
  const [open, setOpen] = useState(false)
  const label = CATEGORY_LABELS[categoryKey] ?? categoryKey
  const summary = categorySummaryPolicy(tools, policies, defaultPolicy)

  const PILL_CLASS: Record<ToolPolicy | 'mixed', string> = {
    mixed:  'bg-violet-500/20 text-violet-300 border-violet-500/40',
    allow:  'bg-emerald-500/20 text-emerald-400 border-emerald-500/40',
    ask:    'bg-amber-500/20 text-amber-400 border-amber-500/40',
    deny:   'bg-red-500/20 text-red-400 border-red-500/40',
  }
  const pillClass = PILL_CLASS[summary]

  const PILL_LABEL: Record<ToolPolicy | 'mixed', string> = {
    mixed: 'Mixed',
    allow: 'Allow',
    ask:   'Ask',
    deny:  'Deny',
  }

  return (
    <div
      className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
    >
      {/* Trigger — always visible; pill is IN the trigger row */}
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex items-center justify-between w-full px-3 py-2.5 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors"
      >
        <span className="flex items-center gap-2">
          <span>{label}</span>
          {/* Summary pill — always visible in the header */}
          <span
            className={`px-1.5 py-0.5 rounded text-[9px] font-semibold border ${pillClass}`}
            data-testid={`category-pill-${categoryKey}`}
          >
            {PILL_LABEL[summary]}
          </span>
        </span>
        {open ? <CaretUp size={13} /> : <CaretDown size={13} />}
      </button>

      {/* Expanded content */}
      {open && (
        <div className="px-3 pb-3 border-t border-[var(--color-border)] pt-3">
          <div className="space-y-0.5">
            {tools.map((tool) => (
              <CategoryToolRow
                key={tool.name}
                tool={tool}
                policies={policies}
                defaultPolicy={defaultPolicy}
                onChange={onToolChange}
                disabled={disabled}
                globalPolicies={globalPolicies}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

/**
 * McpServerSection — collapsible row for a single MCP server group.
 *
 * The header contains the server name + a per-server bulk allow/ask/deny control.
 * Bulk controls write a single wildcard policy key (`<commonPrefix>*`) into the
 * policies map. The common prefix is the longest prefix of all tool names in the
 * group that ends with '_' (e.g. `mcp_github_mcp_` for tools from the server
 * "github_mcp"). Allow removes the wildcard key; Ask/Deny sets it.
 *
 * The trigger button carries data-testid="advanced-disclosure-trigger" for
 * compatibility with existing tests that look for that attribute.
 */
function McpServerSection({
  server,
  serverTools,
  policies,
  defaultPolicy,
  onToolChange,
  onWildcardPolicy,
  disabled,
  globalPolicies,
}: {
  server: string
  serverTools: RegistryTool[]
  policies: Record<string, ToolPolicy>
  defaultPolicy: ToolPolicy
  onToolChange: (toolId: string, p: ToolPolicy) => void
  onWildcardPolicy: (wildcardKey: string, p: ToolPolicy | null) => void
  disabled?: boolean
  globalPolicies?: ToolPolicyValue
}) {
  const [open, setOpen] = useState(false)

  // Compute the wildcard key from the common underscore-terminated prefix of
  // all tool names. Never re-sanitize the server name — derive from actual names.
  const wildcardKey = useMemo(() => {
    const prefix = longestCommonUnderscorePrefix(serverTools.map((t) => t.name))
    return prefix ? `${prefix}*` : null
  }, [serverTools])

  // Reflect current per-server state: look up the wildcard key in policies.
  const currentWildcard: ToolPolicy | null = wildcardKey != null ? (policies[wildcardKey] ?? null) : null

  const BULK_BUTTON_CLASS = (active: boolean, policy: ToolPolicy) => {
    const baseInactive = 'border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
    const activeAllow = 'bg-emerald-500/20 text-emerald-400 border-emerald-500/40'
    const activeAsk   = 'bg-amber-500/20 text-amber-400 border-amber-500/40'
    const activeDeny  = 'bg-red-500/20 text-red-400 border-red-500/40'
    if (!active) return baseInactive
    if (policy === 'allow') return activeAllow
    if (policy === 'ask')   return activeAsk
    return activeDeny
  }

  return (
    <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
      {/* Header row: toggle + server name + bulk controls */}
      <div className="flex items-center justify-between px-3 py-2">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="flex items-center gap-2 text-sm font-medium text-[var(--color-secondary)] hover:text-[var(--color-accent)] transition-colors min-w-0 flex-1 text-left"
          data-testid="advanced-disclosure-trigger"
        >
          <span className="truncate">{server}</span>
          <span className="text-[11px] font-normal text-[var(--color-muted)] shrink-0">
            {serverTools.length} tool{serverTools.length !== 1 ? 's' : ''}
          </span>
          {open ? <CaretUp size={13} className="shrink-0" /> : <CaretDown size={13} className="shrink-0" />}
        </button>

        {/* Per-server bulk allow/ask/deny — only shown when a wildcard key is derivable */}
        {wildcardKey != null && (
          <div
            className="flex gap-1 shrink-0 ml-3"
            data-testid={`mcp-server-bulk-${server}`}
          >
            {ALL_POLICIES.map((p) => {
              // Allow = no wildcard override (remove the key)
              const isActive = p === 'allow' ? currentWildcard === null : currentWildcard === p
              return (
                <button
                  key={p}
                  type="button"
                  disabled={disabled}
                  aria-pressed={isActive}
                  data-testid={`mcp-server-bulk-${server}-${p}`}
                  title={`Set all ${server} tools to ${p}`}
                  onClick={(e) => {
                    e.stopPropagation()
                    if (p === 'allow') {
                      onWildcardPolicy(wildcardKey, null)
                    } else {
                      onWildcardPolicy(wildcardKey, p)
                    }
                  }}
                  className={`px-2 py-0.5 rounded text-[10px] font-medium border transition-colors capitalize disabled:opacity-40 disabled:cursor-not-allowed ${BULK_BUTTON_CLASS(isActive, p)}`}
                >
                  {p.charAt(0).toUpperCase() + p.slice(1)}
                </button>
              )
            })}
          </div>
        )}
      </div>

      {/* Expanded content */}
      {open && (
        <div className="px-3 pb-3 border-t border-[var(--color-border)] pt-3" data-testid="advanced-disclosure-content">
          {/* Source badge */}
          <div className="flex items-center gap-1.5 mb-2">
            <Database size={11} className="text-[var(--color-muted)]" />
            <span
              className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-violet-500/20 text-violet-300 border border-violet-500/40"
              data-testid={`mcp-source-badge-${server}`}
            >
              MCP
            </span>
            <span className="text-[10px] text-[var(--color-muted)]">{server}</span>
          </div>
          <div className="space-y-0.5" data-testid={`mcp-server-${server}`}>
            {serverTools.map((tool) => (
              <CategoryToolRow
                key={tool.name}
                tool={tool}
                policies={policies}
                defaultPolicy={defaultPolicy}
                onChange={onToolChange}
                disabled={disabled}
                globalPolicies={globalPolicies}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Main component ─────────────────────────────────────────────────────────────

export function ToolPolicyEditor({ tools, value, onChange, disabled, globalPolicies }: ToolPolicyEditorProps) {
  const { default_policy: defaultPolicy, policies } = value

  // ── Partition tools into two buckets ─────────────────────────────────────────
  // MCP tools → MCP section (grouped by server name).
  // All builtin tools (including system.*) → single de-duplicated category grid.
  // No separate "system" disclosure — system.* tools appear in the category grid
  // under the "System" label (CATEGORY_LABELS['system'] = 'System').
  // This satisfies US-1 / AC5 / FR-103 / Issue #357.

  const { builtinTools, mcpTools } = useMemo(() => {
    const builtin: RegistryTool[] = []
    const mcp: RegistryTool[] = []

    for (const tool of tools) {
      if (tool.source === 'mcp') {
        mcp.push(tool)
      } else {
        builtin.push(tool)
      }
    }

    return { builtinTools: builtin, mcpTools: mcp }
  }, [tools])

  // Group ALL builtin tools (including system.*) by category.
  // system.* tools carry category='system' → CATEGORY_LABELS['system'] = 'System'.
  // General builtins (exec, web_search, etc.) may carry category='core' →
  // CATEGORY_LABELS['core'] = 'General'. No raw key ever surfaces to the user.
  const groupedBuiltin = useMemo(() => groupByCategory(builtinTools), [builtinTools])

  // Group MCP tools by server name.
  const groupedMcp = useMemo(() => groupMcpByServer(mcpTools), [mcpTools])

  // Detect active preset.
  const activePreset = useMemo(() => detectActivePreset(value), [value])

  // ── Handlers ────────────────────────────────────────────────────────────────

  function handlePresetClick(role: RolePreset) {
    onChange(applyRolePreset(role))
  }

  function handleToolPolicy(toolId: string, p: ToolPolicy) {
    const newPolicies = { ...policies }
    if (p === defaultPolicy) {
      // Matches default — remove the override so the round-trip is clean.
      delete newPolicies[toolId]
    } else {
      newPolicies[toolId] = p
    }
    onChange({ default_policy: defaultPolicy, policies: newPolicies })
  }

  function handleDefaultPolicy(p: ToolPolicy) {
    // Rebuild policies: drop any overrides that now match the new default.
    const newPolicies: Record<string, ToolPolicy> = {}
    for (const [toolId, pol] of Object.entries(policies)) {
      if (pol !== p) {
        newPolicies[toolId] = pol
      }
    }
    onChange({ default_policy: p, policies: newPolicies })
  }

  /**
   * Bulk-set a per-server wildcard policy key (e.g. `mcp_github_mcp_*`).
   * Pass null to remove the key (Allow = revert to default).
   */
  function handleWildcardPolicy(wildcardKey: string, p: ToolPolicy | null) {
    const newPolicies = { ...policies }
    if (p === null) {
      delete newPolicies[wildcardKey]
    } else {
      newPolicies[wildcardKey] = p
    }
    onChange({ default_policy: defaultPolicy, policies: newPolicies })
  }

  // ── Render ───────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-4" data-testid="tool-policy-editor">
      {/* 1. Role preset selector */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-[var(--color-muted)]">Role preset</p>
        <div className="flex gap-2 flex-wrap">
          {(Object.entries(POLICY_PRESETS) as [RolePreset, typeof POLICY_PRESETS[RolePreset]][]).map(([role, preset]) => (
            <button
              key={role}
              type="button"
              disabled={disabled}
              onClick={() => handlePresetClick(role)}
              data-testid={`preset-${role}`}
              title={preset.description}
              className={`px-3 py-1.5 rounded-md text-[11px] font-medium border transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                activePreset === role
                  ? 'bg-[var(--color-accent)]/20 text-[var(--color-accent)] border-[var(--color-accent)]/40'
                  : 'border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
              }`}
            >
              {preset.label}
            </button>
          ))}
        </div>
        {/* Preset description */}
        {activePreset && (
          <p className="text-[11px] text-[var(--color-muted)]">{POLICY_PRESETS[activePreset].description}</p>
        )}
      </div>

      {/* 2. Single de-duplicated category grid — ALL builtin tools exactly once.
              system.* tools appear here under "System"; general builtins under
              "General" (core category). No tool appears twice. */}
      {Object.keys(groupedBuiltin).length > 0 && (
        <div className="space-y-2">
          <p className="text-xs font-medium text-[var(--color-muted)]">Tool categories</p>
          <div className="space-y-1.5" data-testid="category-grid">
            {Object.entries(groupedBuiltin).map(([cat, catTools]) => (
              <CategorySection
                key={cat}
                categoryKey={cat}
                tools={catTools}
                policies={policies}
                defaultPolicy={defaultPolicy}
                onToolChange={handleToolPolicy}
                disabled={disabled}
                globalPolicies={globalPolicies}
              />
            ))}
          </div>
        </div>
      )}

      {/* 3. MCP tools grouped per-server */}
      {mcpTools.length > 0 && (
        <div className="space-y-2" data-testid="mcp-tools-section">
          <p className="text-xs font-medium text-[var(--color-muted)]">MCP server tools</p>
          <div className="space-y-1.5">
            {Object.entries(groupedMcp).map(([server, serverTools]) => (
              <McpServerSection
                key={server}
                server={server}
                serverTools={serverTools}
                policies={policies}
                defaultPolicy={defaultPolicy}
                onToolChange={handleToolPolicy}
                onWildcardPolicy={handleWildcardPolicy}
                disabled={disabled}
                globalPolicies={globalPolicies}
              />
            ))}
          </div>
        </div>
      )}

      {/* 4. Customize defaults (advanced) — default policy control only.
              No per-tool re-listing here; each tool already appears exactly once
              in the category grid above (AC5 / FR-103). */}
      {tools.length > 0 && (
        <AdvancedDisclosure
          title="Customize defaults (advanced)"
          summary={`${Object.keys(policies).length} override${Object.keys(policies).length !== 1 ? 's' : ''} active`}
        >
          <div className="space-y-1.5">
            <p className="text-[11px] text-[var(--color-muted)]">Default policy (applies to any tool without an explicit override)</p>
            <div className="flex gap-1.5">
              {ALL_POLICIES.map((p) => (
                <PolicyBadge
                  key={p}
                  policy={p}
                  active={defaultPolicy === p}
                  onClick={() => handleDefaultPolicy(p)}
                  disabled={disabled}
                />
              ))}
            </div>
          </div>
        </AdvancedDisclosure>
      )}
    </div>
  )
}
