/**
 * Delegation event lines — shared interface (docs/internal/specs/delegation-chat-surface-spec.md).
 *
 * One muted chat line per EVENT, never per tool call. Events are derived from the
 * CHILD's lifecycle transitions (and background-command lifecycle), not from the
 * parent's polling calls: `status`, `peek`, `inbox`, `inbox_ack` produce nothing.
 *
 * Producer: the event-derivation module (`useDelegationEvents`). It must return the
 * SAME events for a live session and for the same session rebuilt from history
 * (reload / catch-up snapshot) — founder decision 2026-09-25.
 * Consumer: the chat thread renders one line per event.
 *
 * D1: no field here may carry child-authored text. `title` is the task label the
 * PARENT gave; `reason` is a gateway/tool refusal reason; `command` is the shell
 * command the parent launched. Never the child's output.
 */

export type DelegationEventKind =
  /** `delegate run` dispatched and the child started (or was queued — see `queued`). */
  | 'delegated'
  /** A previously queued child left the queue and started running. */
  | 'started'
  /** The child reached a terminal success state. Exactly ONE per child. */
  | 'finished'
  /** The child ended without finishing (failed, interrupted, cancelled by itself). */
  | 'stopped'
  /** `delegate steer` — the parent sent the child a new instruction. */
  | 'steered'
  /** `delegate respond` — the parent answered the child's question. */
  | 'answered'
  /** `delegate cancel` — the parent stopped the child (optionally cascading). */
  | 'cancelled'
  /** `delegate follow_up` — the parent gave the child follow-up work. */
  | 'follow_up'
  /** A delegation was refused (depth, concurrency cap, unknown skill, …). No child. */
  | 'refused'
  /** A background shell command was launched. */
  | 'bash_launched'
  /** A background shell command finished successfully. */
  | 'bash_finished'
  /** A background shell command failed (non-zero exit, error, or timeout). */
  | 'bash_failed'
  /** A background shell command was stopped on purpose (killed / cancelled by the agent). */
  | 'bash_stopped'

export interface DelegationEvent {
  /**
   * Stable, deterministic identity — the dedupe key. Identical for the live event
   * and the same event rebuilt from history. E.g. `finished:<childSessionId>`,
   * `steered:<toolCallId>`, `bash_failed:<bashId>`.
   */
  id: string
  kind: DelegationEventKind
  /** The parent (chat) session the line belongs to. */
  sessionId: string
  /**
   * The chat message this line is placed after (the assistant message of the turn
   * in which the event happened). Undefined ⇒ append at the end of the thread.
   */
  anchorMessageId?: string
  /** Epoch ms of the event; orders lines that share an anchor. */
  at: number
  /** Display name of the child agent (subagent kinds only). */
  agentName?: string
  /** Task label the parent gave the delegation (never child output). */
  title?: string
  /** Child session id. Present ⇒ the line shows `[open]` (D4). Absent for refusals and bash. */
  childSessionId?: string
  /** For `refused`: the refusal reason shown to the user. */
  reason?: string
  /** For bash kinds: the command line as launched by the parent. */
  command?: string
  /** For `bash_failed`: the exit code. */
  exitCode?: number
}

/**
 * Producer contract: returns the ordered event list for one chat session.
 * Implemented by the event-derivation lane in `src/hooks/useDelegationEvents.ts`.
 */
export type UseDelegationEvents = (sessionId: string | null | undefined) => DelegationEvent[]
