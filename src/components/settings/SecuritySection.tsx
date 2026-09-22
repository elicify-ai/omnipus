/**
 * SecuritySection — Settings → Security tab.
 *
 * Two-layer IA (US-B1 / #327):
 *   Primary layer  — Security health (DiagnosticsSection score + 3 plain toggles)
 *   Advanced layer — All jargon (sandbox internals, SSRF, deny-regex, tool grid,
 *                    audit log) under ONE AdvancedDisclosure.
 *
 * Risky controls wrapped in RiskySettingControl (US-B2 / #328):
 *   - policyMode (safe = 'deny')
 *   - bind_address (in GatewaySection — not here; done there)
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

import { useState, useEffect, useRef, useMemo } from 'react'
import { AuditLogViewer } from './AuditLogViewer'
import { PromptGuardSection } from './PromptGuardSection'
import { ExecProxyStatusCard } from './ExecProxyStatusCard'
import { SkillTrustSection } from './SkillTrustSection'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash, Key, Lock } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { Switch } from '@/components/ui/switch'
import { SmartSelect } from '@/components/ui/smart-select'
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
  updateConfig,
  fetchCredentials,
  addCredential,
  deleteCredential,
  rotateCredentials,
  fetchBuiltinTools,
  fetchGlobalToolPolicies,
  updateGlobalToolPolicies,
  getErrorMessage,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { DiagnosticsSection } from './DiagnosticsSection'
import { SandboxSection } from './SandboxSection'
import { Label } from '@/components/ui/label'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { ToolPolicyEditor, type ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import { RiskySettingControl } from '@/components/shared/RiskySettingControl'
import { isReAuthCancelled } from './useReAuthGate'
import { useStepUp } from './useStepUp'

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
    { disabled: !isDraftReady },
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

// ── Policy mode risky control (US-B2) ─────────────────────────────────────────

const POLICY_MODE_COPY = {
  dialogTitle: 'Switch to Allow mode?',
  dialogDescription:
    'Allow mode lets agents run tools without asking first. This gives agents more autonomy but lowers your oversight. Switch to Deny to stay in control.',
  confirmLabel: 'Switch to Allow anyway',
  cancelLabel: 'Keep Deny (safer)',
}

// ── SecuritySection ────────────────────────────────────────────────────────────

export function SecuritySection() {
  const { addToast } = useUiStore()
  const stepUp = useStepUp()
  const queryClient = useQueryClient()

  const { data: config, isLoading, isError: configError } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
  })

  const { data: credentials = [], isError: credentialsError } = useQuery({
    queryKey: ['credentials'],
    queryFn: fetchCredentials,
    retry: false,
  })

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  // US-B2: policyMode derives its badge from the PERSISTED value (config.security.policy_mode).
  // The local state is the draft; after save the query is invalidated so config refetches.
  const [policyMode, setPolicyMode] = useState<'allow' | 'deny'>('deny')
  const [execApproval, setExecApproval] = useState<'auto' | 'ask' | 'deny'>('ask')
  // ADR-053 D12: dailyCostCap state retired alongside the SEC-26 USD cap.
  const [agentLlmCallsPerHour, setAgentLlmCallsPerHour] = useState('')
  const [agentToolCallsPerMin, setAgentToolCallsPerMin] = useState('')
  const [execTimeoutSecs, setExecTimeoutSecs] = useState('')
  const [maxBackgroundSecs, setMaxBackgroundSecs] = useState('')
  const [enableDenyPatterns, setEnableDenyPatterns] = useState(false)
  // D3 / UAT spurious-PUT fix: reactive readiness flag, distinct from the
  // `!config` check useAutoSave's `disabled` option used to key off of.
  // `config` turns truthy in the SAME commit the hydration effect below is
  // SCHEDULED, but the effect's own setState calls don't land until the
  // NEXT commit — so `disabled: !config` flipped false one render too
  // early, letting useAutoSave capture the hardcoded useState defaults as
  // its baseline instead of the real persisted values. `securityHydrated`
  // is set at the END of the hydration effect, so it flips true in the
  // same commit the real values land.
  const [securityHydrated, setSecurityHydrated] = useState(false)

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

  useEffect(() => {
    if (!config) return
    if (isDirtyRef.current) return
    setPolicyMode(config.security.policy_mode)
    setExecApproval(config.security.exec_approval)
    setAgentLlmCallsPerHour(config.security.rate_limits.max_agent_llm_calls_per_hour?.toString() ?? '')
    setAgentToolCallsPerMin(config.security.rate_limits.max_agent_tool_calls_per_minute?.toString() ?? '')
    setExecTimeoutSecs(config.security.exec_timeout_seconds?.toString() ?? '')
    setMaxBackgroundSecs(config.security.max_background_seconds?.toString() ?? '')
    setEnableDenyPatterns(config.security.enable_deny_patterns ?? false)
    setSecurityHydrated(true)
  }, [config])

  const securityFormData = useMemo(() => ({
    policy_mode: policyMode,
    exec_approval: execApproval,
    exec_timeout_seconds: execTimeoutSecs,
    max_background_seconds: maxBackgroundSecs,
    enable_deny_patterns: enableDenyPatterns,
    agent_llm_calls_per_hour: agentLlmCallsPerHour,
    agent_tool_calls_per_min: agentToolCallsPerMin,
  }), [policyMode, execApproval, execTimeoutSecs, maxBackgroundSecs, enableDenyPatterns, agentLlmCallsPerHour, agentToolCallsPerMin])

  const { status: saveStatus, error: saveError } = useAutoSave(
    securityFormData,
    async () => {
      await updateConfig({
        security: {
          policy_mode: policyMode,
          exec_approval: execApproval,
          exec_timeout_seconds: execTimeoutSecs ? parseInt(execTimeoutSecs, 10) : undefined,
          max_background_seconds: maxBackgroundSecs ? parseInt(maxBackgroundSecs, 10) : undefined,
          enable_deny_patterns: enableDenyPatterns,
          rate_limits: {
            ...config?.security.rate_limits,
            max_agent_llm_calls_per_hour: agentLlmCallsPerHour ? parseInt(agentLlmCallsPerHour, 10) : undefined,
            max_agent_tool_calls_per_minute: agentToolCallsPerMin ? parseInt(agentToolCallsPerMin, 10) : undefined,
          },
        },
      })
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['config'] })
    },
    { disabled: !securityHydrated },
  )

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

  function requestDeleteCredential(key: string) {
    void stepUp
      .gate(
        (token) => doDeleteCred({ key, token }),
        {
          title: 'Remove this credential?',
          body: `${key} is permanently removed from the vault. Anything using it stops working until you store it again.`,
          confirmLabel: 'Remove credential',
        },
      )
      .catch((err: unknown) => {
        if (isReAuthCancelled(err)) return
      })
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

  // US-B2: badge derives from persisted config.security.policy_mode (not local state).
  // After save, the query is invalidated so persistedPolicyMode updates.
  const persistedPolicyMode = config?.security.policy_mode ?? 'deny'

  return (
    <div className="space-y-[var(--space-4)]">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Security & Policy</h2>
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            Control how protected your setup is and adjust agent boundaries.
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      {/* ── PRIMARY LAYER (US-B1): Security health + plain outcome controls ──── */}

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

        {/* 1. Default policy mode — wraps risky "Allow" (US-B2) */}
        <Card className="p-[var(--space-3)] space-y-[var(--space-2)]">
          <div>
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Agent tool access</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
              Whether agents must ask your permission before running tools or can run freely.
            </p>
          </div>
          <RiskySettingControl
            options={[
              { value: 'deny', label: 'Must ask first (safer)' },
              { value: 'allow', label: 'Run freely' },
            ]}
            currentValue={persistedPolicyMode}
            selectedValue={policyMode}
            safeValue="deny"
            copy={POLICY_MODE_COPY}
            onConfirm={(v) => {
              markDirty()
              setPolicyMode(v as 'allow' | 'deny')
            }}
            onSelectSafe={(v) => {
              markDirty()
              setPolicyMode(v as 'allow' | 'deny')
            }}
          />
        </Card>

        {/* 2. Exec approval */}
        <Card className="p-[var(--space-3)]">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Shell command approval</p>
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">How shell commands are handled when an agent wants to run them</p>
            </div>
            <SmartSelect
              value={execApproval}
              onValueChange={(v) => { markDirty(); setExecApproval(v as typeof execApproval) }}
              triggerClassName="w-[130px] h-8 text-xs"
              ariaLabel="Shell command approval"
              items={[
                { value: 'auto', label: 'Auto-allow' },
                { value: 'ask', label: 'Ask each time' },
                { value: 'deny', label: 'Always deny' },
              ]}
            />
          </div>
        </Card>

        {/* 3. Skill Trust (US-E4 / #340) — plain language, top-level */}
        <SkillTrustSection />
      </section>

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

          {/* Command Execution Internals */}
          <section>
            <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider mb-[var(--space-2-5)]">
              Command Execution
            </p>
            <Card className="p-[var(--space-3)] space-y-[var(--space-3)]">
              <div className="flex items-center justify-between">
                <div>
                  <Label htmlFor="exec-timeout-seconds">Exec timeout (seconds)</Label>
                  <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Max time for a single command, 0 = no limit</p>
                </div>
                <Input
                  id="exec-timeout-seconds"
                  type="number"
                  min="0"
                  value={execTimeoutSecs}
                  onChange={(e) => { markDirty(); setExecTimeoutSecs(e.target.value) }}
                  className="w-24 h-7 text-[length:var(--type-utility-xs-size)] font-mono"
                  placeholder="0"
                />
              </div>

              <Separator />

              <div className="flex items-center justify-between">
                <div>
                  <Label htmlFor="max-background-seconds">Background timeout (seconds)</Label>
                  <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Max time for background processes, 0 = no limit</p>
                </div>
                <Input
                  id="max-background-seconds"
                  type="number"
                  min="0"
                  value={maxBackgroundSecs}
                  onChange={(e) => { markDirty(); setMaxBackgroundSecs(e.target.value) }}
                  className="w-24 h-7 text-[length:var(--type-utility-xs-size)] font-mono"
                  placeholder="0"
                />
              </div>

              <Separator />

              <div className="flex items-center justify-between">
                <div>
                  <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Enable deny patterns</p>
                  <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Block commands matching configured deny patterns</p>
                </div>
                <Switch
                  checked={enableDenyPatterns}
                  onCheckedChange={(v) => { markDirty(); setEnableDenyPatterns(v) }}
                  aria-label="Enable deny patterns"
                />
              </div>
            </Card>
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
            <PromptGuardSection />
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
            <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
              <div className="flex items-center justify-between">
                <div>
                  <Label htmlFor="agent-llm-calls-per-hour">LLM calls / hour</Label>
                  <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Default limit per agent</p>
                </div>
                <Input
                  id="agent-llm-calls-per-hour"
                  type="number"
                  min="0"
                  value={agentLlmCallsPerHour}
                  onChange={(e) => { markDirty(); setAgentLlmCallsPerHour(e.target.value) }}
                  className="w-24 h-7 text-[length:var(--type-utility-xs-size)] font-mono"
                  placeholder="Unlimited"
                />
              </div>
              <div className="flex items-center justify-between">
                <div>
                  <Label htmlFor="agent-tool-calls-per-minute">Tool calls / minute</Label>
                  <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Default limit per agent</p>
                </div>
                <Input
                  id="agent-tool-calls-per-minute"
                  type="number"
                  min="0"
                  value={agentToolCallsPerMin}
                  onChange={(e) => { markDirty(); setAgentToolCallsPerMin(e.target.value) }}
                  className="w-24 h-7 text-[length:var(--type-utility-xs-size)] font-mono"
                  placeholder="Unlimited"
                />
              </div>
            </Card>
          </section>

          <Separator />

          {/* Audit Log */}
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

        </div>
      </AdvancedDisclosure>

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
