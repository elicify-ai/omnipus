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
    'hand_off',
    'return_to_default',
    'remember',
    'write_agent_metadata',
    'get_usage',
    'run_doctor',
  ])('%s is visible, non-verbose', (tool) => {
    expect(shouldRenderToolCall(tool, undefined, false)).toBe(true)
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

describe('shouldRenderToolCall — isError override still forces ToolSearch visibility', () => {
  it.each<[string, Record<string, unknown> | undefined]>([
    ['ToolSearch', undefined],
    ['ToolSearch', {}],
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

  it.each(['delegate', 'bash', 'read_file', 'mcp_some_tool'])(
    '%s is visible in the panel by default (non-verbose) — the panel is the transparency surface',
    (tool) => {
      expect(shouldRenderToolCallInPanel(tool, false)).toBe(true)
    },
  )
})
