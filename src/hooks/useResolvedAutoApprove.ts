import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { fetchAgents, fetchSandboxStatus } from '@/lib/api'

export interface ResolvedAutoApprove {
  /** The effective Auto-approve state for the active chat, folding all three scopes. */
  resolved: boolean
  /** SandboxStatus.kernel_sandbox_active — the platform predicate. */
  kernelSandboxActive: boolean
  /**
   * SandboxStatus.god_mode_active — the global God Mode override. When
   * true, every agent's tool policy is floored at "allow" regardless of
   * Auto-approve/kernel-sandbox state; a caller (e.g. ChatModeBadge) must
   * check this FIRST and render "God Mode", never fall through to "Ask" —
   * God Mode is not the absence of Auto, it is a stronger floor than Auto.
   */
  godModeActive: boolean
  /** Whether the active session is a real, server-known session (not '__pending' or none). */
  hasRealSession: boolean
}

/**
 * useResolvedAutoApprove — the single source of ADR-092's Auto-approve
 * resolution, shared by the composer's per-chat toggle (AutoApprovePicker)
 * and the chat-header mode badge so the two can never disagree.
 *
 * Resolution order (most specific wins): this session's own
 * `autoApproveEffective` (once a `session_mode_updated` ack has arrived, or
 * SessionStateFrame.auto_approve_modifier survived a reload/reconnect) →
 * otherwise the agent x global resolution — `SandboxStatus.auto_approve_effective`
 * (the gateway's global default), floored off if the active agent sets
 * `auto_approve_disabled`. `SandboxStatus` carries no session/agent context
 * (per its own schema description), so folding those two narrower scopes in
 * is explicitly the SPA's job, done here once. `resolved` and
 * `kernelSandboxActive` together answer "does Auto actually clear anything
 * right now" (SandboxStatus's own contract: Auto only takes effect with a
 * kernel sandbox present AND god_mode_active false) — `godModeActive` is a
 * separate, stronger floor a caller must check ahead of both.
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
    godModeActive: sandboxStatus?.god_mode_active === true,
    hasRealSession,
  }
}
