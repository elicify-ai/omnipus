import { Badge } from '@/components/ui/badge'
import { Tooltip } from '@/components/ui/tooltip'
import { useResolvedAutoApprove } from '@/hooks/useResolvedAutoApprove'

/**
 * ChatModeBadge — chat-header indicator of the active chat's resolved
 * ADR-092 permission state.
 *
 * Founder decision (2026-09-24): Auto-approve no longer requires an
 * enforcing kernel sandbox — Auto works for every tool, shell included, on
 * every platform (Windows too), whether or not the sandbox is enforcing.
 * `kernel_sandbox_active` is now WARNING-ONLY, never a switch back to "Ask".
 * Four renderings:
 *
 *   God Mode active                         → "God Mode" — a stronger floor
 *                                               than Auto/Ask; checked FIRST,
 *                                               whatever the other two say
 *                                               (SandboxStatus's own contract).
 *   Auto-approve off                        → "Ask"
 *   Auto-approve on,  kernel sandbox active  → "Auto"
 *   Auto-approve on,  no kernel sandbox      → "Auto — no sandbox", a caution
 *                                               state (still fully Auto —
 *                                               safe tool calls still run
 *                                               without asking), with a
 *                                               tooltip explaining that shell
 *                                               commands are checked by
 *                                               reading the command text only.
 *
 * Reads the same `useResolvedAutoApprove` the composer's AutoApprovePicker
 * does, so the two can never disagree about the chat's current state.
 */
export function ChatModeBadge({ className }: { className?: string }) {
  const { resolved, kernelSandboxActive, godModeActive } = useResolvedAutoApprove()

  if (godModeActive) {
    // error variant, matching GodModeActiveBanner's own red styling — God
    // Mode is the highest-risk state this badge can report, not a neutral
    // status like Ask/Auto.
    return (
      <Badge variant="error" className={className} data-testid="chat-mode-badge">
        God Mode
      </Badge>
    )
  }

  if (!resolved) {
    return (
      <Badge variant="secondary" className={className} data-testid="chat-mode-badge">
        Ask
      </Badge>
    )
  }

  if (kernelSandboxActive) {
    return (
      <Badge variant="secondary" className={className} data-testid="chat-mode-badge">
        Auto
      </Badge>
    )
  }

  return (
    <Tooltip
      data-testid="chat-mode-badge-trigger"
      content="No kernel sandbox is enforcing. Safe tool calls still run without asking, but shell commands are checked by reading the command text only."
    >
      <Badge variant="warning" className={className} data-testid="chat-mode-badge">
        Auto — no sandbox
      </Badge>
    </Tooltip>
  )
}
