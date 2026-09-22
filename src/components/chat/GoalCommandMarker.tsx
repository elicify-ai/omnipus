import { Target } from '@phosphor-icons/react'

/**
 * A quiet marker rendered above a user message that SET a goal.
 *
 * UAT defect B: a goal whose criteria never compiled left nothing
 * goal-shaped in the thread after a reload — the card, the pill and the
 * "Goal set…" ack line are all driven by a live `goal_status` frame, and the
 * instant-activation path writes no transcript anchor for them (see
 * `src/lib/goalCommandMessage.ts` for the full chain). The user's own
 * `/goal …` message DOES persist and replay; it simply replayed as an
 * anonymous bubble.
 *
 * This marker is deliberately the WEAKEST claim the persisted data supports:
 * "this message set a goal". It does not say the goal is active, met,
 * cleared or compiled — none of which survive in the transcript — so it
 * cannot go stale or contradict the live pill when one is present.
 *
 * It takes no props, and deliberately does NOT repeat the goal's intent:
 * the message bubble it sits directly above already renders the user's
 * `/goal ship the release` text verbatim, so printing the intent here too
 * would put the same sentence on screen twice, one line apart — the very
 * defect just fixed one component over in GoalEchoCard (UAT defect C). The
 * marker's whole job is to make the bubble below it recognisable as a goal;
 * the bubble says what the goal was. `messageSetsGoal` therefore answers
 * yes/no rather than returning an intent string nothing renders.
 */
export function GoalCommandMarker() {
  return (
    <span
      data-testid="goal-command-marker"
      title="This message set a goal"
      className="inline-flex items-center gap-[var(--space-1)] font-mono text-[length:var(--type-caption-size)] uppercase tracking-widest text-[var(--color-muted)]"
    >
      <Target size={10} weight="fill" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
      Goal
    </span>
  )
}
