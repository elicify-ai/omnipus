// Client-side render filter for the chat THREAD (transcript) —
// shouldRenderToolCall (tool-call chips) and shouldRenderJudgeVerdictInThread
// (judge-verdict cards). ADR-091 D7/D10 deleted `shouldRenderSubagentSpan`
// (the SubagentBlock delegation-card gate) along with SubagentBlock itself —
// a child's own frames never arrive in the parent's bucket any more, so
// there is no span content left in the thread at any verbosity. The
// `delegate` tool-call line (shouldRenderToolCall's own case, below) is
// therefore the thread's ONLY delegation surface, and ADR-091 D7/AC-7
// requires it to carry that job alone: the parent's chat must show exactly
// the one line a delegation produces, so a `run` delegation (the default
// action) is visible in the normal, non-verbose thread — not deferred to a
// span/step surface that no longer exists.
//
// The ActivityPanel slide-out's own step-level policy
// (`shouldRenderToolCallInPanel`, ToolCallBadge's `surface="panel"` prop) is
// ALSO deleted: ActivityPanel.tsx no longer renders ToolCallBadge at all —
// its rows are a flat ActivityRow (status line + open control), not
// expanded native-agent step rows — so that policy had zero production
// callers left. See ToolCallBadge.tsx for the corresponding simplification.
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
 * This classifier looks at the tool's name, its call-time arguments, and
 * (for SOME tools only) its outcome. Outcome is NOT a blanket override
 * anymore (revised 2026-07-16, user-approved): whether an error/denial
 * forces visibility is now a per-tool-class decision, not a single
 * short-circuit above the switch —
 *   - `ToolSearch` (renamed from `load_tool`, ADR-071 D1): an error still
 *     forces visibility. It has no calling agent's own turn to explain the
 *     failure in, and no other render surface exists for it — so without
 *     this exception the failure would be invisible everywhere except
 *     verbose chat.
 *   - `Skill` (ADR-072 D3): mirrors `ToolSearch` exactly — hidden on success,
 *     forced visible on error. A refused/denied or not-found skill load is a
 *     real, security-relevant outcome the reader needs to see; a successful
 *     load is the silent "check the menu" habit D3 deliberately does not
 *     narrate (see the ADR's D3 §3 and D3.1's audit-vs-render distinction —
 *     the call is still audited and still in the transcript either way, this
 *     is render-only).
 *   - `delegate` (ADR-091 D7/AC-7): visibility is param-based only —
 *     `isError` is never consulted. The `run` action (the default) is
 *     visible unconditionally: it is the parent's chat surface for a
 *     delegation, full stop, now that the span/step surfaces this case used
 *     to defer to (SubagentBlock's delegation card, `shouldRenderSubagentSpan`)
 *     are deleted — a child's own frames never arrive in the parent's bucket
 *     any more, so there is nothing left for a span to show. Only `status`
 *     (polling a previously-delegated task) stays hidden, unconditionally —
 *     pure noise with no standalone meaning to a reader, on any outcome.
 *   - The background-dispatch/poll/read sub-cases of `bash`: NO error
 *     exception. A failed background shell command is returned to the
 *     CALLING agent's own turn as the tool result — that agent decides how
 *     (and whether) to explain the failure in its own response text. Only
 *     verbose chat brings one of these rows back into the thread.
 *   - Every other case ignores `isError` entirely (they're either always
 *     visible or, for `bash`'s foreground/`kill` cases, don't depend on it).
 *
 * @param tool The tool name as it appears on the wire (e.g. "bash", "delegate", "mcp_github_create_issue").
 * @param params The tool call's arguments, if any.
 * @param verboseChatEnabled When true, every tool call renders — this function short-circuits to `true`.
 * @param isError When true, the call's outcome was an error/denial/failure. Only honored by the
 *   `ToolSearch` case (see above) — every other case decides visibility from tool+params alone.
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

  switch (tool) {
    // --- Hidden by default: noisy infra with no standalone chat meaning ---

    case 'ToolSearch':
    case 'load_tool':
      // Every call is infrastructure (loading a tool's full definition into
      // context) — never a meaningful standalone action to a chat reader.
      // Exception: an error/failure outcome still forces visibility (see
      // this function's doc comment) — unlike background-bash below, there
      // is no calling-agent turn that narrates a ToolSearch failure on its
      // behalf.
      // `load_tool` is the pre-ADR-071-D1 name for this same tool — kept
      // here (mirroring humanizeToolName.ts's EXPLICIT_LABELS entry, FR-015)
      // solely so a conversation transcript recorded before the rename still
      // gets the same hide-by-default/error-forces-visible treatment instead
      // of falling through to `default: return true`.
      return isError

    case 'Skill':
      // ADR-072 D3: mirrors ToolSearch exactly. A successful `Skill` call is
      // the silent "check the menu" habit D3 deliberately does not narrate —
      // hidden by default. A refused/denied or not-found load is a real,
      // security-relevant outcome (D3.1: every call is audited regardless of
      // render visibility) the reader needs to see, so an error/failure
      // outcome still forces visibility, same as ToolSearch.
      return isError

    case 'set_goal':
      // ADR-088 D5/A-3 (work-first goal flow), re-anchored by ADR-082 D9:
      // `set_goal` is the working agent's write-path for the goal record
      // (register/update). The record-rendering surface for a reader is the
      // typed record card (GoalEchoCard), rendered directly from THIS
      // call's own result by its dedicated tool UI (SetGoalToolUI, live;
      // the parts-loop `set_goal` branch, replay) — never the raw tool
      // call, same rationale as `delegate`'s hide (a dedicated, purpose-
      // built surface already exists, so the call chip adds no
      // reader-facing meaning). This `false` governs only the RAW call
      // chip's own visibility (GenericToolCall/the Fallback, which a
      // registered dedicated tool UI bypasses entirely) — it does not hide
      // the card itself. No error exception HERE: unlike ToolSearch/Skill,
      // a failed/rejected `set_goal` submission does not bring the RAW call
      // chip back — but it is not invisible either (ADR-082 D9 review S4):
      // the dedicated UI renders a one-line quiet "Goal registration
      // failed" trace (detail on expand) for a failed call when verbose
      // chat is off, and falls through to GenericToolCall — this `true`
      // branch above — when it is on. See SetGoalToolUI.tsx's
      // classifySetGoalCall, the single decision table both the renderer
      // and ChatScreen's wouldToolCallBeVisible consult.
      return false

    case 'delegate': {
      // action defaults to "run" (pkg/tools/delegate.go execute()).
      const action = paramString(params, 'action') ?? 'run'
      if (action === 'status') {
        // Polling a previously-delegated task's status — noisy, and wins
        // over async since delegate.go dispatches on action first. Hidden
        // unconditionally (isError is not consulted): a poll's own outcome
        // has no standalone meaning to a reader either way.
        return false
      }
      // ADR-091 D7/AC-7: `run` (sync or async, the default) and every other
      // action (e.g. 'kill') are visible — unconditionally, `isError`
      // included. The span/step surfaces this case used to defer to
      // (SubagentBlock's delegation card, `shouldRenderSubagentSpan`) are
      // deleted: a child's own frames never arrive in the parent's bucket
      // any more, so there is nothing left for a span to render. This line
      // — the delegate tool call itself — is therefore now the parent's
      // ONLY delegation surface in the thread, at any verbosity, and AC-7
      // requires it to show by default rather than only when verbose chat
      // is on.
      return true
    }

    case 'bash': {
      // action defaults to "run" (pkg/tools/shell.go execute()).
      const action = paramString(params, 'action') ?? 'run'
      if (action === 'poll' || action === 'read') {
        // Checking on / reading from an already-running background session
        // — noisy polling, not a new action. No error exception (same
        // LLM-mediated rationale as delegate above) — verbose only.
        return false
      }
      if (action === 'kill') {
        // A deliberate operator action (terminating a session) — always
        // visible, even though it targets a background session. Must NOT be
        // lumped in with poll/read.
        return true
      }
      // run_in_background defaults to false (getBoolArg() zero value) —
      // bash is foreground-by-default. The background-dispatch branch below
      // has no error exception either, for the same reason as poll/read; a
      // foreground run doesn't need one since it's already always visible.
      const runInBackground = paramBool(params, 'run_in_background') ?? false
      return !(action === 'run' && runInBackground)
    }

    // --- Always visible: deliberate, standalone-meaningful actions ---
    // Named explicitly (rather than left to fall through) so this switch
    // stays a readable, exhaustive table — a future reader can see at a
    // glance these were a deliberate inclusion, not an oversight.

    case 'switch_agent':
      // ADR-071 D4: `hand_off` and `return_to_default` are merged into one
      // tool, `switch_agent(target, note?)`. Both predecessors were always
      // visible (no isError exception, no params-based branching) in this
      // classifier, so the merge needs no reconciliation here — it is a
      // straight rename of one always-visible case, asserted explicitly
      // (rather than left to the `default:` fallthrough, which happens to
      // fail open into the same `true`) per ADR-071 §5.2.2c.
      return true
    case 'remember':
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

/**
 * Decide whether a `Message.type === 'judge_verdict'` transcript entry
 * renders as a standalone card in the chat THREAD (ADR-049 SD-C10). Judge
 * calls are out-of-turn internal LLM actions with no standalone meaning to a
 * reader — same class as background-`bash` — so they follow the same
 * hide-by-default, verbose-only rule rather than a bespoke policy (this is
 * unlike `delegate`, ADR-091 D7/AC-7: a delegation IS a standalone,
 * reader-meaningful action, which is why that case is visible by default —
 * a judge verdict is not). The verdict is still fully persisted (transcript
 * entry) and fully transparent via the ActivityPanel (panel visibility is
 * NOT gated by this function at all — see `useRunningActivity`/
 * `ActivityPanel`, which always render a judge row regardless of verbose
 * chat).
 */
export function shouldRenderJudgeVerdictInThread(verboseChatEnabled: boolean): boolean {
  return verboseChatEnabled
}
