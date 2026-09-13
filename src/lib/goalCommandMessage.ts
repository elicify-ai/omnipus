// Recognising a `/goal <intent>` message in the persisted transcript.
//
// WHY THIS EXISTS (UAT defect B). A goal whose criteria never compiled left
// no goal-shaped trace after a reload. The reason is a persistence asymmetry
// on the SERVER, not a client filter:
//
//   - `pkg/agent/goal_loop.go::activateInstantGoal` (the ADR-081 D1 path
//     every prose `/goal` takes) activates the goal and emits a live
//     `goal_status` frame, but — unlike the marker-activation, marker-restate
//     and fallback-compile paths — it never calls
//     `anchorGoalRecordInTranscript`. So nothing goal-shaped is written to
//     the session JSONL.
//   - The goal card, the goal pill and the "Goal set…" ack line are therefore
//     all LIVE-FRAME-ONLY in that window. `chat.ts`'s own doc comment above
//     `GOAL_ACK_LINE_TEXT` documents this tradeoff and names the durable fix
//     as a backend change.
//
// What IS persisted is the user's own message. `pkg/gateway/websocket.go`
// appends the raw inbound text as a `role: "user"` transcript entry BEFORE
// the agent loop's command rewrite touches it, and `pkg/gateway/replay.go`'s
// `streamReplay` has no slash-command filter — so `/goal ship the release`
// replays intact. It just replays as an anonymous chat bubble,
// indistinguishable from ordinary text, which is why a probe looking for goal
// elements found none.
//
// This module reads that persisted text and nothing else. It reports what the
// user typed; it never claims a goal is active, never reconstructs a record,
// and never invents criteria — the live `goal_status` frame remains the only
// source for any of that.
//
// The parse mirrors the server's own, so the two agree on what counts as
// setting a goal:
//   - `pkg/commands/request.go::CommandArgs` splits on whitespace runs and
//     rejoins with single spaces, so `/goal   ship   it` carries the args
//     "ship it".
//   - `pkg/commands/request.go::normalizeCommandName` lowercases, so `/GOAL`
//     is the same command.
//   - `pkg/agent/goal_loop.go::isGoalClearVerb` treats the six
//     `commands.GoalClearAliases()` verbs as clearing a goal, not setting one.
//   - A bare `/goal` prints status, and `/goal confirm` is an ADR-081 no-op
//     reply — neither sets anything.

/** The six verbs that CLEAR a goal — `pkg/commands/cmd_goal.go::GoalClearAliases`. */
const GOAL_CLEAR_ALIASES = ['clear', 'stop', 'off', 'reset', 'cancel', 'none']

/** ADR-081 D1/FR-022 no-op arg — `pkg/agent/goal_loop.go::goalConfirmNoOpArg`. */
const GOAL_CONFIRM_NOOP_ARG = 'confirm'

/**
 * The goal intent a message SET, or null when the message did not set one.
 *
 * Returns null for anything that is not a goal-setting command: ordinary
 * chat, another slash command, a bare `/goal`, `/goal confirm`, and each of
 * the six clear verbs. Returns the whitespace-normalised intent otherwise —
 * the same string `CommandArgs` would hand the server.
 */
export function goalCommandStatement(content: string | null | undefined): string | null {
  if (!content) return null
  const fields = content.trim().split(/\s+/)
  if (fields.length < 2) return null // a bare `/goal` prints status; it sets nothing.
  if (fields[0].toLowerCase() !== '/goal') return null

  const args = fields.slice(1).join(' ')
  const normalized = args.toLowerCase()
  if (GOAL_CLEAR_ALIASES.includes(normalized)) return null
  if (normalized === GOAL_CONFIRM_NOOP_ARG) return null
  return args
}
