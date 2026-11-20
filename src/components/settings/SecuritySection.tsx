/**
 * SecuritySection — Settings → Security tab.
 *
 * Two-layer IA (US-B1 / #327):
 *   Primary layer  — Security health (DiagnosticsSection score + 3 plain toggles)
 *   Advanced layer — All jargon (sandbox internals, SSRF, deny-regex, tool grid,
 *                    audit log) under ONE AdvancedDisclosure.
 *
 * Global Tool Access via ToolPolicyEditor (US-B3 / #329):
 *   - Replaces GlobalToolPoliciesSection entirely.
 *   - Deletes local CATEGORY_LABELS / PolicyBadge / groupByCategory duplicates.
 *   - Imports shared canonicals from @/lib/toolCategories and @/components/shared.
 *
 * Score-as-control-surface + plain restart banner + vault reassurance (US-B4 / #330):
 *   - action_link / action_label links in DiagnosticsSection IssueCard.
 *   - RestartBanner plain summary with jargon behind "Technical details".
 *   - Credential Vault reassurance line.
 *
 * SkillTrustSection mounted (US-E4 / #340 part):
 *   - Block / Warn-unverified / Allow-all radio already built; imported here.
 */

import { useState, useEffect, useMemo } from 'react'
import { cn } from '@/lib/utils'
import { AuditLogViewer } from './AuditLogViewer'
import { PromptGuardSection, type PromptGuardDraft } from './PromptGuardSection'
import { ExecProxyStatusCard } from './ExecProxyStatusCard'
import { SkillTrustSection } from './SkillTrustSection'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash, Key, Lock } from '@phosphor-icons/react'
import { Link } from '@tanstack/react-router'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Callout, CalloutActions, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useAutoSave } from '@/hooks/useAutoSave'
import { PasswordBadge, SaveBarPanel, SettingRow } from './SaveBar'
import { toWriteResponse } from './saveOutcome'
import { ReadErrorCallout } from './ReadErrorCallout'
import { fieldDomId, useSaveBar, type FieldMessage, type SaveBarField, type SaveBarValue } from './useSaveBar'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Switch } from '@/components/ui/switch'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Separator } from '@/components/ui/separator'
import {
  fetchConfig,
  fetchRateLimits,
  updateRateLimits,
  fetchCredentials,
  addCredential,
  deleteCredential,
  rotateCredentials,
  fetchBuiltinTools,
  fetchGlobalToolPolicies,
  updateGlobalToolPolicies,
  isPersistedPolicyActivationError,
  fetchSandboxConfig,
  updateSandboxConfig,
  fetchSandboxStatus,
  fetchPromptGuardLevel,
  updatePromptGuardLevel,
  getErrorMessage,
} from '@/lib/api'
import type { PromptInjectionLevel } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { DiagnosticsSection } from './DiagnosticsSection'
import { SandboxSection } from './SandboxSection'
import { Label } from '@/components/ui/label'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { ToolPolicyEditor, type ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import { isReAuthCancelled } from './useReAuthGate'
import { useStepUp } from './useStepUp'
import { AutoApproveAskList } from './AutoApproveAskList'

// ── Tool Access — Global Policies (US-B3) ──────────────────────────────────────
// CATEGORY_LABELS, PolicyBadge, and groupByCategory are now imported from the
// shared canonicals. Local duplicates removed per #329.

function GlobalToolPoliciesSection() {
  const queryClient = useQueryClient()

  const { data: builtinTools = [], isLoading: toolsLoading, isError: toolsError } = useQuery({
    queryKey: ['tools-builtin'],
    queryFn: fetchBuiltinTools,
  })

  const { data: globalPolicies, isLoading: policiesLoading, isError: policiesError } = useQuery({
    queryKey: ['global-tool-policies'],
    queryFn: fetchGlobalToolPolicies,
  })

  const [toolPolicyValue, setToolPolicyValue] = useState<ToolPolicyValue>({
    policies: {},
  })
  const [isDraftReady, setIsDraftReady] = useState(false)

  // PUT /api/v1/security/tool-policies has no server-side step-up and gets no
  // confirmation either (FR-OB-046): rest_tool_policies.go:69 removed its gate
  // deliberately, so the client asked for a password the server never demanded.
  // It is not one of ADR-0008 ruling 6's six controls. The auto-save fires the
  // PUT directly.

  useEffect(() => {
    if (!globalPolicies || isDraftReady) return
    setToolPolicyValue({
      policies: globalPolicies.policies ?? {},
    })
    setIsDraftReady(true)
  }, [globalPolicies, isDraftReady])

  const { status: saveStatus, error: saveError } = useAutoSave(
    toolPolicyValue,
    async (cfg) => {
      await updateGlobalToolPolicies(cfg)
      queryClient.invalidateQueries({ queryKey: ['global-tool-policies'] })
    },
    { disabled: !isDraftReady, isPersistedError: isPersistedPolicyActivationError },
  )

  const isLoading = toolsLoading || policiesLoading

  if (isLoading) {
    return (
      <div className="space-y-[var(--space-2)] py-[var(--space-3)]">
        {[1, 2, 3].map((i) => (
          <div key={i} className="h-8 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
        ))}
      </div>
    )
  }

  if (toolsError || policiesError) {
    return (
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-text-error)] py-[var(--space-3)]">
        Failed to load tool policies. Check that the backend is running.
      </p>
    )
  }

  return (
    <div className="space-y-[var(--space-3)]">
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        These policies apply globally across all agents. Per-agent policies cannot override a global
        "Deny". Tools blocked here are greyed out in each agent's tool list.
      </p>
      <ToolPolicyEditor
        tools={builtinTools}
        value={toolPolicyValue}
        onChange={setToolPolicyValue}
        disabled={!isDraftReady}
      />
      <div className="pt-[var(--space-2)] flex items-center gap-[var(--space-2-5)]">
        <AutoSaveIndicator status={saveStatus} error={saveError} />
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {Object.keys(toolPolicyValue.policies).length} tool polic{Object.keys(toolPolicyValue.policies).length !== 1 ? 'ies' : 'y'} configured
        </span>
      </div>
    </div>
  )
}

