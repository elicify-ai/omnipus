import type { Agent, AgentUpdateRequest, ExecutorConfig } from '@/lib/api'
import type { ExecutorConfigUpdate } from '@/lib/api/generated/openapi-types'
import { ENV_VALUE_MASK } from './approvalPreviews/AgentChangeApprovalPreview'

export class ProtectedAgentFieldChangeError extends Error {
  constructor(readonly field: string, reason?: string) {
    super(reason ? `${field}: ${reason}` : `${field} is not editable`)
    this.name = 'ProtectedAgentFieldChangeError'
  }
}

/** A changed env row carries the read mask (a masked row renamed, or the
 *  mask typed as a value). The server refuses the mask and the real value is
 *  not known here, so the save stops and asks for the value instead. */
export class MaskedEnvValueError extends Error {
  constructor(readonly envName: string) {
    super(`Enter the value for ${envName} again: a hidden value cannot be saved under a new name.`)
    this.name = 'MaskedEnvValueError'
  }
}

export function buildAgentUpdate(
  baseline: Agent,
  draft: Record<string, unknown>,
): AgentUpdateRequest | null {
  const descriptors = new Map(
    (baseline.editable_fields ?? []).map((descriptor) => [descriptor.name, descriptor]),
  )
  const changes: Record<string, unknown> = {}

  for (const [field, draftValue] of Object.entries(draft)) {
    if (draftValue === undefined) continue
    if (field === 'executor' && baseline.type === 'subagent_3p') {
      const executorUpdate = buildExecutorUpdate(baseline.executor, draftValue as ExecutorConfig)
      if (!executorUpdate) continue
      // Each part checks its own descriptor (Agent.yaml editable_fields:
      // executor.env_overrides, executor.env_overrides_remove,
      // executor.cli_args), so an arguments-only save is not refused by an
      // environment descriptor, nor the reverse.
      for (const part of Object.keys(executorUpdate)) {
        const name = `executor.${part}`
        const descriptor = descriptors.get(name)
        if (!descriptor?.editable) {
          throw new ProtectedAgentFieldChangeError(name, descriptor?.reason)
        }
      }
      changes[field] = executorUpdate
      continue
    }
    const baselineValue = normalizedBaselineValue(baseline, field)
    if (sameValue(draftValue, baselineValue)) continue

    const descriptor = descriptors.get(field)
    if (!descriptor?.editable) {
      throw new ProtectedAgentFieldChangeError(field, descriptor?.reason)
    }
    changes[field] = draftValue
  }

  if (Object.keys(changes).length === 0) return null
  return { revision: baseline.revision, ...changes } as AgentUpdateRequest
}

/**
 * The executor write for a third-party (subagent_3p) worker, in the
 * ExecutorConfigUpdate shape (T11 FR-8; contracts ExecutorConfigUpdate.yaml).
 * The editor holds the READ model (kind, cli, effective_* and every env value
 * replaced by the mask); the server refuses kind/cli/effective_* and the mask,
 * so only what the person changed is sent:
 * - env_overrides: names added, or whose value differs from the read value.
 *   An untouched masked value equals its read value and is never re-sent.
 *   A row with no name yet is left out until it is named (it stays in the
 *   editor). A changed row carrying the mask is never sent: it throws
 *   MaskedEnvValueError so the person re-enters the value (dropping it would
 *   lose the value when the old name is removed).
 * - env_overrides_remove: names in the read model that the draft no longer has.
 * - cli_args: the draft's arguments when they differ ('' clears them).
 * The saved program path and preset are Settings-owned and never sent.
 * Returns null when nothing writable changed.
 */
export function buildExecutorUpdate(
  baseline: ExecutorConfig | undefined,
  draft: ExecutorConfig | undefined,
): ExecutorConfigUpdate | null {
  if (!draft) return null
  const baselineEnv = baseline?.env_overrides ?? {}
  const draftEnv = draft.env_overrides ?? {}
  const update: ExecutorConfigUpdate = {}

  const set: Record<string, string> = {}
  for (const [name, value] of Object.entries(draftEnv)) {
    if (name === '') continue
    if (Object.hasOwn(baselineEnv, name) && baselineEnv[name] === value) continue
    if (value === ENV_VALUE_MASK) throw new MaskedEnvValueError(name)
    set[name] = value
  }
  if (Object.keys(set).length > 0) update.env_overrides = set

  const remove = Object.keys(baselineEnv).filter((name) => !Object.hasOwn(draftEnv, name))
  if (remove.length > 0) update.env_overrides_remove = remove

  const draftArgs = draft.cli_args ?? ''
  if (draftArgs !== (baseline?.cli_args ?? '')) update.cli_args = draftArgs

  return Object.keys(update).length > 0 ? update : null
}

function normalizedBaselineValue(baseline: Agent, field: string): unknown {
  const value = baseline[field as keyof Agent]
  if ((field === 'skills' || field === 'mcp_servers' || field === 'fallback_models') && value === undefined) return []
  if ((field === 'description' || field === 'model') && value === undefined) return ''
  if (field === 'provider' && value === undefined) return ''
  if (field === 'icon' && value === undefined) return 'Robot'
  if (field === 'default' && value === undefined) return false
  if (field === 'voice' && value === undefined) return null
  if (field === 'model_params' && value === undefined) return { temperature: 1, max_tokens: 4096 }
  // #904: Agent.max_tool_iterations is the server's EFFECTIVE limit; the
  // draft edits the agent's OWN value, so diff against the stored own value
  // (null = none). Saving exactly the global when the agent rides it is a
  // real change (source becomes "agent"), and reset diffs null vs own value.
  if (field === 'max_tool_iterations') return baseline.max_tool_iterations_override ?? null
  return value
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}
