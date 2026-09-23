import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { fetchAgents, fetchSandboxStatus } from '@/lib/api'

export interface ResolvedAutoApprove {
  /** The effective Auto-approve state for the active chat, folding all three scopes. */
  resolved: boolean
  /** SandboxStatus.kernel_sandbox_active — the platform predicate. */
  kernelSandboxActive: boolean
  /** Whether the active session is a real, server-known session (not '__pending' or none). */
  hasRealSession: boolean
}

/**
 * useResolvedAutoApprove — the single source of ADR-091's Auto-approve
 * resolution, shared by the composer's per-chat toggle (AutoApprovePicker)
 * and the chat-header mode badge so the two can never disagree.
 *
 * Resolution order (most specific wins): this session's own
 * `autoApproveEffective` (once a `session_mode_updated` ack has arrived) →
 * otherwise the agent x global resolution — `SandboxStatus.auto_approve_effective`
 * (the gateway's global default), floored off if the active agent sets
 * `auto_approve_disabled`. `SandboxStatus` carries no session/agent context
 * (per its own schema description), so folding those two narrower scopes in
 * is explicitly the SPA's job, done here once.
 */
export function useResolvedAutoApprove(): ResolvedAutoApprove {
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionOverride = useChatStore((s) => s.autoApproveEffective)

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const { data: sandboxStatus } = useQuery({ queryKey: ['sandbox-status'], queryFn: fetchSandboxStatus })

  const activeAgent = agents.find((a) => a.id === activeAgentId)
  const agentForcesOff = activeAgent?.auto_approve_disabled === true
  const globalEffective = sandboxStatus?.auto_approve_effective === true
  const inheritedResolved = agentForcesOff ? false : globalEffective
  const resolved = sessionOverride !== null && sessionOverride !== undefined ? sessionOverride : inheritedResolved

  const hasRealSession = !!activeSessionId && activeSessionId !== '__pending'

  return {
    resolved,
    kernelSandboxActive: sandboxStatus?.kernel_sandbox_active === true,
    hasRealSession,
  }
}
