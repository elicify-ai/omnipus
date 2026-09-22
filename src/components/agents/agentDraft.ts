import type { Agent, AgentUpdateRequest } from '@/lib/api'

export class ProtectedAgentFieldChangeError extends Error {
  constructor(readonly field: string, reason?: string) {
    super(reason ? `${field}: ${reason}` : `${field} is not editable`)
    this.name = 'ProtectedAgentFieldChangeError'
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

function normalizedBaselineValue(baseline: Agent, field: string): unknown {
  const value = baseline[field as keyof Agent]
  if ((field === 'skills' || field === 'mcp_servers' || field === 'fallback_models') && value === undefined) return []
  if ((field === 'description' || field === 'model') && value === undefined) return ''
  if (field === 'provider' && value === undefined) return ''
  if (field === 'icon' && value === undefined) return 'Robot'
  if (field === 'default' && value === undefined) return false
  if (field === 'voice' && value === undefined) return null
  if (field === 'model_params' && value === undefined) return { temperature: 1, max_tokens: 4096 }
  return value
}

function sameValue(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}
