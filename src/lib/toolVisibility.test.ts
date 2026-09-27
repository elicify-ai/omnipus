// toolVisibility.test.ts — coverage for the tool-call render filter
// (src/lib/toolVisibility.ts). Ground truth for `delegate`/`bash` defaults
// verified against pkg/tools/delegate.go and pkg/tools/shell.go on
// origin/hotfix/v0.1.1 — see the header comment in toolVisibility.ts.

import { describe, it, expect } from 'vitest'
import { shouldRenderToolCall } from './toolVisibility'

describe('shouldRenderToolCall — ToolSearch', () => {
  it.each([
    [undefined, false, false],
    [{}, false, false],
    [undefined, true, true],
    [{}, true, true],
  ])('params=%o verbose=%s → %s', (params, verbose, expected) => {
    expect(shouldRenderToolCall('ToolSearch', params as Record<string, unknown> | undefined, verbose)).toBe(
      expected,
    )
  })
})

// FR-015 back-compat: `load_tool` is the pre-ADR-071-D1 name for the same
// tool. A pre-rename session transcript still contains the literal string
// "load_tool" (pkg/gateway/replay.go emits the recorded name verbatim, and
// old transcripts are never migrated) — it must be classified identically to
// `ToolSearch`, not fall through to `default: return true`.
describe('shouldRenderToolCall — load_tool (legacy pre-rename name, same treatment as ToolSearch)', () => {
  it.each([
    [undefined, false, false],
    [{}, false, false],
    [undefined, true, true],
    [{}, true, true],
  ])('params=%o verbose=%s → %s', (params, verbose, expected) => {
    expect(shouldRenderToolCall('load_tool', params as Record<string, unknown> | undefined, verbose)).toBe(
      expected,
    )
  })

  it('an error/failure outcome still forces visibility, same as ToolSearch', () => {
    expect(shouldRenderToolCall('load_tool', undefined, false, true)).toBe(true)
  })
})

// ADR-072 D3: `Skill` mirrors `ToolSearch` exactly — hidden on success,
// forced visible on error/denial/not-found. Spec rows 31/32
// (TestToolVisibility_SkillHiddenOnSuccessShownOnError,
// TestToolVisibility_VerboseChatRevealsSkill).
describe('shouldRenderToolCall — Skill (ADR-072 D3)', () => {
  it.each([
    [undefined, false, false],
    [{}, false, false],
    [{ slug: 'release-notes' }, false, false],
    [undefined, true, true],
    [{}, true, true],
  ])('params=%o verbose=%s → %s', (params, verbose, expected) => {
    expect(shouldRenderToolCall('Skill', params as Record<string, unknown> | undefined, verbose)).toBe(
      expected,
    )
  })

  it('a refused/denied load (error outcome) is forced visible, same as ToolSearch', () => {
    expect(shouldRenderToolCall('Skill', { slug: 'release-notes' }, false, true)).toBe(true)
  })

  it('a not-found load (error outcome) is forced visible', () => {
    expect(shouldRenderToolCall('Skill', { slug: 'no-such-skill' }, false, true)).toBe(true)
  })

  it('a successful load stays hidden even with isError explicitly false', () => {
    expect(shouldRenderToolCall('Skill', { slug: 'release-notes' }, false, false)).toBe(false)
  })

  it('verbose chat reveals a Skill call regardless of outcome', () => {
    expect(shouldRenderToolCall('Skill', undefined, true, false)).toBe(true)
    expect(shouldRenderToolCall('Skill', undefined, true, true)).toBe(true)
  })
})

