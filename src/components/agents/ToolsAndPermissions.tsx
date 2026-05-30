// ToolsAndPermissions — FR-027, FR-029, FR-043, FR-044, FR-086, MAJ-008
//
// Changes from previous version:
// - Switched from /tools/builtin to /tools (central registry, includes MCP tools)
// - Registry tools have source: 'builtin'|'mcp' — MCP tools show a badge
// - agentToolsData.tools (AgentToolEntry[]) exposes fence_applied / effective_policy
//   for per-agent fence badges (MAJ-008)
// - Preset application shows a confirmation dialog (FR-043, FR-044): presets are
//   replace semantics (not merge); dialog warns about admin-required tool fencing

import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Info } from '@phosphor-icons/react'

import { MCPServerPicker } from './MCPServerPicker'
import { PolicyBadge, type ToolPolicy } from '@/components/shared/PolicyBadge'
import { CATEGORY_LABELS, groupByCategory, resolvePolicy } from '@/lib/toolCategories'
import {
  fetchRegistryTools,
  fetchAgentTools,
  fetchMcpServersForAgent,
  updateAgentTools,
  fetchGlobalToolPolicies,
  type AgentToolsCfg,
  type AgentToolEntry,
} from '@/lib/api'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

interface ToolsAndPermissionsProps {
  agentId: string | null
  agentType: 'core' | 'custom' | 'system'
  tools: AgentToolsCfg
  onChange: (tools: AgentToolsCfg) => void
}

// Presets for quick policy configuration
const POLICY_PRESETS: Record<string, { label: string; description: string; defaultPolicy: ToolPolicy; overrides?: Record<string, ToolPolicy> }> = {
  unrestricted: {
    label: 'Unrestricted',
    description: 'All tools allowed without confirmation',
    defaultPolicy: 'allow',
  },
  cautious: {
    label: 'Cautious',
    description: 'All tools require approval before use',
    defaultPolicy: 'ask',
  },
  standard: {
    label: 'Standard',
    description: 'Safe tools allowed, exec and browser require approval',
    defaultPolicy: 'allow',
    overrides: {
      exec: 'ask',
      'browser.navigate': 'ask',
      'browser.click': 'ask',
      'browser.type': 'ask',
      'browser.evaluate': 'deny',
    },
  },
  minimal: {
    label: 'Minimal',
    description: 'Only reading and search allowed, everything else denied',
    defaultPolicy: 'deny',
    overrides: {
      read_file: 'allow',
      list_dir: 'allow',
      web_search: 'allow',
      web_fetch: 'allow',
      task_list: 'allow',
      agent_list: 'allow',
    },
  },
}

