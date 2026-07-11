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
// - Rapid edits use useAutoSave (debounce + latest-value ref) so no edit is ever
//   dropped and the editor never snaps back to a stale value mid-flight.

import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Info, Lock } from '@phosphor-icons/react'

import { ToolPolicyEditor } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import type { ToolPolicy } from '@/components/shared/PolicyBadge'
import { resolvePolicy } from '@/lib/toolCategories'
import { isExternalType } from '@/lib/agentKind'
import {
  fetchRegistryTools,
  fetchAgentTools,
  fetchGlobalToolPolicies,
  updateAgentTools,
  type AgentKind,
  type AgentToolsCfg,
} from '@/lib/api'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { useReAuthGate, isReAuthCancelled } from '@/components/settings/useReAuthGate'
import { useUiStore } from '@/store/ui'
import { useAutoSave } from '@/hooks/useAutoSave'

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
    policies: (tools.builtin?.policies as Record<string, ToolPolicy>) ?? {},
  }
}

/** Convert ToolPolicyValue (editor shape) back to AgentToolsCfg (wire shape). */
function valueToCfg(value: ToolPolicyValue, existing: AgentToolsCfg): AgentToolsCfg {
  return {
    ...existing,
    builtin: {
      ...existing.builtin,
      policies: value.policies,
    },
  }
}

// ── Component ──────────────────────────────────────────────────────────────────

