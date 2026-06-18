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

import * as React from 'react'
import { useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { useUiStore } from '@/store/ui'
import { createAgent, isApiError } from '@/lib/api'
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

/** Convert the wizard's submit payload to a wire AgentCreateRequest. */
function payloadToCreateRequest(
  payload: WizardSubmitPayload,
): AgentCreateRequest {
  const req: AgentCreateRequest = {
    type: payload.type,
    name: payload.name,
    description: payload.description,
    color: payload.color,
    icon: payload.icon,
    model: payload.model,
    soul: payload.soul,
    instructions: payload.instructions || '',
  }
  if (payload.heartbeat !== undefined) req.heartbeat = payload.heartbeat
  if (payload.heartbeat_enabled !== undefined) req.heartbeat_enabled = payload.heartbeat_enabled
  if (payload.heartbeat_interval !== undefined) req.heartbeat_interval = payload.heartbeat_interval
  if (payload.voice !== undefined) req.voice = payload.voice
  if (payload.tools_cfg !== undefined) req.tools_cfg = payload.tools_cfg as AgentCreateRequest['tools_cfg']
  if (payload.skills !== undefined) req.skills = payload.skills
  if (payload.fallback_models !== undefined) {
    req.fallback_models = payload.fallback_models as AgentCreateRequest['fallback_models']
  }
  if (payload.cli || payload.executor_cli_path || payload.executor_env_overrides || payload.executor_cli_args) {
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

  const createAgentMutation = useMutation({
    mutationFn: onCreateProp ?? ((data: AgentCreateRequest) => createAgent(data)),
    onSuccess: (agent: Agent) => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({
        id: `agent-created-${agent.id}`,
        message: `Created agent "${agent.name}"`,
        variant: 'success',
      })
      handleClose()
    },
    onError: (err: unknown) => {
      const message = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Failed to create agent'
      addToast({
        id: 'agent-create-error',
        message,
        variant: 'error',
      })
    },
  })

  const handleSubmit = useCallback(
    async (payload: WizardSubmitPayload) => {
      const req = payloadToCreateRequest(payload)
      await createAgentMutation.mutateAsync(req)
    },
    [createAgentMutation],
  )

  if (!isOpen) return null

  return (
    <CreateAgentWizard
      initialType={effectiveType}
      {...(initialCli ? { initialCli } : {})}
      onSubmit={handleSubmit}
    />
  )
}

export default CreateAgentModal