// ADR-088 D5/A-3 (work-first goal flow), re-anchored by ADR-082 D9: the RAW
// `set_goal` call chip is hidden by default — the record card (GoalEchoCard,
// rendered directly from the call's own result by its dedicated tool UI,
// SetGoalToolUI) is the visible surface for a reader, same rationale as
// `delegate`'s hide. Unlike `ToolSearch`/`Skill`, there is NO error
// exception (mirrors `delegate`/background-`bash`): a rejected submission
// is a bounded-retry validation loop the calling agent handles inline.
describe('shouldRenderToolCall — set_goal (ADR-088 D5/A-3)', () => {
  it.each([
    [undefined, false, false],
    [{}, false, false],
    [{ mode: 'register' }, false, false],
    [{ mode: 'update' }, false, false],
    [undefined, true, true],
    [{}, true, true],
  ])('params=%o verbose=%s → %s', (params, verbose, expected) => {
    expect(shouldRenderToolCall('set_goal', params as Record<string, unknown> | undefined, verbose)).toBe(
      expected,
    )
  })

  it('stays hidden on a validation-error/rejected submission (non-verbose) — no isError exception', () => {
    expect(shouldRenderToolCall('set_goal', { mode: 'register' }, false, true)).toBe(false)
  })

  it('becomes visible on error only once verbose chat is enabled', () => {
    expect(shouldRenderToolCall('set_goal', { mode: 'register' }, true, true)).toBe(true)
  })
})

describe('shouldRenderToolCall — delegate', () => {
  // Spec D2: every delegate action is a line, not a badge, unless verbose
  // chat is on (that short-circuit is tested below). `isError` does not
  // bring a badge back.
  it.each<[Record<string, unknown> | undefined, boolean]>([
    [undefined, false],
    [{}, false],
    [{ async: true }, false],
    [{ async: false }, false],
    [{ action: 'status' }, false],
    [{ action: 'status', async: false }, false],
    [{ action: 'run', async: false }, false],
    [{ action: 'kill' }, false],
  ])('params=%o → %s', (params, expected) => {
    expect(shouldRenderToolCall('delegate', params, false)).toBe(expected)
  })
})

describe('shouldRenderToolCall — bash', () => {
  it.each<[Record<string, unknown> | undefined, boolean]>([
    [undefined, true], // defaults: action=run, run_in_background=false → visible
    [{}, true],
    [{ run_in_background: true }, false], // background run → hidden
    [{ run_in_background: false }, true], // explicit foreground → visible
    [{ action: 'poll' }, false],
    [{ action: 'read' }, false],
    [{ action: 'kill' }, true], // regression guard: kill must NOT be lumped with poll/read
    [{ action: 'kill', run_in_background: true }, true], // kill wins even against a background session
  ])('params=%o → %s', (params, expected) => {
    expect(shouldRenderToolCall('bash', params, false)).toBe(expected)
  })
})

describe('shouldRenderToolCall — always-visible tools', () => {
  it.each([
    'switch_agent',
    'remember',
    'get_usage',
    'run_doctor',
  ])('%s is visible, non-verbose', (tool) => {
    expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
  })
})

// ADR-071 D4: `hand_off` and `return_to_default` are merged into
// `switch_agent`. Asserted explicitly (not left to the `default:`
// fallthrough, which happens to fail open into the same `true`) per
// ADR-071 §5.2.2c.
describe('shouldRenderToolCall — switch_agent (ADR-071 D4 merge of hand_off + return_to_default)', () => {
  it('is visible, non-verbose, regardless of target', () => {
    expect(shouldRenderToolCall('switch_agent', { target: 'jim', note: 'handing off' }, false)).toBe(true)
    expect(shouldRenderToolCall('switch_agent', { target: 'default' }, false)).toBe(true)
    expect(shouldRenderToolCall('switch_agent', undefined, false)).toBe(true)
  })

  it('is visible when verbose', () => {
    expect(shouldRenderToolCall('switch_agent', { target: 'jim' }, true)).toBe(true)
  })

  it('is visible on error too — no isError exception needed (neither predecessor had one)', () => {
    expect(shouldRenderToolCall('switch_agent', { target: 'jim' }, false, true)).toBe(true)
  })
})

describe('shouldRenderToolCall — under-review set (not hidden by this change)', () => {
  it.each(['recall_memory', 'read_agent_metadata'])('%s is visible, non-verbose', (tool) => {
    expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
  })
})

