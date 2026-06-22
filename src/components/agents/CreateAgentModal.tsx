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
  fetchProviders,
  fetchRegistryTools,
  fetchSkills,
  isApiError,
} from '@/lib/api'
import type { Agent, AgentCreateRequest } from '@/lib/api'

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
 *  - Description / instructions are omitted when empty (Main agents
 *    treat them as optional; the server schema accepts `undefined`).
 *  - Executor block is only emitted when at least one of cli / path /
 *    env_overrides / cli_args is set; the server defaults `kind` to
 *    `native` for non-External agents when the block is absent.
 *  - Tools_cfg / fallback_models are forwarded as-is (the wizard
 *    types them properly, so no `as` cast is required here).
 *  - All string fields are `.trim()`-ed to match the wizard's step
 *    gating (which validates `.trim().length > 0`).
 */
function payloadToCreateRequest(
  payload: WizardSubmitPayload,
): AgentCreateRequest {
  // UAT 4a: native subagents that inherit a field from the caller omit it
  // from the create request so the server keeps the inherited rail. These
  // flags are UI-only and never cross the wire. They apply only to native
  // Subagents (Main never inherits; external subagents run their own config).
  const isNativeSubagent = payload.type === 'Subagent'
  const inheritModel = isNativeSubagent && payload.inherit_model === true
  const inheritTools = isNativeSubagent && payload.inherit_tools === true
  const inheritSkills = isNativeSubagent && payload.inherit_skills === true
  const inheritSandbox = isNativeSubagent && payload.inherit_sandbox === true

  const req: AgentCreateRequest = {
    type: payload.type,
    name: payload.name.trim(),
    color: payload.color,
    icon: payload.icon,
    soul: payload.soul.trim(),
  }
  // Model omitted when inherited (server falls back to the caller/global model).
  if (!inheritModel && payload.model.trim()) req.model = payload.model.trim()
  if (payload.description.trim()) req.description = payload.description.trim()
  if (payload.instructions.trim()) req.instructions = payload.instructions.trim()
  // O3 two-field: forward provider only when non-empty and not inheriting model.
  if (!inheritModel && payload.provider && payload.provider.trim() !== '') req.provider = payload.provider.trim()
  if (payload.heartbeat !== undefined) req.heartbeat = payload.heartbeat
  if (payload.heartbeat_enabled !== undefined) req.heartbeat_enabled = payload.heartbeat_enabled
  if (payload.heartbeat_interval !== undefined) req.heartbeat_interval = payload.heartbeat_interval
  if (payload.voice !== undefined && payload.voice !== '') req.voice = payload.voice
  // Tools / Skills / Sandbox omitted when inherited so the server keeps the
  // caller's rail (UAT 4a). Fallback models ride with the primary model.
  if (!inheritTools && payload.tools_cfg !== undefined) req.tools_cfg = payload.tools_cfg
  if (!inheritSkills && payload.skills !== undefined) req.skills = payload.skills
  if (!inheritModel && payload.fallback_models !== undefined) req.fallback_models = payload.fallback_models
  if (payload.model_params !== undefined) req.model_params = payload.model_params
  if (!inheritSandbox && payload.sandbox_profile !== undefined) req.sandbox_profile = payload.sandbox_profile
  if (payload.shell_policy !== undefined) req.shell_policy = payload.shell_policy
  if (payload.rate_limits !== undefined) req.rate_limits = payload.rate_limits
  if (payload.timeout_seconds !== undefined) req.timeout_seconds = payload.timeout_seconds
  if (payload.max_tool_iterations !== undefined) req.max_tool_iterations = payload.max_tool_iterations
  if (payload.steering_mode !== undefined) req.steering_mode = payload.steering_mode

  // Executor block. Subagents default to the in-process native runner when
  // the user has not configured an external CLI. External agents must set
  // kind='external-cli' and a CLI path.
  const hasExecutorFields = payload.cli || payload.executor_cli_path || payload.executor_env_overrides || payload.executor_cli_args
  if (payload.type === 'Subagent' && !hasExecutorFields) {
    req.executor = { kind: 'native' }
  } else if (hasExecutorFields) {
    req.executor = {
      kind: payload.type === 'subagent_3p' ? 'external-cli' : 'native',
    }
    if (payload.cli) req.executor.cli = payload.cli
    if (payload.executor_cli_path) req.executor.cli_path = payload.executor_cli_path
    if (payload.executor_env_overrides) req.executor.env_overrides = payload.executor_env_overrides
    if (payload.executor_cli_args) req.executor.cli_args = payload.executor_cli_args
  }
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

  const createAgentMutation = useMutation<Agent, Error, AgentCreateRequest>({
    mutationFn: (data) =>
      onCreateProp
        ? // Legacy / test prop returns Promise<void>; synthesize an Agent
          // with the wire fields so the success path can still fire
          // (toast, query invalidation, close).
          onCreateProp(data).then(() => data as unknown as Agent)
        : createAgent(data),
    onSuccess: (agent: Agent) => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({
        message: `Created agent "${agent.name}"`,
        variant: 'success',
      })
      handleClose()
    },
    onError: (err: unknown) => {
      // The wizard surfaces the same error inline (role=alert). Firing a
      // toast here is the only place the user gets a global notification,
      // since the wizard's catch only sets state (no toast — the parent
      // owns the lifecycle). See CreateAgentWizard.handleSubmit.
      const message = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Failed to create agent'
      addToast({ message, variant: 'error' })
    },
  })

  const handleSubmit = useCallback(
    async (payload: WizardSubmitPayload) => {
      const req = payloadToCreateRequest(payload)
      await createAgentMutation.mutateAsync(req)
    },
    [createAgentMutation],
  )

  // Providers / tools / skills — fetched at the modal level (which sits
  // inside QueryClientProvider) and forwarded as props so the wizard
  // sub-components stay query-client-free and unit-testable.
  const providersQuery = useQuery({ queryKey: ['providers'], queryFn: fetchProviders })
  const toolsQuery = useQuery({ queryKey: ['registry-tools'], queryFn: fetchRegistryTools })
  const skillsQuery = useQuery({ queryKey: ['skills'], queryFn: fetchSkills })

  const connectedProviders = (providersQuery.data ?? []).filter(
    (p) => p.status === 'connected',
  )
  const registryTools = toolsQuery.data ?? []
  const skills = skillsQuery.data ?? []

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
    />
  )
}

export default CreateAgentModal
