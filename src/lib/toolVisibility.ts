// Client-side render filter, governing TWO chat surfaces via THREE exported
// policies:
//   - The chat THREAD (transcript) — shouldRenderToolCall (tool-call chips)
//     and shouldRenderSubagentSpan (SubagentBlock delegation cards).
//   - The ActivityPanel slide-out — shouldRenderToolCallInPanel (expanded
//     native-agent step rows), whose default INVERTS the thread's.
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

import type { SpanLikeStatus } from './toolStatusConfig'

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
 *     failure in, and the panel hides ToolSearch by default too
 *     (shouldRenderToolCallInPanel below) — so without this exception the
 *     failure would be invisible everywhere except verbose chat.
 *   - `delegate` and the background-dispatch/poll/read sub-cases of `bash`:
 *     NO error exception. A failed/denied delegation or background shell
 *     command is returned to the CALLING agent's own turn as the tool
 *     result — that agent decides how (and whether) to explain the failure
 *     in its own response text. The ActivityPanel slide-out is the durable
 *     fallback for THIS case, but its coverage is narrower than "fully
 *     transparent": it only ever shows subagent SPANS (running, plus a
 *     recently-finished list capped at 8 — RECENTLY_FINISHED_CAP in
 *     useRunningActivity.ts) and background bash SESSIONS, and (Fix 1,
 *     2026-07-16) stays reachable at idle only while a failure is still
 *     retained in that capped list (ActivityBar.tsx). A delegation DENIED
 *     outright at dispatch time never opens a span, so it never reaches the
 *     panel either — verbose chat (which reveals this row directly,
 *     DelegationFailureDisplay included) is the only render surface for
 *     that specific case; absent verbose chat, the calling agent's own
 *     narration is the only place the denial surfaces. A delegation that
 *     DOES dispatch (a span opens) has its own nested step-level failures
 *     shown in the panel via shouldRenderToolCallInPanel below (shows
 *     everything except ToolSearch). Only verbose chat brings any of these
 *     rows back into the thread itself.
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
      // this function's doc comment) — unlike delegate/background-bash
      // below, there is no calling-agent turn that narrates a ToolSearch
      // failure on its behalf.
      // `load_tool` is the pre-ADR-071-D1 name for this same tool — kept
      // here (mirroring humanizeToolName.ts's EXPLICIT_LABELS entry, FR-015)
      // solely so a conversation transcript recorded before the rename still
      // gets the same hide-by-default/error-forces-visible treatment instead
      // of falling through to `default: return true`.
      return isError

    case 'delegate': {
      // action defaults to "run" (pkg/tools/delegate.go execute()).
      const action = paramString(params, 'action') ?? 'run'
      if (action === 'status') {
        // Polling a previously-delegated task's status — noisy, and wins
        // over async since delegate.go dispatches on action first.
        return false
      }
      if (action === 'run') {
        // Fix 2 (user-approved, revised 2026-07-16): a 'run' delegation is
        // hidden from the thread by default for BOTH sync and async, AND
        // regardless of outcome (`isError` is deliberately not consulted
        // here — see this function's doc comment for the LLM-mediated
        // rationale). The thread's delegation surface is now the span card
        // alone (gated separately by shouldRenderSubagentSpan, below), and
        // that card carries the same no-error-exception, verbose-only rule.
        return false
      }
      // Any other action (e.g. 'kill') is unchanged — a deliberate,
      // standalone-meaningful action, always visible.
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
 * Decide whether a delegation card (SubagentBlock) renders inline in the
 * chat THREAD (Fix 2, user-approved 2026-07-16; revised same day). Delegation
 * is hidden from the thread by default, UNCONDITIONALLY — including a failed/
 * errored/timed-out/interrupted span. There is deliberately no failed-state
 * exception here (an earlier revision of this policy had one — removed):
 * a subagent's error is returned to the DELEGATING agent's own turn as the
 * tool result, and that agent decides how to present or react to it in its
 * own response text, so the thread doesn't need its own separate failure
 * card for the same information. The raw span (including its failure) stays
 * fully transparent in the ActivityPanel slide-out — see
 * shouldRenderToolCallInPanel below, whose own rendering is NOT gated by
 * this function at all. The thread card reappears only via verbose chat.
 *
 * Kept as its own named policy function (rather than inlined at each call
 * site as a bare `verboseChatEnabled` check) so this rationale — and the
 * span-status domain it deliberately does NOT branch on — stays discoverable
 * in one place for the next person who wonders "why is a failed delegation
 * invisible in the thread?".
 *
 * @param span Any object carrying the span's status — deliberately typed
 *   structurally (not as the full `SubagentSpan` union from `@/store/chat`)
 *   so this lib module never has to import the store. Unused now that the
 *   failed-state exception is gone, but kept in the signature so call sites
 *   read naturally ("should this span render") and so a future revision of
 *   this policy (e.g. reinstating a narrower exception) doesn't need to
 *   touch every call site's argument list.
 * @param verboseChatEnabled When true, every span renders. This is now the
 *   ONLY condition under which a span renders in the thread.
 */
export function shouldRenderSubagentSpan(
  _span: { status: SpanLikeStatus },
  verboseChatEnabled: boolean,
): boolean {
  return verboseChatEnabled
}

/**
 * Panel-only visibility policy for ToolCallBadge rows expanded inside the
 * ActivityPanel slide-out (Fix 2, user-approved 2026-07-16 — inverted from
 * the thread's policy). The panel is the designated home for exactly the
 * background/noisy detail the thread (shouldRenderToolCall) hides — a user
 * who opened the panel already asked to see what's happening, so default
 * to showing everything EXCEPT `ToolSearch` (renamed from `load_tool`,
 * ADR-071 D1 — pure internal tool-definition-loading infra — "Find & load
 * tools" humanized — with no standalone meaning even in a detail view).
 * `load_tool` (the pre-rename name) is excluded alongside it for the same
 * FR-015 back-compat reason as the thread-side case above — an old
 * transcript's pre-rename calls get the same panel treatment as new ones.
 * Verbose chat reveals both there too. Deliberately name-based only, no
 * params: unlike shouldRenderToolCall this isn't classifying dispatch
 * shape/outcome, only excluding two always-noisy tool names (one current,
 * one legacy alias for the same tool).
 */
export function shouldRenderToolCallInPanel(
  tool: string,
  verboseChatEnabled: boolean,
): boolean {
  return verboseChatEnabled || (tool !== 'ToolSearch' && tool !== 'load_tool')
}

/**
 * Decide whether a `Message.type === 'judge_verdict'` transcript entry
 * renders as a standalone card in the chat THREAD (ADR-049 SD-C10). Mirrors
 * `shouldRenderSubagentSpan` exactly: judge calls are out-of-turn internal
 * LLM actions with no standalone meaning to a reader — same class as
 * `delegate`/background-`bash` — so they follow the same hide-by-default,
 * verbose-only rule rather than a bespoke policy. The verdict is still fully
 * persisted (transcript entry) and fully transparent via the ActivityPanel
 * (panel visibility is NOT gated by this function at all — see
 * `useRunningActivity`/`ActivityPanel`, which always render a judge row
 * regardless of verbose chat).
 */
export function shouldRenderJudgeVerdictInThread(verboseChatEnabled: boolean): boolean {
  return verboseChatEnabled
}
