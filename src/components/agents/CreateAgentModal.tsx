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

import { useCallback } from 'react'
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
import { applyRolePreset } from '@/lib/toolPolicyPresets'

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

/**
 * The Tools step (Step3Tools.tsx) DISPLAYS a Balanced-preset default before
 * the user has touched the per-tool editor — "something concrete to render"
 * — but only ever commits `payload.tools_cfg` when the operator genuinely
 * interacts with `ToolPolicyEditor` (a preset button or an individual
 * allow/ask/deny toggle). If the operator submits without touching Step 3,
 * `payload.tools_cfg` is still `undefined` here.
 *
 * We MUST commit the same Balanced-preset policy that was displayed in that
 * case — otherwise omitting `tools_cfg` from the request lets the backend's
 * `NewCustomAgentToolsCfg()` seed apply instead (deny-everything except a
 * narrow read-only allow-list), silently far more restrictive than what the
 * wizard showed the operator. `applyRolePreset('balanced', tools)` is the
 * exact same call Step3Tools.tsx's `toolPolicyValue()` makes for its
 * uncommitted-default render path — single source of truth in
 * `@/lib/toolPolicyPresets`, not a re-derived literal.
 *
 * Only applies to Main / Subagent (native agents governed by Omnipus's own
 * tool-policy mechanism). subagent_3p never has this problem: its variant
 * schema (`AgentCreateRequestSubagent3p`, `additionalProperties: false`)
 * does not carry `tools_cfg` at all — the external CLI runs its own tool
 * loop and never reaches the Tools step (2-step wizard for that type).
 */
function defaultToolsCfg(
  payload: WizardSubmitPayload,
  tools: RegistryTool[],
): NonNullable<AgentCreateRequestMain['tools_cfg']> {
  return payload.tools_cfg !== undefined
    ? payload.tools_cfg
    : { builtin: applyRolePreset('balanced', tools) }
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
 *  - Main / Subagent (when not inheriting tools) always send `tools_cfg` —
 *    see `defaultToolsCfg` above for why an untouched Tools step must still
 *    commit the displayed Balanced-preset default rather than omit the
 *    field.
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
    if (!inheritTools) req.tools_cfg = defaultToolsCfg(payload, tools)
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
  req.tools_cfg = defaultToolsCfg(payload, tools)
  if (payload.skills !== undefined) req.skills = payload.skills
  if (payload.fallback_models !== undefined) req.fallback_models = payload.fallback_models
  if (payload.model_params !== undefined) req.model_params = payload.model_params
  if (payload.shell_policy !== undefined) req.shell_policy = payload.shell_policy
  if (payload.rate_limits !== undefined) req.rate_limits = payload.rate_limits
  if (payload.timeout_seconds !== undefined) req.timeout_seconds = payload.timeout_seconds
  if (payload.max_tool_iterations !== undefined) req.max_tool_iterations = payload.max_tool_iterations
  if (payload.steering_mode !== undefined) req.steering_mode = payload.steering_mode
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

  // `registryTools` also feeds `defaultToolsCfg` (see payloadToCreateRequest)
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
    />
  )
}

export default CreateAgentModal
