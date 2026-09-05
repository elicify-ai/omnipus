// toolVisibility.test.ts — coverage for the tool-call render filter
// (src/lib/toolVisibility.ts). Ground truth for `delegate`/`bash` defaults
// verified against pkg/tools/delegate.go and pkg/tools/shell.go on
// origin/hotfix/v0.1.1 — see the header comment in toolVisibility.ts.

import { describe, it, expect } from 'vitest'
import { shouldRenderToolCall, shouldRenderSubagentSpan, shouldRenderToolCallInPanel } from './toolVisibility'

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

describe('shouldRenderToolCall — delegate', () => {
  // Fix 2 (user-approved 2026-07-16, revised same day): a 'run' delegation
  // is hidden regardless of async — this INVERTS the pre-fix behavior for
  // { action: 'run', async: false } (explicit blocking/sync run), which used
  // to be visible. The thread's sole delegation surface is now the
  // SubagentBlock span card (gated by shouldRenderSubagentSpan below), not
  // this row.
  it.each<[Record<string, unknown> | undefined, boolean]>([
    [undefined, false], // defaults: action=run, async=true → hidden
    [{}, false],
    [{ async: true }, false], // explicit async=true, same as default → hidden
    [{ async: false }, false], // INVERTED: explicit await (blocking) is now ALSO hidden
    [{ action: 'status' }, false], // status polling → hidden
    [{ action: 'status', async: false }, false], // status wins over async
    [{ action: 'run', async: false }, false], // INVERTED: explicit run + await is now hidden too
    [{ action: 'kill' }, true], // any action other than run/status is unchanged — always visible
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

  it('a hidden background delegate call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('delegate', undefined, true)).toBe(true)
  })

  it('a hidden bash poll/read call becomes visible when verbose', () => {
    expect(shouldRenderToolCall('bash', { action: 'poll' }, true)).toBe(true)
    expect(shouldRenderToolCall('bash', { action: 'read' }, true)).toBe(true)
  })
})

// ── isError override — now a PER-TOOL-CLASS decision (revised 2026-07-16),
// not a blanket short-circuit. `ToolSearch` keeps the override (nothing else
// narrates its failure). `delegate` and the background-dispatch/poll/read
// sub-cases of `bash` deliberately do NOT: a subagent/background-shell
// failure is returned to the CALLING agent's own turn as the tool result —
// that agent explains it in its own response text — and the raw failure
// stays fully transparent in the ActivityPanel slide-out
// (shouldRenderToolCallInPanel shows everything but ToolSearch). Only verbose
// chat brings these specific rows back into the thread. ──────────────────

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

describe('shouldRenderToolCall — isError override does NOT apply to delegate (LLM-mediated failure presentation)', () => {
  // A failed/denied delegate 'run' or 'status' call stays hidden even on
  // error — only verboseChatEnabled reveals it. The delegating agent's own
  // response text is the place the failure gets explained; the panel is the
  // place the raw result stays inspectable.
  it.each<[Record<string, unknown> | undefined]>([
    [undefined], // default action=run, async=true
    [{}],
    [{ async: true }],
    [{ async: false }], // explicit blocking run — still no error exception
    [{ action: 'status' }],
  ])('params=%o stays hidden when isError=true (non-verbose)', (params) => {
    expect(shouldRenderToolCall('delegate', params, false, true)).toBe(false)
  })

  it('becomes visible on error only once verbose chat is enabled', () => {
    expect(shouldRenderToolCall('delegate', undefined, true, true)).toBe(true)
  })

  it('a delegate action unrelated to run/status (e.g. kill) is unaffected — already always visible', () => {
    expect(shouldRenderToolCall('delegate', { action: 'kill' }, false, true)).toBe(true)
    expect(shouldRenderToolCall('delegate', { action: 'kill' }, false, false)).toBe(true)
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
    ['delegate', undefined, false],
    ['delegate', { async: false }, false], // no longer forced visible (see describe block above)
    ['bash', { run_in_background: true }, false],
    ['bash', { action: 'kill' }, true],
    ['remember', undefined, true],
  ])('tool=%s params=%o → %s (unchanged from isError-omitted behavior)', (tool, params, expected) => {
    expect(shouldRenderToolCall(tool, params, false, false)).toBe(expected)
    // Same result whether the 4th arg is omitted or explicitly false.
    expect(shouldRenderToolCall(tool, params, false)).toBe(expected)
  })
})

// ── shouldRenderSubagentSpan — thread visibility for the SubagentBlock
// delegation card (Fix 2, user-approved 2026-07-16, revised same day):
// verbose-only, unconditionally. No failed-state exception (an earlier
// revision had one; removed — same LLM-mediated-failure-presentation
// rationale as the delegate/background-bash isError carve-outs above). ────

describe('shouldRenderSubagentSpan — verbose-only, no failed-state exception', () => {
  it.each<import('./toolStatusConfig').SpanLikeStatus>([
    'running',
    'success',
    'cancelled',
    'error',
    'timeout',
    'interrupted',
  ])('status=%s is hidden by default (verbose off), INCLUDING failure states', (status) => {
    expect(shouldRenderSubagentSpan({ status }, false)).toBe(false)
  })

  it.each<import('./toolStatusConfig').SpanLikeStatus>([
    'running',
    'success',
    'cancelled',
    'error',
    'timeout',
    'interrupted',
  ])('status=%s is visible once verbose chat is enabled', (status) => {
    expect(shouldRenderSubagentSpan({ status }, true)).toBe(true)
  })
})

// ── shouldRenderToolCallInPanel — ActivityPanel-only policy (Fix 2,
// user-approved 2026-07-16): INVERTED from the thread — show everything
// except ToolSearch by default; verbose reveals ToolSearch too. This is the
// transparency valve for exactly what the thread hides (including failed
// delegate/background-bash rows, by design). ──────────────────────────────

describe('shouldRenderToolCallInPanel', () => {
  it('hides ToolSearch by default', () => {
    expect(shouldRenderToolCallInPanel('ToolSearch', false)).toBe(false)
  })

  it('shows ToolSearch when verbose chat is enabled', () => {
    expect(shouldRenderToolCallInPanel('ToolSearch', true)).toBe(true)
  })

  // FR-015 back-compat: load_tool is the pre-ADR-071-D1 name for ToolSearch;
  // a pre-rename transcript's recorded calls must get the same panel
  // treatment as new ones, not fall through to the always-visible default.
  it('hides load_tool by default, same as ToolSearch', () => {
    expect(shouldRenderToolCallInPanel('load_tool', false)).toBe(false)
  })

  it('shows load_tool when verbose chat is enabled', () => {
    expect(shouldRenderToolCallInPanel('load_tool', true)).toBe(true)
  })

  it.each(['delegate', 'bash', 'read_file', 'mcp_some_tool'])(
    '%s is visible in the panel by default (non-verbose) — the panel is the transparency surface',
    (tool) => {
      expect(shouldRenderToolCallInPanel(tool, false)).toBe(true)
    },
  )
})

// ── ADR-072 / browser-agent-capability-spec FR-028 + FR-039 ──────────────
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

  // What this describe block does NOT cover, stated so the next reader does
  // not mistake its green for the whole of FR-028's chat half. This is the
  // BADGE-level gate only. Whether the badge is ever REACHED is decided one
  // level up by shouldRenderSubagentSpan for a delegated call — revision 3 of
  // the spec let the assertion above stand as the whole claim, which is a
  // green that could not have seen the failure. The next describe block is
  // the missing half; the two must be read together (S-43 + S-63).
  it('documents its own limit: this says nothing about a call inside a delegated span', () => {
    // Same tool, same default — but a delegated call lives inside a span,
    // and the span gate answers `false` before the badge gate is consulted.
    expect(shouldRenderToolCall('browser_snapshot', undefined, false)).toBe(true)
    expect(shouldRenderSubagentSpan({ status: 'success' }, false)).toBe(false)
  })
})

