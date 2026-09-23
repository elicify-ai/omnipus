import { Badge } from '@/components/ui/badge'
import { Tooltip } from '@/components/ui/tooltip'
import { useResolvedAutoApprove } from '@/hooks/useResolvedAutoApprove'

/**
 * ChatModeBadge — chat-header indicator of the active chat's resolved
 * ADR-092 permission state. Three renderings, per the founder's ruling that
 * "safe" is what never leaves the kernel sandbox, judged per call:
 *
 *   Auto-approve off                        → "Ask" (kernel state irrelevant —
 *                                               every "ask" tool always prompts)
 *   Auto-approve on,  kernel sandbox active  → "Auto"
 *   Auto-approve on,  no kernel sandbox      → "Auto → Ask", with a tooltip —
 *                                               nothing can be positively
 *                                               cleared without a sandbox to
 *                                               check a call against, so
 *                                               every "ask" tool still prompts.
 *
 * Reads the same `useResolvedAutoApprove` the composer's AutoApprovePicker
 * does, so the two can never disagree about the chat's current state.
 */
export function ChatModeBadge({ className }: { className?: string }) {
  const { resolved, kernelSandboxActive } = useResolvedAutoApprove()

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
      content="No active kernel sandbox on this platform, so nothing can be positively cleared — every tool set to “ask” still prompts every time."
    >
      <Badge variant="secondary" className={className} data-testid="chat-mode-badge">
        Auto → Ask
      </Badge>
    </Tooltip>
  )
}
