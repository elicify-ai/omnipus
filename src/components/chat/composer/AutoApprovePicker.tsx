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
 * chat-header mode badge, ChatModeBadge) — which already folds in a pending
 * pre-session choice (see below), so this component never needs to compute
 * that fallback itself. Sharing the hook is not enough on its own to agree
 * with ChatModeBadge, though: `godModeActive` is a stronger floor than
 * `resolved` and must be checked explicitly (see below) — see the hook's own
 * doc comment. Toggling always records an explicit
 * true/false for this chat — this is the ONE scope in the whole contract
 * allowed to LOOSEN past the agent/global default, because a human is
 * present in this session to accept that.
 *
 * A brand-new chat has no server-known session yet — `session_mode_update`
 * needs a session id the server already knows about, which does not exist
 * until the first message is sent. Rather than disabling the switch until
 * then (which left the founder unable to even try Auto-approve in a fresh
 * chat), a toggle flipped here records a PENDING choice
 * (`ChatStore.pendingAutoApproveChoice`) that is reflected immediately via
 * `useResolvedAutoApprove` — flipping the switch never runs anything by
 * itself, it only records the choice.
 *
 * Founder ruling (2026-09-24): the choice must take effect at the chat's
 * first activity — every tool call of the first turn, including the very
 * first one — not merely "soon after". A round trip that waits for the
 * server's `session_started` ack and only THEN sends `session_mode_update`
 * cannot guarantee that: the turn is dispatched to the agent loop
 * (`pkg/bus/bus.go::PublishInbound`) on a separate goroutine the instant the
 * message is admitted, so a slow client or a fast first LLM call can let the
 * first tool call be decided before that follow-up frame ever arrives. Fixed
 * by carrying the choice ON the minting message itself:
 * `sendMessage`'s no-active-session branch
 * (`src/store/chat/slices/outbound-lifecycle.ts`) sends
 * `pendingAutoApproveChoice` as `MessageFrame.auto_approve` on the very frame
 * that mints the session, and the server writes it into `SessionModeStore`
 * (`pkg/gateway/websocket_chat.go::recordSessionAndTranscript`) BEFORE that
 * same handler publishes the turn to the bus — so the mode is already live
 * before the agent loop's goroutine can even start. `frames.ts`'s
 * `session_started` case then reflects the same value straight into this
 * session's bucket (no WS round trip needed — the server already has it) and
 * clears `pendingAutoApproveChoice` so it is never reused for a later,
 * unrelated new chat.
 *
 * In a chat that ALREADY has a real session, flipping applies from the very
 * next tool call, not merely "the next message" — a running turn can flip
 * mid-stream. `handleToggle` calls `sendSessionModeUpdate` unconditionally
 * (no `isStreaming` gate here or in `sendSessionModeUpdate` itself,
 * `src/store/chat/slices/outbound-responses.ts`), and the composer never
 * disables this switch while streaming (`ChatScreen.tsx` passes only
 * `disabled={agentRemoved}`) — so the frame is never queued or held back
 * mid-turn. Founder ruling (2026-09-24), confirmed backend-side: the gate
 * reads the per-chat mode store fresh on every tool-call dispatch
 * (`pkg/agent/auto_approve_gate.go::autoApproveActive`), not once per turn,
 * so a flip mid-turn changes the very next tool call, while a call already
 * showing an approval prompt is not retroactively resolved — it has already
 * asked, and stays open until the human answers it.
 *
 * Disabled when there is truly no active agent to run tools at all — never
 * because the chat has no session yet, and never while streaming — AND
 * disabled under God Mode (see below), which is the one other case.
 *
 * God Mode is a STRONGER floor than Auto (`useResolvedAutoApprove`'s own
 * doc), not the absence of it — ChatModeBadge already checks
 * `godModeActive` first and renders "God Mode" whatever `resolved` says.
 * This switch must agree: under God Mode every tool call runs without
 * asking regardless of this chat's own Auto choice, so the switch shows
 * on-and-disabled (flipping it would change nothing real) with a tooltip
 * saying so, rather than showing `resolved` (possibly off) next to a badge
 * that says "God Mode" — the exact disagreement this component used to
 * allow by never reading `godModeActive` at all.
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
  const setPendingAutoApproveChoice = useChatStore((s) => s.setPendingAutoApproveChoice)
  const { resolved, hasRealSession, kernelSandboxActive, godModeActive } = useResolvedAutoApprove()

  const isDisabled = disabled || !activeAgentId || godModeActive

  function handleToggle(next: boolean) {
    if (hasRealSession && activeSessionId) {
      sendSessionModeUpdate(activeSessionId, next)
      return
    }
    // No real session yet (brand-new chat) — record the choice locally; the
    // frame slice's `session_started` handler sends it the moment a real
    // session id exists.
    setPendingAutoApproveChoice(next)
  }

  // Design-system rule 14: a recurring "explain this control" job goes
  // through the catalogued Tooltip, never a native title= — a title
  // tooltip is also mouse-only (never shown on keyboard focus at all).
  //
  // Founder-specified wording (2026-09-24), split by whether this chat has
  // a real session yet: a brand-new chat's choice applies from the first
  // message (there is no "next step" yet to speak of); a running chat's
  // flip applies from the very next tool call, not the next typed message —
  // "next step" says that without implying the user has to type anything.
  const baseTooltipContent = hasRealSession
    ? 'Auto-approve for this chat — applies from the next step'
    : 'Auto-approve for this chat — applies from your first message'

  // Founder decision (2026-09-24): Auto works without an enforcing kernel
  // sandbox, but it's still worth a calm caution — appended to the same
  // tooltip rather than a second visual element, matching ChatModeBadge's
  // "Auto — no sandbox" wording so the two never disagree.
  const autoTooltipContent = resolved && !kernelSandboxActive
    ? `${baseTooltipContent}. No kernel sandbox — shell commands ask first unless read-only or operator-allowed.`
    : baseTooltipContent

  // God Mode is a stronger floor than this chat's own Auto choice — it runs
  // every tool without asking regardless of what `resolved` says, so the
  // switch's own explanation must say that, not the ordinary Auto copy above.
  const tooltipContent = godModeActive
    ? 'God Mode is on — every tool runs without asking. This chat’s Auto-approve choice does not apply.'
    : autoTooltipContent

  return (
    <Tooltip
      content={tooltipContent}
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
          checked={godModeActive ? true : resolved}
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
