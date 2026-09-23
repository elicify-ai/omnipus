import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { Tooltip } from '@/components/ui/tooltip'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useResolvedAutoApprove } from '@/hooks/useResolvedAutoApprove'
import { cn } from '@/lib/utils'

/**
 * AutoApprovePicker — the composer's per-chat Auto-approve quick switch
 * (ADR-092). A human-only action: nothing agent-facing can reach
 * `sendSessionModeUpdate` — it is a session-scoped WS send this component is
 * the only caller of.
 *
 * Reads its checked state from `useResolvedAutoApprove` (shared with the
 * chat-header mode badge, so the two can never disagree). Toggling always
 * sends an explicit true/false for this chat — this is the ONE scope in the
 * whole contract allowed to LOOSEN past the agent/global default, because a
 * human is present in this session to accept that.
 *
 * Disabled with no real session yet (a session_mode_update needs a
 * session_id server-side already knows about) or no active agent.
 */
export function AutoApprovePicker({
  className,
  disabled = false,
}: {
  className?: string
  disabled?: boolean
}) {
  const activeAgentId = useSessionStore((s) => s.activeAgentId)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const sendSessionModeUpdate = useChatStore((s) => s.sendSessionModeUpdate)
  const { resolved, hasRealSession } = useResolvedAutoApprove()

  const isDisabled = disabled || !hasRealSession || !activeAgentId

  function handleToggle(next: boolean) {
    if (!hasRealSession || !activeSessionId) return
    sendSessionModeUpdate(activeSessionId, next)
  }

  // Design-system rule 14: a recurring "explain this control" job goes
  // through the catalogued Tooltip, never a native title= — a title
  // tooltip is also mouse-only (never shown on keyboard focus at all),
  // which mattered most for the DISABLED reason: a disabled Switch is
  // pulled out of the tab order, so without Tooltip's own focusable
  // trigger a keyboard user had no way to discover why it's disabled.
  return (
    <Tooltip
      content={
        hasRealSession
          ? 'Auto-approve for this chat — turns on or off for this conversation only'
          : 'Send a message first to enable per-chat Auto-approve'
      }
      data-testid="composer-auto-approve-tooltip-trigger"
    >
      <Label
        className={cn(
          'flex items-center gap-[var(--space-1)] shrink-0 text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]',
          isDisabled ? 'opacity-50' : 'cursor-pointer',
          className,
        )}
      >
        <Switch
          checked={resolved}
          disabled={isDisabled}
          onCheckedChange={handleToggle}
          aria-label="Auto-approve for this chat"
          data-testid="composer-auto-approve-toggle"
        />
        <span className="hidden @2xl:inline">Auto</span>
      </Label>
    </Tooltip>
  )
}
