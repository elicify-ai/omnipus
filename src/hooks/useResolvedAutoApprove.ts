import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { fetchSandboxStatus } from '@/lib/api'

export interface ResolvedAutoApprove {
  /** The effective Auto-approve state for the active chat, folding both scopes (chat modifier, else global). */
  resolved: boolean
  /**
   * SandboxStatus.kernel_sandbox_active — whether a kernel sandbox is
   * currently enforcing on this platform. Founder decision (2026-09-24):
   * this is WARNING-ONLY. Auto works for every tool, shell included, on
   * every platform (Windows too), whether or not the sandbox is enforcing —
   * this flag no longer gates `resolved` or switches Auto off. A caller
   * (the composer Auto button) uses it only to decide whether to show a caution
   * that shell commands, with no sandbox to check them against, are cleared
   * by reading the command text only.
   */
  kernelSandboxActive: boolean
  /**
   * SandboxStatus.god_mode_active — the global God Mode override. When
   * true, every agent's tool policy is floored at "allow" regardless of
   * Auto-approve state; a caller (the composer Auto button) must check this FIRST
   * and stay pressed, never fall through to off — God Mode is not
   * the absence of Auto, it is a stronger floor than Auto.
   */
  godModeActive: boolean
  /** Whether the active session is a real, server-known session (not '__pending' or none). */
  hasRealSession: boolean
}

/**
 * useResolvedAutoApprove — the single source of ADR-092's Auto-approve
 * resolution, shared by the composer's per-chat toggle (AutoApprovePicker)
 * so every surface reads the same underlying state. Agreement is NOT
 * automatic just from sharing this hook, though: `godModeActive` is a
 * stronger floor than `resolved` (see its own field doc below) and every
 * caller must check it FIRST. A caller that reads only `resolved` and ignores
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
 * the composer toggle work before the first message) → otherwise the global
 * default, `SandboxStatus.auto_approve_effective`. Two levels only: the chat
 * modifier, else the global setting (a helper inherits its parent's).
 * `SandboxStatus` carries no session context (per its own schema
 * description), so folding the chat scope in is explicitly the SPA's job,
 * done here once.
 *
 * Founder decision (2026-09-24): Auto-approve no longer requires an
 * enforcing kernel sandbox — it is effective for every tool on every
 * platform regardless of `kernel_sandbox_active`. `kernelSandboxActive` is
 * exposed purely so a caller can WARN ("no kernel sandbox — shell commands
 * ask first, except read-only ones and commands an operator rule allows");
 * it must never be folded into `resolved`. `godModeActive` remains a
 * separate, stronger floor a caller must check ahead of `resolved`.
 */
export function useResolvedAutoApprove(): ResolvedAutoApprove {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sessionOverride = useChatStore((s) => s.autoApproveEffective)
  const pendingChoice = useChatStore((s) => s.pendingAutoApproveChoice)

  const { data: sandboxStatus } = useQuery({ queryKey: ['sandbox-status'], queryFn: fetchSandboxStatus })

  const globalEffective = sandboxStatus?.auto_approve_effective === true
  const hasSessionOverride = sessionOverride !== null && sessionOverride !== undefined
  const resolved = hasSessionOverride
    ? sessionOverride
    : pendingChoice !== null
      ? pendingChoice
      : globalEffective

  const hasRealSession = !!activeSessionId && activeSessionId !== '__pending'

  return {
    resolved,
    kernelSandboxActive: sandboxStatus?.kernel_sandbox_active === true,
    godModeActive: sandboxStatus?.god_mode_active === true,
    hasRealSession,
  }
}
