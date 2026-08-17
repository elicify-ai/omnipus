// CreateAgentModal — thin wrapper that mounts CreateAgentWizard.
//
// W4 of agent-form-requirements rewrites the create-agent flow as a 3-step
// + Advanced wizard (Main / Subagent / subagent_3p), per
// docs/internal/specs/agent-form-requirements.md §5.3-§5.5. The legacy
// 2-tab `CreateAgentModal.tsx` (573 LOC, using 'custom' | 'worker' types)
// was the W3 Wave C1 G4 signpost; this file preserves the mount point
// (`<CreateAgentModal />` in `AgentListScreen.tsx:192`) while delegating
// the entire form surface to `<CreateAgentWizard initialType={...} />`.
//
// Production path: reads `createAgentModalOpen` + `createAgentModalType` from
// the Zustand store. W6's `openCreateAgentModal('Main' | 'Subagent' | 'subagent_3p')`
// passes the type into the store, which feeds the wizard's initialType.
//
// Test path: legacy callers pass `open onClose onCreate initialType` props.
// initialType accepts the W3-era 'custom' | 'worker' literals for backward
// compatibility — we map them to the new wire enum ('custom' → 'Main',
// 'worker' → 'Subagent').

import { useCallback, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { useUiStore } from '@/store/ui'
import {
  createAgent,
  fetchGlobalToolPolicies,
  fetchProviders,
  fetchRegistryTools,
  fetchSkills,
  getErrorMessage,
} from '@/lib/api'
import type {
  Agent,
  AgentCreateRequest,
  AgentCreateRequestMain,
  AgentCreateRequestSubagent,
  AgentCreateRequestSubagent3p,
  RegistryTool,
} from '@/lib/api'
import { findOrphanedPresetOverrideKeys, resolveToolsCfg } from '@/lib/toolPolicyPresets'

import {
  CreateAgentWizard,
  type WizardCli,
  type WizardSubmitPayload,
  type WizardType,
} from './CreateAgentWizard'

interface CreateAgentModalProps {
  /** Override modal open state (optional — defaults to Zustand store) */
  open?: boolean
  /** Override close handler (optional — defaults to Zustand store) */
  onClose?: () => void
  /** Override create handler (optional — defaults to REST API) */
  onCreate?: (data: AgentCreateRequest) => Promise<void>
  /**
   * Initial wizard type. Accepts the new wire enum OR the legacy
   * 'custom' | 'worker' literals (mapped to 'Main' / 'Subagent' respectively)
   * for backward compatibility with W3 tests.
   */
  initialType?: WizardType | 'custom' | 'worker'
  /**
   * Initial CLI choice for External wizards. Optional.
   */
  initialCli?: WizardCli
}

/** Map legacy 'custom' | 'worker' literals to the new wire enum. */
function normalizeWizardType(
  t: WizardType | 'custom' | 'worker' | undefined,
  fallback: WizardType,
): WizardType {
  if (!t) return fallback
  if (t === 'custom') return 'Main'
  if (t === 'worker') return 'Subagent'
  return t
}

/** Convert the wizard's submit payload to a wire AgentCreateRequest.
 *
 *  AgentCreateRequest is a discriminated union (one variant per agent type,
 *  `additionalProperties: false` — see contracts/components/schemas/
 *  AgentCreateRequest*.yaml and the field matrix in docs/internal/
 *  architecture/agent-types-field-matrix.md), so each branch builds exactly
 *  the fields its variant carries; sending a field on the wrong variant is
 *  a 400, not a silent drop.
 *
 *  - Description is omitted when empty (optional for Main; the server
 *    enforces non-empty for workers).
 *  - Main / Subagent never send `executor` — the server derives `native`.
 *    subagent_3p always sends `kind: external-cli` (the variant requires it).
 *  - All string fields are `.trim()`-ed to match the wizard's step
 *    gating (which validates `.trim().length > 0`).
 *  - Main / Subagent (when not inheriting tools) send `tools_cfg` via
 *    `resolveToolsCfg` (`@/lib/toolPolicyPresets`) — the SAME helper
 *    Step3Tools.tsx's `toolPolicyValue()` uses for its uncommitted-default
 *    render, so an untouched Tools step still commits the Balanced-preset
 *    policy the wizard displayed, rather than silently omitting the field
 *    and falling back to the backend's much-more-restrictive
 *    `NewCustomAgentToolsCfg()` seed. `resolveToolsCfg` returns `undefined`
 *    (field omitted, same as before any default was committed) only in the
 *    degenerate case where the tool registry hasn't resolved yet — see its
 *    doc comment.
 *  - subagent_3p never has this concern: its variant schema
 *    (`AgentCreateRequestSubagent3p`, `additionalProperties: false`) does
 *    not carry `tools_cfg` at all — the external CLI runs its own tool loop
 *    and never reaches the Tools step (2-step wizard for that type).
 */
function payloadToCreateRequest(
  payload: WizardSubmitPayload,
  tools: RegistryTool[],
): AgentCreateRequest {
  const name = payload.name.trim()
  const soul = payload.soul.trim()
  const description = payload.description.trim()

  if (payload.type === 'subagent_3p') {
    const req: AgentCreateRequestSubagent3p = {
      type: 'subagent_3p',
      name,
      color: payload.color,
      icon: payload.icon,
      soul,
      // The external CLI is the runner; the variant requires the block.
      executor: { kind: 'external-cli' },
    }
    if (payload.cli) req.executor.cli = payload.cli
    if (payload.executor_cli_path) req.executor.cli_path = payload.executor_cli_path
    if (payload.executor_env_overrides) req.executor.env_overrides = payload.executor_env_overrides
    if (payload.executor_cli_args) req.executor.cli_args = payload.executor_cli_args
    if (description) req.description = description
    if (payload.model.trim()) req.model = payload.model.trim()
    if (payload.provider?.trim()) req.provider = payload.provider.trim()
    if (payload.rate_limits !== undefined) req.rate_limits = payload.rate_limits
    if (payload.timeout_seconds !== undefined) req.timeout_seconds = payload.timeout_seconds
    return req
  }

  if (payload.type === 'Subagent') {
    // UAT 4a: native subagents that inherit a field from the caller omit it
    // from the create request so the server keeps the inherited rail. These
    // flags are UI-only and never cross the wire.
    const inheritModel = payload.inherit_model === true
    const inheritTools = payload.inherit_tools === true
    const inheritSkills = payload.inherit_skills === true

    const req: AgentCreateRequestSubagent = {
      type: 'Subagent',
      name,
      color: payload.color,
      icon: payload.icon,
      soul,
    }
    if (description) req.description = description
    // Model omitted when inherited (server falls back to the caller/global
    // model); provider and fallbacks ride with the primary model.
    if (!inheritModel && payload.model.trim()) req.model = payload.model.trim()
    if (!inheritModel && payload.provider?.trim()) req.provider = payload.provider.trim()
    if (!inheritModel && payload.fallback_models !== undefined) req.fallback_models = payload.fallback_models
    if (!inheritTools) {
      const toolsCfg = resolveToolsCfg(payload.tools_cfg, tools)
      if (toolsCfg !== undefined) req.tools_cfg = toolsCfg
    }
    if (!inheritSkills && payload.skills !== undefined) req.skills = payload.skills
    if (payload.model_params !== undefined) req.model_params = payload.model_params
    if (payload.shell_policy !== undefined) req.shell_policy = payload.shell_policy
    if (payload.rate_limits !== undefined) req.rate_limits = payload.rate_limits
    if (payload.timeout_seconds !== undefined) req.timeout_seconds = payload.timeout_seconds
    if (payload.max_tool_iterations !== undefined) req.max_tool_iterations = payload.max_tool_iterations
    return req
  }

  const req: AgentCreateRequestMain = {
    type: 'Main',
    name,
    color: payload.color,
    icon: payload.icon,
    soul,
  }
  if (description) req.description = description
  if (payload.model.trim()) req.model = payload.model.trim()
  if (payload.provider?.trim()) req.provider = payload.provider.trim()
  if (payload.voice !== undefined && payload.voice !== '') req.voice = payload.voice
  const toolsCfg = resolveToolsCfg(payload.tools_cfg, tools)
  if (toolsCfg !== undefined) req.tools_cfg = toolsCfg
  if (payload.skills !== undefined) req.skills = payload.skills
  if (payload.fallback_models !== undefined) req.fallback_models = payload.fallback_models
  if (payload.model_params !== undefined) req.model_params = payload.model_params
  if (payload.shell_policy !== undefined) req.shell_policy = payload.shell_policy
  if (payload.rate_limits !== undefined) req.rate_limits = payload.rate_limits
  if (payload.timeout_seconds !== undefined) req.timeout_seconds = payload.timeout_seconds
  if (payload.max_tool_iterations !== undefined) req.max_tool_iterations = payload.max_tool_iterations
  return req
}

export function CreateAgentModal({
  open: openProp,
  onClose: onCloseProp,
  onCreate: onCreateProp,
  initialType,
  initialCli,
}: CreateAgentModalProps) {
  const {
    createAgentModalOpen,
    createAgentModalType,
    createAgentModalCli,
    closeCreateAgentModal,
    addToast,
  } = useUiStore()
  const queryClient = useQueryClient()

  const isOpen = openProp !== undefined ? openProp : createAgentModalOpen
  const handleClose = onCloseProp ?? closeCreateAgentModal

  // Store-driven path: openCreateAgentModal('Main' | 'Subagent' | 'subagent_3p').
  // Test path: legacy props map 'custom'/'worker' to the new wire enum.
  const effectiveType: WizardType = normalizeWizardType(
    openProp !== undefined ? initialType : createAgentModalType,
    'Main',
  )

  // Store-driven CLI lock (for roster-launched external wizards); explicit
  // prop wins over the store so callers can pin the CLI per-test.
  const effectiveCli: WizardCli | undefined =
    initialCli ?? (createAgentModalCli as WizardCli | undefined)

  const createAgentMutation = useMutation<Agent | null, Error, AgentCreateRequest>({
    mutationFn: (data) =>
      onCreateProp
        ? // Legacy / test prop returns Promise<void> — it never hands back a
          // server-computed Agent (id, timestamps, resolved defaults, etc.),
          // so the mutation result is `null` for this path. Do NOT cast the
          // request payload to Agent: it only shares a few field *names*
          // with the response entity and callers must not assume any
          // server-computed field is present.
          onCreateProp(data).then(() => null)
        : createAgent(data),
    onSuccess: (agent, variables) => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({
        // Real create path: use the server-echoed name. Legacy path (agent
        // is null, no server entity was returned): fall back to the name
        // from the submitted request — the only field guaranteed to exist.
        message: `Created agent "${agent ? agent.name : variables.name}"`,
        variant: 'success',
      })
      handleClose()
    },
    onError: (err: unknown) => {
      // The wizard surfaces the same error inline (role=alert). Firing a
      // toast here is the only place the user gets a global notification,
      // since the wizard's catch only sets state (no toast — the parent
      // owns the lifecycle). See CreateAgentWizard.handleSubmit.
      const message = getErrorMessage(err, 'Failed to create agent')
      addToast({ message, variant: 'error' })
    },
  })

  // Providers / tools / skills — fetched at the modal level (which sits
  // inside QueryClientProvider) and forwarded as props so the wizard
  // sub-components stay query-client-free and unit-testable.
  const providersQuery = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const toolsQuery = useQuery({ queryKey: ['registry-tools'], queryFn: fetchRegistryTools })
  const skillsQuery = useQuery({ queryKey: ['skills'], queryFn: fetchSkills })
  // Global tool policy — forwarded to Step 3 so it can lock per-agent controls
  // that would contradict a global deny/ask (no contradicting configs).
  const globalPoliciesQuery = useQuery({ queryKey: ['global-tool-policies'], queryFn: fetchGlobalToolPolicies })

  const connectedProviders = (providersQuery.data ?? []).filter(
    (p) => p.status === 'connected',
  )
  const registryTools = toolsQuery.data ?? []
  const skills = skillsQuery.data ?? []
  const globalPolicies = globalPoliciesQuery.data
    ? { policies: globalPoliciesQuery.data.policies ?? {} }
    : undefined

  // Bug: the model picker collapsed four distinct provider-catalog states
  // (query loading / query failed / provider connected but its model
  // catalogue came back empty / genuinely no provider connected) into one
  // "connect a provider in Settings" message — true for only the last
  // case. Motivating incident: CI logged the gateway's upstream fetch to
  // openrouter.ai failing nine times with `context canceled` and zero
  // successes (the /providers endpoint itself was healthy — a direct curl
  // from the same worker returned 200 in 0.46s), and the picker still told
  // the user no provider was connected. `providersLoading` /
  // `providersError` let Step 1 tell those apart; the "connected but empty
  // catalogue" case is derived from each `Provider.warning` field
  // (already on the wire — see `pkg/gateway/rest.go`'s providers-list
  // handler) directly in Step1Identity, no extra fetch needed.
  const providersLoading = providersQuery.isLoading || providersQuery.isFetching
  const providersError = providersQuery.isError
    ? getErrorMessage(providersQuery.error, 'Failed to load providers')
    : undefined
  const refetchProviders = providersQuery.refetch
  const handleRetryProviders = useCallback(() => {
    void refetchProviders()
  }, [refetchProviders])

  // Dev-time drift guard: once the full tool registry resolves, warn if any
  // role-preset override key (toolPolicyPresets.ts) doesn't match a real
  // tool name — the exact class of bug that shipped with the dotted
  // `browser.navigate` etc. override keys (fixed 2026-07-11; see
  // toolPolicyPresets.ts's Balanced preset comment). Console-only, dev
  // builds only — never throws, never blocks the wizard.
  useEffect(() => {
    if (!import.meta.env.DEV || registryTools.length === 0) return
    const orphaned = findOrphanedPresetOverrideKeys(registryTools)
    if (orphaned.length > 0) {
      console.warn(
        '[toolPolicyPresets] override key(s) do not match any tool in the live registry ' +
          '(silently fall through to defaultPolicy instead of applying):',
        orphaned,
      )
    }
  }, [registryTools])

  // `registryTools` also feeds `resolveToolsCfg` (see payloadToCreateRequest)
  // so the tools_cfg committed on submit is built from the SAME catalog the
  // Tools step rendered its Balanced-preset default from.
  const handleSubmit = useCallback(
    async (payload: WizardSubmitPayload) => {
      const req = payloadToCreateRequest(payload, registryTools)
      await createAgentMutation.mutateAsync(req)
    },
    [createAgentMutation, registryTools],
  )

  if (!isOpen) return null

  return (
    <CreateAgentWizard
      initialType={effectiveType}
      {...(effectiveCli ? { initialCli: effectiveCli } : {})}
      onSubmit={handleSubmit}
      onClose={handleClose}
      connectedProviders={connectedProviders}
      registryTools={registryTools}
      skills={skills}
      globalPolicies={globalPolicies}
      providersLoading={providersLoading}
      providersError={providersError}
      onRetryProviders={handleRetryProviders}
    />
  )
}

export default CreateAgentModal