describe('shouldRenderToolCall — mcp_* and unknown tools (default fallthrough, no wildcard match)', () => {
  it.each(['mcp_github_create_issue', 'mcp_some_other_tool', 'some_future_unknown_tool'])(
    '%s is visible, non-verbose',
    (tool) => {
      expect(shouldRenderToolCall(tool, { anything: 'goes' }, false)).toBe(true)
    },
  )
})

describe('shouldRenderToolCall — verbose override', () => {
  it('ToolSearch becomes visible when verbose', () => {
    expect(shouldRenderToolCall('ToolSearch', undefined, true)).toBe(true)
  })

  it('a hidden background bash call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('bash', { run_in_background: true }, true)).toBe(true)
  })

  it('a delegate run badge stays hidden without verbose (the event line replaces it)', () => {
    expect(shouldRenderToolCall('delegate', undefined, false)).toBe(false)
  })

  it('a hidden delegate status-poll call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('delegate', { action: 'status' }, true)).toBe(true)
  })

  it('a hidden set_goal call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('set_goal', undefined, true)).toBe(true)
  })

  it('a hidden bash poll/read call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('bash', { action: 'poll' }, true)).toBe(true)
    expect(shouldRenderToolCall('bash', { action: 'read' }, true)).toBe(true)
  })
})

// ── isError override — now a PER-TOOL-CLASS decision (revised 2026-07-16),
// not a blanket short-circuit. `ToolSearch` keeps the override (nothing else
// narrates its failure). The background-dispatch/poll/read sub-cases of
// `bash` deliberately do NOT: a background-shell failure is returned to the
// CALLING agent's own turn as the tool result — that agent explains it in
// its own response text. Only verbose chat brings these specific rows back
// into the thread. `delegate` (below) doesn't consult isError at all any
// more — its visibility is param-based only (ADR-091 D7/AC-7: the `run`
// line is already visible by default; only `status` stays hidden, and that
// hiding does not depend on outcome either). ──────────────────

describe('shouldRenderToolCall — isError override still forces ToolSearch/Skill visibility', () => {
  it.each<[string, Record<string, unknown> | undefined]>([
    ['ToolSearch', undefined],
    ['ToolSearch', {}],
    ['Skill', undefined],
    ['Skill', {}],
    ['Skill', { slug: 'release-notes' }],
  ])('tool=%s params=%o is visible when isError=true', (tool, params) => {
    expect(shouldRenderToolCall(tool, params, false, true)).toBe(true)
  })
})

describe('shouldRenderToolCall — delegate ignores isError entirely', () => {
  // An error does not bring a non-verbose delegate badge back. Verbose
  // chat's short-circuit (checked first) is what reveals every action.
  it.each<[Record<string, unknown> | undefined, boolean]>([
    [undefined, false],
    [{}, false],
    [{ async: true }, false],
    [{ async: false }, false],
    [{ action: 'status' }, false],
    [{ action: 'kill' }, false],
  ])('params=%o isError=true → %s', (params, expected) => {
    expect(shouldRenderToolCall('delegate', params, false, true)).toBe(expected)
  })

  it('a hidden status poll becomes visible on error only once verbose chat is enabled', () => {
    expect(shouldRenderToolCall('delegate', { action: 'status' }, true, true)).toBe(true)
  })
})

describe('shouldRenderToolCall — isError override does NOT apply to background bash (poll/read/dispatch) — same LLM-mediated rationale as delegate', () => {
  it.each<[Record<string, unknown> | undefined]>([
    [{ run_in_background: true }], // background dispatch
    [{ action: 'poll' }],
    [{ action: 'read' }],
  ])('params=%o stays hidden when isError=true (non-verbose)', (params) => {
    expect(shouldRenderToolCall('bash', params, false, true)).toBe(false)
  })

  it('becomes visible on error only once verbose chat is enabled', () => {
    expect(shouldRenderToolCall('bash', { run_in_background: true }, true, true)).toBe(true)
  })

  it('a foreground bash run is unaffected by isError — already always visible', () => {
    expect(shouldRenderToolCall('bash', { command: 'ls' }, false, true)).toBe(true)
    expect(shouldRenderToolCall('bash', { command: 'ls' }, false, false)).toBe(true)
  })

  it('bash kill is unaffected by isError — already always visible, even against a background session', () => {
    expect(shouldRenderToolCall('bash', { action: 'kill', run_in_background: true }, false, true)).toBe(true)
  })
})

