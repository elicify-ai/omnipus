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
// - Tool-policy saves are re-auth gated: when the user actually changes a policy,
//   the save obtains a consent token via useReAuthGate/runGated and replays it in
//   the X-Reauth-Token header (ADR-022 / Spec-6 FR-12.2).
// - Opening the Tools tab fires ZERO writes: server-hydrated config is reconciled
//   into the local editorValue without calling the parent onChange (hydration ≠
//   user edit) so the parent autoSave is never triggered on tab open.
// - Shell/fs conflict banner and fence badge are RETAINED from the previous
//   version; they operate at the raw-tool level and still apply.

import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient, useMutation } from '@tanstack/react-query'
import { Info, Lock } from '@phosphor-icons/react'

import { ToolPolicyEditor } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'
import { resolvePolicy } from '@/lib/toolCategories'
import {
  fetchRegistryTools,
  fetchAgentTools,
  fetchGlobalToolPolicies,
  updateAgentTools,
  type AgentKind,
  type AgentToolsCfg,
} from '@/lib/api'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { applyRolePreset } from '@/lib/toolPolicyPresets'
import { useReAuthGate, isReAuthCancelled } from '@/components/settings/useReAuthGate'
import { useUiStore } from '@/store/ui'
import { isApiError } from '@/lib/api-error'

interface ToolsAndPermissionsProps {
  agentId: string | null
  agentType: AgentKind
  /** Whether the agent is locked (core/identity-locked). Read-only when true. */
  isLocked?: boolean
  tools: AgentToolsCfg
  /**
   * Called when the server-hydrated config has been loaded (to keep parent
   * toolsCfg in sync for display — e.g. the overrides count badge). This is
   * NOT used as a dirty-change signal: it fires only on server load, not on
   * every user edit. Real saves go through the re-auth-gated mutation below.
   */
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
  const addToast = useUiStore((s) => s.addToast)

  // Re-auth gate — mirrors GlobalToolPoliciesSection (SecuritySection.tsx).
  // The gate opens a consent dialog if the server returns a re-auth 403.
  // On confirmation, the minted token is replayed into the mutation retry.
  const { runGated, dialog: reAuthDialog } = useReAuthGate({
    title: 'Confirm to change tool access',
    description: "Re-type your password to change this agent's tool policies.",
  })

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

  // Global (Settings → Security) tool policy — locks per-agent controls that
  // would contradict a global deny/ask (most-restrictive-wins; no contradictions).
  const { data: globalPolicies } = useQuery({
    queryKey: ['global-tool-policies'],
    queryFn: fetchGlobalToolPolicies,
  })
  const globalPolicyValue: ToolPolicyValue | undefined = globalPolicies
    ? { default_policy: globalPolicies.default_policy, policies: globalPolicies.policies ?? {} }
    : undefined

  // Save status for the AutoSaveIndicator.
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [saveError, setSaveError] = useState<string | undefined>(undefined)

  // Re-auth-gated save mutation. Fires only when the user explicitly changes a
  // tool policy (via handleEditorChange → saveMutation.mutate). Never fires on
  // tab open or server hydration.
  const saveMutation = useMutation({
    mutationFn: async (cfg: AgentToolsCfg) => {
      setSaveStatus('saving')
      setSaveError(undefined)
      return await runGated((token) => updateAgentTools(agentId!, cfg, token))
    },
    onSuccess: (result) => {
      setSaveStatus('saved')
      onChange(result.config)
      queryClient.invalidateQueries({ queryKey: ['agent-tools', agentId] })
      // Reset to idle after a short display window.
      setTimeout(() => setSaveStatus((s) => (s === 'saved' ? 'idle' : s)), 2000)
    },
    onError: (err: unknown) => {
      if (isReAuthCancelled(err)) {
        // User dismissed the re-auth dialog — treat as a no-op, not an error.
        setSaveStatus('idle')
        return
      }
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed'
      setSaveError(msg)
      setSaveStatus('error')
      addToast({ message: `Tool policy save failed: ${msg}`, variant: 'error' })
    },
  })