// ── Auto-approve (ADR-092) — global default ───────────────────────────────────
//
// Auto-approve is a SEPARATE setting from tool policy (allow/deny/ask) — it
// only has meaning for a tool currently resolved to "ask". It covers every
// such tool, not just shell commands (ADR-092 addendum §2/§3) — "runs" is
// what the classifier proves never leaves the workspace/sandbox for that
// specific call; a short, fixed list always asks regardless (§7).
//
// Founder decision (2026-09-24): Auto-approve no longer requires an
// enforcing kernel sandbox — it works for every tool, shell included, on
// every platform (Windows too), whether or not the sandbox is enforcing.
// `kernel_sandbox_active` (read from the same ['sandbox-status'] query the
// chat-header badge uses) is WARNING-ONLY here: with no enforcing sandbox,
// shell commands now ask first unless they are read-only or covered by an
// operator allow rule (see pkg/tools/shell_no_sandbox_gate.go), and the
// card's platform caveat line is shown as a caution instead of muted.
//
// Lives on the same SandboxConfig the Process Sandbox (Advanced) section
// already manages, and goes through the same re-auth-gated
// PUT /security/sandbox-config handler — no separate auth path to build.
//
// Exported (not just used locally) so AutoApproveControl.stories.tsx can
// mount this one card in isolation for a visual check, without seeding
// every query the rest of SecuritySection needs.
/** Config key of the Defense level (pending-restart names it this way). */
const PROMPT_GUARD_KEY = 'sandbox.prompt_injection_level'

export interface AutoApproveDraft { // not-wire-format: React props from Protection's Save bar
  /** The value shown: the draft, or the saved value. */
  value: boolean
  onChange: (next: boolean) => void
  /** The Save bar's line for this value (an error or "Saved, but not in use yet"). */
  message?: FieldMessage
  /** data-settings-field id, used to move focus to it. */
  fieldId: string
}

/**
 * Auto-approve is a draft on Protection's Save bar (Settings redesign #1519
 * FR-010, FR-025): nothing is sent until Save changes, which asks for the
 * password once for the whole Save.
 */