export function ToolsAndPermissions({ agentId, agentType: _agentType, tools, onChange }: ToolsAndPermissionsProps) {
  const queryClient = useQueryClient()

  // FR-043, FR-044: preset confirmation state
  const [pendingPresetKey, setPendingPresetKey] = useState<string | null>(null)

  const { status: saveStatus, error: saveError } = useAutoSave(
    tools,
    (data) => updateAgentTools(agentId!, data).then((result) => {
      onChange(result.config)
      queryClient.invalidateQueries({ queryKey: ['agent-tools', agentId] })
    }),
    { disabled: !agentId },
  )

  // FR-027, FR-029: central registry — includes both builtin and MCP tools
  const { data: registryTools = [], isLoading: toolsLoading, isError: toolsError } = useQuery({
    queryKey: ['registry-tools'],
    queryFn: fetchRegistryTools,
  })

  const { data: globalPolicies, isError: globalPoliciesError } = useQuery({
    queryKey: ['global-tool-policies'],
    queryFn: fetchGlobalToolPolicies,
  })

  const { data: mcpServers = [], isError: mcpError } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: fetchMcpServersForAgent,
  })

  const { data: agentToolsData } = useQuery({
    queryKey: ['agent-tools', agentId],
    queryFn: () => fetchAgentTools(agentId!),
    enabled: !!agentId,
  })

  useEffect(() => {
    if (agentToolsData && agentId) {
      // Only propagate when the incoming config actually differs from what is
      // already in state — prevents a spurious auto-save on the initial load.
      const incoming = JSON.stringify(agentToolsData.config)
      const current = JSON.stringify(tools)
      if (incoming !== current) {
        onChange(agentToolsData.config)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentToolsData, agentId])

  // FR-086, MAJ-008: build an index from the per-agent tool list for quick lookup
  const agentToolIndex = useMemo<Map<string, AgentToolEntry>>(() => {
    const map = new Map<string, AgentToolEntry>()
    for (const entry of agentToolsData?.tools ?? []) {
      map.set(entry.name, entry)
    }
    return map
  }, [agentToolsData])

  const defaultPolicy: ToolPolicy = (tools.builtin?.default_policy as ToolPolicy) || 'allow'
  const policies: Record<string, ToolPolicy> = (tools.builtin?.policies as Record<string, ToolPolicy>) || {}

  function setDefaultPolicy(p: ToolPolicy) {
    onChange({
      ...tools,
      builtin: { ...tools.builtin, default_policy: p, policies },
    })
  }

  function setToolPolicy(toolName: string, p: ToolPolicy) {
    const newPolicies = { ...policies }
    if (p === defaultPolicy) {
      delete newPolicies[toolName] // matches default, no override needed
    } else {
      newPolicies[toolName] = p
    }
    onChange({
      ...tools,
      builtin: { ...tools.builtin, default_policy: defaultPolicy, policies: newPolicies },
    })
  }

  // FR-043: request preset — show confirmation dialog before applying
  function requestPreset(key: string) {
    setPendingPresetKey(key)
  }

  // FR-044: confirm preset — replace semantics (not merge)
  function confirmPreset() {
    if (!pendingPresetKey) return
    const preset = POLICY_PRESETS[pendingPresetKey]
    if (preset) {
      onChange({
        ...tools,
        builtin: {
          default_policy: preset.defaultPolicy,
          policies: preset.overrides ? { ...preset.overrides } : {},
        },
      })
    }
    setPendingPresetKey(null)
  }

  function cancelPreset() {
    setPendingPresetKey(null)
  }

  // Detect which preset matches current config
  const activePreset = useMemo(() => {
    for (const [key, preset] of Object.entries(POLICY_PRESETS)) {
      if (preset.defaultPolicy !== defaultPolicy) continue
      const overrideCount = Object.keys(preset.overrides || {}).length
      const currentOverrideCount = Object.keys(policies).length
      if (overrideCount === 0 && currentOverrideCount === 0) return key
      if (overrideCount === currentOverrideCount) {
        const matches = Object.entries(preset.overrides || {}).every(([k, v]) => policies[k] === v)
        if (matches) return key
      }
    }
    return 'custom'
  }, [defaultPolicy, policies])

  // Detect shell/filesystem policy conflict: workspace.shell allowed but filesystem ops denied
  const shellFsConflict = useMemo(() => {
    const shellPolicy = resolvePolicy('workspace.shell', policies, defaultPolicy)
    if (shellPolicy === 'deny') return false
    const fsTools = ['write_file', 'read_file', 'list_dir'] as const
    return fsTools.some((t) => resolvePolicy(t, policies, defaultPolicy) === 'deny')
  }, [policies, defaultPolicy])

  // Filter out system-scope tools — never shown in agent tool configuration
  const displayTools = registryTools.filter((t) => {
    if (t.scope === 'system') return false
    return true
  })

  const grouped = groupByCategory(displayTools)

  if (toolsLoading) {
    return (
      <div className="space-y-2 py-4">
        {[1, 2, 3].map((i) => (
          <div key={i} className="h-9 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
    )
  }

  if (toolsError) {
    return (
      <p className="text-xs text-[var(--color-error)] py-4">
        Failed to load tool list. Check that the backend is running.
      </p>
    )
  }

  const pendingPreset = pendingPresetKey ? POLICY_PRESETS[pendingPresetKey] : null

  return (
    <>
      <div className="space-y-5">
        {/* Shell/filesystem conflict banner */}
        {shellFsConflict && (
          <div
            role="status"
            data-testid="shell-fs-conflict-banner"
            className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
          >
            <Info size={13} className="text-[var(--color-secondary)] shrink-0 mt-0.5" />
            <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">
              <code className="font-mono text-[var(--color-secondary)]">workspace.shell</code>{' '}
              can perform filesystem operations directly. Denying{' '}
              <code className="font-mono text-[var(--color-secondary)]">write_file</code>/
              <code className="font-mono text-[var(--color-secondary)]">read_file</code>/
              <code className="font-mono text-[var(--color-secondary)]">list_dir</code>{' '}
              won&apos;t stop the shell — to block filesystem access, deny{' '}
              <code className="font-mono text-[var(--color-secondary)]">workspace.shell</code>{' '}
              instead.
            </p>
          </div>
        )}

        {/* Auto-save status — top of section for visibility */}
        <div className="flex items-center gap-3">
          <AutoSaveIndicator status={saveStatus} error={saveError} />
          <span className="text-[10px] text-[var(--color-muted)]">
            {Object.keys(policies).length} override{Object.keys(policies).length !== 1 ? 's' : ''} | Default: {defaultPolicy}
          </span>
        </div>

        {/* Preset row — FR-043: clicking opens confirmation dialog */}
        <div className="space-y-2">
          <p className="text-xs text-[var(--color-muted)]">Policy presets</p>
          <div className="flex gap-2 flex-wrap">
            {Object.entries(POLICY_PRESETS).map(([key, preset]) => (
              <button
                key={key}
                type="button"
                onClick={() => requestPreset(key)}
                className={`px-3 py-1.5 rounded-md text-[11px] font-medium border transition-colors ${
                  activePreset === key
                    ? 'bg-[var(--color-accent)]/20 text-[var(--color-accent)] border-[var(--color-accent)]/40'
                    : 'border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
                }`}
                title={preset.description}
              >
                {preset.label}
              </button>
            ))}
          </div>
        </div>

        {/* Default policy */}
        <div className="space-y-1.5">
          <p className="text-xs text-[var(--color-muted)]">Default policy for unlisted tools</p>
          <div className="flex gap-1.5">
            {(['allow', 'ask', 'deny'] as ToolPolicy[]).map((p) => (
              <PolicyBadge key={p} policy={p} active={defaultPolicy === p} onClick={() => setDefaultPolicy(p)} />
            ))}
          </div>
        </div>

        {/* Per-tool policies grouped by category */}
        <div className="space-y-3">
          <p className="text-xs text-[var(--color-muted)]">
            Per-tool policies ({displayTools.length} tools)
            {mcpError && (
              <span className="text-[var(--color-warning)] ml-2">(MCP servers unavailable)</span>
            )}
          </p>
          {globalPolicies && (
            <p className="text-[10px] text-[var(--color-muted)]">
              Tools blocked by global security policies are shown greyed out
            </p>
          )}
          {globalPoliciesError && (
            <p className="text-[10px] text-[var(--color-warning)]">
              Could not load global security policies — restrictions may not be shown
            </p>
          )}
          {Object.entries(grouped).map(([category, catTools]) => (
            <div key={category} className="space-y-1">
              <p className="text-[10px] font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
                {CATEGORY_LABELS[category] || category}
              </p>
              <div className="space-y-0.5">
                {catTools.map((tool) => {
                  const currentPolicy = resolvePolicy(tool.name, policies, defaultPolicy)
                  const isOverridden = tool.name in policies

                  // Determine effective global policy for this tool.
                  // When the fetch failed, default to 'deny' (deny-by-default security posture).
                  const globalDefaultPolicy = globalPoliciesError ? 'deny' : (globalPolicies?.default_policy ?? 'allow')
                  const globalToolPolicy = globalPoliciesError
                    ? 'deny'
                    : (globalPolicies?.policies?.[tool.name] ?? globalDefaultPolicy)
                  const isGloballyDenied = globalToolPolicy === 'deny'
                  const isGloballyAsked = globalToolPolicy === 'ask'

                  // FR-086, MAJ-008: per-agent fence info
                  const agentEntry = agentToolIndex.get(tool.name)
                  const fenceApplied = agentEntry?.fence_applied === true
                  const effectivePolicy = agentEntry?.effective_policy
                  const configuredPolicy = agentEntry?.configured_policy

                  return (
                    <div
                      key={tool.name}
                      className={`flex items-center justify-between py-1 px-2 rounded transition-colors ${
                        isGloballyDenied
                          ? 'opacity-50 cursor-not-allowed'
                          : 'hover:bg-[var(--color-surface-2)]'
                      }`}
                    >
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-1.5 flex-wrap">
                          <span className={`text-xs font-mono ${isOverridden && !isGloballyDenied ? 'text-[var(--color-secondary)]' : 'text-[var(--color-muted)]'}`}>
                            {tool.name}
                          </span>
                          {/* FR-027, FR-029: source badge for MCP tools */}
                          {tool.source === 'mcp' && (
                            <span className="text-[9px] font-semibold uppercase tracking-wide px-1 py-0.5 rounded bg-[var(--color-accent)]/10 text-[var(--color-accent)] border border-[var(--color-accent)]/20">
                              MCP
                            </span>
                          )}
                        </div>
                        {isGloballyDenied ? (
                          <span className="text-[10px] text-red-400 ml-0">Blocked by security policy</span>
                        ) : isGloballyAsked ? (
                          <span className="text-[10px] text-amber-400" title="Global policy: Ask — agent cannot override to Allow">
                            Global: Ask
                          </span>
                        ) : fenceApplied && configuredPolicy && effectivePolicy ? (
                          // FR-086, MAJ-008: fence badge — show both configured and effective policy
                          <div className="flex items-center gap-1 mt-0.5">
                            <Info size={10} className="text-amber-400 shrink-0" />
                            <span
                              className="text-[10px] text-amber-400"
                              title="downgraded to ask: admin-required tool on custom agent"
                            >
                              downgraded to ask: admin-required tool on custom agents
                            </span>
                            <span className="text-[9px] text-[var(--color-muted)] ml-1">
                              Configured: {configuredPolicy} &rarr; Effective: {effectivePolicy}
                            </span>
                          </div>
                        ) : (
                          <span className="text-[10px] text-[var(--color-muted)] hidden sm:inline">
                            {tool.description?.slice(0, 50)}{(tool.description?.length ?? 0) > 50 ? '...' : ''}
                          </span>
                        )}
                      </div>
                      <div className="flex gap-0.5 shrink-0">
                        {(['allow', 'ask', 'deny'] as ToolPolicy[]).map((p) => (
                          <PolicyBadge
                            key={p}
                            policy={p}
                            active={currentPolicy === p}
                            onClick={() => setToolPolicy(tool.name, p)}
                            // When globally denied: all buttons disabled
                            // When globally asked: Allow button disabled (can't upgrade past Ask)
                            disabled={isGloballyDenied || (isGloballyAsked && p === 'allow')}
                          />
                        ))}
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
          ))}
        </div>

        {/* MCP section */}
        <div className="space-y-2">
          <p className="text-xs font-medium text-[var(--color-secondary)]">MCP Servers</p>
          <MCPServerPicker
            servers={mcpServers}
            mcpConfig={tools.mcp}
            onChange={(mcp) => onChange({ ...tools, mcp })}
          />
        </div>

      </div>

      {/* FR-043, FR-044: Preset confirmation dialog — replace semantics warning */}
      <Dialog open={pendingPresetKey !== null} onOpenChange={(open) => { if (!open) cancelPreset() }}>
        <DialogContent className="sm:max-w-md bg-[var(--color-surface-1)] border-[var(--color-border)]">
          <DialogHeader>
            <DialogTitle className="text-[var(--color-secondary)]">
              Apply preset: {pendingPreset?.label}
            </DialogTitle>
            <DialogDescription className="text-[var(--color-muted)] text-sm">
              {pendingPreset?.description}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <p className="text-sm text-[var(--color-secondary)]">
              This will <strong className="text-[var(--color-accent)]">replace</strong> all current per-tool policies.
              Your existing overrides will be lost.
            </p>
            <div className="flex items-start gap-2 rounded-md bg-amber-500/10 border border-amber-500/20 px-3 py-2">
              <Info size={14} className="text-amber-400 mt-0.5 shrink-0" />
              <p className="text-[11px] text-amber-300">
                Admin-required tools on custom agents will use <strong>ask</strong> regardless of the preset policy.
              </p>
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button
              size="sm"
              variant="ghost"
              onClick={cancelPreset}
              className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
            >
              Cancel
            </Button>
            <Button
              size="sm"
              variant="default"
              onClick={confirmPreset}
              className="bg-[var(--color-accent)] text-black hover:bg-[var(--color-accent)]/90"
            >
              Apply preset
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