export function ToolsAndPermissions({
  agentId,
  agentType,
  isLocked = false,
  tools,
  onChange,
}: ToolsAndPermissionsProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  // Field matrix (docs/internal/architecture/agent-types-field-matrix.md):
  // tools_cfg is "—" for subagent_3p — the external runner has its own
  // tools; per-tool CLI flags govern instead. AgentProfile already hides
  // the whole Tools & Permissions section for external agents; this is the
  // defense-in-depth guard for any other caller of this component.
  const isExternal = isExternalType(agentType)

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
    ? { policies: globalPolicies.policies ?? {} }
    : undefined

  // Local copy for ToolPolicyEditor (controlled).
  const [editorValue, setEditorValue] = useState<ToolPolicyValue>(() => cfgToValue(tools))

  // isDraftReady gates useAutoSave: stays false until the server data has
  // arrived and hydrated editorValue. This prevents a spurious save on open
  // (the same pattern used in GlobalToolPoliciesSection in SecuritySection.tsx).
  const [isDraftReady, setIsDraftReady] = useState(false)

  // toolsRef always holds the latest `tools` prop. The save function reads
  // from it so it always builds the cfg from the most recent prop snapshot
  // without needing tools in the useAutoSave dependency array.
  const toolsRef = useRef(tools)
  toolsRef.current = tools

  // agentIdRef always holds the latest agentId so the save closure captures
  // the current id even if the agent changes between debounce and fire.
  const agentIdRef = useRef(agentId)
  agentIdRef.current = agentId

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
  //
  // Guard: only snap to server data when there are no pending user edits
  // (isDraftReady=false means we haven't diverged from server yet). Once the
  // user has made edits, we never snap back to stale server data.
  useEffect(() => {
    if (!agentToolsData || !agentId) return
    if (isDraftReady) return // user has edited — do NOT snap back
    const incomingValue = cfgToValue(agentToolsData.config)
    setEditorValue(incomingValue)
    setIsDraftReady(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentToolsData, agentId])

  // Keep editorValue in sync with parent `tools` prop when the agent changes
  // (e.g. navigating to a different agent). Reset isDraftReady so the next
  // agentToolsData hydration is accepted. This is also hydration — no save.
  const prevAgentIdRef = useRef(agentId)
  useEffect(() => {
    if (agentId !== prevAgentIdRef.current) {
      // Agent switched — reset so the incoming agentToolsData hydrates fresh.
      prevAgentIdRef.current = agentId
      setIsDraftReady(false)
      setEditorValue(cfgToValue(tools))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId])

  // useAutoSave: debounces user edits and fires the re-auth-gated PUT with the
  // LATEST editorValue (held in useAutoSave's internal latestDataRef). This
  // guarantees:
  //   - No edit is dropped (latest-wins: the ref is always up-to-date).
  //   - No stale snap-back (isDraftReady gate prevents hydration effects from
  //     overwriting the editor after the first user edit).
  //   - The save is coalesced (debounce collapses rapid edits into one PUT).
  //   - The unmount flush fires the final edit even if the user navigates away
  //     before the debounce settles.
  //
  // Mirrors GlobalToolPoliciesSection in SecuritySection.tsx exactly.
  const { status: saveStatus, error: saveError } = useAutoSave(
    editorValue,
    async (value) => {
      const id = agentIdRef.current
      if (!id) return
      const cfg = valueToCfg(value, toolsRef.current)
      const result = await runGated((token) => updateAgentTools(id, cfg, token))
      // Propagate to parent and invalidate the cache after a successful save.
      onChange(result.config)
      queryClient.invalidateQueries({ queryKey: ['agent-tools', id] })
    },
    { disabled: isLocked || isExternal || !isDraftReady },
  )

  // Surface runGated cancellation and API errors as toasts. useAutoSave catches
  // errors from the saveFn and sets status='error' + error string; we also want
  // a toast for discoverability. We use a ref to track the previous error so we
  // only toast on transitions (new error), not on every render.
  const prevSaveErrorRef = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (saveError && saveError !== prevSaveErrorRef.current) {
      if (!isReAuthCancelled(new Error(saveError))) {
        addToast({ message: `Tool policy save failed: ${saveError}`, variant: 'error' })
      }
    }
    prevSaveErrorRef.current = saveError
  }, [saveError, addToast])

  // handleEditorChange is the ONLY path for real user edits. It updates the
  // local controlled state immediately (responsive UI) and lets useAutoSave
  // observe the change and fire the debounced gated PUT with the latest value.
  // It never skips or drops an edit regardless of in-flight saves.
  function handleEditorChange(next: ToolPolicyValue) {
    if (isLocked || isExternal || !agentId) return
    setEditorValue(next)
  }

  // Shell/fs conflict detection (retained from previous version).
  // Uses the per-tool policy map from `tools` prop. A tool with no explicit
  // entry resolves to `undefined` (unconfigured) — never treated as 'deny'.
  const policies = (tools.builtin?.policies as Record<string, ToolPolicy>) ?? {}

  const shellFsConflict = useMemo(() => {
    // ADR-036: `bash` is the unified shell tool (replaces exec / workspace_shell
    // / workspace_shell_bg) — it's the one whose policy determines whether the
    // filesystem-bypass conflict below applies.
    //
    // Deliberate: resolvePolicy() can return `undefined` for a genuinely
    // unconfigured tool (no exact/glob entry) — this check only ever compares
    // against the literal 'deny', so an unconfigured bash or fs-tool policy
    // reads as "no known conflict yet", not as an implicit allow/deny. That's
    // intentional for this soft-advisory banner (it should only fire on a
    // confirmed, saved deny), not a fallback to "fix" — the authoritative
    // "needs configuration" surfacing for an unconfigured tool is the
    // ToolPolicyEditor's own "Unset" badge/pill, not this banner.
    const shellPolicy = resolvePolicy('bash', policies)
    if (shellPolicy === 'deny') return false
    const fsTools = ['write_file', 'read_file', 'list_directory'] as const
    return fsTools.some((t) => resolvePolicy(t, policies) === 'deny')
  }, [policies])

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

      {/* Field matrix: subagent_3p read-only notice (defense-in-depth — the
          parent AgentProfile already hides this whole section for external
          agents; this covers any other caller). */}
      {isExternal && !isLocked && (
        <div
          data-testid="external-cli-tools-notice"
          className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
        >
          <Info size={13} className="text-[var(--color-muted)] shrink-0 mt-0.5" />
          <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">
            Tool policies do not apply to agents running on an external CLI runner —
            the runner manages its own tool access. Configure per-tool flags on the
            runner instead.
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
            <code className="font-mono text-[var(--color-secondary)]">bash</code>{' '}
            can perform filesystem operations directly. Denying{' '}
            <code className="font-mono text-[var(--color-secondary)]">write_file</code>/
            <code className="font-mono text-[var(--color-secondary)]">read_file</code>/
            <code className="font-mono text-[var(--color-secondary)]">list_directory</code>{' '}
            won&apos;t stop the shell — to block filesystem access, deny{' '}
            <code className="font-mono text-[var(--color-secondary)]">bash</code>{' '}
            instead.
          </p>
        </div>
      )}

      {/* Save status — hidden for locked/external agents (no writes ever fire) */}
      {!isLocked && !isExternal && (
        <div className="flex items-center gap-3">
          <AutoSaveIndicator status={saveStatus} error={saveError} />
          <span className="text-[10px] text-[var(--color-muted)]">
            {Object.keys(policies).length} tool polic{Object.keys(policies).length !== 1 ? 'ies' : 'y'} configured
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
        disabled={isLocked || isExternal}
        globalPolicies={globalPolicyValue}
      />

      {/* Re-auth consent dialog — rendered once here; opened by runGated when
          the server demands re-auth on the tools PUT. */}
      {reAuthDialog}
    </div>
  )
}
