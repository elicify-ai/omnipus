// Recognising a goal-setting command (`/goal <intent>`) in the persisted
// transcript.
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
// setting a goal. `pkg/commands/request.go::parseCommandName` is the oracle
// for the command word:
//   - `commandPrefixes = []string{"/", "!"}` — BOTH prefixes reach the same
//     handler, so `!goal ship it` sets a real goal server-side. A parser
//     that only knew `/` marked none of those messages, which is the exact
//     defect this module exists to fix, one prefix over.
//   - the `@…` suffix (Telegram's `/goal@bot`) is cut before the name is
//     compared, so `/goal@omnipus ship it` is the `goal` command too.
//   - `normalizeCommandName` lowercases, so `/GOAL` is the same command.
// And for the argument text:
//   - `pkg/commands/request.go::CommandArgs` splits on whitespace runs and
//     rejoins with single spaces, so `/goal   ship   it` carries the args
//     "ship it" — whitespace runs are immaterial to every comparison below.
//   - `pkg/agent/goal_loop.go::isGoalClearVerb` treats the six
//     `commands.GoalClearAliases()` verbs — compared against the WHOLE args
//     string — as clearing a goal, not setting one.
//   - A bare `/goal` prints status, and `/goal confirm` is an ADR-081 no-op
//     reply — neither sets anything.
//
// WHAT IT RETURNS, AND WHY IT IS A BOOLEAN. An earlier version returned the
// whitespace-normalised intent string. Nothing could use it: the only
// consumer is the quiet marker above the user's own message bubble — and
// that bubble already renders the raw `/goal ship the release` text
// verbatim. Printing the intent in the marker too would put the same
// sentence on screen twice, one line apart, which is precisely the defect
// that was just fixed in GoalEchoCard (UAT defect C). So the question this
// module answers is the one its caller actually asks — "did this message set
// a goal?" — and the answer is yes or no.
//
// COST. This runs once per user row per render, on every render of a
// virtualized transcript, for messages that are overwhelmingly NOT commands.
// So the first thing it does is the cheapest test that can settle the common
// case: does the text begin (after any leading whitespace) with a command
// prefix character? That test allocates nothing and reads at most a few
// bytes, so a pasted 100 KB log costs a few bytes per render instead of an
// array of every one of its tokens.

/** The six verbs that CLEAR a goal — `pkg/commands/cmd_goal.go::GoalClearAliases`. */
const GOAL_CLEAR_ALIASES = ['clear', 'stop', 'off', 'reset', 'cancel', 'none']

/** ADR-081 D1/FR-022 no-op arg — `pkg/agent/goal_loop.go::goalConfirmNoOpArg`. */
const GOAL_CONFIRM_NOOP_ARG = 'confirm'

/** The command word, `pkg/commands/cmd_goal.go`'s `Name: "goal"` — no aliases. */
const GOAL_COMMAND_NAME = 'goal'

/**
 * The cheap gate: leading whitespace, then one of `pkg/commands/request.go`'s
 * `commandPrefixes`. `.test()` captures nothing and stops at the first
 * non-whitespace character, so ordinary chat — including a huge paste — exits
 * here having allocated nothing.
 */
const COMMAND_PREFIX = /^\s*[/!]/

/** Leading whitespace, the command token, and the whitespace after it. */
const COMMAND_TOKEN = /^\s*(\S+)\s*/

/**
 * The first ARGUMENT token, plus one character of whatever follows it —
 * sticky, so it reads from the offset the command token ended at rather than
 * scanning (or copying) the rest of the message. The second group is empty
 * exactly when the args are a SINGLE token, which is the only case the clear
 * verbs and `confirm` can match: `isGoalClearVerb` compares the whole args
 * string, and no alias contains a space.
 */
const ARG_TOKEN = /(\S+)\s*(\S?)/y

/**
 * Whether this message SET a goal — true only for what the server would treat
 * as a goal-setting command.
 *
 * False for everything else: ordinary chat, another command, a bare `/goal`
 * (which prints status), `/goal confirm`, and each of the six clear verbs.
 */
export function messageSetsGoal(content: string | null | undefined): boolean {
  if (!content) return false
  // Common case first — see "COST" above.
  if (!COMMAND_PREFIX.test(content)) return false

  const head = COMMAND_TOKEN.exec(content)
  if (!head) return false

  // The command word, mirroring `parseCommandName`: drop the prefix, cut at
  // an `@…` suffix, lowercase, compare.
  let name = head[1].slice(1)
  const at = name.indexOf('@')
  if (at >= 0) name = name.slice(0, at)
  if (name.toLowerCase() !== GOAL_COMMAND_NAME) return false

  // The arguments. `lastIndex` is set immediately before every use and the
  // function has no await, so the shared sticky regex cannot be re-entered
  // mid-parse.
  ARG_TOKEN.lastIndex = head[0].length
  const args = ARG_TOKEN.exec(content)
  if (!args) return false // a bare `/goal` prints status; it sets nothing.
  if (args[2] !== '') return true // two or more words — no alias can match.

  const onlyArg = args[1].toLowerCase()
  if (GOAL_CLEAR_ALIASES.includes(onlyArg)) return false
  if (onlyArg === GOAL_CONFIRM_NOOP_ARG) return false
  return true
}