describe('shouldRenderToolCall — isError=false explicit is a no-op (regression guard)', () => {
  // Confirms passing isError=false explicitly reproduces the exact same
  // classifications as omitting the parameter entirely.
  it.each<[string, Record<string, unknown> | undefined, boolean]>([
    ['ToolSearch', undefined, false],
    ['Skill', undefined, false],
    ['delegate', undefined, false], // run badge hidden; the event line is the surface
    ['delegate', { action: 'status' }, false], // status stays hidden
    ['bash', { run_in_background: true }, false],
    ['bash', { action: 'kill' }, true],
    ['remember', undefined, true],
  ])('tool=%s params=%o → %s (unchanged from isError-omitted behavior)', (tool, params, expected) => {
    expect(shouldRenderToolCall(tool, params, false, false)).toBe(expected)
    // Same result whether the 4th arg is omitted or explicitly false.
    expect(shouldRenderToolCall(tool, params, false)).toBe(expected)
  })
})

// ADR-091 D7/D10: `shouldRenderSubagentSpan` and the SubagentBlock thread
// card it gated are deleted — a child's own frames never arrive in the
// parent's bucket any more (I-4), so there is no span-level content left to
// render in the thread at any verbosity. The delegate tool-call line
// (shouldRenderToolCall's 'delegate' case, above) is the thread's only
// remaining delegation surface, and (D7/AC-7) is now visible by default for
// the `run` action rather than deferring to a deleted span surface.
//
// `shouldRenderToolCallInPanel` and ToolCallBadge's `surface="panel"` prop
// are ALSO deleted here: the ActivityPanel step list they gated (nested
// native-agent step rows) is gone along with SubagentBlock — ActivityPanel.tsx
// no longer renders ToolCallBadge at all (it has its own flat ActivityRow),
// and MessageItem.tsx (ToolCallBadge's one remaining production caller)
// never passes `surface`, so it was zero-caller dead code. See
// ToolCallBadge.tsx/.test.tsx for the corresponding deletion.

// ── ADR-075 / browser-agent-capability-spec FR-028 + FR-039 ──────────────
//
// The D2 spec adds six browser tools — browser_select_option,
// browser_press_key, browser_hover, browser_upload_file,
// browser_handle_dialog, browser_snapshot. FR-028's chat half and §11(b)
// reason (iii) (the argument for seeding browser_handle_dialog `allow`) both
// rest on those calls rendering in the chat thread at the default
// verboseChatEnabled=false. §5 makes "must not add any browser tool to
// toolVisibility.ts's hidden set" a hard non-behaviour.
//
// These are TESTS ONLY — no SPA source changes here or anywhere in this unit.

