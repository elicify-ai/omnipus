// Client-side render filter for tool-call chips in the chat transcript.
//
// This is a PURE UI decision — it never touches the persisted session
// transcript (JSONL on disk keeps every tool call untouched). It only
// decides whether a given tool call renders inline in the live/finalized
// chat view when "verbose chat" is off. Some tool calls are noisy
// background infra with no standalone meaning to someone reading the
// conversation (a background delegate kicking off, a background shell
// session being polled) — those stay hidden by default; anything a human
// would recognize as a deliberate, meaningful action stays visible.
//
// Ground truth for the two multi-action tools (`delegate`, `bash`) was
// verified directly against the current tool registry on
// origin/hotfix/v0.1.1:
//   - pkg/tools/delegate.go — DelegateTool.execute() defaults `action` to
//     "run" when absent (`action == "" { action = "run" }`), and
//     executeRun() defaults `async` to `true` when the field is absent
//     (`async := true` before the presence check) — i.e. delegate is
//     background-by-default. `action: "status"` is dispatched before the
//     async branch even applies, so it wins regardless of `async`.
//   - pkg/tools/shell.go — ExecTool.execute() defaults `action` to "run"
//     when absent, identically to delegate. `run_in_background` is read via
//     getBoolArg(), whose zero value (missing/non-bool) is `false` — i.e.
//     bash is foreground-by-default.

/** Narrow an unknown params bag to a string field, honoring only real strings. */
function paramString(params: Record<string, unknown> | undefined, key: string): string | undefined {
  const value = params?.[key]
  return typeof value === 'string' ? value : undefined
}

/** Narrow an unknown params bag to a boolean field, honoring only real booleans. */
function paramBool(params: Record<string, unknown> | undefined, key: string): boolean | undefined {
  const value = params?.[key]
  return typeof value === 'boolean' ? value : undefined
}

/**
 * Decide whether a tool call should render inline in the chat transcript.
 *
 * This classifier looks ONLY at the tool's name and its call-time arguments —
 * it has no idea whether the call ultimately succeeded, failed, or was
 * denied. That is deliberate: outcome is a completely orthogonal axis, and
 * callers that know the outcome pass it via `isError` rather than this
 * function trying to infer it from params. An error/denial/failure must
 * always be visible, no matter how noisy the tool+params combination would
 * otherwise be classified — a policy-denied delegation or a failed
 * background command is never "just infra".
 *
 * @param tool The tool name as it appears on the wire (e.g. "bash", "delegate", "mcp_github_create_issue").
 * @param params The tool call's arguments, if any.
 * @param verboseChatEnabled When true, every tool call renders — this function short-circuits to `true`.
 * @param isError When true, the call's outcome was an error/denial/failure — this function
 *   short-circuits to `true` regardless of what the tool+params classification below would say.
 *   Defaults to `false` so existing call sites that haven't been updated with an outcome signal
 *   keep their current (name/params-only) behavior unchanged.
 */
export function shouldRenderToolCall(
  tool: string,
  params: Record<string, unknown> | undefined,
  verboseChatEnabled: boolean,
  isError: boolean = false,
): boolean {
  // Verbose mode shows everything, unconditionally — check first so nothing
  // below needs to reason about it.
  if (verboseChatEnabled) {
    return true
  }

  // An error/denial/failure outcome always renders, regardless of what the
  // tool+params classification below would otherwise decide. This must run
  // BEFORE the switch — the switch only knows how to reason about the
  // "normal" background-infra case, not about outcomes.
  if (isError) {
    return true
  }

  switch (tool) {
    // --- Hidden by default: noisy infra with no standalone chat meaning ---

    case 'load_tool':
      // Every call is infrastructure (loading a tool's full definition into
      // context) — never a meaningful standalone action to a chat reader.
      return false

    case 'delegate': {
      // action defaults to "run" (pkg/tools/delegate.go execute()).
      const action = paramString(params, 'action') ?? 'run'
      if (action === 'status') {
        // Polling a previously-delegated task's status — noisy, and wins
        // over async since delegate.go dispatches on action first.
        return false
      }
      // async defaults to true — background delegation (executeRun()).
      const async = paramBool(params, 'async') ?? true
      // Hidden only for the default background run (action=run, async=true).
      // An explicit async=false (await/blocking) is a deliberate, visible action.
      return !(action === 'run' && async)
    }

    case 'bash': {
      // action defaults to "run" (pkg/tools/shell.go execute()).
      const action = paramString(params, 'action') ?? 'run'
      if (action === 'poll' || action === 'read') {
        // Checking on / reading from an already-running background session
        // — noisy polling, not a new action.
        return false
      }
      if (action === 'kill') {
        // A deliberate operator action (terminating a session) — always
        // visible, even though it targets a background session. Must NOT be
        // lumped in with poll/read.
        return true
      }
      // run_in_background defaults to false (getBoolArg() zero value) —
      // bash is foreground-by-default.
      const runInBackground = paramBool(params, 'run_in_background') ?? false
      // Hidden only for the default foreground-vs-background split when
      // kicking off a background run; a foreground run is always visible.
      return !(action === 'run' && runInBackground)
    }

    // --- Always visible: deliberate, standalone-meaningful actions ---
    // Named explicitly (rather than left to fall through) so this switch
    // stays a readable, exhaustive table — a future reader can see at a
    // glance these were a deliberate inclusion, not an oversight.

    case 'hand_off':
    case 'return_to_default':
    case 'remember':
    case 'write_agent_metadata':
    case 'get_usage':
    case 'run_doctor':
      return true

    // Deliberately left visible — these surface memory/metadata context to
    // the user; hiding them is a candidate for a future, separately-decided
    // pass, not decided here.
    case 'recall_memory':
    case 'read_agent_metadata':
      return true

    default:
      // Covers every mcp_* real external MCP tool call and any
      // unrecognized/future tool name. Deliberate: the hide-list above is a
      // closed set of exact literal names, never a wildcard/prefix match —
      // an unknown tool is always shown rather than silently swallowed.
      return true
  }
}
