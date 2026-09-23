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
 * chat-header mode badge, so the two can never disagree) — which already
 * folds in a pending pre-session choice (see below), so this component never
 * needs to compute that fallback itself. Toggling always records an explicit
 * true/false for this chat — this is the ONE scope in the whole contract
 * allowed to LOOSEN past the agent/global default, because a human is
 * present in this session to accept that.
 *
 * A brand-new chat has no server-known session yet — `session_mode_update`
 * needs a session id the server already knows about, which does not exist
 * until the first message is sent. Rather than disabling the switch until
 * then (which left the founder unable to even try Auto-approve in a fresh
 * chat), a toggle flipped here records a PENDING choice
 * (`ChatStore.pendingAutoApproveChoice`) that: (1) is reflected immediately
 * via `useResolvedAutoApprove` — flipping the switch never runs anything by
 * itself, it only records the choice — and (2) is flushed as a real
 * `session_mode_update` the moment the session is minted — see the
 * `session_started` case in `src/store/chat/slices/frames.ts`, which sends
 * it as the very next frame after the ack, before doing anything else.
 *
 * Founder ruling (2026-09-24): this must reach the server before the first
 * turn's first tool call is decided, not merely "soon after". Confirmed
 * against the backend (`pkg/gateway/websocket_chat.go::handleChatMessage`,
 * `pkg/agent/auto_approve_gate.go`, `pkg/gateway/ws_session_mode.go`):
 * `session_started` is sent back to the client the instant the session is
 * minted, while the turn itself is only handed to the agent-loop goroutine
 * via a buffered channel (`pkg/bus/bus.go::PublishInbound`) — the LLM call
 * that has to complete before any tool call exists to approve happens on
 * that separate goroutine. The auto-approve gate reads the per-session mode
 * fresh at each tool-call dispatch (`SessionModeStore.Get`), not a value
 * snapshotted at turn start, and `session_mode_update`'s handler writes into
 * that same live store on the WS read loop, processing the client's very
 * next frame after `session_started`. So sending immediately on the ack —
 * this component's whole approach — reliably beats the LLM round-trip that
 * gates the first tool call, without any backend change.
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
 * Disabled only when there is truly no active agent to run tools at all —
 * never because the chat has no session yet, and never while streaming.
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
  const { resolved, hasRealSession } = useResolvedAutoApprove()

  const isDisabled = disabled || !activeAgentId

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
  const tooltipContent = hasRealSession
    ? 'Auto-approve for this chat — applies from the next step'
    : 'Auto-approve for this chat — applies from your first message'

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