describe('shouldRenderToolCall — all six new browser tools render in their own turn thread (FR-028, S-43)', () => {
  const SIX_NEW_BROWSER_TOOLS = [
    'browser_select_option',
    'browser_press_key',
    'browser_hover',
    'browser_upload_file',
    'browser_handle_dialog',
    'browser_snapshot',
  ] as const

  // THE ORACLE. Behavioural, calling the real exported function — deliberately
  // NOT "the file contains no 'browser' substring". A substring-absence check
  // goes green if hiding is later introduced through a different mechanism: a
  // new predicate, a name list imported from another module, a category rule,
  // a prefix match added above the switch. Only calling the function can see
  // that (round-2 M6).
  it.each(SIX_NEW_BROWSER_TOOLS)(
    '%s renders at the default verboseChatEnabled=false',
    (tool) => {
      expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
    },
  )

  // Params must not open a back door. shouldRenderToolCall's delegate/bash
  // cases branch on `action`, so a browser tool called with an `action`
  // argument must still fall through to the always-visible default rather
  // than colliding with those branches.
  it.each(SIX_NEW_BROWSER_TOOLS)(
    '%s renders regardless of its call arguments',
    (tool) => {
      expect(shouldRenderToolCall(tool, { action: 'run' }, false)).toBe(true)
      expect(shouldRenderToolCall(tool, { action: 'status' }, false)).toBe(true)
      expect(shouldRenderToolCall(tool, { run_in_background: true }, false)).toBe(true)
    },
  )

  // A failed/denied browser call must not be hidden either. The
  // ToolSearch/Skill cases key off isError to FORCE visibility; nothing
  // should ever use it to suppress one of these.
  it.each(SIX_NEW_BROWSER_TOOLS)(
    '%s renders on an error/denial outcome too',
    (tool) => {
      expect(shouldRenderToolCall(tool, undefined, false, true)).toBe(true)
    },
  )

  // §11(b) reason (iii) specifically: an operator can see `accept:true` in the
  // thread. That argument now rests on this assertion rather than on an
  // unpinned property (round-2 m3).
  it('browser_handle_dialog{accept:true} renders, which is what §11(b) reason (iii) leans on', () => {
    expect(shouldRenderToolCall('browser_handle_dialog', { accept: true }, false)).toBe(true)
  })

  // SECONDARY HINT ONLY — never the oracle. Kept because it localises a
  // regression to this one file when it does fire, but it is not evidence on
  // its own: it cannot see hiding introduced anywhere else, and it goes green
  // for a file that has been emptied.
  it('secondary hint: the eleven shipped browser tools are unaffected by the same default', () => {
    for (const tool of ['browser_navigate', 'browser_click', 'browser_type', 'browser_get_text']) {
      expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
    }
  })

  // ADR-091 D7/D10: this describe block used to document a gap against a
  // span-level gate (`shouldRenderSubagentSpan`) that decided whether a
  // delegated call's badge was ever reached. That gate — and the span-level
  // thread rendering it gated — is deleted: a child's own frames never
  // arrive in the parent's bucket any more, so there is no span content in
  // the thread to reach at any verbosity. The badge-level answer above is
  // therefore now the whole answer for the parent thread; a call inside a
  // CHILD's own session is reached by opening that session directly (the
  // side panel's Open control), not through any thread-side gate here.
})

// Delegation chat surface (docs/internal/specs/delegation-chat-surface-spec.md
// D2, AC-9, AC-5 render half). The grey event lines replace delegate badges
// in the normal thread. Verbose chat is unchanged: the function's first
// branch still returns true for every tool, delegate included.
describe('shouldRenderToolCall — delegation lines replace delegate badges (D2)', () => {
  const DELEGATE_ACTIONS = [
    'run',
    'status',
    'peek',
    'inbox',
    'inbox_ack',
    'steer',
    'respond',
    'cancel',
    'follow_up',
  ] as const

  it.each(DELEGATE_ACTIONS)(
    'non-verbose: delegate action %s renders no badge (the line is the surface)',
    (action) => {
      expect(shouldRenderToolCall('delegate', { action }, false)).toBe(false)
      expect(shouldRenderToolCall('delegate', { action }, false, true)).toBe(false)
    },
  )

  it('non-verbose: a delegate call with no action (the run default) renders no badge', () => {
    expect(shouldRenderToolCall('delegate', undefined, false)).toBe(false)
    expect(shouldRenderToolCall('delegate', {}, false)).toBe(false)
  })

  it.each(DELEGATE_ACTIONS)(
    'verbose: delegate action %s still renders a badge (AC-9)',
    (action) => {
      expect(shouldRenderToolCall('delegate', { action }, true)).toBe(true)
      expect(shouldRenderToolCall('delegate', { action }, true, true)).toBe(true)
    },
  )

  it('verbose: a delegate call with no action still renders a badge (AC-9)', () => {
    expect(shouldRenderToolCall('delegate', undefined, true)).toBe(true)
  })
})
