import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { fetchAgents, fetchSandboxStatus } from '@/lib/api'

export interface ResolvedAutoApprove {
  /** The effective Auto-approve state for the active chat, folding all three scopes. */
  resolved: boolean
  /**
   * SandboxStatus.kernel_sandbox_active — whether a kernel sandbox is
   * currently enforcing on this platform. Founder decision (2026-09-24):
   * this is WARNING-ONLY. Auto works for every tool, shell included, on
   * every platform (Windows too), whether or not the sandbox is enforcing —
   * this flag no longer gates `resolved` or switches Auto off. A caller
   * (e.g. ChatModeBadge) uses it only to decide whether to show a caution
   * that shell commands, with no sandbox to check them against, are cleared
   * by reading the command text only.
   */
  kernelSandboxActive: boolean
  /**
   * SandboxStatus.god_mode_active — the global God Mode override. When
   * true, every agent's tool policy is floored at "allow" regardless of
   * Auto-approve state; a caller (e.g. ChatModeBadge) must check this FIRST
   * and render "God Mode", never fall through to "Ask" — God Mode is not
   * the absence of Auto, it is a stronger floor than Auto.
   */
  godModeActive: boolean
  /** Whether the active session is a real, server-known session (not '__pending' or none). */
  hasRealSession: boolean
}

/**
 * useResolvedAutoApprove — the single source of ADR-092's Auto-approve
 * resolution, shared by the composer's per-chat toggle (AutoApprovePicker)
 * and the chat-header mode badge (ChatModeBadge) so both read the same
 * underlying state. Agreement is NOT automatic just from sharing this hook,
 * though: `godModeActive` is a stronger floor than `resolved` (see its own
 * field doc below) and every caller must check it FIRST, exactly as
 * ChatModeBadge does. A caller that reads only `resolved` and ignores
 * `godModeActive` — AutoApprovePicker used to be exactly this caller — can
 * still show state that visibly contradicts one that checks both, even
 * though both are reading the same hook.
 *
 * Resolution order (most specific wins): this session's own
 * `autoApproveEffective` (once a `session_mode_updated` ack has arrived, or
 * SessionStateFrame.auto_approve_modifier survived a reload/reconnect) →
 * otherwise, with no real session yet, a `pendingAutoApproveChoice` the user
 * already made in the composer for this not-yet-created chat (see
 * `ChatStore.pendingAutoApproveChoice`'s doc comment — the UX fix that lets
 * the composer toggle work before the first message) → otherwise the agent x
 * global resolution — `SandboxStatus.auto_approve_effective` (the gateway's
 * global default), floored off if the active agent sets
 * `auto_approve_disabled`. `SandboxStatus` carries no session/agent context
 * (per its own schema description), so folding those narrower scopes in is
 * explicitly the SPA's job, done here once.
 *
 * Founder decision (2026-09-24): Auto-approve no longer requires an
 * enforcing kernel sandbox — it is effective for every tool on every
 * platform regardless of `kernel_sandbox_active`. `kernelSandboxActive` is
 * exposed purely so a caller can WARN ("no kernel sandbox — shell commands
 * are checked by reading the command text only"); it must never be folded
 * into `resolved`. `godModeActive` remains a separate, stronger floor a
 * caller must check ahead of `resolved`.
 */
export function useResolvedAutoApprove(): ResolvedAutoApprove {
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionOverride = useChatStore((s) => s.autoApproveEffective)
  const pendingChoice = useChatStore((s) => s.pendingAutoApproveChoice)

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const { data: sandboxStatus } = useQuery({ queryKey: ['sandbox-status'], queryFn: fetchSandboxStatus })

  const activeAgent = agents.find((a) => a.id === activeAgentId)
  const agentForcesOff = activeAgent?.auto_approve_disabled === true
  const globalEffective = sandboxStatus?.auto_approve_effective === true
  const inheritedResolved = agentForcesOff ? false : globalEffective
  const hasSessionOverride = sessionOverride !== null && sessionOverride !== undefined
  const resolved = hasSessionOverride
    ? sessionOverride
    : pendingChoice !== null
      ? pendingChoice
      : inheritedResolved

  const hasRealSession = !!activeSessionId && activeSessionId !== '__pending'

  return {
    resolved,
    kernelSandboxActive: sandboxStatus?.kernel_sandbox_active === true,
    godModeActive: sandboxStatus?.god_mode_active === true,
    hasRealSession,
  }
}