export function AutoApproveControl({ draft }: { draft: AutoApproveDraft }) {
  const { isLoading, isError } = useQuery({
    queryKey: ['sandbox-config'],
    queryFn: fetchSandboxConfig,
  })
  const { data: sandboxStatus } = useQuery({
    queryKey: ['sandbox-status'],
    queryFn: fetchSandboxStatus,
  })
  const kernelSandboxEnforcing = sandboxStatus?.kernel_sandbox_active === true
  const noSandboxCaution = sandboxStatus !== undefined && !kernelSandboxEnforcing

  if (isLoading) {
    return (
      <Card className="p-[var(--space-3)] space-y-[var(--space-2)]">
        <div className="h-4 w-32 rounded bg-[var(--color-surface-2)] animate-pulse" />
        <div className="h-3 w-full rounded bg-[var(--color-surface-2)] animate-pulse" />
      </Card>
    )
  }

  if (isError) {
    return (
      <Card className="p-[var(--space-3)]">
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
          Failed to load the Auto-approve setting. Please try again.
        </p>
      </Card>
    )
  }

  // #920 founder decision D3 (2026-09-27): the summary no longer says tools
  // "stay inside your workspace" — under Auto, read_file, list_directory and
  // grep run outside it too; only writing keeps the workspace rule.
  //
  // Founder feedback (2026-09-24): "a huge blob of text, not well written".
  // Redesign — one short summary sentence, a collapsed group list
  // (AutoApproveAskList), and a small muted platform caveat, instead of one
  // long paragraph carrying the summary, the full 28-tool list, the
  // workspace path rule, an example, and the Windows caveat all at once.
  return (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
      <div className="flex items-center justify-between gap-[var(--space-3)]">
        <div>
          <p className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            Auto-approve
            <PasswordBadge />
          </p>
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            Tools set to &ldquo;Ask&rdquo; run without a prompt when it&rsquo;s safe: reading, listing and
            searching run anywhere except Omnipus&rsquo;s protected files, and writing runs without a prompt
            only inside your workspace and the folders mounted into it.
          </p>
        </div>
        <Switch
          checked={draft.value}
          onCheckedChange={draft.onChange}
          aria-label="Auto-approve"
          data-testid="auto-approve-global-switch"
          data-settings-field={draft.fieldId}
        />
      </div>
      {draft.message && (
        <p
          role={draft.message.tone === 'error' ? 'alert' : undefined}
          className={cn(
            'text-[length:var(--type-body-compact-size)]',
            draft.message.tone === 'error' ? 'text-[var(--color-error)]' : 'text-[var(--color-warning)]',
          )}
        >
          {draft.message.text}
        </p>
      )}

      <AutoApproveAskList />

      <p
        className={cn(
          'text-[length:var(--type-caption-size)]',
          noSandboxCaution ? 'text-[var(--color-warning)]' : 'text-[var(--color-muted)]',
        )}
      >
        Without a kernel sandbox (for example on Windows), shell commands ask first, except read-only ones and
        commands an operator rule allows.
      </p>
    </Card>
  )
}

// ── SecuritySection ────────────────────────────────────────────────────────────

/** #1882: DOM id of the rate-limit save-status live region. */

/**
 * Settings redesign #1519 splits this section in two: `protection` (health,
 * Approvals with Auto-approve and God Mode, prompt injection defense, rate
 * limits, audit log, and the notice that tool and skill permissions moved to
 * Skills & Tools — FR-051, FR-060) and `keys` (the stored keys and the master
 * key — Keys & secrets). Without `view` the old combined layout renders.
 */
export interface SecuritySectionProps { // not-wire-format: React props
  view?: 'protection' | 'keys'
  /** Protection: the God Mode control, placed after Approvals (FR-051). */
  godMode?: React.ReactNode
}