// ── FR-039 / S-63: the delegated population, which FR-028's chat half does
// NOT cover. Four assertions in four directions on purpose — a single "it is
// hidden" assertion goes green if the span mechanism is deleted outright, and
// a single "it is visible in the panel" assertion goes green while the
// external-CLI case shows nothing anywhere. The panel-side halves (3 and 4)
// live in src/components/chat/ActivityPanel.test.tsx, where the rendering
// they describe actually happens. ─────────────────────────────────────────

describe('shouldRenderSubagentSpan — a delegated browser call is verbose-only in the parent thread (FR-039, S-63)', () => {
  it('direction 1: at the default verboseChatEnabled=false the span does not render, so a delegated browser_snapshot renders nowhere in the parent thread', () => {
    expect(shouldRenderSubagentSpan({ status: 'success' }, false)).toBe(false)
  })

  it('direction 2: with verbose chat on the same span renders — the gap is the DEFAULT, not the absence of a path', () => {
    expect(shouldRenderSubagentSpan({ status: 'success' }, true)).toBe(true)
  })

  // The span gate is what decides the outcome, and it decides it identically
  // for every terminal status — including the failure states an operator is
  // most likely to assume are surfaced. Asserted across the status domain so
  // a future narrower exception (e.g. "show failed spans") cannot land here
  // silently while this file still reports green.
  it.each<import('./toolStatusConfig').SpanLikeStatus>([
    'running',
    'success',
    'cancelled',
    'error',
    'timeout',
    'interrupted',
  ])(
    'a span in status=%s hides its delegated browser call at the default, while the badge predicate for that same call says true',
    (status) => {
      expect(shouldRenderSubagentSpan({ status }, false)).toBe(false)
      // The badge-level answer is irrelevant when the span never renders —
      // this pair is the exact mismatch revision 3 of the spec shipped as a
      // passing mitigation.
      expect(shouldRenderToolCall('browser_snapshot', undefined, false)).toBe(true)
    },
  )

  // The panel is the only partial fallback, and only for the calls it admits.
  // Asserted here at the predicate; ActivityPanel.test.tsx asserts the
  // rendering, and asserts that an external-CLI span carries nothing.
  it('the panel predicate admits a delegated browser_snapshot — the partial fallback FR-039 names', () => {
    expect(shouldRenderToolCallInPanel('browser_snapshot', false)).toBe(true)
  })
})