  // Track whether the component has completed its first server-hydration pass.
  // Guards the editorValue sync so we don't treat the initial server GET as a
  // user edit.
  const hydrated = useRef(false)

  // Local copy for ToolPolicyEditor (controlled).
  const [editorValue, setEditorValue] = useState<ToolPolicyValue>(() => cfgToValue(tools))

  // Hydrate editorValue from the dedicated GET /agents/{id}/tools response.
  // This fires once when agentToolsData arrives (and again if the agent id
  // changes). It does NOT trigger a save and does NOT call the parent
  // onChange — server data arriving is hydration, not a user edit.
  //
  // The parent (AgentProfile) already hydrates toolsCfg from agent.tools_cfg
  // in its own useEffect; the overrides-count badge is accurate without a
  // second onChange call here. Calling onChange would also risk triggering
  // any parent logic that treats it as a dirty-change signal.
  //
  // We compare ToolPolicyValue shapes (not raw AgentToolsCfg shapes) so the
  // comparison is apples-to-apples and key ordering never causes a false diff.
  useEffect(() => {
    if (!agentToolsData || !agentId) return
    const incomingValue = cfgToValue(agentToolsData.config)
    const incoming = JSON.stringify(incomingValue)
    const current = JSON.stringify(editorValue)
    if (incoming !== current) {
      setEditorValue(incomingValue)
    }
    hydrated.current = true
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentToolsData, agentId])

  // Keep editorValue in sync with parent `tools` prop when it changes from
  // outside (e.g. agent navigation, role preset applied from a future parent
  // control). This is also hydration — no save is triggered.
  useEffect(() => {
    const incomingValue = cfgToValue(tools)
    const incoming = JSON.stringify(incomingValue)
    const current = JSON.stringify(editorValue)
    if (incoming !== current && !saveMutation.isPending) {
      // Only resync when we're not in the middle of saving (to avoid the
      // server response racing with a pending user edit).
      setEditorValue(incomingValue)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tools])

  // handleEditorChange is the ONLY path for real user edits. It updates the
  // local controlled state and fires the re-auth-gated save mutation.
  // It does NOT call the parent onChange synchronously — the save's onSuccess
  // handler calls it after the PUT succeeds.
  function handleEditorChange(next: ToolPolicyValue) {
    if (isLocked || !agentId) return
    const nextCfg = valueToCfg(next, tools)
    setEditorValue(next)
    saveMutation.mutate(nextCfg)
  }

  // Shell/fs conflict detection (retained from previous version).
  // Uses the default_policy + per-tool overrides from `tools` prop.
  const defaultPolicy = (tools.builtin?.default_policy as ToolPolicy) ?? 'allow'
  const policies = (tools.builtin?.policies as Record<string, ToolPolicy>) ?? {}

  const shellFsConflict = useMemo(() => {
    const shellPolicy = resolvePolicy('workspace_shell', policies, defaultPolicy)
    if (shellPolicy === 'deny') return false
    const fsTools = ['write_file', 'read_file', 'list_directory'] as const
    return fsTools.some((t) => resolvePolicy(t, policies, defaultPolicy) === 'deny')
  }, [policies, defaultPolicy])

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
            <code className="font-mono text-[var(--color-secondary)]">workspace_shell</code>{' '}
            can perform filesystem operations directly. Denying{' '}
            <code className="font-mono text-[var(--color-secondary)]">write_file</code>/
            <code className="font-mono text-[var(--color-secondary)]">read_file</code>/
            <code className="font-mono text-[var(--color-secondary)]">list_directory</code>{' '}
            won&apos;t stop the shell — to block filesystem access, deny{' '}
            <code className="font-mono text-[var(--color-secondary)]">workspace_shell</code>{' '}
            instead.
          </p>
        </div>
      )}

      {/* Save status — hidden for locked agents (no writes ever fire) */}
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
        globalPolicies={globalPolicyValue}
      />

      {/* Re-auth consent dialog — rendered once here; opened by runGated when
          the server demands re-auth on the tools PUT. */}
      {reAuthDialog}
    </div>
  )
}

// Re-export applyRolePreset for use in CreateAgentModal default init
export { applyRolePreset }
