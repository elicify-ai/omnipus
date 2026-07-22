// GoalEchoCard — ADR-053 FE-8 / US-3 / design §1 (D11).
//
// Renders the compiled-goal ECHO in the chat thread: when the engine compiles
// user intent into the goal definition + acceptance-criteria ladder (including
// literal machine-check commands), the agent echoes it back IN CHAT (no
// form/modal) and the user confirms by replying in normal chat. This card is
// that echo surface — it shows exactly what will be run, and prompts the user
// to reply to confirm or restate to amend.
//
// Per the design (§1, "Echo & confirm — in chat, no form (D11)"): "The refined
// goal — including the literal compiled command(s) — is echoed by the agent in
// the chat, and the user replies in the chat to confirm (no approval
// form/modal). The goal goes active only on that reply. The user sees exactly
// what will be run."
//
// Purely presentational — driven by props. The literal commands are the
// machine-check commands the compiler minted (shown verbatim so the user can
// vet them before confirming — they run under the goal-bearing agent's own
// tool policy, never a bypass).

import { Target, Terminal, ArrowBendUpRight } from '@phosphor-icons/react'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

export interface GoalEchoCardProps {
  /** The goal_status frame describing the compiled goal (condition + accounting). */
  frame: GoalStatusFrame
  /**
   * Literal compiled machine-check commands the compiler minted from user
   * intent — shown verbatim so the user sees exactly what will run. Empty when
   * the goal has no machine-check criteria (behaviour/prose only).
   */
  literalCommands?: string[]
}

export function GoalEchoCard({ frame, literalCommands = [] }: GoalEchoCardProps) {
  return (
    <div
      data-testid="goal-echo-card"
      className="my-2 rounded-lg border border-[var(--color-accent)]/30 bg-[var(--color-surface-1)] px-4 py-3 text-xs"
    >
      {/* Header — compiled-goal banner */}
      <div className="flex items-center gap-2 mb-2">
        <Target size={14} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
        <span className="font-medium text-[var(--color-secondary)] uppercase tracking-wide text-[10px]">
          Compiled goal — reply to confirm
        </span>
      </div>

      {/* Condition (the compiled goal definition) */}
      <p className="text-[var(--color-secondary)] break-words" data-testid="goal-echo-condition">
        {frame.condition}
      </p>

      {/* Round accounting */}
      <p className="text-[var(--color-muted)] mt-1.5 tabular-nums" data-testid="goal-echo-round">
        {frame.max_rounds} rounds · {frame.cap} concurrent loop{frame.cap === 1 ? '' : 's'}
      </p>

      {/* Literal commands — the machine checks the compiler authored */}
      {literalCommands.length > 0 && (
        <div className="mt-2.5" data-testid="goal-echo-commands">
          <div className="flex items-center gap-1.5 text-[var(--color-muted)] mb-1 text-[10px] uppercase tracking-wide">
            <Terminal size={11} aria-hidden="true" />
            Literal commands
          </div>
          <ul className="space-y-1">
            {literalCommands.map((cmd, i) => (
              <li key={i} className="flex items-start gap-1.5">
                <code className="font-mono text-[11px] text-[var(--color-accent)] bg-[var(--color-surface-2)] rounded px-1.5 py-0.5 break-all flex-1">
                  {cmd}
                </code>
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Confirmation prompt — conversational, no buttons */}
      <div className="mt-2.5 flex items-center gap-1.5 text-[var(--color-muted)] italic">
        <ArrowBendUpRight size={11} aria-hidden="true" />
        <span>Reply to confirm, or restate to amend.</span>
      </div>
    </div>
  )
}
