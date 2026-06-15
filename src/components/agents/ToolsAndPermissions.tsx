// ToolsAndPermissions — FR-027, FR-029, FR-043, FR-044, FR-086, MAJ-008
//
// Changes from previous version (US-D2, US-D4, US-D5, #333, #331, #332, #337):
// - Replaced 4 ad-hoc POLICY_PRESETS + raw grid with shared <ToolPolicyEditor>
//   (#333 / US-D2). This automatically handles:
//     - Cautious / Balanced / Full access role presets
//     - category==='system' tools in the Advanced/system disclosure (B-1 fix)
//     - MCP tools grouped per-server with a source badge (US-E5 / #337)
//     - Mixed summary pill for heterogeneous categories
// - isLocked prop passed from agentType: locked core agents see the editor as
//   read-only (disabled=true); no write is fired (B-2 fix / US-D5 / #332).
// - autoSave is disabled for locked agents (prevents the spurious 403).
// - Shell/fs conflict banner and fence badge are RETAINED from the previous
//   version; they operate at the raw-tool level and still apply.

import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Lock } from '@phosphor-icons/react'

import { ToolPolicyEditor } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'
import { resolvePolicy } from '@/lib/toolCategories'
import {
  fetchRegistryTools,
  fetchAgentTools,
  updateAgentTools,
  type AgentToolsCfg,
} from '@/lib/api'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { applyRolePreset } from '@/lib/toolPolicyPresets'

interface ToolsAndPermissionsProps {
  agentId: string | null
  agentType: 'core' | 'custom' | 'system' | 'worker'
  /** Whether the agent is locked (core/identity-locked). Read-only when true. */
  isLocked?: boolean
  tools: AgentToolsCfg
  onChange: (tools: AgentToolsCfg) => void
}

// ── Helpers ────────────────────────────────────────────────────────────────────

/** Convert AgentToolsCfg (wire shape) to ToolPolicyValue (editor shape). */
function cfgToValue(tools: AgentToolsCfg): ToolPolicyValue {
  return {
    default_policy: (tools.builtin?.default_policy as ToolPolicy) ?? 'allow',
    policies: (tools.builtin?.policies as Record<string, ToolPolicy>) ?? {},
  }
}

/** Convert ToolPolicyValue (editor shape) back to AgentToolsCfg (wire shape). */
function valueToCfg(value: ToolPolicyValue, existing: AgentToolsCfg): AgentToolsCfg {
  return {
    ...existing,
    builtin: {
      ...existing.builtin,
      default_policy: value.default_policy,
      policies: value.policies,
    },
  }
}

// ── Component ──────────────────────────────────────────────────────────────────

export function ToolsAndPermissions({
  agentId,
  agentType: _agentType,
  isLocked = false,
  tools,
  onChange,
}: ToolsAndPermissionsProps) {
  const queryClient = useQueryClient()

  // B-2 (US-D5 / #332): auto-save is completely disabled for locked agents.
  // The ToolPolicyEditor is also rendered with disabled=true so the controls
  // are non-interactive. No write ever fires.
  const { status: saveStatus, error: saveError } = useAutoSave(
    tools,
    (data) => updateAgentTools(agentId!, data).then((result) => {
      onChange(result.config)
      queryClient.invalidateQueries({ queryKey: ['agent-tools', agentId] })
    }),
    { disabled: !agentId || isLocked },
  )

  // FR-027, FR-029: central registry — includes both builtin and MCP tools.
  const { data: registryTools = [], isLoading: toolsLoading, isError: toolsError } = useQuery({
    queryKey: ['registry-tools'],
    queryFn: fetchRegistryTools,
  })

  const { data: agentToolsData } = useQuery({
    queryKey: ['agent-tools', agentId],
    queryFn: () => fetchAgentTools(agentId!),
    enabled: !!agentId,
  })

  useEffect(() => {
    if (agentToolsData && agentId) {
      const incoming = JSON.stringify(agentToolsData.config)
      const current = JSON.stringify(tools)
      if (incoming !== current) {
        onChange(agentToolsData.config)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentToolsData, agentId])

  // Shell/fs conflict detection (retained from previous version).
  // Uses the default_policy + per-tool overrides from `tools` prop.
  const defaultPolicy = (tools.builtin?.default_policy as ToolPolicy) ?? 'allow'
  const policies = (tools.builtin?.policies as Record<string, ToolPolicy>) ?? {}

  const shellFsConflict = useMemo(() => {
    const shellPolicy = resolvePolicy('workspace.shell', policies, defaultPolicy)
    if (shellPolicy === 'deny') return false
    const fsTools = ['write_file', 'read_file', 'list_dir'] as const
    return fsTools.some((t) => resolvePolicy(t, policies, defaultPolicy) === 'deny')
  }, [policies, defaultPolicy])

  // FR-043 preset confirmation state (now handled inside ToolPolicyEditor but
  // we keep the preset confirmation flow via direct role preset application).
  // The ToolPolicyEditor calls onChange synchronously so we don't need a dialog
  // at this level — it's a role-preset selection, not a replace-semantics dialog.
  // The ToolPolicyEditor handles its own internal state.

  // Local copy for ToolPolicyEditor (controlled).
  const [editorValue, setEditorValue] = useState<ToolPolicyValue>(() => cfgToValue(tools))

  // Keep local editorValue in sync with parent `tools` prop when it changes
  // from outside (e.g. after server load, preset apply in parent, etc.)
  const incomingValue = cfgToValue(tools)
  useEffect(() => {
    const incoming = JSON.stringify(incomingValue)
    const current = JSON.stringify(editorValue)
    if (incoming !== current) {
      setEditorValue(incomingValue)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tools])

  function handleEditorChange(next: ToolPolicyValue) {
    setEditorValue(next)
    onChange(valueToCfg(next, tools))
  }

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

  return (
    <div className="space-y-5">
      {/* B-2 (US-D5 / #332): locked agent read-only notice */}
      {isLocked && (
        <div
          data-testid="locked-agent-readonly-notice"
          className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
        >
          <Lock size={13} className="text-[var(--color-muted)] shrink-0 mt-0.5" />
          <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">
            Tool policies for locked core agents are read-only. To change tool access,
            create a custom agent.
          </p>
        </div>
      )}

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

      {/* Auto-save status — hidden for locked agents (no writes ever fire) */}
      {!isLocked && (
        <div className="flex items-center gap-3">
          <AutoSaveIndicator status={saveStatus} error={saveError} />
          <span className="text-[10px] text-[var(--color-muted)]">
            {Object.keys(policies).length} override{Object.keys(policies).length !== 1 ? 's' : ''} | Default: {defaultPolicy}
          </span>
        </div>
      )}

      {/* #333 (US-D2) / B-1 (#331) / US-E5 (#337):
          ToolPolicyEditor handles:
          - Cautious / Balanced / Full access role preset selector
          - category==='system' tools in the Advanced disclosure (NOT scope-based)
          - MCP tools grouped per-server with a source badge
          - Summary pill per category
          - disabled=true when isLocked (B-2 / #332) */}
      <ToolPolicyEditor
        tools={registryTools}
        value={editorValue}
        onChange={handleEditorChange}
        disabled={isLocked}
      />
    </div>
  )
}

// Re-export applyRolePreset for use in CreateAgentModal default init
export { applyRolePreset }