export function SecuritySection({ view, godMode }: SecuritySectionProps = {}) {
  const showProtection = view !== 'keys'
  const showKeys = view !== 'protection'
  const legacy = view === undefined
  const { addToast } = useUiStore()
  const stepUp = useStepUp()
  const queryClient = useQueryClient()

  // The config read still decides the section's loading and error states; the
  // per-agent limits come from their own route below (#1882).
  const { isLoading, isError: configError } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
  })

  const { data: credentials = [], isError: credentialsError } = useQuery({
    queryKey: ['credentials'],
    queryFn: fetchCredentials,
    retry: false,
  })

  // #1882: the per-agent limits live behind their own route. PUT /config
  // refuses `security.*` by design, so they are read and saved there only.
  const {
    data: rateLimits,
    isError: rateLimitsError,
    error: rateLimitsReadError,
    refetch: refetchRateLimits,
    isFetching: rateLimitsFetching,
  } = useQuery({
    queryKey: ['security', 'rate-limits'],
    queryFn: fetchRateLimits,
    retry: false,
  })

  // Auto-approve is a draft on the same Save bar (FR-010, FR-025): Save sends
  // it with PUT /security/sandbox-config after the Save's one password prompt.
  const { data: sandboxConfig } = useQuery({
    queryKey: ['sandbox-config'],
    queryFn: fetchSandboxConfig,
    // Read here only on Protection; elsewhere the control's own read fills
    // the same cache entry, so no extra request is sent.
    enabled: view === 'protection',
  })
  // Defense level is a draft on the same Save bar too (FR-010): Save sends it
  // with PUT /security/prompt-guard, which needs no password.
  const { data: promptGuard } = useQuery({
    queryKey: ['prompt-guard-k'],
    queryFn: fetchPromptGuardLevel,
    // Read here only on Protection; elsewhere the control's own read fills
    // the same cache entry, so no extra request is sent.
    enabled: view === 'protection',
  })

  // Rate limits wait for Save changes (FR-010, #346: no auto-save in Settings).
  // An empty field means "Unlimited", which the route spells as 0.
  const rateLimitFields = useMemo<SaveBarField[]>(() => {
    const validate = (v: SaveBarValue) => {
      if (v === '' || v === null) return null
      const n = Number(v)
      return Number.isInteger(n) && n >= 0 ? null : 'Enter a whole number, or leave it empty for no limit.'
    }
    return [
      {
        key: 'auto_approve',
        label: 'Auto-approve',
        value: sandboxConfig?.auto_approve === true,
        kind: 'switch',
        password: true,
        target: 'sandbox-config',
        disabled: !sandboxConfig,
      },
      {
        key: PROMPT_GUARD_KEY,
        label: 'Defense level',
        value: promptGuard?.level ?? null,
        kind: 'select',
        options: ['low', 'medium', 'high'],
        target: 'prompt-guard',
        disabled: !promptGuard,
      },
      {
        key: 'max_agent_llm_calls_per_hour',
        label: 'Model calls per hour',
        value: rateLimits?.max_agent_llm_calls_per_hour || '',
        kind: 'number',
        description: 'Default limit for each agent. Empty means no limit.',
        validate,
        disabled: !rateLimits,
      },
      {
        key: 'max_agent_tool_calls_per_minute',
        label: 'Tool calls per minute',
        value: rateLimits?.max_agent_tool_calls_per_minute || '',
        kind: 'number',
        description: 'Default limit for each agent. Empty means no limit.',
        validate,
        disabled: !rateLimits,
      },
    ]
  }, [rateLimits, sandboxConfig, promptGuard])
  const rateLimitState = useSaveBar({
    section: 'protection',
    fields: rateLimitFields,
    onSave: (body, ctx) =>
      ctx.target === 'sandbox-config'
        ? toWriteResponse(() => updateSandboxConfig({ auto_approve: body.auto_approve === true }, ctx.reauthToken))
        : ctx.target === 'prompt-guard'
        ? toWriteResponse(() => updatePromptGuardLevel(body[PROMPT_GUARD_KEY] as PromptInjectionLevel))
        : toWriteResponse(() =>
        updateRateLimits({
          max_agent_llm_calls_per_hour: Number(body.max_agent_llm_calls_per_hour ?? rateLimits?.max_agent_llm_calls_per_hour ?? 0) || 0,
          max_agent_tool_calls_per_minute: Number(body.max_agent_tool_calls_per_minute ?? rateLimits?.max_agent_tool_calls_per_minute ?? 0) || 0,
        }),
      ),
    readBack: async (key) => {
      if (key === 'auto_approve') return (await fetchSandboxConfig()).auto_approve === true
      if (key === PROMPT_GUARD_KEY) return (await fetchPromptGuardLevel()).level
      const fresh = await fetchRateLimits()
      return (fresh as unknown as Record<string, unknown>)[key]
    },
    onSaved: (keys) => {
      if (keys.includes('auto_approve')) {
        void queryClient.invalidateQueries({ queryKey: ['sandbox-config'] })
        void queryClient.invalidateQueries({ queryKey: ['sandbox-status'] })
      }
      if (keys.includes(PROMPT_GUARD_KEY)) void queryClient.invalidateQueries({ queryKey: ['prompt-guard-k'] })
      void queryClient.invalidateQueries({ queryKey: ['security', 'rate-limits'] })
    },
  })
  const autoApproveField = rateLimitFields[0]
  const promptGuardField = rateLimitFields[1]
  // FR-010: wherever these controls show, they are drafts on this Save bar.
  const autoApproveDraft: AutoApproveDraft = {
    value: rateLimitState.value('auto_approve') === true,
    onChange: (next) => rateLimitState.setDraft('auto_approve', next),
    message: rateLimitState.message('auto_approve'),
    fieldId: fieldDomId('protection', 'auto_approve'),
  }
  const promptGuardDraft: PromptGuardDraft = {
    // Null only before the level is read; the control then shows its own
    // loading or error state instead of the choices.
    value: rateLimitState.value(PROMPT_GUARD_KEY) as PromptInjectionLevel,
    onChange: (next) => rateLimitState.setDraft(PROMPT_GUARD_KEY, next),
    message: rateLimitState.message(PROMPT_GUARD_KEY),
    fieldId: fieldDomId('protection', PROMPT_GUARD_KEY),
  }

  // Audit log dialog state
  const [auditLogOpen, setAuditLogOpen] = useState(false)

  // Credential vault modal state
  const [credModalOpen, setCredModalOpen] = useState(false)
  const [credKey, setCredKey] = useState('')
  const [credValue, setCredValue] = useState('')
  const [rotateModalOpen, setRotateModalOpen] = useState(false)
  const [rotatePassphrase, setRotatePassphrase] = useState('')
  // ADR-0010 WP3: each vault operation gets its OWN step-up gate (ReAuthDialog
  // + a replayed consent token in local mode, ConfirmDialog with no token in
  // platform mode). Deleting and re-keying the vault are among the
  // highest-blast-radius operations in the product; "the vault is one
  // control" is a UI grouping, not a licence to gate once (spec FR-OB-040).

  const { mutateAsync: doAddCred, isPending: isAddingCred } = useMutation({
    mutationFn: (vars: { token?: string }) => addCredential(credKey.trim(), credValue, vars.token),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['credentials'] })
      addToast({ message: `Credential "${credKey}" saved`, variant: 'success' })
      setCredModalOpen(false)
      setCredKey('')
      setCredValue('')
    },
    onError: (err: unknown) => {
      addToast({ message: getErrorMessage(err, 'Save failed'), variant: 'error' })
    },
  })

  const { mutateAsync: doDeleteCred } = useMutation({
    mutationFn: (vars: { key: string; token?: string }) => deleteCredential(vars.key, vars.token),
    onSuccess: (_data, vars) => {
      queryClient.invalidateQueries({ queryKey: ['credentials'] })
      addToast({ message: `Credential "${vars.key}" removed`, variant: 'success' })
    },
    onError: (err: unknown) => {
      addToast({ message: getErrorMessage(err, 'Delete failed'), variant: 'error' })
    },
  })

  const { mutateAsync: doRotate, isPending: isRotating } = useMutation({
    mutationFn: (vars: { token?: string }) => rotateCredentials(rotatePassphrase, vars.token),
    onSuccess: () => {
      addToast({ message: 'Credential vault re-encrypted with the new passphrase', variant: 'success' })
      setRotateModalOpen(false)
      setRotatePassphrase('')
    },
    onError: (err: unknown) => {
      addToast({ message: getErrorMessage(err, 'Rotation failed'), variant: 'error' })
    },
  })

  // requestAddCredential/requestDeleteCredential/requestRotate each run their
  // mutation through the step-up gate (ADR-0010 WP3): ReAuthDialog + a
  // replayed consent token in local mode, ConfirmDialog with no token in
  // platform mode. The write only fires once the operator stands behind it.
  function requestAddCredential() {
    void stepUp
      .gate(
        (token) => doAddCred({ token }),
        {
          title: 'Store this credential?',
          body: `${credKey.trim() || 'This key'} is encrypted and written to the vault. Anything already stored under that name is replaced.`,
          confirmLabel: 'Store credential',
        },
      )
      .catch((err: unknown) => {
        if (isReAuthCancelled(err)) return
      })
  }

  // B7 / FR-056 (S-Q11 B): deleting a stored key asks one confirm, not the
  // password — the server no longer requires a consent token for it.
  const [deleteKey, setDeleteKey] = useState<string | null>(null)
  function requestDeleteCredential(key: string) {
    setDeleteKey(key)
  }

  function requestRotate() {
    void stepUp
      .gate(
        (token) => doRotate({ token }),
        {
          title: 'Rotate the master key?',
          body: 'Every stored provider key and connector credential is re-encrypted under a new key. Agents keep running; nothing needs restarting.',
          confirmLabel: 'Rotate master key',
        },
      )
      .catch((err: unknown) => {
        if (isReAuthCancelled(err)) return
      })
  }

  if (isLoading) {
    return <div className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Loading...</div>
  }

  if (configError) {
    return <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-text-error)]">Failed to load security settings. Please try again.</p>
  }

  // ADR-053 D12 retired the "Daily spending limit" UI block.

  const rateLimitsCard = (
  <Card className="px-[var(--space-3)]">
    {rateLimitsError && (
      <div className="pt-[var(--space-2)]">
      <ReadErrorCallout
        title="The current limits could not be loaded"
        action="Press Retry, or reload the page if it keeps failing."
        error={rateLimitsReadError}
        onRetry={() => void refetchRateLimits()}
        retrying={rateLimitsFetching}
        data-testid="rate-limits-load-error"
      />
      </div>
    )}
    {rateLimitFields.filter((f) => f !== autoApproveField && f !== promptGuardField).map((f) => (
      <SettingRow key={f.key} state={rateLimitState} field={f} />
    ))}
    {view !== 'protection' && <SaveBarPanel {...rateLimitState.bar} />}
  </Card>
  )

  const auditBlock = (
  <section>
    <div className="flex items-center justify-between mb-[var(--space-2-5)]">
      <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">
        Audit Log
      </p>
      <Button
        variant="outline"
        size="sm"
        className="h-7 px-[var(--space-2)] text-[length:var(--type-utility-xs-size)]"
        onClick={() => setAuditLogOpen(true)}
      >
        View Log
      </Button>
    </div>
    <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
      Security events, policy decisions, and tool executions.
    </p>
  </section>
  )

  return (
    <div className="space-y-[var(--space-4)]">
      {legacy && (
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Security & Policy</h2>
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            Control how protected your setup is and adjust agent boundaries.
          </p>
        </div>
      )}

      {/* ── PRIMARY LAYER (US-B1): Security health + plain outcome controls ──── */}

      {showProtection && (
      <>
      {/* Security Health — score always visible at top */}
      <section
        className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-[var(--space-3)] space-y-[var(--space-3)]"
        aria-label="Security health"
        data-testid="security-health-header"
      >
        <DiagnosticsSection />
      </section>

      {/* Plain outcome toggles (US-B1: 3-4 toggles without jargon) */}
      <section className="space-y-[var(--space-2-5)]" data-testid="plain-toggles">
        <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">
          Protection settings
        </p>

        {/* 1. Auto-approve (ADR-092) — primary-layer tool-access control:
            whether a tool resolved to "ask" still prompts. */}
        <AutoApproveControl draft={autoApproveDraft} />

        {/* 2. Skill Trust (US-E4 / #340) — plain language, top-level. In the
            redesign it moved to Skills & Tools (FR-060). */}
        {legacy && <SkillTrustSection />}

        {/* God Mode sits after Approvals in Protection (FR-051). */}
        {view === 'protection' && godMode}
      </section>

      </>
      )}

      {legacy && (
      <>
      {/* ── ADVANCED LAYER (US-B1): all jargon behind one collapsed section ── */}
      <AdvancedDisclosure
        title="Advanced / technical details"
        summary="Process isolation, tool grid, audit log — safe to skip"
        data-testid="advanced-technical-details"
      >
        <div className="space-y-[var(--space-4)]">

          {/* Tool Access — Global Policies (US-B3) */}
          <section>
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider mb-[var(--space-2-5)]">
              Tool Access — Global Policies
            </p>
            <GlobalToolPoliciesSection />
          </section>

          <Separator />

          {/* SSRF Proxy */}
          <section>
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider mb-[var(--space-2-5)]">
              SSRF Proxy
            </p>
            <ExecProxyStatusCard />
          </section>

          <Separator />

          {/* Prompt Guard */}
          <section>
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider mb-[var(--space-2-5)]">
              Prompt Injection Defense
            </p>
            <PromptGuardSection draft={promptGuardDraft} />
          </section>

          <Separator />

          {/* Process Sandbox */}
          <section>
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider mb-[var(--space-2-5)]">
              Process Sandbox (Landlock / seccomp)
            </p>
            <SandboxSection />
            {/* The old footnote read "Sandbox configuration is auto-detected at
                startup based on your kernel capabilities" — printed directly
                beneath a control the operator very much does set, and it read
                as "this is not yours to change". What is actually detected is
                the kernel's capabilities, which decide which modes will work.
                (UAT defect 002 / ADR-068 §6.) */}
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] pt-[var(--space-1)]">
              What your kernel supports is detected when the gateway starts, and that decides which
              modes will actually work — the mode itself is yours to choose above. The sandbox is
              only one of the boundaries on what an agent may touch: the shell workspace limit is a
              separate rule with its own setting, in the same panel.
            </p>
          </section>

          <Separator />

          {/* Per-Agent Rate Limits */}
          <section>
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider mb-[var(--space-2-5)]">
              Per-Agent Rate Limits
            </p>
            {rateLimitsCard}
          </section>

          <Separator />

          {auditBlock}

        </div>
      </AdvancedDisclosure>
      </>
      )}

      {view === 'protection' && (
        <>
          <section aria-label="Prompt injection defense" className="space-y-[var(--space-2)]">
            <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Prompt injection defense</h3>
            <PromptGuardSection draft={promptGuardDraft} />
          </section>
          <section aria-label="Rate limits per agent" className="space-y-[var(--space-2)]">
            <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Rate limits per agent</h3>
            {rateLimitsCard}
          </section>
          {auditBlock}
          <Callout variant="info" data-testid="protection-moved-permissions">
            <CalloutTitle>Tool and skill permissions moved</CalloutTitle>
            <CalloutDescription>
              Allow, Ask or Deny for each tool, and whether unverified skills may load, are set on that page now.
              An agent can still be stricter on its own profile.
            </CalloutDescription>
            <CalloutActions>
              <Button asChild variant="outline" size="sm">
                <Link to="/skills">Open Skills &amp; Tools</Link>
              </Button>
            </CalloutActions>
          </Callout>
          {/* One Save bar for the section: Auto-approve, Defense level and the rate limits. */}
          <SaveBarPanel {...rateLimitState.bar} />
        </>
      )}

      {showKeys && (
      <>
      {/* ── Credential Vault (US-B4) — always visible with reassurance line ─── */}
      <section>
        <div className="flex items-center justify-between mb-[var(--space-2-5)]">
          <div>
            <div className="flex items-center gap-[var(--space-2)]">
              <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Credential Vault</h3>
            </div>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)] flex items-center gap-[var(--space-1)]">
              <Lock size={11} />
              Your keys are encrypted and stored only on this server — never sent anywhere.
            </p>
          </div>
          <div className="flex items-center gap-[var(--space-2)]">
            <Button
              size="sm"
              variant="outline"
              className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
              onClick={() => setRotateModalOpen(true)}
              data-testid="rotate-master-key"
            >
              <Key size={11} weight="bold" />
              Rotate master key
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 px-[var(--space-2)] gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
              onClick={() => setCredModalOpen(true)}
            >
              <Plus size={11} weight="bold" />
              Add key
            </Button>
          </div>
        </div>

        <Card className="divide-y divide-[var(--color-border)]">
          {credentialsError && (
            <div className="p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-text-error)]">Failed to load credentials. Please try again.</div>
          )}
          {!credentialsError && credentials.length === 0 && (
            <div className="p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] flex items-center gap-[var(--space-2)]">
              <Key size={14} />
              No credentials stored. Add your first key above.
            </div>
          )}
          {credentials.map((cred) => (
            <div key={cred.key} className="flex items-center justify-between px-[var(--space-3)] py-[var(--space-2)]">
              <div>
                <p className="text-[length:var(--type-body-compact-size)] font-mono text-[var(--color-secondary)]">{cred.key}</p>
                <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] font-mono">••••••••••••</p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="h-7 w-7 p-0 text-[var(--color-muted)] hover:text-[var(--color-error)]"
                onClick={() => requestDeleteCredential(cred.key)}
                data-testid={`delete-cred-${cred.key}`}
                aria-label={`Remove credential ${cred.key}`}
              >
                <Trash size={13} />
              </Button>
            </div>
          ))}
        </Card>
      </section>

      </>
      )}

      <ConfirmDialog
        open={deleteKey !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteKey(null)
        }}
        title="Delete this key?"
        description={`${deleteKey ?? ''}: agents and connectors that use it stop working until you add it again. This cannot be undone.`}
        confirmLabel="Delete key"
        destructive
        onConfirm={() => {
          const key = deleteKey
          setDeleteKey(null)
          // The mutation's onError already shows the failure as a toast;
          // this catch only stops an unhandled rejection.
          if (key) void doDeleteCred({ key }).catch(() => undefined)
        }}
      />

      <AuditLogViewer open={auditLogOpen} onOpenChange={setAuditLogOpen} />

      {/* Rotate master key modal (G5) */}
      <Dialog open={rotateModalOpen} onOpenChange={setRotateModalOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Rotate master key</DialogTitle>
          </DialogHeader>
          <div className="space-y-[var(--space-2-5)] py-[var(--space-2)]">
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              Re-encrypts the entire credential vault under a new passphrase. Back up the new
              passphrase — it&apos;s required to unlock the vault next time.
            </p>
            <div className="space-y-[var(--space-2)]">
              <Label htmlFor="rotate-passphrase">New passphrase</Label>
              <Input
                id="rotate-passphrase"
                type="password"
                value={rotatePassphrase}
                onChange={(e) => setRotatePassphrase(e.target.value)}
                placeholder="Enter a new passphrase"
                className="h-8 text-[length:var(--type-utility-xs-size)] font-mono"
                data-testid="rotate-passphrase-input"
                autoFocus
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setRotateModalOpen(false)}>Cancel</Button>
            <Button
              size="sm"
              onClick={requestRotate}
              disabled={!rotatePassphrase.trim() || isRotating}
              data-testid="rotate-confirm"
            >
              {isRotating ? 'Rotating...' : 'Rotate'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add credential modal */}
      <Dialog open={credModalOpen} onOpenChange={setCredModalOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="font-headline text-base">Add Credential</DialogTitle>
          </DialogHeader>
          <div className="space-y-[var(--space-2-5)] py-[var(--space-2)]">
            <div className="space-y-[var(--space-2)]">
              <Label htmlFor="cred-key">Key name</Label>
              <Input
                id="cred-key"
                value={credKey}
                onChange={(e) => setCredKey(e.target.value)}
                placeholder="e.g. OPENAI_API_KEY"
                className="h-8 text-[length:var(--type-utility-xs-size)] font-mono"
                autoFocus
              />
            </div>
            <div className="space-y-[var(--space-2)]">
              <Label htmlFor="cred-value">Value</Label>
              <Input
                id="cred-value"
                type="password"
                value={credValue}
                onChange={(e) => setCredValue(e.target.value)}
                placeholder="sk-..."
                className="h-8 text-[length:var(--type-utility-xs-size)] font-mono"
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setCredModalOpen(false)}>Cancel</Button>
            <Button
              size="sm"
              onClick={requestAddCredential}
              disabled={!credKey.trim() || !credValue || isAddingCred}
              data-testid="add-cred-save"
            >
              {isAddingCred ? 'Saving...' : 'Save'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ADR-0010 WP3 — one step-up gate per vault operation. */}
      {stepUp.dialogs}
    </div>
  )
}
